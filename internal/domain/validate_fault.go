package domain

import (
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// ---- scopes -----------------------------------------------------------------------------

// scopeOpts restricts the kinds of scope that are allowed.
type scopeOpts struct {
	// only lists the allowed kinds (device, group, network, remote_network, global); empty means any.
	only []string
}

func scopeKind(s *model.Scope) string {
	switch {
	case s.Device != nil:
		return "device"
	case s.Group != nil:
		return "group"
	case s.Network != nil:
		return "network"
	case s.RemoteNetwork != nil:
		return "remote_network"
	case s.Global != nil:
		return "global"
	}
	return ""
}

// scope validates a source or target: the allowed kinds, and a remote_network CIDR that lies
// inside a prefix the gateway knows (a client network, a link route, or any prefix when a link
// runs dynamic routing).
func (v *validator) scope(path string, s *model.Scope, o scopeOpts) {
	kind := scopeKind(s)
	if kind == "" {
		return // the schema reports a scope without exactly one property
	}
	if len(o.only) > 0 {
		ok := false
		for _, k := range o.only {
			ok = ok || k == kind
		}
		if !ok {
			v.add(path, CodeTargetKind, "a %s scope is not allowed here (allowed: %s)", kind, strings.Join(o.only, ", "))
		}
	}
	if s.RemoteNetwork != nil && s.RemoteNetwork.Cidr != nil {
		p, ok := v.cidrOK(path+"/remote_network/cidr", *s.RemoteNetwork.Cidr)
		if ok && !v.knownRemote(p) {
			v.add(path+"/remote_network/cidr", CodeRemoteNetwork, "%s is not inside a client network or a route via a link", p)
		}
	}
}

// knownRemote reports whether p lies inside a prefix of a remote network.
func (v *validator) knownRemote(p netip.Prefix) bool {
	for _, id := range sortedKeys(v.idx.Devices) {
		d := v.idx.Devices[id]
		if d.Origin != OriginClient {
			continue
		}
		for _, cn := range deref(d.Client.ClientNetworks) {
			if c, ok := parsePrefix(cn); ok && c.Masked().Contains(p.Addr()) && c.Bits() <= p.Bits() {
				return true
			}
		}
	}
	for _, id := range sortedKeys(v.idx.Networks) {
		n := v.idx.Networks[id]
		if !n.IsLink() {
			continue
		}
		for _, r := range deref(n.WG.Routes) {
			if c, ok := parsePrefix(r); ok && c.Masked().Contains(p.Addr()) && c.Bits() <= p.Bits() {
				return true
			}
		}
	}
	// learned routes are not known here: with dynamic routing on a link any prefix may appear
	return v.cfg.Routing != nil && len(deref(v.cfg.Routing.Protocols)) > 0
}

// ---- impairment (netem) ------------------------------------------------------------------

func pct(s *model.Percentage) float64 {
	if s == nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(*s, "%"), 64)
	if err != nil {
		return 0
	}
	return f
}

// netemSet lists the parameters of a NetemParams that are set, by their JSON name.
func netemSet(p model.NetemParams) []string {
	var set []string
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("latency", p.Latency != nil)
	add("jitter", p.Jitter != nil)
	add("distribution", p.Distribution != nil)
	add("keep_order", p.KeepOrder != nil)
	add("loss", p.Loss != nil)
	add("loss_correlation", p.LossCorrelation != nil)
	add("burst_loss", p.BurstLoss != nil)
	add("rate", p.Rate != nil)
	add("queue_limit", p.QueueLimit != nil)
	add("reorder", p.Reorder != nil)
	add("duplicate", p.Duplicate != nil)
	add("corrupt", p.Corrupt != nil)
	add("blackout", p.Blackout != nil)
	add("flapping", p.Flapping != nil)
	return set
}

// tunnelParams are the only parameters a tunnel fault takes (plan §2.2.1).
var tunnelParams = map[string]bool{"latency": true, "jitter": true, "loss": true, "burst_loss": true, "blackout": true, "flapping": true}

