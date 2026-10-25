package tlsresponder

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The changes of a `modify` rule to the request reach the server: a header the rule names is added or replaced, one with an empty value is
// removed, and the headers of the device that no rule names are left alone. The server answers with what it saw.
func TestTheRequestHeadersOfAModifyRuleReachTheServer(t *testing.T) {
	seen := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Saw-Added", r.Header.Get("X-Added-By-Rule"))
		w.Header().Set("X-Saw-Replaced", r.Header.Get("X-Replace-Me"))
		w.Header().Set("X-Saw-Removed", "["+r.Header.Get("X-Remove-Me")+"]")
		w.Header().Set("X-Saw-Kept", r.Header.Get("X-Keep"))
		_, _ = io.WriteString(w, "ok")
	})
	ir := newInterceptRig(t, riglOpt{handler: seen})
	ir.gw.setRules(rule("modify", map[string]any{"match": matchPath("/mod"), "modify": map[string]any{
		"request_headers": map[string]any{"X-Added-By-Rule": "1", "X-Replace-Me": "by-the-rule", "X-Remove-Me": ""},
	}}))
	time.Sleep(500 * time.Millisecond)
	dev := ir.device(ir.roots, nil)

	do := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "https://"+hostName+path, nil)
		req.Header.Set("X-Replace-Me", "by-the-device")
		req.Header.Set("X-Remove-Me", "secret")
		req.Header.Set("X-Keep", "kept")
		resp, err := dev.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp
	}
	resp := do("/mod/x")
	for h, want := range map[string]string{"X-Saw-Added": "1", "X-Saw-Replaced": "by-the-rule", "X-Saw-Removed": "[]", "X-Saw-Kept": "kept"} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("the server saw %s = %q, want %q", h, got, want)
		}
	}
	// a request the rule does not match is not changed
	resp = do("/other")
	for h, want := range map[string]string{"X-Saw-Added": "", "X-Saw-Replaced": "by-the-device", "X-Saw-Removed": "[secret]", "X-Saw-Kept": "kept"} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("an unmatched request: the server saw %s = %q, want %q", h, got, want)
		}
	}
}

// With block_quic (on unless the case turns it off) the proxy takes the advertisements of HTTP/3 out of the `Alt-Svc` header of the
// responses, so a client that would try QUIC next time does not learn of it; the other entries stay, and the header goes when
// nothing is left. A case that keeps QUIC leaves the header as the server sent it.
func TestAltSvcOfTheServerLosesItsHTTP3EntriesWhileTheCaseBlocksQUIC(t *testing.T) {
	alt := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/both":
			w.Header().Set("Alt-Svc", `h3=":443"; ma=86400, h3-29=":443", h2=":8443"; ma=60`)
		case "/h3only":
			w.Header().Set("Alt-Svc", `h3=":443"; ma=86400`)
		}
		_, _ = io.WriteString(w, "ok")
	})
	ir := newInterceptRig(t, riglOpt{handler: alt})
	time.Sleep(500 * time.Millisecond)
	dev := ir.device(ir.roots, nil)
	altSvc := func(path string) (string, bool) {
		t.Helper()
		resp, _, err := ir.get(dev, path)
		if err != nil {
			t.Fatal(err)
		}
		v, ok := resp.Header["Alt-Svc"]
		return strings.Join(v, "|"), ok
	}
	if got, _ := altSvc("/both"); got != `h2=":8443"; ma=60` {
		t.Errorf("Alt-Svc with the HTTP/3 entries taken out: %q", got)
	}
	if got, ok := altSvc("/h3only"); ok {
		t.Errorf("Alt-Svc with nothing left but HTTP/3 is still there: %q", got)
	}
	if _, ok := altSvc("/none"); ok {
		t.Error("an Alt-Svc header appeared")
	}

	// the case keeps QUIC: the header is the server's
	off := false
	ir.gw.mu.Lock()
	ir.gw.blockQUIC = &off
	ir.gw.gen++
	ir.gw.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ := altSvc("/h3only")
		if got == `h3=":443"; ma=86400` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with block_quic false the header is %q, want the server's", got)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got, _ := altSvc("/both"); got != `h3=":443"; ma=86400, h3-29=":443", h2=":8443"; ma=60` {
		t.Errorf("with block_quic false: %q", got)
	}
}

