package bird

// Allowed is an entry of an import filter's prefix list: the prefix and, optionally, the longest
// more specific prefix that is accepted too.
type Allowed struct {
	Prefix string `json:"prefix"`
	// MaxLength 0 means the prefix only.
	MaxLength int `json:"max_length,omitempty"`
}

// Import describes what a protocol may learn.
type Import struct {
	// AllowDefault lets a default route through; it is rejected otherwise.
	AllowDefault bool `json:"allow_default,omitempty"`
	// Allowed restricts the learned prefixes to a list; empty means every prefix that is not
	// protected.
	Allowed []Allowed `json:"allowed,omitempty"`
	// MaxPrefixes disables the protocol when a neighbor announces more; 0 means no limit.
	MaxPrefixes int `json:"max_prefixes,omitempty"`
}

// BGP settings of a protocol.
type BGP struct {
	NeighborASN int64 `json:"neighbor_asn"`
	// HoldTime and KeepaliveTime in seconds.
	HoldTime      int  `json:"hold_time"`
	KeepaliveTime int  `json:"keepalive_time"`
	Passive       bool `json:"passive,omitempty"`
}

// OSPF settings (OSPFv2, point to point on the link).
type OSPF struct {
	Area string `json:"area"`
	Cost int    `json:"cost,omitempty"`
	// HelloInterval and DeadInterval in seconds.
	HelloInterval int `json:"hello_interval"`
	DeadInterval  int `json:"dead_interval"`
}

// Babel settings (tunnel interface).
type Babel struct {
	// HelloInterval in seconds.
	HelloInterval int `json:"hello_interval"`
	RxCost        int `json:"rxcost,omitempty"`
}

// Protocol is one routing protocol on one WireGuard link.
type Protocol struct {
	// Name is the BIRD protocol name (an identifier).
	Name string `json:"name"`
	// Type is bgp, ospf or babel.
	Type string `json:"type"`
	// Interface is the link's WireGuard interface.
	Interface string `json:"interface"`
	// LocalAddress and NeighborAddress are the two ends of the link's transfer net.
	LocalAddress    string `json:"local_address"`
	NeighborAddress string `json:"neighbor_address"`
	// Announce are the prefixes announced to the neighbor.
	Announce []string `json:"announce,omitempty"`
	Import   Import   `json:"import"`
	BGP      *BGP     `json:"bgp,omitempty"`
	OSPF     *OSPF    `json:"ospf,omitempty"`
	Babel    *Babel   `json:"babel,omitempty"`
	// Custom is raw BIRD configuration appended inside the protocol block (unmanaged).
	Custom string `json:"custom,omitempty"`
}

// External imports routes that another daemon writes into a kernel table.
type External struct {
	Table  int    `json:"table"`
	Import Import `json:"import"`
}

// Config is everything the configuration is generated from.
type Config struct {
	RouterID string `json:"router_id"`
	// ASN is the local AS number for BGP.
	ASN int64 `json:"asn,omitempty"`
	// KernelTable is Chaos Gateway's routing table: the only one learned routes are exported into.
	KernelTable int `json:"kernel_table"`
	// Protected are the prefixes no neighbor may announce, nor anything inside them: the
	// management network, the uplink's subnet and the gateway's own networks.
	Protected []string   `json:"protected"`
	Protocols []Protocol `json:"protocols"`
	External  *External  `json:"external,omitempty"`
}

// Empty reports whether the configuration runs nothing but the device and kernel protocols.
func (c Config) Empty() bool { return len(c.Protocols) == 0 && c.External == nil }

// Ports of the routing protocols: the gateway accepts them from the link's interface.
const (
	BGPPort   = 179
	BabelPort = 6696
	// OSPFProtocol is the IP protocol number of OSPF.
	OSPFProtocol = 89
)
