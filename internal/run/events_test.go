package run_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// syncBuffer is a log destination that the recorder and the test can both use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recorderBed makes a manager whose recorder is held at the first event of a burst (`release` lets it go), on a bus with
// a small buffer for its subscribers and the given replay window (0: everything). It returns the
// numbers of the burst events that are in the event file of its run, a function that sends a burst, and the log.
func recorderBed(t *testing.T, replayMax int) (h *harness, got func() []int, burst func(n int), release func(), log *syncBuffer) {
	t.Helper()
	log = &syncBuffer{}
	held := make(chan struct{})
	var once sync.Once
	enc := func(ev engine.Event) ([]byte, bool) {
		if ev.Type == "burst" && ev.Data["n"] == 0 {
			<-held
		}
		b, err := json.Marshal(map[string]any{"type": ev.Type, "n": ev.Data["n"]})
		return b, err == nil
	}
	h = newHarness(t, func(c *run.Config) {
		c.EncodeEvent = enc
		c.Log = slog.New(slog.NewTextHandler(log, nil))
		gw := c.Gateway.(*fakeGateway)
		gw.subBuffer, gw.replayMax = 8, replayMax
	})
	r := h.start(long("long", esp))
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	// what the recorder has written so far has been taken before the burst
	eventually(t, "the events of the start", func() bool {
		lines, _ := h.m.Events(r.Id)
		return len(lines) >= 2
	})
	burst = func(n int) {
		for i := 0; i < n; i++ {
			h.gw.Emit("burst", map[string]any{"run": r.Id.String(), "n": i})
		}
	}
	got = func() []int {
		lines, err := h.m.Events(r.Id)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, l := range lines {
			var e struct {
				Type string `json:"type"`
				N    int    `json:"n"`
			}
			if json.Unmarshal(l, &e) == nil && e.Type == "burst" {
				out = append(out, e.N)
			}
		}
		return out
	}
	release = func() { once.Do(func() { close(held) }) }
	t.Cleanup(release)
	return h, got, burst, release, log
}

// The bus drops a subscriber that falls behind. The recorder of the events of the runs subscribes again from the newest
// event it took and writes what came in between first, so the event file of a run does not lose its steps or its end
// (plan §3.11: writers of run files never drop records).
func TestTheRecorderReplaysWhatTheBusDroppedWhileItFellBehind(t *testing.T) {
	_, got, burst, release, log := recorderBed(t, 0)
	burst(60) // the recorder holds the first, 8 fit the buffer of the bus, the rest are dropped with the subscriber
	release()
	eventually(t, "all 60 events of the burst in the event file", func() bool { return len(got()) >= 60 })
	n := got()
	for i, v := range n {
		if v != i {
			t.Fatalf("event %d of the burst is number %d: %v", i, v, n)
		}
	}
	if len(n) != 60 {
		t.Errorf("%d events", len(n))
	}
	if s := log.String(); bytes.Contains([]byte(s), []byte("missing")) {
		t.Errorf("the log says events are missing though the bus replayed them all:\n%s", s)
	}
}

// What the bus does not keep any more is lost, and the log says how many.
func TestTheRecorderSaysWhenTheBusKeptTooLittleToReplay(t *testing.T) {
	_, got, burst, release, log := recorderBed(t, 20)
	burst(60)
	release()
	eventually(t, "the log to say events are missing", func() bool { return bytes.Contains([]byte(log.String()), []byte("missing")) })
	eventually(t, "the newest events of the burst", func() bool {
		n := got()
		return len(n) > 0 && n[len(n)-1] == 59
	})
	if n := got(); len(n) >= 60 {
		t.Errorf("%d events of the burst though the bus kept 20", len(n))
	}
}
