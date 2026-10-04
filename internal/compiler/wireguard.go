package compiler

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// Problem codes of WireGuard networks.
const (
	CodeWireGuardKey = "wireguard_key"
	// DefaultMTU is the MTU of WireGuard interfaces (plan §2.2.1).
	DefaultMTU = 1420
	// DefaultKeepalive is the persistent keepalive in seconds of a peer the gateway initiates.
	DefaultKeepalive = 25
)

// WGPeer is a peer of a WireGuard interface: a client of a hub or the remote side of a link.
type WGPeer struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	PublicKey       string   `json:"public_key"`
	PresharedKeyRef string   `json:"preshared_key_ref,omitempty"`
	AllowedIPs      []string `json:"allowed_ips"`
	Keepalive       int      `json:"keepalive,omitempty"`
	Endpoint        string   `json:"endpoint,omitempty"`
	// Routes are the prefixes the table 100 reaches through this peer: the networks behind a
	// client, the static routes of a link.
	Routes []string `json:"routes,omitempty"`

	// reachable is what the client may reach: implicit access matrix entries
	reachable []model.MatrixEndpoint
	address   netip.Addr
	networks  []netip.Prefix
}

// WGInterface is a WireGuard network on the host.
type WGInterface struct {
	NetworkID   string       `json:"network_id"`
	NetworkName string       `json:"network_name"`
	Name        string       `json:"name"`
	Kind        string       `json:"kind"` // hub | link
	Role        string       `json:"role"` // test | management
	Address     netip.Prefix `json:"address"`
	MTU         int          `json:"mtu"`
	ListenPort  int          `json:"listen_port"`
	// KeyRef is the secrets store key of the interface's private key (the network's id).
	KeyRef string `json:"key_ref"`
	// PublicKey is the interface's public key: verify compares it with the kernel's.
	PublicKey string `json:"public_key"`
	// NAT reports masquerade towards the uplink.
	NAT   bool     `json:"nat"`
	Peers []WGPeer `json:"peers"`
	// LinkLocal is an IPv6 link-local address for a link interface that runs Babel (M4c-05): Babel's
	// wire protocol needs one even to exchange IPv4 routes, and a WireGuard interface gets none on
	// its own. nil when not needed.
	LinkLocal *netip.Prefix `json:"link_local,omitempty"`
}

// wgName derives the interface name of a WireGuard network: wg-<name>, at most 15 characters.
func wgName(id, name string, used map[string]bool) string {
	n := strings.Trim(nameClean.ReplaceAllString(strings.ToLower(name), "-"), "-_")
	if len(n) > 12 {
		n = n[:12]
	}
	cand := "wg-" + n
	if n == "" || used[cand] {
		cand = "wg-" + strings.ReplaceAll(id, "-", "")[:8]
	}
	used[cand] = true
	return cand
}

func keyGeneration(k *model.WireGuardKeySettings) int {
	if k == nil || k.Generation == nil {
		return 0
	}
	return *k.Generation
}

func seconds(d *model.Duration, def int) int {
	if d == nil {
		return def
	}
	v, err := time.ParseDuration(*d)
	if err != nil || v < 0 {
		return def
	}
	return int(v / time.Second)
}

func maskedStrings(in *[]string) []string {
	if in == nil {
		return nil
	}
	var out []string
	for _, s := range *in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked().String())
		}
	}
	sort.Strings(out)
	return out
}

func (t *Target) compileWireGuardNetwork(id string, n *domain.NetInfo, in Input, used map[string]bool) {
	wg := n.WG
	addr, err := netip.ParsePrefix(wg.Address)
	if err != nil {
		t.errorf(CodeUnsupported, n.Name, "network %q has no valid address", n.Name)
		return
	}
	pub, ok := in.Keys[id]
	if !ok || pub == "" {
		t.errorf(CodeWireGuardKey, n.Name, "the WireGuard network %q has no interface key: provision the configuration first", n.Name)
		return
	}
	w := WGInterface{
		NetworkID: id, NetworkName: n.Name, Name: wgName(id, n.Name, used), Kind: string(wg.Kind), Role: "test",
		Address: addr, MTU: DefaultMTU, ListenPort: wg.ListenPort, KeyRef: id, PublicKey: pub,
		NAT: wg.Nat == nil || *wg.Nat,
	}
	if wg.Role != nil {
		w.Role = string(*wg.Role)
	}
	if wg.Mtu != nil {
		w.MTU = *wg.Mtu
	}
	switch wg.Kind {
	case model.Hub:
		if wg.Clients != nil {
			cids := make([]string, 0, len(*wg.Clients))
			for cid := range *wg.Clients {
				cids = append(cids, cid)
			}
			sort.Strings(cids)
			for _, cid := range cids {
				c := (*wg.Clients)[cid]
				if c.Enabled != nil && !*c.Enabled {
					continue // a disabled client is removed from the interface
				}
				if c.Key == nil || c.Key.PublicKey == nil || *c.Key.PublicKey == "" {
					t.errorf(CodeWireGuardKey, n.Name, "the client %q has no public key: provision the configuration first", c.Name)
					continue
				}
				a, err := netip.ParseAddr(c.Address)
				if err != nil {
					t.errorf(CodeUnsupported, n.Name, "the client %q has no valid address", c.Name)
					continue
				}
				nets := maskedStrings(c.ClientNetworks)
				p := WGPeer{ID: cid, Name: c.Name, PublicKey: *c.Key.PublicKey, Routes: nets, address: a}
				p.AllowedIPs = append([]string{netip.PrefixFrom(a, 32).String()}, nets...)
				for _, s := range nets {
					pf, _ := netip.ParsePrefix(s)
					p.networks = append(p.networks, pf)
				}
				if c.Key.PresharedKey != nil && *c.Key.PresharedKey {
					p.PresharedKeyRef = wireguard.KeyID(cid, keyGeneration(c.Key))
				}
				if c.Reachable != nil {
					p.reachable = *c.Reachable
				}
				w.Peers = append(w.Peers, p)
			}
		}
	case model.Link:
		if wg.Peer != nil && (wg.Peer.Enabled == nil || *wg.Peer.Enabled) {
			if wg.Peer.Key == nil || wg.Peer.Key.PublicKey == nil || *wg.Peer.Key.PublicKey == "" {
				t.errorf(CodeWireGuardKey, n.Name, "the link %q has no public key for its remote side", n.Name)
			} else {
				p := WGPeer{ID: id, Name: n.Name, PublicKey: *wg.Peer.Key.PublicKey, AllowedIPs: []string{"0.0.0.0/0"}}
				if wg.Peer.Endpoint != nil {
					p.Endpoint = *wg.Peer.Endpoint
				}
				def := 0
				if p.Endpoint != "" {
					def = DefaultKeepalive // the gateway initiates the tunnel and keeps it open
				}
				p.Keepalive = seconds(wg.Peer.Keepalive, def)
				if wg.Routes != nil {
					p.Routes = maskedStrings(wg.Routes)
				}
				if wg.Peer.Key.PresharedKey != nil && *wg.Peer.Key.PresharedKey {
					p.PresharedKeyRef = wireguard.KeyID(wireguard.LinkPeerKeyID(id), keyGeneration(wg.Peer.Key))
				}
				w.Peers = append(w.Peers, p)
			}
		}
	}
	t.WireGuard = append(t.WireGuard, w)
}

