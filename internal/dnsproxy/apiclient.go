package dnsproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
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

	client *http.Client
}

func (c *APIClient) http() (*http.Client, error) {
	if c.client != nil {
		return c.client, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CertFile != "" {
		pem, err := os.ReadFile(c.CertFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no certificate", c.CertFile)
		}
		tc.RootCAs = pool
	} else {
		tc.InsecureSkipVerify = true //nolint:gosec // the private link to the gateway; see CertFile
	}
	c.client = &http.Client{Transport: &http.Transport{TLSClientConfig: tc, MaxIdleConns: 2, IdleConnTimeout: 60 * time.Second, DialContext: c.Dial}}
	return c.client, nil
}

func (c *APIClient) do(ctx context.Context, method, path string, body []byte, timeout time.Duration) (*http.Response, error) {
	cl, err := c.http()
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("the service token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+"/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := cl.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	res.Body = &cancelBody{ReadCloser: res.Body, cancel: cancel}
	return res, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }

// CloseIdleConnections closes the connections the client keeps open.
func (c *APIClient) CloseIdleConnections() {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
}

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
	b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return nil, fmt.Errorf("the API answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
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
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("the API answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Follow registers with the API and follows its configuration until ctx ends: the first request
// asks for any configuration, every later one for a newer generation. A failure is retried with a
// growing pause; the configuration in use stays.
func (s *Server) Follow(ctx context.Context, c *APIClient, log *slog.Logger) {
	var after int64
	pause := time.Second
	for ctx.Err() == nil {
		cfg, err := c.Config(ctx, after)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				log.Warn("cannot fetch the DNS configuration", "error", err)
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
			log.Info("the DNS configuration is in use", "generation", cfg.Generation, "networks", len(cfg.Networks), "upstream", cfg.Upstream)
		}
		pause = time.Second
	}
}
