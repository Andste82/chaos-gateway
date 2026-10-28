package diag

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The bounds of the headers a result carries: their number, the length of each name and value, and the size of the
// whole block that is read before the result is built.
const (
	maxHeaders       = 64
	maxHeaderValue   = 1024
	maxHeaderBlock   = 64 << 10
	errHeadersTooBig = "the response headers are too large"
)

// httpGet is the diagnostic `http`: one GET request, whose first response is the result (a redirect is reported, not
// followed). It is ok for a status below 400.
func (r *Runner) httpGet(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	raw := "http://" + req.Host + "/"
	if req.Url != nil {
		raw = *req.Url
	} else if req.Port != nil {
		raw = "http://" + net.JoinHostPort(req.Host, strconv.Itoa(*req.Port)) + "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		res.Error = ptr("the URL is not valid")
		return
	}
	addr, _, err := r.resolveHost(ctx, u.Hostname(), timeout)
	if err != nil {
		res.Error = ptr(err.Error())
		return
	}
	if u.Hostname() != addr.String() {
		res.Address = ptr(addr.String())
	}
	tcfg := &tls.Config{Time: r.cfg.Now}
	if r.cfg.Roots != nil {
		tcfg.RootCAs = r.cfg.Roots()
	}
	tr := &http.Transport{
		// the environment's proxy has no say: the gateway's own stack is what is diagnosed
		Proxy: nil,
		DialContext: func(ctx context.Context, network, hostport string) (net.Conn, error) {
			_, p, err := net.SplitHostPort(hostport)
			if err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, err
			}
			return r.dial(ctx, "tcp", addr, n)
		},
		TLSClientConfig:     tcfg,
		TLSHandshakeTimeout: timeout,
		DisableKeepAlives:   true,
		// the diagnostic shows what the server answered: no Accept-Encoding of its own, no decoding that would take
		// Content-Encoding and Content-Length from the result (nothing reads the body)
		DisableCompression:     true,
		MaxResponseHeaderBytes: maxHeaderBlock,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.Error = ptr("the URL is not valid")
		return
	}
	hreq.Header.Set("User-Agent", r.cfg.UserAgent)
	hreq.Header.Set("Accept", "*/*")

	t0 := r.clk.Monotonic()
	resp, err := client.Do(hreq)
	rtt := r.clk.Monotonic() - t0
	if err != nil {
		res.Error = ptr(r.httpFailure(err, timeout))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	h := newOf(res.Http)
	res.Http = h
	h.Status, h.RttMs = ptr(resp.StatusCode), ptr(ms(rtt))
	// the first 64 by name, so that the same answer gives the same result; names and values are cut like each other
	names := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		names = append(names, k)
	}
	sort.Strings(names)
	headers := map[string]string{}
	for _, k := range names {
		if len(headers) == maxHeaders {
			break
		}
		headers[cut(k, maxHeaderValue)] = cut(strings.Join(resp.Header[k], ", "), maxHeaderValue)
	}
	h.Headers = &headers
	if res.Ok = resp.StatusCode < 400; !res.Ok {
		res.Error = ptr("HTTP " + resp.Status)
	}
}

// cut returns s, at most n bytes of it.
func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// httpFailure is the error of a request in a sentence: a certificate that does not verify says why.
func (r *Runner) httpFailure(err error, timeout time.Duration) string {
	if strings.Contains(err.Error(), "response headers exceeded") {
		return errHeadersTooBig
	}
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		if len(cve.UnverifiedCertificates) > 0 {
			return "TLS: " + verifyMessage(cve.Err, cve.UnverifiedCertificates[0], r.cfg.Now())
		}
		return "TLS: " + strings.TrimPrefix(cve.Err.Error(), "x509: ")
	}
	var ue *url.Error
	if errors.As(err, &ue) && !isTimeout(err) {
		return failure(ue.Err, timeout)
	}
	return failure(err, timeout)
}
