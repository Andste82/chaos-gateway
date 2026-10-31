package executor

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The fuzz targets start from seeds (M29, the review of the executor interface): a type of operation without one is fuzzed from
// nothing, and the fuzzer has to find its shape by chance. Every type the decoder knows has at least one seed that the decoder
// accepts, so a new operation cannot join the interface without a starting point for the nightly run.
func TestEveryOperationTypeHasAFuzzSeedThatTheDecoderAccepts(t *testing.T) {
	src, err := os.ReadFile("op.go")
	if err != nil {
		t.Fatal(err)
	}
	types := regexp.MustCompile(`(?m)^\s+Type[A-Za-z]+\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(types) < 20 {
		t.Fatalf("found %d operation types in op.go", len(types))
	}
	for _, m := range types {
		accepted := false
		for _, s := range fuzzSeeds {
			if !strings.Contains(s, `"type":"`+m[1]+`"`) {
				continue
			}
			if _, err := Decode([]byte(s)); err == nil {
				accepted = true
				break
			}
		}
		if !accepted {
			t.Errorf("no fuzz seed of the type %q is accepted by the decoder", m[1])
		}
	}
}
