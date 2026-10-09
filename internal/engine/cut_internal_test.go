package engine

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// plan builds a plan of rules selecting sources, protocol and ports; the rules are in the order given.
func plan(rules ...compiler.AccessRule) *compiler.AccessPlan {
	p := &compiler.AccessPlan{}
	for i, r := range rules {
		r.Position = i
		if r.Protocol == "" {
			r.Protocol = "any"
		}
		p.Rules = append(p.Rules, r)
	}
	return p
}

func rule(key, action string, cut bool, src string, proto string, ports ...int) compiler.AccessRule {
	r := compiler.AccessRule{Key: key, Action: action, CutExisting: cut, Protocol: proto, Sources: []netip.Prefix{pfx(src)}}
	for _, p := range ports {
		r.Ports = append(r.Ports, compiler.PortRange{From: p, To: p})
	}
	return r
}

func tcpFlow(state, src string, sport int, dst string, dport int) linux.Conntrack {
	return linux.Conntrack{Proto: "tcp", State: state,
		Original: linux.Tuple{Src: src, Dst: dst, SPort: sport, DPort: dport},
		Reply:    linux.Tuple{Src: dst, Dst: src, SPort: dport, DPort: sport}}
}

func target(p *compiler.AccessPlan) *compiler.Target {
	return &compiler.Target{Access: p, Management: compiler.Management{Sources: []netip.Prefix{pfx("192.168.56.0/24")}, UIPort: 8080}}
}

func keysOf(fs []cutFlow) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.rule+":"+f.flow.Proto+"/"+f.flow.Src+":"+strconv.Itoa(f.flow.SPort))
	}
	return strings.Join(out, ",")
}

// A connection belongs to the first rule that selects it: a cutting rule behind an allow rule, or behind
// a rule that does not cut, leaves that rule's connections alone.
func TestACutOnlyTakesTheConnectionsTheRuleIsTheFirstToSelect(t *testing.T) {
	old := target(plan())
	next := target(plan(
		rule("config:keep", "allow", false, "10.10.0.0/24", "tcp", 22),
		rule("config:soft", "drop", false, "10.10.0.0/24", "tcp", 1883),
		rule("config:cut", "drop", true, "10.10.0.0/24", "tcp"),
	))
	flows := []linux.Conntrack{
		tcpFlow("ESTABLISHED", "10.10.0.5", 40001, "203.0.113.10", 22),   // allow rule's
		tcpFlow("ESTABLISHED", "10.10.0.5", 40002, "203.0.113.10", 1883), // a drop rule that does not cut
		tcpFlow("ESTABLISHED", "10.10.0.5", 40003, "203.0.113.10", 8883), // the cutting rule's
		tcpFlow("ESTABLISHED", "10.20.0.5", 40004, "203.0.113.10", 8883), // another network
	}
	got := cutFlows(old, next, flows)
	if len(got) != 1 || got[0].rule != "config:cut" || got[0].flow.SPort != 40003 || !got[0].tcp {
		t.Errorf("%s", keysOf(got))
	}
}

// A rule that already cut is not cut again when another rule changes around it; one that moves ahead of
// an allow rule takes the connections of the rule it displaced; the removal of an earlier allow rule
// hands its connections to the cutting rule behind it.
func TestACutFollowsWhatChangedBetweenTheOldAndTheNewRules(t *testing.T) {
	cut := rule("config:cut", "drop", true, "10.10.0.0/24", "tcp")
	allow := rule("config:keep", "allow", false, "10.10.0.0/24", "tcp", 22)
	ssh := tcpFlow("ESTABLISHED", "10.10.0.5", 40001, "203.0.113.10", 22)
	mqtt := tcpFlow("ESTABLISHED", "10.10.0.5", 40002, "203.0.113.10", 8883)

	// the same cutting rule in both: nothing to cut for the connections it owned
	if got := cutFlows(target(plan(allow, cut)), target(plan(allow, cut, rule("config:x", "drop", false, "10.30.0.0/24", ""))), []linux.Conntrack{ssh, mqtt}); len(got) != 0 {
		t.Errorf("a cut that was done is repeated: %s", keysOf(got))
	}
	// the allow rule goes: the ssh connection is the cutting rule's now, the mqtt one still was
	if got := cutFlows(target(plan(allow, cut)), target(plan(cut)), []linux.Conntrack{ssh, mqtt}); len(got) != 1 || got[0].flow.SPort != 40001 {
		t.Errorf("%s", keysOf(got))
	}
	// the effect changes from drop to reject: the rule has to cut again
	rej := cut
	rej.Action = "reject"
	if got := cutFlows(target(plan(cut)), target(plan(rej)), []linux.Conntrack{mqtt}); len(got) != 1 {
		t.Errorf("%s", keysOf(got))
	}
	// cut_existing switched on for a rule that dropped new connections only
	soft := cut
	soft.CutExisting = false
	if got := cutFlows(target(plan(soft)), target(plan(cut)), []linux.Conntrack{mqtt}); len(got) != 1 {
		t.Errorf("%s", keysOf(got))
	}
	// and switched off: nothing is cut
	if got := cutFlows(target(plan(cut)), target(plan(soft)), []linux.Conntrack{mqtt}); len(got) != 0 {
		t.Errorf("%s", keysOf(got))
	}
}

