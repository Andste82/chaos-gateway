package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is precedence resolution (plan §2.4): which fault, per family, applies to the
// traffic of a device, and why the others do not.
//
//   - Overlays always win over the configuration (D24): for every family the matching overlays
//     are resolved first; only without one, the matching configured faults.
//   - Within a layer the most specific scope wins (the ten levels of the plan).
//   - On the same scope and level a fault beats a part of an activated profile; across scopes of
//     the same level the newer entry wins (D26: no explicit priorities). "Newer" is created_at for
//     configured faults and updated_at for overlays.
//   - Parameters are never merged: the winner's complete set applies.
//   - Faults of different families combine.
//   - Faults act on the initiator's traffic (initiator semantics): the query's source is the
//     device that opened the connection.
//
// Resolution is a function of one query: the traffic of one device towards one destination. The
// compiler (M4, M7) expands it over all devices and selectors to build its maps; it can use
// Candidates, Level and the order defined here for that, but the expansion itself is not part of
// the domain.

// Layer says where a candidate comes from.
type Layer string

const (
	LayerOverlay Layer = "overlay"
	LayerConfig  Layer = "config"
)

// Families that are resolved by scope and specificity. The tunnel family is resolved per
// tunnel (ResolveTunnels) and the access rules by order (ResolveAccess).
const (
	FamilyImpairment = "impairment"
	FamilyMTU        = "mtu"
	FamilyDNS        = "dns"
	FamilyTLS        = "tls"
	FamilyDHCP       = "dhcp"
	FamilyTunnel     = "tunnel"
)

// resolvedFamilies is the order in which families are reported.
var resolvedFamilies = []string{FamilyImpairment, FamilyMTU, FamilyDNS, FamilyTLS, FamilyDHCP}

// World is the input of a resolution: a configuration whose references are UUIDs (see
// Normalize) and the active overlays, also with UUID references (see ValidateOverlay).
type World struct {
	Config   *model.Configuration
	Index    *Index
	Overlays []model.Overlay

	prefixes map[string][]netip.Prefix // per network: the prefixes that belong to it
	mgmt     []netip.Prefix            // management sources: not reached via the uplink

	candOnce sync.Once
	cands    []Candidate // candidates(), computed once: a World never changes

	tabMu    sync.Mutex
	tabCache map[string]tableResult // Table's results by the candidates they were built from
}

// NewWorld prepares a resolution. The configuration must be normalized (see Normalize): it
// refuses one that still has names instead of UUIDs.
func NewWorld(cfg *model.Configuration, overlays []model.Overlay) (*World, error) {
	if !IsNormalized(cfg) {
		return nil, errors.New("domain: NewWorld needs a normalized configuration")
	}
	idx, _ := BuildIndex(cfg)
	w := &World{Config: cfg, Index: idx, Overlays: overlays, prefixes: map[string][]netip.Prefix{}}
	for _, id := range sortedKeys(idx.Networks) {
		w.prefixes[id] = prefixesOf(idx.Networks[id])
	}
	for _, s := range deref(cfg.Management.AllowedSources) {
		if p, ok := parsePrefix(s); ok {
			w.mgmt = append(w.mgmt, p.Masked())
		}
	}
	return w, nil
}

// prefixesOf returns the prefixes that belong to a network: its subnet, for a hub also the
// networks behind its clients, for a link its static routes.
func prefixesOf(n *NetInfo) []netip.Prefix {
	var out []netip.Prefix
	if s, ok := networkSubnet(n); ok {
		out = append(out, s)
	}
	if n.IsHub() {
		for _, c := range deref(n.WG.Clients) {
			for _, cn := range deref(c.ClientNetworks) {
				if p, ok := parsePrefix(cn); ok {
					out = append(out, p.Masked())
				}
			}
		}
	}
	if n.IsLink() {
		for _, r := range deref(n.WG.Routes) {
			if p, ok := parsePrefix(r); ok {
				out = append(out, p.Masked())
			}
		}
	}
	return out
}

