package api_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Andste82/chaos-gateway/internal/api"
)

// webBuild is a stand-in for web/dist: the page, a hashed asset and a file at the root.
var webBuild = fstest.MapFS{
	"index.html":             {Data: []byte("<!doctype html><title>Chaos Gateway</title>")},
	"assets/index-1a2b3c.js": {Data: []byte("console.log('app')")},
	"favicon.svg":            {Data: []byte("<svg/>")},
}

func getRaw(t *testing.T, g *gw, method, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, g.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// M12: the binary serves the web app next to the API (plan §3.7). A route of the app gets the page,
// so a reload of a deep link works; build files are sent as they are; nothing under /api/ is ever
// the page; and the app loads before the setup is finished, since the setup wizard is part of it.
func TestTheWebAppIsServedNextToTheAPI(t *testing.T) {
	g := newGW(t, func(o *options) { o.ui = webBuild }) // setup not finished

	for _, p := range []string{"/", "/devices", "/devices/esp32-42", "/setup"} {
		res, body := getRaw(t, g, "GET", p)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "<title>Chaos Gateway</title>") {
			t.Fatalf("GET %s: %d %q", p, res.StatusCode, body)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: content type %q", p, ct)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("GET %s: CSP %q", p, csp)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s: the page must not be cached (%q)", p, cc)
		}
	}

	res, body := getRaw(t, g, "GET", "/assets/index-1a2b3c.js")
	if res.StatusCode != http.StatusOK || body != "console.log('app')" || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("asset: %d %q %q", res.StatusCode, body, res.Header.Get("Content-Type"))
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("a hashed asset may be cached for good: %q", cc)
	}
	if res, _ := getRaw(t, g, "HEAD", "/favicon.svg"); res.StatusCode != http.StatusOK {
		t.Errorf("HEAD of a build file: %d", res.StatusCode)
	}

	// a missing build file is a 404 problem, not the page parsed as JavaScript
	for _, p := range []string{"/assets/index-old.js", "/missing.png"} {
		res, body := getRaw(t, g, "GET", p)
		if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "not_found") {
			t.Errorf("GET %s: %d %q", p, res.StatusCode, body)
		}
	}
	// the API keeps its own answers: an unknown API path and a write outside /api/ are no pages
	for _, c := range []struct{ method, path string }{{"GET", "/api/v1/nothing-here"}, {"GET", "/api"}, {"POST", "/devices"}} {
		res, body := getRaw(t, g, c.method, c.path)
		if res.StatusCode != http.StatusNotFound || strings.Contains(body, "<title>") || !strings.Contains(res.Header.Get("Content-Type"), "problem+json") {
			t.Errorf("%s %s: %d %q", c.method, c.path, res.StatusCode, body)
		}
	}
	// a path that climbs out of the build stays inside it
	if res, body := getRaw(t, g, "GET", "/../../etc/passwd"); res.StatusCode == http.StatusOK && strings.Contains(body, "root:") {
		t.Errorf("path traversal: %q", body)
	}
}

func TestWithoutAWebAppEveryOtherPathIsNotFound(t *testing.T) {
	g := newGW(t)
	if res, body := getRaw(t, g, "GET", "/"); res.StatusCode != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Errorf("%d %q", res.StatusCode, body)
	}
}

func TestCheckUIWantsAnIndexPage(t *testing.T) {
	if err := api.CheckUI(webBuild); err != nil {
		t.Error(err)
	}
	if err := api.CheckUI(fstest.MapFS{"assets/a.js": {}}); err == nil {
		t.Error("a build without index.html was accepted")
	}
}

// M12: the uplink reports its interface byte counters, which the overview's traffic chart turns
// into rates.
func TestTheUplinkReportsItsByteCounters(t *testing.T) {
	dir := t.TempDir()
	stats := filepath.Join(dir, "wan0", "statistics")
	if err := os.MkdirAll(stats, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, v := range map[string]string{"rx_bytes": "123456\n", "tx_bytes": "7890\n", "../carrier": "1\n"} {
		if err := os.WriteFile(filepath.Join(stats, f), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer api.SetSysClassNet(dir)()
	g := ready(t)
	st := g.do("GET", "/uplink", nil, nil, nil).json(t)["status"].(map[string]any)
	if st["rx_bytes"] != float64(123456) || st["tx_bytes"] != float64(7890) || st["link_up"] != true {
		t.Errorf("%v", st)
	}

	// without counters the fields are left out, not zero
	defer api.SetSysClassNet(t.TempDir())()
	st = g.do("GET", "/uplink", nil, nil, nil).json(t)["status"].(map[string]any)
	if _, ok := st["rx_bytes"]; ok {
		t.Errorf("%v", st)
	}
}
