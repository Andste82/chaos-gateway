package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/diag"
)

// The measurements of the self-test (plan §2.12 "calibration", §4.3): a probe sends numbered datagrams to an echo server and
// times the answers; the round trips give the delay and the missing answers the loss. A TCP echo gives the throughput. The
// timing is the real clock's, taken in the process that sends: the answers come back to the same socket.

// clk times the sockets: the real clock, whatever the clock of the caller is (the kernel's delays are real).
var clk clock.Clock = &clock.Real{}

// EchoSpec says how an echo run is sent.
type EchoSpec struct {
	Host netip.Addr
	Port int
	// Count datagrams are sent, Interval apart; Settle is the wait for the last answers after the last datagram.
	Count    int
	Interval time.Duration
	Settle   time.Duration
	// Size is the datagram's size in bytes (at least 16); 0 sends 32.
	Size int
}

// EchoResult is what an echo run observed.
type EchoResult struct {
	Sent     int `json:"sent"`
	Received int `json:"received"`
	// RTTUs are the round trip times of the answers in microseconds, in the order of the datagrams they answer; a datagram that
	// was not answered has none. A duplicate answer counts once.
	RTTUs []int64 `json:"rtt_us"`
	// ElapsedMs is the length of the run from the first datagram to the end of the wait.
	ElapsedMs int64 `json:"elapsed_ms"`
}

const echoMagic = 0x63687a45 // "chzE"

// Echo sends the datagrams of spec to an echo server (it returns each datagram unchanged) from the stack n and times the
// answers. It ends early with ctx. A server that is not there (the kernel answers port unreachable) is an error when no
// datagram was answered.
func Echo(ctx context.Context, n diag.Net, spec EchoSpec) (EchoResult, error) {
	size := max(spec.Size, 16)
	if spec.Size == 0 {
		size = 32
	}
	conn, err := n.DialContext(ctx, "udp", netip.AddrPortFrom(spec.Host, uint16(spec.Port)).String())
	if err != nil {
		return EchoResult{}, err
	}
	defer func() { _ = conn.Close() }()
	sentAt := make([]atomic.Int64, spec.Count) // nanoseconds since start, 0 for not sent yet
	rtt := make([]atomic.Int64, spec.Count)    // microseconds + 1, 0 for not answered
	start := clk.Monotonic()
	var received, refused atomic.Int64
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 2048)
		for {
			k, err := conn.Read(buf)
			if err != nil {
				if errors.Is(err, syscall.ECONNREFUSED) {
					refused.Add(1)
					continue
				}
				return
			}
			if k < 16 || binary.BigEndian.Uint32(buf[0:4]) != echoMagic {
				continue
			}
			seq := int(binary.BigEndian.Uint32(buf[4:8]))
			if seq < 0 || seq >= spec.Count {
				continue
			}
			sent := sentAt[seq].Load()
			if sent == 0 {
				continue
			}
			us := ((clk.Monotonic()-start).Nanoseconds()-sent)/1000 + 1
			if rtt[seq].CompareAndSwap(0, us) {
				received.Add(1)
			}
		}
	}()
	pkt := make([]byte, size)
	binary.BigEndian.PutUint32(pkt[0:4], echoMagic)
	sent := 0
	tick := clk.NewTimer(0)
	defer tick.Stop()
send:
	for i := 0; i < spec.Count; i++ {
		select {
		case <-ctx.Done():
			break send
		case <-tick.C():
		}
		binary.BigEndian.PutUint32(pkt[4:8], uint32(i))
		sentAt[i].Store(max((clk.Monotonic() - start).Nanoseconds(), 1))
		if _, err := conn.Write(pkt); err != nil && !errors.Is(err, syscall.ECONNREFUSED) {
			break
		}
		sent++
		tick.Reset(max(start+time.Duration(i+1)*spec.Interval-clk.Monotonic(), 0))
	}
	// the answers still on their way
	settle := clk.NewTimer(spec.Settle)
	defer settle.Stop()
	poll := clk.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
wait:
	for int(received.Load()) < sent {
		select {
		case <-ctx.Done():
			break wait
		case <-settle.C():
			break wait
		case <-poll.C():
		}
	}
	_ = conn.Close()
	<-readerDone
	res := EchoResult{Sent: sent, Received: int(received.Load()), ElapsedMs: (clk.Monotonic() - start).Milliseconds()}
	for i := 0; i < sent; i++ {
		if v := rtt[i].Load(); v > 0 {
			res.RTTUs = append(res.RTTUs, v-1)
		}
	}
	if res.Received == 0 && refused.Load() > 0 {
		return res, errors.New("connection refused: nothing listens at the echo server's port")
	}
	return res, nil
}

// RateResult is what a rate run observed.
type RateResult struct {
	// Bytes is what came back during the part of the run that counts (the last three quarters: the first quarter is the
	// ramp-up of TCP and of the queues), over ElapsedMs, which gives RateBps in bit/s.
	Bytes     int64 `json:"bytes"`
	ElapsedMs int64 `json:"elapsed_ms"`
	RateBps   int64 `json:"rate_bps"`
}

// Rate sends data to a TCP echo server from the stack n for d and measures how fast it comes back: with a limit on one
// direction or both, the slower one decides.
func Rate(ctx context.Context, n diag.Net, server netip.AddrPort, d time.Duration) (RateResult, error) {
	conn, err := n.DialContext(ctx, "tcp", server.String())
	if err != nil {
		return RateResult{}, err
	}
	defer func() { _ = conn.Close() }()
	start := clk.Monotonic()
	end := start + d
	// a deadline is the kernel's, on the wall clock
	wallEnd := clk.Now().Add(d)
	_ = conn.SetDeadline(wallEnd.Add(2 * time.Second))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the sender stops with the run; a send that is stuck behind the limit ends with the deadline
		defer wg.Done()
		block := make([]byte, 16<<10)
		for clk.Monotonic() < end && ctx.Err() == nil {
			if _, err := conn.Write(block); err != nil {
				return
			}
		}
	}()
	var total, atQuarter int64
	quarter := start + d/4
	quarterSet := false
	var quarterAt, lastAt time.Duration
	buf := make([]byte, 64<<10)
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(wallEnd)
		k, err := conn.Read(buf)
		total += int64(k)
		now := clk.Monotonic()
		if !quarterSet && now >= quarter {
			quarterAt, atQuarter, quarterSet = now, total, true
		}
		if k > 0 {
			lastAt = now
		}
		if err != nil {
			if errors.Is(err, io.EOF) && total == 0 {
				return RateResult{}, errors.New("the server closed the connection")
			}
			break
		}
	}
	_ = conn.Close()
	wg.Wait()
	if !quarterSet {
		return RateResult{}, fmt.Errorf("nothing came back within %s", d)
	}
	elapsed := lastAt - quarterAt
	res := RateResult{Bytes: total - atQuarter, ElapsedMs: elapsed.Milliseconds()}
	if elapsed > 0 {
		res.RateBps = int64(float64(res.Bytes) * 8 / elapsed.Seconds())
	}
	return res, nil
}
