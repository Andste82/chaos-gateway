package domain

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the domain side of port forwarding (plan §2.2 layer 4, M35a, D34): the forwards of a
// configuration with their defaults filled in, whether each is in effect for the observed state (a
// device target follows the device's address), and the access decision of a forwarded connection,
// which the access rules judge with the forward's target as responder (plan §2.4).

// Entry sides of a forward, as the spec names them.
const (
	EntryUplink     = string(model.PortForwardEntryUplink)
	EntryManagement = string(model.PortForwardEntryManagement)
)

// Reasons why a forward is inactive (PortForwardStatus.reason).
const (
	ForwardDisabled            = string(model.PortForwardStatusReasonDisabled)
	ForwardDeviceNoAddress     = string(model.PortForwardStatusReasonDeviceWithoutAddress)
	ForwardDeviceOutside       = string(model.PortForwardStatusReasonDeviceOutsideTestNetwork)
	ForwardManagementIsUplink  = string(model.PortForwardStatusReasonManagementIsUplink)
	ForwardEntryWithoutAddress = string(model.PortForwardStatusReasonEntryWithoutAddress)
)

// FaultsNotOnForwards is the note of the preview and of explain: faults do not apply to forwarded
// connections before M35b.
const FaultsNotOnForwards = "faults do not apply to forwarded connections (M35b)"

// Forward is a port forward of a configuration with its defaults filled in: the target range is as long
// as the external one and starts at the target port (the external port by default).
type Forward struct {
	ID, Name string
	Protocol string // tcp or udp
	Entry    string // EntryUplink or EntryManagement
	// ExtFrom..ExtTo is the external range, TgtFrom..TgtTo the target range (single ports have From ==
	// To); a valid forward has ranges of the same length.
	ExtFrom, ExtTo int
	TgtFrom, TgtTo int
	Enabled        bool
	// Device is the target device (its UUID, lower case), empty for an address target; Address is the
	// configured target address, invalid for a device target.
	Device  string
	Address netip.Addr
}

// ForwardOf fills in the defaults of a configured forward.
func ForwardOf(id string, f model.PortForward) Forward {
	out := Forward{ID: lower(id), Name: deref(f.Name), Protocol: string(f.Protocol), Entry: string(f.Entry),
		ExtFrom: f.ExternalPort, ExtTo: f.ExternalPort, Enabled: isEnabled(f.Enabled)}
	if f.ExternalPortEnd != nil {
		out.ExtTo = *f.ExternalPortEnd
	}
	out.TgtFrom = out.ExtFrom
	if f.TargetPort != nil {
		out.TgtFrom = *f.TargetPort
	}
	out.TgtTo = out.TgtFrom + (out.ExtTo - out.ExtFrom)
	if f.TargetPortEnd != nil {
		out.TgtTo = *f.TargetPortEnd
	}
	if f.Target.Device != nil {
		out.Device = lower(*f.Target.Device)
	}
	if f.Target.Address != nil {
		out.Address, _ = parseAddr(*f.Target.Address)
	}
	return out
}