// TLS 1.2 and TLS 1.3 both go through the proxy (the S4 follow-up of plan §5): a device that speaks only 1.2, or only 1.3, is served, the
// handshake is reported with the version the device negotiated, and the secrets of its connection are in what the proxy gives the gateway
// (TLS 1.2 has the one line `CLIENT_RANDOM`, TLS 1.3 several).
func TestDevicesOfTLS12AndTLS13AreInterceptedAndTheirSecretsReachTheGateway(t *testing.T) {
	for _, v := range []struct {
		name    string
		version uint16
		want    string
	}{{"TLS 1.2", tls.VersionTLS12, "TLS 1.2"}, {"TLS 1.3", tls.VersionTLS13, "TLS 1.3"}} {
		t.Run(v.name, func(t *testing.T) {
			ir := newInterceptRig(t, riglOpt{})
			time.Sleep(300 * time.Millisecond)
			var own lockedWriter
			dev := ir.device(ir.roots, &own)
			cfg := dev.Transport.(*http.Transport).TLSClientConfig
			cfg.MinVersion, cfg.MaxVersion = v.version, v.version
			resp, body, err := ir.get(dev, "/versions")
			if err != nil || body != "hello from upstream /versions" || resp.TLS == nil || resp.TLS.Version != v.version {
				t.Fatalf("%v %q %+v", err, body, resp)
			}
			dev.Transport.(*http.Transport).CloseIdleConnections()
			if h := ir.gw.waitHandshakes(t, 1, "accepted")[0]; h.TlsVersion == nil || *h.TlsVersion != v.want {
				t.Errorf("the handshake was reported as %v, want %s", h.TlsVersion, v.want)
			}
			deviceLines := nonEmptyLines(own.String())
			if len(deviceLines) == 0 || (v.version == tls.VersionTLS12 && !strings.HasPrefix(deviceLines[0], "CLIENT_RANDOM ")) {
				t.Fatalf("the device wrote %q", deviceLines)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				have := map[string]bool{}
				_, _, keys := ir.gw.snapshot()
				for _, k := range keys {
					for _, l := range k["lines"].([]any) {
						have[l.(string)] = true
					}
				}
				missing := 0
				for _, l := range deviceLines {
					if !have[l] {
						missing++
					}
				}
				if missing == 0 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("%d of the device's %d lines are not in the key log of the proxy: %v", missing, len(deviceLines), have)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

// A response that never ends reaches the device as it comes: the proxy streams a body unless a rule replaces text in it, so a long poll or a
// stream of server-sent events works through it. The changes of a rule to the headers of such a response are made before the body goes on,
// the exchange is reported with the bytes that passed, and a response whose body a rule changes is still buffered and changed.
func TestAResponseThatNeverEndsReachesTheDeviceWhileTheServerIsStillSending(t *testing.T) {
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			_, _ = io.WriteString(w, "a body with text to replace")
			return
		}
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		case <-time.After(30 * time.Second):
			return
		}
		_, _ = io.WriteString(w, "second\n")
	})
	ir := newInterceptRig(t, riglOpt{handler: h})
	ir.gw.setRules(
		rule("modify", map[string]any{"match": matchPath("/stream"), "modify": map[string]any{"response_headers": map[string]any{"X-Chaos": "streamed"}}}),
		rule("modify", map[string]any{"match": matchPath("/replace"), "modify": map[string]any{"response_body_replace": []any{map[string]any{"search": "text", "replace": "words"}}}}),
	)
	time.Sleep(500 * time.Millisecond)
	dev := ir.device(ir.roots, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostName+"/stream", nil)
	resp, err := dev.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("X-Chaos") != "streamed" {
		t.Errorf("the header change of the rule is missing on a streamed response: %v", resp.Header)
	}
	br := bufio.NewReader(resp.Body)
	got := make(chan string, 1)
	go func() { l, _ := br.ReadString('\n'); got <- l }()
	select {
	case l := <-got:
		if l != "first\n" {
			t.Fatalf("the first chunk is %q", l)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first chunk of a response that is not over did not reach the device: the proxy buffers it")
	}
	close(release)
	if l, err := br.ReadString('\n'); err != nil || l != "second\n" {
		t.Errorf("the rest of the response: %q %v", l, err)
	}
	for _, f := range ir.gw.waitFlows(t, 1) {
		if f["path"] == "/stream" && (f["response_bytes"] != float64(len("first\nsecond\n")) || f["status"] != float64(200)) {
			t.Errorf("the exchange is reported as %v", f)
		}
	}

	// a body a rule changes is buffered and changed
	if _, body, err := ir.get(dev, "/replace"); err != nil || body != "a body with words to replace" {
		t.Errorf("%q %v", body, err)
	}
}
