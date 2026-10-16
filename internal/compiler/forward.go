package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/domain"
)

// This file is port forwarding (plan §2.2 layer 4, milestone M35a, D34): connections that come in on an
// entry side's address with a forward's protocol and external port are sent (DNAT) to the forward's
// target and target port.
//
// Where a forward stands in the packet path.
//
//	prerouting (nat, -100)  one rule for all forwards: in on an entry interface, to an address of that
//	                        interface (`fib daddr . iif type local`, so a new address is followed by the
//	                        kernel at once, like masquerade), DNAT to the element of the map `fwd_dnat`
//	                        keyed on the entry interface, the protocol and the destination port. The map
//	                        holds one element per forwarded port (a range is shifted one to one) with the
//	                        target's current address: a device target follows its address by element
//	                        updates, like the identity map (M7). A forward without a target address has
//	                        no elements, and its connections are not translated.
//	forward                 established/related ... the access rules (which match a forwarded connection
//	                        with the target as responder, access.go) -> one accept rule per forward
//	                        (`ct status dnat`, in on its entry interface, out to a local test network,
//	                        its protocol and external ports), which counts the connections the forward
//	                        let through -> the access matrix, whose default keeps everything else from
//	                        the uplink out of the test networks.
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
// pass it (an old revision, a file applied with `chaosgw apply`) never takes the control plane's ports.
const CodeForwardRefused = "forward_refused"

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
	// Counter is the named counter of the forward's accept rule: the new connections it let through.
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
	local := eq(map[string]any{"fib": map[string]any{"result": "type", "flags": []any{"daddr", "iif"}}}, "local")
	key := concat(meta("iifname"), meta("l4proto"), payload("th", "dport"))
	dnat := map[string]any{"dnat": map[string]any{"family": "ip", "type_flags": "concat",
		"addr": map[string]any{"map": map[string]any{"key": key, "data": setRef(t.ForwardMap)}}}}
	return []Rule{newRule(eq(meta("iifname"), iif), eq(meta("l4proto"), anon("tcp", "udp")), local, dnat)}
}

// forwardAccepts are the accept rules of the forwards in the forward chain, after the access rules and
// in front of the access matrix: a connection the DNAT of a forward translated (`ct status dnat`), in on
// its entry interface, out to a local test network, with its protocol and an external port of its range.
// Each counts into the forward's counter, so the counter is the new connections the forward let through,
// and notes when it last matched (the `last` statement, like the access rules).
func (t *Target) forwardAccepts(lanSet string) []Rule {
	var out []Rule
	for _, f := range t.Forwards {
		out = append(out, newRule(iifname(f.Iface), oifSet(lanSet), ctStatusDNAT(), eq(meta("l4proto"), f.Protocol),
			eq(ctOriginal("proto-dst"), portValue([]PortRange{{f.ExtFrom, f.ExtTo}})), counter(f.Counter), lastStmt(), verdict("accept")))
	}
	return out
}

// ctStatusDNAT matches a connection whose destination was translated (`ct status dnat`).
func ctStatusDNAT() any { return match(ctKey("status"), "in", "dnat") }
