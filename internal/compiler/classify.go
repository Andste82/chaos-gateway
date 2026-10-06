package compiler

import (
	"fmt"
	"net/netip"
	"sort"
)

// This file is the classification mechanism of plan §3.3 (milestone M7): on prerouting, for test,
// WireGuard and remote-network traffic only, write the winning fault's id and the packet's
// direction into reserved mark bits, through a lookup chain of nftables maps keyed on the
// conntrack original tuple. M7 itself resolves no real fault (that is M8a onward): the mechanism
// is proven with a per-id mark-writing chain that a test points a map element at (TestClassifyIDs),
// ready for M8a to drive with domain.Resolve's winners instead.

// Mark bit layout (plan §3.3): bits 4-15 hold the fault id (12 bits, up to 4095), bit 16 the
// direction (0 = original/upload, 1 = reply/download, from `ct direction`). Bits 17-19 (PMTU),
// 20 (service selection) and 21-23 (reserved) are untouched here; so are bits 0-3 and 24-31.
const (
	MarkIDShift      = 4
	MarkIDBits       = 12
	MarkIDMax        = 1<<MarkIDBits - 1 // 4095: the capacity limit of plan §3.3
	MarkDirectionBit = 16

	markIDMaskBits  = uint32(MarkIDMax) << MarkIDShift // 0x0000fff0: the id's own bits
	markDirMaskBits = uint32(1) << MarkDirectionBit    // 0x00010000: the direction bit

	// MarkKeepOnIDWrite is kept when the fault id is (re)written: every other bit survives,
	// including the direction bit. A mask that also cleared bit 16 (markKeepOnIDWriteBuggy,
	// 0xfffe000f) was spike S15's bug: both directions of a connection got the upload parameters,
	// because the direction a classification wrote just before was wiped out by the next chain
	// that classified the same packet. A golden test pins this exact value.
	MarkKeepOnIDWrite = ^markIDMaskBits // 0xffff000f
	// markKeepOnIDWriteBuggy is S15's wrong mask: kept only so the golden test can show the
	// difference is deliberate, not a typo the other way around.
	markKeepOnIDWriteBuggy = ^(markIDMaskBits | markDirMaskBits) // 0xfffe000f

	// MarkKeepOnDirectionWrite is kept when the direction bit is (re)written: only that one bit is
	// cleared first.
	MarkKeepOnDirectionWrite = ^markDirMaskBits // 0xfffeffff
)

// ClassifyChain is the prerouting chain that writes the classification mark.
const ClassifyChain = "classify"

// ClassifyPriority runs after the kernel's own conntrack hook (NF_IP_PRI_CONNTRACK, -200) so `ct`
// fields are populated, and before the service redirect's DNAT (service.go's "prerouting", -100).
const ClassifyPriority = -150

// markChainPrefix names the per-id chain a classification map's element jumps to.
const markChainPrefix = "mark_"

// MarkChainName is the chain a classification map element for this id jumps to; it writes the id
// into bits 4-15 and returns. The chain must exist before an element names it (plan §3.3): the
// compiler creates one for every id TestClassifyIDs or (from M8a) a resolved fault names.
func MarkChainName(id int) string { return fmt.Sprintf("%s%d", markChainPrefix, id) }

// classifyLevels is the lookup chain's granularity, most specific first (plan §3.3, levels 1-4):
// device+destination+port, device+destination, device+port, device. The domain model's groups and
// networks (levels 5-8) and the two source-less levels (any+destination/port, global, 9-10) are
// not implemented yet: the access-matrix/fault-resolution machinery to expand a group or a network
// into this kind of map does not exist before M8a/M9 (see docs/open-items.md P2-M7-01).
var classifyLevels = []struct {
	field string
	key   []string
}{
	{"devdestport", []string{"ipv4_addr", "ipv4_addr", "inet_proto", "inet_service"}},
	{"devdest", []string{"ipv4_addr", "ipv4_addr"}},
	{"devport", []string{"ipv4_addr", "inet_proto", "inet_service"}},
	{"dev", []string{"ipv4_addr"}},
}

