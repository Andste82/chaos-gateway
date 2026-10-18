package capture

import (
	"encoding/binary"
	"net/netip"

	"golang.org/x/net/bpf"
)

// MaxBPFInstructions is the kernel's limit for the length of a classic BPF program (BPF_MAXINSNS).
const MaxBPFInstructions = 4096

// BPF compiles the filter to a classic BPF program for the AF_PACKET socket of the interface (SO_ATTACH_FILTER).
// The program returns snaplen for a packet it selects (the kernel cuts the packet to that length) and 0 for
// any other. It selects exactly what Match selects; a filter whose program would not fit the kernel's limit
// returns ErrTooLarge, and the capture then filters in user space.
func (m *Matcher) BPF(snaplen int) ([]bpf.Instruction, error) {
	g := &gen{m: m}
	accept, reject := g.newLabel(), g.newLabel()
	if !m.f.matchesAll() {
		if m.link == LinkEthernet {
			// a frame shorter than its header is no packet (Match parses nothing of it)
			g.add(bpf.LoadExtension{Num: bpf.ExtLen})
			g.cond(bpf.JumpLessThan, 14, reject, fall)
		}
		rev := g.newLabel()
		g.direction(true, accept, rev, reject)
		g.bind(rev)
		g.direction(false, accept, reject, reject)
	}
	g.bind(accept)
	g.add(bpf.RetConstant{Val: uint32(snaplen)})
	g.bind(reject)
	g.add(bpf.RetConstant{Val: 0})
	prog := g.resolve()
	if len(prog) > MaxBPFInstructions {
		return nil, ErrTooLarge
	}
	return prog, nil
}

// gen assembles a program with labels. Conditions are three instructions (the test and a long jump for each
// outcome, because the offsets of a conditional jump are 8 bits wide and a list of addresses is long); jumps
// to the next instruction are dropped when the program is resolved.
type gen struct {
	m      *Matcher
	items  []item
	labels []int // label → index into items, -1 while unbound
}

type item struct {
	ins bpf.Instruction
	to  int // label of a jump, else -1
}

func (g *gen) newLabel() int {
	g.labels = append(g.labels, -1)
	return len(g.labels) - 1
}

func (g *gen) bind(l int) { g.labels[l] = len(g.items) }

func (g *gen) add(ins bpf.Instruction) { g.items = append(g.items, item{ins: ins, to: -1}) }

func (g *gen) jump(l int) { g.items = append(g.items, item{ins: bpf.Jump{}, to: l}) }

// fall as the target of a condition means the instruction that follows it.
const fall = -1

// cond adds a test of A against val that continues at t or f (a label, or fall). A jump to the instruction
// that follows costs nothing (resolve).
func (g *gen) cond(test bpf.JumpTest, val uint32, t, f int) {
	after := g.newLabel()
	if t == fall {
		t = after
	}
	if f == fall {
		f = after
	}
	g.add(bpf.JumpIf{Cond: test, Val: val, SkipTrue: 0, SkipFalse: 1})
	g.jump(t)
	g.jump(f)
	g.bind(after)
}