// Subject is the device whose traffic is looked at: the initiator of the connection.
type Subject struct {
	// Device is the UUID in the device namespace; empty when the device is not known.
	Device string
	// Network is the network the device lives in, when the observed state says so.
	Network string
	// IP is the current address; it places devices in networks and client networks.
	IP netip.Addr
}

// Query describes the traffic to resolve for. Selectors of a candidate only apply when the query
// states the matching part: a fault that names a destination does not apply to a query without
// one (this is how "the winner for the device's traffic in general" is asked for).
type Query struct {
	Source Subject
	// DestIP is the destination address; DestNames are the names the device resolved to it.
	DestIP    netip.Addr
	DestNames []string
	Protocol  string // tcp, udp, icmp or "" when unspecified
	Port      int    // 0 when unspecified
	// DNSName is the name being resolved (DNS family); SNI the server name of a TLS handshake.
	DNSName string
	SNI     string
}

// Candidate is a fault, a part of an activated profile or a DHCP action that competes in a
// family.
type Candidate struct {
	Family string
	Layer  Layer
	// ID is the overlay's or the configured fault's UUID.
	ID, Name string
	// Profile is set when the candidate is a part of an activated profile.
	ProfileID, ProfileName string
	Level                  int       // 1–10 (plan §2.4); 0 for tunnel faults
	Since                  time.Time // created_at (configuration) or updated_at (overlay)
	Scope                  model.Scope
	Match                  model.TrafficMatch

	Impairment *model.FaultBody
	MTU        *model.MtuParams
	DNS        *model.DnsFault
	TLS        *model.TlsCase
	DHCP       *model.DhcpAction
	Tunnel     *model.TunnelRef
}

// IsProfilePart reports whether the candidate comes from an activated profile.
func (c Candidate) IsProfilePart() bool { return c.ProfileID != "" }

// Overridden is a candidate that matched but lost, with the reason.
type Overridden struct {
	Candidate
	Reason string
}

// FamilyResult is the outcome for one family.
type FamilyResult struct {
	Family     string
	Winner     *Candidate
	Overridden []Overridden
}

// ---- candidates -------------------------------------------------------------------------

// specificity returns the level of plan §2.4 for a scope, with or without a selector part.
func specificity(scope string, hasDest, hasPortProto bool) int {
	sel := hasDest || hasPortProto
	switch scope {
	case "device":
		switch {
		case hasDest && hasPortProto:
			return 1
		case hasDest:
			return 2
		case hasPortProto:
			return 3
		}
		return 4
	case "group":
		if sel {
			return 5
		}
		return 6
	case "network", "remote_network":
		if sel {
			return 7
		}
		return 8
	}
	if sel {
		return 9
	}
	return 10
}

func hasPortProto(m model.TrafficMatch) bool {
	return protocolOf(m) != "any" || len(deref(m.Ports)) > 0 || len(deref(m.PortRanges)) > 0
}

func isEnabled(b *bool) bool { return b == nil || *b }

func newCandidate(family string, layer Layer, id, name string, since time.Time, scope model.Scope, hasDest, hasPP bool) Candidate {
	return Candidate{
		Family: family, Layer: layer, ID: id, Name: name, Since: since, Scope: scope,
		Level: specificity(scopeKind(&scope), hasDest, hasPP),
	}
}

func impairmentCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, body model.FaultBody) Candidate {
	m := convert[model.TrafficMatch](body)
	c := newCandidate(FamilyImpairment, layer, id, name, since, scope, m.Destination != nil, hasPortProto(m))
	c.Match, c.Impairment = m, &body
	return c
}

func mtuCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, body model.FaultBody) Candidate {
	m := convert[model.TrafficMatch](body)
	c := newCandidate(FamilyMTU, layer, id, name, since, scope, m.Destination != nil, hasPortProto(m))
	c.Match, c.MTU = m, body.Mtu
	return c
}

func dnsCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, d model.DnsFault) Candidate {
	c := newCandidate(FamilyDNS, layer, id, name, since, scope, len(deref(d.Names)) > 0, false)
	c.DNS = &d
	return c
}

func tlsCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, t model.TlsCase) Candidate {
	m := convert[model.TrafficMatch](t)
	// without ports the case applies to the default TLS ports, so it always has a port part
	c := newCandidate(FamilyTLS, layer, id, name, since, scope, m.Destination != nil || len(deref(t.Sni)) > 0, true)
	c.Match, c.TLS = m, &t
	return c
}

func dhcpCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, d model.DhcpAction) Candidate {
	c := newCandidate(FamilyDHCP, layer, id, name, since, scope, false, false)
	c.DHCP = &d
	return c
}

// profile looks up a profile by UUID: configured ones first, then the built-in ones.
func (w *World) profile(id string) (model.Profile, bool) {
	if p, ok := deref(w.Config.Profiles)[id]; ok {
		return p, true
	}
	for _, b := range builtinProfiles {
		if b.ID == id {
			return clone(b.Profile), true
		}
	}
	return model.Profile{}, false
}

// candidates returns every candidate of the families resolved by scope, from both layers,
// before any query is applied. The slice is shared and must not be modified.
func (w *World) candidates() []Candidate {
	w.candOnce.Do(func() { w.cands = w.buildCandidates() })
	return w.cands
}

func (w *World) buildCandidates() []Candidate {
	var out []Candidate
	for _, id := range sortedKeys(deref(w.Config.Faults)) {
		f := deref(w.Config.Faults)[id]
		if !isEnabled(f.Enabled) || f.Source == nil {
			continue
		}
		body := convert[model.FaultBody](f)
		name := deref(f.Name)
		since := deref(f.CreatedAt)
		switch family := faultFamily(body); family {
		case FamilyImpairment:
			out = append(out, impairmentCandidate(LayerConfig, id, name, since, *f.Source, body))
		case FamilyMTU:
			out = append(out, mtuCandidate(LayerConfig, id, name, since, *f.Source, body))
		}
	}
	for _, o := range w.Overlays {
		id := o.Id.String()
		since := o.UpdatedAt
		switch o.Kind {
		case "fault":
			if o.Target == nil || o.Fault == nil {
				continue
			}
			switch faultFamily(*o.Fault) {
			case FamilyImpairment:
				out = append(out, impairmentCandidate(LayerOverlay, id, "", since, *o.Target, *o.Fault))
			case FamilyMTU:
				out = append(out, mtuCandidate(LayerOverlay, id, "", since, *o.Target, *o.Fault))
			}
		case "dns":
			if o.Target != nil && o.Dns != nil {
				out = append(out, dnsCandidate(LayerOverlay, id, "", since, *o.Target, *o.Dns))
			}
		case "tls":
			if o.Target != nil && o.Tls != nil {
				out = append(out, tlsCandidate(LayerOverlay, id, "", since, *o.Target, *o.Tls))
			}
		case "dhcp":
			// delete_lease acts once; it is not a state that could win or lose
			if o.Target != nil && o.Dhcp != nil && o.Dhcp.Action != "delete_lease" {
				out = append(out, dhcpCandidate(LayerOverlay, id, "", since, *o.Target, *o.Dhcp))
			}
		case "profile":
			if o.Target == nil || o.Profile == nil {
				continue
			}
			out = append(out, w.profileParts(id, since, *o.Target, *o.Profile)...)
		}
	}
	return out
}

func faultFamily(f model.FaultBody) string {
	if f.Family == nil {
		return FamilyImpairment
	}
	return string(*f.Family)
}

