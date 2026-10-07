package overlay

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	idESP = "1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a" // device esp32-42
	idIoT = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" // network IoT
)

var (
	start = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	admin = model.Owner{Type: "user", Id: "admin"}
)

func token(id string) model.Owner { return model.Owner{Type: "token", Id: id} }

// testConfig is the example configuration with its references resolved to UUIDs.
func testConfig(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", "configuration.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	norm, errs := domain.Normalize(cfg)
	if len(errs) != 0 {
		t.Fatalf("normalize: %v", errs)
	}
	return norm
}

// fixture is a store on a fake clock, with a way to write requests the way the API does:
// validated, references resolved.
type fixture struct {
	t     *testing.T
	cfg   *model.Configuration
	clk   *clock.Fake
	store *Store
	gen   int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clk := clock.NewFake(start)
	n := 0
	return &fixture{t: t, cfg: testConfig(t), clk: clk, store: New(Options{Clock: clk, NewID: func() uuid.UUID {
		n++
		return uuid.MustParse("00000000-0000-4000-8000-" + hex12(n))
	}})}
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

// request decodes and validates a YAML overlay request against the configuration.
func (f *fixture) request(body string) *model.OverlayRequest {
	f.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		f.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := domain.ValidateOverlay(f.cfg, req)
	if len(errs) != 0 {
		f.t.Fatalf("%s: %v", body, errs)
	}
	return norm
}

// put writes a request as an owner and returns the change.
func (f *fixture) put(owner model.Owner, body string) Change {
	f.t.Helper()
	f.gen++
	ch, err := f.store.Put(owner, f.request(body), PutOptions{Generation: f.gen})
	if err != nil {
		f.t.Fatalf("put %s: %v", body, err)
	}
	return ch
}

func ids(changes []Change) []string {
	var out []string
	for _, c := range changes {
		out = append(out, c.Overlay.Id.String())
	}
	return out
}
