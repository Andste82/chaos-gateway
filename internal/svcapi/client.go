package svcapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Client sends the requests of a service container to the internal API.
type Client struct {
	// Base is the API's address, e.g. https://169.254.100.1:8443.
	Base string
	// TokenFile holds the service token; it is read for every request, so a rotated token is used.
	TokenFile string
	// CertFile is the API's certificate to trust; empty trusts any certificate (the API listens on
	// the private link to the gateway only).
	CertFile string
	// Dial replaces the dialer; the tests of the service namespace connect from inside it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	// mu guards client and cert: the requests of the container share the client
	mu     sync.Mutex
	client *http.Client
	cert   []byte
}

// http returns the client for the certificate in CertFile now: a certificate the API has issued again
// (issue #64: one without the service address) is trusted without a restart of the service.
func (c *Client) http() (*http.Client, error) {
	var cert []byte
	if c.CertFile != "" {
		b, err := os.ReadFile(c.CertFile)
		if err != nil {
			return nil, err
		}
		cert = b
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && bytes.Equal(cert, c.cert) {
		return c.client, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CertFile != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cert) {
			return nil, fmt.Errorf("%s holds no certificate", c.CertFile)
		}
		tc.RootCAs = pool
	} else {
		tc.InsecureSkipVerify = true //nolint:gosec // the private link to the gateway; see CertFile
	}
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	c.client = &http.Client{Transport: &http.Transport{TLSClientConfig: tc, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 60 * time.Second, DialContext: c.Dial}}
	c.cert = cert
	return c.client, nil
}

// Do sends a request of the internal API; path starts below /api/v1. The caller closes the body, which also ends
// the timeout.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, timeout time.Duration) (*http.Response, error) {
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
func (c *Client) CloseIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
}

// Failure is the error of a response that was not the one expected: the status and the start of the body (a
// *StatusError).
func Failure(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return &StatusError{Status: res.StatusCode, Body: strings.TrimSpace(string(b))}
}

// StatusError is an answer of the API with a status that was not the one expected.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("the API answered %d: %s", e.Status, e.Body) }

// Refused says whether the API refused a request for its content, for good: a 4xx other than 408 (Request Timeout) and
// 429 (Too Many Requests). Sending the same request again would be refused again; a transport error, a 5xx, 408 and 429
// may pass on a later try.
func Refused(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status >= 400 && se.Status < 500 && se.Status != http.StatusRequestTimeout && se.Status != http.StatusTooManyRequests
}
