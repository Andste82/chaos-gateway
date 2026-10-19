package ctl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// connection is what the global flags and the environment say about the gateway.
type connection struct {
	url         string
	token       string
	tokenFile   string
	caCert      string
	fingerprint string
	insecure    bool
	timeout     time.Duration
}

const defaultURL = "https://127.0.0.1:8443"

// apiBase turns the address a user gives into the base of the API: the scheme and host, with or without
// the path /api/v1.
func apiBase(raw string) (string, error) {
	if raw == "" {
		raw = defaultURL
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("%q is not the address of a gateway (https://host:8443)", raw)
	}
	p := strings.TrimSuffix(u.Path, "/")
	if p == "" {
		p = "/api/v1"
	}
	if p != "/api/v1" {
		return "", fmt.Errorf("%q: the path of the API is /api/v1", raw)
	}
	u.Path, u.RawQuery, u.Fragment = p, "", ""
	return u.String(), nil
}

// client is an HTTP client for the API of one gateway.
type client struct {
	base  string
	token string
	hc    *http.Client
	// timeout bounds one request
	timeout time.Duration
}

func newClient(c connection) (*client, error) {
	base, err := apiBase(c.url)
	if err != nil {
		return nil, err
	}
	token := c.token
	if token == "" && c.tokenFile != "" {
		b, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, errors.New("no API token: give --token-file or --token, or set CHAOSGW_TOKEN (docs/install.md, \"Command line client\", says how to create one)")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case c.insecure:
		cfg.InsecureSkipVerify = true //nolint:gosec // the explicit choice of --insecure, for a gateway with its self-signed certificate
	case c.fingerprint != "":
		want, err := parseFingerprint(c.fingerprint)
		if err != nil {
			return nil, err
		}
		// the certificate is trusted by its fingerprint alone: that is how a self-signed certificate is trusted
		// without a copy of it
		cfg.InsecureSkipVerify = true //nolint:gosec // the peer is verified below by its pinned fingerprint
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("the gateway sent no certificate")
			}
			if sum := sha256.Sum256(raw[0]); sum != want {
				return fmt.Errorf("the certificate of the gateway has the SHA-256 fingerprint %s, not the pinned one", hex.EncodeToString(sum[:]))
			}
			return nil
		}
	case c.caCert != "":
		pem, err := os.ReadFile(c.caCert)
		if err != nil {
			return nil, fmt.Errorf("--ca-cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--ca-cert: %s holds no PEM certificate", c.caCert)
		}
		cfg.RootCAs = pool
	}
	tr := &http.Transport{TLSClientConfig: cfg, Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &client{base: base, token: token, hc: &http.Client{Transport: tr}, timeout: timeout}, nil
}

// close drops the idle connections.
func (c *client) close() { c.hc.CloseIdleConnections() }

// parseFingerprint reads a SHA-256 fingerprint as hex, with or without colons and a `sha256:` prefix.
func parseFingerprint(s string) ([32]byte, error) {
	var out [32]byte
	h := strings.NewReplacer(":", "", " ", "").Replace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "sha256:"))
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != len(out) {
		return out, fmt.Errorf("--fingerprint %q is not a SHA-256 fingerprint (64 hex digits)", s)
	}
	copy(out[:], b)
	return out, nil
}

// apiError is an answer of the gateway that is not a success.
type apiError struct {
	Status  int
	Problem *model.Problem
	Body    string
}

func (e *apiError) Error() string {
	if e.Problem == nil {
		return fmt.Sprintf("the gateway answered %d %s", e.Status, strings.TrimSpace(e.Body))
	}
	p := e.Problem
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s: ", e.Status, p.Code)
	if p.Detail != nil && *p.Detail != "" {
		b.WriteString(*p.Detail)
	} else {
		b.WriteString(p.Title)
	}
	if p.Errors != nil {
		for _, ve := range *p.Errors {
			fmt.Fprintf(&b, "\n  %s: %s", ve.Path, ve.Message)
		}
	}
	if p.References != nil {
		for _, r := range *p.References {
			fmt.Fprintf(&b, "\n  used by %s %s (%s)", r.Kind, r.Id, r.Object)
		}
	}
	return b.String()
}

func asAPIError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// call sends a request and decodes a JSON answer into out (nil: the body is discarded). A body is sent as
// JSON. It returns the response headers.
func (c *client) call(ctx context.Context, method, path string, query url.Values, body, out any) (http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res, err := c.send(ctx, method, path, query, rd, body != nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.Header, problemOf(res.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.Header, fmt.Errorf("%s %s: the answer is not what the API describes: %w", method, path, err)
		}
	}
	return res.Header, nil
}

// open starts a GET and returns the response with a body that is still to be read (the caller closes it) and
// the file name the gateway suggests ("" for none). The path is relative to the API (`/captures/<id>/download`).
func (c *client) open(ctx context.Context, path string, query url.Values) (*http.Response, string, error) {
	res, err := c.send(ctx, http.MethodGet, path, query, nil, false)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		return nil, "", problemOf(res.StatusCode, raw)
	}
	name := ""
	if _, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	return res, name, nil
}

// download streams the body of a GET to w and returns the file name the gateway suggests ("" for none).
func (c *client) download(ctx context.Context, path string, query url.Values, w io.Writer) (string, error) {
	res, name, err := c.open(ctx, path, query)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := io.Copy(w, res.Body); err != nil {
		return name, fmt.Errorf("GET %s: %w", path, err)
	}
	return name, nil
}

func (c *client) send(ctx context.Context, method, path string, query url.Values, body io.Reader, hasBody bool) (*http.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, */*;q=0.5")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return res, nil
}

func problemOf(status int, raw []byte) error {
	e := &apiError{Status: status, Body: string(raw)}
	var p model.Problem
	if json.Unmarshal(raw, &p) == nil && p.Code != "" {
		e.Problem = &p
	}
	return e
}

// listAll follows the cursor of a list.
func listAll[T any](ctx context.Context, c *client, path string, query url.Values) ([]T, error) {
	var all []T
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	for {
		var page struct {
			Items      []T     `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		if _, err := c.call(ctx, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}
