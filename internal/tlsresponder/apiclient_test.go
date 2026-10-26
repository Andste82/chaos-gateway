package tlsresponder

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

// fakeAPI serves the three operations of the internal API the responder uses and records what it was sent.
type fakeAPI struct {
	mu       sync.Mutex
	auth     []string
	config   func(after string) (int, any)
	resolve  func(model.TlsResolveRequest) (int, any)
	posted   [][]model.TlsHandshakeReport
	polled   []string
	postCode int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *APIClient) {
	t.Helper()
	f := &fakeAPI{postCode: http.StatusNoContent}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, body any) {
		if body != nil {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(code)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	mux.HandleFunc("/api/v1/internal/tls/config", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.polled = append(f.polled, r.URL.Query().Get("after"))
		fn := f.config
		f.mu.Unlock()
		code, body := fn(r.URL.Query().Get("after"))
		reply(w, code, body)
	})
	mux.HandleFunc("/api/v1/internal/tls/resolve", func(w http.ResponseWriter, r *http.Request) {
		var req model.TlsResolveRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		fn := f.resolve
		f.mu.Unlock()
		code, body := fn(req)
		reply(w, code, body)
	})
	mux.HandleFunc("/api/v1/internal/tls/handshakes", func(w http.ResponseWriter, r *http.Request) {
		var batch []model.TlsHandshakeReport
		_ = json.NewDecoder(r.Body).Decode(&batch)
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.posted = append(f.posted, batch)
		code := f.postCode
		f.mu.Unlock()
		w.WriteHeader(code)
		if code != http.StatusNoContent {
			_, _ = io.WriteString(w, "no")
		}
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("service-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, &APIClient{Base: ts.URL, TokenFile: token, Wait: 5 * time.Second}
}

func TestTheClientAsksForTheDecisionOfAConnectionWithTheServiceToken(t *testing.T) {
	f, c := newFakeAPI(t)
	var got model.TlsResolveRequest
	f.resolve = func(r model.TlsResolveRequest) (int, any) {
		got = r
		return 200, map[string]any{"generation": 7, "overlay": "11111111-1111-4111-8111-111111111111", "case": map[string]any{"case": "expired", "ports": []int{8883}},
			"expected": "reject", "device": "22222222-2222-4222-8222-222222222222", "trusts_test_ca": true, "name": "from-dns.example.com"}
	}
	d, err := c.Resolve(context.Background(), ConnInfo{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883"), SNI: "broker.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Client != "10.10.0.31" || got.OriginalDst != "203.0.113.10:8883" || got.Sni == nil || *got.Sni != "broker.example.com" {
		t.Errorf("the request: %+v", got)
	}
	if d.Generation != 7 || d.Overlay != "11111111-1111-4111-8111-111111111111" || d.Case == nil || d.Case.Case != model.TlsCaseCaseExpired || d.Expected != "reject" ||
		d.Device != "22222222-2222-4222-8222-222222222222" || !d.TrustsTestCA || d.Name != "from-dns.example.com" || d.Passthrough {
		t.Errorf("%+v", d)
	}
	if len(f.auth) != 1 || f.auth[0] != "Bearer service-token" {
		t.Errorf("the token: %v", f.auth)
	}
	// no server name: none is sent; no case and a connection that may pass through
	f.resolve = func(r model.TlsResolveRequest) (int, any) {
		got = r
		return 200, map[string]any{"generation": 8, "passthrough": true}
	}
	d, err = c.Resolve(context.Background(), ConnInfo{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:443")})
	if err != nil || got.Sni != nil || d.Case != nil || !d.Passthrough || d.Generation != 8 {
		t.Errorf("%+v %v (request %+v)", d, err, got)
	}
	// an error of the API is an error, with what it said
	f.resolve = func(model.TlsResolveRequest) (int, any) { return 503, map[string]any{"code": "unavailable"} }
	if _, err := c.Resolve(context.Background(), ConnInfo{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:443")}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("%v", err)
	}
}

func TestTheClientFollowsTheConfigurationByLongPollAndPostsTheReports(t *testing.T) {
	f, c := newFakeAPI(t)
	var polls atomic.Int32
	f.config = func(after string) (int, any) {
		if polls.Add(1) == 1 {
			return 200, map[string]any{"generation": 5, "cases": []any{map[string]any{"case": "expired", "overlay": "11111111-1111-4111-8111-111111111111", "sources": []string{"10.10.0.31/32"}, "expected": "reject"}}}
		}
		return 204, nil
	}
	cfg, err := c.Config(context.Background(), 0)
	if err != nil || cfg == nil || cfg.Generation != 5 || len(cfg.Cases) != 1 || string(cfg.Cases[0].Expected) != "reject" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if cfg, err := c.Config(context.Background(), 5); err != nil || cfg != nil {
		t.Errorf("an unchanged configuration is nil: %+v %v", cfg, err)
	}
	if f.polled[0] != "0" || f.polled[1] != "5" {
		t.Errorf("after: %v", f.polled)
	}
	now := time.Now().UTC()
	if err := c.Post(context.Background(), []model.TlsHandshakeReport{{Time: now, Client: "10.10.0.31", OriginalDst: "203.0.113.10:8883", Outcome: model.TlsHandshakeReportOutcomeRejected}}); err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 1 || len(f.posted[0]) != 1 || f.posted[0][0].Outcome != model.TlsHandshakeReportOutcomeRejected {
		t.Errorf("%+v", f.posted)
	}
	f.mu.Lock()
	f.postCode = http.StatusInternalServerError
	f.mu.Unlock()
	if err := c.Post(context.Background(), []model.TlsHandshakeReport{{Time: now, Client: "10.10.0.31", OriginalDst: "x", Outcome: "rejected"}}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("%v", err)
	}
}

// Follow gives every new configuration to the server and goes on after a failure.
func TestFollowGivesEveryNewConfigurationToTheServerAndGoesOnAfterAFailure(t *testing.T) {
	f, c := newFakeAPI(t)
	var polls atomic.Int32
	f.config = func(after string) (int, any) {
		switch polls.Add(1) {
		case 1:
			return 500, map[string]any{"code": "internal"}
		case 2:
			return 200, map[string]any{"generation": 3, "cases": []any{}}
		case 3:
			return 200, map[string]any{"generation": 4, "cases": []any{}}
		}
		time.Sleep(50 * time.Millisecond)
		return 204, nil
	}
	srv := New(Options{Log: slog.New(slog.DiscardHandler), Issuer: &Issuer{}, Resolver: &fakeResolver{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Follow(ctx, c, slog.New(slog.DiscardHandler)); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for srv.Config() == nil || srv.Config().Generation != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("the configuration in use: %+v", srv.Config())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.polled) < 3 || f.polled[1] != "0" || f.polled[2] != "3" {
		t.Errorf("the generations asked for after each answer: %v", f.polled)
	}
}

// A batch of handshake reports the API refuses for its content (a 4xx other than 408 and 429) would be refused again on every
// try and hold back every later report: it is dropped and counted, and the next report goes through. A batch that may pass
// later (a 5xx, 429) stays and is sent again (P5-AUDIT-18).
func TestABatchOfReportsTheAPIRefusesIsDroppedAndTheNextOneGoesThrough(t *testing.T) {
	f, c := newFakeAPI(t)
	rep := newReporter(c, &clock.Real{}, slog.New(slog.DiscardHandler))
	report := func(client string) model.TlsHandshakeReport {
		return model.TlsHandshakeReport{Time: time.Now().UTC(), Client: client, OriginalDst: "203.0.113.10:8883", Outcome: model.TlsHandshakeReportOutcomeRejected}
	}
	setCode := func(code int) {
		f.mu.Lock()
		f.postCode = code
		f.mu.Unlock()
	}
	posted := func() [][]model.TlsHandshakeReport {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([][]model.TlsHandshakeReport(nil), f.posted...)
	}
	pending := func() int {
		rep.mu.Lock()
		defer rep.mu.Unlock()
		return len(rep.pending)
	}
	ctx := context.Background()

	setCode(http.StatusUnprocessableEntity)
	rep.add(report("10.10.0.31"))
	rep.flush(ctx)
	if got := posted(); len(got) != 1 || pending() != 0 || rep.Refused() != 1 {
		t.Fatalf("the refused batch: posted %d, pending %d, refused %d", len(got), pending(), rep.Refused())
	}
	setCode(http.StatusNoContent)
	rep.add(report("10.10.0.32"))
	rep.flush(ctx)
	if got := posted(); len(got) != 2 || len(got[1]) != 1 || got[1][0].Client != "10.10.0.32" || pending() != 0 {
		t.Fatalf("the next report does not go through alone: %+v, pending %d", got, pending())
	}

	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusRequestTimeout} {
		setCode(code)
		rep.add(report("10.10.0.33"))
		rep.flush(ctx)
		if pending() != 1 || rep.Refused() != 1 {
			t.Fatalf("%d: pending %d, refused %d, want the batch kept", code, pending(), rep.Refused())
		}
		setCode(http.StatusNoContent)
		rep.flush(ctx)
		got := posted()
		if last := got[len(got)-1]; pending() != 0 || len(last) != 1 || last[0].Client != "10.10.0.33" {
			t.Fatalf("%d: the kept batch was not sent again: %+v, pending %d", code, last, pending())
		}
	}
}
