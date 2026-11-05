package ntpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The three kinds that break one part of the answer claim no synchronization on a host whose clock is not synchronized,
// like a normal answer, and break their part on top of it; a fault that sets the time itself (absolute) is served as it is.
func TestTheInvalidKindsAreUnsynchronizedWhileTheHostClockIsNot(t *testing.T) {
	for _, synced := range []bool{false, true} {
		for _, kind := range []model.NtpQueryLogEntryAnswer{
			model.NtpQueryLogEntryAnswerOriginMismatch, model.NtpQueryLogEntryAnswerZeroTransmit, model.NtpQueryLogEntryAnswerWrongMode,
		} {
			for _, absolute := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s synced=%v absolute=%v", kind, synced, absolute), func(t *testing.T) {
					sync := StaticSync{Synchronized: synced, EstError: time.Millisecond, MaxError: time.Second}
					s, _ := newServer(t, func(o *Options) { o.Sync = sync })
					body := fmt.Sprintf(`"answer":%q`, kind)
					if absolute {
						body += `,"absolute":"2038-01-19T03:09:07Z"`
					}
					s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.11"}, body)))
					p, e, ok := ask(t, s, dev1, Port)
					if !ok || *e.Answer != kind {
						t.Fatalf("%v %+v", ok, e)
					}
					if !synced && !absolute {
						if p.Leap != LeapUnsynchronized || p.Stratum != invalidStratum || p.RefID != [4]byte{} {
							t.Errorf("leap %d stratum %d reference id %v, want 3, 16 and none", p.Leap, p.Stratum, p.RefID)
						}
					} else if p.Leap != 0 || p.Stratum != answerStratum || p.RefID != [4]byte{10, 20, 0, 1} {
						t.Errorf("leap %d stratum %d reference id %v, want 0, 2 and the gateway", p.Leap, p.Stratum, p.RefID)
					}
					// the part the kind breaks is broken in every case
					switch kind {
					case model.NtpQueryLogEntryAnswerOriginMismatch:
						if p.Origin == 0x1234_5678_9abc_def0 {
							t.Error("the origin is the client's transmit timestamp")
						}
					case model.NtpQueryLogEntryAnswerZeroTransmit:
						if p.Transmit != 0 {
							t.Error("the transmit timestamp is set")
						}
					case model.NtpQueryLogEntryAnswerWrongMode:
						if p.Mode != ModeBroadcast {
							t.Errorf("mode %d", p.Mode)
						}
					}
				})
			}
		}
	}
}

// The reference timestamp is a whole second, and the second that wraps to 0 (the start of an era) would read "not set"
// next to a synchronized stratum: it shows the last second of the era before.
func TestTheReferenceTimestampIsNeverZeroAtTheStartOfAnEra(t *testing.T) {
	for _, era := range []int64{1, 2, 3} {
		start := time.Unix(era*eraSeconds-ntpToUnix, 500_000_000).UTC()
		for _, tc := range []struct{ name, body string }{
			{"absolute", fmt.Sprintf(`"absolute":%q`, start.Format(time.RFC3339Nano))},
			{"offset", fmt.Sprintf(`"offset":"%ds"`, int64(start.Sub(t0)/time.Second))},
		} {
			t.Run(fmt.Sprintf("era %d %s", era, tc.name), func(t *testing.T) {
				s, clk := newServer(t, nil)
				s.SetConfig(config(t, []int{Port}, fault(ovA, Port, clk.Now(), []string{"10.20.0.11"}, tc.body)))
				p, _, ok := ask(t, s, dev1, Port)
				if !ok {
					t.Fatal("no answer")
				}
				if p.Stratum != answerStratum || p.Leap != 0 {
					t.Fatalf("%+v", p)
				}
				if p.Receive.Seconds() != 0 {
					t.Fatalf("the receive timestamp %#x is not in the first second of the era", uint64(p.Receive))
				}
				if p.Reference == 0 {
					t.Fatal("the reference timestamp is 0, which means not set")
				}
				ref := p.Reference.Time(start)
				if d := start.Sub(ref); d < 0 || d > 2*time.Second {
					t.Errorf("reference %v, the answer is for %v", ref, start)
				}
			})
		}
	}
	// and one second later nothing is special
	s, clk := newServer(t, nil)
	clk.JumpWall(time.Unix(eraSeconds-ntpToUnix+1, 500_000_000).Sub(t0))
	s.SetConfig(config(t, []int{Port}))
	if p, _, _ := ask(t, s, dev1, Port); p.Reference != Timestamp(1)<<32 {
		t.Errorf("reference %#x, want the second 1", uint64(p.Reference))
	}
}

