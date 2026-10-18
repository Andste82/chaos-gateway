package capture

import (
	"errors"
	"math/rand/v2"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/net/bpf"
)

func mustMatcher(t testing.TB, link LinkType, f Filter) *Matcher {
	t.Helper()
	m, err := f.Matcher(link)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	return m
}

func pfx(s string) netip.Prefix {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p
	}
	a := netip.MustParseAddr(s)
	return netip.PrefixFrom(a, a.BitLen())
}

// runBPF runs the program on a packet like the kernel does: a load past the end of the packet ends the
// program with 0.
func runBPF(t testing.TB, prog []bpf.Instruction, b []byte) bool {
	t.Helper()
	vm, err := bpf.NewVM(prog)
	if err != nil {
		t.Fatalf("the program is refused: %v", err)
	}
	n, err := vm.Run(b)
	if err != nil {
		t.Fatalf("the program failed: %v", err)
	}
	return n > 0
}

func TestAFilterSelectsBothDirectionsOfTheLocalSide(t *testing.T) {
	m := mustMatcher(t, LinkEthernet, Filter{MACs: []string{macDev}})
	for name, p := range map[string]pk{
		"from the device": tcpUp(8883),
		"to the device":   tcpDown(8883),
	} {
		if !m.Match(p.build()) {
			t.Errorf("%s is not selected", name)
		}
	}
	other := tcpUp(8883)
	other.src, other.sa = macOther, "10.10.0.12"
	if m.Match(other.build()) {
		t.Error("the packet of another device is selected")
	}
}

func TestPeersProtocolAndPortsNarrowTheSelectionAndPortsAreThePeersPorts(t *testing.T) {
	f := Filter{MACs: []string{macDev}, Peers: []netip.Prefix{pfx("203.0.113.0/24")}, Protocol: TCP, Ports: []PortRange{{8883, 8883}}}
	m := mustMatcher(t, LinkEthernet, f)
	udp := tcpUp(8883)
	udp.proto = protoUDP
	farPeer := tcpUp(8883)
	farPeer.da = "198.51.100.7"
	farReply := tcpDown(8883)
	farReply.sa = "198.51.100.7"
	cases := []struct {
		name string
		p    pk
		want bool
	}{
		{"the device's request", tcpUp(8883), true},
		{"the answer", tcpDown(8883), true},
		{"another port", tcpUp(443), false},
		{"the answer from another port", tcpDown(443), false},
		{"another protocol", udp, false},
		{"another peer", farPeer, false},
		{"the answer of another peer", farReply, false},
	}
	for _, c := range cases {
		if got := m.Match(c.p.build()); got != c.want {
			t.Errorf("%s: selected %v, want %v", c.name, got, c.want)
		}
	}
	// the device's own source port is not the port that was asked for
	up := tcpUp(443)
	up.sp = 8883
	if m.Match(up.build()) {
		t.Error("a packet from the device's port 8883 to port 443 is selected")
	}
}

