package diag

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// lookup is an answer to one question for the A records of a name.
type lookup struct {
	Name    string
	Server  netip.AddrPort
	Rcode   string
	Answers []string
	Addrs   []netip.Addr
	RTT     time.Duration
}

// query asks server for the A records of name over UDP, and over TCP when the answer is truncated. Any answer is a
// lookup, whatever its response code; only a server that does not answer is an error.
func (r *Runner) query(ctx context.Context, server netip.AddrPort, name string, timeout time.Duration) (*lookup, error) {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), dns.TypeA)
	q.RecursionDesired = true
	t0 := r.clk.Monotonic()
	resp, err := r.exchange(ctx, "udp", server, q, timeout)
	if err == nil && resp.Truncated {
		resp, err = r.exchange(ctx, "tcp", server, q, timeout-(r.clk.Monotonic()-t0))
	}
	if err != nil {
		return nil, err
	}
	lk := &lookup{Name: name, Server: server, Rcode: dns.RcodeToString[resp.Rcode], RTT: r.clk.Monotonic() - t0}
	if lk.Rcode == "" {
		lk.Rcode = fmt.Sprintf("RCODE%d", resp.Rcode)
	}
	for _, rr := range resp.Answer {
		switch v := rr.(type) {
		case *dns.A:
			if a, ok := netip.AddrFromSlice(v.A.To4()); ok {
				lk.Addrs = append(lk.Addrs, a)
				lk.Answers = append(lk.Answers, a.String())
			}
		case *dns.CNAME:
			lk.Answers = append(lk.Answers, "CNAME "+strings.TrimSuffix(v.Target, "."))
		}
	}
	return lk, nil
}

func (r *Runner) exchange(ctx context.Context, network string, server netip.AddrPort, q *dns.Msg, timeout time.Duration) (*dns.Msg, error) {
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := r.net.DialContext(ctx, network, server.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	dl, _ := ctx.Deadline()
	_ = conn.SetDeadline(dl)
	// a canceled request must not wait for the deadline of the socket
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	dc := &dns.Conn{Conn: conn}
	if err := dc.WriteMsg(q); err != nil {
		return nil, err
	}
	resp, err := dc.ReadMsg()
	if err != nil {
		return nil, err
	}
	if resp.Id != q.Id {
		return nil, errors.New("the answer does not belong to the question")
	}
	return resp, nil
}

// errNoServer is the error of a name that cannot be resolved because no server is configured.
var errNoServer = errors.New("no DNS server is configured")

// resolve resolves name with the configured servers: the next one is asked only when the one before cannot be
// reached at all.
func (r *Runner) resolve(ctx context.Context, name string, timeout time.Duration) (*lookup, error) {
	var servers []netip.AddrPort
	if r.cfg.Resolvers != nil {
		servers = r.cfg.Resolvers()
	}
	if len(servers) == 0 {
		return nil, errNoServer
	}
	var err error
	for _, s := range servers {
		var lk *lookup
		if lk, err = r.query(ctx, s, name, timeout); err == nil {
			return lk, nil
		}
		if !unreachable(err) {
			break
		}
	}
	return nil, err
}

// resolveHost turns the host of a request into the address to talk to. An address is itself. For a name the lookup is
// returned as well, and a name that does not resolve is an error in a sentence.
func (r *Runner) resolveHost(ctx context.Context, host string, timeout time.Duration) (netip.Addr, *lookup, error) {
	if a, ok := literal(host); ok {
		return a, nil, nil
	}
	lk, err := r.resolve(ctx, host, timeout)
	if err != nil {
		return netip.Addr{}, nil, fmt.Errorf("cannot resolve %s: %s", host, failure(err, timeout))
	}
	if lk.Rcode != "NOERROR" {
		return netip.Addr{}, lk, fmt.Errorf("cannot resolve %s: %s", host, lk.Rcode)
	}
	if len(lk.Addrs) == 0 {
		return netip.Addr{}, lk, fmt.Errorf("cannot resolve %s: the name has no A record", host)
	}
	return lk.Addrs[0], lk, nil
}

// dnsLookup is the diagnostic `dns`: with `dns_name` the host is the server that is asked, without it the host is the
// name and the gateway's DNS proxy is asked.
func (r *Runner) dnsLookup(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	name, server := req.Host, r.cfg.ProxyServer
	if req.DnsName != nil {
		name = *req.DnsName
		addr, _, err := r.resolveHost(ctx, req.Host, timeout)
		if err != nil {
			res.Error = ptr(err.Error())
			return
		}
		if req.Host != addr.String() {
			res.Address = ptr(addr.String())
		}
		server = netip.AddrPortFrom(addr, uint16(port(req, 53)))
	} else if !server.IsValid() {
		res.Error = ptr("the address of the gateway's DNS proxy is not known")
		return
	}
	d := newOf(res.Dns)
	res.Dns = d
	d.Name, d.Server = ptr(name), ptr(server.String())
	lk, err := r.query(ctx, server, name, timeout)
	if err != nil {
		res.Error = ptr(failure(err, timeout))
		if isTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
			res.Error = ptr(fmt.Sprintf("%s did not answer within %s", server, timeout))
		}
		return
	}
	d.Rcode, d.RttMs = ptr(lk.Rcode), ptr(ms(lk.RTT))
	d.Answers = ptr(append([]string{}, lk.Answers...))
	switch {
	case lk.Rcode != "NOERROR":
		res.Error = ptr(lk.Rcode)
	case len(lk.Addrs) == 0:
		res.Error = ptr("NOERROR, but no A record in the answer")
	default:
		res.Ok = true
	}
}

// newOf returns a new zero value of the type p points at (the result's parts are anonymous structs).
func newOf[T any](*T) *T { return new(T) }

// dial connects a socket of the stack to addr and port.
func (r *Runner) dial(ctx context.Context, network string, addr netip.Addr, port int) (net.Conn, error) {
	return r.net.DialContext(ctx, network, netip.AddrPortFrom(addr, uint16(port)).String())
}
