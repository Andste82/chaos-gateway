package tlsresponder

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// WaitForCAs loads the two CAs from dir, trying every interval until it works or ctx ends: the API makes them when it
// starts, and the responder's container may start first. A failure is logged as a warning once.
func WaitForCAs(ctx context.Context, clk clock.Clock, dir string, interval time.Duration, log *slog.Logger) (*CAs, error) {
	warned := false
	for {
		cas, err := LoadCAs(dir)
		if err == nil {
			return cas, nil
		}
		if !warned {
			warned = true
			log.Warn("the CAs are not there yet: waiting for the API to make them", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-clk.After(interval):
		}
	}
}

// WatchCAs reloads the CAs into the issuer when their files change, until ctx ends: a CA that was made anew (its
// regeneration is `POST /tls/ca/regenerate`, M22) is the one the responder signs with from then on, so the certificates it shows chain to
// what the devices were given.
func WatchCAs(ctx context.Context, clk clock.Clock, dir string, iss *Issuer, interval time.Duration, log *slog.Logger) {
	last := stamp(dir)
	for {
		select {
		case <-ctx.Done():
			return
		case <-clk.After(interval):
		}
		now := stamp(dir)
		if now == last {
			continue
		}
		cas, err := LoadCAs(dir)
		if err != nil {
			log.Warn("the CAs changed and cannot be read: the old ones stay in use", "error", err)
			continue
		}
		iss.SetCAs(cas)
		last = now
		log.Info("the CAs were reloaded")
	}
}

// stamp is what tells that the CA files changed: their sizes and times.
func stamp(dir string) [4]int64 {
	var out [4]int64
	for i, n := range []string{testCAFile + ".pem", unknownCAFile + ".pem"} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil {
			out[2*i], out[2*i+1] = fi.Size(), fi.ModTime().UnixNano()
		}
	}
	return out
}
