package dnsproxy

import (
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the DNS faults of plan §2.6, M20. The configuration holds the resolved faults, in the order
// the precedence rules of §2.4 try them for each source (compiler.DNSFault): a query is answered by the first
// entry whose sources contain the client and whose names match the name asked (or that names none). The
// proxy keeps no state of its own and never decides precedence.
//
//	nxdomain      the name does not exist (NXDOMAIN), the upstream is not asked
//	servfail      SERVFAIL, the upstream is not asked
//	timeout       no answer at all: the client waits and gives up (over TCP the connection is left open until
//	              its read timeout)
//	delay         the answer comes `delay` later than it would have
//	wrong_answer  an A query gets the configured addresses instead (the upstream is not asked); AAAA
//	              gets no data, as with every name in the V1 networks; other types are answered as usual
//	truncate      over UDP the answer is empty with the TC bit, so the client asks again over TCP, where it
//	              gets the real answer
//	short_ttl     the real answer with the TTL of every record replaced by the configured one

// wrongTTL is the TTL of a wrong answer: a minute, like a static entry.
const wrongTTL = staticTTL

// parsedConfig is a configuration with what every query needs worked out once: the prefixes of the sources, the
// durations and the sets of hostname selectors.
type parsedConfig struct {
	faults []parsedFault
	sets   []parsedSet
}

type parsedFault struct {
	action  model.DnsFaultAction
	names   []string
	overlay string
	uuid    *uuid.UUID
	sources []netip.Prefix
	delay   time.Duration
	ttl     uint32
	answers []net.IP
}

type parsedSet struct {
	id      string
	names   []string
	sources []netip.Prefix // none: every client
}

func parseSources(in []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		} else if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func contains(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseConfig(c *model.DnsServiceConfig) *parsedConfig {
	p := &parsedConfig{}
	for _, f := range c.Faults {
		pf := parsedFault{action: model.DnsFaultAction(f.Action), overlay: f.Overlay.String(), sources: parseSources(f.Sources)}
		if f.Names != nil {
			pf.names = *f.Names
		}
		id := f.Overlay
		pf.uuid = &id
		if f.Delay != nil {
			pf.delay, _ = time.ParseDuration(*f.Delay)
		}
		if f.Ttl != nil {
			if d, err := time.ParseDuration(*f.Ttl); err == nil && d > 0 {
				pf.ttl = uint32(d / time.Second)
			}
		}
		if f.Answers != nil {
			for _, a := range *f.Answers {
				if ip, err := netip.ParseAddr(a); err == nil && ip.Is4() {
					pf.answers = append(pf.answers, net.IP(ip.AsSlice()))
				}
			}
		}
		p.faults = append(p.faults, pf)
	}
	if c.HostnameSets != nil {
		for _, s := range *c.HostnameSets {
			ps := parsedSet{id: s.Set}
			for _, n := range s.Names {
				ps.names = append(ps.names, domain.HostnamePattern(n))
			}
			if s.Sources != nil {
				ps.sources = parseSources(*s.Sources)
			}
			p.sets = append(p.sets, ps)
		}
	}
	return p
}

// faultFor returns the fault that answers a query: the first entry for the client whose names match the name.
func (p *parsedConfig) faultFor(client netip.Addr, name string) *parsedFault {
	for i := range p.faults {
		f := &p.faults[i]
		if !contains(f.sources, client) {
			continue
		}
		if len(f.names) > 0 {
			matched := false
			for _, n := range f.names {
				matched = matched || domain.HostnameMatches(n, name)
			}
			if !matched {
				continue
			}
		}
		return f
	}
	return nil
}

// synthetic builds the answer of a fault that does not ask the upstream. ok is false for a fault that needs
// the real answer first (delay, truncate, short_ttl) or does not apply to this query type (wrong_answer
// for anything but A and AAAA).
func (f *parsedFault) synthetic(r *dns.Msg, q dns.Question) (m *dns.Msg, ok bool) {
	m = new(dns.Msg)
	switch f.action {
	case model.DnsFaultActionNxdomain:
		m.SetRcode(r, dns.RcodeNameError)
	case model.DnsFaultActionServfail:
		m.SetRcode(r, dns.RcodeServerFailure)
	case model.DnsFaultActionWrongAnswer:
		switch q.Qtype {
		case dns.TypeA:
			m.SetReply(r)
			for _, ip := range f.answers {
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: wrongTTL}, A: ip})
			}
		case dns.TypeAAAA:
			m.SetReply(r) // the name exists, and has no IPv6 address
		default:
			return nil, false
		}
	default:
		return nil, false
	}
	m.RecursionAvailable = true
	return m, true
}

// shorten sets the TTL of every record of an answer (the OPT record has none).
func shorten(m *dns.Msg, ttl uint32) {
	for _, section := range [][]dns.RR{m.Answer, m.Ns, m.Extra} {
		for _, rr := range section {
			if rr.Header().Rrtype != dns.TypeOPT {
				rr.Header().Ttl = ttl
			}
		}
	}
}

// changed reports whether a fault of this action changes the answer to this query on this transport: the query
// log names the fault only then (a truncation over TCP, a wrong answer for an MX query leave the answer as it was).
func (f *parsedFault) changes(q dns.Question, tcp bool) bool {
	switch f.action {
	case model.DnsFaultActionTruncate:
		return !tcp
	case model.DnsFaultActionWrongAnswer:
		return q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA
	}
	return true
}

func lowerName(n string) string { return strings.ToLower(n) }
