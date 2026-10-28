package diag

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func httpReq(u string, mods ...func(*model.DiagnosticRequest)) model.DiagnosticRequest {
	return req(model.DiagnosticRequestTypeHttp, "ignored.test", append([]func(*model.DiagnosticRequest){func(r *model.DiagnosticRequest) { r.Url = &u }}, mods...)...)
}

func TestHTTPReportsTheStatusAndTheHeaders(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		w.Header().Set("X-Chaos", "yes")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	res := run(t, runner(nil), httpReq(srv.URL+"/health"))
	if !res.Ok || res.Http == nil || *res.Http.Status != 200 || res.Http.RttMs == nil || res.Error != nil {
		t.Fatalf("%+v", res)
	}
	h := *res.Http.Headers
	if h["X-Chaos"] != "yes" || h["Set-Cookie"] != "a=1, b=2" || h["Content-Length"] != "5" {
		t.Errorf("headers %v", h)
	}
	if !strings.HasPrefix(ua.Load().(string), "chaos-gateway") {
		t.Errorf("user agent %v", ua.Load())
	}
	if res.Address != nil {
		t.Errorf("address %s", *res.Address)
	}
}

func TestHTTPWithoutAURLBuildsOneFromTheHostAndPort(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path.Store(r.URL.Path) }))
	defer srv.Close()
	u := srv.Listener.Addr().String()
	port, _ := strconv.Atoi(u[strings.LastIndexByte(u, ':')+1:])
	res := run(t, runner(nil), req(model.DiagnosticRequestTypeHttp, loop, withPort(port)))
	if !res.Ok || path.Load() != "/" {
		t.Errorf("%+v %v", res, path.Load())
	}
}

func TestHTTPStatusesFromAndAboveFourHundredFail(t *testing.T) {
	for code, want := range map[int]string{404: "HTTP 404 Not Found", 503: "HTTP 503 Service Unavailable"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		res := run(t, runner(nil), httpReq(srv.URL))
		srv.Close()
		if res.Ok || *res.Http.Status != code || str(res.Error) != want {
			t.Errorf("%d: %+v / %s", code, res, str(res.Error))
		}
	}
}

func TestHTTPDoesNotFollowARedirect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	res := run(t, runner(nil), httpReq(srv.URL))
	if !res.Ok || *res.Http.Status != 302 || (*res.Http.Headers)["Location"] != "/elsewhere" || hits.Load() != 1 {
		t.Errorf("%+v hits %d", res, hits.Load())
	}
}

func TestHTTPFailuresAreResults(t *testing.T) {
	res := run(t, runner(nil), httpReq("http://"+loop+":"+strconv.Itoa(closedTCPPort(t))+"/"))
	if res.Ok || res.Http != nil || str(res.Error) != "connection refused" {
		t.Errorf("refused: %+v / %s", res, str(res.Error))
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	res = run(t, runner(nil), httpReq(slow.URL, withTimeout("200ms")))
	if res.Ok || str(res.Error) != "timed out after 200ms" {
		t.Errorf("slow: %+v / %s", res, str(res.Error))
	}
	res = run(t, runner(nil), httpReq("http://gone.test/"))
	if res.Ok || str(res.Error) != "cannot resolve gone.test: no DNS server is configured" {
		t.Errorf("name: %+v / %s", res, str(res.Error))
	}
}

func TestHTTPResolvesTheNameOfTheURLAndKeepsItAsTheHost(t *testing.T) {
	var host atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { host.Store(r.Host) }))
	defer srv.Close()
	port := srv.Listener.Addr().String()[len(loop)+1:]
	d := serveDNS(t, answerA(loop))
	res := run(t, runner(nil, d.Addr), httpReq("http://api.test:"+port+"/"))
	if !res.Ok || str(res.Address) != loop || host.Load() != "api.test:"+port {
		t.Errorf("%+v host %v", res, host.Load())
	}
}