// Forwards returns the port forwards of a configuration, sorted by name, then id.
func Forwards(cfg *model.Configuration) []Forward {
	var out []Forward
	for id, f := range deref(cfg.PortForwards) {
		out = append(out, ForwardOf(id, f))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Covers reports whether the forward's external range contains the port.
func (f Forward) Covers(port int) bool { return port >= f.ExtFrom && port <= f.ExtTo }

// TargetPortOf is the target port an external port is sent to: the ranges are shifted one to one.
func (f Forward) TargetPortOf(port int) int { return f.TgtFrom + (port - f.ExtFrom) }

// ExternalPorts and TargetPorts write the ranges as `8000` or `8000-8010`.
func (f Forward) ExternalPorts() string { return portsString(f.ExtFrom, f.ExtTo) }
func (f Forward) TargetPorts() string   { return portsString(f.TgtFrom, f.TgtTo) }

func portsString(from, to int) string {
	if from == to {
		return strconv.Itoa(from)
	}
	return fmt.Sprintf("%d-%d", from, to)
}

// ForwardFacts is what the status of a forward needs besides the configuration.
type ForwardFacts struct {
	// Identity holds the devices' current addresses (a device target follows them).
	Identity Identity
	// TwoPort reports that the management interface is the uplink interface as the host resolves them
	// (a configuration may name them differently, by name and by MAC).
	TwoPort bool
	// Entry says for each entry side (EntryUplink, EntryManagement) whether its interface has an IPv4
	// address now. Nil: not known, the entry is not judged.
	Entry map[string]bool
}

// ForwardState says whether a forward is in effect and where it sends its connections.
type ForwardState struct {
	Active bool
	// Reason is one of the Forward… reasons when the forward is inactive; Message says it in words.
	Reason, Message string
	// Target is the address the connections are sent to now: the configured address, or the device's
	// current address in a local test network. Invalid when there is none.
	Target netip.Addr
	// Network is the local test network the target lies in, empty when it lies in none.
	Network string
}

// ForwardStatus decides whether a forward is in effect for the observed state: enabled, the entry side
// is its own interface and has an address, and the target has an address in a local test network.
func (x *Index) ForwardStatus(f Forward, facts ForwardFacts) ForwardState {
	st := ForwardState{Target: f.Address}
	if f.Device != "" {
		st.Target = netip.Addr{}
		addrs := facts.Identity.Addresses[f.Device]
		for _, a := range addrs {
			if n, ok := x.lanOf(a); ok {
				st.Target, st.Network = a, n
				break
			}
		}
		if !st.Target.IsValid() {
			if len(addrs) == 0 {
				st.Reason, st.Message = ForwardDeviceNoAddress, "inactive: the device has no address"
			} else {
				st.Reason, st.Message = ForwardDeviceOutside, "inactive: the device has no address in a local test network"
			}
		}
	} else if n, ok := x.lanOf(f.Address); ok {
		st.Network = n
	}
	switch {
	case !f.Enabled:
		// the first reason a person would act on: the forward is switched off
		st.Reason, st.Message = ForwardDisabled, "inactive: the forward is disabled"
	case f.Entry == EntryManagement && facts.TwoPort:
		st.Reason, st.Message = ForwardManagementIsUplink, "inactive: the management interface is the uplink interface (two-port topology)"
	case facts.Entry != nil && !facts.Entry[f.Entry]:
		st.Reason, st.Message = ForwardEntryWithoutAddress, "inactive: the "+f.Entry+" interface has no IPv4 address"
	}
	st.Active = st.Reason == ""
	return st
}

// lanOf returns the local test network whose subnet holds a as a host address other than the
// gateway's own.
func (x *Index) lanOf(a netip.Addr) (string, bool) {
	if !a.IsValid() {
		return "", false
	}
	for _, id := range sortedKeys(x.Networks) {
		n := x.Networks[id]
		if !n.IsLan() {
			continue
		}
		gw, ok := parsePrefix(n.Lan.Address)
		if !ok {
			continue
		}
		if usableHost(gw.Masked(), a) && a != gw.Addr() {
			return id, true
		}
	}
	return "", false
}

// TargetLabel says in words where a forward goes: `device esp32-42` or `10.10.0.20`.
func (x *Index) TargetLabel(f Forward) string {
	if f.Device != "" {
		return "device " + x.NameOf(KindDevice, f.Device)
	}
	return f.Address.String()
}

// ForwardedQuery is a forwarded connection: from a peer behind the entry side to the external port.
type ForwardedQuery struct {
	Forward Forward
	// Target is the address the connection is sent to (ForwardState.Target), Network its network.
	Target  netip.Addr
	Network string
	// Device is the device that owns an address target now (the observed identity), empty when none
	// does; a device target is the forward's own.
	Device string
	// Peer is the address the connection comes from; Protocol and Port (an external port) its tuple.
	Peer     netip.Addr
	Protocol string
	Port     int
}

// ForwardedDecision decides what the gateway does with the first packet of a forwarded connection
// (plan §2.4, D34): the access rules match it with the forward's target as responder (a rule's source
// against the target, its destination against the peer, its ports against the target port), the first
// matching rule decides, overlay rules before configured ones; a connection no rule decides is accepted
// by the forward itself, in front of the access matrix. The management prefixes of the facts are not
// the uplink: a peer there is not matched by the destination `uplink`.
func (w *World) ForwardedDecision(q ForwardedQuery, f AccessFacts) AccessExplanation {
	sub := Subject{Device: q.Forward.Device, Network: q.Network, IP: q.Target}
	if sub.Device == "" {
		// an address target that a device owns is that device for the rules, as for any connection
		// of the device
		sub.Device = q.Device
	}
	r := w.ResolveAccess(Query{Source: sub, DestIP: q.Peer, Protocol: q.Protocol, Port: q.Forward.TargetPortOf(q.Port), NotUplink: f.Management})
	if r.Matched {
		layer := AccessConfigRule
		if r.Layer == LayerOverlay {
			layer = AccessOverlayRule
		}
		label := "the " + string(r.Layer) + " rule " + r.RuleID
		if r.Name != "" {
			label = "the rule " + r.Name + " (" + string(r.Layer) + ", " + r.RuleID + ")"
		}
		if r.Action != "allow" {
			return AccessExplanation{Verdict: r.Action, Layer: layer, Reason: label + " " + ruleVerb(r) + " this forwarded connection (the target is matched as responder)", Rule: r.RuleID, RuleName: r.Name}
		}
		return AccessExplanation{Verdict: "allow", Layer: layer, Reason: label + " allows this forwarded connection (the target is matched as responder)", Rule: r.RuleID, RuleName: r.Name}
	}
	label := "the port forward " + q.Forward.ID
	if q.Forward.Name != "" {
		label = "the port forward " + q.Forward.Name + " (" + q.Forward.ID + ")"
	}
	return AccessExplanation{Verdict: "allow", Layer: AccessPortForward, Reason: label + " accepts this connection: no access rule decides it, and the forward stands in front of the access matrix"}
}

// MatchForward finds the enabled forward that a connection to the entry side's address takes: the
// protocol and an external port in its range. ok is false when none does.
func MatchForward(fs []Forward, entry, protocol string, port int) (Forward, bool) {
	for _, f := range fs {
		if f.Enabled && f.Entry == entry && f.Protocol == protocol && f.Covers(port) {
			return f, true
		}
	}
	return Forward{}, false
}

// ForwardFaultNote is a fault that selects the target of an enabled forward: it does not impair the
// connections through the forward (M35b), which the preview says.
type ForwardFaultNote struct {
	Forward Forward
	Fault   Candidate
}

// ForwardFaultNotes finds, for each enabled forward with a target, the faults of the impairment and the
// mtu family that win for the target's traffic in general (a fault that names a destination is not
// asked: the peers of a forward are not known in advance). An address target that a device owns now
// counts as that device.
func (w *World) ForwardFaultNotes(fs []Forward, facts ForwardFacts) []ForwardFaultNote {
	var out []ForwardFaultNote
	for _, f := range fs {
		if !f.Enabled {
			continue
		}
		st := w.Index.ForwardStatus(f, facts)
		sub := Subject{Device: f.Device, Network: st.Network, IP: st.Target}
		if sub.Device == "" && st.Target.IsValid() {
			sub.Device, _ = facts.Identity.OwnerOf(st.Target)
			sub.Device = lower(sub.Device)
		}
		if sub.Device == "" && !sub.IP.IsValid() {
			continue
		}
		results := w.Resolve(Query{Source: sub})
		for _, fam := range []string{FamilyImpairment, FamilyMTU} {
			if c := Winner(results, fam); c != nil {
				out = append(out, ForwardFaultNote{Forward: f, Fault: *c})
			}
		}
	}
	return out
}
