package api_test

import (
	"testing"
)

// Every page of the NTP query log names the gateway's own time to the nanosecond (`now`): `chaosctl wait ntp` starts at it, on
// the clock the entries are stamped with, and not at the Date header, which is rounded down to the second.
func TestEveryPageOfTheNTPQueryLogNamesTheGatewaysTimeToTheNanosecond(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	clk.Advance(1234567 * 1000) // 1.234567 s
	want := clk.Now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	g.asAdmin(admin, func() {
		for _, path := range []string{"/ntp/queries", "/ntp/queries?device=esp32-42&limit=1"} {
			got := g.do("GET", path, nil, nil, nil).json(t)
			if got["now"] != want {
				t.Errorf("%s: now %v, want %s", path, got["now"], want)
			}
		}
	})
}
