package api

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// ReadTimeout bounds how long a request body may take to arrive (M5-11): a client trickling a
// body otherwise ties up a connection indefinitely. A package variable so tests can shorten it.
var ReadTimeout = 60 * time.Second

// Binder keeps the API listening on the addresses it should be reachable at (plan §2.16): until the
// setup is finished on every address of the host, afterwards only on the management network (and the
// loopback, for the health check). It compares what it listens on with what is wanted and opens or
// closes listeners to match, so a changed management address or a finished setup takes effect
// without a restart.
type Binder struct {
	// Addrs returns the addresses to listen on now.
	Addrs func() []netip.Addr
	// Port returns the port.
	Port func() int
	// Handler is the API.
	Handler http.Handler
	// TLS is the configuration of the HTTPS listeners.
	TLS *tls.Config
	Log *slog.Logger

	mu      sync.Mutex
	servers map[netip.AddrPort]*http.Server
	// lastErr remembers the last bind error per address (M5-14), so a repeated failure across
	// reconciles logs only once, not every interval.
	lastErr map[netip.AddrPort]string
}

// Listening returns the addresses the binder listens on, sorted.
func (b *Binder) Listening() []netip.AddrPort {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]netip.AddrPort, 0, len(b.servers))
	for a := range b.servers {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Reconcile opens the listeners that are missing and closes those that are not wanted.
func (b *Binder) Reconcile() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.servers == nil {
		b.servers = map[netip.AddrPort]*http.Server{}
	}
	want := map[netip.AddrPort]bool{}
	for _, a := range b.Addrs() {
		want[netip.AddrPortFrom(a, uint16(b.Port()))] = true
	}
	for ap, srv := range b.servers {
		if !want[ap] {
			b.Log.Info("the API stops listening", "address", ap.String())
			_ = srv.Close()
			delete(b.servers, ap)
		}
	}
	for ap := range b.lastErr {
		if !want[ap] {
			delete(b.lastErr, ap)
		}
	}
	for ap := range want {
		if _, ok := b.servers[ap]; ok {
			continue
		}
		l, err := net.Listen("tcp", ap.String())
		if err != nil {
			if b.lastErr[ap] != err.Error() {
				b.Log.Warn("the API cannot listen", "address", ap.String(), "error", err)
				if b.lastErr == nil {
					b.lastErr = map[netip.AddrPort]string{}
				}
				b.lastErr[ap] = err.Error()
			}
			continue
		}
		if _, hadErr := b.lastErr[ap]; hadErr {
			b.Log.Info("the API can listen again", "address", ap.String())
			delete(b.lastErr, ap)
		}
		srv := &http.Server{Handler: b.Handler, TLSConfig: b.TLS, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: ReadTimeout, IdleTimeout: 2 * time.Minute}
		b.servers[ap] = srv
		b.Log.Info("the API listens", "address", ap.String())
		go func() {
			var err error
			if b.TLS != nil {
				err = srv.ServeTLS(l, "", "")
			} else {
				err = srv.Serve(l)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				b.Log.Error("the API listener ended", "address", ap.String(), "error", err)
				b.mu.Lock()
				if b.servers[ap] == srv {
					delete(b.servers, ap) // the next reconcile listens again
				}
				b.mu.Unlock()
			}
		}()
	}
}

// Run reconciles every interval until ctx ends, then closes everything.
func (b *Binder) Run(ctx context.Context, interval time.Duration) {
	b.Reconcile()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			b.Close()
			return
		case <-t.C:
			b.Reconcile()
		}
	}
}

// Close closes every listener and waits for nothing: open connections end with them.
func (b *Binder) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ap, srv := range b.servers {
		_ = srv.Close()
		delete(b.servers, ap)
	}
}
