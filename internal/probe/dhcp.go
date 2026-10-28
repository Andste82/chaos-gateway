// Package probe is what runs inside the network namespace of a probe (plan §2.12, M25): the DHCP client that gives the
// probe its address like a real device's, and the echo measurement that the self-test "measure this profile" is made
// of. The executor starts both in the namespace; the logic here has no kernel of its own, it works on a Socket and a
// net stack, so the tests drive it on the loopback and with scripted servers.
package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// DHCP message types (RFC 2132, option 53).
const (
	msgDiscover = 1
	msgOffer    = 2
	msgRequest  = 3
	msgAck      = 5
	msgNak      = 6
)

// DHCP options the client reads or writes (RFC 2132).
const (
	optSubnetMask  = 1
	optRouter      = 3
	optDNS         = 6
	optHostname    = 12
	optDomain      = 15
	optRequestedIP = 50
	optLeaseTime   = 51
	optMessageType = 53
	optServerID    = 54
	optParamList   = 55
	optRenewalT1   = 58
	optRebindingT2 = 59
	optClientID    = 61
	optEnd         = 255
)

const (
	serverPort = 67
	// bootpMin is the smallest message a BOOTP server accepts (RFC 951).
	bootpMin = 300
	magic    = 0x63825363
)

// Lease is what a server granted.
type Lease struct {
	// Addr is the address with the network's prefix length.
	Addr netip.Prefix `json:"addr"`
	// Router is the first router of the lease, the zero value when it names none.
	Router netip.Addr   `json:"router,omitzero"`
	DNS    []netip.Addr `json:"dns,omitempty"`
	Domain string       `json:"domain,omitempty"`
	// Server is the server that granted it (option 54).
	Server netip.Addr `json:"server"`
	// Duration is the lease time; zero is an infinite lease. T1 and T2 are the times of renewing and rebinding, counted from
	// the grant.
	Duration time.Duration `json:"duration"`
	T1, T2   time.Duration `json:"-"`
	// Granted is when the lease was granted (wall clock), Expires when it runs out (zero for an infinite lease).
	Granted time.Time `json:"granted_at"`
	Expires time.Time `json:"expires_at,omitzero"`
}

// Socket is the datagram socket of the client on its interface: UDP port 68 on all addresses, bound to the interface,
// allowed to broadcast. The executor makes it inside the probe's namespace.
type Socket interface {
	// Send sends one DHCP message to dst: the broadcast address and port 67, or the server's address.
	Send(pkt []byte, dst netip.AddrPort) error
	// Recv blocks until a message arrives; a closed socket makes it fail.
	Recv() ([]byte, error)
	Close() error
}

// Client is the DHCP client of one probe. Run drives it until the context ends.
type Client struct {
	// MAC is the hardware address of the interface, Hostname the name sent in option 12 (empty: none).
	MAC      net.HardwareAddr
	Hostname string
	Sock     Socket
	Clock    clock.Clock
	// Bound is called when the client holds a lease (a new one, or a renewed one with other parameters) and Lost when it has
	// none any more (the lease ran out, or the server refused it). The address is configured by these.
	Bound func(Lease)
	Lost  func(Lease)
	Log   *slog.Logger
	// XID returns the transaction id of a new exchange; nil draws a random one.
	XID func() uint32
	// Retry is the wait for the first answer, doubling up to MaxRetry; the defaults are 2 s and 16 s.
	Retry, MaxRetry time.Duration

	mu    sync.Mutex
	lease *Lease
}

// Lease returns the lease the client holds now, nil when it has none.
func (c *Client) Lease() *Lease {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease == nil {
		return nil
	}
	l := *c.lease
	return &l
}

func (c *Client) setLease(l *Lease) {
	c.mu.Lock()
	c.lease = l
	c.mu.Unlock()
}

