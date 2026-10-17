package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// This file is port forwarding (plan §2.2 layer 4, milestone M35a, D34): connections that come in on an
// entry side's address with a forward's protocol and external port are sent (DNAT) to the forward's
// target and target port.
//
// Where a forward stands in the packet path.
//
//	prerouting (nat, -100)  one counting rule per forward (forwardCounts): in on its entry interface, to an
//	                        address of that interface, its protocol and external ports; it counts into
//	                        the forward's counter and notes the time (`last`). A NAT chain sees only the
//	                        first packet of a connection, so the counter counts the connections that
//	                        arrive through the forward, whatever the access rules decide about them later
//	                        (P31-M35a-09). The rule of a forward from the management side also sets the
//	                        conntrack mark bit ForwardReplyMarkBit (see below). Then one rule for all
//	                        forwards: in on an entry interface, to an address of that interface
//	                        (`fib daddr . iif type local`, so a new address is followed by the kernel at
//	                        once, like masquerade), DNAT to the element of the map `fwd_dnat` keyed on the
//	                        entry interface, the protocol and the destination port. The map holds one
//	                        element per forwarded port (a range is shifted one to one) with the target's
//	                        current address: a device target follows its address by element updates, like
//	                        the identity map (M7). A forward without a target address has no elements, and
//	                        its connections are not translated.
//	forward                 established/related ... the access rules (which match a forwarded connection
//	                        with the target as responder, access.go) -> one accept rule per forward
//	                        (`ct status dnat`, in on its entry interface, out to a local test network,
//	                        its protocol and external ports) -> the access matrix, whose default keeps
//	                        everything else from the uplink out of the test networks.
//
// The replies of a forward from the management side (three-port topology; P31-M35a-05 and -10, option
// (c)). A device's packets are looked up in the policy table 100, whose default is the uplink, and that
// table has no route to the management network, so a device cannot open a connection there whatever the
// access rules allow. Only the replies of a connection that came in through a management-side forward go
// back through the management interface: its counting rule sets bit 22 of the conntrack mark (plan §3.3),
// the chain fwd_reply (prerouting, before the routing decision) copies it onto the packet mark of the
// replies that come in from a local test network, and the policy rule `fwmark 0x400000/0x400000`
// (priority 925, before the rules of table 100) sends them to table 101: the management interface's
// connected subnet, its default gateway when it has one (so a peer behind a router of the management
// network is answered too), and a prohibit route as the fallback, so such a reply never leaves through
// the uplink. Without a forward from the management side none of this is compiled.
//
// Faults do not apply to forwarded connections (M35b): the classification chain runs before the DNAT on
// the conntrack original tuple, whose ends (the peer and the entry address) lie in no test network, so it
// never marks them. Hairpin access (a device using the entry address) is not supported: the DNAT rule
// matches the entry interfaces only.

// ForwardMapBase is the base of the name of the DNAT map.
const ForwardMapBase = "fwd_dnat"

// CodeForwardRefused is the warning for a forward the compiler leaves out although the configuration has
// it: one the validation refuses (it took a port of the gateway, overlaps another or goes outside the
// local test networks). The compiler is the backstop of the validation, so a configuration that did not
// pass it (an old revision) never takes the control plane's ports: `management.ui_port`, else the port
// the caller names (Input.DefaultUIPort: the API's --port, or the --ui-port of `chaosgw apply`, which
// validates a file against it, too).
const CodeForwardRefused = "forward_refused"

// The routing of the replies of a forward from the management side (see the overview above).
const (
	// ForwardReplyMarkBit is bit 22 of the mark layout of plan §3.3 (bits 22-23: further routing marks): in
	// the conntrack mark it says that the connection came in through a forward of the management side, in
	// the packet mark that the packet is a reply of such a connection and goes back through the management
	// interface. No other chain reads or writes it (the classification keeps every bit but its own).
	ForwardReplyMarkBit = uint32(1) << 22
	// ForwardReplyMark is the fwmark of the policy rule: the bit, under a mask of the bit alone.
	ForwardReplyMark = "0x400000/0x400000"
	// ForwardReplyTable is the routing table of those replies: 101, free between the policy table and
	// the service table.
	ForwardReplyTable = PolicyTable + 1
	// ForwardReplyRulePriority puts its rule after the service selection (900) and before the PMTU
	// mirrors (950) and the rules of the policy table (1000).
	ForwardReplyRulePriority = PolicyRulePriority - 75
	// ForwardReplyChain is the chain that copies the bit from the conntrack mark onto the packet mark of
	// the replies; ForwardReplyPriority runs it after the classification (-150), before the NAT chain
	// (-100) and the routing decision.
	ForwardReplyChain    = "fwd_reply"
	ForwardReplyPriority = ClassifyPriority + 10
)

