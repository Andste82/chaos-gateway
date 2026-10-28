package probe

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// fakeSocket is the client's socket in a test: what it sends is handed to the test, what the test puts in rx is what arrives.
type fakeSocket struct {
	sent chan sentPacket
	rx   chan []byte
	done chan struct{}
}

type sentPacket struct {
	pkt []byte
	dst netip.AddrPort
}

func newFakeSocket() *fakeSocket {
	return &fakeSocket{sent: make(chan sentPacket, 64), rx: make(chan []byte, 64), done: make(chan struct{})}
}

func (s *fakeSocket) Send(p []byte, dst netip.AddrPort) error {
	s.sent <- sentPacket{append([]byte(nil), p...), dst}
	return nil
}

func (s *fakeSocket) Recv() ([]byte, error) {
	select {
	case b := <-s.rx:
		return b, nil
	case <-s.done:
		return nil, net.ErrClosed
	}
}

func (s *fakeSocket) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

var testMAC = net.HardwareAddr{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

// request parses a message the client sent: the same layout as a reply, with the op 1.
func request(t *testing.T, p sentPacket) *message {
	t.Helper()
	b := append([]byte(nil), p.pkt...)
	b[0] = 2
	m, err := parse(b)
	if err != nil {
		t.Fatalf("the client sent something that is not DHCP: %v", err)
	}
	return m
}

// reply builds what a server answers.
func reply(typ byte, xid uint32, yiaddr string, opts map[byte][]byte) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 2, 1, 6
	binary.BigEndian.PutUint32(b[4:8], xid)
	if yiaddr != "" {
		a := netip.MustParseAddr(yiaddr).As4()
		copy(b[16:20], a[:])
	}
	copy(b[28:44], testMAC)
	binary.BigEndian.PutUint32(b[236:240], magic)
	b = append(b, optMessageType, 1, typ)
	for code, v := range opts {
		b = append(b, code, byte(len(v)))
		b = append(b, v...)
	}
	return append(b, optEnd)
}

func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

func addr4(s string) []byte { a := netip.MustParseAddr(s).As4(); return a[:] }

func leaseOpts(secs uint32) map[byte][]byte {
	return map[byte][]byte{
		optServerID:   addr4("10.10.0.1"),
		optSubnetMask: addr4("255.255.255.0"),
		optRouter:     addr4("10.10.0.1"),
		optDNS:        append(addr4("10.10.0.1"), addr4("9.9.9.9")...),
		optDomain:     []byte("lab.test"),
		optLeaseTime:  u32(secs),
	}
}

type harness struct {
	t       *testing.T
	sock    *fakeSocket
	clk     *clock.Fake
	c       *Client
	bound   chan Lease
	lost    chan Lease
	cancel  context.CancelFunc
	stopped chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, sock: newFakeSocket(), clk: clock.NewFake(time.Unix(1_800_000_000, 0)), bound: make(chan Lease, 8), lost: make(chan Lease, 8), stopped: make(chan struct{})}
	xid := uint32(0x1000)
	h.c = &Client{MAC: testMAC, Hostname: "probe-a", Sock: h.sock, Clock: h.clk, XID: func() uint32 { xid++; return xid },
		Bound: func(l Lease) { h.bound <- l }, Lost: func(l Lease) { h.lost <- l }}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { defer close(h.stopped); _ = h.c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-h.stopped })
	return h
}

func (h *harness) next() sentPacket {
	h.t.Helper()
	select {
	case p := <-h.sock.sent:
		return p
	case <-time.After(5 * time.Second):
		h.t.Fatal("the client sent nothing")
	}
	return sentPacket{}
}

func (h *harness) nextBound() Lease {
	h.t.Helper()
	select {
	case l := <-h.bound:
		return l
	case <-time.After(5 * time.Second):
		h.t.Fatal("the client holds no lease")
	}
	return Lease{}
}

func (h *harness) nextLost() Lease {
	h.t.Helper()
	select {
	case l := <-h.lost:
		return l
	case <-time.After(5 * time.Second):
		h.t.Fatal("the client did not give the lease up")
	}
	return Lease{}
}

// acquire runs the exchange up to the lease and returns it.
func (h *harness) acquire(secs uint32) Lease {
	h.t.Helper()
	d := h.next()
	m := request(h.t, d)
	if m.typ != msgDiscover || d.dst != broadcast {
		h.t.Fatalf("the first message is %d to %v", m.typ, d.dst)
	}
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", leaseOpts(secs))
	r := h.next()
	rm := request(h.t, r)
	if rm.typ != msgRequest {
		h.t.Fatalf("the second message is %d", rm.typ)
	}
	h.sock.rx <- reply(msgAck, rm.xid, "10.10.0.77", leaseOpts(secs))
	return h.nextBound()
}