func (c *Client) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (c *Client) xid() uint32 {
	if c.XID != nil {
		return c.XID()
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func (c *Client) retries() (first, most time.Duration) {
	first, most = c.Retry, c.MaxRetry
	if first <= 0 {
		first = 2 * time.Second
	}
	if most <= 0 {
		most = 16 * time.Second
	}
	return
}

// Run gets a lease and keeps it (renews at T1, rebinds at T2, starts over when it ran out) until ctx ends. It returns ctx's
// error and closes the socket.
func (c *Client) Run(ctx context.Context) error {
	defer func() { _ = c.Sock.Close() }()
	in := make(chan *message, 16)
	go func() {
		defer close(in)
		for {
			b, err := c.Sock.Recv()
			if err != nil {
				return
			}
			if m, err := parse(b); err == nil && bytes.Equal(m.chaddr[:len(c.MAC)], c.MAC) {
				select {
				case in <- m:
				default:
				}
			}
		}
	}()
	for ctx.Err() == nil {
		l, err := c.acquire(ctx, in)
		if err != nil {
			if ctx.Err() == nil {
				c.log().Warn("dhcp: no lease", "error", err)
				select {
				case <-ctx.Done():
				case <-c.Clock.After(time.Second):
				}
			}
			continue
		}
		c.setLease(l)
		if c.Bound != nil {
			c.Bound(*l)
		}
		last, lost := c.keep(ctx, in, l)
		c.setLease(nil)
		if lost && c.Lost != nil {
			c.Lost(last)
		}
	}
	return ctx.Err()
}

// acquire runs DISCOVER, OFFER, REQUEST and ACK until a lease is granted. A message is repeated with the doubling wait of
// RFC 2131 until its answer comes. A refusal (NAK) or a reply that cannot be used is followed by a pause that doubles up to
// the longest wait, so that a server which refuses everything is not answered with a flood of DISCOVERs.
func (c *Client) acquire(ctx context.Context, in <-chan *message) (*Lease, error) {
	xid := c.xid()
	first, most := c.retries()
	wait, pause := first, first
	// again pauses and prepares a new exchange (the loop ends when the context ended meanwhile)
	again := func(why string) {
		c.log().Warn("dhcp: the exchange is started again", "reason", why, "after", pause)
		select {
		case <-ctx.Done():
		case <-c.Clock.After(pause):
		}
		pause = min(pause*2, most)
		xid = c.xid()
	}
	for ctx.Err() == nil {
		c.drain(in)
		if err := c.Sock.Send(c.build(msgDiscover, xid, netip.Addr{}, netip.Addr{}, netip.Addr{}), broadcast); err != nil {
			return nil, err
		}
		offer, err := c.await(ctx, in, xid, wait, msgOffer)
		if err != nil {
			if errors.Is(err, errTimeout) {
				wait = min(wait*2, most)
				continue
			}
			return nil, err
		}
		server, hasServer := offer.addr(optServerID)
		if !hasServer || !usable(offer.yiaddr) {
			again("the offer has no server identifier or no usable address")
			continue
		}
		if err := c.Sock.Send(c.build(msgRequest, xid, netip.Addr{}, offer.yiaddr, server), broadcast); err != nil {
			return nil, err
		}
		ack, err := c.await(ctx, in, xid, wait, msgAck)
		if err != nil {
			switch {
			case errors.Is(err, errNak):
				again(err.Error())
				continue
			case errors.Is(err, errTimeout):
				xid = c.xid()
				continue
			}
			return nil, err
		}
		l, err := c.leaseOf(ack)
		if err == nil && l.Server != server {
			err = fmt.Errorf("the ACK comes from %v, the offer from %v", l.Server, server)
		}
		if err != nil {
			again(err.Error())
			continue
		}
		return l, nil
	}
	return nil, ctx.Err()
}

// keep holds the lease: it renews with the server at T1, asks any server at T2 and gives the lease up when it runs out or
// the server refuses it. It returns the lease it last held and whether that was lost (false: the context ended).
func (c *Client) keep(ctx context.Context, in <-chan *message, l *Lease) (Lease, bool) {
	for {
		if l.Duration == 0 {
			<-ctx.Done() // an infinite lease is kept for ever
			return *l, false
		}
		start := c.Clock.Monotonic()
		select {
		case <-ctx.Done():
			return *l, false
		case <-c.Clock.After(l.T1):
		}
		nl, err := c.renew(ctx, in, l, start)
		switch {
		case err == nil:
			if nl.Addr != l.Addr || nl.Router != l.Router || fmt.Sprint(nl.DNS) != fmt.Sprint(l.DNS) {
				c.setLease(nl)
				if c.Bound != nil {
					c.Bound(*nl)
				}
			} else {
				c.setLease(nl)
			}
			l = nl
		case ctx.Err() != nil:
			return *l, false
		default:
			return *l, true
		}
	}
}

// renew asks the server of the lease to extend it, unicast, until T2, then every server by broadcast until the lease runs
// out. start is when the lease was granted, on the monotonic clock.
func (c *Client) renew(ctx context.Context, in <-chan *message, l *Lease, start time.Duration) (*Lease, error) {
	// one renewal is one transaction: its repetitions keep the id, so a late answer to an earlier try is still the answer
	xid := c.xid()
	for {
		elapsed := c.Clock.Monotonic() - start
		if elapsed >= l.Duration {
			return nil, errors.New("the lease ran out")
		}
		dst, until := netip.AddrPortFrom(l.Server, serverPort), l.T2
		if elapsed >= l.T2 {
			dst, until = broadcast, l.Duration
		}
		c.drain(in)
		if err := c.Sock.Send(c.build(msgRequest, xid, l.Addr.Addr(), netip.Addr{}, netip.Addr{}), dst); err != nil {
			c.log().Warn("dhcp: cannot renew", "error", err)
		}
		left := until - elapsed
		wait := min(max(left/2, time.Second), left)
		ack, err := c.await(ctx, in, xid, wait, msgAck)
		switch {
		case err == nil:
			nl, lerr := c.leaseOf(ack)
			if lerr == nil {
				return nl, nil
			}
			// an ACK that cannot be used is no answer: ask again (after a pause) until the lease runs out
			c.log().Warn("dhcp: the renewal is not taken", "reason", lerr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.Clock.After(time.Second):
			}
		case errors.Is(err, errTimeout): // asked again, or the lease has run out by now
		default:
			return nil, err
		}
	}
}

// The smallest lease time the client takes (a server that grants less would have it asking in a loop) and the smallest
// distance of T1 from the grant and of T2 from T1.
const (
	minLease = 4 * time.Second
	minTimer = time.Second
)

// usable says whether an address can be a client's: not unspecified, not the broadcast address, no multicast, no loopback.
func usable(a netip.Addr) bool {
	return a.Is4() && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLoopback() && a != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

// leaseOf reads a lease from an ACK. An ACK that RFC 2131 does not allow is an error: no usable address, no server
// identifier, no lease time (0xffffffff is the infinite lease), a lease time below minLease, a mask with holes.
func (c *Client) leaseOf(m *message) (*Lease, error) {
	now := c.Clock.Now()
	if !usable(m.yiaddr) {
		return nil, fmt.Errorf("the ACK gives the address %v", m.yiaddr)
	}
	l := &Lease{Granted: now, Addr: netip.PrefixFrom(m.yiaddr, 24)}
	if mask, ok := m.addr(optSubnetMask); ok {
		ones, bits := net.IPMask(mask.AsSlice()).Size()
		if bits != 32 || ones == 0 {
			return nil, fmt.Errorf("the ACK gives the subnet mask %v", mask)
		}
		l.Addr = netip.PrefixFrom(m.yiaddr, ones)
	}
	l.Router, _ = m.addr(optRouter)
	l.DNS = m.addrs(optDNS)
	l.Domain = string(m.opts[optDomain])
	var ok bool
	if l.Server, ok = m.addr(optServerID); !ok || !usable(l.Server) {
		return nil, errors.New("the ACK has no server identifier")
	}
	v, ok := m.u32(optLeaseTime)
	switch {
	case !ok:
		return nil, errors.New("the ACK has no lease time")
	case v == 0xffffffff:
		return l, nil
	case time.Duration(v)*time.Second < minLease:
		return nil, fmt.Errorf("the lease time of %d s is too short", v)
	}
	l.Duration = time.Duration(v) * time.Second
	l.Expires = now.Add(l.Duration)
	l.T1, l.T2 = l.Duration/2, l.Duration*7/8
	if t, ok := m.u32(optRenewalT1); ok && time.Duration(t)*time.Second < l.Duration {
		l.T1 = time.Duration(t) * time.Second
	}
	if t, ok := m.u32(optRebindingT2); ok && time.Duration(t)*time.Second < l.Duration {
		l.T2 = time.Duration(t) * time.Second
	}
	// the timers have a floor, so that a T1 of 0 does not have the client renew as fast as the server answers
	l.T1 = max(l.T1, minTimer)
	l.T2 = max(l.T2, l.T1+minTimer)
	return l, nil
}

var (
	errTimeout = errors.New("no answer")
	errNak     = errors.New("the server refused (NAK)")
	broadcast  = netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), serverPort)
)

// await waits up to wait for a message of the type with the transaction id; a NAK ends the wait with errNak.
func (c *Client) await(ctx context.Context, in <-chan *message, xid uint32, wait time.Duration, want byte) (*message, error) {
	timer := c.Clock.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C():
			return nil, errTimeout
		case m, ok := <-in:
			if !ok {
				return nil, errors.New("the socket is closed")
			}
			if m.xid != xid {
				continue
			}
			switch m.typ {
			case want:
				return m, nil
			case msgNak:
				return nil, errNak
			}
		}
	}
}