// forwardMapKey and forwardMapValue are the types of the DNAT map: the entry interface, the protocol and
// the external port to the target's address and the target port.
var (
	forwardMapKey   = []string{"ifname", "inet_proto", "inet_service"}
	forwardMapValue = "ipv4_addr . inet_service"
)

// PortForward is a forward as the compiler built it. Only enabled forwards whose entry interface exists
// (and is not the uplink's in the role of the management side) are compiled.
type PortForward struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol"`
	// Entry is the entry side (uplink or management), Iface its interface.
	Entry string `json:"entry"`
	Iface string `json:"iface"`
	// ExtFrom..ExtTo are the external ports, TgtFrom..TgtTo the target ports (the same length).
	ExtFrom int `json:"ext_from"`
	ExtTo   int `json:"ext_to"`
	TgtFrom int `json:"tgt_from"`
	TgtTo   int `json:"tgt_to"`
	// Target is the address the connections are sent to now; invalid when the target device has no
	// address in a local test network (the map has no element for the forward then).
	Target netip.Addr `json:"target"`
	// Active reports whether the forward is in effect; Reason says why not (domain.Forward… reasons).
	Active bool   `json:"active"`
	Reason string `json:"reason,omitempty"`
	// Counter is the named counter of the forward's counting rule in the NAT prerouting chain: the
	// connections that arrived through the forward (the first packet of each), whatever the access rules
	// decided about them.
	Counter string `json:"counter"`
}

// ForwardCounterName names the counter of a forward by its id, so it stays while the forward stays.
func ForwardCounterName(id string) string {
	h := sha256.Sum256([]byte("forward:" + id))
	return "fwd_" + hex.EncodeToString(h[:5])
}

// ForwardMapName is the name of the DNAT map.
func ForwardMapName() string {
	return hashMapName(ForwardMapBase, forwardMapKey, forwardMapValue, nil)
}

// Forward returns the compiled forward of an id.
func (t *Target) Forward(id string) (*PortForward, bool) {
	for i := range t.Forwards {
		if t.Forwards[i].ID == id {
			return &t.Forwards[i], true
		}
	}
	return nil, false
}

// forwardsFrom reports whether a forward of the entry side is compiled.
func (t *Target) forwardsFrom(entry string) bool {
	for _, f := range t.Forwards {
		if f.Entry == entry {
			return true
		}
	}
	return false
}

// compileForwards builds the forwards of the configuration against the entry interfaces and the
// identity of the devices: which are compiled, where they send their connections now, and the elements
// of the DNAT map. The rules themselves are written by compileNft.
func (t *Target) compileForwards(in Input) {
	cfg := in.Config
	if cfg.PortForwards == nil || len(*cfg.PortForwards) == 0 {
		return
	}
	if !domain.IsNormalized(cfg) {
		n, errs := domain.Normalize(cfg)
		if len(errs) > 0 {
			return // a configuration with dangling references has no forwards to resolve
		}
		cfg = n
	}
	idx, _ := domain.BuildIndex(cfg)
	facts := domain.ForwardFacts{
		TwoPort: t.Uplink.Name != "" && t.Management.Name == t.Uplink.Name,
		Entry:   map[string]bool{domain.EntryUplink: t.Uplink.Addr.IsValid(), domain.EntryManagement: t.Management.Subnet.IsValid()},
	}
	if in.Identity != nil {
		facts.Identity = *in.Identity
	}
	ifaces := map[string]string{domain.EntryUplink: t.Uplink.Name, domain.EntryManagement: t.Management.Name}
	m := MapDef{Name: ForwardMapName(), KeyType: forwardMapKey, ValueType: forwardMapValue}
	for _, f := range domain.Forwards(cfg) {
		if !f.Enabled || (f.Entry == domain.EntryManagement && facts.TwoPort) {
			continue // a disabled forward leaves nothing in the kernel; `management` is refused in the two-port topology
		}
		iface := ifaces[f.Entry]
		if iface == "" {
			continue // the entry interface is not present (reported for the uplink and the management interface)
		}
		st := idx.ForwardStatus(f, facts)
		if why := t.forwardRefusal(f, st, facts.TwoPort); why != "" {
			t.warn(CodeForwardRefused, "", "the port forward %s is left out: %s", forwardLabel(f), why)
			continue
		}
		pf := PortForward{ID: f.ID, Name: f.Name, Protocol: f.Protocol, Entry: f.Entry, Iface: iface,
			ExtFrom: f.ExtFrom, ExtTo: f.ExtTo, TgtFrom: f.TgtFrom, TgtTo: f.TgtTo,
			Target: st.Target, Active: st.Active, Reason: st.Reason, Counter: ForwardCounterName(f.ID)}
		if pf.Target.IsValid() {
			for p := f.ExtFrom; p <= f.ExtTo; p++ {
				m.Elements = append(m.Elements, MapElement{
					Key:   fmt.Sprintf("%s . %s . %d", iface, f.Protocol, p),
					Value: fmt.Sprintf("%s . %d", pf.Target, f.TargetPortOf(p)),
				})
			}
		}
		t.Forwards = append(t.Forwards, pf)
	}
	if len(t.Forwards) == 0 {
		return
	}
	sort.Slice(m.Elements, func(i, j int) bool { return m.Elements[i].Key < m.Elements[j].Key })
	t.ForwardMap = m.Name
	t.forwardMap = m
}