func TestHTTPSVerifiesTheCertificate(t *testing.T) {
	ca, other := newPKI(t, "Test CA"), newPKI(t, "Unknown CA")
	now := time.Now()
	for name, c := range map[string]struct {
		cert tls.Certificate
		err  string
	}{
		"valid":      {ca.good(t, "api.test"), ""},
		"expired":    {ca.leaf(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour), "api.test"), "TLS: certificate has expired"},
		"wrong name": {ca.good(t, "other.test"), "TLS: certificate is valid for other.test, not api.test"},
		"unknown CA": {other.good(t, "api.test"), "TLS: certificate signed by unknown authority"},
	} {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{c.cert}}
		srv.StartTLS()
		port := srv.Listener.Addr().String()[len(loop)+1:]
		d := serveDNS(t, answerA(loop))
		res := run(t, runner(ca.roots, d.Addr), httpReq("https://api.test:"+port+"/"))
		srv.Close()
		if c.err == "" {
			if !res.Ok || *res.Http.Status != 200 {
				t.Errorf("%s: %+v / %s", name, res, str(res.Error))
			}
			continue
		}
		if res.Ok || str(res.Error) != c.err {
			t.Errorf("%s: %+v / %s", name, res, str(res.Error))
		}
	}
}

// The type exists to show what the server answered: the transport decodes nothing and asks for nothing of its own
// (P6-AUDIT-17).
func TestHTTPShowsTheContentEncodingOfAGzipAnswerAndSendsNoAcceptEncoding(t *testing.T) {
	var accept atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write([]byte("not really gzip"))
	}))
	defer srv.Close()
	res := run(t, runner(nil), httpReq(srv.URL))
	if !res.Ok || res.Http == nil {
		t.Fatalf("%+v / %s", res, str(res.Error))
	}
	h := *res.Http.Headers
	if h["Content-Encoding"] != "gzip" || h["Content-Length"] != "15" {
		t.Errorf("headers %v", h)
	}
	if v := accept.Load().(string); v != "" {
		t.Errorf("the request carried Accept-Encoding: %q", v)
	}
}

// The headers of a result are bounded in number and in the length of each name and value (P6-AUDIT-18, P6-AUDIT-20).
func TestHTTPBoundsTheHeadersOfTheResult(t *testing.T) {
	long := strings.Repeat("n", 5<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 70; i++ {
			w.Header().Set("X-Header-"+strconv.Itoa(1000+i), "v")
		}
		w.Header().Set("A-Long-"+long, long) // sorts among the first 64
	}))
	defer srv.Close()
	res := run(t, runner(nil), httpReq(srv.URL))
	if !res.Ok || res.Http == nil {
		t.Fatalf("%+v / %s", res, str(res.Error))
	}
	h := *res.Http.Headers
	if len(h) != maxHeaders {
		t.Errorf("%d headers, want %d", len(h), maxHeaders)
	}
	for k, v := range h {
		if len(k) > maxHeaderValue || len(v) > maxHeaderValue {
			t.Errorf("a header of %d and %d bytes", len(k), len(v))
		}
	}
	// the same answer gives the same headers: the first by name
	if _, ok := h["Content-Length"]; !ok {
		t.Errorf("Content-Length is not among the first by name: %d headers", len(h))
	}
	if _, ok := h["X-Header-1069"]; ok {
		t.Errorf("a header past the 64th by name is in the result")
	}
}

// What is read before the result is built is bounded too.
func TestHTTPRefusesAHeaderBlockThatIsTooLarge(t *testing.T) {
	long := strings.Repeat("v", 900)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 100; i++ {
			w.Header().Set("X-Header-"+strconv.Itoa(1000+i), long)
		}
	}))
	defer srv.Close()
	res := run(t, runner(nil), httpReq(srv.URL))
	if res.Ok || res.Http != nil || str(res.Error) != errHeadersTooBig {
		t.Errorf("%+v / %s", res, str(res.Error))
	}
}
