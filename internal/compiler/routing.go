package compiler

import (
	"encoding/binary"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Problem codes of dynamic routing.
const CodeRouting = "routing"

// BirdInstance is the name of the BIRD instance Chaos Gateway manages: its configuration file and
// control socket are <bird dir>/<instance>.conf and .ctl.
const BirdInstance = "chaosgw"

// compileRouting builds the BIRD configuration from the routing model (plan §2.2.2): one protocol per
// entry on its WireGuard link, the prefixes it announces, the import filter with the protected
// prefixes, and Chaos Gateway's table as the only place learned routes go.
func (t *Target) compileBird(cfg *model.Configuration, idx *domain.Index) {
	r := cfg.Routing
	if r == nil {
		return
	}
	externalOn := r.External != nil && r.External.Enabled != nil && *r.External.Enabled
	if (r.Protocols == nil || len(*r.Protocols) == 0) && !externalOn {
		return
	}
	c := bird.Config{KernelTable: PolicyTable, Protected: t.protectedPrefixes()}
	if r.Asn != nil {
		c.ASN = *r.Asn
	}
	c.RouterID = t.routerID(r)
	if c.RouterID == "" {
		t.errorf(CodeRouting, "", "dynamic routing needs a router id and the gateway has no address to derive one from")
		return
	}
	linkByID := map[string]*WGInterface{}
	for i := range t.WireGuard {
		linkByID[t.WireGuard[i].NetworkID] = &t.WireGuard[i]
	}
	if r.Protocols != nil {
		ids := make([]string, 0, len(*r.Protocols))
		for id := range *r.Protocols {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			a, b := (*r.Protocols)[ids[i]], (*r.Protocols)[ids[j]]
			if a.Name != b.Name {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return ids[i] < ids[j]
		})
		names := map[string]bool{}
		for _, id := range ids {
			p := (*r.Protocols)[id]
			if p.Enabled != nil && !*p.Enabled {
				continue
			}
			w := linkByID[p.Link]
			if w == nil || w.Kind != "link" {
				t.warn(CodeRouting, "", "the routing protocol %q runs on a link that is not available: it is not started", p.Name)
				continue
			}
			bp, ok := t.birdProtocol(cfg, idx, p, w)
			if !ok {
				continue
			}
			bp.Name = bird.ProtocolName(string(p.Type), p.Name)
			for names[bp.Name] {
				bp.Name += "_" + shortID(id)[:4]
			}
			names[bp.Name] = true
			c.Protocols = append(c.Protocols, bp)
		}
	}
	if externalOn {
		if r.External.Table == nil {
			t.errorf(CodeRouting, "", "external routing needs the kernel table to import from")
			return
		}
		ext := &bird.External{Table: *r.External.Table}
		ext.Import = birdImport(r.External.Import)
		c.External = ext
	}
	if c.Empty() {
		return
	}
	text, err := c.Render()
	if err != nil {
		t.errorf(CodeRouting, "", "the BIRD configuration cannot be generated: %v", err)
		return
	}
	t.Bird = &BirdTarget{Instance: BirdInstance, Config: c, Text: text}
	if externalOn {
		t.Bird.ImportTables = []int{*r.External.Table}
	}
}

// BirdTarget is the configuration of the BIRD instance.
type BirdTarget struct {
	Instance string      `json:"instance"`
	Config   bird.Config `json:"config"`
	// Text is the rendered configuration: the executor writes it, verify compares its hash.
	Text string `json:"text"`
	// ImportTables are the kernel tables BIRD may read besides Chaos Gateway's own.
	ImportTables []int `json:"import_tables,omitempty"`
}

func (t *Target) birdProtocol(cfg *model.Configuration, idx *domain.Index, p model.RoutingProtocol, w *WGInterface) (bird.Protocol, bool) {
	bp := bird.Protocol{Type: string(p.Type), Interface: w.Name, LocalAddress: w.Address.Addr().String()}
	// the neighbor's address: the link peer's, unless the protocol names another
	peer := ""
	if n := idx.Networks[p.Link]; n != nil && n.WG != nil && n.WG.Peer != nil {
		peer = n.WG.Peer.Address
	}
	if p.Type == model.RoutingProtocolTypeBgp && p.Bgp != nil && p.Bgp.NeighborAddress != nil {
		peer = *p.Bgp.NeighborAddress
	}
	if peer == "" {
		t.warn(CodeRouting, "", "the routing protocol %q has no neighbor address", p.Name)
		return bp, false
	}
	bp.NeighborAddress = peer
	bp.Import = birdImport(p.Import)
	for _, a := range deref(p.Announce) {
		bp.Announce = append(bp.Announce, t.announced(cfg, a)...)
	}
	bp.Announce = dedupeStrings(bp.Announce)
	if p.CustomSnippet != nil {
		bp.Custom = *p.CustomSnippet
	}
	switch p.Type {
	case model.RoutingProtocolTypeBgp:
		if p.Bgp == nil {
			t.errorf(CodeRouting, "", "the BGP protocol %q has no settings", p.Name)
			return bp, false
		}
		bp.BGP = &bird.BGP{NeighborASN: p.Bgp.NeighborAsn, HoldTime: dur(p.Bgp.HoldTime, 90), KeepaliveTime: dur(p.Bgp.KeepaliveTime, 30)}
		bp.BGP.Passive = p.Bgp.Passive != nil && *p.Bgp.Passive
	case model.RoutingProtocolTypeOspf:
		o := bird.OSPF{Area: "0", HelloInterval: 10, DeadInterval: 40}
		if p.Ospf != nil {
			if p.Ospf.Area != nil {
				o.Area = *p.Ospf.Area
			}
			if p.Ospf.Cost != nil {
				o.Cost = *p.Ospf.Cost
			}
			o.HelloInterval = dur(p.Ospf.HelloInterval, 10)
			o.DeadInterval = dur(p.Ospf.DeadInterval, 40)
		}
		bp.OSPF = &o
	case model.RoutingProtocolTypeBabel:
		b := bird.Babel{HelloInterval: 4}
		if p.Babel != nil {
			b.HelloInterval = dur(p.Babel.HelloInterval, 4)
			if p.Babel.Rxcost != nil {
				b.RxCost = *p.Babel.Rxcost
			}
		}
		bp.Babel = &b
	}
	return bp, true
}

func dur(d *model.Duration, defSeconds int) int {
	if d == nil {
		return defSeconds
	}
	v, err := time.ParseDuration(*d)
	if err != nil || v <= 0 {
		return defSeconds
	}
	return int(v / time.Second)
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func birdImport(f *model.ImportFilter) bird.Import {
	var i bird.Import
	if f == nil {
		return i
	}
	i.AllowDefault = f.AllowDefault != nil && *f.AllowDefault
	if f.MaxPrefixes != nil {
		i.MaxPrefixes = *f.MaxPrefixes
	}
	for _, a := range deref(f.AllowedPrefixes) {
		e := bird.Allowed{Prefix: a.Prefix}
		if a.MaxLength != nil {
			e.MaxLength = *a.MaxLength
		}
		i.Allowed = append(i.Allowed, e)
	}
	return i
}

// announced returns the prefixes an announce entry stands for: a prefix itself, a network's subnet
// (a WireGuard network with the networks behind its clients, a link with its static routes), the
// networks behind a client.
func (t *Target) announced(cfg *model.Configuration, a model.AnnounceEntry) []string {
	var out []string
	add := func(s string) {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked().String())
		}
	}
	switch {
	case a.Cidr != nil:
		add(*a.Cidr)
	case a.Network != nil && cfg.Networks != nil:
		if n, ok := (*cfg.Networks)[*a.Network]; ok {
			if disc, _ := n.Discriminator(); disc == "lan" {
				if lan, err := n.AsLanNetwork(); err == nil {
					add(lan.Address)
				}
			} else if wg, err := n.AsWireGuardNetwork(); err == nil {
				add(wg.Address)
				for _, c := range deref2(wg.Clients) {
					for _, cn := range deref(c.ClientNetworks) {
						add(cn)
					}
				}
				for _, r := range deref(wg.Routes) {
					add(r)
				}
			}
		}
	case a.Client != nil && cfg.Networks != nil:
		for _, n := range *cfg.Networks {
			if wg, err := n.AsWireGuardNetwork(); err == nil && wg.Clients != nil {
				if c, ok := (*wg.Clients)[*a.Client]; ok {
					for _, cn := range deref(c.ClientNetworks) {
						add(cn)
					}
				}
			}
		}
	}
	return out
}

