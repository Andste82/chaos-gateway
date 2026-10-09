package compiler

import (
	"fmt"
	"math"
	"net/netip"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// This file is the classification mechanism of plan §3.3 (milestones M7 and M8a): on prerouting,
// for test, WireGuard and remote-network traffic only, write the winning fault's id and the
// packet's direction into reserved mark bits, through a lookup chain of nftables maps keyed on the
// conntrack original tuple. The maps' elements are the resolved winners (faults.go); the chain
// `mark_<id>` an element goes to writes the id and counts the packet.
//
// First match wins by `goto`, not `jump`. After a jump the kernel continues with the next rule of the
// calling chain (what follows the lookup in the same rule is never run), so a `return` after the
// lookup does not stop the chain, and the next level, if it holds an entry for the same traffic,
// overwrites the id. M7 had that latent defect: its levels never held two entries for the same
// traffic until real faults filled them. A goto leaves the base chain for good: the chain it names
// runs, and when it ends the base chain's evaluation ends with it, accept.

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

// MarkDupBit is the mark bit that says "duplicate this packet": written by the mark chain of a
// fault that duplicates, with the fault's probability, and consumed (cleared) by the egress hook of
// the interface the packet leaves through. Bits 21 to 23 were reserved for further routing marks;
// this is the first of them in use (plan §3.3).
const MarkDupBit = uint32(1) << 21

// DupResolution is the modulus of the random number a duplicating fault compares with: a
// probability resolves to one part in 10^9, which is 10^-7 percent.
const DupResolution = 1_000_000_000

// dupThreshold is the number below which the random number of a packet makes it a duplicate.
func dupThreshold(percent float64) int64 {
	if percent <= 0 {
		return 0
	}
	return min(int64(math.Round(percent*DupResolution/100)), DupResolution)
}

// ClassifyChain is the prerouting chain that writes the classification mark.
const ClassifyChain = "classify"

// ClassifyPriority runs after the kernel's own conntrack hook (NF_IP_PRI_CONNTRACK, -200) so `ct`
// fields are populated, and before the service redirect's DNAT (service.go's "prerouting", -100).
const ClassifyPriority = -150

// markChainPrefix names the per-id chain a classification map's element goes to.
const markChainPrefix = executor.MarkChainPrefix

// MarkChainName is the chain a classification map element for this id goes to; it writes the id
// into bits 4-15 and returns. The chain must exist before an element names it (plan §3.3): the
// compiler creates one for every id a resolved fault holds, and mark_0 where a fault that impairs
// nothing shadows another.
func MarkChainName(id int) string { return fmt.Sprintf("%s%d", markChainPrefix, id) }

// classifyLevels is the lookup chain's granularity, most specific first (plan §3.3, levels 1-4):
// device+destination+port, device+destination, device+port, device. The maps hold the resolved
// winner per key, and the sources' groups, networks and global scopes are folded into the sources'
// own entries (domain.World.Table, docs/open-items.md P2-M8a-01), so these four are all the chain
// needs. The maps are interval maps: an element is a range of source addresses (a device's
// addresses, or the addresses of a stretch no device owns), a range of destinations and a range of
// ports, disjoint from every other element of its map (plan §3.2, "overlapping selectors").
var classifyLevels = []struct {
	level int
	field string
	key   []string
}{
	{1, "devdestport", []string{"ipv4_addr", "ipv4_addr", "inet_proto", "inet_service"}},
	{2, "devdest", []string{"ipv4_addr", "ipv4_addr"}},
	{3, "devport", []string{"ipv4_addr", "inet_proto", "inet_service"}},
	{4, "dev", []string{"ipv4_addr"}},
}

// classifyNetsName is the name of the set of test, WireGuard and remote-network prefixes: the
// classification chain and the global scope of the access rules are guarded by it.
func classifyNetsName() string { return hashName("classify_nets", "ipv4_addr", []string{"interval"}) }

// compileClassify builds the classification mechanism: the guard set of test/WireGuard/remote
// prefixes, the lookup chain's maps with the elements compileFaults resolved, the per-id chains
// and counters, and the "classify" chain itself.
func (t *Target) compileClassify() {
	netsSet := SetDef{Type: "ipv4_addr", Flags: []string{"interval"}, Elements: t.classifyNets()}
	netsSet.Name = classifyNetsName()
	t.Nft.Sets = append(t.Nft.Sets, netsSet)
	t.ClassifyNets = netsSet.Name

	t.ClassifyMaps = map[string]string{}
	fb := t.faultBuild
	if fb == nil {
		fb = &faultBuild{}
	}
	for i, lv := range classifyLevels {
		md := MapDef{KeyType: lv.key, ValueType: "verdict", Flags: []string{"interval"}}
		if i < len(fb.maps) {
			md.Elements = fb.maps[i].Elements
		}
		md.Name = hashMapName("cls_"+lv.field, md.KeyType, md.ValueType, md.Flags)
		t.Nft.Maps = append(t.Nft.Maps, md)
		t.ClassifyMaps[lv.field] = md.Name
	}
	t.Nft.Chains = append(t.Nft.Chains, fb.chains...)
	t.Nft.Counters = append(t.Nft.Counters, fb.counters...)
	sort.Strings(t.Nft.Counters)

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
	// the MTU family resolves on its own (plan §2.4) and comes first: its lookup chain is jumped to and
	// comes back, the impairment lookup below ends the chain with its goto
	if r, ok := t.pmtuClassify(); ok {
		c.Rules = append(c.Rules, r)
	}
	c.Rules = append(c.Rules,
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr"), meta("l4proto"), ctOriginal("proto-dst")), t.ClassifyMaps["devdestport"])),
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr")), t.ClassifyMaps["devdest"])),
		newRule(vmap(concat(ctOriginalIP("saddr"), meta("l4proto"), ctOriginal("proto-dst")), t.ClassifyMaps["devport"])),
		newRule(vmap(ctOriginalIP("saddr"), t.ClassifyMaps["dev"])),
	)
	t.Nft.Chains = append(t.Nft.Chains, c)
}

// markChain is the per-id chain a classification map element goes to: it writes the id into bits
// 4-15, keeping everything else (MarkKeepOnIDWrite), counts the packet in the counter of its
// direction and ends (the classification of the packet is done). Id 0 clears the id: it is the entry of a
// fault that impairs nothing, which still shadows the less specific faults below it.
//
// A fault that duplicates packets also decides here, per packet and direction, whether this packet is
// duplicated: with the probability of the fault it sets MarkDupBit, which the tc hook of the egress
// interface turns into a copy of the packet (DupFilter). The duplication is not netem's: the kernel
// refuses a duplicating netem on an interface that has any other netem (docs/open-items.md
// P2-M8b-01, P2-M10-01).
func markChain(id int, counterUp, counterDown string, dupUp, dupDown float64) Chain {
	rules := []Rule{newRule(markSet(bitOr(bitAnd(meta("mark"), int64(MarkKeepOnIDWrite)), lshift(id, MarkIDShift))))}
	for _, d := range []struct {
		dir string
		p   float64
	}{{"original", dupUp}, {"reply", dupDown}} {
		n := dupThreshold(d.p)
		switch {
		case n >= DupResolution:
			// always: the comparison would be against the modulus, which nft refuses (the value of
			// `numgen random mod N` is below N)
			rules = append(rules, newRule(match(ctKey("direction"), "==", d.dir),
				markSet(bitOr(meta("mark"), int64(MarkDupBit)))))
		case n > 0:
			rules = append(rules, newRule(match(ctKey("direction"), "==", d.dir),
				match(numgenRandom(DupResolution), "<", n),
				markSet(bitOr(meta("mark"), int64(MarkDupBit)))))
		}
	}
	if counterUp != "" {
		rules = append(rules,
			newRule(match(ctKey("direction"), "==", "original"), counter(counterUp)),
			newRule(match(ctKey("direction"), "==", "reply"), counter(counterDown)))
	}
	rules = append(rules, newRule(verdict("return")))
	return Chain{Name: MarkChainName(id), Rules: rules}
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
