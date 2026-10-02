package domain

import (
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

func (v *validator) scenarios() {
	for _, id := range sortedKeys(deref(v.cfg.Scenarios)) {
		v.scenario(schema.Pointer("/scenarios", id), deref(v.cfg.Scenarios)[id])
	}
}

// stepKinds lists the kinds of step in the order the spec names them.
var stepKinds = []string{"profile", "fault", "rule", "dns", "tls", "dhcp", "wireguard", "capture", "wait", "remove", "restore"}

// stepKind returns the kinds a step sets.
func stepKind(s model.Step) []string {
	var set []string
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("profile", s.Profile != nil)
	add("fault", s.Fault != nil)
	add("rule", s.Rule != nil)
	add("dns", s.Dns != nil)
	add("tls", s.Tls != nil)
	add("dhcp", s.Dhcp != nil)
	add("wireguard", s.Wireguard != nil)
	add("capture", s.Capture != nil)
	add("wait", s.Wait != nil)
	add("remove", s.Remove != nil)
	add("restore", s.Restore != nil)
	return set
}

// createsOverlay reports whether a step of that kind creates an overlay of the run.
func createsOverlay(kind string) bool {
	switch kind {
	case "profile", "fault", "rule", "dns", "tls", "dhcp", "wireguard":
		return true
	}
	return false
}

// timelineOrder returns the step indexes in the order the engine runs them: by `at`, and in
// list order for equal `at` (plan §2.10).
func timelineOrder(steps []model.Step) []int {
	order := make([]int, len(steps))
	for i := range order {
		order[i] = i
	}
	at := func(i int) time.Duration { return orDuration(&steps[i].At, 0) }
	sort.SliceStable(order, func(a, b int) bool { return at(order[a]) < at(order[b]) })
	return order
}

// ValidateScenario checks a scenario against a configuration: used when a scenario is imported
// or run from a file (`POST /runs` with an inline scenario) without becoming part of a
// revision. References may be names; errors are reported relative to the scenario.
func ValidateScenario(cfg *model.Configuration, sc *model.Scenario) []model.ValidationError {
	work := clone(*cfg)
	idx, _ := BuildIndex(&work)
	local := clone(*sc)
	var errs []model.ValidationError
	visitScenario("", &local, func(path string, kind Kind, ref *string) {
		if id, ok := idx.Resolve(kind, *ref); ok {
			*ref = id
			return
		}
		errs = append(errs, unknownRef(path, kind, *ref))
	})
	v := &validator{cfg: &work, idx: idx}
	v.scenario("", local)
	return sortErrors(append(errs, v.errs...))
}

func (v *validator) scenario(path string, sc model.Scenario) {
	v.scope(path+"/target", &sc.Target, scopeOpts{only: []string{"device", "group", "network"}})

	// step ids
	ids := map[string]int{}
	for i, st := range sc.Steps {
		p := schema.Pointer(path+"/steps", itoa(i))
		if st.Id == "start" {
			v.add(p+"/id", CodeReservedID, "start is reserved for the beginning of the run")
		} else if first, dup := ids[st.Id]; dup {
			v.add(p+"/id", CodeDuplicateStep, "the step id %q is already used by step %d", st.Id, first)
		}
		ids[st.Id] = i
	}
	// the first occurrence of an id is the one that references resolve to
	first := map[string]int{}
	for i, st := range sc.Steps {
		if _, ok := first[st.Id]; !ok {
			first[st.Id] = i
		}
	}
	position := map[int]int{} // step index → position on the timeline
	for pos, i := range timelineOrder(sc.Steps) {
		position[i] = pos
	}

	for i, st := range sc.Steps {
		v.step(schema.Pointer(path+"/steps", itoa(i)), i, st, sc, first, position)
	}
	for i, c := range deref(sc.Checks) {
		v.check(schema.Pointer(path+"/checks", itoa(i)), c, sc, first, position)
	}
}

func (v *validator) step(path string, i int, st model.Step, sc model.Scenario, first map[string]int, position map[int]int) {
	kinds := stepKind(st)
	if len(kinds) != 1 {
		v.add(path, CodeStepKind, "a step has exactly one of: %s (found %d)", strings.Join(stepKinds, ", "), len(kinds))
		return
	}
	kind := kinds[0]
	if at, ok := parseDuration(st.At); !ok || at < 0 {
		v.add(path+"/at", CodeInvalidDuration, "the offset must not be negative")
	}

	target := sc.Target
	if st.Target != nil {
		v.scope(path+"/target", st.Target, scopeOpts{})
		if !v.contained(*st.Target, sc.Target) {
			v.add(path+"/target", CodeTargetWidened, "a step may narrow the scenario target, never widen it")
		}
		target = *st.Target
	}

	switch kind {
	case "fault":
		v.faultBody(path+"/fault", *st.Fault)
		if st.Fault.Family != nil && *st.Fault.Family == "tunnel" {
			v.tunnelInsideTarget(path+"/fault/tunnel", st.Fault.Tunnel, target)
		}
	case "rule":
		v.accessRuleBody(path+"/rule", *st.Rule)
	case "dns":
		v.dnsFault(path+"/dns", *st.Dns)
	case "tls":
		v.tlsCase(path+"/tls", *st.Tls)
	case "dhcp":
		v.dhcpAction(path+"/dhcp", *st.Dhcp)
		if k := scopeKind(&target); k != "device" && k != "network" {
			v.add(path+"/dhcp", CodeTargetKind, "a DHCP action targets a device or a network, not a %s", k)
		}
	case "wireguard":
		v.wireguardAction(path+"/wireguard", *st.Wireguard)
		v.tunnelInsideTarget(path+"/wireguard", &model.TunnelRef{Client: st.Wireguard.Client, Link: st.Wireguard.Link}, target)
	case "wait":
		v.wait(path+"/wait", *st.Wait)
	case "remove":
		ref, ok := first[*st.Remove]
		switch {
		case !ok:
			v.add(path+"/remove", CodeStepOrder, "there is no step %q", *st.Remove)
		case !createsOverlay(firstKind(sc.Steps[ref])):
			v.add(path+"/remove", CodeStepOrder, "step %q creates no overlay that could be removed", *st.Remove)
		case position[ref] >= position[i]:
			v.add(path+"/remove", CodeStepOrder, "step %q runs after this step", *st.Remove)
		}
	}
}

func firstKind(s model.Step) string {
	if k := stepKind(s); len(k) > 0 {
		return k[0]
	}
	return ""
}

// tunnelInsideTarget checks that a tunnel fault or WireGuard action only touches a tunnel that
// lies inside the target: the target is that WireGuard network or that client.
func (v *validator) tunnelInsideTarget(path string, t *model.TunnelRef, target model.Scope) {
	if t == nil {
		return
	}
	ok := false
	switch {
	case t.Client != nil:
		c, known := v.idx.Devices[*t.Client]
		ok = known && ((target.Device != nil && *target.Device == c.ID) || (target.Network != nil && *target.Network == c.Network))
	case t.Link != nil:
		ok = target.Network != nil && *target.Network == *t.Link
	}
	if ok {
		return
	}
	// references that did not resolve are reported elsewhere
	if (t.Client != nil && v.idx.Devices[*t.Client] == nil) || (t.Link != nil && v.idx.Networks[*t.Link] == nil) {
		return
	}
	v.add(path, CodeTunnelOutsideTarget, "a tunnel fault or WireGuard action affects everything in the tunnel: the target must be that client or its WireGuard network")
}

func (v *validator) wait(path string, w model.WaitCondition) {
	v.match(path, convert[model.TrafficMatch](w))
	if got, ok := parseDuration(w.Timeout); !ok || got <= 0 {
		v.add(path+"/timeout", CodeInvalidDuration, "the timeout must be longer than zero")
	}
	needsName := w.For == "dns_query"
	switch {
	case needsName && w.Name == nil:
		v.add(path+"/name", CodeMissingField, "waiting for a DNS query needs the name")
	case !needsName && w.Name != nil:
		v.add(path+"/name", CodeUnexpectedField, "name is only used when waiting for a dns_query")
	}
}

// contained reports whether every packet the inner scope selects is also selected by the outer
// one, as far as the configuration tells (devices belong to one network).
func (v *validator) contained(inner, outer model.Scope) bool {
	ik, ok := scopeKind(&inner), scopeKind(&outer)
	switch ok {
	case "device":
		return ik == "device" && *inner.Device == *outer.Device
	case "group":
		switch ik {
		case "group":
			return *inner.Group == *outer.Group
		case "device":
			return v.inGroup(*inner.Device, *outer.Group)
		}
	case "network":
		switch ik {
		case "network":
			return *inner.Network == *outer.Network
		case "device":
			return v.deviceNetwork(*inner.Device) == *outer.Network
		case "group":
			members := v.groupMembers(*inner.Group)
			for _, m := range members {
				if v.deviceNetwork(m) != *outer.Network {
					return false
				}
			}
			return true
		case "remote_network":
			if c := inner.RemoteNetwork.Client; c != nil {
				return v.deviceNetwork(*c) == *outer.Network
			}
			if l := inner.RemoteNetwork.Link; l != nil {
				return *l == *outer.Network
			}
		}
	}
	return false
}

func (v *validator) deviceNetwork(id string) string {
	if d, ok := v.idx.Devices[id]; ok {
		return d.Network
	}
	return ""
}

func (v *validator) groupMembers(id string) []string {
	return deref(deref(v.cfg.Groups)[id].Members)
}

func (v *validator) inGroup(device, group string) bool {
	for _, m := range v.groupMembers(group) {
		if m == device {
			return true
		}
	}
	return false
}

// checkKinds lists the check types.
var checkKinds = []string{"reconnected", "no_connection", "traffic_seen", "traffic_not_seen", "dns_query_seen", "tls_rejected", "tls_accepted"}

func (v *validator) check(path string, c model.Check, sc model.Scenario, first map[string]int, position map[int]int) {
	var kinds []string
	add := func(name string, present bool) {
		if present {
			kinds = append(kinds, name)
		}
	}
	add("reconnected", c.Reconnected != nil)
	add("no_connection", c.NoConnection != nil)
	add("traffic_seen", c.TrafficSeen != nil)
	add("traffic_not_seen", c.TrafficNotSeen != nil)
	add("dns_query_seen", c.DnsQuerySeen != nil)
	add("tls_rejected", c.TlsRejected != nil)
	add("tls_accepted", c.TlsAccepted != nil)
	if len(kinds) != 1 {
		v.add(path, CodeStepKind, "a check has exactly one of: %s (found %d)", strings.Join(checkKinds, ", "), len(kinds))
	}
	for name, m := range map[string]*model.TrafficMatch{
		"reconnected": c.Reconnected, "no_connection": c.NoConnection, "traffic_seen": c.TrafficSeen,
		"tls_rejected": c.TlsRejected, "tls_accepted": c.TlsAccepted,
	} {
		if m != nil {
			v.match(path+"/"+name, *m)
		}
	}
	if c.TrafficNotSeen != nil {
		v.match(path+"/traffic_not_seen", convert[model.TrafficMatch](*c.TrafficNotSeen))
	}

	w := c.Window
	from, fromOK := windowPosition(w.From, first, position)
	if !fromOK {
		v.add(path+"/window/from", CodeCheckWindow, "%q is neither start nor a step of the scenario", w.From)
	}
	if (w.Within == nil) == (w.Until == nil) {
		v.add(path+"/window", CodeCheckWindow, "give exactly one of within and until")
	}
	if w.Within != nil {
		if got, ok := parseDuration(*w.Within); !ok || got <= 0 {
			v.add(path+"/window/within", CodeInvalidDuration, "the window must be longer than zero")
		}
	}
	if w.Until != nil {
		until, ok := windowPosition(*w.Until, first, position)
		switch {
		case *w.Until == "start" || !ok:
			v.add(path+"/window/until", CodeCheckWindow, "%q is not a step of the scenario", *w.Until)
		case fromOK && until <= from:
			v.add(path+"/window/until", CodeCheckWindow, "the window ends at step %q, which does not come after its start %q", *w.Until, w.From)
		}
	}
}

// windowPosition returns the position of a step on the timeline; "start" is before every step.
func windowPosition(id string, first map[string]int, position map[int]int) (int, bool) {
	if id == "start" {
		return -1, true
	}
	i, ok := first[id]
	if !ok {
		return 0, false
	}
	return position[i], true
}
