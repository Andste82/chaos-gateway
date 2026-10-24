package domain

import (
	"sort"
	"strings"
)

// This file is the vocabulary of hostname selectors (plan §2.4, §2.6, M20): how a pattern is
// written canonically, which names it matches, and the order in which the candidates that name a
// hostname are tried.

// HostnamePattern is the canonical form of a hostname selector: lower case, without a trailing dot.
// The DNS-derived address sets, the configuration of the DNS proxy and the matching all use it, so
// "MQTT.Example.com." and "mqtt.example.com" are one selector.
func HostnamePattern(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// HostnameMatches reports whether a DNS name matches a hostname selector: an exact name, or
// `*.suffix`, which matches the subdomains of the suffix and not the suffix itself (the schema's
// Hostname). Case and a trailing dot do not matter.
func HostnameMatches(pattern, name string) bool {
	pattern, name = HostnamePattern(pattern), HostnamePattern(name)
	if name == "" {
		return false
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(name, "."+suffix)
	}
	return pattern == name
}

// Outranks reports whether candidate a beats candidate b where both apply to the same traffic
// (plan §2.4): overlays before configuration, then the more specific level; on the very same scope a
// fault beats a part of a profile; and at the same level the newer one wins (D26). It is the
// pairwise form of the order winnerOf resolves a whole list with; for two candidates of the
// same layer, level and scope it is the same, and across scopes of one level only the newest
// champion of each scope competes in winnerOf, which a pairwise order cannot say (see RankOrder).
func Outranks(a, b Candidate) bool {
	if a.Layer != b.Layer {
		return a.Layer == LayerOverlay
	}
	if a.Level != b.Level {
		return a.Level < b.Level
	}
	if sameScope(a, b) && a.IsProfilePart() != b.IsProfilePart() {
		return !a.IsProfilePart()
	}
	return newer(a, b)
}

// RankOrder sorts candidates that apply to the same source so that the first of them that matches a
// piece of traffic is the one the precedence rules of §2.4 pick: the strongest first. It is for the
// candidates whose match cannot be resolved at compile time: a hostname selector (the addresses
// of a name are known at run time only) and a DNS fault (it matches a name asked at run time).
// Two candidates that never meet on one scope in a way the pairwise order gets wrong (a fault and a profile
// part of one scope that sit between the champions of two other scopes of one level) are ordered as
// Outranks says, which is the order winnerOf gives wherever no such triple exists.
func RankOrder(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool { return Outranks(cs[i], cs[j]) })
}
