package diag

import (
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The identifiers a script gives to a reply: that of the echo it answers, or another one.
const (
	ownID   = -1
	otherID = -2
)

// fakeICMP is the socket of the echoes with a script: reply says what comes back for the echo with that sequence
// number and when.
type fakeICMP struct {
	datagram bool
	peer     net.IP
	reply    func(seq int) (typ icmp.Type, id int, delay time.Duration, ok bool)
	sendErr  error

	mu       sync.Mutex
	queue    chan []byte
	closed   chan struct{}
	once     sync.Once
	deadline time.Time
	wrote    int
	// also are the other sockets that get every reply too, as the raw sockets of a host do
	also []*fakeICMP
}

func newFakeICMP(datagram bool, peer string, reply func(seq int) (icmp.Type, int, time.Duration, bool)) *fakeICMP {
	return &fakeICMP{datagram: datagram, peer: net.ParseIP(peer), reply: reply, queue: make(chan []byte, 256), closed: make(chan struct{})}
}

func (f *fakeICMP) WriteTo(b []byte, _ net.Addr) (int, error) {
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	m, err := icmp.ParseMessage(1, b)
	if err != nil {
		return 0, err
	}
	echo := m.Body.(*icmp.Echo)
	f.mu.Lock()
	f.wrote++
	f.mu.Unlock()
	if typ, id, delay, ok := f.reply(echo.Seq); ok {
		switch id {
		case ownID:
			id = echo.ID
		case otherID:
			id = echo.ID + 1
		}
		var body icmp.MessageBody = &icmp.Echo{ID: id, Seq: echo.Seq, Data: echo.Data}
		if typ == ipv4.ICMPTypeDestinationUnreachable {
			body = &icmp.DstUnreach{Data: b}
		}
		out, _ := (&icmp.Message{Type: typ, Body: body}).Marshal(nil)
		time.AfterFunc(delay, func() {
			for _, c := range append([]*fakeICMP{f}, f.also...) {
				select {
				case c.queue <- out:
				case <-c.closed:
				}
			}
		})
	}
	return len(b), nil
}

func (f *fakeICMP) ReadFrom(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	dl := f.deadline
	f.mu.Unlock()
	var timer <-chan time.Time
	if !dl.IsZero() {
		timer = time.After(time.Until(dl))
	}
	select {
	case p := <-f.queue:
		n := copy(b, p)
		if f.datagram {
			return n, &net.UDPAddr{IP: f.peer}, nil
		}
		return n, &net.IPAddr{IP: f.peer}, nil
	case <-f.closed:
		return 0, nil, net.ErrClosed
	case <-timer:
		return 0, nil, &net.OpError{Op: "read", Err: timeoutErr{}}
	}
}

func (f *fakeICMP) Close() error                     { f.once.Do(func() { close(f.closed) }); return nil }
func (f *fakeICMP) LocalAddr() net.Addr              { return &net.IPAddr{} }
func (f *fakeICMP) SetDeadline(t time.Time) error    { return f.SetReadDeadline(t) }
func (f *fakeICMP) SetWriteDeadline(time.Time) error { return nil }
func (f *fakeICMP) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

type icmpNet struct {
	HostNet
	conn net.PacketConn
	err  error
}

func (n icmpNet) ListenICMP() (net.PacketConn, bool, error) {
	f, _ := n.conn.(*fakeICMP)
	return n.conn, f != nil && f.datagram, n.err
}

func pingReq(host string, count int, timeout string) model.DiagnosticRequest {
	return req(model.DiagnosticRequestTypePing, host, func(r *model.DiagnosticRequest) { r.Count = &count }, withTimeout(timeout))
}

func ok(delay time.Duration) func(int) (icmp.Type, int, time.Duration, bool) {
	return func(int) (icmp.Type, int, time.Duration, bool) {
		return ipv4.ICMPTypeEchoReply, ownID, delay, true
	}
}

func TestPingCountsLossAndMeasuresTheRoundTrips(t *testing.T) {
	delays := map[int]time.Duration{1: 10 * time.Millisecond, 3: 20 * time.Millisecond, 5: 30 * time.Millisecond}
	f := newFakeICMP(true, "203.0.113.10", func(seq int) (icmp.Type, int, time.Duration, bool) {
		d, ok := delays[seq]
		return ipv4.ICMPTypeEchoReply, 4242, d, ok // the kernel owns the identifier of a datagram socket
	})
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 5, "300ms"))
	p := res.Ping
	if !res.Ok || p == nil || *p.Sent != 5 || *p.Received != 3 || *p.LossPercent != 40 || res.Error != nil {
		t.Fatalf("%+v %+v", res, p)
	}
	if *p.RttMinMs < 10 || *p.RttMinMs > 60 || *p.RttMaxMs < 30 || *p.RttMaxMs > 150 || *p.RttAvgMs < *p.RttMinMs || *p.RttAvgMs > *p.RttMaxMs || *p.RttMdevMs <= 0 {
		t.Errorf("rtt min %v avg %v max %v mdev %v", *p.RttMinMs, *p.RttAvgMs, *p.RttMaxMs, *p.RttMdevMs)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wrote != 5 {
		t.Errorf("%d echoes written", f.wrote)
	}
}