// profileParts expands the activation of a profile into one candidate per part.
func (w *World) profileParts(overlayID string, since time.Time, scope model.Scope, ref string) []Candidate {
	p, ok := w.profile(ref)
	if !ok {
		return nil
	}
	var out []Candidate
	tag := func(c Candidate) Candidate {
		c.ProfileID, c.ProfileName = ref, p.Name
		return c
	}
	if p.Parts.Impairment != nil {
		out = append(out, tag(impairmentCandidate(LayerOverlay, overlayID, "", since, scope, convert[model.FaultBody](*p.Parts.Impairment))))
	}
	if p.Parts.Mtu != nil {
		out = append(out, tag(mtuCandidate(LayerOverlay, overlayID, "", since, scope, model.FaultBody{Mtu: p.Parts.Mtu})))
	}
	if p.Parts.Dns != nil {
		out = append(out, tag(dnsCandidate(LayerOverlay, overlayID, "", since, scope, *p.Parts.Dns)))
	}
	if p.Parts.Tls != nil {
		out = append(out, tag(tlsCandidate(LayerOverlay, overlayID, "", since, scope, *p.Parts.Tls)))
	}
	return out
}

// ---- matching ---------------------------------------------------------------------------

// subjectNetworks returns the networks the subject is in: the one it is known to be in, the
// one it is configured in, and every network that has the subject's address among its prefixes
// (for a hub that includes the networks behind its clients).
func (w *World) subjectNetworks(s Subject) map[string]bool {
	out := map[string]bool{}
	if s.Network != "" {
		out[s.Network] = true
	}
	if d, ok := w.Index.Devices[s.Device]; ok && d.Network != "" {
		out[d.Network] = true
	}
	if s.IP.IsValid() {
		for id, prefixes := range w.prefixes {
			for _, p := range prefixes {
				if p.Contains(s.IP) {
					out[id] = true
				}
			}
		}
	}
	return out
}

// scopeMatches reports whether the subject lies in the scope.
func (w *World) scopeMatches(sc model.Scope, s Subject) bool {
	switch scopeKind(&sc) {
	case "device":
		return s.Device != "" && lower(*sc.Device) == s.Device
	case "group":
		return s.Device != "" && w.inGroup(s.Device, lower(*sc.Group))
	case "network":
		return w.subjectNetworks(s)[lower(*sc.Network)]
	case "remote_network":
		return w.inRemoteNetwork(*sc.RemoteNetwork, s.IP)
	case "global":
		return true
	}
	return false
}

func (w *World) inGroup(device, group string) bool {
	for _, m := range deref(deref(w.Config.Groups)[group].Members) {
		if lower(m) == device {
			return true
		}
	}
	return false
}

func (w *World) inRemoteNetwork(r model.RemoteNetworkRef, ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	switch {
	case r.Client != nil:
		if d, ok := w.Index.Devices[lower(*r.Client)]; ok && d.Client != nil {
			for _, cn := range deref(d.Client.ClientNetworks) {
				if p, ok := parsePrefix(cn); ok && p.Contains(ip) {
					return true
				}
			}
		}
	case r.Link != nil:
		if n, ok := w.Index.Networks[lower(*r.Link)]; ok && n.IsLink() {
			for _, route := range deref(n.WG.Routes) {
				if p, ok := parsePrefix(route); ok && p.Contains(ip) {
					return true
				}
			}
		}
	case r.Cidr != nil:
		if p, ok := parsePrefix(*r.Cidr); ok {
			return p.Contains(ip)
		}
	}
	return false
}

// nameMatches reports whether a DNS name matches an exact name or a `*.suffix` pattern. The
// pattern `*.example.com` matches the subdomains of example.com, not example.com itself.
func nameMatches(pattern, name string) bool {
	pattern, name = lower(pattern), lower(name)
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(name, "."+suffix)
	}
	return pattern == name
}

func anyNameMatches(patterns []string, names ...string) bool {
	for _, p := range patterns {
		for _, n := range names {
			if n != "" && nameMatches(p, n) {
				return true
			}
		}
	}
	return false
}