// forwardRefusal says why a forward must not be compiled, empty when it may: what the validation
// refuses with the pointer (domain.validator.portForwards), checked again against the target state.
func (t *Target) forwardRefusal(f domain.Forward, st domain.ForwardState, twoPort bool) string {
	switch {
	case f.Protocol != "tcp" && f.Protocol != "udp":
		return fmt.Sprintf("the protocol %q is not tcp or udp", f.Protocol)
	case f.ExtFrom < 1 || f.ExtTo > 65535 || f.ExtTo < f.ExtFrom:
		return fmt.Sprintf("the external ports %s are not a range of ports", f.ExternalPorts())
	case f.TgtFrom < 1 || f.TgtTo > 65535 || f.TgtTo-f.TgtFrom != f.ExtTo-f.ExtFrom:
		return fmt.Sprintf("the target ports %s are not a range as long as the external one", f.TargetPorts())
	case f.Covers(t.Management.UIPort):
		return fmt.Sprintf("port %d is the UI/API port of the gateway", t.Management.UIPort)
	case f.Protocol == "tcp" && f.Covers(22) && (f.Entry == domain.EntryManagement || twoPort):
		return "tcp/22 is the gateway's SSH on the management interface"
	case f.Device == "" && st.Network == "":
		return fmt.Sprintf("the target %s lies in no local test network", f.Address)
	}
	if f.Protocol == "udp" {
		for _, w := range t.WireGuard {
			if f.Covers(w.ListenPort) {
				return fmt.Sprintf("udp/%d is the listen port of the WireGuard interface %s", w.ListenPort, w.Name)
			}
		}
	}
	for _, p := range t.Management.Sources {
		if f.Device == "" && p.Contains(f.Address) {
			return fmt.Sprintf("the target %s lies in the management network %s", f.Address, p)
		}
	}
	for _, o := range t.Forwards {
		if o.Protocol == f.Protocol && f.ExtFrom <= o.ExtTo && o.ExtFrom <= f.ExtTo {
			return fmt.Sprintf("%s/%s overlaps the forward %s", f.Protocol, f.ExternalPorts(), o.ID)
		}
	}
	return ""
}

func forwardLabel(f domain.Forward) string {
	if f.Name != "" {
		return f.Name + " (" + f.ID + ")"
	}
	return f.ID
}

// forwardDNAT is the rule of the prerouting NAT chain that translates every forward: in on an entry
// interface, to an address of that interface (the kernel's own lookup: an address the interface gets
// later is followed at once), TCP or UDP, DNAT to the map's element of the interface, protocol and port.
// A connection whose key has no element (no forward, or one whose target has no address) is left alone.
func (t *Target) forwardDNAT() []Rule {
	if len(t.Forwards) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var ifs []any
	for _, f := range t.Forwards {
		if !seen[f.Iface] {
			seen[f.Iface] = true
			ifs = append(ifs, f.Iface)
		}
	}
	sort.Slice(ifs, func(i, j int) bool { return ifs[i].(string) < ifs[j].(string) })
	iif := ifs[0]
	if len(ifs) > 1 {
		iif = anon(ifs...)
	}
	local := fibLocal()
	key := concat(meta("iifname"), meta("l4proto"), payload("th", "dport"))
	dnat := map[string]any{"dnat": map[string]any{"family": "ip", "type_flags": "concat",
		"addr": map[string]any{"map": map[string]any{"key": key, "data": setRef(t.ForwardMap)}}}}
	return []Rule{newRule(eq(meta("iifname"), iif), eq(meta("l4proto"), anon("tcp", "udp")), local, dnat)}
}