func TestAFilterWithPeersNeverSelectsAFrameThatIsNoIPPacket(t *testing.T) {
	arp := make([]byte, 42)
	copy(arp[0:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(arp[6:], []byte{2, 0, 0, 0, 0, 0x0b})
	arp[12], arp[13] = 0x08, 0x06
	if !mustMatcher(t, LinkEthernet, Filter{MACs: []string{macDev}}).Match(arp) {
		t.Error("the device's ARP frame is not part of its capture")
	}
	for _, f := range []Filter{{MACs: []string{macDev}, Peers: []netip.Prefix{pfx("10.0.0.0/8")}}, {MACs: []string{macDev}, Protocol: TCP}} {
		if mustMatcher(t, LinkEthernet, f).Match(arp) {
			t.Errorf("%s selects an ARP frame", f.String())
		}
	}
}

func TestNotPeersExcludeAddressesAndNeedAnIPPacket(t *testing.T) {
	// the destination "uplink": everything but the configured networks
	m := mustMatcher(t, LinkEthernet, Filter{MACs: []string{macDev}, NotPeers: []netip.Prefix{pfx("10.0.0.0/8")}})
	out := tcpUp(443) // to 203.0.113.10
	local := tcpUp(443)
	local.da = "10.10.0.50"
	localReply := tcpDown(443)
	localReply.sa = "10.10.0.50"
	arp := make([]byte, 60)
	copy(arp[6:], []byte{2, 0, 0, 0, 0, 0x0b})
	arp[12], arp[13] = 0x08, 0x06
	for name, c := range map[string]struct {
		b    []byte
		want bool
	}{"to the uplink": {out.build(), true}, "the reply from the uplink": {tcpDown(443).build(), true}, "to a local network": {local.build(), false}, "the reply from a local host": {localReply.build(), false}, "an ARP frame": {arp, false}} {
		if got := m.Match(c.b); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestHostsSelectRawIPPacketsOfBothFamilies(t *testing.T) {
	m := mustMatcher(t, LinkRaw, Filter{Hosts: []netip.Prefix{pfx("10.20.0.2"), pfx("fd00:20::/64")}})
	v4 := pk{sa: "10.20.0.2", da: "203.0.113.10", proto: protoUDP, sp: 5000, dp: 53}
	v4r := pk{sa: "203.0.113.10", da: "10.20.0.2", proto: protoUDP, sp: 53, dp: 5000}
	v4x := pk{sa: "10.20.0.3", da: "203.0.113.10", proto: protoUDP, sp: 5000, dp: 53}
	v6 := pk{sa: "fd00:20::7", da: "2001:db8::1", proto: protoTCP, sp: 5000, dp: 443}
	v6x := pk{sa: "fd00:21::7", da: "2001:db8::1", proto: protoTCP, sp: 5000, dp: 443}
	for name, c := range map[string]struct {
		p    pk
		want bool
	}{"v4 up": {v4, true}, "v4 down": {v4r, true}, "another v4 host": {v4x, false}, "v6 in the prefix": {v6, true}, "v6 outside": {v6x, false}} {
		if got := m.Match(c.p.build()); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestAZeroFilterSelectsEverythingAndHasAShortProgram(t *testing.T) {
	m := mustMatcher(t, LinkEthernet, Filter{})
	if !m.Match([]byte{1, 2, 3}) || !m.Match(tcpUp(80).build()) {
		t.Error("the zero filter does not select everything")
	}
	prog, err := m.BPF(262144)
	if err != nil {
		t.Fatal(err)
	}
	if !runBPF(t, prog, tcpUp(80).build()) {
		t.Error("the program of the zero filter drops a packet")
	}
	if len(prog) != 2 {
		t.Errorf("the program of the zero filter has %d instructions, want 2 (accept, reject)", len(prog))
	}
}

func TestTheProgramReturnsTheSnapLength(t *testing.T) {
	prog, err := mustMatcher(t, LinkEthernet, Filter{MACs: []string{macDev}}).BPF(96)
	if err != nil {
		t.Fatal(err)
	}
	vm, err := bpf.NewVM(prog)
	if err != nil {
		t.Fatal(err)
	}
	p := pk{eth: true, src: macDev, dst: macGW, sa: "10.0.0.1", da: "10.0.0.2", proto: protoUDP, sp: 1, dp: 2, payload: 400}
	n, err := vm.Run(p.build())
	if err != nil || n != 96 {
		t.Fatalf("the program returns %d, %v; want 96", n, err)
	}
}

func TestFilterValidationNamesTheProblem(t *testing.T) {
	for name, c := range map[string]struct {
		link LinkType
		f    Filter
		want string
	}{
		"a MAC on a raw interface": {LinkRaw, Filter{MACs: []string{macDev}}, "Ethernet address"},
		"a bad MAC":                {LinkEthernet, Filter{MACs: []string{"nonsense"}}, "not an Ethernet address"},
		"an unknown protocol":      {LinkEthernet, Filter{Protocol: "sctp"}, "protocol"},
		"ports without protocol":   {LinkEthernet, Filter{Ports: []PortRange{{1, 2}}}, "ports need"},
		"a reversed range":         {LinkEthernet, Filter{Protocol: TCP, Ports: []PortRange{{9, 3}}}, "port range"},
		"port zero":                {LinkEthernet, Filter{Protocol: UDP, Ports: []PortRange{{0, 3}}}, "port range"},
		"an unknown link type":     {LinkType(9), Filter{}, "link type"},
		"a mapped address":         {LinkRaw, Filter{Hosts: []netip.Prefix{netip.MustParsePrefix("::ffff:10.0.0.1/128")}}, "mapped"},
		"an invalid prefix":        {LinkRaw, Filter{Peers: []netip.Prefix{{}}}, "not valid"},
	} {
		_, err := c.f.Matcher(c.link)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error with %q", name, err, c.want)
		}
	}
}

func hex2(i int) string {
	const d = "0123456789abcdef"
	return string([]byte{d[i>>4&0xf], d[i&0xf]})
}

func TestAHugeListOfAddressesIsTooLargeForTheKernelAndStillMatches(t *testing.T) {
	var macs []string
	for i := 0; i < 600; i++ {
		macs = append(macs, "02:00:00:00:"+hex2(i>>8)+":"+hex2(i&0xff))
	}
	m := mustMatcher(t, LinkEthernet, Filter{MACs: macs})
	if _, err := m.BPF(65535); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a program for 600 addresses: %v, want ErrTooLarge", err)
	}
	p := tcpUp(80)
	p.src = macs[599]
	if !m.Match(p.build()) {
		t.Error("the user space filter does not select the last address")
	}
	// 200 addresses fit
	m = mustMatcher(t, LinkEthernet, Filter{MACs: macs[:200]})
	prog, err := m.BPF(65535)
	if err != nil {
		t.Fatal(err)
	}
	p.src = macs[199]
	if !runBPF(t, prog, p.build()) {
		t.Error("the program does not select the last of 200 addresses")
	}
}

// randomPacket builds a packet that sometimes fits the filters of the equivalence test, with the oddities a
// filter must treat the same everywhere: fragments, IP options, truncation, IPv6, other protocols.
func randomPacket(r *rand.Rand, eth bool) []byte {
	macs := []string{macDev, macOther, macGW}
	v4 := []string{"10.10.0.11", "10.10.0.12", "203.0.113.10", "198.51.100.7", "10.20.0.2"}
	v6 := []string{"fd00:20::7", "fd00:21::7", "2001:db8::1", "2001:db8:1::1"}
	ports := []uint16{53, 443, 8883, 8884, 40000, 1, 65535}
	protos := []uint8{protoTCP, protoTCP, protoUDP, protoUDP, protoICMP, 47}
	p := pk{eth: eth, src: macs[r.IntN(3)], dst: macs[r.IntN(3)], proto: protos[r.IntN(len(protos))],
		sp: ports[r.IntN(len(ports))], dp: ports[r.IntN(len(ports))], payload: r.IntN(40)}
	if r.IntN(4) == 0 {
		p.sa, p.da = v6[r.IntN(len(v6))], v6[r.IntN(len(v6))]
		if p.proto == protoICMP {
			p.proto = protoICM6
		}
	} else {
		p.sa, p.da = v4[r.IntN(len(v4))], v4[r.IntN(len(v4))]
		if r.IntN(8) == 0 {
			p.frag = uint16(1 + r.IntN(5))
		}
		if r.IntN(8) == 0 {
			p.options = 4 * (1 + r.IntN(4))
		}
	}
	b := p.build()
	if r.IntN(6) == 0 {
		b = b[:r.IntN(len(b)+1)]
	}
	return b
}

func TestTheKernelFilterSelectsExactlyWhatTheUserSpaceFilterSelects(t *testing.T) {
	filters := []struct {
		link LinkType
		f    Filter
	}{
		{LinkEthernet, Filter{}},
		{LinkEthernet, Filter{MACs: []string{macDev}}},
		{LinkEthernet, Filter{MACs: []string{macDev, macOther}, Protocol: TCP}},
		{LinkEthernet, Filter{MACs: []string{macDev}, Protocol: TCP, Ports: []PortRange{{8883, 8883}}}},
		{LinkEthernet, Filter{MACs: []string{macDev}, Protocol: UDP, Ports: []PortRange{{53, 53}, {40000, 65535}}, Peers: []netip.Prefix{pfx("203.0.113.0/24")}}},
		{LinkEthernet, Filter{Peers: []netip.Prefix{pfx("198.51.100.7"), pfx("2001:db8::/32")}}},
		{LinkEthernet, Filter{Protocol: ICMP}},
		{LinkEthernet, Filter{Hosts: []netip.Prefix{pfx("10.10.0.0/24")}, Protocol: TCP, Ports: []PortRange{{443, 443}, {8884, 8884}}}},
		{LinkEthernet, Filter{MACs: []string{macGW}, Hosts: []netip.Prefix{pfx("fd00:20::/64")}, Peers: []netip.Prefix{pfx("0.0.0.0/0")}}},
		{LinkEthernet, Filter{Hosts: []netip.Prefix{pfx("::/0")}}},
		{LinkEthernet, Filter{MACs: []string{macDev}, NotPeers: []netip.Prefix{pfx("10.0.0.0/8"), pfx("fd00::/8")}}},
		{LinkEthernet, Filter{Hosts: []netip.Prefix{pfx("10.10.0.0/24")}, Peers: []netip.Prefix{pfx("203.0.113.0/24"), pfx("198.51.100.0/24")}, NotPeers: []netip.Prefix{pfx("198.51.100.7")}, Protocol: TCP}},
		{LinkRaw, Filter{}},
		{LinkRaw, Filter{Hosts: []netip.Prefix{pfx("10.20.0.2")}, NotPeers: []netip.Prefix{pfx("203.0.113.0/24")}}},
		{LinkRaw, Filter{Hosts: []netip.Prefix{pfx("10.20.0.2"), pfx("fd00:20::/64")}}},
		{LinkRaw, Filter{Hosts: []netip.Prefix{pfx("10.10.0.0/16")}, Protocol: UDP, Ports: []PortRange{{53, 53}}}},
		{LinkRaw, Filter{Peers: []netip.Prefix{pfx("2001:db8:1::/48")}, Protocol: ICMP}},
	}
	r := rand.New(rand.NewPCG(7, 11))
	for i, c := range filters {
		m := mustMatcher(t, c.link, c.f)
		prog, err := m.BPF(65535)
		if err != nil {
			t.Fatalf("filter %d (%s): %v", i, c.f.String(), err)
		}
		selected := 0
		for n := 0; n < 4000; n++ {
			b := randomPacket(r, c.link == LinkEthernet)
			want, got := m.Match(b), runBPF(t, prog, b)
			if want != got {
				t.Fatalf("filter %d (%s): user space says %v, kernel filter says %v for %x", i, c.f.String(), want, got, b)
			}
			if want {
				selected++
			}
		}
		if selected == 0 {
			t.Errorf("filter %d (%s) selected none of 4000 packets: the test proves nothing", i, c.f.String())
		}
	}
}