// netem validates one complete parameter set (one direction, or both).
func (v *validator) netem(path string, p model.NetemParams, tunnel bool) {
	latency := orDuration(p.Latency, 0)
	jitter := orDuration(p.Jitter, 0)
	if jitter > latency {
		v.add(path+"/jitter", CodeJitterExceedsLatency, "the jitter %v is larger than the latency %v: netem would clamp it and skew the distribution", jitter, latency)
	}
	if p.Reorder != nil && pct(p.Reorder) > 0 && latency <= 0 {
		v.add(path+"/reorder", CodeReorderNeedsLatency, "reordering needs a latency: packets are reordered by delaying some of them")
	}
	if p.Distribution != nil && jitter <= 0 {
		v.add(path+"/distribution", CodeDistributionNeedsJit, "a distribution shapes the jitter: set a jitter")
	}
	if p.LossCorrelation != nil && p.Loss == nil {
		v.add(path+"/loss_correlation", CodeLossCorrelationNeeds, "the correlation applies to the random loss: set loss")
	}
	if p.Loss != nil && p.BurstLoss != nil {
		v.add(path+"/burst_loss", CodeExclusive, "loss and burst_loss are exclusive: choose one loss model")
	}
	if p.Blackout != nil && *p.Blackout && p.Flapping != nil {
		v.add(path+"/flapping", CodeExclusive, "blackout and flapping are exclusive: flapping is a timed blackout")
	}
	if p.Flapping != nil {
		up, ok1 := parseDuration(p.Flapping.Up)
		down, ok2 := parseDuration(p.Flapping.Down)
		if !ok1 || !ok2 || up <= 0 || down <= 0 {
			v.add(path+"/flapping", CodeInvalidFlapping, "up and down must both be longer than zero")
		}
	}
	if tunnel {
		for _, name := range netemSet(p) {
			if !tunnelParams[name] {
				v.add(path+"/"+name, CodeTunnelParameter, "a tunnel fault takes latency, jitter, loss, burst_loss, blackout and flapping only")
			}
		}
	}
}

// impairment validates ImpairmentParams: flat for both directions, or one set per direction.
func (v *validator) impairment(path string, flat model.NetemParams, upload, download *model.NetemParams, tunnel bool) {
	perDirection := upload != nil || download != nil
	if perDirection && len(netemSet(flat)) > 0 {
		v.add(path, CodeMixedDirections, "flat parameters and upload/download must not be mixed: give each direction its complete parameter set")
	}
	if !perDirection {
		v.netem(path, flat, tunnel)
		return
	}
	if upload != nil {
		v.netem(path+"/upload", *upload, tunnel)
	}
	if download != nil {
		v.netem(path+"/download", *download, tunnel)
	}
}

// hasEffect reports whether the parameters change anything.
func hasEffect(flat model.NetemParams, upload, download *model.NetemParams) bool {
	if len(netemSet(flat)) > 0 {
		return true
	}
	return (upload != nil && len(netemSet(*upload)) > 0) || (download != nil && len(netemSet(*download)) > 0)
}

// ---- fault bodies -----------------------------------------------------------------------

func (v *validator) mtu(path string, m model.MtuParams) {
	if m.Size < 552 || m.Size > 1500 {
		v.add(path+"/size", CodeInvalidPrefixLength, "the size must be between 552 and 1500 bytes")
	}
}

func (v *validator) tunnelRef(path string, t *model.TunnelRef) {
	if t == nil {
		v.add(path, CodeMissingField, "a tunnel fault names the WireGuard client or link whose tunnel it impairs")
	}
}

