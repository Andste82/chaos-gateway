package ntpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func countLines(log string, parts ...string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		all := l != ""
		for _, p := range parts {
			all = all && strings.Contains(l, p)
		}
		if all {
			n++
		}
	}
	return n
}

func tokenFile(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTheClientAsksForTheConfigurationAndPostsTheLog(t *testing.T) {
	var posted atomic.Value
	var auth atomic.Value
	cfgJSON := `{"generation":7,"listen":[123,12301],"networks":[{"network":"` + ovB + `","gateway":"10.20.0.1"}],"faults":[` +
		fault(ovA, 12301, t0, []string{"10.20.0.11"}, `"offset":"+1h"`) + `]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/api/v1/internal/ntp/config" && r.URL.Query().Get("after") == "0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(cfgJSON))
		case r.URL.Path == "/api/v1/internal/ntp/config":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/internal/ntp/queries" && r.Method == http.MethodPost:
			var es []model.NtpQueryLogEntry
			if err := json.NewDecoder(r.Body).Decode(&es); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			posted.Store(es)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := &APIClient{Base: ts.URL, TokenFile: tokenFile(t), Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	cfg, err := c.Config(context.Background(), 0)
	if err != nil || cfg == nil || cfg.Generation != 7 || len(cfg.Faults) != 1 || cfg.Faults[0].Port != 12301 || len(cfg.Listen) != 2 {
		t.Fatalf("%+v %v", cfg, err)
	}
	if auth.Load() != "Bearer svc" {
		t.Errorf("authorization %v", auth.Load())
	}
	if cfg, err = c.Config(context.Background(), 7); err != nil || cfg != nil {
		t.Errorf("a long poll with nothing new: %+v %v", cfg, err)
	}
	kind := model.NtsKe
	if err := c.Post(context.Background(), []model.NtpQueryLogEntry{{Time: t0, Client: "10.20.0.11", Port: 4460, Kind: &kind}}); err != nil {
		t.Fatal(err)
	}
	if es, _ := posted.Load().([]model.NtpQueryLogEntry); len(es) != 1 || es[0].Port != 4460 {
		t.Errorf("posted %+v", es)
	}
	bad := &APIClient{Base: ts.URL + "/nowhere", TokenFile: tokenFile(t)}
	defer bad.CloseIdleConnections()
	if err := bad.Post(context.Background(), nil); err == nil {
		t.Error("a refused post is no error")
	}
}

// As the DNS proxy (issue #57 F6): before the setup is applied the API cannot be reached, and the server warns once with a
// hint, again only when the error changes, and says once that it can fetch the configuration again.
func TestTheServerWarnsOnceWhileTheAPICannotBeReached(t *testing.T) {
	var phase atomic.Value
	phase.Store("not-yet")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := phase.Load().(string); p {
		case "ok":
			if r.URL.Query().Get("after") != "0" {
				<-r.Context().Done() // the long poll of a newer generation
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":1,"networks":[],"listen":[123],"faults":[]}`))
		default:
			http.Error(w, p, http.StatusServiceUnavailable)
		}
	}))
	defer ts.Close()
	var out syncBuffer
	log := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clk := clock.NewFake(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	c := &APIClient{Base: ts.URL, TokenFile: tokenFile(t), Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	s := New(Options{Clock: clk, Sync: Synchronized})
	armed := clk.Pending()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Follow(ctx, c, log); close(done) }()
	retry := func(next string) {
		clk.BlockUntil(armed + 1) // Follow waits for its pause
		phase.Store(next)
		clk.Advance(time.Minute) // longer than the longest pause
	}
	for range 4 {
		retry("not-yet")
	}
	retry("still-not")
	retry("still-not")
	retry("ok")
	waitFor(t, "a configuration was fetched", func() bool { return s.Config() != nil })
	waitFor(t, "the recovery is logged", func() bool { return strings.Contains(out.String(), "the NTP configuration is in use") })
	cancel()
	<-done
	got := out.String()
	if n := countLines(got, "level=WARN", "not-yet", "hint="); n != 1 {
		t.Errorf("%d warnings of the first error, want 1 with a hint:\n%s", n, got)
	}
	if n := countLines(got, "level=WARN", "still-not"); n != 1 {
		t.Errorf("%d warnings of the changed error, want 1:\n%s", n, got)
	}
	if n := countLines(got, "level=DEBUG", "cannot fetch the NTP configuration"); n != 5 {
		t.Errorf("%d debug lines, want one per repeated attempt (5):\n%s", n, got)
	}
	if n := countLines(got, "level=INFO", "can be fetched again"); n != 1 {
		t.Errorf("the recovery is said %d times, want once:\n%s", n, got)
	}
}

// A restart of the gateway drops the overlays, and with them the time faults, while the server keeps the configuration in use
// as long as the API cannot be reached: the pause between two attempts stays at followPauseInUse once one has been in use
// (P5-AUDIT-14), so the configuration without the dropped fault is in use within it.
func TestTheServerAsksAgainWithinTwoSecondsOnceAConfigurationWasInUse(t *testing.T) {
	var phase atomic.Value
	phase.Store("ok")
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch phase.Load().(string) {
		case "ok":
			if r.URL.Query().Get("after") != "0" {
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":1,"networks":[],"listen":[123],"faults":[` + fault(ovA, 123, t0, []string{"10.20.0.11"}, `"offset":"+1h"`) + `]}`))
		case "back":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":2,"networks":[],"listen":[123],"faults":[]}`))
			phase.Store("idle")
		case "idle":
			<-r.Context().Done()
		default:
			http.Error(w, "restarting", http.StatusServiceUnavailable)
		}
	}))
	defer ts.Close()
	clk := clock.NewFake(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	c := &APIClient{Base: ts.URL, TokenFile: tokenFile(t), Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	s := New(Options{Clock: clk, Sync: Synchronized})
	armed := clk.Pending()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	faultsInUse := func() int {
		if pc := s.parsed.Load(); pc != nil {
			return len(pc.faults)
		}
		return -1
	}
	go s.Follow(ctx, c, slog.New(slog.DiscardHandler))
	waitFor(t, "the first configuration is not in use", func() bool { return faultsInUse() == 1 })
	phase.Store("down")
	c.CloseIdleConnections()
	ts.CloseClientConnections() // the long poll fails, as the API's container stops
	for i := range 8 {
		clk.BlockUntil(armed + 1)
		before := requests.Load()
		if i == 7 {
			phase.Store("back")
		}
		clk.Advance(followPauseInUse)
		waitFor(t, fmt.Sprintf("attempt %d did not come within %v of the one before", i+2, followPauseInUse), func() bool { return requests.Load() > before })
	}
	waitFor(t, "the configuration without the dropped fault is not in use", func() bool { return faultsInUse() == 0 })
}
