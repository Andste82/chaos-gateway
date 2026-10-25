package tlsresponder

import (
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The policy for the certificates of the servers (P5-M22-03, the field `upstream` of an interception case), with the real proxy. The
// server of the rig has a certificate for example.com that no CA of mitmproxy's bundle signed, so what the proxy does with it is the
// policy. The installation's side of the policy is what the proxy is started with: a CA file (CHAOSGW_UPSTREAM_CA_FILE) that has the
// server's certificate, a CA file that has not, none, or CHAOSGW_UPSTREAM_INSECURE=1.

// upstreamOutcome is what a device gets for a request through the proxy, and what the proxy reported for it. A server the proxy refuses
// is a 502 over the proxy's own certificate (mitmproxy answers the device it could not serve), and two reports: the failed connection
// to the server (protocol tcp, `TLS to the server failed: ...`) and the exchange it made impossible (the path, no status, the error).
type upstreamOutcome struct {
	status int
	body   string
	flows  []map[string]any
}

// ask makes one request for the host and waits for the proxy's reports of it (one for an exchange that was served, two for one that was
// refused, as the status says).
func (ir *interceptRig) ask(host, path string) upstreamOutcome {
	ir.t.Helper()
	_, before, _ := ir.gw.snapshot()
	tr := &http.Transport{
		DialContext:     ir.device(ir.roots, nil).Transport.(*http.Transport).DialContext,
		TLSClientConfig: &tls.Config{RootCAs: ir.roots},
	}
	ir.t.Cleanup(tr.CloseIdleConnections)
	resp, err := (&http.Client{Transport: tr, Timeout: 20 * time.Second}).Get("https://" + host + path)
	if err != nil {
		ir.t.Fatalf("the device got no answer from the proxy for %s%s: %v", host, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	n := 1
	if resp.StatusCode == http.StatusBadGateway {
		n = 2
	}
	return upstreamOutcome{status: resp.StatusCode, body: string(b), flows: ir.gw.waitFlows(ir.t, len(before)+n)[len(before):]}
}

// wantServed and wantRefused check one request: served is the server's answer, reached by the request, with no error reported; refused
// is the proxy's 502 that says the certificate failed, with the reason in both reports, and the server never saw a request.
func (ir *interceptRig) wantServed(host, path string) {
	ir.t.Helper()
	hits := ir.upHits.Load()
	o := ir.ask(host, path)
	if o.status != http.StatusOK || o.body != "hello from upstream "+path || ir.upHits.Load() != hits+1 || len(o.flows) != 1 || o.flows[0]["error"] != nil {
		ir.t.Errorf("%s%s was expected to be served: %d %q, reports %v, %d requests reached the server", host, path, o.status, o.body, o.flows, ir.upHits.Load()-hits)
	}
}

func (ir *interceptRig) wantRefused(host, path, reason string) upstreamOutcome {
	ir.t.Helper()
	hits := ir.upHits.Load()
	o := ir.ask(host, path)
	if o.status != http.StatusBadGateway || ir.upHits.Load() != hits || len(o.flows) != 2 {
		ir.t.Errorf("%s%s was expected to be refused: %d, reports %v, %d requests reached the server", host, path, o.status, o.flows, ir.upHits.Load()-hits)
		return o
	}
	failed, exchange := o.flows[0], o.flows[1]
	if e, _ := failed["error"].(string); failed["protocol"] != "tcp" || !strings.HasPrefix(e, "TLS to the server failed: Certificate verify failed: ") || !strings.Contains(e, reason) {
		ir.t.Errorf("the report of the failed connection: %v (want %q)", failed, reason)
	}
	if e, _ := exchange["error"].(string); exchange["path"] != path || exchange["status"] != nil || !strings.HasPrefix(e, "Certificate verify failed: ") {
		ir.t.Errorf("the report of the exchange: %v", exchange)
	}
	return o
}

// policies sets the policy of the case and waits until the proxy works with it.
func (ir *interceptRig) policy(p string) {
	ir.t.Helper()
	ir.gw.setUpstream(p)
	ir.gw.waitApplied(ir.t)
}

// With nothing given to the proxy but its bundle, a server whose certificate no CA of that bundle signed is refused: by default, with
// the policy verify said aloud, and with own_ca, which has no CA file to go by (and says so). With the policy insecure it is served.
func TestAServerWhoseCertificateIsNotValidIsRefusedByDefaultAndServedWithInsecure(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none"})
	ir.gw.waitApplied(t)

	// no policy: the installation's, which is to verify
	o := ir.wantRefused(hostName, "/default", "self-signed certificate")
	if !strings.Contains(o.body, "Certificate verify failed") {
		t.Errorf("the device is told why: %q", o.body)
	}
	if hs := ir.gw.waitHandshakes(t, 1, "accepted"); hs[0].Case == nil || *hs[0].Case != "intercept" {
		t.Errorf("the device's own handshake with the proxy went through: %+v", hs[0])
	}
	ir.policy("verify")
	ir.wantRefused(hostName, "/verify", "self-signed certificate")
	ir.policy("own_ca")
	ir.wantRefused(hostName, "/own", "CHAOSGW_UPSTREAM_CA_FILE")

	ir.policy("insecure")
	ir.wantServed(hostName, "/insecure")
	ir.policy("verify")
	ir.wantRefused(hostName, "/again", "self-signed certificate") // the next configuration takes the policy back
}

// The installation names a CA file with the server's certificate (a lab with a private CA): that is what a case without a policy and one
// with own_ca verify against, a case that says verify wants the bundle and refuses the server, and insecure serves it.
func TestACaseCanAskForTheBundleWhereTheInstallationNamesACAFile(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	ir.wantServed(hostName, "/default")
	ir.policy("own_ca")
	ir.wantServed(hostName, "/own")
	ir.policy("verify")
	ir.wantRefused(hostName, "/verify", "self-signed certificate")
	ir.policy("insecure")
	ir.wantServed(hostName, "/insecure")
}

// A CA file that did not sign the server verifies nothing about it: the default and own_ca refuse the server, insecure serves it. The
// file is the only trust of own_ca (not the bundle's, not the test CA's, which signs the device's certificate and no server's).
func TestOwnCAIsTheCAFileOfTheInstallationAndNoOtherCA(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "other"})
	ir.gw.waitApplied(t)
	ir.wantRefused(hostName, "/default", "self-signed certificate")
	ir.policy("own_ca")
	ir.wantRefused(hostName, "/own", "self-signed certificate")
	ir.policy("insecure")
	ir.wantServed(hostName, "/insecure")
}

// CHAOSGW_UPSTREAM_INSECURE=1 is the policy of every case without one; a case can still ask to be verified, against the bundle (verify) or
// against a CA file the installation has named as well (own_ca; this installation has none, so that case is refused with the reason).
func TestTheInstallationThatVerifiesNothingIsOverriddenByACase(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none", upstreamInsecure: true})
	ir.gw.waitApplied(t)
	ir.wantServed(hostName, "/default")
	ir.policy("insecure")
	ir.wantServed(hostName, "/insecure")
	ir.policy("verify")
	ir.wantRefused(hostName, "/verify", "self-signed certificate")
	ir.policy("own_ca")
	ir.wantRefused(hostName, "/own", "CHAOSGW_UPSTREAM_CA_FILE")
}

func TestOwnCAOfAnInstallationThatVerifiesNothingIsTheCAFileItNames(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamInsecure: true}) // a CA file with the server's certificate, and no verification
	ir.gw.waitApplied(t)
	ir.wantServed(hostName, "/default")
	ir.policy("own_ca")
	ir.wantServed(hostName, "/own")
	ir.policy("verify")
	ir.wantRefused(hostName, "/verify", "self-signed certificate")
}

