package probe

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
)

// echoServer is a UDP echo on the loopback that answers after delay and drops the share loss of the datagrams it gets.
func echoServer(t *testing.T, delay time.Duration, loss float64) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	rng := rand.New(rand.NewPCG(1, 2))
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			mu.Lock()
			drop := rng.Float64() < loss
			mu.Unlock()
			if drop {
				continue
			}
			pkt := append([]byte(nil), buf[:n]...)
			time.AfterFunc(delay, func() { _, _ = pc.WriteTo(pkt, from) })
		}
	}()
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

func TestEchoTimesTheRoundTripsOfAnEchoServer(t *testing.T) {
	srv := echoServer(t, 20*time.Millisecond, 0)
	res, err := Echo(context.Background(), diag.HostNet{}, EchoSpec{Host: srv.Addr(), Port: int(srv.Port()), Count: 100, Interval: 2 * time.Millisecond, Settle: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 100 || res.Received != 100 || len(res.RTTUs) != 100 {
		t.Fatalf("%+v", res)
	}
	slices.Sort(res.RTTUs)
	if med := res.RTTUs[50]; med < 20_000 || med > 60_000 {
		t.Errorf("the median round trip of a 20 ms echo is %d us", med)
	}
	if res.ElapsedMs < 200 {
		t.Errorf("100 datagrams 2 ms apart took %d ms", res.ElapsedMs)
	}
}

func TestEchoCountsTheDatagramsThatAreNotAnswered(t *testing.T) {
	srv := echoServer(t, 0, 0.3)
	res, err := Echo(context.Background(), diag.HostNet{}, EchoSpec{Host: srv.Addr(), Port: int(srv.Port()), Count: 1000, Interval: 200 * time.Microsecond, Settle: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	loss := 1 - float64(res.Received)/float64(res.Sent)
	if res.Sent != 1000 || loss < 0.2 || loss > 0.4 || len(res.RTTUs) != res.Received {
		t.Errorf("sent %d received %d (loss %.2f), %d round trips", res.Sent, res.Received, loss, len(res.RTTUs))
	}
}

func TestEchoToAClosedPortIsAnError(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()
	_, err = Echo(context.Background(), diag.HostNet{}, EchoSpec{Host: netip.MustParseAddr("127.0.0.1"), Port: port, Count: 20, Interval: time.Millisecond, Settle: 200 * time.Millisecond})
	if err == nil {
		t.Fatal("a closed port is no echo server")
	}
}

func TestEchoEndsWithItsContext(t *testing.T) {
	srv := echoServer(t, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Echo(ctx, diag.HostNet{}, EchoSpec{Host: srv.Addr(), Port: int(srv.Port()), Count: 10000, Interval: 10 * time.Millisecond, Settle: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second || res.Sent >= 10000 || res.Sent == 0 {
		t.Errorf("sent %d in %v", res.Sent, time.Since(start))
	}
}

// tcpEcho echoes what it reads at no more than rate bytes per second (0: as fast as it can).
func tcpEcho(t *testing.T, rate int) netip.AddrPort {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				start, sent := time.Now(), 0
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
						sent += n
						if rate > 0 {
							if wait := time.Duration(float64(sent)/float64(rate)*float64(time.Second)) - time.Since(start); wait > 0 {
								time.Sleep(wait)
							}
						}
					}
					if err != nil {
						if !errors.Is(err, io.EOF) {
							return
						}
						return
					}
				}
			}()
		}
	}()
	return netip.MustParseAddrPort(l.Addr().String())
}

func TestRateMeasuresTheThroughputOfATCPEcho(t *testing.T) {
	// 250 kB/s = 2 Mbit/s
	srv := tcpEcho(t, 250_000)
	res, err := Rate(context.Background(), diag.HostNet{}, srv, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.RateBps < 1_700_000 || res.RateBps > 2_400_000 {
		t.Errorf("a 2 Mbit/s echo measures %d bit/s (%+v)", res.RateBps, res)
	}
}

func TestRateOfAServerThatIsNotThereIsAnError(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddrPort(l.Addr().String())
	_ = l.Close()
	if _, err := Rate(context.Background(), diag.HostNet{}, addr, time.Second); err == nil {
		t.Error("nothing listens")
	}
}