// viaUplink reports whether an address is routed via the uplink: it belongs to no known network.
func (w *World) viaUplink(ip netip.Addr) bool {
	for _, prefixes := range w.prefixes {
		for _, p := range prefixes {
			if p.Contains(ip) {
				return false
			}
		}
	}
	for _, p := range w.mgmt { // the management network is reached through its own interface
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func (w *World) destinationMatches(d *model.Destination, q Query) bool {
	if d == nil {
		return true
	}
	switch {
	case d.Hostname != nil:
		return anyNameMatches([]string{*d.Hostname}, q.DestNames...)
	case !q.DestIP.IsValid():
		return false
	case d.Uplink != nil:
		return w.viaUplink(q.DestIP)
	case d.Network != nil:
		for _, p := range w.prefixes[lower(*d.Network)] {
			if p.Contains(q.DestIP) {
				return true
			}
		}
		return false
	case d.Cidr != nil:
		if a, ok := parseAddr(*d.Cidr); ok {
			return a == q.DestIP
		}
		p, ok := parsePrefix(*d.Cidr)
		return ok && p.Contains(q.DestIP)
	}
	return false
}

// trafficMatches applies a selector to the traffic of the query.
func (w *World) trafficMatches(m model.TrafficMatch, q Query) bool {
	if p := protocolOf(m); p != "any" && q.Protocol != p {
		return false
	}
	if len(deref(m.Ports)) > 0 || len(deref(m.PortRanges)) > 0 {
		if q.Port == 0 || !portIn(m, q.Port) {
			return false
		}
	}
	return w.destinationMatches(m.Destination, q)
}

func portIn(m model.TrafficMatch, port int) bool {
	for _, p := range deref(m.Ports) {
		if p == port {
			return true
		}
	}
	for _, r := range deref(m.PortRanges) {
		if port >= r.From && port <= r.To {
			return true
		}
	}
	return false
}

// defaultTLSPorts are the ports a TLS case applies to when it names none (plan §2.8).
var defaultTLSPorts = []int{443, 8883}

func (w *World) candidateMatches(c Candidate, q Query) bool {
	if !w.scopeMatches(c.Scope, q.Source) {
		return false
	}
	switch c.Family {
	case FamilyImpairment, FamilyMTU:
		return w.trafficMatches(c.Match, q)
	case FamilyDNS:
		names := deref(c.DNS.Names)
		return len(names) == 0 || anyNameMatches(names, q.DNSName)
	case FamilyTLS:
		m := c.Match
		if len(deref(m.Ports)) == 0 && len(deref(m.PortRanges)) == 0 {
			m.Ports = &defaultTLSPorts
		}
		if p := protocolOf(m); p != "tcp" && p != "any" {
			return false
		}
		if m.Protocol == nil || *m.Protocol == "any" {
			tcp := model.Protocol("tcp")
			m.Protocol = &tcp
		}
		if sni := deref(c.TLS.Sni); len(sni) > 0 && !anyNameMatches(sni, q.SNI) {
			return false
		}
		return w.trafficMatches(m, q)
	case FamilyDHCP:
		return true
	}
	return false
}

// ---- resolution -------------------------------------------------------------------------

// reason explains why loser lost against winner.
func reason(winner, loser Candidate) string {
	switch {
	case winner.Layer != loser.Layer:
		return "overlay layer wins (D24)"
	case winner.Level != loser.Level:
		return fmt.Sprintf("level %d beats level %d", winner.Level, loser.Level)
	case winner.IsProfilePart() != loser.IsProfilePart() && sameScope(winner, loser):
		return "a fault beats a profile part at the same scope"
	case !winner.Since.Equal(loser.Since):
		return "newer at the same level"
	}
	return "tie at the same level and time: the higher id wins"
}

// sameScope reports whether two candidates apply to the very same scope.
func sameScope(a, b Candidate) bool { return scopeKey(&a.Scope) == scopeKey(&b.Scope) }

// newer reports whether a is newer than b (D26): by time, then, at the very same time, by the
// higher id, which is the newer one for time-ordered UUIDs (v7) and otherwise just a stable
// choice.
func newer(a, b Candidate) bool {
	if !a.Since.Equal(b.Since) {
		return a.Since.After(b.Since)
	}
	return a.ID > b.ID
}

// champion reports whether a beats b on the very same scope and level: a fault beats a part of
// an activated profile (E8), and otherwise the newer entry wins.
func champion(a, b Candidate) bool {
	if a.IsProfilePart() != b.IsProfilePart() {
		return !a.IsProfilePart()
	}
	return newer(a, b)
}

// winnerOf picks the winner of one layer's candidates: the most specific level; on that level
// every scope first puts forward its champion (a fault before a profile part, then the newer
// entry), and the newest champion wins (D26, E6, E8). The rule "a fault beats a profile part"
// holds on the same scope only, so it is applied per scope, not as a pairwise order: a pairwise
// order over all candidates of the level would not be transitive (a beats b on one scope, b is
// newer than c, c is newer than a) and the winner would depend on the input order.
func winnerOf(cs []Candidate) int {
	level := cs[0].Level
	for _, c := range cs {
		if c.Level < level {
			level = c.Level
		}
	}
	champions := map[string]int{} // scope → index of its champion
	for i, c := range cs {
		if c.Level != level {
			continue
		}
		key := scopeKey(&c.Scope)
		if cur, ok := champions[key]; !ok || champion(c, cs[cur]) {
			champions[key] = i
		}
	}
	win := -1
	for _, key := range sortedKeys(champions) {
		if i := champions[key]; win < 0 || newer(cs[i], cs[win]) {
			win = i
		}
	}
	return win
}

// rankLosers orders the candidates that lost, for the explanation: the most specific level first,
// then the newer ones.
func rankLosers(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Level != cs[j].Level {
			return cs[i].Level < cs[j].Level
		}
		return newer(cs[i], cs[j])
	})
}

