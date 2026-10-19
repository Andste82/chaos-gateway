package run

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func flowOf(id, state string, up, down int64) engine.Flow {
	return engine.Flow{ID: id, Protocol: "tcp", State: state, Upload: engine.Traffic{Packets: up}, Download: engine.Traffic{Packets: down}}
}

// The tracker tells a poll's news from the poll before: a connection that appears, one that becomes
// established, one that carries packets, and the same tuple again after its entry went.
func TestTheFlowTrackerReportsWhatChangedBetweenTwoPolls(t *testing.T) {
	var tr flowTracker
	kinds := func(evs []flowEvent) map[string]string {
		out := map[string]string{}
		for _, e := range evs {
			s := ""
			if e.created {
				s += "created "
			}
			if e.established {
				s += "established "
			}
			if e.traffic {
				s += "traffic"
			}
			out[e.flow.ID] = s
		}
		return out
	}
	eq := func(got map[string]string, want map[string]string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("got %v, want %v", got, want)
			return
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("got %v, want %v", got, want)
				return
			}
		}
	}
	eq(kinds(tr.update([]engine.Flow{flowOf("a", "SYN_SENT", 1, 0), flowOf("b", "ESTABLISHED", 5, 5)})),
		map[string]string{"a": "created traffic", "b": "created established traffic"})
	// nothing changed
	eq(kinds(tr.update([]engine.Flow{flowOf("a", "SYN_SENT", 1, 0), flowOf("b", "ESTABLISHED", 5, 5)})), map[string]string{})
	// a is answered; b only carries packets
	eq(kinds(tr.update([]engine.Flow{flowOf("a", "ESTABLISHED", 2, 1), flowOf("b", "ESTABLISHED", 6, 5)})),
		map[string]string{"a": "established traffic", "b": "traffic"})
	// b's entry goes and comes back, with fewer packets than it had
	eq(kinds(tr.update([]engine.Flow{flowOf("a", "ESTABLISHED", 2, 1)})), map[string]string{})
	eq(kinds(tr.update([]engine.Flow{flowOf("a", "ESTABLISHED", 2, 1), flowOf("b", "ESTABLISHED", 1, 1)})),
		map[string]string{"b": "created established traffic"})
	// the same id with another start time is another connection, even if it looks like the old one
	later := flowOf("a", "ESTABLISHED", 2, 1)
	later.StartedAt = time.Unix(1700000000, 0)
	eq(kinds(tr.update([]engine.Flow{later, flowOf("b", "ESTABLISHED", 1, 1)})), map[string]string{"a": "created established traffic"})
	// the same key (id and start time) that is still in the table but whose counters started over is a new connection too
	// (the entry was replaced between two polls)
	restarted := flowOf("a", "ESTABLISHED", 1, 0)
	restarted.StartedAt = later.StartedAt
	eq(kinds(tr.update([]engine.Flow{restarted, flowOf("b", "ESTABLISHED", 1, 1)})), map[string]string{"a": "created established traffic"})
}

// What a check looks at: the target's connections that its selector selects, of the kind that decides it.
func TestACheckObservesTheConnectionsOfItsKind(t *testing.T) {
	nets := newFlowNets(&engine.Snapshot{})
	target := model.Scope{Device: ptrTo("dev-1")}
	devices := map[string]bool{"dev-1": true}
	to := func(addr string) engine.Flow {
		f := flowOf("x", "ESTABLISHED", 1, 1)
		f.Device = "dev-1"
		f.Dst = netip.MustParseAddr(addr)
		f.DPort = 8883
		return f
	}
	trying := to("203.0.113.9")
	trying.State = "SYN_SENT"
	foreign := to("203.0.113.9")
	foreign.Device = "dev-2"

	connect := &liveCheck{kind: "reconnected"}
	seen := &liveCheck{kind: "traffic_seen"}
	quiet := &liveCheck{kind: "traffic_not_seen", except: []model.Destination{{Cidr: ptrTo("203.0.113.0/24")}}}
	for name, c := range map[string]struct {
		check *liveCheck
		ev    flowEvent
		want  bool
	}{
		"a connection check wants it established":  {connect, flowEvent{flow: trying, created: true, traffic: true}, false},
		"it counts once established":               {connect, flowEvent{flow: to("203.0.113.9"), established: true}, true},
		"a packet of an old connection is no news": {connect, flowEvent{flow: to("203.0.113.9"), traffic: true}, false},
		"a traffic check takes a handshake":        {seen, flowEvent{flow: trying, created: true, traffic: true}, true},
		"another device is not the target":         {seen, flowEvent{flow: foreign, created: true, traffic: true}, false},
		"an exception is allowed":                  {quiet, flowEvent{flow: to("203.0.113.77"), traffic: true}, false},
		"anything else is not":                     {quiet, flowEvent{flow: to("198.51.100.1"), traffic: true}, true},
	} {
		if got := c.check.observes(nets, target, devices, c.ev); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// P4-AUDIT-19: a connection that began after the window closed is not the window's; one without a start time cannot be
// dated and is kept.
func TestACheckWhoseWindowHasClosedIgnoresConnectionsThatBeganAfterIt(t *testing.T) {
	nets := newFlowNets(&engine.Snapshot{})
	target := model.Scope{Device: ptrTo("dev-1")}
	devices := map[string]bool{"dev-1": true}
	end := time.Unix(1700000000, 0)
	flow := func(started time.Time) flowEvent {
		f := flowOf("x", "ESTABLISHED", 1, 1)
		f.Device = "dev-1"
		f.StartedAt = started
		return flowEvent{flow: f, created: true, established: true, traffic: true}
	}
	for name, c := range map[string]struct {
		check *liveCheck
		ev    flowEvent
		want  bool
	}{
		"inside the window":                {&liveCheck{kind: "no_connection", ended: true, wendWall: end}, flow(end.Add(-time.Second)), true},
		"at its last moment":               {&liveCheck{kind: "no_connection", ended: true, wendWall: end}, flow(end), true},
		"after its end":                    {&liveCheck{kind: "no_connection", ended: true, wendWall: end}, flow(end.Add(time.Millisecond)), false},
		"after the end of a traffic check": {&liveCheck{kind: "traffic_not_seen", ended: true, wendWall: end}, flow(end.Add(time.Millisecond)), false},
		"without a start time":             {&liveCheck{kind: "no_connection", ended: true, wendWall: end}, flow(time.Time{}), true},
		"a window that is still open":      {&liveCheck{kind: "no_connection"}, flow(end.Add(time.Hour)), true},
	} {
		if got := c.check.observes(nets, target, devices, c.ev); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestTheCheckKindsOfAScenarioAreNamedAndWindowsDescribed(t *testing.T) {
	cfg := &model.Configuration{}
	tcp := model.Protocol("tcp")
	within, until := "30s", "restore"
	c := model.Check{Reconnected: &model.TrafficMatch{Protocol: &tcp, Ports: &[]int{8883}, PortRanges: &[]model.PortRange{{From: 1, To: 9}}},
		Window: model.CheckWindow{From: "cut", Within: &within}}
	if got := checkName(cfg, &c); got != "reconnected tcp/8883,1-9, from cut within 30s" {
		t.Errorf("%q", got)
	}
	c.Window = model.CheckWindow{From: "start", Until: &until}
	if got := checkName(cfg, &c); got != "reconnected tcp/8883,1-9, from start until restore" {
		t.Errorf("%q", got)
	}
	name := "my check"
	c.Name = &name
	if got := checkName(cfg, &c); got != "my check" {
		t.Errorf("%q", got)
	}
}
