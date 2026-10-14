package linux

import (
	"strings"
	"testing"
	"time"
)

// P3-M13-01: an access rule notes when it last matched with the `last` statement; `nft -j list` prints
// {"last": {"used": <ms>}} for a rule that matched and {"last": null} for one that did not. The rule
// stands in the forward and the input hook with one named counter: the most recent match counts.
const lastListing = `{"nftables":[
  {"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak #3","json_schema_version":1}},
  {"table":{"family":"inet","name":"chaosgw","handle":1}},
  {"counter":{"family":"inet","table":"chaosgw","name":"rule_a","handle":2,"packets":3,"bytes":180}},
  {"counter":{"family":"inet","table":"chaosgw","name":"rule_b","handle":3,"packets":0,"bytes":0}},
  {"chain":{"family":"inet","table":"chaosgw","name":"access_forward","handle":4}},
  {"rule":{"family":"inet","table":"chaosgw","chain":"access_forward","handle":5,"comment":"c1","expr":[{"counter":"rule_a"},{"last":{"used":5300}},{"drop":null}]}},
  {"rule":{"family":"inet","table":"chaosgw","chain":"access_forward","handle":6,"comment":"c2","expr":[{"counter":"rule_b"},{"last":null},{"accept":null}]}},
  {"chain":{"family":"inet","table":"chaosgw","name":"access_input","handle":7}},
  {"rule":{"family":"inet","table":"chaosgw","chain":"access_input","handle":8,"comment":"c1","expr":[{"counter":"rule_a"},{"last":{"used":61001}},{"drop":null}]}},
  {"rule":{"family":"inet","table":"chaosgw","chain":"access_input","handle":9,"comment":"c2","expr":[{"counter":"rule_b"},{"last":null},{"accept":null}]}},
  {"rule":{"family":"inet","table":"chaosgw","chain":"access_input","handle":10,"comment":"c3","expr":[{"counter":{"packets":1,"bytes":60}},{"accept":null}]}}
]}`

func TestTheLastStatementOfARuleIsReadByItsNamedCounter(t *testing.T) {
	rs, err := ParseNft([]byte(lastListing))
	if err != nil {
		t.Fatal(err)
	}
	fwd := rs.Rules("access_forward")
	if fwd[0].CounterName() != "rule_a" || fwd[1].CounterName() != "rule_b" {
		t.Errorf("counters: %q %q", fwd[0].CounterName(), fwd[1].CounterName())
	}
	if used, set, has := fwd[0].Last(); !has || !set || used != 5300*time.Millisecond {
		t.Errorf("a rule that matched: %v %v %v", used, set, has)
	}
	if _, set, has := fwd[1].Last(); !has || set {
		t.Errorf("a rule that never matched: set %v has %v", set, has)
	}
	in := rs.Rules("access_input")
	if _, _, has := in[2].Last(); has || in[2].CounterName() != "" {
		t.Errorf("an anonymous counter without last: %q %v", in[2].CounterName(), has)
	}
	// the most recent of both hooks, and nothing for a counter whose rules never matched
	if got := rs.LastUsed(); len(got) != 1 || got["rule_a"] != 5300*time.Millisecond {
		t.Errorf("LastUsed = %v", got)
	}
	// per chain, for the transaction that carries the times over
	got := rs.LastUsedIn()
	if len(got) != 2 || got[LastKey("access_forward", "rule_a")] != 5300*time.Millisecond || got[LastKey("access_input", "rule_a")] != 61001*time.Millisecond {
		t.Errorf("LastUsedIn = %v", got)
	}
	var nilRS *Ruleset
	if len(nilRS.LastUsed()) != 0 || len(nilRS.LastUsedIn()) != 0 {
		t.Error("a nil ruleset has no last match")
	}
}

// The technical view prints the statement as nft does: `last used 1m1s1ms`, `last used never`.
func TestRenderNftPrintsTheLastStatementAsNftDoes(t *testing.T) {
	rs, err := ParseNft([]byte(lastListing))
	if err != nil {
		t.Fatal(err)
	}
	got := RenderNft(rs, 0)
	for _, want := range []string{
		`counter name "rule_a" last used 5s300ms drop`,
		`counter name "rule_b" last used never accept`,
		`counter name "rule_a" last used 1m1s1ms drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for ms, want := range map[int64]string{0: "0ms", 350: "350ms", 1000: "1s", 3_600_000 + 2000: "1h2s", 86_400_000 + 1: "1d1ms"} {
		if got := nftTime(ms); got != want {
			t.Errorf("nftTime(%d) = %q, want %q", ms, got, want)
		}
	}
}