// resolveFamily applies the rules to the candidates of one family.
func resolveFamily(family string, matching []Candidate) (FamilyResult, bool) {
	var overlays, configs []Candidate
	for _, c := range matching {
		if c.Family != family {
			continue
		}
		if c.Layer == LayerOverlay {
			overlays = append(overlays, c)
		} else {
			configs = append(configs, c)
		}
	}
	if len(overlays)+len(configs) == 0 {
		return FamilyResult{}, false
	}
	// overlays before configuration (D24): a configured fault is only considered without an overlay
	winning, other := configs, overlays
	if len(overlays) > 0 {
		winning, other = overlays, configs
	}
	wi := winnerOf(winning)
	win := winning[wi]
	res := FamilyResult{Family: family, Winner: &win}
	var losers []Candidate
	for i, c := range winning {
		if i != wi {
			losers = append(losers, c)
		}
	}
	rankLosers(losers)
	rankLosers(other)
	for _, loser := range append(losers, other...) {
		res.Overridden = append(res.Overridden, Overridden{Candidate: loser, Reason: reason(win, loser)})
	}
	return res, true
}

// Resolve returns, for every family that has a matching candidate, the winner and the
// candidates it overrode (plan §2.4). Faults of different families combine, so every family
// has its own winner.
func (w *World) Resolve(q Query) []FamilyResult {
	var matching []Candidate
	for _, c := range w.candidates() {
		if w.candidateMatches(c, q) {
			matching = append(matching, c)
		}
	}
	var out []FamilyResult
	for _, family := range resolvedFamilies {
		if r, ok := resolveFamily(family, matching); ok {
			out = append(out, r)
		}
	}
	return out
}

// Winner returns the winning candidate of a family in a resolution, or nil.
func Winner(results []FamilyResult, family string) *Candidate {
	for _, r := range results {
		if r.Family == family {
			return r.Winner
		}
	}
	return nil
}

// ---- tunnel faults ----------------------------------------------------------------------

// TunnelResult is the outcome for one tunnel: tunnel faults have no scope and no level, they
// are resolved per tunnel, overlays before configuration, the newest wins.
type TunnelResult struct {
	// Tunnel identifies the tunnel: "client:<id>" or "link:<id>".
	Tunnel     string
	Winner     Candidate
	Overridden []Overridden
}

