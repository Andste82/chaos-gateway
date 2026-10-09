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
	// the access rules (M9): the access chains, the source sets, the counters, reject with icmpx and
	// with tcp reset, the cut chains
	for name, tg := range accessScenarios(t) {
		out[name] = tg
	}
	return out
}
