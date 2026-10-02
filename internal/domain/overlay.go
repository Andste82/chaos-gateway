package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// overlayKinds lists the kinds of overlay in the order the spec names them.
var overlayKinds = []string{"profile", "fault", "rule", "dns", "tls", "dhcp", "wireguard"}

// OverlayKindOf returns the kind of an overlay request: the one of profile, fault, rule, dns,
// tls, dhcp and wireguard that it sets. It is an error when there is not exactly one.
func OverlayKindOf(req *model.OverlayRequest) (string, error) {
	var set []string
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("profile", req.Profile != nil)
	add("fault", req.Fault != nil)
	add("rule", req.Rule != nil)
	add("dns", req.Dns != nil)
	add("tls", req.Tls != nil)
	add("dhcp", req.Dhcp != nil)
	add("wireguard", req.Wireguard != nil)
	if len(set) != 1 {
		return "", fmt.Errorf("an overlay has exactly one of %s (found %d)", strings.Join(overlayKinds, ", "), len(set))
	}
	return set[0], nil
}

// isTunnelFault reports whether a fault body is of family tunnel.
func isTunnelFault(f *model.FaultBody) bool {
	return f != nil && f.Family != nil && *f.Family == "tunnel"
}

// ValidateOverlay checks an overlay request against a configuration and returns it with every
// reference resolved to a UUID. The request is not changed. The errors are relative to the
// request body, like the JSON pointers of a `validation_failed` problem.
//
// Rules: exactly one kind; a target for every kind except tunnel faults and WireGuard actions,
// which have none; DHCP actions target a device or a network; the parameters of the kind are
// consistent (the same rules as in the configuration); ttl and lease are positive.
func ValidateOverlay(cfg *model.Configuration, req *model.OverlayRequest, opts ...Option) (*model.OverlayRequest, []model.ValidationError) {
	out := clone(*req)
	work := clone(*cfg)
	idx, _ := newIndex(&work, collectOptions(opts))
	var errs []model.ValidationError
	visitOverlayRequest(&out, func(path string, kind Kind, ref *string) {
		if id, ok := idx.Resolve(kind, *ref); ok {
			*ref = id
			return
		}
		errs = append(errs, unknownRef(path, kind, *ref))
	})
	v := &validator{cfg: &work, idx: idx}

	kind, err := OverlayKindOf(&out)
	if err != nil {
		return &out, sortErrors(append(errs, model.ValidationError{Path: "", Code: CodeInvalidOverlay, Message: err.Error()}))
	}
	targetless := kind == "wireguard" || (kind == "fault" && isTunnelFault(out.Fault))
	switch {
	case targetless && out.Target != nil:
		v.add("/target", CodeUnexpectedField, "a %s has no target: it names the tunnel it affects", describeTargetless(kind))
	case !targetless && out.Target == nil:
		v.add("/target", CodeMissingField, "an overlay of kind %s needs a target", kind)
	case out.Target != nil:
		v.scope("/target", out.Target, scopeOpts{})
	}

	switch kind {
	case "fault":
		v.faultBody("/fault", *out.Fault)
	case "rule":
		v.accessRuleBody("/rule", *out.Rule)
	case "dns":
		v.dnsFault("/dns", *out.Dns)
	case "tls":
		v.tlsCase("/tls", *out.Tls)
	case "dhcp":
		v.dhcpAction("/dhcp", *out.Dhcp)
		if out.Target != nil {
			if k := scopeKind(out.Target); k != "device" && k != "network" {
				v.add("/target", CodeTargetKind, "a DHCP action targets a device or a network, not a %s", k)
			}
		}
	case "wireguard":
		v.wireguardAction("/wireguard", *out.Wireguard)
	}
	for _, p := range []struct {
		path string
		d    *model.Duration
	}{{"/ttl", out.Ttl}, {"/lease", out.Lease}} {
		if p.d == nil {
			continue
		}
		if d, ok := parseDuration(*p.d); !ok || d < time.Second {
			v.add(p.path, CodeInvalidDuration, "must be at least 1s")
		}
	}
	return &out, sortErrors(append(errs, v.errs...))
}

func describeTargetless(kind string) string {
	if kind == "wireguard" {
		return "WireGuard action"
	}
	return "tunnel fault"
}