// faultBody validates a fault of family impairment, mtu or tunnel as used by configured
// faults, overlays and scenario steps. `needSource` is true for configured faults, whose source
// is validated by the caller.
func (v *validator) faultBody(path string, f model.FaultBody) {
	family := "impairment"
	if f.Family != nil {
		family = string(*f.Family)
	}
	flat := convert[model.NetemParams](f)
	match := convert[model.TrafficMatch](f)
	hasNetem := hasEffect(flat, f.Upload, f.Download)
	hasMatch := match.Destination != nil || match.Protocol != nil || len(deref(match.Ports)) > 0 || len(deref(match.PortRanges)) > 0

	switch family {
	case "impairment":
		if f.Mtu != nil {
			v.add(path+"/mtu", CodeMixedFamily, "an impairment fault has no mtu: use family mtu")
		}
		if f.Tunnel != nil {
			v.add(path+"/tunnel", CodeMixedFamily, "an impairment fault has no tunnel: use family tunnel")
		}
		if !hasNetem {
			v.add(path, CodeEmptyFault, "the fault sets no parameter")
		}
		v.impairment(path, flat, f.Upload, f.Download, false)
		v.match(path, match)
	case "mtu":
		if f.Mtu == nil {
			v.add(path+"/mtu", CodeMissingField, "an mtu fault needs its mtu settings")
		} else {
			v.mtu(path+"/mtu", *f.Mtu)
		}
		if hasNetem {
			v.add(path, CodeMixedFamily, "an mtu fault has no impairment parameters: use a separate impairment fault")
		}
		if f.Tunnel != nil {
			v.add(path+"/tunnel", CodeMixedFamily, "an mtu fault has no tunnel")
		}
		v.match(path, match)
	case "tunnel":
		v.tunnelRef(path+"/tunnel", f.Tunnel)
		if f.Mtu != nil {
			v.add(path+"/mtu", CodeMixedFamily, "a tunnel fault has no mtu")
		}
		if hasMatch {
			v.add(path, CodeUnexpectedField, "a tunnel fault impairs the whole tunnel: no destination, protocol or ports")
		}
		if !hasNetem {
			v.add(path, CodeEmptyFault, "the fault sets no parameter")
		}
		v.impairment(path, flat, f.Upload, f.Download, true)
	}
}

// faults validates the configured faults.
func (v *validator) faults() {
	for _, id := range sortedKeys(deref(v.cfg.Faults)) {
		f := deref(v.cfg.Faults)[id]
		path := schema.Pointer("/faults", id)
		v.faultBody(path, convert[model.FaultBody](f))
		family := "impairment"
		if f.Family != nil {
			family = string(*f.Family)
		}
		switch {
		case family == "tunnel" && f.Source != nil:
			v.add(path+"/source", CodeUnexpectedField, "a tunnel fault names its tunnel; it has no source")
		case family != "tunnel" && f.Source == nil:
			v.add(path+"/source", CodeMissingField, "a fault needs the source it applies to")
		case f.Source != nil:
			v.scope(path+"/source", f.Source, scopeOpts{})
		}
	}
}

// ---- DNS, TLS, DHCP, WireGuard actions --------------------------------------------------

func (v *validator) dnsFault(path string, d model.DnsFault) {
	action := string(d.Action)
	need := func(field string, present bool, onlyFor string) {
		switch {
		case action == onlyFor && !present:
			v.add(path+"/"+field, CodeInvalidDNSFault, "the action %s needs %s", action, field)
		case action != onlyFor && present:
			v.add(path+"/"+field, CodeInvalidDNSFault, "%s is only used by the action %s", field, onlyFor)
		}
	}
	need("delay", d.Delay != nil, "delay")
	need("answers", len(deref(d.Answers)) > 0, "wrong_answer")
	need("ttl", d.Ttl != nil, "short_ttl")
	if d.Delay != nil {
		if got, ok := parseDuration(*d.Delay); !ok || got <= 0 {
			v.add(path+"/delay", CodeInvalidDuration, "the delay must be longer than zero")
		}
	}
	if d.Ttl != nil {
		if got, ok := parseDuration(*d.Ttl); !ok || got < time.Second {
			v.add(path+"/ttl", CodeInvalidDuration, "the TTL must be at least 1s")
		}
	}
	for i, a := range deref(d.Answers) {
		if ip, ok := parseAddr(a); !ok || ip.IsUnspecified() {
			v.add(schema.Pointer(path+"/answers", itoa(i)), CodeInvalidAddress, "must be an IPv4 address")
		}
	}
	seen := map[string]bool{}
	for i, n := range deref(d.Names) {
		if seen[lower(n)] {
			v.add(schema.Pointer(path+"/names", itoa(i)), CodeDuplicateName, "%q is listed twice", n)
		}
		seen[lower(n)] = true
	}
}