// resolve drops the jumps to the next instruction and fills in the offsets of the others.
func (g *gen) resolve() []bpf.Instruction {
	for {
		drop := -1
		for i, it := range g.items {
			if it.to >= 0 && g.labels[it.to] == i+1 {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		g.items = append(g.items[:drop], g.items[drop+1:]...)
		for l, pos := range g.labels {
			if pos > drop {
				g.labels[l] = pos - 1
			}
		}
	}
	out := make([]bpf.Instruction, len(g.items))
	for i, it := range g.items {
		out[i] = it.ins
		if it.to >= 0 {
			out[i] = bpf.Jump{Skip: uint32(g.labels[it.to] - (i + 1))}
		}
	}
	return out
}

// base is the offset of the IP header.
func (g *gen) base() uint32 {
	if g.m.link == LinkEthernet {
		return 14
	}
	return 0
}

// direction adds the test of one direction: forward means the source is the local side. Success continues at
// accept, a failure that only concerns this direction at fail, and one that concerns both (the protocol) at reject.
func (g *gen) direction(forward bool, accept, fail, reject int) {
	f := &g.m.f
	peer, proto, ports := g.newLabel(), g.newLabel(), g.newLabel()
	g.local(forward, peer, fail)
	g.bind(peer)
	g.peer(forward, proto, fail)
	g.bind(proto)
	g.protocol(ports, reject)
	g.bind(ports)
	if len(f.Ports) == 0 {
		g.jump(accept)
		return
	}
	g.portTest(forward, accept, fail)
}

// local adds the test that the local side is the source (forward) or the destination.
func (g *gen) local(forward bool, ok, fail int) {
	f := &g.m.f
	if !f.hasLocal() {
		g.jump(ok)
		return
	}
	for _, mac := range g.m.macs {
		next := g.newLabel()
		off := uint32(0) // the destination
		if forward {
			off = 6
		}
		g.add(bpf.LoadAbsolute{Off: off, Size: 4})
		g.cond(bpf.JumpEqual, binary.BigEndian.Uint32(mac[0:4]), fall, next)
		g.add(bpf.LoadAbsolute{Off: off + 4, Size: 2})
		g.cond(bpf.JumpEqual, uint32(binary.BigEndian.Uint16(mac[4:6])), ok, next)
		g.bind(next)
	}
	g.prefixes(f.Hosts, forward, ok, fail)
}

// peer adds the test that the peer side is the destination (forward) or the source.
func (g *gen) peer(forward bool, ok, fail int) {
	f := &g.m.f
	if len(f.Peers) == 0 && len(f.NotPeers) == 0 {
		g.jump(ok)
		return
	}
	if len(f.NotPeers) > 0 {
		// an address in one of them is no peer; a packet that is no IP packet is none either
		cont := g.newLabel()
		g.prefixes(f.NotPeers, !forward, fail, cont)
		g.bind(cont)
		if len(f.Peers) == 0 {
			g.isAnyIP(ok, fail)
			return
		}
	}
	g.prefixes(f.Peers, !forward, ok, fail)
}

// isAnyIP adds the test that the packet is a whole-headered IPv4 or IPv6 packet.
func (g *gen) isAnyIP(ok, fail int) {
	b := g.base()
	v6 := g.newLabel()
	g.isIP(4, v6)
	g.add(bpf.LoadExtension{Num: bpf.ExtLen})
	g.cond(bpf.JumpLessThan, b+20, fail, ok)
	g.bind(v6)
	g.isIP(6, fail)
	g.add(bpf.LoadExtension{Num: bpf.ExtLen})
	g.cond(bpf.JumpLessThan, b+40, fail, ok)
}

// prefixes adds the test that the source (src) or the destination of an IP packet is in one of the prefixes.
func (g *gen) prefixes(ps []netip.Prefix, src bool, ok, fail int) {
	b := g.base()
	for _, p := range ps {
		next := g.newLabel()
		a := p.Masked().Addr()
		if a.Is4() {
			g.isIP(4, next)
			off := b + 16 // the destination
			if src {
				off = b + 12
			}
			g.add(bpf.LoadExtension{Num: bpf.ExtLen})
			g.cond(bpf.JumpLessThan, b+20, next, fall)
			g.add(bpf.LoadAbsolute{Off: off, Size: 4})
			bits := p.Bits()
			if bits < 32 {
				mask := ^uint32(0) << (32 - bits)
				g.add(bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: mask})
			}
			a4 := a.As4()
			g.cond(bpf.JumpEqual, binary.BigEndian.Uint32(a4[:]), ok, next)
		} else {
			g.isIP(6, next)
			off := b + 24 // the destination
			if src {
				off = b + 8
			}
			g.add(bpf.LoadExtension{Num: bpf.ExtLen})
			g.cond(bpf.JumpLessThan, b+40, next, fall)
			a16 := a.As16()
			bits := p.Bits()
			for w := 0; w < 4 && w*32 < bits; w++ {
				g.add(bpf.LoadAbsolute{Off: off + uint32(4*w), Size: 4})
				if rest := bits - w*32; rest < 32 {
					g.add(bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: ^uint32(0) << (32 - rest)})
				}
				last := (w+1)*32 >= bits
				t := next
				if last {
					g.cond(bpf.JumpEqual, binary.BigEndian.Uint32(a16[4*w:4*w+4]), ok, t)
				} else {
					g.cond(bpf.JumpEqual, binary.BigEndian.Uint32(a16[4*w:4*w+4]), fall, t)
				}
			}
			if bits == 0 {
				g.jump(ok)
			}
		}
		g.bind(next)
	}
	g.jump(fail)
}

