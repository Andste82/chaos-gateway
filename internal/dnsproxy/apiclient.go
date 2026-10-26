package dnsproxy

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

// APIClient is the proxy's side of the internal API (scope `service`): it fetches the configuration
// by long poll and posts the query log.
type APIClient struct {
	// Base is the API's address, e.g. https://169.254.100.1:8443.
	Base string
	// TokenFile holds the service token; it is read for every request, so a rotated token is used.
	TokenFile string
	// CertFile is the API's certificate to trust; empty trusts any certificate (the API listens on
	// the private link to the gateway only).
	CertFile string
	// Wait is how long a poll may take; default 40 s (the API ends it after 30 s).
	Wait time.Duration
	// Dial replaces the dialer; the tests of the service namespace connect from inside it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	// svc is the shared client, made from the fields above at the first request
	once sync.Once
	svc  svcapi.Client
}

func (c *APIClient) shared() *svcapi.Client {
	c.once.Do(func() {
		c.svc = svcapi.Client{Base: c.Base, TokenFile: c.TokenFile, CertFile: c.CertFile, Dial: c.Dial}
	})
	return &c.svc
}

func (c *APIClient) do(ctx context.Context, method, path string, body []byte, timeout time.Duration) (*http.Response, error) {
	return c.shared().Do(ctx, method, path, body, timeout)
}

// CloseIdleConnections closes the connections the client keeps open.
func (c *APIClient) CloseIdleConnections() { c.shared().CloseIdleConnections() }

// Config asks for a configuration newer than generation `after` (0 asks for any). It returns nil
// when nothing changed within the poll.
func (c *APIClient) Config(ctx context.Context, after int64) (*model.DnsServiceConfig, error) {
	wait := c.Wait
	if wait <= 0 {
		wait = 40 * time.Second
	}
	res, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/internal/dns/config?after=%d", after), nil, wait)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var cfg model.DnsServiceConfig
		if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&cfg); err != nil {
			return nil, fmt.Errorf("the configuration: %w", err)
		}
		return &cfg, nil
	}
	return nil, svcapi.Failure(res)
}

// Post implements Sink.
func (c *APIClient) Post(ctx context.Context, entries []model.DnsQueryLogEntry) error {
	body, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	res, err := c.do(ctx, http.MethodPost, "/internal/dns/queries", body, 10*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		return svcapi.Failure(res)
	}
	return nil
}

// PostResolutions implements ResolutionSink: the addresses the proxy is about to answer with for the names that
// hostname selectors match. The API returns after the gateway's DNS-derived sets hold them.
func (c *APIClient) PostResolutions(ctx context.Context, rs []model.DnsResolution) error {
	body, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	res, err := c.do(ctx, http.MethodPost, "/internal/dns/resolutions", body, callTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		return svcapi.Failure(res)
	}
	return nil
}

// The pauses of Follow between two failed attempts: they double from a second up to followPauseMax while no
// configuration has been in use (the gateway's setup is not applied yet), and stay at followPauseInUse once one has:
// a restart of the gateway drops the overlays, and the DNS faults of the configuration in use must not outlive them by
// more than a moment (P5-AUDIT-14).
const (
	followPauseMax   = 10 * time.Second
	followPauseInUse = 2 * time.Second
)

// Follow registers with the API and follows its configuration until ctx ends: the first request
// asks for any configuration, every later one for a newer generation. A failure is retried with a
// growing pause (followPauseMax, or followPauseInUse once a configuration has been in use); the
// configuration in use stays. A failure is logged as a warning once, with a hint,
// and again only when the error changes; the attempts in between are logged at debug level (issue
// #57 F6: before the setup is applied the API cannot be reached, and every attempt was a warning).
func (s *Server) Follow(ctx context.Context, c *APIClient, log *slog.Logger) {
	var after int64
	inUse := false
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
				log.Warn("cannot fetch the DNS configuration: retrying, and logging it again only when the error changes",
					"error", err, "hint", "normal until the setup has been applied (the gateway makes the link to this namespace at the first apply); after it, check the API container")
			default:
				log.Debug("cannot fetch the DNS configuration", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-s.opt.Clock.After(pause):
			}
			limit := followPauseMax
			if inUse {
				limit = followPauseInUse
			}
			pause = min(pause*2, limit)
			continue
		case cfg != nil:
			s.SetConfig(cfg)
			after, inUse = cfg.Generation, true
			log.Info("the DNS configuration is in use", "generation", cfg.Generation, "networks", len(cfg.Networks), "upstream", cfg.Upstream)
		}
		if warned != "" {
			log.Info("the DNS configuration can be fetched again")
			warned = ""
		}
		pause = time.Second
	}
}