func (v *validator) tlsCase(path string, t model.TlsCase) {
	match := convert[model.TrafficMatch](t)
	// TLS runs over TCP: an omitted protocol means tcp, so ports are fine without one
	checked := match
	if p := protocolOf(match); p == "any" {
		tcp := model.Protocol("tcp")
		checked.Protocol = &tcp
	}
	v.match(path, checked)
	if p := protocolOf(match); p != "tcp" && p != "any" {
		v.add(path+"/protocol", CodeInvalidTLSCase, "TLS runs over TCP: the protocol must be tcp or omitted")
	}
	intercept := t.Case == "intercept"
	if t.Intercept != nil && !intercept {
		v.add(path+"/intercept", CodeInvalidTLSCase, "interception settings belong to the case intercept")
	}
	if t.Intercept != nil {
		for i, r := range deref(t.Intercept.HttpRules) {
			rp := schema.Pointer(path+"/intercept/http_rules", itoa(i))
			need := func(field string, present bool, action string) {
				if string(r.Action) == action && !present {
					v.add(rp+"/"+field, CodeInvalidTLSCase, "the action %s needs %s", action, field)
				}
				if string(r.Action) != action && present {
					v.add(rp+"/"+field, CodeInvalidTLSCase, "%s is only used by the action %s", field, action)
				}
			}
			need("status", r.Status != nil, "status")
			need("delay", r.Delay != nil, "delay")
			need("rate", r.Rate != nil, "throttle")
			need("modify", r.Modify != nil, "modify")
		}
	}
}

func (v *validator) dhcpAction(path string, d model.DhcpAction) {
	action := string(d.Action)
	if (action == "short_lease") != (d.LeaseTime != nil) {
		v.add(path+"/lease_time", CodeInvalidDHCPAction, "lease_time is needed by, and only used by, the action short_lease")
	}
	if (action == "set_options") != (d.Options != nil) {
		v.add(path+"/options", CodeInvalidDHCPAction, "options are needed by, and only used by, the action set_options")
	}
	if d.LeaseTime != nil {
		if got, ok := parseDuration(*d.LeaseTime); !ok || got < time.Second {
			v.add(path+"/lease_time", CodeInvalidDuration, "the lease time must be at least 1s")
		}
	}
	v.dhcpOptions(path+"/options", d.Options)
}

func (v *validator) wireguardAction(path string, a model.WireGuardAction) {
	if (a.Client == nil) == (a.Link == nil) {
		v.add(path, CodeInvalidOverlay, "name exactly one of client and link")
	}
}

// ---- profiles ---------------------------------------------------------------------------

func (v *validator) profiles() {
	for _, id := range sortedKeys(deref(v.cfg.Profiles)) {
		p := deref(v.cfg.Profiles)[id]
		path := schema.Pointer("/profiles", id) + "/parts"
		v.profileParts(path, p.Parts)
	}
}

func (v *validator) profileParts(path string, parts model.ProfileParts) {
	if parts.Impairment != nil {
		imp := parts.Impairment
		v.impairment(path+"/impairment", convert[model.NetemParams](imp), imp.Upload, imp.Download, false)
	}
	if parts.Mtu != nil {
		v.mtu(path+"/mtu", *parts.Mtu)
	}
	if parts.Dns != nil {
		v.dnsFault(path+"/dns", *parts.Dns)
	}
	if parts.Tls != nil {
		v.tlsCase(path+"/tls", *parts.Tls)
	}
}
