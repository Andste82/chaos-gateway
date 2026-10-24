package compiler

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the DNS family (plan §2.4, §2.6, M20): DNS faults are not rules of the kernel but
// behavior of the DNS proxy, which "keeps no state of its own": it asks the API for the resolved faults
// (GET /internal/dns/config) and answers a query with the first entry whose sources and names match. The
// precedence of §2.4 is applied here, not in the proxy, with one difference to the families that
// resolve into the kernel: what a DNS fault selects is the name asked, which is known at run time only,
// so the compiler cannot say which fault wins for a name. It gives every source the faults that apply
// to it in the order the precedence rules try them (domain.RankOrder), and the first that names the name
// asked, or names no name at all, is the winner. Faults of the DNS family only ever compete with
// each other: a latency fault of the device does not cancel a DNS broken profile on its network (E5).

// DNSFault is one entry of the proxy's list of resolved faults: a DNS overlay, or the dns part of an
// activated profile, for the clients of its Sources.
type DNSFault struct {
	// Key is `layer:id:dns` like the keys of the other families: the id is the overlay's.
	Key string `json:"key"`
	// Overlay is the overlay (the activation, for the part of a profile); Profile the name of the profile.
	Overlay string `json:"overlay"`
	Profile string `json:"profile,omitempty"`
	// Scope says where the fault applies, for messages.
	Scope string `json:"scope"`
	// Fault is the fault as it was written.
	Fault model.DnsFault `json:"fault"`
	// Sources are the clients the entry is for: the addresses of the devices and the stretches of a
	// network that have exactly the same faults in the same order.
	Sources []netip.Prefix `json:"sources"`
}

// compileDNS builds the proxy's list: the faults that apply to each source in order of rank, the sources
// with the same list sharing their entries.
func (t *Target) compileDNS(w *domain.World, sources []domain.Source, idx *domain.Index) {
	type group struct {
		cands    []domain.Candidate
		prefixes []netip.Prefix
	}
	groups := map[string]*group{}
	var order []string
	for _, src := range sources {
		cands := w.DNSCandidates(src)
		if len(cands) == 0 {
			continue
		}
		keys := make([]string, len(cands))
		for i, c := range cands {
			keys[i] = c.Key()
		}
		k := strings.Join(keys, ",")
		g := groups[k]
		if g == nil {
			g = &group{cands: cands}
			groups[k] = g
			order = append(order, k)
		}
		g.prefixes = append(g.prefixes, src.Prefixes()...)
	}
	seen := map[string]bool{}
	for _, k := range order {
		g := groups[k]
		clients := domain.CollapsePrefixes(g.prefixes)
		for _, c := range g.cands {
			if c.DNS == nil {
				continue
			}
			t.DNSFaults = append(t.DNSFaults, DNSFault{Key: c.Key(), Overlay: c.ID, Profile: c.ProfileName,
				Scope: describeScope(idx, c.Scope), Fault: *c.DNS, Sources: clients})
			if !seen[c.Key()] {
				seen[c.Key()] = true
				t.Winners = append(t.Winners, c.Key())
				t.Effective = append(t.Effective, EffectiveFault{Key: c.Key(), Layer: string(c.Layer), Source: c.ID, Family: c.Family,
					Profile: c.ProfileName, Scope: describeScope(idx, c.Scope), Summary: c.Summary()})
			}
		}
	}
	sort.Strings(t.Winners)
}
