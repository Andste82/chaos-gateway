package tlsresponder

import (
	"strings"
	"testing"
	"time"
)

// A connection whose case the proxy does not know (P5-AUDIT-04), with the real proxy. The responder decides each connection against the
// API's current snapshot and names the overlay; the proxy learns the cases by its long poll, a moment later. A connection whose overlay
// is not in the proxy's configuration must not pass without the rules of its case, and not with the installation's policy for the
// servers' certificates either: here the installation verifies nothing (CHAOSGW_UPSTREAM_INSECURE=1) and the server's certificate is
// valid for no CA, so a proxy that fell back on the installation would serve it.

const unknownOverlay = "44444444-4444-4444-8444-444444444444"

func TestAConnectionOfACaseTheProxyDoesNotKnowIsRefusedAndNeverReachesTheServer(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none", upstreamInsecure: true})
	ir.gw.waitApplied(t)
	ir.overlay.Store(unknownOverlay)
	if _, body, err := ir.get(ir.device(ir.roots, nil), "/unknown"); err == nil {
		t.Errorf("a connection of a case the proxy does not know was served: %q", body)
	}
	if n := ir.upHits.Load(); n != 0 {
		t.Errorf("%d requests reached the server", n)
	}
	flows := ir.gw.waitFlows(t, 1)
	if f := flows[0]; f["error"] != "the case of the connection is not known to the proxy" || f["overlay"] != unknownOverlay || f["protocol"] != "tcp" {
		t.Errorf("the report of the refused connection: %v", f)
	}
	if _, _, keys := ir.gw.snapshot(); len(keys) != 0 {
		t.Errorf("the proxy gave secrets of a connection it refused: %v", keys)
	}
	if !strings.Contains(ir.proxy.Output(), "the case of the connection is not known to the proxy") {
		t.Errorf("the proxy's log does not say why it refused the connection")
	}
}

// The long poll's lag: a connection that the responder decided with a case the proxy has not received yet waits for the next configuration
// and is then decided by that case (here insecure, which serves the server).
func TestAConnectionWaitsForTheCaseThatTheProxyReceivesAMomentLater(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none"})
	ir.gw.waitApplied(t)
	ir.overlay.Store(unknownOverlay)
	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		_, body, err := ir.get(ir.device(ir.roots, nil), "/later")
		done <- result{body, err}
	}()
	time.Sleep(time.Second)
	ir.gw.setOthers(map[string]string{unknownOverlay: "insecure"})
	r := <-done
	if r.err != nil || r.body != "hello from upstream /later" {
		t.Fatalf("the connection was not served once its case arrived: %q %v", r.body, r.err)
	}
}