// Whatever a rule says, the connections of the control plane are not among the ones a cut deletes, the
// connections that are over are not worth a reset, and a protocol the operation does not name is left to
// the kernel.
func TestACutLeavesTheControlPlaneFinishedConnectionsAndOtherProtocolsAlone(t *testing.T) {
	next := target(plan(rule("config:all", "drop", true, "0.0.0.0/0", "")))
	old := target(plan())
	flows := []linux.Conntrack{
		tcpFlow("ESTABLISHED", "192.168.56.2", 40001, "10.10.0.1", 8080), // the UI, from a management source
		tcpFlow("ESTABLISHED", "192.168.56.2", 40002, "10.10.0.1", 22),   // SSH, likewise
		tcpFlow("ESTABLISHED", "192.168.56.2", 40003, "10.10.0.1", 3000), // a management source elsewhere: not the control plane
		tcpFlow("TIME_WAIT", "10.10.0.5", 40004, "203.0.113.10", 80),
		tcpFlow("CLOSE", "10.10.0.5", 40005, "203.0.113.10", 80),
		tcpFlow("LAST_ACK", "10.10.0.5", 40006, "203.0.113.10", 80),
		{Proto: "gre", Original: linux.Tuple{Src: "10.10.0.5", Dst: "203.0.113.10"}},
		{Proto: "tcp", State: "ESTABLISHED", Original: linux.Tuple{Src: "garbage", Dst: "203.0.113.10", SPort: 1, DPort: 2}},
		tcpFlow("SYN_SENT", "10.10.0.5", 40007, "203.0.113.10", 80),
		tcpFlow("ESTABLISHED", "10.10.0.5", 40008, "203.0.113.10", 80),
	}
	got := cutFlows(old, next, flows)
	if len(got) != 3 {
		t.Fatalf("%s", keysOf(got))
	}
	for _, f := range got {
		if f.flow.SPort == 40001 || f.flow.SPort == 40002 {
			t.Errorf("the control plane is cut: %s", keysOf(got))
		}
	}
	// only the established connection is reached by a reset
	var resets int
	for _, f := range got {
		if f.tcp {
			resets++
		}
	}
	if resets != 2 { // 40003 and 40008
		t.Errorf("%d connections for the window: %s", resets, keysOf(got))
	}
}

// A connection the gateway opened (its source is one of the gateway's own addresses, e.g. a BGP session
// to a device) and traffic that stays inside one network (switched, seen with br_netfilter) are never
// judged by a rule, so a cut must not delete their entries or open a window for them.
func TestACutLeavesGatewayOriginatedAndSwitchedConnectionsAlone(t *testing.T) {
	p := plan(rule("config:cut", "drop", true, "10.10.0.0/24", "tcp"))
	p.GatewayAddrs = []netip.Addr{netip.MustParseAddr("10.10.0.1"), netip.MustParseAddr("169.254.100.1")}
	p.Segments = [][]netip.Prefix{{pfx("10.10.0.0/24"), pfx("10.99.0.0/16")}, {pfx("10.20.0.0/24")}}
	flows := []linux.Conntrack{
		tcpFlow("ESTABLISHED", "10.10.0.1", 40001, "10.10.0.5", 179),     // the gateway's BGP session to a device
		tcpFlow("ESTABLISHED", "10.10.0.5", 40002, "10.10.0.6", 8883),    // two devices of one network
		tcpFlow("ESTABLISHED", "10.10.0.5", 40003, "10.99.0.7", 8883),    // a device and a host behind its router
		tcpFlow("ESTABLISHED", "10.10.0.5", 40004, "10.20.0.6", 8883),    // another network: routed, judged
		tcpFlow("ESTABLISHED", "10.10.0.5", 40005, "203.0.113.10", 8883), // the uplink: judged
		tcpFlow("ESTABLISHED", "10.10.0.5", 40006, "10.10.0.1", 8883),    // a device to the gateway: judged
	}
	got := cutFlows(target(plan()), target(p), flows)
	var ports []int
	for _, f := range got {
		ports = append(ports, f.flow.SPort)
	}
	sort.Ints(ports)
	if len(ports) != 3 || ports[0] != 40004 || ports[1] != 40005 || ports[2] != 40006 {
		t.Errorf("cut source ports %v, want 40004 40005 40006", ports)
	}
}
