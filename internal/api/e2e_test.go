//go:build testbed

package api_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// bedGW is the API on the real executor in the namespaces of the testbed.
func newBedGW(t *testing.T, extra ...func(*options)) (*gw, *testbed.Topology) {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false), testbed.WithRemotes(true))
	g := newGW(t, append([]func(*options){func(o *options) { o.runner = executor.NewExecRunner(); o.namespace = top.GW.Name }}, extra...)...)
	raw, err := os.ReadFile("../engine/testdata/testbed_wg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	g.finishSetupWith(map[string]any{"admin_password": adminPassword, "configuration": toMap(t, cfg)})
	return g, top
}

func pingOK(from *testbed.Namespace, src, dst string) bool {
	args := []string{"-c", "2", "-W", "1", "-n"}
	if src != "" {
		args = append(args, "-I", src)
	}
	_, err := from.Run(context.Background(), "ping", append(args, dst)...)
	return err == nil
}

// M5 test: a gateway that is configured only through the API carries traffic, and a WireGuard client
// created through the API brings up a tunnel from the configuration it downloads.
func TestConfigureThroughTheAPIAndTrafficFlows(t *testing.T) {
	g, top := newBedGW(t)

	// the setup applied revision 1: a test-network device reaches the server through the gateway
	if !pingOK(top.A, "", testbed.ServerAddr) {
		t.Fatalf("A cannot reach the server through the gateway configured by the setup\n%s", top.GW.Must("nft", "list", "ruleset"))
	}
	// the state tells what was applied
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["active_revision"] != float64(1) || st["last_apply"].(map[string]any)["result"] != "ok" {
		t.Fatalf("%v", st)
	}

	// create a client through the API: merge patch, preview, apply
	id := g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"}},
	}}}})
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil).json(t)
	if len(pv["domain"].([]any)) == 0 {
		t.Errorf("%v", pv)
	}
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}

	// download the configuration and bring the tunnel up in the remote client's namespace
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), "PrivateKey = ") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	top.RC.Must("wg-quick", "up", conf)
	if !pingOK(top.RC, "", "10.99.0.1") {
		t.Fatalf("the downloaded configuration does not bring up a working tunnel\n%s\n%s", top.GW.Must("wg", "show"), top.RC.Must("wg", "show"))
	}

	// the engine sees the handshake: the client is online in the API
	if err := g.e.PollWireGuard(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	online := false
	for time.Now().Before(deadline) && !online {
		c := g.do("GET", "/networks/lab-hub/clients/rC", nil, nil, nil).json(t)
		online = c["status"].(map[string]any)["online"] == true
		time.Sleep(500 * time.Millisecond)
	}
	if !online {
		t.Error("the client is not online in the API after its handshake")
	}

	// the audit log has what was done, and the export was recorded
	items, _, _ := g.log.List(auditFilter(), "", 50)
	var actions []string
	for _, e := range items {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"setup.complete", "revision.create", "revision.apply", "wireguard.export"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Errorf("no %s in the audit log: %v", want, actions)
		}
	}
}