// A certificate for another name than the one the device asked for is not valid either: the proxy verifies the name it was asked for (the
// server name of the device's ClientHello) against the server's certificate, whatever CA file it has.
func TestAServerWhoseCertificateIsForAnotherNameIsRefusedUnlessTheCaseSaysInsecure(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	ir.wantServed(hostName, "/right-name")
	ir.wantRefused("wrong.example.net", "/wrong-name", "hostname mismatch")
	ir.policy("own_ca")
	ir.wantRefused("wrong.example.net", "/wrong-name", "hostname mismatch")
	ir.policy("insecure")
	ir.wantServed("wrong.example.net", "/wrong-name")
}

// Two cases are in force at once, each with its policy, and the connections of a device are judged by the case that decided them: the
// point of the policy being a field of the case and not a setting of the installation.
func TestCasesWithDifferentPoliciesAreInForceAtTheSameTime(t *testing.T) {
	const strict, lax, ownCA = "44444444-4444-4444-8444-444444444441", "44444444-4444-4444-8444-444444444442", "44444444-4444-4444-8444-444444444443"
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.setOthers(map[string]string{strict: "verify", lax: "insecure", ownCA: "own_ca"})
	ir.gw.waitApplied(t)
	for i, step := range []struct {
		overlay string
		served  bool
	}{{strict, false}, {lax, true}, {ownCA, true}, {interceptOverlay, true}, {strict, false}, {lax, true}} {
		ir.overlay.Store(step.overlay)
		path := "/" + strings.Repeat("x", i+1)
		if step.served {
			ir.wantServed(hostName, path)
		} else {
			ir.wantRefused(hostName, path, "self-signed certificate")
		}
	}
	for _, f := range ir.gw.waitFlows(t, 6) {
		if f["overlay"] == nil {
			t.Errorf("an exchange without the overlay that decided it: %v", f)
		}
	}
}

