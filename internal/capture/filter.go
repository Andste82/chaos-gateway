package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

// LinkType is the pcap link type of an interface (LINKTYPE_*): what the first byte of a captured packet is.
type LinkType uint16

// The two link types a capture can have: the test networks' bridges are Ethernet, a WireGuard interface
// carries raw IP packets (plan §2.11).
const (
	LinkEthernet LinkType = 1   // LINKTYPE_ETHERNET
	LinkRaw      LinkType = 101 // LINKTYPE_RAW: an IPv4 or IPv6 packet without a link header
)

func (l LinkType) String() string {
	switch l {
	case LinkEthernet:
		return "ethernet"
	case LinkRaw:
		return "raw"
	}
	return fmt.Sprintf("linktype-%d", uint16(l))
}

// Protocol is the transport protocol a filter selects.
type Protocol string

// The protocols a filter selects; the empty protocol selects every packet.
const (
	AnyProtocol Protocol = ""
	TCP         Protocol = "tcp"
	UDP         Protocol = "udp"
	// ICMP is ICMP for IPv4 and ICMPv6 for IPv6.
	ICMP Protocol = "icmp"
)

// PortRange is an inclusive range of ports; a single port has From == To.
type PortRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

// Filter selects the packets of a capture on one interface (plan §2.11, spike S9). A packet has two
// sides, its source and its destination; the filter names the *local* side (the device or network
// that is being captured) and the *peer* side (what it talks to) and selects a packet when either
// direction fits:
//
//	local(source) and peer(destination) and the port is the destination port, or
//	local(destination) and peer(source) and the port is the source port
//
// so a connection of the device is captured in both directions and with its replies, which is what
// "exact against a capture on the device itself" asks for. Every empty part selects everything: the
// zero Filter selects every packet of the interface.
type Filter struct {
	// MACs are the Ethernet addresses of the local side; a frame is local when its source (or, for the
	// other direction, its destination) is one of them. Only for Ethernet interfaces.
	MACs []string `json:"macs,omitempty"`
	// Hosts are the addresses and prefixes of the local side (a device on an interface without
	// Ethernet addresses, such as a WireGuard client). When MACs and Hosts are both set, either decides.
	Hosts []netip.Prefix `json:"hosts,omitempty"`
	// Peers are the addresses and prefixes the local side talks to, NotPeers those it does not (the destination
	// "uplink" is every address outside the configured networks). A packet that is not IP is never selected
	// when Peers, NotPeers, Protocol or Ports are set.
	Peers    []netip.Prefix `json:"peers,omitempty"`
	NotPeers []netip.Prefix `json:"not_peers,omitempty"`
	// Protocol and Ports select the transport; Ports are the peer's ports (the destination port of what the local
	// side sends, the source port of what it receives) and need the protocol tcp or udp.
	Protocol Protocol    `json:"protocol,omitempty"`
	Ports    []PortRange `json:"ports,omitempty"`
}

// ErrTooLarge is returned by BPF for a filter whose program would not fit the kernel's limit of 4096
// instructions: the capture then filters in user space only.
var ErrTooLarge = errors.New("capture: the filter is too large for a kernel filter")

// Validate checks the filter for the link type of the interface it is for.
func (f *Filter) Validate(link LinkType) error {
	switch link {
	case LinkEthernet, LinkRaw:
	default:
		return fmt.Errorf("the link type %s cannot be captured", link)
	}
	if len(f.MACs) > 0 && link != LinkEthernet {
		return errors.New("an Ethernet address can only select packets of an Ethernet interface")
	}
	for _, m := range f.MACs {
		if _, err := parseMAC(m); err != nil {
			return err
		}
	}
	for _, p := range slices.Concat(f.Hosts, f.Peers, f.NotPeers) {
		if !p.IsValid() {
			return errors.New("an address or prefix of the filter is not valid")
		}
		if p.Addr().Is4In6() {
			return fmt.Errorf("%s is an IPv4-mapped IPv6 address: use the IPv4 address", p)
		}
	}
	switch f.Protocol {
	case AnyProtocol, TCP, UDP, ICMP:
	default:
		return fmt.Errorf("the protocol %q is not tcp, udp or icmp", f.Protocol)
	}
	if len(f.Ports) > 0 && f.Protocol != TCP && f.Protocol != UDP {
		return errors.New("ports need the protocol tcp or udp")
	}
	for _, r := range f.Ports {
		if r.From == 0 || r.To < r.From {
			return fmt.Errorf("the port range %d-%d is not valid", r.From, r.To)
		}
	}
	return nil
}

// matchesAll reports whether the filter selects every packet.
func (f *Filter) matchesAll() bool {
	return len(f.MACs) == 0 && len(f.Hosts) == 0 && len(f.Peers) == 0 && len(f.NotPeers) == 0 && f.Protocol == AnyProtocol && len(f.Ports) == 0
}

// hasLocal reports whether the filter names the local side.
func (f *Filter) hasLocal() bool { return len(f.MACs) > 0 || len(f.Hosts) > 0 }

func parseMAC(s string) ([6]byte, error) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return [6]byte{}, fmt.Errorf("%q is not an Ethernet address", s)
	}
	return [6]byte(hw), nil
}

// packet is the part of a captured packet a filter looks at.
type packet struct {
	eth      bool
	src, dst [6]byte // Ethernet addresses
	ip       bool
	sa, da   netip.Addr
	proto    uint8 // IP protocol / next header
	l4       bool  // the transport header is there: the first fragment, no extension headers
	sp, dp   uint16
}

const (
	etherIPv4 = 0x0800
	etherIPv6 = 0x86dd
	protoICMP = 1
	protoTCP  = 6
	protoUDP  = 17
	protoICM6 = 58
)

