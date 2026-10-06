//go:build testbed

package executor_test

import (
	"context"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// TestZZConcatMapBisection is a temporary diagnostic (M7 level-1b kernel investigation): every
// real-kernel apply fails on the level 1b (VM, kernel 6.8.0-142-generic) job with "Could not
// process rule: Operation not supported" once the classify chain's guard set and four
// classification maps exist in the ruleset, three of them keyed by a concatenation of fields.
// This isolates each map's "add map" statement on its own, one per case, to find out which
// concatenation (if any) kernel 6.8 actually rejects, since nft's own error carries no further
// detail and dmesg logs nothing for this errno.
func TestZZConcatMapBisection(t *testing.T) {
	g := startGateway(t)
	cases := []struct {
		name string
		typ  string
	}{
		{"single_addr", `"ipv4_addr"`},
		{"two_addr", `["ipv4_addr","ipv4_addr"]`},
		{"addr_proto_port", `["ipv4_addr","inet_proto","inet_service"]`},
		{"two_addr_proto_port", `["ipv4_addr","ipv4_addr","inet_proto","inet_service"]`},
	}
	for _, c := range cases {
		extra := `,{"add":{"map":{"family":"inet","table":"chaosgw","name":"zz_` + c.name + `","type":` + c.typ + `,"map":"verdict"}}}`
		_, err := g.c.Do(context.Background(), &executor.NftApply{Target: tgt(g.ns), Ruleset: []byte(nftRuleset(extra))})
		if err != nil {
			t.Errorf("case %s (type %s): FAILED: %v", c.name, c.typ, err)
		} else {
			t.Logf("case %s (type %s): ok", c.name, c.typ)
		}
	}
}
