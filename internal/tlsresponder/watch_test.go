package tlsresponder

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The responder's container may start before the API has made the CAs: it waits for them, and ends the wait with its context.
func TestTheResponderWaitsForTheCAsTheAPIMakes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tlsca")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = EnsureCAs(dir, time.Now())
	}()
	cas, err := WaitForCAs(ctx, &clock.Real{}, dir, 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil || cas == nil || cas.Test == nil || cas.Unknown == nil {
		t.Fatalf("%+v %v", cas, err)
	}
	// without CAs the wait ends with the context
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if _, err := WaitForCAs(short, &clock.Real{}, filepath.Join(t.TempDir(), "none"), 20*time.Millisecond, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("no error without CAs")
	}
}

// A CA that was made anew is the one the responder signs with from then on.
func TestACAThatChangesIsReloadedIntoTheIssuer(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := NewIssuer(cas, &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := iss.Certificates(model.TlsCaseCaseExpired, "a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WatchCAs(ctx, &clock.Real{}, dir, iss, 20*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()
	defer func() { cancel(); <-done }()

	// new authorities in the files
	fresh := t.TempDir()
	if err := EnsureCAs(fresh, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"test-ca.pem", "test-ca.key", "unknown-ca.pem", "unknown-ca.key"} {
		raw, _ := os.ReadFile(filepath.Join(fresh, f))
		if err := os.WriteFile(filepath.Join(dir, f), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		after, err := iss.Certificates(model.TlsCaseCaseExpired, "a.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before[0].Certificate[0], after[0].Certificate[0]) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the issuer still signs with the old CAs")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