func (c *Client) drain(in <-chan *message) {
	for {
		select {
		case <-in:
		default:
			return
		}
	}
}

// message is a parsed DHCP message.
type message struct {
	xid                            uint32
	flags                          uint16
	ciaddr, yiaddr, siaddr, giaddr netip.Addr
	chaddr                         [16]byte
	typ                            byte
	opts                           map[byte][]byte
}

func (m *message) addr(code byte) (netip.Addr, bool) {
	if v := m.opts[code]; len(v) >= 4 {
		return netip.AddrFrom4([4]byte(v[:4])), true
	}
	return netip.Addr{}, false
}

func (m *message) addrs(code byte) []netip.Addr {
	var out []netip.Addr
	v := m.opts[code]
	for ; len(v) >= 4; v = v[4:] {
		out = append(out, netip.AddrFrom4([4]byte(v[:4])))
	}
	return out
}

func (m *message) u32(code byte) (uint32, bool) {
	if v := m.opts[code]; len(v) == 4 {
		return binary.BigEndian.Uint32(v), true
	}
	return 0, false
}

// parse reads a BOOTREPLY.
func parse(b []byte) (*message, error) {
	if len(b) < 240 || b[0] != 2 || binary.BigEndian.Uint32(b[236:240]) != magic {
		return nil, errors.New("not a DHCP reply")
	}
	m := &message{xid: binary.BigEndian.Uint32(b[4:8]), flags: binary.BigEndian.Uint16(b[10:12]), opts: map[byte][]byte{}}
	a4 := func(o int) netip.Addr { return netip.AddrFrom4([4]byte(b[o : o+4])) }
	m.ciaddr, m.yiaddr, m.siaddr, m.giaddr = a4(12), a4(16), a4(20), a4(24)
	copy(m.chaddr[:], b[28:44])
	for o := 240; o < len(b); {
		code := b[o]
		if code == optEnd {
			break
		}
		if code == 0 {
			o++
			continue
		}
		if o+1 >= len(b) || o+2+int(b[o+1]) > len(b) {
			return nil, errors.New("truncated option")
		}
		n := int(b[o+1])
		if _, dup := m.opts[code]; !dup {
			m.opts[code] = append([]byte(nil), b[o+2:o+2+n]...)
		}
		o += 2 + n
	}
	if v := m.opts[optMessageType]; len(v) == 1 {
		m.typ = v[0]
	}
	if m.typ == 0 {
		return nil, errors.New("no message type")
	}
	return m, nil
}

