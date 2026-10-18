//go:build testbed

package kea

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

// The tests in this file need the real Kea as root: they carry the build tag testbed, so the testbed
// runner (level 1 and 1b) runs them and fails a run in which they skip, instead of a level-0 run on a
// machine without Kea skipping them unnoticed.

// #64: the real Kea takes the retries: an interface it cannot open a socket on (here the loopback,
// which Kea never serves) leaves it retrying instead of failed for good.
func TestKeaRetriesASocketItCouldNotOpen(t *testing.T) {
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo"
	cfg.Subnets[0].Subnet = netip.MustParsePrefix("127.0.0.0/8")
	cfg.Subnets[0].Pools = []Pool{{netip.MustParseAddr("127.0.0.100"), netip.MustParseAddr("127.0.0.200")}}
	cfg.Subnets[0].Reservations = nil
	cfg.Subnets[0].Router = netip.MustParseAddr("127.0.0.1")
	cfg.Subnets[0].DNS, cfg.Subnets[0].NTP = nil, nil
	cfg.Script = ""
	c := startKea(t, cfg)
	a, err := c.Do(context.Background(), "status-get", nil)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Sockets struct {
			Status string   `json:"status"`
			Errors []string `json:"errors"`
		} `json:"sockets"`
	}
	if err := json.Unmarshal(a.Arguments, &st); err != nil {
		t.Fatal(err)
	}
	if st.Sockets.Status != "retrying" || len(st.Sockets.Errors) == 0 || !strings.Contains(st.Sockets.Errors[0], "lo") {
		t.Errorf("%s", a.Arguments)
	}
}
