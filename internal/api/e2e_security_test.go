//go:build testbed

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M29, regression of M4 on the real kernel with the real HTTPS server: the UI and the API are reached from the management
// network and from nowhere else. A device in a test network and a client of a WireGuard network with the role `test` (through its
// tunnel) cannot open a connection to the port of the API on any address of the gateway, while the management network gets the
// health check over TLS with the certificate of the gateway, and a certificate installed with PUT /system/certificate is the one
// it is shown from then on. The compiler's input chain is pinned by the level 0 tests (TestAManagementAddressOnATestNetwork...);
// this test puts the whole server behind it.

// httpsGet fetches /api/v1/system/health over TLS from a namespace, without verifying the certificate, and returns the status
// code and the SHA-256 fingerprint of the certificate it was shown. A connection that is dropped or refused is an error.
func httpsGet(ns *testbed.Namespace, host string, port int) (status int, fingerprint string, err error) {
	out, err := ns.Run(context.Background(), "python3", "-c", fmt.Sprintf(`import hashlib, socket, ssl, sys
try:
    raw = socket.create_connection(("%s", %d), timeout=3)
    ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
    s = ctx.wrap_socket(raw, server_hostname="%s")
    der = s.getpeercert(binary_form=True)
    s.sendall(b"GET /api/v1/system/health HTTP/1.1\r\nHost: gw\r\nConnection: close\r\n\r\n")
    data = b""
    while True:
        b = s.recv(4096)
        if not b: break
        data += b
    print(data.split(b" ")[1].decode(), hashlib.sha256(der).hexdigest())
except Exception as e:
    print("error", e, file=sys.stderr); sys.exit(1)
`, host, port, host))
	if err != nil {
		return 0, "", err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, "", fmt.Errorf("unexpected output %q", out)
	}
	_, _ = fmt.Sscanf(f[0], "%d", &status)
	return status, f[1], nil
}

func TestTheUIAndTheAPIAreReachedFromTheManagementNetworkOnlyOnTheRealKernel(t *testing.T) {
	g, top := newBedGW(t)
	port := 443
	if cfg := g.e.Snapshot().Config; cfg != nil && cfg.Management.UiPort != nil && *cfg.Management.UiPort > 0 {
		port = *cfg.Management.UiPort
	}
	// the server as the daemon runs it: the TLS configuration of the certificates, in the namespace of the gateway, on every address
	l, err := testbed.ListenIn(top.GW.Name, "tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: g.srv.Handler(), TLSConfig: g.certs.TLSConfig(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.ServeTLS(l, "", "") }()
	t.Cleanup(func() { _ = hs.Close() })

	// a client of a WireGuard network with the role test, from its tunnel, and a second test network on lan1: the configuration of
	// the bed has only lan0, so device C had no address to reach at the gateway and its refusal below proved nothing (the positive
	// control of the loop found it)
	const lab1ID = "5d3a8e6f-2b1c-4d7e-9f08-a1b2c3d4e5f6"
	id := g.mustPatch(map[string]any{"networks": map[string]any{
		lab1ID: map[string]any{"type": "lan", "name": "Lab", "address": testbed.LAN1Gateway + "/24",
			"interfaces": []any{map[string]any{"name": "lan1", "mac": testbed.GWLan1MAC}}},
		hubID: map[string]any{"clients": map[string]any{
			"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"}},
		}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	top.RC.Must("wg-quick", "up", conf)
	waitFor(t, 30*time.Second, "the tunnel carries traffic", func() bool { return pingOK(top.RC, "", "10.99.0.1") })

	// the management network reaches it, and is shown the gateway's own certificate
	own := g.certs.Info()
	var status int
	var fp string
	waitFor(t, 20*time.Second, "the management network reaches the API over TLS", func() bool {
		status, fp, err = httpsGet(top.Mgmt, testbed.MgmtGateway, port)
		return err == nil
	})
	if status != 200 || fp != *own.FingerprintSha256 {
		t.Errorf("from the management network: %d, certificate %s, want 200 and %s", status, fp, *own.FingerprintSha256)
	}
	// the loopback of the gateway itself too: the server is up, so that a refusal below is the firewall's
	if status, _, err := httpsGet(top.GW, "127.0.0.1", port); err != nil || status != 200 {
		t.Fatalf("the server does not answer on the loopback of the gateway: %d %v", status, err)
	}

	// no test network, no device and no test-role client reaches it, on any address of the gateway
	for _, c := range []struct {
		name string
		ns   *testbed.Namespace
		host string
	}{
		{"device A", top.A, testbed.LAN0Gateway},
		{"device B", top.B, testbed.LAN0Gateway},
		{"device C", top.C, testbed.LAN1Gateway},
		{"device A to the management address", top.A, testbed.MgmtGateway},
		{"the test-role WireGuard client", top.RC, "10.99.0.1"},
		{"the server behind the uplink", top.Server, testbed.UplinkGateway},
	} {
		// the positive control: the source reaches the gateway at this address with something that is allowed (the gateway answers
		// ICMP echo to the test networks and the uplink), so that a failed connection below is the firewall's refusal of the port and
		// not a source without a path, a missing address or a namespace that is down
		if !pingOK(c.ns, "", c.host) {
			t.Errorf("%s does not reach the gateway at %s with a ping: the refusal of the port below would prove nothing", c.name, c.host)
			continue
		}
		if status, _, err := httpsGet(c.ns, c.host, port); err == nil {
			t.Errorf("%s reached the API at %s:%d: HTTP %d\n%s", c.name, c.host, port, status, top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "input"))
		}
	}

	// an installed certificate is the one the management network is shown from the next connection on
	in := newCert(t, certSpec{ips: []net.IP{net.ParseIP(testbed.MgmtGateway)}})
	if r := put(g, in); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	sum := sha256.Sum256(in.der)
	if _, fp, err := httpsGet(top.Mgmt, testbed.MgmtGateway, port); err != nil || fp != hex.EncodeToString(sum[:]) {
		t.Errorf("after the installation the management network is shown %s (%v), want %x", fp, err, sum)
	}
	// ... and the loopback, where the gateway's services verify the gateway's own certificate, keeps showing that one
	if _, fp, err := httpsGet(top.GW, "127.0.0.1", port); err != nil || fp != *own.FingerprintSha256 {
		t.Errorf("on the loopback the certificate is %s (%v), want the gateway's own %s", fp, err, *own.FingerprintSha256)
	}
}