func TestPingEndsWhenEveryReplyIsIn(t *testing.T) {
	f := newFakeICMP(true, "203.0.113.10", ok(time.Millisecond))
	t0 := time.Now()
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 3, "10s"))
	if !res.Ok || *res.Ping.Received != 3 || *res.Ping.LossPercent != 0 {
		t.Fatalf("%+v", res.Ping)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Errorf("took %s: the last reply is not waited for the timeout", d)
	}
}

func TestPingWithoutAReplyFails(t *testing.T) {
	f := newFakeICMP(true, "203.0.113.10", func(int) (icmp.Type, int, time.Duration, bool) { return nil, 0, 0, false })
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 2, "200ms"))
	if res.Ok || *res.Ping.Sent != 2 || *res.Ping.Received != 0 || *res.Ping.LossPercent != 100 || res.Ping.RttMinMs != nil || str(res.Error) != "no reply to 2 echoes within 200ms" {
		t.Errorf("%+v / %s", res.Ping, str(res.Error))
	}
}

func TestPingNamesAnICMPErrorThatCameBack(t *testing.T) {
	f := newFakeICMP(false, "203.0.113.10", func(int) (icmp.Type, int, time.Duration, bool) {
		return ipv4.ICMPTypeDestinationUnreachable, 0, time.Millisecond, true
	})
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 1, "150ms"))
	if res.Ok || str(res.Error) != "no reply to 1 echoes within 150ms (ICMP destination unreachable)" {
		t.Errorf("%s", str(res.Error))
	}
}

func TestPingOnARawSocketTakesOnlyItsOwnRepliesFromTheTarget(t *testing.T) {
	// a raw socket sees the ICMP of every host and every process: the identifier and the source decide
	f := newFakeICMP(false, "203.0.113.10", func(seq int) (icmp.Type, int, time.Duration, bool) {
		if seq == 1 {
			return ipv4.ICMPTypeEchoReply, otherID, time.Millisecond, true // somebody else's
		}
		return ipv4.ICMPTypeEchoReply, ownID, time.Millisecond, true
	})
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 2, "300ms"))
	if !res.Ok || *res.Ping.Received != 1 || *res.Ping.LossPercent != 50 {
		t.Errorf("%+v", res.Ping)
	}
	other := newFakeICMP(false, "203.0.113.99", ok(time.Millisecond))
	res = run(t, New(Config{Net: icmpNet{conn: other}}), pingReq("203.0.113.10", 1, "150ms"))
	if res.Ok {
		t.Errorf("a reply of another host counted: %+v", res.Ping)
	}
}

