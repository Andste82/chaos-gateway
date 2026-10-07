package domain

import (
	"fmt"
	"math/bits"
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file turns the precedence rules of plan §2.4 into what the compiler writes into the
// classification maps of plan §3.3: for every source of traffic and every family that selects by
// destination, protocol and port (impairment, MTU), a Table of entries. The maps are first-match
// lookups on the conntrack original tuple at four levels (device + destination + port, device +
// destination, device + port, device), so they cannot hold raw faults: they hold the *resolved*
// result. Where a less specific overlay overrides a more specific configured fault (E1), the
// entry of the specific key carries the overlay's winner, so the first-match lookup never
// contradicts the precedence rules.
//
// How a table is built. The faults that apply to a source (device, group, network, any and
// global scopes are all folded into the source, so the maps need no group or network levels) name
// destinations and protocol/port selectors. Their boundaries cut the destination space (IPv4
// addresses) and the protocol/port space into elementary pieces; on a piece nothing changes, so
// the resolution of one representative traffic (Resolve's own rules, applied to a Query) is the
// resolution of the whole piece. The pieces are disjoint, so a lookup at one level has at most one
// matching entry and the nftables maps need no overlapping intervals. The entries are then chosen
// so that the first-match lookup gives the resolved winner everywhere, with as few entries as
// that takes:
//
//	level 4  the winner of traffic that no selector names
//	level 3  per protocol/port piece: its winner, if it differs from level 4
//	level 2  per destination piece: its winner, if it differs from level 4
//	level 1  per (destination piece, protocol/port piece): its winner, if it differs from what
//	         the levels below give there
//
// Every entry has a winner: the candidates that match a piece include those of its row and column,
// so a piece with no winner has none below it either. Traffic that no entry names has no fault.
//
// Not part of a table: hostname destinations. They are resolved through DNS-derived address sets
// that exist at run time only (M20); the candidates are listed in Table.Unresolved so the
// compiler can say what it left out.

// IPRange is an inclusive range of IPv4 addresses.
type IPRange struct{ First, Last netip.Addr }

// Contains reports whether the address lies in the range.
func (r IPRange) Contains(a netip.Addr) bool {
	return a.Is4() && r.First.Compare(a) <= 0 && a.Compare(r.Last) <= 0
}

func (r IPRange) String() string {
	if r.First == r.Last {
		return r.First.String()
	}
	return r.First.String() + "-" + r.Last.String()
}

// Prefixes returns the range as the shortest list of CIDR prefixes.
func (r IPRange) Prefixes() []netip.Prefix {
	return spansToPrefixes(span{addrNum(r.First), addrNum(r.Last)})
}

// PortSel selects one protocol and a range of ports: tcp and udp with ports From to To, or icmp
// (From and To are 0). Traffic of other protocols is never selected by a protocol/port piece.
type PortSel struct {
	Proto    string
	From, To int
}

// Contains reports whether traffic of a protocol and a destination port lies in the selector.
func (p PortSel) Contains(proto string, port int) bool {
	if p.Proto != proto {
		return false
	}
	return proto == "icmp" || (port >= p.From && port <= p.To)
}

func (p PortSel) String() string {
	switch {
	case p.Proto == "icmp":
		return "icmp"
	case p.From == p.To:
		return fmt.Sprintf("%s/%d", p.Proto, p.From)
	}
	return fmt.Sprintf("%s/%d-%d", p.Proto, p.From, p.To)
}

// Entry is one element of a Table.
type Entry struct {
	// Level is the lookup level: 1 destination and protocol/port, 2 destination, 3 protocol/port,
	// 4 neither (the source's default).
	Level int
	Dest  *IPRange // levels 1 and 2
	Port  *PortSel // levels 1 and 3
	// Winner is the fault that applies to traffic matching the entry.
	Winner *Candidate
}

func (e Entry) String() string {
	var parts []string
	if e.Dest != nil {
		parts = append(parts, e.Dest.String())
	}
	if e.Port != nil {
		parts = append(parts, e.Port.String())
	}
	key := "*"
	if len(parts) > 0 {
		key = strings.Join(parts, " ")
	}
	return fmt.Sprintf("L%d %s -> %s", e.Level, key, winnerName(e.Winner))
}

func winnerName(c *Candidate) string {
	if c == nil {
		return "none"
	}
	return string(c.Layer) + ":" + c.ID
}

// Source is a source of traffic the compiler classifies for: one device (with the addresses it
// has right now, or the address ranges that identify it) or a stretch of addresses that no device
// owns. The traffic is the initiator's (initiator semantics, plan §2.4).
type Source struct {
	// Subject is what the resolution is asked for.
	Subject Subject
	// Device is the UUID of the device; empty for traffic of no known device.
	Device string
	// Addrs are the device's current addresses.
	Addrs []netip.Addr
	// Ranges are the addresses of a stretch no device owns, or the ranges that identify a device.
	Ranges []IPRange
}

// Table is the classification of one source for one family: the result of every precedence
// question about that source's traffic, as lookup entries.
type Table struct {
	Source Source
	Family string
	// Entries are sorted by level, then by key.
	Entries []Entry
	// Unresolved are the candidates that name a hostname: their addresses are known at run time
	// only, so they are not in Entries.
	Unresolved []Candidate
}

// Signature is a string that is equal for tables with equal entries, whatever source they are
// for: the compiler shares the maps of sources whose tables are equal.
func (t Table) Signature() string {
	var b strings.Builder
	for _, e := range t.Entries {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Lookup does what the chain of lookup maps does for a packet of the source: the first level that
// has an entry for the traffic decides. It returns the winner (nil: no fault) and whether an entry
// was found at all.
func (t Table) Lookup(q Query) (*Candidate, bool) {
	for level := 1; level <= 4; level++ {
		for _, e := range t.Entries {
			if e.Level != level {
				continue
			}
			if e.Dest != nil && !e.Dest.Contains(q.DestIP) {
				continue
			}
			if e.Port != nil && !e.Port.Contains(q.Protocol, q.Port) {
				continue
			}
			return e.Winner, true
		}
	}
	return nil, false
}

// ---- address arithmetic ------------------------------------------------------------------

// span is an inclusive range of addresses as numbers.
type span struct{ lo, hi uint64 }

const addrSpace = uint64(1) << 32

func addrNum(a netip.Addr) uint64 {
	b := a.As4()
	return uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
}

func numAddr(n uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

func (s span) ipRange() IPRange { return IPRange{numAddr(s.lo), numAddr(s.hi)} }

func prefixSpan(p netip.Prefix) span {
	p = p.Masked()
	lo := addrNum(p.Addr())
	return span{lo, lo + (uint64(1) << (32 - p.Bits())) - 1}
}

// spansToPrefixes returns the shortest list of prefixes that covers exactly the span.
func spansToPrefixes(s span) []netip.Prefix {
	var out []netip.Prefix
	for lo := s.lo; lo <= s.hi; {
		size := addrSpace
		if lo != 0 {
			size = lo & -lo // the largest block that starts at lo
		}
		for size > s.hi-lo+1 {
			size >>= 1
		}
		out = append(out, netip.PrefixFrom(numAddr(lo), 32-(bits.Len64(size)-1)))
		lo += size
	}
	return out
}

// mergeSpans returns the union of spans: sorted, overlapping and adjacent ones joined.
func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	s := append([]span(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i].lo < s[j].lo })
	out := []span{s[0]}
	for _, x := range s[1:] {
		last := &out[len(out)-1]
		if x.lo <= last.hi+1 {
			if x.hi > last.hi {
				last.hi = x.hi
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

// complementSpans returns the addresses that none of the spans covers.
func complementSpans(in []span) []span {
	var out []span
	next := uint64(0)
	for _, s := range mergeSpans(in) {
		if s.lo > next {
			out = append(out, span{next, s.lo - 1})
		}
		next = s.hi + 1
	}
	if next < addrSpace {
		out = append(out, span{next, addrSpace - 1})
	}
	return out
}

// elementary cuts the line at the boundaries of the covering spans and returns the pieces that at
// least one span covers, in order. Within a piece, membership of every span is the same.
func elementary(cover []span, limit uint64) []span {
	cuts := map[uint64]bool{}
	for _, s := range cover {
		cuts[s.lo] = true
		if s.hi+1 < limit {
			cuts[s.hi+1] = true
		}
	}
	points := make([]uint64, 0, len(cuts))
	for c := range cuts {
		points = append(points, c)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	var out []span
	for i, lo := range points {
		hi := limit - 1
		if i+1 < len(points) {
			hi = points[i+1] - 1
		}
		for _, s := range cover {
			if s.lo <= lo && hi <= s.hi {
				out = append(out, span{lo, hi})
				break
			}
		}
	}
	return out
}

// ---- selectors of a candidate -------------------------------------------------------------

// destSpans returns the addresses a destination selector names. ok is false for a hostname,
// whose addresses are not known. A destination that names nothing known (an unknown network)
// has no spans and matches nothing, like destinationMatches.
func (w *World) destSpans(d *model.Destination) (spans []span, ok bool) {
	switch {
	case d.Hostname != nil:
		return nil, false
	case d.Uplink != nil:
		var known []span
		for _, ps := range w.prefixes {
			for _, p := range ps {
				known = append(known, prefixSpan(p))
			}
		}
		for _, p := range w.mgmt {
			known = append(known, prefixSpan(p))
		}
		return complementSpans(known), true
	case d.Network != nil:
		for _, p := range w.prefixes[lower(*d.Network)] {
			spans = append(spans, prefixSpan(p))
		}
		return mergeSpans(spans), true
	case d.Cidr != nil:
		if a, ok := parseAddr(*d.Cidr); ok {
			n := addrNum(a)
			return []span{{n, n}}, true
		}
		if p, ok := parsePrefix(*d.Cidr); ok {
			return []span{prefixSpan(p)}, true
		}
	}
	return nil, true
}

// portSels returns the protocol/port pieces a traffic selector names; wildcard is true when it
// names none (every protocol and port).
func portSels(m model.TrafficMatch) (sels []PortSel, wildcard bool) {
	proto := protocolOf(m)
	ports, ranges := deref(m.Ports), deref(m.PortRanges)
	if proto == "any" && len(ports)+len(ranges) == 0 {
		return nil, true
	}
	protos := []string{proto}
	if proto == "any" { // ports without a protocol: validation refuses it, tcp and udp is the sane reading
		protos = []string{"tcp", "udp"}
	}
	for _, p := range protos {
		switch {
		case p == "icmp":
			sels = append(sels, PortSel{Proto: "icmp"})
		case len(ports)+len(ranges) == 0:
			sels = append(sels, PortSel{p, 1, 65535})
		default:
			for _, port := range ports {
				sels = append(sels, PortSel{p, port, port})
			}
			for _, r := range ranges {
				sels = append(sels, PortSel{p, r.From, r.To})
			}
		}
	}
	return sels, false
}

// portPieces cuts the protocol/port space at the boundaries of the selectors and returns the
// pieces that at least one selector covers.
func portPieces(sels []PortSel) []PortSel {
	var out []PortSel
	for _, proto := range []string{"tcp", "udp"} {
		var cover []span
		for _, s := range sels {
			if s.Proto == proto {
				cover = append(cover, span{uint64(s.From), uint64(s.To)})
			}
		}
		for _, p := range elementary(cover, 65536) {
			out = append(out, PortSel{proto, int(p.lo), int(p.hi)})
		}
	}
	for _, s := range sels {
		if s.Proto == "icmp" {
			out = append(out, PortSel{Proto: "icmp"})
			break
		}
	}
	return out
}

// ---- the table of a source ------------------------------------------------------------------

// TableFamilies are the families that have a Table: those that select by destination, protocol
// and port.
var TableFamilies = []string{FamilyImpairment, FamilyMTU}

func sameWinner(a, b *Candidate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Layer == b.Layer && a.ID == b.ID && a.Family == b.Family
}

// Table builds the classification table of a source for a family of TableFamilies.
func (w *World) Table(src Source, family string) (Table, error) {
	if family != FamilyImpairment && family != FamilyMTU {
		return Table{}, fmt.Errorf("domain: the family %s has no classification table (it is not selected by destination and port)", family)
	}
	t := Table{Source: src, Family: family}

	var relevant []Candidate
	var destCover []span
	var sels []PortSel
	for _, c := range w.candidates() {
		if c.Family != family || !w.scopeMatches(c.Scope, src.Subject) {
			continue
		}
		relevant = append(relevant, c)
		if c.Match.Destination != nil {
			spans, ok := w.destSpans(c.Match.Destination)
			if !ok {
				t.Unresolved = append(t.Unresolved, c)
				continue
			}
			destCover = append(destCover, spans...)
		}
		s, _ := portSels(c.Match)
		sels = append(sels, s...)
	}
	// the candidates that name a hostname take no part in the table
	if len(t.Unresolved) > 0 {
		skip := map[string]bool{}
		for _, c := range t.Unresolved {
			skip[string(c.Layer)+c.ID+c.Family] = true
		}
		kept := relevant[:0:0]
		for _, c := range relevant {
			if !skip[string(c.Layer)+c.ID+c.Family] {
				kept = append(kept, c)
			}
		}
		relevant = kept
	}

	winner := func(dest netip.Addr, proto string, port int) *Candidate {
		q := Query{Source: src.Subject, DestIP: dest, Protocol: proto, Port: port}
		var matching []Candidate
		for _, c := range relevant {
			if w.candidateMatches(c, q) {
				matching = append(matching, c)
			}
		}
		if r, ok := resolveFamily(family, matching); ok {
			return r.Winner
		}
		return nil
	}
	repPort := func(p PortSel) (string, int) { return p.Proto, p.From }

	dests := elementary(destCover, addrSpace)
	ports := portPieces(sels)

	v4 := winner(netip.Addr{}, "", 0)
	if v4 != nil {
		t.Entries = append(t.Entries, Entry{Level: 4, Winner: v4})
	}
	byPort := make([]*Candidate, len(ports))
	for j, p := range ports {
		proto, port := repPort(p)
		byPort[j] = winner(netip.Addr{}, proto, port)
	}
	byDest := make([]*Candidate, len(dests))
	for i, d := range dests {
		byDest[i] = winner(numAddr(d.lo), "", 0)
	}

	var l1, l2, l3 []Entry
	for j, p := range ports {
		if !sameWinner(byPort[j], v4) {
			p := p
			l3 = append(l3, Entry{Level: 3, Port: &p, Winner: byPort[j]})
		}
	}
	for i, d := range dests {
		if !sameWinner(byDest[i], v4) {
			r := d.ipRange()
			l2 = append(l2, Entry{Level: 2, Dest: &r, Winner: byDest[i]})
		}
	}
	for i, d := range dests {
		for j, p := range ports {
			below := v4 // what the levels below level 1 give here
			switch {
			case !sameWinner(byDest[i], v4):
				below = byDest[i]
			case !sameWinner(byPort[j], v4):
				below = byPort[j]
			}
			proto, port := repPort(p)
			if got := winner(numAddr(d.lo), proto, port); !sameWinner(got, below) {
				r, p := d.ipRange(), p
				l1 = append(l1, Entry{Level: 1, Dest: &r, Port: &p, Winner: got})
			}
		}
	}
	t.Entries = append(append(append(mergeLevel1(l1), mergeLevel2(l2)...), mergeLevel3(l3)...), t.Entries...)
	return t, nil
}

func protoOrder(p string) int {
	switch p {
	case "tcp":
		return 0
	case "udp":
		return 1
	}
	return 2
}

func portLess(a, b PortSel) bool {
	if a.Proto != b.Proto {
		return protoOrder(a.Proto) < protoOrder(b.Proto)
	}
	if a.From != b.From {
		return a.From < b.From
	}
	return a.To < b.To
}

// mergeLevel2 joins adjacent destination pieces that have the same winner.
func mergeLevel2(es []Entry) []Entry {
	sort.Slice(es, func(i, j int) bool { return es[i].Dest.First.Less(es[j].Dest.First) })
	var out []Entry
	for _, e := range es {
		if n := len(out); n > 0 && addrNum(out[n-1].Dest.Last)+1 == addrNum(e.Dest.First) && sameWinner(out[n-1].Winner, e.Winner) {
			r := IPRange{out[n-1].Dest.First, e.Dest.Last}
			out[n-1].Dest = &r
			continue
		}
		out = append(out, e)
	}
	return out
}

// mergeLevel3 joins adjacent ports of one protocol that have the same winner.
func mergeLevel3(es []Entry) []Entry {
	sort.Slice(es, func(i, j int) bool { return portLess(*es[i].Port, *es[j].Port) })
	var out []Entry
	for _, e := range es {
		if n := len(out); n > 0 {
			last := out[n-1].Port
			if last.Proto == e.Port.Proto && last.To+1 == e.Port.From && sameWinner(out[n-1].Winner, e.Winner) {
				p := PortSel{last.Proto, last.From, e.Port.To}
				out[n-1].Port = &p
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// mergeLevel1 joins adjacent ports for one destination piece, then adjacent destination pieces for
// one port range, as far as the winner is the same.
func mergeLevel1(es []Entry) []Entry {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Dest.First != es[j].Dest.First {
			return es[i].Dest.First.Less(es[j].Dest.First)
		}
		return portLess(*es[i].Port, *es[j].Port)
	})
	var byPort []Entry
	for _, e := range es {
		if n := len(byPort); n > 0 {
			last := byPort[n-1]
			if last.Dest.First == e.Dest.First && last.Port.Proto == e.Port.Proto && last.Port.To+1 == e.Port.From && sameWinner(last.Winner, e.Winner) {
				p := PortSel{last.Port.Proto, last.Port.From, e.Port.To}
				byPort[n-1].Port = &p
				continue
			}
		}
		byPort = append(byPort, e)
	}
	sort.Slice(byPort, func(i, j int) bool {
		a, b := byPort[i], byPort[j]
		if *a.Port != *b.Port {
			return portLess(*a.Port, *b.Port)
		}
		return a.Dest.First.Less(b.Dest.First)
	})
	var out []Entry
	for _, e := range byPort {
		if n := len(out); n > 0 {
			last := out[n-1]
			if *last.Port == *e.Port && addrNum(last.Dest.Last)+1 == addrNum(e.Dest.First) && sameWinner(last.Winner, e.Winner) {
				r := IPRange{last.Dest.First, e.Dest.Last}
				out[n-1].Dest = &r
				continue
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dest.First != out[j].Dest.First {
			return out[i].Dest.First.Less(out[j].Dest.First)
		}
		return portLess(*out[i].Port, *out[j].Port)
	})
	return out
}