// build writes a BOOTREQUEST. ciaddr is the client's address when it has one (renewing, rebinding), requested the address it
// asks for and server the one it chose (selecting).
func (c *Client) build(typ byte, xid uint32, ciaddr, requested, server netip.Addr) []byte {
	b := make([]byte, 240, bootpMin)
	b[0], b[1], b[2] = 1, 1, byte(len(c.MAC))
	binary.BigEndian.PutUint32(b[4:8], xid)
	if ciaddr.IsValid() {
		a := ciaddr.As4()
		copy(b[12:16], a[:])
	} else {
		binary.BigEndian.PutUint16(b[10:12], 0x8000) // answer by broadcast: the client has no address to be answered at
	}
	copy(b[28:44], c.MAC)
	binary.BigEndian.PutUint32(b[236:240], magic)
	opt := func(code byte, v ...byte) { b = append(b, append([]byte{code, byte(len(v))}, v...)...) }
	opt(optMessageType, typ)
	opt(optClientID, append([]byte{1}, c.MAC...)...)
	if c.Hostname != "" {
		opt(optHostname, []byte(c.Hostname)...)
	}
	if requested.IsValid() {
		a := requested.As4()
		opt(optRequestedIP, a[:]...)
	}
	if server.IsValid() {
		a := server.As4()
		opt(optServerID, a[:]...)
	}
	opt(optParamList, optSubnetMask, optRouter, optDNS, optDomain, optLeaseTime, optRenewalT1, optRebindingT2)
	b = append(b, optEnd)
	for len(b) < bootpMin {
		b = append(b, 0)
	}
	return b
}