// finishManagementSources adds the tunnel subnets of management-role WireGuard networks to the
// sources that reach the control plane, and reports when there are none at all.
func (t *Target) finishManagementSources() {
	m := &t.Management
	for _, w := range t.WireGuard {
		if w.Role == "management" {
			m.Sources = append(m.Sources, w.Address.Masked())
		}
	}
	sort.Slice(m.Sources, func(i, j int) bool { return m.Sources[i].String() < m.Sources[j].String() })
	m.Sources = dedupePrefixes(m.Sources)
	if len(m.Sources) == 0 {
		t.warn(CodeNoManagementSrc, "", "no source is allowed to reach the control plane (UI, API, SSH) from the management network")
	}
}

// topo is what the nftables compilation needs to know about the networks: the interface of each
// network, the match of each client, and the interface sets.
type topo struct {
	netDev  map[string]string // network id -> bridge or WireGuard interface
	clients map[string]clientMatch
	// order of the clients with implicit access
	reach []reachEntry
	lan   []string // bridges of local test networks
	test  []string // untrusted interfaces: bridges and WireGuard networks of role test
	all   []string // every interface of Chaos Gateway's networks
	wg    []string // WireGuard interfaces
	nat   []natSource
}

type clientMatch struct {
	dev      string
	prefixes []netip.Prefix // tunnel address and the networks behind the client
}

type reachEntry struct {
	client string
	ep     model.MatrixEndpoint
}

// natSource is one postrouting masquerade rule: matched either by source prefix, or, for a routed
// link, by the interface it comes in on (dev set, prefixes empty).
type natSource struct {
	id       string
	prefixes []netip.Prefix
	dev      string
}

func (t *Target) topology(idx *domain.Index, nets map[string]*Bridge) *topo {
	tp := &topo{netDev: map[string]string{}, clients: map[string]clientMatch{}}
	for _, b := range t.Bridges {
		tp.netDev[b.NetworkID] = b.Name
		tp.lan = append(tp.lan, b.Name)
		tp.test = append(tp.test, b.Name)
		tp.all = append(tp.all, b.Name)
		if b.NAT {
			tp.nat = append(tp.nat, natSource{id: b.NetworkID, prefixes: []netip.Prefix{b.Address.Masked()}})
		}
	}
	for _, w := range t.WireGuard {
		tp.netDev[w.NetworkID] = w.Name
		tp.wg = append(tp.wg, w.Name)
		tp.all = append(tp.all, w.Name)
		if w.Role != "management" {
			tp.test = append(tp.test, w.Name)
		}
		src := []netip.Prefix{w.Address.Masked()}
		for _, p := range w.Peers {
			tp.clients[p.ID] = clientMatch{dev: w.Name, prefixes: append([]netip.Prefix{netip.PrefixFrom(p.address, 32)}, p.networks...)}
			for _, r := range p.reachable {
				tp.reach = append(tp.reach, reachEntry{client: p.ID, ep: r})
			}
			src = append(src, p.networks...)
		}
		if !w.NAT {
			continue
		}
		if w.Kind == "link" {
			// a link's traffic is routed, not addressed from a fixed prefix: everything coming in
			// through it is masqueraded towards the uplink, covering static routes and whatever a
			// routing protocol learns over it (plan §2.2.1).
			tp.nat = append(tp.nat, natSource{id: w.NetworkID, dev: w.Name})
			continue
		}
		tp.nat = append(tp.nat, natSource{id: w.NetworkID, prefixes: src})
	}
	sort.Strings(tp.lan)
	sort.Strings(tp.test)
	sort.Strings(tp.all)
	sort.Strings(tp.wg)
	return tp
}
