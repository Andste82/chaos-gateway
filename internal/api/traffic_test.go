package api_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Issue #63: GET /uplink/traffic returns the rates the API sampled from the uplink's byte counters on
// its own clock, every interval (5 s), for the period (10 min), and with `since` only the newer ones.
func TestTheUplinkTrafficIsSampledByTheAPIAndKeptForThePeriod(t *testing.T) {
	dir := t.TempDir()
	stats := filepath.Join(dir, "wan0", "statistics")
	if err := os.MkdirAll(stats, 0o755); err != nil {
		t.Fatal(err)
	}
	counters := func(rx, tx int64) {
		t.Helper()
		for f, v := range map[string]int64{"rx_bytes": rx, "tx_bytes": tx} {
			if err := os.WriteFile(filepath.Join(stats, f), []byte(strconv.FormatInt(v, 10)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	counters(1000, 500)
	defer api.SetSysClassNet(dir)()
	clk := clock.NewFake(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()

	// nothing sampled yet: an empty list, not null
	tr := g.do("GET", "/uplink/traffic", nil, nil, nil).json(t)
	if s, ok := tr["samples"].([]any); !ok || len(s) != 0 || tr["interval_seconds"] != float64(5) || tr["period_seconds"] != float64(600) {
		t.Fatalf("%v", tr)
	}

	clk.Advance(5 * time.Second) // the first reading of wan0
	counters(1000+125_000, 500+12_500)
	clk.Advance(4 * time.Second) // not yet a reading
	if s := g.do("GET", "/uplink/traffic", nil, nil, nil).json(t)["samples"].([]any); len(s) != 0 {
		t.Fatalf("a reading before the interval: %v", s)
	}
	clk.Advance(time.Second)
	tr = g.do("GET", "/uplink/traffic", nil, nil, nil).json(t)
	s := tr["samples"].([]any)
	if len(s) != 1 || tr["interface"] != "wan0" || tr["counters"] != true {
		t.Fatalf("%v", tr)
	}
	first := s[0].(map[string]any)
	if first["down_bps"] != float64(200_000) || first["up_bps"] != float64(20_000) || first["time"] != "2026-10-17T12:00:10Z" {
		t.Errorf("%v", first)
	}
	if s := g.do("GET", "/uplink/traffic?since="+url.QueryEscape(first["time"].(string)), nil, nil, nil).json(t)["samples"].([]any); len(s) != 0 {
		t.Errorf("since the newest sample: %v", s)
	}

	// eleven minutes later the first sample is gone; a period's worth of readings is kept
	clk.Advance(11 * time.Minute)
	s = g.do("GET", "/uplink/traffic", nil, nil, nil).json(t)["samples"].([]any)
	if len(s) != 120 {
		t.Fatalf("%d samples in 10 minutes of 5 s", len(s))
	}
	if s[0].(map[string]any)["time"] == first["time"] || s[0].(map[string]any)["down_bps"] != float64(0) {
		t.Errorf("the oldest sample: %v", s[0])
	}
	if s := g.do("GET", "/uplink/traffic?since="+url.QueryEscape(s[118].(map[string]any)["time"].(string)), nil, nil, nil).json(t)["samples"].([]any); len(s) != 1 {
		t.Errorf("since the one before the newest: %v", s)
	}
}