func TestTheClientGetsALeaseInFourMessages(t *testing.T) {
	h := newHarness(t)
	d := h.next()
	m := request(t, d)
	if m.typ != msgDiscover || d.dst != broadcast {
		t.Fatalf("%d to %v", m.typ, d.dst)
	}
	// a message of a client that is not us, and one of another exchange, are ignored
	other := reply(msgOffer, m.xid, "10.10.0.99", leaseOpts(3600))
	copy(other[28:34], []byte{2, 1, 1, 1, 1, 1})
	h.sock.rx <- other
	h.sock.rx <- reply(msgOffer, m.xid+7, "10.10.0.98", leaseOpts(3600))
	if m.flags != 0x8000 || m.ciaddr.IsValid() && m.ciaddr != netip.AddrFrom4([4]byte{}) {
		t.Errorf("a discover asks for an answer by broadcast: flags %#x ciaddr %v", m.flags, m.ciaddr)
	}
	if string(m.opts[optHostname]) != "probe-a" || len(m.opts[optClientID]) != 7 || m.opts[optClientID][0] != 1 || string(m.opts[optClientID][1:]) != string(testMAC) {
		t.Errorf("hostname %q client id %x", m.opts[optHostname], m.opts[optClientID])
	}
	if len(d.pkt) < bootpMin {
		t.Errorf("a message of %d bytes is shorter than BOOTP's minimum", len(d.pkt))
	}
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", leaseOpts(3600))
	r := request(t, h.next())
	if r.typ != msgRequest || netip.AddrFrom4([4]byte(r.opts[optRequestedIP])) != netip.MustParseAddr("10.10.0.77") || netip.AddrFrom4([4]byte(r.opts[optServerID])) != netip.MustParseAddr("10.10.0.1") || r.xid != m.xid {
		t.Errorf("%+v", r)
	}
	h.sock.rx <- reply(msgAck, r.xid, "10.10.0.77", leaseOpts(3600))
	l := h.nextBound()
	if l.Addr != netip.MustParsePrefix("10.10.0.77/24") || l.Router != netip.MustParseAddr("10.10.0.1") || len(l.DNS) != 2 || l.DNS[1] != netip.MustParseAddr("9.9.9.9") ||
		l.Domain != "lab.test" || l.Server != netip.MustParseAddr("10.10.0.1") || l.Duration != time.Hour || l.T1 != 30*time.Minute || l.T2 != 3150*time.Second {
		t.Errorf("%+v", l)
	}
	if got := h.c.Lease(); got == nil || got.Addr != l.Addr {
		t.Errorf("Lease() = %+v", got)
	}
}

func TestTheClientAsksAgainWithTheSameExchangeWhenNobodyAnswers(t *testing.T) {
	h := newHarness(t)
	first := request(t, h.next())
	h.clk.BlockUntil(1)
	h.clk.Advance(2 * time.Second)
	second := request(t, h.next())
	if second.typ != msgDiscover || second.xid != first.xid {
		t.Errorf("%+v", second)
	}
	// the wait doubles: 4 s now, and after that 8 s
	h.clk.BlockUntil(1)
	h.clk.Advance(3 * time.Second)
	select {
	case <-h.sock.sent:
		t.Fatal("asked again after 3 s of a 4 s wait")
	case <-time.After(100 * time.Millisecond):
	}
	h.clk.Advance(time.Second)
	third := request(t, h.next())
	if third.typ != msgDiscover {
		t.Errorf("%+v", third)
	}
}

