package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// stream is an open Server-Sent Events connection.
type stream struct {
	t      *testing.T
	cancel context.CancelFunc
	lines  chan string
	status int
	header http.Header
	// doc validates each received event against the spec's Event schema (M5-19): the contract
	// harness skips SSE bodies entirely otherwise, since they are not a single JSON response.
	doc *openapi3.T
}

type sseEvent struct {
	ID, Event string
	Data      map[string]any
}

func (g *gw) openStream(lastID, query string) *stream {
	g.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.ts.URL+"/api/v1/events"+query, nil)
	req.Header.Set("Authorization", "Bearer "+g.token)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		g.t.Fatal(err)
	}
	s := &stream{t: g.t, cancel: cancel, lines: make(chan string, 1000), status: res.StatusCode, header: res.Header, doc: g.doc}
	go func() {
		defer func() { _ = res.Body.Close(); close(s.lines) }()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 4<<20)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
	}()
	g.t.Cleanup(cancel)
	return s
}

// next returns the next event, skipping comments.
func (s *stream) next(d time.Duration) (sseEvent, bool) {
	var ev sseEvent
	deadline := time.After(d)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return ev, false
			}
			switch {
			case strings.HasPrefix(l, "id: "):
				ev.ID = strings.TrimPrefix(l, "id: ")
			case strings.HasPrefix(l, "event: "):
				ev.Event = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &ev.Data)
			case l == "" && ev.Event != "":
				s.checkEvent(ev)
				return ev, true
			}
		case <-deadline:
			return ev, false
		}
	}
}

// checkEvent validates a received SSE event against the spec's Event schema (M5-19).
func (s *stream) checkEvent(ev sseEvent) {
	s.t.Helper()
	schema := s.doc.Components.Schemas["Event"]
	if schema == nil {
		s.t.Fatalf("the spec has no Event schema")
	}
	if err := schema.Value.VisitJSON(ev.Data); err != nil {
		s.t.Errorf("SSE event %q does not match Event: %v\n%+v", ev.Event, err, ev.Data)
	}
}

// until reads events until one of the type arrives.
func (s *stream) until(typ string, d time.Duration) (sseEvent, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ev, ok := s.next(time.Until(deadline))
		if !ok {
			return ev, false
		}
		if ev.Event == typ {
			return ev, true
		}
	}
	return sseEvent{}, false
}

func TestEventsAreStreamedWithIds(t *testing.T) {
	g := ready(t)
	s := g.openStream("", "")
	if s.status != 200 || !strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %v", s.status, s.header)
	}
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatal(r.Status)
	}
	created, ok := s.until("revision_created", 5*time.Second)
	if !ok || created.Data["data"].(map[string]any)["revision"] != float64(id) || created.ID == "" {
		t.Fatalf("%+v", created)
	}
	// M5-06: the event carries who caused it and what it is about
	actor, ok := created.Data["actor"].(map[string]any)
	if !ok || actor["type"] != "token" || actor["id"] == "" {
		t.Errorf("no actor on revision_created: %+v", created.Data)
	}
	subject, ok := created.Data["subject"].(map[string]any)
	if !ok || subject["kind"] != "revision" || subject["id"] != itoa(id) {
		t.Errorf("no subject on revision_created: %+v", created.Data)
	}
	if _, ok := created.Data["data"].(map[string]any)["actor"]; ok {
		t.Errorf("the actor is duplicated inside data: %+v", created.Data)
	}
	applied, ok := s.until("applied", 5*time.Second)
	if !ok || applied.Data["type"] != "applied" || applied.Data["id"] != applied.ID || applied.Data["time"] == nil {
		t.Fatalf("%+v", applied)
	}
	if applied.Data["generation"] == nil {
		t.Errorf("the applied event names the generation: %v", applied.Data)
	}
}

func TestAReconnectingClientGetsTheMissedEvents(t *testing.T) {
	g := ready(t)
	s := g.openStream("", "")
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	first, ok := s.until("revision_created", 5*time.Second)
	if !ok {
		t.Fatal("no event")
	}
	s.cancel()
	// events while the client is away
	g.apply(id)
	id2 := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}})
	g.apply(id2)

	again := g.openStream(first.ID, "")
	var types []string
	for {
		ev, ok := again.next(1 * time.Second)
		if !ok {
			break
		}
		types = append(types, ev.Event)
	}
	count := func(t string) int {
		n := 0
		for _, x := range types {
			if x == t {
				n++
			}
		}
		return n
	}
	if count("applied") < 2 || count("revision_applied") != 2 || count("revision_created") != 1 {
		t.Errorf("replayed %v", types)
	}
	// the ids go on without a gap or a repeat
	if r := g.do("GET", "/events", nil, map[string]string{"Last-Event-ID": "banana"}, nil); r.Status != 400 {
		t.Errorf("a garbled Last-Event-ID: %d", r.Status)
	}
}

// M5-02 test: a Last-Event-ID from a boot other than the current one gets a synthetic
// events_lost event (reason "restart") instead of a silent, partial or empty replay.
func TestAnIdFromAnotherBootGetsEventsLost(t *testing.T) {
	g := ready(t)
	s := g.openStream("another-boot-id-1", "")
	lost, ok := s.until("events_lost", 5*time.Second)
	if !ok || lost.Data["data"].(map[string]any)["reason"] != "restart" {
		t.Fatalf("%+v", lost)
	}
}