// isIP adds the test that the packet is IP version v (the Ethernet type, or the version nibble of a raw
// packet); a packet of another kind continues at fail.
func (g *gen) isIP(v int, fail int) {
	if g.m.link == LinkEthernet {
		et := uint32(etherIPv4)
		if v == 6 {
			et = etherIPv6
		}
		g.add(bpf.LoadAbsolute{Off: 12, Size: 2})
		g.cond(bpf.JumpEqual, et, fall, fail)
		return
	}
	g.add(bpf.LoadAbsolute{Off: 0, Size: 1})
	g.add(bpf.ALUOpConstant{Op: bpf.ALUOpShiftRight, Val: 4})
	g.cond(bpf.JumpEqual, uint32(v), fall, fail)
}

// protocol adds the test of the protocol; the test fails for both directions at reject.
func (g *gen) protocol(ok, reject int) {
	p := g.m.f.Protocol
	if p == AnyProtocol {
		g.jump(ok)
		return
	}
	b := g.base()
	var v4, v6 uint32
	switch p {
	case TCP:
		v4, v6 = protoTCP, protoTCP
	case UDP:
		v4, v6 = protoUDP, protoUDP
	case ICMP:
		v4, v6 = protoICMP, protoICM6
	}
	v6l := g.newLabel()
	g.isIP(4, v6l)
	g.add(bpf.LoadExtension{Num: bpf.ExtLen})
	g.cond(bpf.JumpLessThan, b+20, reject, fall)
	g.add(bpf.LoadAbsolute{Off: b + 9, Size: 1})
	g.cond(bpf.JumpEqual, v4, ok, reject)
	g.bind(v6l)
	g.isIP(6, reject)
	g.add(bpf.LoadExtension{Num: bpf.ExtLen})
	g.cond(bpf.JumpLessThan, b+40, reject, fall)
	g.add(bpf.LoadAbsolute{Off: b + 6, Size: 1})
	g.cond(bpf.JumpEqual, v6, ok, reject)
}

// portTest adds the test of the peer's port: the destination port (forward) or the source port. A transport
// header that is not there (a fragment that is not the first, a packet that is too short) fails the
// test.
func (g *gen) portTest(forward bool, ok, fail int) {
	b := g.base()
	off := uint32(2) // the destination port
	if !forward {
		off = 0
	}
	v6l, load := g.newLabel(), g.newLabel()
	g.isIP(4, v6l)
	// only the first fragment has the transport header
	g.add(bpf.LoadAbsolute{Off: b + 6, Size: 2})
	g.cond(bpf.JumpBitsSet, 0x1fff, fail, fall)
	g.add(bpf.LoadMemShift{Off: b})
	g.add(bpf.LoadIndirect{Off: b + 2, Size: 2}) // the whole of both ports must be there, as Match demands
	g.add(bpf.LoadIndirect{Off: b + off, Size: 2})
	g.jump(load)
	g.bind(v6l)
	g.isIP(6, fail)
	g.add(bpf.LoadAbsolute{Off: b + 40 + 2, Size: 2})
	g.add(bpf.LoadAbsolute{Off: b + 40 + off, Size: 2})
	g.bind(load)
	for _, r := range g.m.f.Ports {
		next := g.newLabel()
		if r.From == r.To {
			g.cond(bpf.JumpEqual, uint32(r.From), ok, next)
		} else {
			g.cond(bpf.JumpLessThan, uint32(r.From), next, fall)
			g.cond(bpf.JumpGreaterThan, uint32(r.To), next, ok)
		}
		g.bind(next)
	}
	g.jump(fail)
}