// Only a source in the client range of a network is a device of the gateway. A spoofed source (the management network, say)
// would make the reply leave as an established flow past the guards, so it is ignored and not even logged.
func TestAQueryFromOutsideEveryNetworkIsIgnored(t *testing.T) {
	raw := `{"generation":1,"listen":[123],"faults":[],"networks":[
		{"network":"` + ovA + `","gateway":"10.20.0.1","clients":"10.20.0.0/24"},
		{"network":"` + ovA + `","gateway":"10.20.0.1","clients":"10.30.0.0/24"},
		{"network":"` + ovB + `","gateway":"10.40.0.1"}]}`
	var c model.NtpServiceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	s, _ := newServer(t, nil)
	s.SetConfig(&c)
	spoofed := []string{"192.168.100.5", "10.20.1.1", "10.40.0.9", "0.0.0.0", "255.255.255.255", "::1", "2001:db8::1"}
	for _, from := range spoofed {
		reply, e := s.Process(request(4, 7), netip.MustParseAddr(from), Port, time.Time{})
		if reply != nil || e != nil {
			t.Errorf("%s was answered or logged: %v %v", from, reply, e)
		}
	}
	got := s.Counters()
	if got.Ignored != int64(len(spoofed)) || got.Queries != 0 || got.Answered != 0 || got.Unserved != 0 {
		t.Errorf("%+v", got)
	}
	// a device of a network, also one behind a router of it, is served
	for _, from := range []string{"10.20.0.5", "10.30.0.200"} {
		if _, e, ok := ask(t, s, netip.MustParseAddr(from), Port); !ok || e == nil {
			t.Errorf("%s: answered %v", from, ok)
		}
	}
	// the health probe of the host is answered whatever the configuration says
	reply, e := s.Process(Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}.Marshal(), netip.MustParseAddr("127.0.0.1"), Port, time.Time{})
	if reply == nil || e != nil {
		t.Error("the health probe was not answered")
	}
}

// A configuration without a network serves nobody.
func TestAConfigurationWithoutNetworksServesNobody(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(&model.NtpServiceConfig{Generation: 1})
	if _, e, ok := ask(t, s, dev1, Port); ok || e != nil {
		t.Errorf("answered %v, logged %v", ok, e)
	}
	if got := s.Counters(); got.Ignored != 1 || got.Queries != 0 {
		t.Errorf("%+v", got)
	}
}

type countingSink struct {
	fail  atomic.Bool
	posts atomic.Int64
	sent  atomic.Int64
}

func (c *countingSink) Post(_ context.Context, es []model.NtpQueryLogEntry) error {
	c.posts.Add(1)
	if c.fail.Load() {
		return errors.New("the API is away")
	}
	c.sent.Add(int64(len(es)))
	return nil
}

// While the sink refuses, the log tries again after a back-off that grows, not once per added entry.
func TestTheQueryLogBacksOffWhileTheSinkRefuses(t *testing.T) {
	sink := &countingSink{}
	sink.fail.Store(true)
	s, clk := newServer(t, func(o *Options) { o.Sink = sink })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunLog(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	clk.BlockUntil(1) // the log's ticker

	add := func(n int) {
		for range n {
			s.log.add(model.NtpQueryLogEntry{Client: "10.20.0.11", Port: Port})
		}
	}
	// the wake of the entries is taken, the post fails once; the many entries after it wake nothing
	add(logBatch)
	waitFor(t, "the first post", func() bool { return sink.posts.Load() == 1 })
	add(5000)
	waitFor(t, "the wake is taken", func() bool { return len(s.log.wake) == 0 })
	time.Sleep(100 * time.Millisecond)
	if n := sink.posts.Load(); n != 1 {
		t.Fatalf("%d posts while the sink refuses, want 1", n)
	}
	if got := s.Counters().LogDropped; got != 0 {
		t.Errorf("%d entries dropped", got)
	}
	// the first retry is one interval after the failure
	clk.Advance(logInterval)
	waitFor(t, "the first retry", func() bool { return sink.posts.Load() == 2 })
	// the next one waits two intervals: the tick after one interval and the entries do not start a post
	clk.Advance(logInterval)
	add(logBatch)
	time.Sleep(100 * time.Millisecond)
	if n := sink.posts.Load(); n != 2 {
		t.Fatalf("%d posts, want 2: the retry came before its back-off ended", n)
	}
	clk.Advance(logInterval)
	waitFor(t, "the second retry", func() bool { return sink.posts.Load() == 3 })

	// the sink takes the entries again: they are all sent, and the back-off is over
	sink.fail.Store(false)
	clk.Advance(logBackoffMax)
	waitFor(t, "all entries are sent", func() bool { return sink.sent.Load() == 5000+2*logBatch })
	before := sink.posts.Load()
	add(logBatch)
	waitFor(t, "a batch is sent at once", func() bool { return sink.posts.Load() > before })
}