func deref2[T any](m *map[string]T) map[string]T {
	if m == nil {
		return nil
	}
	return *m
}

// protectedPrefixes are what no neighbor may announce: the management network, the uplink's subnet
// and the gateway's own networks (plan §2.2.2).
func (t *Target) protectedPrefixes() []string {
	var out []string
	for _, p := range t.Management.Sources {
		out = append(out, p.Masked().String())
	}
	if t.Uplink.Addr.IsValid() {
		out = append(out, t.Uplink.Addr.Masked().String())
	}
	for _, b := range t.Bridges {
		out = append(out, b.Address.Masked().String())
	}
	for _, w := range t.WireGuard {
		out = append(out, w.Address.Masked().String())
	}
	// what the executor routes itself: the networks behind clients, links and routers. A more
	// specific prefix from a neighbor would win over them in the kernel.
	for _, r := range t.Routes {
		if r.Table == PolicyTable && r.Dst != "" && r.Dst != "default" {
			if p, err := netip.ParsePrefix(r.Dst); err == nil {
				out = append(out, p.Masked().String())
			} else if a, err := netip.ParseAddr(r.Dst); err == nil {
				out = append(out, netip.PrefixFrom(a, a.BitLen()).String())
			}
		}
	}
	return dedupeStrings(out)
}

// routerID is the configured router id, or the numerically lowest gateway address of the local test
// networks.
func (t *Target) routerID(r *model.Routing) string {
	if r.RouterId != nil {
		return *r.RouterId
	}
	var lowest uint32
	found := false
	for _, b := range t.Bridges {
		if !b.Address.Addr().Is4() {
			continue
		}
		v := binary.BigEndian.Uint32(b.Address.Addr().AsSlice())
		if !found || v < lowest {
			lowest, found = v, true
		}
	}
	if !found {
		for _, w := range t.WireGuard {
			v := binary.BigEndian.Uint32(w.Address.Addr().AsSlice())
			if !found || v < lowest {
				lowest, found = v, true
			}
		}
	}
	if !found {
		return ""
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], lowest)
	return netip.AddrFrom4(b).String()
}