// ResolveTunnels resolves the tunnel faults, one result per tunnel that has any.
func (w *World) ResolveTunnels() []TunnelResult {
	groups := map[string][]Candidate{}
	add := func(layer Layer, id, name string, since time.Time, body model.FaultBody) {
		if faultFamily(body) != FamilyTunnel || body.Tunnel == nil {
			return
		}
		key := tunnelKey(body.Tunnel)
		groups[key] = append(groups[key], Candidate{
			Family: FamilyTunnel, Layer: layer, ID: id, Name: name, Since: since,
			Impairment: &body, Tunnel: body.Tunnel,
		})
	}
	for _, id := range sortedKeys(deref(w.Config.Faults)) {
		f := deref(w.Config.Faults)[id]
		if isEnabled(f.Enabled) {
			add(LayerConfig, id, deref(f.Name), deref(f.CreatedAt), convert[model.FaultBody](f))
		}
	}
	for _, o := range w.Overlays {
		if o.Kind == "fault" && o.Fault != nil {
			add(LayerOverlay, o.Id.String(), "", o.UpdatedAt, *o.Fault)
		}
	}
	var out []TunnelResult
	for _, key := range sortedKeys(groups) {
		cs := groups[key]
		sort.SliceStable(cs, func(i, j int) bool {
			a, b := cs[i], cs[j]
			if a.Layer != b.Layer {
				return a.Layer == LayerOverlay
			}
			if !a.Since.Equal(b.Since) {
				return a.Since.After(b.Since)
			}
			return a.ID > b.ID
		})
		r := TunnelResult{Tunnel: key, Winner: cs[0]}
		for _, loser := range cs[1:] {
			why := "newer"
			if loser.Layer != cs[0].Layer {
				why = "overlay layer wins (D24)"
			}
			r.Overridden = append(r.Overridden, Overridden{Candidate: loser, Reason: why})
		}
		out = append(out, r)
	}
	return out
}

// ---- access rules -----------------------------------------------------------------------

// AccessResult is the outcome of the access rules for a query. Without a matching rule the
// verdict comes from the access matrix (the compiler's business).
type AccessResult struct {
	Matched bool
	Layer   Layer
	// RuleID and Name identify the rule; Action is allow, drop, reject or reset.
	RuleID, Name string
	Action       string
	CutExisting  bool
}

// ResolveAccess finds the rule that decides: first match wins; overlay rules come before
// configured rules, newest overlay first, configured rules in their order (plan §2.4).
func (w *World) ResolveAccess(q Query) AccessResult {
	type overlayRule struct {
		id    string
		since time.Time
		sc    model.Scope
		body  model.AccessRuleBody
	}
	var ov []overlayRule
	for _, o := range w.Overlays {
		if o.Kind == "rule" && o.Rule != nil && o.Target != nil {
			ov = append(ov, overlayRule{o.Id.String(), o.UpdatedAt, *o.Target, *o.Rule})
		}
	}
	sort.SliceStable(ov, func(i, j int) bool {
		if !ov[i].since.Equal(ov[j].since) {
			return ov[i].since.After(ov[j].since)
		}
		return ov[i].id < ov[j].id
	})
	for _, r := range ov {
		if w.scopeMatches(r.sc, q.Source) && w.trafficMatches(convert[model.TrafficMatch](r.body), q) {
			return AccessResult{true, LayerOverlay, r.id, "", string(r.body.Action), deref(r.body.CutExisting)}
		}
	}
	rules := deref(w.Config.AccessRules)
	for _, uid := range deref(w.Config.AccessRuleOrder) {
		id := uid.String()
		r, ok := rules[id]
		if !ok || !isEnabled(r.Enabled) {
			continue
		}
		if w.scopeMatches(r.Source, q.Source) && w.trafficMatches(convert[model.TrafficMatch](r), q) {
			return AccessResult{true, LayerConfig, id, deref(r.Name), string(r.Action), deref(r.CutExisting)}
		}
	}
	return AccessResult{}
}