// A connection that is already open keeps what it was made with; the next connection follows the configuration. (A new policy does not need
// the proxy to restart.)
func TestANewPolicyIsInForceForTheNextConnectionWithoutRestartingTheProxy(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none"})
	ir.gw.waitApplied(t)
	ir.policy("insecure")
	dev := ir.device(ir.roots, nil)
	if _, body, err := ir.get(dev, "/kept"); err != nil || body != "hello from upstream /kept" {
		t.Fatalf("%v %q", err, body)
	}
	ir.policy("verify")
	// the connection of the device and the proxy's own connection to the server are both kept alive: no new TLS to the server is made
	if resp, body, err := ir.get(dev, "/kept-too"); err != nil || resp.StatusCode != http.StatusOK || body != "hello from upstream /kept-too" {
		t.Errorf("a connection that is open: %v %v %q", err, resp, body)
	}
	ir.gw.waitFlows(t, 2) // the reports of the two exchanges, before the next request is counted
	ir.wantRefused(hostName, "/new", "self-signed certificate")
}

// The connection to the server whose verification a case changed is still in the key log: the secrets of the proxy's own connection reach the
// gateway (they decrypt a capture of the uplink), as with the policy of the installation, though the context of the connection was replaced.
func TestTheKeyLogHoldsTheSecretsOfTheProxysConnectionWhenACaseChangedHowItIsVerified(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamCA: "none"})
	ir.policy("insecure")
	var own lockedWriter
	dev := ir.device(ir.roots, &own)
	if _, body, err := ir.get(dev, "/secret-material"); err != nil || body != "hello from upstream /secret-material" {
		t.Fatalf("%v %q", err, body)
	}
	dev.Transport.(*http.Transport).CloseIdleConnections() // the connection ends, the proxy flushes what is left
	deviceLines := nonEmptyLines(own.String())
	if len(deviceLines) < 3 {
		t.Fatalf("the device wrote %d lines", len(deviceLines))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
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
		if missing == 0 && len(have) > len(deviceLines) {
			return // the device's lines, and more: those of the connection to the server
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the proxy's key log lacks the secrets of one of the two connections (the device holds %d lines)", len(deviceLines))
}

// A policy that cannot be applied does not fall back to a connection nobody verified: the CA file of the installation is gone when a case asks
// for own_ca (the proxy reads it for the case), so the connection is refused and the exchange says that the policy could not be applied. The
// other policies and a case without one are not affected, and the file coming back is enough for own_ca to serve the server again.
func TestACaseWhoseCAFileCannotBeReadIsRefusedAndNeverServedUnverified(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{upstreamInsecure: true}) // the installation has a CA file, and verifies nothing
	ir.gw.waitApplied(t)
	ir.policy("own_ca")
	ir.wantServed(hostName, "/readable")
	pem, err := os.ReadFile(ir.upstreamCAFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ir.upstreamCAFile); err != nil {
		t.Fatal(err)
	}
	o := ir.wantRefused(hostName, "/gone", "could not be applied")
	if !strings.Contains(o.body, "Certificate verify failed") {
		t.Errorf("the device is told why: %q", o.body)
	}
	ir.policy("insecure")
	ir.wantServed(hostName, "/insecure")
	ir.policy("verify")
	ir.wantRefused(hostName, "/verify", "self-signed certificate")
	if err := os.WriteFile(ir.upstreamCAFile, pem, 0o644); err != nil {
		t.Fatal(err)
	}
	ir.policy("own_ca")
	ir.wantServed(hostName, "/back")
}