// NewOverlay builds the overlay that a request creates: it gets its kind, its owner and its
// time stamps. The request must have passed ValidateOverlay. Writing an overlay that replaces
// another one keeps the old id and CreatedAt; that is the store's business (M8a).
func NewOverlay(req model.OverlayRequest, owner model.Owner, id uuid.UUID, now time.Time) (model.Overlay, error) {
	kind, err := OverlayKindOf(&req)
	if err != nil {
		return model.Overlay{}, err
	}
	if t := owner.Type; t != "user" && t != "token" && t != "run" {
		return model.Overlay{}, fmt.Errorf("an overlay is owned by a user, a token or a run, not %q", t)
	}
	return model.Overlay{
		Id: id, Kind: model.OverlayKind(kind), Owner: owner,
		Target: req.Target, Ttl: req.Ttl, Lease: req.Lease,
		Profile: req.Profile, Fault: req.Fault, Rule: req.Rule, Dns: req.Dns, Tls: req.Tls,
		Dhcp: req.Dhcp, Wireguard: req.Wireguard,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ---- overlay keys -----------------------------------------------------------------------

// scopeKey is a canonical string for a scope.
func scopeKey(s *model.Scope) string {
	if s == nil {
		return "-"
	}
	switch {
	case s.Device != nil:
		return "device:" + lower(*s.Device)
	case s.Group != nil:
		return "group:" + lower(*s.Group)
	case s.Network != nil:
		return "network:" + lower(*s.Network)
	case s.RemoteNetwork != nil:
		r := s.RemoteNetwork
		switch {
		case r.Client != nil:
			return "remote:client:" + lower(*r.Client)
		case r.Link != nil:
			return "remote:link:" + lower(*r.Link)
		case r.Cidr != nil:
			return "remote:cidr:" + *r.Cidr
		}
	case s.Global != nil:
		return "global"
	}
	return "?"
}

func destinationKey(d *model.Destination) string {
	if d == nil {
		return "-"
	}
	switch {
	case d.Uplink != nil:
		return "uplink"
	case d.Network != nil:
		return "network:" + lower(*d.Network)
	case d.Cidr != nil:
		// an address and its /32 are the same destination
		return "cidr:" + strings.TrimSuffix(*d.Cidr, "/32")
	case d.Hostname != nil:
		return "host:" + lower(*d.Hostname)
	}
	return "?"
}

// matchKey is a canonical string for a traffic selector: equal selectors give equal keys,
// whatever order their ports are listed in.
func matchKey(m model.TrafficMatch) string {
	ports := append([]int(nil), deref(m.Ports)...)
	sort.Ints(ports)
	var uniq []string
	for i, p := range ports {
		if i == 0 || p != ports[i-1] {
			uniq = append(uniq, fmt.Sprint(p))
		}
	}
	ranges := make([]string, 0)
	for _, r := range deref(m.PortRanges) {
		ranges = append(ranges, fmt.Sprintf("%d-%d", r.From, r.To))
	}
	sort.Strings(ranges)
	return fmt.Sprintf("dst=%s;proto=%s;ports=%s;ranges=%s", destinationKey(m.Destination), protocolOf(m),
		strings.Join(uniq, ","), strings.Join(ranges, ","))
}

// sortedLower returns the set of names, lower-cased, sorted and without duplicates.
func sortedLower(names []string) string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if l := lower(n); !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// OverlayKey returns the key of an overlay request: owner, kind, target and the selector part
// of its kind (plan §2.1.1, OverlayRequest in the spec). Writing an overlay with an existing key
// replaces it. References must be resolved to UUIDs (ValidateOverlay does that).
//
//	fault      family + traffic selector (impairment, mtu) or the tunnel (tunnel)
//	profile    none: one profile activation per owner and target
//	rule       traffic selector
//	dns        the set of names
//	tls        traffic selector + SNI
//	dhcp       the action
//	wireguard  client or link + action
func OverlayKey(owner model.Owner, req *model.OverlayRequest) (string, error) {
	kind, err := OverlayKindOf(req)
	if err != nil {
		return "", err
	}
	var selector string
	switch kind {
	case "fault":
		f := req.Fault
		family := "impairment"
		if f.Family != nil {
			family = string(*f.Family)
		}
		if family == "tunnel" {
			selector = "tunnel:" + tunnelKey(f.Tunnel)
		} else {
			selector = family + ";" + matchKey(convert[model.TrafficMatch](f))
		}
	case "profile":
		selector = "-"
	case "rule":
		selector = matchKey(convert[model.TrafficMatch](req.Rule))
	case "dns":
		selector = sortedLower(deref(req.Dns.Names))
	case "tls":
		selector = matchKey(convert[model.TrafficMatch](req.Tls)) + ";sni=" + sortedLower(deref(req.Tls.Sni))
	case "dhcp":
		selector = string(req.Dhcp.Action)
	case "wireguard":
		selector = tunnelKey(&model.TunnelRef{Client: req.Wireguard.Client, Link: req.Wireguard.Link}) + ";" + string(req.Wireguard.Action)
	}
	return strings.Join([]string{string(owner.Type), owner.Id, kind, scopeKey(req.Target), selector}, "|"), nil
}

// ShortKey shortens an overlay key to a fixed-length identifier, for logs and maps.
func ShortKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func tunnelKey(t *model.TunnelRef) string {
	switch {
	case t == nil:
		return "?"
	case t.Client != nil:
		return "client:" + lower(*t.Client)
	case t.Link != nil:
		return "link:" + lower(*t.Link)
	}
	return "?"
}