// ---- the API's view of a result ----------------------------------------------------------

// Ref converts a candidate into the FaultRef of the API; a non-empty reason marks it as overridden.
func (c Candidate) Ref(reason string) model.FaultRef {
	id, _ := uuid.Parse(c.ID)
	ref := model.FaultRef{Family: model.FaultFamily(c.Family), Layer: model.FaultRefLayer(c.Layer), Id: id}
	if c.Name != "" {
		ref.Name = &c.Name
	}
	if c.Level > 0 {
		level := c.Level
		ref.Level = &level
	}
	if !c.Since.IsZero() {
		since := c.Since
		ref.Since = &since
	}
	if c.IsProfilePart() {
		pid, _ := uuid.Parse(c.ProfileID)
		name := c.ProfileName
		ref.Profile = &struct {
			Id   *model.Uuid `json:"id,omitempty"`
			Name *string     `json:"name,omitempty"`
		}{Id: &pid, Name: &name}
	}
	if s := c.Summary(); s != "" {
		ref.Summary = &s
	}
	if reason != "" {
		ref.Reason = &reason
	}
	return ref
}

// Summary describes what the candidate does, in a few words.
func (c Candidate) Summary() string {
	switch c.Family {
	case FamilyImpairment, FamilyTunnel:
		if c.Impairment == nil {
			return ""
		}
		return summarizeImpairment(*c.Impairment)
	case FamilyMTU:
		if c.MTU != nil {
			mode := "icmp"
			if c.MTU.Mode != nil {
				mode = string(*c.MTU.Mode)
			}
			return fmt.Sprintf("mtu %d (%s)", c.MTU.Size, mode)
		}
	case FamilyDNS:
		if c.DNS != nil {
			return "dns " + string(c.DNS.Action)
		}
	case FamilyTLS:
		if c.TLS != nil {
			return "tls " + string(c.TLS.Case)
		}
	case FamilyDHCP:
		if c.DHCP != nil {
			return "dhcp " + string(c.DHCP.Action)
		}
	}
	return ""
}

func summarizeNetem(p model.NetemParams) string {
	var parts []string
	if p.Latency != nil {
		s := "latency " + *p.Latency
		if p.Jitter != nil {
			s += " ± " + *p.Jitter
		}
		parts = append(parts, s)
	}
	if p.Loss != nil {
		parts = append(parts, "loss "+*p.Loss)
	}
	if p.BurstLoss != nil {
		parts = append(parts, "burst loss")
	}
	if p.Rate != nil {
		parts = append(parts, "rate "+*p.Rate)
	}
	if p.QueueLimit != nil {
		parts = append(parts, fmt.Sprintf("queue %d packets", *p.QueueLimit))
	}
	if p.KeepOrder != nil && *p.KeepOrder {
		parts = append(parts, "keep order")
	}
	if p.Reorder != nil {
		parts = append(parts, "reorder "+*p.Reorder)
	}
	if p.Duplicate != nil {
		parts = append(parts, "duplicate "+*p.Duplicate)
	}
	if p.Corrupt != nil {
		parts = append(parts, "corrupt "+*p.Corrupt)
	}
	if p.Blackout != nil && *p.Blackout {
		parts = append(parts, "blackout")
	}
	if p.Flapping != nil {
		parts = append(parts, fmt.Sprintf("flapping %s up/%s down", p.Flapping.Up, p.Flapping.Down))
	}
	return strings.Join(parts, ", ")
}

func summarizeImpairment(f model.FaultBody) string {
	if f.Upload != nil || f.Download != nil {
		var parts []string
		if f.Upload != nil {
			parts = append(parts, "upload "+summarizeNetem(*f.Upload))
		}
		if f.Download != nil {
			parts = append(parts, "download "+summarizeNetem(*f.Download))
		}
		return strings.Join(parts, ", ")
	}
	return summarizeNetem(convert[model.NetemParams](f))
}
