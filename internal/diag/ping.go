package diag

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// pingPayload is the data of an echo: 32 bytes, as ping sends. The last four are the number of the run.
var pingPayload = []byte("chaos-gateway diagnostics ping..")

// pingRuns counts the pings of the process. A raw socket gets the ICMP of every socket of the host, so a run takes
// the replies of its own echoes only: its identifier differs from that of every other run that goes at the same time,
// and its payload carries its number (a ping of another process may use the same identifier).
var pingRuns atomic.Uint32

// ping is the diagnostic `ping`: `count` echoes, 200 ms apart, and the wait for the last reply. It succeeds with one
// reply, as ping(8) does.
func (r *Runner) ping(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	addr, ok := r.target(ctx, req, timeout, res)
	if !ok {
		return
	}
	conn, datagram, err := r.net.ListenICMP()
	if err != nil {
		res.Error = ptr(err.Error())
		return
	}
	defer func() { _ = conn.Close() }()
	count := domain.DiagnosticCount(req)
	run := pingRuns.Add(1)
	id := (os.Getpid() + int(run)) & 0xffff
	data := append([]byte(nil), pingPayload...)
	binary.BigEndian.PutUint32(data[len(data)-4:], run)
	dst := &net.IPAddr{IP: addr.AsSlice()}
	// the datagram socket is addressed like a UDP socket, the raw one by the address alone
	var to net.Addr = dst
	if datagram {
		to = &net.UDPAddr{IP: dst.IP}
	}

	var mu sync.Mutex
	sent := map[int]time.Duration{}
	rtts := map[int]time.Duration{}
	var lastErr string
	replied := make(chan struct{}, count)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 1500)
		for {
			n, peer, err := conn.ReadFrom(buf)
			now := r.clk.Monotonic()
			if err != nil {
				if isTimeout(err) || errors.Is(err, net.ErrClosed) {
					return
				}
				// an ICMP error for an echo (no route, unreachable) comes back as the error of the next read
				mu.Lock()
				lastErr = failure(err, timeout)
				mu.Unlock()
				continue
			}
			if pa, ok := peer.(*net.UDPAddr); ok && !pa.IP.Equal(dst.IP) {
				continue
			} else if pi, ok := peer.(*net.IPAddr); ok && !pi.IP.Equal(dst.IP) {
				// a raw socket sees the ICMP of every host
				continue
			}
			m, err := icmp.ParseMessage(1, buf[:n])
			if err != nil {
				continue
			}
			switch m.Type {
			case ipv4.ICMPTypeEchoReply:
				e, ok := m.Body.(*icmp.Echo)
				if !ok || (!datagram && e.ID != id) || !bytes.Equal(e.Data, data) {
					continue
				}
				mu.Lock()
				if t, ok := sent[e.Seq]; ok {
					if _, dup := rtts[e.Seq]; !dup {
						rtts[e.Seq] = now - t
						replied <- struct{}{}
					}
				}
				mu.Unlock()
			case ipv4.ICMPTypeDestinationUnreachable, ipv4.ICMPTypeTimeExceeded:
				mu.Lock()
				lastErr = "ICMP " + m.Type.(ipv4.ICMPType).String()
				mu.Unlock()
			}
		}
	}()

	nsent := 0
	var sendErr error
	tick := r.clk.NewTicker(domain.PingInterval)
	defer tick.Stop()
send:
	for i := 1; i <= count; i++ {
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: id, Seq: i, Data: data}}
		b, err := msg.Marshal(nil)
		if err != nil {
			sendErr = err
			break
		}
		mu.Lock()
		sent[i] = r.clk.Monotonic()
		mu.Unlock()
		if _, err := conn.WriteTo(b, to); err != nil {
			mu.Lock()
			delete(sent, i)
			mu.Unlock()
			sendErr = err
			break
		}
		nsent++
		if i == count {
			break
		}
		select {
		case <-tick.C():
		case <-ctx.Done():
			break send
		}
	}
	// wait for the replies still on the way
	_ = conn.SetReadDeadline(r.clk.Now().Add(timeout + 50*time.Millisecond))
	wait := r.clk.NewTimer(timeout)
	defer wait.Stop()
wait:
	for {
		mu.Lock()
		got := len(rtts)
		mu.Unlock()
		if got >= nsent {
			break
		}
		select {
		case <-replied:
		case <-wait.C():
			break wait
		case <-ctx.Done():
			break wait
		}
	}
	_ = conn.Close()
	<-readDone

	if nsent == 0 && sendErr != nil {
		res.Error = ptr(failure(sendErr, timeout))
		return
	}
	mu.Lock()
	defer mu.Unlock()
	p := newOf(res.Ping)
	res.Ping = p
	p.Sent, p.Received = ptr(nsent), ptr(len(rtts))
	loss := 0.0
	if nsent > 0 {
		loss = float64(nsent-len(rtts)) / float64(nsent) * 100
	}
	p.LossPercent = ptr(float32(math.Round(loss*10) / 10))
	if len(rtts) == 0 {
		msg := fmt.Sprintf("no reply to %d echoes within %s", nsent, timeout)
		if lastErr != "" {
			msg += " (" + lastErr + ")"
		}
		res.Error = ptr(msg)
		return
	}
	min, max, sum, sumSq := time.Duration(math.MaxInt64), time.Duration(0), 0.0, 0.0
	for _, d := range rtts {
		min, max = minDur(min, d), maxDur(max, d)
		f := float64(d) / float64(time.Millisecond)
		sum += f
		sumSq += f * f
	}
	n := float64(len(rtts))
	avg := sum / n
	mdev := math.Sqrt(math.Max(sumSq/n-avg*avg, 0))
	p.RttMinMs, p.RttMaxMs = ptr(ms(min)), ptr(ms(max))
	p.RttAvgMs, p.RttMdevMs = ptr(float32(math.Round(avg*1000)/1000)), ptr(float32(math.Round(mdev*1000)/1000))
	res.Ok = true
}

func minDur(a, b time.Duration) time.Duration {
	if b < a {
		return b
	}
	return a
}

func maxDur(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}
