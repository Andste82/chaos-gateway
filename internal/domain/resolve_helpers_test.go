package domain

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// testWorld is a configuration (normalized) and the overlays created so far.
type testWorld struct {
	t        *testing.T
	cfg      *model.Configuration
	overlays []model.Overlay
	next     int
}

// newTestWorld starts from the example configuration, with its configured faults removed unless
// keepFaults is set: most tests build their own faults to keep the cases readable.
func newTestWorld(t *testing.T, keepFaults bool) *testWorld {
	t.Helper()
	cfg, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatalf("normalize: %v", errs)
	}
	if !keepFaults {
		empty := map[string]model.ConfigFault{}
		cfg.Faults = &empty
	}
	return &testWorld{t: t, cfg: cfg}
}

// configFault adds a configured fault (YAML body) created at t0+age.
func (w *testWorld) configFault(id, body string, age time.Duration) {
	w.t.Helper()
	var f model.ConfigFault
	doc, err := ParseDocument([]byte(body), FormatYAML)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := decodeInto(doc, &f); err != nil {
		w.t.Fatal(err)
	}
	created := t0.Add(age)
	f.CreatedAt = &created
	// references in the body are names: normalize by validating the whole configuration
	(*w.cfg.Faults)[id] = f
	norm, errs := Normalize(w.cfg)
	if len(errs) != 0 {
		w.t.Fatalf("normalize: %v", errs)
	}
	w.cfg = norm
}

// overlay creates an overlay from a YAML request body, updated at t0+age.
func (w *testWorld) overlay(body string, age time.Duration) model.Overlay {
	w.t.Helper()
	req, err := DecodeOverlayRequest([]byte(body), FormatYAML)
	if err != nil {
		w.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := ValidateOverlay(w.cfg, req)
	if len(errs) != 0 {
		w.t.Fatalf("%s: %v", body, errs)
	}
	w.next++
	id := uuid.MustParse("00000000-0000-4000-8000-" + padHex(w.next))
	o, err := NewOverlay(*norm, model.Owner{Type: "user", Id: "admin"}, id, t0.Add(age))
	if err != nil {
		w.t.Fatal(err)
	}
	w.overlays = append(w.overlays, o)
	return o
}

func padHex(n int) string {
	const digits = "0123456789abcdef"
	out := []byte("000000000000")
	for i := len(out) - 1; i >= 0 && n > 0; i-- {
		out[i] = digits[n%16]
		n /= 16
	}
	return string(out)
}

func (w *testWorld) world() *World { return NewWorld(w.cfg, w.overlays) }

// A is the device of the examples: esp32-42 in the network IoT.
var subjectA = Subject{Device: idESP, IP: netip.MustParseAddr("10.10.0.42")}

// queries
func toServer(proto string, port int) Query {
	return Query{
		Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10"),
		DestNames: []string{"broker.example.com"}, Protocol: proto, Port: port,
	}
}

func mustWinner(t *testing.T, results []FamilyResult, family string) Candidate {
	t.Helper()
	w := Winner(results, family)
	if w == nil {
		t.Fatalf("no winner for family %s in %+v", family, results)
	}
	return *w
}

func latencyOf(c Candidate) string {
	if c.Impairment == nil {
		return ""
	}
	n := convertNetem(*c.Impairment)
	return deref(n.Latency)
}

func convertNetem(f model.FaultBody) model.NetemParams { return convert[model.NetemParams](f) }

func lossOf(c Candidate) string { return deref(convertNetem(*c.Impairment).Loss) }

func overriddenReason(t *testing.T, r FamilyResult, id string) string {
	t.Helper()
	for _, o := range r.Overridden {
		if o.ID == id {
			return o.Reason
		}
	}
	t.Fatalf("%s is not among the overridden candidates: %+v", id, r.Overridden)
	return ""
}

func familyResult(t *testing.T, results []FamilyResult, family string) FamilyResult {
	t.Helper()
	for _, r := range results {
		if r.Family == family {
			return r
		}
	}
	t.Fatalf("no result for family %s", family)
	return FamilyResult{}
}

type model_scope = model.Scope

type uuidT = uuid.UUID

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }

func ptrTo[T any](v T) *T { return &v }