// compileClassify builds the classification mechanism: the guard set of test/WireGuard/remote
// prefixes, the lookup chain's maps (empty: M7 resolves no fault) and the "classify" chain itself.
// TestClassifyIDs' per-id chains let a test exercise the mechanism end-to-end before M8a exists.
func (t *Target) compileClassify(testIDs []int) {
	netsSet := SetDef{Type: "ipv4_addr", Flags: []string{"interval"}, Elements: t.classifyNets()}
	netsSet.Name = hashName("classify_nets", netsSet.Type, netsSet.Flags)
	t.Nft.Sets = append(t.Nft.Sets, netsSet)
	t.ClassifyNets = netsSet.Name

	t.ClassifyMaps = map[string]string{}
	for _, lv := range classifyLevels {
		md := MapDef{KeyType: lv.key, ValueType: "verdict"}
		md.Name = hashMapName("cls_"+lv.field, md.KeyType, md.ValueType)
		t.Nft.Maps = append(t.Nft.Maps, md)
		t.ClassifyMaps[lv.field] = md.Name
	}

	ids := uniqueSortedIDs(testIDs)
	for _, id := range ids {
		t.Nft.Chains = append(t.Nft.Chains, markChain(id))
	}

	c := Chain{Name: ClassifyChain, Base: &BaseChain{Type: "filter", Hook: "prerouting", Prio: ClassifyPriority, Policy: "accept"}}
	c.Rules = append(c.Rules,
		// only test, WireGuard and remote-network traffic is classified; the gateway's own traffic
		// (management, updates, BIRD, the WireGuard underlay, unless a tunnel fault targets it in
		// M10) never has its mark read or written here (plan §3.3).
		newRule(
			match(ctOriginalIP("saddr"), "!=", setRef(netsSet.Name)),
			match(ctOriginalIP("daddr"), "!=", setRef(netsSet.Name)),
			verdict("return"),
		),
		// the direction bit is written once, before the lookup chain: every level below keeps it
		// (MarkKeepOnIDWrite), so it survives whichever level ends up matching. It takes two
		// mutually exclusive rules, not one `mark | ct direction << 16` expression: ct direction is
		// a 1-byte value and the kernel (checked on 6.8.0-142, the minimum supported kernel)
		// refuses to shift it inside a bitwise expression (EOPNOTSUPP), which fails the whole
		// atomic nft batch. Both rules keep every other mark bit.
		newRule(match(ctKey("direction"), "==", "reply"), markSet(bitOr(meta("mark"), int64(markDirMaskBits)))),
		newRule(match(ctKey("direction"), "==", "original"), markSet(bitAnd(meta("mark"), int64(MarkKeepOnDirectionWrite)))),
	)
	c.Rules = append(c.Rules,
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr"), meta("l4proto"), ctOriginal("proto-dst")), t.ClassifyMaps["devdestport"]), verdict("return")),
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr")), t.ClassifyMaps["devdest"]), verdict("return")),
		newRule(vmap(concat(ctOriginalIP("saddr"), meta("l4proto"), ctOriginal("proto-dst")), t.ClassifyMaps["devport"]), verdict("return")),
		newRule(vmap(ctOriginalIP("saddr"), t.ClassifyMaps["dev"]), verdict("return")),
	)
	t.Nft.Chains = append(t.Nft.Chains, c)
}

// markChain is the per-id chain a classification map element jumps to: it writes the id into bits
// 4-15, keeping everything else (MarkKeepOnIDWrite), and returns to the classify chain's "return".
func markChain(id int) Chain {
	return Chain{Name: MarkChainName(id), Rules: []Rule{
		newRule(markSet(bitOr(bitAnd(meta("mark"), int64(MarkKeepOnIDWrite)), lshift(id, MarkIDShift))), verdict("return")),
	}}
}

func uniqueSortedIDs(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, id := range in {
		if id < 0 || id > MarkIDMax || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// classifyNets lists the prefixes classification touches: test networks, WireGuard networks and
// the remote networks reached through a router, a WireGuard client or a link (plan §3.3's "test,
// WireGuard or remote networks"). The gateway's own interfaces (uplink, management) are never in
// it.
func (t *Target) classifyNets() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p netip.Prefix) {
		if !p.IsValid() {
			return
		}
		s := p.Masked().String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	addStr := func(s string) {
		if p, err := netip.ParsePrefix(s); err == nil {
			add(p)
		} else if a, err := netip.ParseAddr(s); err == nil {
			add(netip.PrefixFrom(a, a.BitLen()))
		}
	}
	for _, b := range t.Bridges {
		add(b.Address)
		for _, r := range b.Routes {
			addStr(r)
		}
	}
	for _, w := range t.WireGuard {
		add(w.Address)
		for _, p := range w.Peers {
			for _, r := range p.Routes {
				addStr(r)
			}
		}
	}
	sort.Strings(out)
	return out
}
