package domain

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func timeSelectorOf(dest string) string {
	return TimeSelectorKey(model.TimeFault{Destination: &model.Destination{Cidr: ptr(dest)}, Offset: ptr("+1d")})
}

// The NTP server looks a fault up by the port a query arrived on, so the compiler has to resolve the precedence for
// every port of a group of sources beforehand: TimeWinner is that rule. A fault without a destination selector applies
// on every port, and rank decides between it and a fault for one server.
func TestTheWinnerOfARedirectPortIsTheFirstFaultOfTheRankThatNamesNoServerOrThisOne(t *testing.T) {
	const server, other = "203.0.113.7", "198.51.100.9"
	onlyOne := func(want string) func(string) bool {
		return func(sel string) bool { return sel == want }
	}
	for _, narrowIsNewer := range []bool{false, true} {
		name := "the fault for every server is newer"
		narrowAge, wideAge := time.Second, time.Minute // the larger age is the newer
		if narrowIsNewer {
			name, narrowAge, wideAge = "the fault for one server is newer", time.Minute, time.Second
		}
		t.Run(name, func(t *testing.T) {
			tw := newTestWorld(t, false)
			narrow := tw.overlay(`{target: {group: sensors}, time: {destination: {cidr: `+server+`}, offset: +1d}}`, narrowAge)
			wide := tw.overlay(`{target: {group: sensors}, time: {offset: -1d}}`, wideAge)
			ranked := tw.world().TimeCandidates(Source{Subject: subjectA})
			forServer, ok := TimeWinner(ranked, onlyOne(timeSelectorOf(server)))
			if !ok {
				t.Fatal("no winner for the port of the server")
			}
			forOthers, ok := TimeWinner(ranked, onlyOne(timeSelectorOf(other)))
			if !ok {
				t.Fatal("no winner for the port of another server")
			}
			general, ok := TimeWinner(ranked, func(string) bool { return false })
			if !ok {
				t.Fatal("no winner for the port 123")
			}
			// the fault without a selector decides on the port 123 and on the port of every other server whatever its age
			if forOthers.ID != wide.Id.String() || general.ID != wide.Id.String() {
				t.Errorf("another server and port 123: %s and %s, want the fault for every server %s", forOthers.ID, general.ID, wide.Id)
			}
			want := wide.Id.String()
			if narrowIsNewer {
				want = narrow.Id.String()
			}
			if forServer.ID != want {
				t.Errorf("the port of the server: %s, want %s", forServer.ID, want)
			}
		})
	}
	if _, ok := TimeWinner(nil, func(string) bool { return true }); ok {
		t.Error("an empty list has no winner")
	}
}

// A device that has only the fault for every server is served by it on the port of a server that other devices have a
// fault for: the compiler writes an entry for that port for it too.
func TestAFaultWithoutAServerAppliesOnThePortOfEveryOtherSelector(t *testing.T) {
	tw := newTestWorld(t, false)
	every := tw.overlay(`{target: {device: esp32-42}, time: {offset: -1d}}`, time.Second)
	ranked := tw.world().TimeCandidates(Source{Subject: subjectA})
	sel := timeSelectorOf("203.0.113.7")
	got, ok := TimeWinner(ranked, func(s string) bool { return s == sel })
	if !ok || got.ID != every.Id.String() {
		t.Fatalf("winner = %+v, %v", got, ok)
	}
}

func TestTheSelectorOfATimeFaultIsItsDestinationTogetherWithItsPorts(t *testing.T) {
	cidr := func(c string) *model.Destination { return &model.Destination{Cidr: ptr(c)} }
	for name, tt := range map[string]struct {
		a, b model.TimeFault
		same bool
	}{
		"no destination and the default port are none":     {model.TimeFault{}, model.TimeFault{Ports: &[]int{123}, Protocol: ptr(model.Protocol("udp"))}, true},
		"an address and its /32 are one":                   {model.TimeFault{Destination: cidr("203.0.113.7")}, model.TimeFault{Destination: cidr("203.0.113.7/32")}, true},
		"the order of the ports does not matter":           {model.TimeFault{Ports: &[]int{123, 1123}}, model.TimeFault{Ports: &[]int{1123, 123}}, true},
		"two servers are two":                              {model.TimeFault{Destination: cidr("203.0.113.7")}, model.TimeFault{Destination: cidr("203.0.113.8")}, false},
		"another port without a destination is a selector": {model.TimeFault{}, model.TimeFault{Ports: &[]int{1123}}, false},
		"a server and the same server on another port":     {model.TimeFault{Destination: cidr("203.0.113.7")}, model.TimeFault{Destination: cidr("203.0.113.7"), Ports: &[]int{1123}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := TimeSelectorKey(tt.a) == TimeSelectorKey(tt.b); got != tt.same {
				t.Errorf("%q and %q: same = %v", TimeSelectorKey(tt.a), TimeSelectorKey(tt.b), got)
			}
		})
	}
	if k := TimeSelectorKey(model.TimeFault{Offset: ptr("+1d")}); k != "" {
		t.Errorf("a fault for every server has no selector, got %q", k)
	}
}
