package domain

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// Error codes of the semantic validation: the rules that the schema cannot express (plan §2.1,
// §2.4, §2.10 and the conventions of api/openapi.yaml). The codes are stable; the API reports
// them in `errors[].code` of a `validation_failed` problem.
const (
	CodeHostBitsSet            = "host_bits_set"
	CodeOverlappingSubnet      = "overlapping_subnet"
	CodeReservedRange          = "reserved_range"
	CodeInvalidPrefixLength    = "invalid_prefix_length"
	CodeInvalidAddress         = "invalid_address"
	CodeOutsideSubnet          = "outside_subnet"
	CodeDuplicateAddress       = "duplicate_address"
	CodeDuplicateInterface     = "duplicate_interface"
	CodeDuplicatePort          = "duplicate_port"
	CodeInvalidEndpoint        = "invalid_endpoint"
	CodeWrongKind              = "wrong_kind"
	CodePoolOrder              = "pool_order"
	CodePoolOverlap            = "pool_overlap"
	CodeInvalidDuration        = "invalid_duration"
	CodeNoIdentifier           = "no_identifier"
	CodeDuplicateIdentifier    = "duplicate_identifier"
	CodeInvalidMAC             = "invalid_mac"
	CodeFixedIPRequires        = "fixed_ip_requires"
	CodeDuplicateMember        = "duplicate_member"
	CodeProbeNetwork           = "probe_network_not_lan"
	CodeMatrixSelf             = "matrix_self_entry"
	CodeMatrixDuplicate        = "duplicate_matrix_entry"
	CodeManagementOverlap      = "management_overlaps_network"
	CodeRuleOrder              = "rule_order"
	CodePortsRequireProtocol   = "ports_require_protocol"
	CodeInvalidPortRange       = "invalid_port_range"
	CodeResetRequiresTCP       = "reset_requires_tcp"
	CodeCutRequiresTCP         = "cut_existing_requires_tcp"
	CodeDuplicatePublicKey     = "duplicate_public_key"
	CodeKeySettings            = "invalid_key_settings"
	CodeMissingField           = "missing_field"
	CodeUnexpectedField        = "unexpected_field"
	CodeProtocolSettings       = "invalid_protocol_settings"
	CodeTimers                 = "invalid_timers"
	CodeInvalidTable           = "invalid_table"
	CodeRemoteNetwork          = "unknown_remote_network"
	CodeStepOrder              = "invalid_step_reference"
	CodeTargetWidened          = "target_widened"
	CodeTargetKind             = "invalid_target"
	CodeTunnelOutsideTarget    = "tunnel_outside_target"
	CodeReservedID             = "reserved_id"
	CodeStepKind               = "invalid_step"
	CodeCheckWindow            = "invalid_window"
	CodeDuplicateStep          = "duplicate_step_id"
	CodeJitterExceedsLatency   = "jitter_exceeds_latency"
	CodeReorderNeedsLatency    = "reorder_requires_latency"
	CodeExclusive              = "mutually_exclusive"
	CodeMixedDirections        = "mixed_directions"
	CodeMixedFamily            = "mixed_family"
	CodeEmptyFault             = "empty_fault"
	CodeInvalidMTU             = "invalid_mtu"
	CodeCutWithAllow           = "cut_existing_with_allow"
	CodeTunnelParameter        = "tunnel_parameter"
	CodeInvalidDNSFault        = "invalid_dns_fault"
	CodeInvalidTLSCase         = "invalid_tls_case"
	CodeInvalidDHCPAction      = "invalid_dhcp_action"
	CodeInvalidOverlay         = "invalid_overlay"
	CodeInvalidFlapping        = "invalid_flapping"
	CodeInvalidRate            = "invalid_rate"
	CodeInvalidName            = "invalid_name"
	CodeDuplicateRoutingKind   = "duplicate_protocol"
	CodeUnknownRoutingSettings = "missing_routing_settings"
	CodeReservedOption         = "reserved_option"
)

// reservedDHCPOptions are the DHCPv4 option codes Kea or the compiler already manage (router,
// lease time, DNS/NTP servers, …); a custom option with one of these codes would either be
// rejected by Kea's config-set or silently overridden, breaking every scope in the same network.
var reservedDHCPOptions = map[int]bool{
	1: true, 3: true, 6: true, 12: true, 15: true, 28: true, 42: true, 50: true, 51: true,
	53: true, 54: true, 55: true, 58: true, 59: true, 61: true, 82: true, 255: true,
}