func TestTheClientRenewsAtT1AndKeepsTheLease(t *testing.T) {
	h := newHarness(t)
	l := h.acquire(100) // T1 50 s, T2 87.5 s
	if l.T1 != 50*time.Second {
		t.Fatalf("%+v", l)
	}
	h.clk.BlockUntil(1)
	h.clk.Advance(50 * time.Second)
	d := h.next()
	m := request(t, d)
	if m.typ != msgRequest || d.dst != netip.MustParseAddrPort("10.10.0.1:67") || m.ciaddr != netip.MustParseAddr("10.10.0.77") || m.flags != 0 {
		t.Fatalf("a renewal is a unicast request with ciaddr: %d to %v, ciaddr %v flags %#x", m.typ, d.dst, m.ciaddr, m.flags)
	}
	if _, has := m.opts[optRequestedIP]; has {
		t.Error("a renewal carries no requested address")
	}
	h.sock.rx <- reply(msgAck, m.xid, "10.10.0.77", leaseOpts(200))
	// the lease is the new one, and it is not reported again: nothing changed for the interface
	deadline := time.Now().Add(5 * time.Second)
	for h.c.Lease() == nil || h.c.Lease().Duration != 200*time.Second {
		if time.Now().After(deadline) {
			t.Fatalf("the renewed lease was not taken: %+v", h.c.Lease())
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case l := <-h.bound:
		t.Errorf("bound again: %+v", l)
	default:
	}
}

func TestTheClientRebindsByBroadcastAtT2AndLosesTheLeaseWhenItRunsOut(t *testing.T) {
	h := newHarness(t)
	h.acquire(100)
	h.clk.BlockUntil(1)
	h.clk.Advance(50 * time.Second) // T1: renew, unanswered
	if m := request(t, h.next()); m.typ != msgRequest {
		t.Fatal("no renewal")
	}
	var sawBroadcast bool
	// time passes in steps; the client keeps asking, by broadcast once T2 (87.5 s) is past
	for i := 0; i < 80 && len(h.lost) == 0; i++ {
		h.clk.BlockUntil(1)
		h.clk.Advance(time.Second)
		for len(h.sock.sent) > 0 {
			d := <-h.sock.sent
			if request(t, d).typ == msgRequest && d.dst == broadcast {
				sawBroadcast = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawBroadcast {
		t.Error("the client never asked by broadcast")
	}
	l := h.nextLost()
	if l.Addr != netip.MustParsePrefix("10.10.0.77/24") {
		t.Errorf("%+v", l)
	}
	if h.c.Lease() != nil {
		t.Error("the client still holds a lease")
	}
	// and it starts over
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case d := <-h.sock.sent:
			if request(t, d).typ == msgDiscover {
				return
			}
			continue
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no new discover after the lease ran out")
		}
		h.clk.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
}

func TestANakTakesTheLeaseAwayAndTheClientStartsOver(t *testing.T) {
	h := newHarness(t)
	h.acquire(100)
	h.clk.BlockUntil(1)
	h.clk.Advance(50 * time.Second)
	m := request(t, h.next())
	h.sock.rx <- reply(msgNak, m.xid, "", map[byte][]byte{optServerID: addr4("10.10.0.1")})
	h.nextLost()
	if d := request(t, h.next()); d.typ != msgDiscover {
		t.Errorf("%+v", d)
	}
}

func TestAnInfiniteLeaseIsKeptForEver(t *testing.T) {
	h := newHarness(t)
	l := h.acquire(0xffffffff)
	if l.Duration != 0 || !l.Expires.IsZero() {
		t.Fatalf("%+v", l)
	}
	h.clk.Advance(1000 * time.Hour)
	select {
	case d := <-h.sock.sent:
		t.Fatalf("an infinite lease is renewed: %+v", request(t, d))
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAnOfferThatIsRefusedByANakMakesANewExchange(t *testing.T) {
	h := newHarness(t)
	m := request(t, h.next())
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", leaseOpts(3600))
	r := request(t, h.next())
	h.sock.rx <- reply(msgNak, r.xid, "", nil)
	time.Sleep(50 * time.Millisecond) // the client takes the NAK and sets its pause
	// after a refusal the client pauses, so a server that refuses everything is not flooded
	h.clk.BlockUntil(1)
	h.clk.Advance(2 * time.Second)
	again := request(t, h.next())
	if again.typ != msgDiscover || again.xid == m.xid {
		t.Errorf("a refused request starts a new exchange: %+v", again)
	}
}

func TestMessagesThatAreNotDHCPRepliesAreRefused(t *testing.T) {
	for name, b := range map[string][]byte{
		"short":     make([]byte, 100),
		"a request": append([]byte{1}, make([]byte, 300)...),
		"no cookie": append([]byte{2}, make([]byte, 300)...),
	} {
		if _, err := parse(b); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	// an option that runs past the end
	b := reply(msgOffer, 1, "10.0.0.1", nil)
	b = append(b[:len(b)-1], optRouter, 40, 1)
	if _, err := parse(b); err == nil {
		t.Error("a truncated option was taken")
	}
	// a message without a type
	b = make([]byte, 241)
	b[0] = 2
	binary.BigEndian.PutUint32(b[236:240], magic)
	b[240] = optEnd
	if _, err := parse(b); err == nil {
		t.Error("a message without a type was taken")
	}
}

func TestTheRenewalAndRebindingTimesOfTheServerAreUsedWhenTheyFit(t *testing.T) {
	h := newHarness(t)
	opts := leaseOpts(1000)
	opts[optRenewalT1] = u32(100)
	opts[optRebindingT2] = u32(2000) // longer than the lease: ignored
	m := request(t, h.next())
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", opts)
	r := request(t, h.next())
	h.sock.rx <- reply(msgAck, r.xid, "10.10.0.77", opts)
	l := h.nextBound()
	if l.T1 != 100*time.Second || l.T2 != 875*time.Second {
		t.Errorf("%+v", l)
	}
}

func TestTheRealSocketReceivesWhatIsSentToItsPort(t *testing.T) {
	s, err := ListenDHCP("", "lo")
	if err != nil {
		t.Skipf("cannot open UDP port 68 bound to lo here: %v", err)
	}
	defer func() { _ = s.Close() }()
	got := make(chan []byte, 1)
	go func() {
		b, err := s.Recv()
		if err == nil {
			got <- b
		}
	}()
	c, err := net.Dial("udp4", "127.0.0.1:68")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if string(b) != "hello" {
			t.Errorf("%q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived")
	}
	if err := s.Send([]byte("x"), netip.MustParseAddrPort("127.0.0.1:9")); err != nil {
		t.Errorf("send: %v", err)
	}
}