// M5-02 test: a Last-Event-ID from the current boot, but older than what the replay buffer still
// holds, also gets events_lost, with reason "expired".
func TestAStaleLastEventIDGetsEventsLost(t *testing.T) {
	saved := engine.ReplayMax
	engine.ReplayMax = 2
	t.Cleanup(func() { engine.ReplayMax = saved })

	g := ready(t)
	s := g.openStream("", "")
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	first, ok := s.until("revision_created", 5*time.Second)
	if !ok {
		t.Fatal("no event")
	}
	s.cancel()
	// enough further events to evict the one above from the replay buffer (ReplayMax above)
	g.apply(id)
	id2 := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}})
	g.apply(id2)
	id3 := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.13"}})
	g.apply(id3)

	again := g.openStream(first.ID, "")
	lost, ok := again.until("events_lost", 5*time.Second)
	if !ok || lost.Data["data"].(map[string]any)["reason"] != "expired" {
		t.Fatalf("%+v", lost)
	}
}

func TestEventsCanBeFilteredByType(t *testing.T) {
	g := ready(t)
	s := g.openStream("", "?types=revision_applied,revision_confirmed")
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	g.apply(id)
	ev, ok := s.next(5 * time.Second)
	if !ok || ev.Event != "revision_applied" {
		t.Fatalf("%+v", ev)
	}
	for {
		ev, ok := s.next(500 * time.Millisecond)
		if !ok {
			break
		}
		if ev.Event != "revision_applied" && ev.Event != "revision_confirmed" {
			t.Errorf("an event of another type: %s", ev.Event)
		}
	}
	if r := g.do("GET", "/events?types=bogus", nil, nil, nil); r.Status != 400 && r.Status != 422 {
		t.Errorf("an unknown type: %d", r.Status)
	}
}

func TestAKeepaliveCommentIsSentWhenIdle(t *testing.T) {
	defer api.SetStreamTimings(5*time.Second, 100*time.Millisecond)()
	g := ready(t)
	s := g.openStream("", "")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case l := <-s.lines:
			if l == ": keepalive" {
				return
			}
		case <-deadline:
			t.Fatal("no keepalive")
		}
	}
}

func TestASubscriberThatStopsReadingIsDisconnectedWithoutDelayingOthers(t *testing.T) {
	defer api.SetStreamTimings(500*time.Millisecond, time.Minute)()
	g := ready(t)

	// the slow one: sends the request and never reads
	conn, err := net.Dial("tcp", strings.TrimPrefix(g.ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Fprintf(conn, "GET /api/v1/events HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nAccept: text/event-stream\r\n\r\n", g.token)
	time.Sleep(200 * time.Millisecond)

	// a normal one
	fast := g.openStream("", "")
	if _, ok := fast.next(200 * time.Millisecond); ok {
		t.Fatal("an event before any")
	}

	// a burst of large events: far more than the buffers of the connection and the bus hold
	const n = 200
	big := strings.Repeat("x", 256*1024)
	start := time.Now()
	for i := 0; i < n; i++ {
		g.e.Emit("network_degraded", map[string]any{"network": "n", "reason": big, "i": i})
		time.Sleep(3 * time.Millisecond) // a reader that is not starved by the burst must keep up
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("publishing %d events took %v: a slow subscriber delays the publisher", n, d)
	}
	// the fast one gets every event, in order
	var wg sync.WaitGroup
	wg.Add(1)
	got := 0
	go func() {
		defer wg.Done()
		for got < n {
			ev, ok := fast.next(10 * time.Second)
			if !ok {
				return
			}
			if ev.Event == "network_degraded" {
				got++
			}
		}
	}()
	wg.Wait()
	if got != n {
		t.Errorf("the fast subscriber got %d of %d events", got, n)
	}
	// the slow connection is closed by the server once a write has been stuck for the timeout:
	// wait for that, then read what is buffered, then EOF
	time.Sleep(2 * time.Second)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(conn)
	for {
		if _, err := r.ReadByte(); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatal("the connection of a client that does not read stays open")
			}
			break
		}
	}
}

// CC-01: every event type the engine's own Event* constants can publish must be in the spec's
// EventType enum, so a client never sees a type it cannot decode.
func TestEveryEngineEventTypeIsInTheSpec(t *testing.T) {
	types := []string{
		engine.EventApplied,
		engine.EventApplyFailed,
		engine.EventRolledBack,
		engine.EventConfirmPending,
		engine.EventConfirmed,
		engine.EventNetworkDegraded,
		engine.EventNetworkRecovered,
		engine.EventUplinkChanged,
		engine.EventObservedChanged,
		engine.EventDeviceDiscovered,
		engine.EventDeviceOnline,
		engine.EventDeviceOffline,
		engine.EventDeviceIdentityChanged,
		engine.EventDHCPLease,
		engine.EventRoutingChanged,
		engine.EventRoutingRoutesChanged,
		engine.EventPeerOnline,
		engine.EventPeerOffline,
	}
	for _, typ := range types {
		if !model.EventType(typ).Valid() {
			t.Errorf("%q is not in the spec's EventType enum", typ)
		}
	}
}