// parse reads what the filter needs of a packet. A packet that is too short is parsed as far as it goes.
func parse(link LinkType, b []byte) packet {
	var p packet
	family := 0
	if link == LinkEthernet {
		if len(b) < 14 {
			return p
		}
		p.eth = true
		copy(p.dst[:], b[0:6])
		copy(p.src[:], b[6:12])
		switch binary.BigEndian.Uint16(b[12:14]) {
		case etherIPv4:
			family = 4
		case etherIPv6:
			family = 6
		}
		b = b[14:]
	} else if len(b) > 0 {
		family = int(b[0] >> 4)
	}
	var l4 []byte
	switch {
	case family == 4 && len(b) >= 20:
		ihl := int(b[0]&0xf) * 4
		p.ip = true
		p.sa, p.da = netip.AddrFrom4([4]byte(b[12:16])), netip.AddrFrom4([4]byte(b[16:20]))
		p.proto = b[9]
		// only the first fragment has the transport header
		if binary.BigEndian.Uint16(b[6:8])&0x1fff == 0 && len(b) >= ihl {
			l4 = b[ihl:]
		}
	case family == 6 && len(b) >= 40:
		p.ip = true
		p.sa, p.da = netip.AddrFrom16([16]byte(b[8:24])), netip.AddrFrom16([16]byte(b[24:40]))
		p.proto = b[6]
		l4 = b[40:]
	default:
		return p
	}
	if (p.proto == protoTCP || p.proto == protoUDP) && len(l4) >= 4 {
		p.l4 = true
		p.sp, p.dp = binary.BigEndian.Uint16(l4[0:2]), binary.BigEndian.Uint16(l4[2:4])
	}
	return p
}

// Matcher is a filter prepared for one link type: the specification of the kernel filter BPF compiles
// (they are tested against each other) and the filter of a capture whose program is too large.
type Matcher struct {
	f    Filter
	link LinkType
	macs [][6]byte
}

// Matcher validates the filter for the link type and prepares it for Match and BPF.
func (f *Filter) Matcher(link LinkType) (*Matcher, error) {
	if err := f.Validate(link); err != nil {
		return nil, err
	}
	m := &Matcher{f: *f, link: link}
	for _, s := range f.MACs {
		hw, _ := parseMAC(s)
		m.macs = append(m.macs, hw)
	}
	return m, nil
}

// Match reports whether the filter selects the packet, which starts with the link header of the link type.
func (m *Matcher) Match(b []byte) bool {
	if m.f.matchesAll() {
		return true
	}
	p := parse(m.link, b)
	return m.matches(&p, true) || m.matches(&p, false)
}

// matches tests one direction: forward means the source is the local side.
func (m *Matcher) matches(p *packet, forward bool) bool {
	f := &m.f
	if f.hasLocal() {
		var mac [6]byte
		var addr netip.Addr
		if forward {
			mac, addr = p.src, p.sa
		} else {
			mac, addr = p.dst, p.da
		}
		local := false
		if p.eth {
			local = slices.Contains(m.macs, mac)
		}
		if !local && p.ip {
			for _, h := range f.Hosts {
				if h.Contains(addr) {
					local = true
					break
				}
			}
		}
		if !local {
			return false
		}
	}
	if len(f.Peers) > 0 || len(f.NotPeers) > 0 {
		if !p.ip {
			return false
		}
		addr := p.da
		if !forward {
			addr = p.sa
		}
		if slices.ContainsFunc(f.NotPeers, func(h netip.Prefix) bool { return h.Contains(addr) }) {
			return false
		}
		if len(f.Peers) > 0 && !slices.ContainsFunc(f.Peers, func(h netip.Prefix) bool { return h.Contains(addr) }) {
			return false
		}
	}
	if f.Protocol != AnyProtocol {
		if !p.ip {
			return false
		}
		switch f.Protocol {
		case TCP:
			if p.proto != protoTCP {
				return false
			}
		case UDP:
			if p.proto != protoUDP {
				return false
			}
		case ICMP:
			if (p.proto != protoICMP && p.proto != protoICM6) || (p.proto == protoICMP) != p.sa.Is4() {
				return false
			}
		}
	}
	if len(f.Ports) > 0 {
		if !p.l4 {
			return false
		}
		port := p.dp
		if !forward {
			port = p.sp
		}
		found := false
		for _, r := range f.Ports {
			if port >= r.From && port <= r.To {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// String describes the filter for a human: the addresses, the protocol and the ports.
func (f *Filter) String() string {
	if f.matchesAll() {
		return "all packets"
	}
	var parts []string
	if len(f.MACs) > 0 {
		parts = append(parts, "ether "+strings.Join(f.MACs, ","))
	}
	if len(f.Hosts) > 0 {
		parts = append(parts, "host "+joinPrefixes(f.Hosts))
	}
	if len(f.Peers) > 0 {
		parts = append(parts, "peer "+joinPrefixes(f.Peers))
	}
	if len(f.NotPeers) > 0 {
		parts = append(parts, "not peer "+joinPrefixes(f.NotPeers))
	}
	if f.Protocol != AnyProtocol {
		parts = append(parts, string(f.Protocol))
	}
	if len(f.Ports) > 0 {
		ps := make([]string, len(f.Ports))
		for i, r := range f.Ports {
			if r.From == r.To {
				ps[i] = fmt.Sprint(r.From)
			} else {
				ps[i] = fmt.Sprintf("%d-%d", r.From, r.To)
			}
		}
		parts = append(parts, "port "+strings.Join(ps, ","))
	}
	return strings.Join(parts, " and ")
}

func joinPrefixes(ps []netip.Prefix) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		if p.IsSingleIP() {
			s[i] = p.Addr().String()
		} else {
			s[i] = p.String()
		}
	}
	return strings.Join(s, ",")
}