// reservedPrefixes may not be used for test, WireGuard or client networks: loopback, multicast,
// "this network", link-local (the gateway's service namespace uses 169.254.100.0/30, plan §3.3)
// and the reserved class E.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// Validate checks everything about a configuration that the schema cannot: references resolve,
// names are unique in their namespace, subnets do not overlap, parameters are consistent
// (jitter ≤ latency, "exactly one of" bodies, …) and scenarios are well-formed. References may
// be names or UUIDs. It returns every problem found, sorted by path.
//
// Syntax and the schema are checked by DecodeConfiguration; Validate expects a document that
// decoded into the model.
func Validate(cfg *model.Configuration, opts ...Option) []model.ValidationError {
	o := collectOptions(opts)
	work := clone(*cfg)
	idx, errs := newIndex(&work, o)
	errs = append(errs, resolveRefs(&work, idx)...)
	// resolveRefs rewrote the references inside the configuration; the index must see them
	idx, _ = newIndex(&work, o)

	v := &validator{cfg: &work, idx: idx}
	v.run()
	errs = append(errs, v.errs...)
	return sortErrors(errs)
}

func sortErrors(errs []model.ValidationError) []model.ValidationError {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Path != errs[j].Path {
			return errs[i].Path < errs[j].Path
		}
		return errs[i].Code < errs[j].Code
	})
	// the same problem found twice (e.g. through two paths of the checks) is reported once
	out := errs[:0]
	for i, e := range errs {
		if i > 0 && e == errs[i-1] {
			continue
		}
		out = append(out, e)
	}
	return out
}

type validator struct {
	cfg  *model.Configuration
	idx  *Index
	errs []model.ValidationError
	// prefixes of all networks, client networks and routes, for the overlap check
	registry []prefixOwner
}

type prefixOwner struct {
	prefix netip.Prefix
	path   string
	what   string
}

func (v *validator) add(path, code, format string, args ...any) {
	v.errs = append(v.errs, model.ValidationError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) run() {
	v.settings()
	v.uplinkAndManagement()
	v.networks()
	v.devices()
	v.groups()
	v.probes()
	v.accessMatrix()
	v.routing()
	v.accessRules()
	v.faults()
	v.profiles()
	v.scenarios()
}

// ---- small parsing helpers ---------------------------------------------------------------

func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	return a, err == nil && a.Is4()
}

func parsePrefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	return p, err == nil && p.Addr().Is4()
}

func parseDuration(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(s)
	return d, err == nil
}

// broadcast returns the last address of a prefix.
func broadcast(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	n := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	n |= (1 << host) - 1
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// usableHost reports whether a is a host address of p: inside it, and neither the network nor
// the broadcast address (a /31 has no such addresses, a /32 neither).
func usableHost(p netip.Prefix, a netip.Addr) bool {
	if !p.Contains(a) {
		return false
	}
	if p.Bits() >= 31 {
		return true
	}
	return a != p.Masked().Addr() && a != broadcast(p)
}

func overlaps(a, b netip.Prefix) bool { return a.Overlaps(b) }

// cidrOK checks a network prefix: well-formed, host bits zero. It returns the prefix.
func (v *validator) cidrOK(path, s string) (netip.Prefix, bool) {
	p, ok := parsePrefix(s)
	if !ok {
		v.add(path, CodeInvalidAddress, "%q is not an IPv4 prefix", s)
		return p, false
	}
	if p != p.Masked() {
		v.add(path, CodeHostBitsSet, "%s has host bits set; the network prefix is %s", s, p.Masked())
		return p, false
	}
	return p, true
}

// reserved reports whether p touches a range that networks must not use.
func reserved(p netip.Prefix) (netip.Prefix, bool) {
	for _, r := range reservedPrefixes {
		if overlaps(p, r) {
			return r, true
		}
	}
	return netip.Prefix{}, false
}

// register adds a prefix to the overlap registry and reports an overlap with an earlier one.
func (v *validator) register(path string, p netip.Prefix, what string) {
	if r, bad := reserved(p); bad {
		v.add(path, CodeReservedRange, "%s %s overlaps the reserved range %s", what, p, r)
	}
	for _, o := range v.registry {
		if overlaps(p, o.prefix) {
			v.add(path, CodeOverlappingSubnet, "%s %s overlaps %s %s at %s", what, p, o.what, o.prefix, o.path)
		}
	}
	v.registry = append(v.registry, prefixOwner{prefix: p, path: path, what: what})
}

func lower(s string) string { return strings.ToLower(s) }