// forwardCounts are the counting rules of the forwards in the prerouting NAT chain, in front of the DNAT
// rule: in on the forward's entry interface, to an address of that interface, its protocol and external
// ports; each counts into the forward's counter and notes when it last matched (the `last` statement,
// like the access rules). A NAT chain sees the first packet of a connection only, so the counter counts
// connections: each connection that arrives through the forward once, whether an access rule or the
// forward's accept rule lets it through or a rule drops it (a dropped TCP SYN leaves no conntrack entry,
// so its retransmission arrives, and is counted, as a new connection). The rule of a forward from the
// management side also sets ForwardReplyMarkBit in the conntrack mark, so the replies of the connection
// are routed back through the management interface (forwardReplyChain, forwardReplyRouting).
func (t *Target) forwardCounts() []Rule {
	var out []Rule
	for _, f := range t.Forwards {
		expr := []any{iifname(f.Iface), eq(meta("l4proto"), f.Protocol), fibLocal(),
			eq(payload("th", "dport"), portValue([]PortRange{{f.ExtFrom, f.ExtTo}}))}
		if f.Entry == domain.EntryManagement && t.managementReplies() {
			expr = append(expr, ctMarkSet(bitOr(ctKey("mark"), int64(ForwardReplyMarkBit))))
		}
		out = append(out, newRule(append(expr, counter(f.Counter), lastStmt())...))
	}
	return out
}

// forwardAccepts are the accept rules of the forwards in the forward chain, after the access rules and
// in front of the access matrix: a connection the DNAT of a forward translated (`ct status dnat`), in on
// its entry interface, out to a local test network, with its protocol and an external port of its range.
// They count nothing: the forward's counter is that of its counting rule (forwardCounts).
func (t *Target) forwardAccepts(lanSet string) []Rule {
	var out []Rule
	for _, f := range t.Forwards {
		out = append(out, newRule(iifname(f.Iface), oifSet(lanSet), ctStatusDNAT(), eq(meta("l4proto"), f.Protocol),
			eq(ctOriginal("proto-dst"), portValue([]PortRange{{f.ExtFrom, f.ExtTo}})), verdict("accept")))
	}
	return out
}

// managementReplies reports whether the replies of forwarded connections are routed back through the
// management interface: while a forward from the management side is compiled, in the three-port
// topology (the two-port one compiles no such forward) and with the management interface's subnet known.
func (t *Target) managementReplies() bool {
	return t.Management.Name != "" && t.Management.Name != t.Uplink.Name && t.Management.Subnet.IsValid() &&
		t.forwardsFrom(domain.EntryManagement)
}

// forwardReplyChain copies ForwardReplyMarkBit from the conntrack mark onto the packet mark of a reply
// that comes in from a local test network, before the routing decision, so the policy rule of
// ForwardReplyMark sends it to ForwardReplyTable. Every other bit of the packet mark is kept. Nil without
// a forward from the management side.
func (t *Target) forwardReplyChain(lanSet string) *Chain {
	if !t.managementReplies() {
		return nil
	}
	bit := int64(ForwardReplyMarkBit)
	return &Chain{Name: ForwardReplyChain, Base: &BaseChain{Type: "filter", Hook: "prerouting", Prio: ForwardReplyPriority, Policy: "accept"},
		Rules: []Rule{newRule(iifSet(lanSet), match(ctKey("direction"), "==", "reply"), eq(bitAnd(ctKey("mark"), bit), bit),
			markSet(bitOr(meta("mark"), bit)))}}
}

// forwardReplyRouting adds the table and the policy rule of the replies of a management-side forward:
// the management interface's connected subnet, its default gateway when it has one, and a prohibit route
// as the fallback, so a reply the table has no route for fails instead of falling through to the policy
// table, whose default is the uplink.
func (t *Target) forwardReplyRouting() {
	if !t.managementReplies() {
		return
	}
	m := t.Management
	t.Routes = append(t.Routes, executor.Route{Action: "replace", Family: 4, Table: ForwardReplyTable, Dst: m.Subnet.String(), Dev: m.Name})
	if m.gateway.IsValid() {
		zero := 0
		t.Routes = append(t.Routes, executor.Route{Action: "replace", Family: 4, Table: ForwardReplyTable, Dst: "default", Via: m.gateway.String(), Dev: m.Name, Metric: &zero})
	}
	prohibitMetric := 4096
	t.Routes = append(t.Routes, executor.Route{Action: "replace", Family: 4, Table: ForwardReplyTable, Dst: "default", Type: "prohibit", Metric: &prohibitMetric})
	t.Rules = append(t.Rules, executor.Rule{Action: "add", Family: 4, Priority: ForwardReplyRulePriority, Fwmark: ForwardReplyMark, Table: ForwardReplyTable})
}

// fibLocal matches a packet to an address of the interface it came in on (`fib daddr . iif type local`).
func fibLocal() any {
	return eq(map[string]any{"fib": map[string]any{"result": "type", "flags": []any{"daddr", "iif"}}}, "local")
}

// ctMarkSet is "ct mark set <value>".
func ctMarkSet(value any) any {
	return map[string]any{"mangle": map[string]any{"key": ctKey("mark"), "value": value}}
}

// ctStatusDNAT matches a connection whose destination was translated (`ct status dnat`).
func ctStatusDNAT() any { return match(ctKey("status"), "in", "dnat") }