// Up to eight pings run at once, and a raw socket gets the ICMP of every socket: a run takes the replies of its own
// echoes only (P6-AUDIT-19).
func TestTwoPingsAtOnceOnRawSocketsDoNotTakeEachOthersReplies(t *testing.T) {
	// A is answered, slowly; B starts a little later and is never answered, but its socket sees A's replies
	a := newFakeICMP(false, "203.0.113.10", ok(300*time.Millisecond))
	b := newFakeICMP(false, "203.0.113.10", func(int) (icmp.Type, int, time.Duration, bool) { return nil, 0, 0, false })
	a.also = []*fakeICMP{b}
	var resA, resB model.DiagnosticResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		resA = run(t, New(Config{Net: icmpNet{conn: a}}), pingReq("203.0.113.10", 3, "1s"))
	}()
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		resB = run(t, New(Config{Net: icmpNet{conn: b}}), pingReq("203.0.113.10", 3, "300ms"))
	}()
	wg.Wait()
	if !resA.Ok || *resA.Ping.Received != 3 {
		t.Errorf("A: %+v", resA.Ping)
	}
	if resB.Ok || *resB.Ping.Received != 0 || *resB.Ping.LossPercent != 100 {
		t.Errorf("B counted replies that belong to A: ok %v, %+v", resB.Ok, resB.Ping)
	}
}

// The identifier alone is not enough on a raw socket: a ping of another process may use the same one.
func TestPingOnARawSocketIgnoresAReplyWithAnotherPayload(t *testing.T) {
	f := newFakeICMP(false, "203.0.113.10", ok(time.Millisecond))
	res := run(t, New(Config{Net: icmpNet{conn: &foreignData{fakeICMP: f}}}), pingReq("203.0.113.10", 2, "200ms"))
	if res.Ok || *res.Ping.Received != 0 {
		t.Errorf("%+v", res.Ping)
	}
}

// foreignData changes the last byte of every reply, as the echo of another run would carry other data (the parser of
// the package does not check the checksum).
type foreignData struct{ *fakeICMP }

func (d *foreignData) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, err := d.fakeICMP.ReadFrom(b)
	if err == nil && n > 0 {
		b[n-1] ^= 0xff
	}
	return n, a, err
}

func TestPingCountsADuplicateReplyOnce(t *testing.T) {
	f := newFakeICMP(true, "203.0.113.10", ok(time.Millisecond))
	conn := &dupICMP{fakeICMP: f}
	res := run(t, New(Config{Net: icmpNet{conn: conn}}), pingReq("203.0.113.10", 2, "300ms"))
	if !res.Ok || *res.Ping.Received != 2 || *res.Ping.LossPercent != 0 {
		t.Errorf("%+v", res.Ping)
	}
}

// dupICMP delivers every reply twice.
type dupICMP struct{ *fakeICMP }

func (d *dupICMP) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, err := d.fakeICMP.ReadFrom(b)
	if err == nil {
		d.queue <- append([]byte(nil), b[:n]...)
	}
	return n, a, err
}

func TestPingFailuresToSendAndToOpenTheSocketAreResults(t *testing.T) {
	f := newFakeICMP(true, "203.0.113.10", ok(0))
	f.sendErr = os.NewSyscallError("sendto", syscall.ENETUNREACH)
	res := run(t, New(Config{Net: icmpNet{conn: f}}), pingReq("203.0.113.10", 3, "200ms"))
	if res.Ok || res.Ping != nil || str(res.Error) != "network is unreachable" {
		t.Errorf("%+v / %s", res.Ping, str(res.Error))
	}
	res = run(t, New(Config{Net: icmpNet{conn: f, err: errors.New("cannot open an ICMP socket (permission denied)")}}), pingReq("203.0.113.10", 1, "200ms"))
	if res.Ok || !strings.Contains(str(res.Error), "cannot open an ICMP socket") {
		t.Errorf("%s", str(res.Error))
	}
}

// The real thing, on the loopback: the kernel answers the echoes. It needs an ICMP socket, which a container without
// ping_group_range for its group does not give.
func TestPingTheLoopbackOnTheRealStack(t *testing.T) {
	if c, _, err := (HostNet{}).ListenICMP(); err != nil {
		t.Skipf("this process cannot open an ICMP socket: %v", err)
	} else {
		_ = c.Close()
	}
	res := run(t, New(Config{}), pingReq(loop, 3, "2s"))
	if !res.Ok || *res.Ping.Sent != 3 || *res.Ping.Received != 3 || *res.Ping.LossPercent != 0 || *res.Ping.RttMinMs <= 0 || *res.Ping.RttMaxMs > 500 {
		t.Errorf("%+v / %s", res.Ping, str(res.Error))
	}
}
