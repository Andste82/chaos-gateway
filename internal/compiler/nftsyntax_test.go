package compiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// nft parses and evaluates a ruleset before it talks to the kernel: a JSON expression it does not
// understand is rejected with a message of its own, while a valid one gets as far as the kernel
// (which an unprivileged process is refused: "Operation not permitted"). Privileged, `-c` checks
// the whole transaction against the kernel without applying it.
func TestEveryTransactionIsAcceptedByNftsParser(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft is not installed")
	}
	for name, tg := range transactionScenarios(t) {
		tx, err := tg.Nft.Transaction(nil)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(nft, "-j", "-c", "-f", "-")
		cmd.Stdin = bytes.NewReader(tx)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err = cmd.Run()
		msg := stderr.String()
		if err == nil || strings.Contains(msg, "Operation not permitted") {
			continue // parsed and evaluated, and then the kernel was the next step
		}
		t.Errorf("%s: nft rejects the ruleset:\n%s", name, msg)
	}
}

// transactionScenarios are the configurations whose nftables transaction must be valid. The parser
// test above and the kernel test (nftkernel_test.go, build tag testbed) run the same set: a
// scenario added here is checked against the real kernel too.
func transactionScenarios(t *testing.T) map[string]*Target {
	t.Helper()
	out := map[string]*Target{
		"routed":    compileBasic(t, nil),
		"twoport":   compileBasic(t, twoPortMod),
		"wireguard": compileWG(t, nil),
		"routing":   withRouting(t, nil),
		"service":   withService(t),
		"dhcp":      compileBasic(t, func(c *model.Configuration, _ *Host) { withDHCP(c, iotNet, &model.DhcpScope{}) }),
	}
	// the fault scenarios (M8a): interval maps with ranges and prefixes, the per-id chains with
	// their counters, mark_0
	for name, tg := range faultScenarios(t) {
		out[name] = tg
	}
	// the MTU family (M10): the second lookup, jumped to from the classify chain, and the three modes
	out["pmtu"] = scenarioPMTU(t)
	// the tunnel faults and the WireGuard actions (M10): the output hook with its endpoint map, the chains of the faults,
	// the blocked endpoints at raw priority on output and input
	out["tunnel"] = scenarioTunnel(t)
	// profiles (M11): the MTU part of a profile next to the impairment parts that faults replace
	out["profiles-mtu"] = scenarioProfilePrecedence(t, true)
	// the access rules (M9): the access chains, the source sets, the counters, reject with icmpx and
	// with tcp reset, the cut chains
	for name, tg := range accessScenarios(t) {
		out[name] = tg
	}
	// the two-port guard in front of the access rules (P2-M9-11)
	out["access-twoport"] = scenarioAccessTwoPort(t)
	// the guard of the WireGuard networks of the role management in both topologies (P31-AUDIT-11)
	out["access-threeport-wg"] = adminWG(t, false)
	out["access-twoport-wg"] = adminWG(t, true)
	// the order of a specific entry and the management entry (the review of 2026-10-18)
	out["access-threeport-wg-specific-deny"] = adminWGWith(t, false, specificDeny)
	out["access-twoport-wg-specific-allow"] = adminWGWith(t, true, specificAllow)
	// the port forwards (M35a): the DNAT map with its concatenated value, the DNAT through it next to the
	// service redirect, the accept rules with `ct status dnat`, the responder forms of the access rules
	out["forward"] = scenarioForward(t)
	// hostnames (M20): the DNS-derived sets (a concatenated type with a timeout), the hostname faults in front of
	// the lookup levels and next to the MTU lookup, a hostname rule and its responder form
	out["hostnames"] = scenarioHostnames(t)
	// the hardcoded resolvers of a network (M20): the DNAT of every other destination of port 53 and the DoT chain
	out["dns-network"] = compileDNSNet(t, true, true, true)
	// the TLS cases (M21): the chain that selects connections for the responder (conntrack mark, service mark, a
	// hostname set, the uplink's set shared with an access rule) and the accept towards the service namespace
	out["tls"] = scenarioTLS(t)
	// the interception cases (M22): the UDP 443 drop of block_quic next to the selection of the connections
	out["intercept"] = scenarioIntercept(t)
	return out
}
