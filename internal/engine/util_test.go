package engine_test

import (
	"testing"

	"encoding/json"
	"net/netip"
	"strconv"

	"github.com/Andste82/chaos-gateway/internal/model"
)

type netipPrefix = netip.Prefix

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func itoa(n int) string                { return strconv.Itoa(n) }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func cloneCfg(t *testing.T, c *model.Configuration) *model.Configuration {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var out model.Configuration
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}
