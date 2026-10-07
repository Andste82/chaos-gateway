package compiler

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	devESP42 = "1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a"
	devESP43 = "2e3f4a5b-6c7d-4e8f-9a0b-1c2d3e4f5a6b"
	devLab   = "3a4b5c6d-7e8f-4a9b-8c0d-1e2f3a4b5c6d"
	grpSens  = "3f4a5b6c-7d8e-4f9a-0b1c-2d3e4f5a6b7c"
)

var tFault0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// faultWorld is the fault fixture: the routed gateway with three devices and a group, the
// identity of the devices (their current addresses) and the overlays created so far.
type faultWorld struct {
	t        *testing.T
	cfg      *model.Configuration
	id       domain.Identity
	overlays []model.Overlay
	next     int
	ids      map[string]int
}

func newFaultWorld(t *testing.T) *faultWorld { return newFaultWorldFile(t, "faults.yaml") }

func newFaultWorldFile(t *testing.T, name string) *faultWorld {
	t.Helper()
	cfg, errs := domain.Normalize(loadConfig(t, name))
	if len(errs) != 0 {
		t.Fatalf("normalize: %v", errs)
	}
	w := &faultWorld{t: t, cfg: cfg}
	w.setAddrs(map[string][]string{devESP42: {"10.10.0.42"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	return w
}

// setAddrs replaces the identity: the current addresses of each device.
func (w *faultWorld) setAddrs(addrs map[string][]string) {
	w.id = domain.Identity{Addresses: map[string][]netip.Addr{}, Owner: map[netip.Addr]string{}}
	for dev, as := range addrs {
		for _, a := range as {
			ip := netip.MustParseAddr(a)
			w.id.Addresses[dev] = append(w.id.Addresses[dev], ip)
			w.id.Owner[ip] = dev
		}
	}
}

// addConfigFault adds a configured fault (a YAML body) created at tFault0+age.
func (w *faultWorld) addConfigFault(id, body string, age time.Duration) {
	w.t.Helper()
	doc, err := domain.ParseDocument([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	var f model.ConfigFault
	if err := json.Unmarshal(raw, &f); err != nil {
		w.t.Fatal(err)
	}
	created := tFault0.Add(age)
	f.CreatedAt = &created
	if w.cfg.Faults == nil {
		m := map[string]model.ConfigFault{}
		w.cfg.Faults = &m
	}
	(*w.cfg.Faults)[id] = f
	norm, errs := domain.Normalize(w.cfg)
	if len(errs) != 0 {
		w.t.Fatalf("normalize: %v", errs)
	}
	w.cfg = norm
}

// overlay creates an overlay from a YAML request body, updated at tFault0+age.
func (w *faultWorld) overlay(body string, age time.Duration) model.Overlay {
	w.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := domain.ValidateOverlay(w.cfg, req)
	if len(errs) != 0 {
		w.t.Fatalf("%s: %v", body, errs)
	}
	w.next++
	id := uuid.MustParse("00000000-0000-4000-8000-" + hex12(w.next))
	o, err := domain.NewOverlay(*norm, model.Owner{Type: "user", Id: "admin"}, id, tFault0.Add(age))
	if err != nil {
		w.t.Fatal(err)
	}
	w.overlays = append(w.overlays, o)
	return o
}

func hex12(n int) string {
	const digits = "0123456789abcdef"
	out := []byte("000000000000")
	for i := len(out) - 1; i >= 0 && n > 0; i-- {
		out[i] = digits[n%16]
		n /= 16
	}
	return string(out)
}

// compile compiles the fixture, feeding back the fault ids of the previous compile.
func (w *faultWorld) compile(mod func(*Input)) *Target {
	w.t.Helper()
	id := w.id
	in := Input{Config: w.cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, Identity: &id,
		Overlays: w.overlays, FaultIDs: w.ids,
		// the tests of capacity name the limit they mean; the others must not depend on the
		// architecture default (200 on ARM64, where a 250-device network does not fit)
		ClassLimit: DefaultClassLimitX86}
	if mod != nil {
		mod(&in)
	}
	tg := Compile(in)
	w.ids = tg.FaultIDs
	return tg
}

// faultOf returns the fault id of the overlay (the one with that source; device "" or the device).
func faultOf(t *testing.T, tg *Target, source, device string) Fault {
	t.Helper()
	for _, f := range tg.Faults {
		if f.Source == source && f.Device == device {
			return f
		}
	}
	t.Fatalf("no fault of %s (device %q) in %+v", source, device, tg.Faults)
	return Fault{}
}
