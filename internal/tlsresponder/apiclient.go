package tlsresponder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/svcapi"
)

// APIClient is the responder's side of the internal API (scope `service`): it fetches the configuration by long
// poll, asks for the decision of every connection and posts the outcomes of the handshakes.
type APIClient struct {
	// Base is the API's address, e.g. https://169.254.100.1:8443.
	Base string
	// TokenFile holds the service token; it is read for every request, so a rotated token is used.
	TokenFile string
	// CertFile is the API's certificate to trust; empty trusts any certificate (the API listens on the private link to
	// the gateway only).
	CertFile string
	// Wait is how long a poll may take; default 40 s (the API ends it after 30 s).
	Wait time.Duration
	// Dial replaces the dialer; the tests of the service namespace connect from inside it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	once sync.Once
	svc  svcapi.Client
}

func (c *APIClient) shared() *svcapi.Client {
	c.once.Do(func() {
		c.svc = svcapi.Client{Base: c.Base, TokenFile: c.TokenFile, CertFile: c.CertFile, Dial: c.Dial}
	})
	return &c.svc
}

// CloseIdleConnections closes the connections the client keeps open.
func (c *APIClient) CloseIdleConnections() { c.shared().CloseIdleConnections() }

// Config asks for a configuration newer than generation `after` (0 asks for any). It returns nil when nothing changed
// within the poll.
func (c *APIClient) Config(ctx context.Context, after int64) (*model.TlsServiceConfig, error) {
	wait := c.Wait
	if wait <= 0 {
		wait = 40 * time.Second
	}
	res, err := c.shared().Do(ctx, http.MethodGet, fmt.Sprintf("/internal/tls/config?after=%d", after), nil, wait)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var cfg model.TlsServiceConfig
		if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&cfg); err != nil {
			return nil, fmt.Errorf("the configuration: %w", err)
		}
		return &cfg, nil
	}
	return nil, svcapi.Failure(res)
}

// Post implements Sink.
func (c *APIClient) Post(ctx context.Context, reports []model.TlsHandshakeReport) error {
	body, err := json.Marshal(reports)
	if err != nil {
		return err
	}
	res, err := c.shared().Do(ctx, http.MethodPost, "/internal/tls/handshakes", body, 10*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		return svcapi.Failure(res)
	}
	return nil
}

// Resolve implements Resolver.
func (c *APIClient) Resolve(ctx context.Context, conn ConnInfo) (Decision, error) {
	req := model.TlsResolveRequest{Client: conn.Client.String(), OriginalDst: conn.Dst.String()}
	if conn.SNI != "" {
		req.Sni = &conn.SNI
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Decision{}, err
	}
	res, err := c.shared().Do(ctx, http.MethodPost, "/internal/tls/resolve", body, 5*time.Second)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Decision{}, svcapi.Failure(res)
	}
	var out model.TlsResolveResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return Decision{}, fmt.Errorf("the decision: %w", err)
	}
	d := Decision{Generation: out.Generation, Expected: string(deref(out.Expected)), Name: deref(out.Name), TrustsTestCA: deref(out.TrustsTestCa), Passthrough: deref(out.Passthrough)}
	if out.Overlay != nil {
		d.Overlay = out.Overlay.String()
	}
	if out.Device != nil {
		d.Device = out.Device.String()
	}
	d.Case = out.Case
	return d, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Follow follows the API's configuration until ctx ends: the first request asks for any configuration, every later one
// for a newer generation. A failure is retried with a growing pause and logged as a warning once, and again only when the
// error changes (the attempts in between are logged at debug level: before the setup is applied the API cannot be
// reached).
func (s *Server) Follow(ctx context.Context, c *APIClient, log *slog.Logger) {
	var after int64
	pause := time.Second
	warned := ""
	for ctx.Err() == nil {
		cfg, err := c.Config(ctx, after)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			switch msg := err.Error(); {
			case errors.Is(err, context.DeadlineExceeded): // a poll that took too long: not a failure
			case msg != warned:
				warned = msg
				log.Warn("cannot fetch the TLS configuration: retrying, and logging it again only when the error changes",
					"error", err, "hint", "normal until the setup has been applied (the gateway makes the link to this namespace at the first apply); after it, check the API container")
			default:
				log.Debug("cannot fetch the TLS configuration", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-s.opt.Clock.After(pause):
			}
			if pause < 10*time.Second {
				pause *= 2
			}
			continue
		case cfg != nil:
			s.SetConfig(cfg)
			after = cfg.Generation
			log.Info("the TLS configuration is in use", "generation", cfg.Generation, "cases", len(cfg.Cases))
		}
		if warned != "" {
			log.Info("the TLS configuration can be fetched again")
			warned = ""
		}
		pause = time.Second
	}
}
