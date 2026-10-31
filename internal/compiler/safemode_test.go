package compiler

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
)

// The safe configuration of M27 (plan §3.9) compiles: no test network, so no bridge, no tunnel, no probe and no rule that names
// an interface of a test network; the management network keeps its normal path to the uplink (plan §2.14), which is not
// forwarding between test networks or from them.
func TestTheSafeModeConfigurationCompilesToNoTestNetworkAtAll(t *testing.T) {
	cfg := loadConfig(t, "gateway.yaml")
	tg := Compile(Input{Config: domain.SafeModeConfiguration(cfg), Host: testbedHost(), Generation: Generation{Revision: 0, Seq: 7}})
	if tg.HasErrors() {
		t.Fatalf("problems: %+v", tg.Problems)
	}
	if len(tg.Bridges) != 0 || len(tg.WireGuard) != 0 || len(tg.Probes) != 0 || tg.Service != nil {
		t.Errorf("bridges %v, tunnels %v, probes %v, service %v", tg.Bridges, tg.WireGuard, tg.Probes, tg.Service)
	}
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"br-iot", "br-lab", "lan0", "lan1", "masquerade"} {
		if strings.Contains(string(tx), name) {
			t.Errorf("the safe configuration's rules mention %q", name)
		}
	}
	// the full configuration does name them, so the check above can fail
	full := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 7}})
	ftx, err := full.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ftx), "br-iot") || !strings.Contains(string(ftx), "masquerade") {
		t.Error("the full configuration does not name a test network's bridge or a masquerade: the check above checks nothing")
	}
}
