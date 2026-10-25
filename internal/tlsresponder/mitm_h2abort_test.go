package tlsresponder

import (
	"net/http"
	"testing"
	"time"
)

// An abort rule over HTTP/2 (plan M22 "Tests", left untested by the first implementation), with the real proxy. HTTP/2 has many requests
// on one connection, and the abort is a reset of the connection: the responder resets the device's TCP connection when the proxy asks it
// to, so the request that matched the rule fails, and so does every other request in flight on that connection (a client cannot tell them
// apart: that is what a device meets when its connection is reset). The next request opens a connection, which is served as before.
func TestAnAbortRuleOverHTTP2ResetsTheConnectionAndTheRequestsInFlightOnIt(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{h2: true})
	ir.gw.setRules(
		rule("abort", map[string]any{"match": matchPath("/abort")}),
		rule("delay", map[string]any{"match": matchPath("/slow"), "delay": "3s"}),
	)
	ir.gw.waitApplied(t)
	dev := ir.device(ir.roots, nil)

	resp, body, err := ir.get(dev, "/fine")
	if err != nil || resp.ProtoMajor != 2 || body != "hello from upstream /fine" {
		t.Fatalf("the first request over HTTP/2: %v %v %q", err, resp, body)
	}
	// a request that the proxy holds back, on the same connection, while another is aborted
	type result struct {
		err     error
		elapsed time.Duration
	}
	slow := make(chan result, 1)
	started := time.Now()
	go func() {
		_, _, err := ir.get(dev, "/slow/x")
		slow <- result{err, time.Since(started)}
	}()
	time.Sleep(400 * time.Millisecond)
	if _, _, err := ir.get(dev, "/abort/now"); err == nil {
		t.Fatal("a request that an abort rule matches was answered")
	}
	select {
	case r := <-slow:
		if r.err == nil || r.elapsed >= 2500*time.Millisecond {
			t.Errorf("the request in flight on the same connection ended with %v after %s: want an error long before its 3 s of delay", r.err, r.elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request in flight is still waiting: the abort did not reset the connection")
	}

	var aborted map[string]any
	for _, f := range ir.gw.waitFlows(t, 2) {
		if f["path"] == "/abort/now" {
			aborted = f
		}
	}
	if aborted == nil || aborted["error"] != "aborted by a rule" || actionsOf(aborted) != "abort" || aborted["status"] != nil {
		t.Errorf("the aborted exchange: %v", aborted)
	}
	if got := ir.rig.srv.Aborted(); got < 1 {
		t.Errorf("the responder reset %d connections", got)
	}
	// the next connection is served, over HTTP/2 again
	resp, body, err = ir.get(ir.device(ir.roots, nil), "/again")
	if err != nil || resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 || body != "hello from upstream /again" {
		t.Errorf("after the abort: %v %v %q", err, resp, body)
	}
	if hits := ir.upHits.Load(); hits != 2 {
		t.Errorf("the server saw %d requests: the first and the one after the abort, not the aborted one, and not the held-back one", hits)
	}
}
