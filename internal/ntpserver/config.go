package ntpserver

import (
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the configuration of the server as it is used for a query: the faults of the API with their offsets and
// prefixes worked out once. The server never decides precedence (plan §2.4): the compiler has, per group of sources and
// per redirect port, already picked the one fault that decides (domain.TimeWinner), so a query is answered by the first
// entry whose sources contain the client and whose port is the port the query arrived on.

// parsedFault is a time fault ready to be applied.
type parsedFault struct {
	port    int
	sources []netip.Prefix
	overlay uuid.UUID
	// since is the instant the fault took effect: `absolute` runs on from it and a skew grows from it
	since time.Time

	offset   time.Duration
	absolute time.Time // the zero time: no absolute time
	skewPpm  float64
	leap     uint8

	// kind is how the fault answers; normal builds the answer from the fields above
	kind model.NtpQueryLogEntryAnswer
	kiss [4]byte
	// server is the server the device asked, where the fault's selector names exactly one address
	server string
}

type parsedNetwork struct {
	gateway [4]byte
	clients netip.Prefix // invalid: not known
}

type parsedConfig struct {
	generation int64
	faults     []parsedFault
	networks   []parsedNetwork
}

func parseSources(in []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out
}

func parseConfig(c *model.NtpServiceConfig) *parsedConfig {
	p := &parsedConfig{generation: c.Generation}
	for _, n := range c.Networks {
		pn := parsedNetwork{}
		if a, err := netip.ParseAddr(n.Gateway); err == nil && a.Is4() {
			pn.gateway = a.As4()
		}
		if n.Clients != nil {
			if cp, err := netip.ParsePrefix(*n.Clients); err == nil {
				pn.clients = cp.Masked()
			}
		}
		p.networks = append(p.networks, pn)
	}
	for _, f := range c.Faults {
		pf := parsedFault{port: f.Port, sources: parseSources(f.Sources), overlay: f.Overlay, since: f.Since, kind: model.NtpQueryLogEntryAnswerNormal}
		if f.Offset != nil {
			// the API validated the offset; one that does not parse is no offset
			pf.offset, _ = domain.ParseTimeOffset(*f.Offset)
		}
		if f.Absolute != nil {
			pf.absolute = *f.Absolute
		}
		if f.SkewPpm != nil {
			pf.skewPpm = float64(*f.SkewPpm)
		}
		if f.Leap != nil {
			switch *f.Leap {
			case model.NtpServiceConfigFaultsLeapInsert:
				pf.leap = LeapInsert
			case model.NtpServiceConfigFaultsLeapDelete:
				pf.leap = LeapDelete
			}
		}
		if f.Answer != nil {
			switch a := model.NtpQueryLogEntryAnswer(*f.Answer); a {
			case model.NtpQueryLogEntryAnswerUnsynchronized, model.NtpQueryLogEntryAnswerKissOfDeath, model.NtpQueryLogEntryAnswerSilence,
				model.NtpQueryLogEntryAnswerOriginMismatch, model.NtpQueryLogEntryAnswerZeroTransmit, model.NtpQueryLogEntryAnswerWrongMode:
				pf.kind = a
			}
		}
		if pf.kind == model.NtpQueryLogEntryAnswerKissOfDeath {
			code := "RATE"
			if f.KissCode != nil {
				code = string(*f.KissCode)
			}
			copy(pf.kiss[:], code)
		}
		pf.server = serverOf(f.Destination, f.Ports, f.PortRanges)
		p.faults = append(p.faults, pf)
	}
	return p
}

// serverOf names the server a fault's selector asks, where it is exactly one address: the port 123 or the one port the
// selector names. A prefix or a hostname does not name one server, and neither does a selector of several ports (the API
// fills in the server it saw for the flows it observes, best effort).
func serverOf(d *model.Destination, ports *[]int, ranges *[]model.PortRange) string {
	// a selector of several ports shares one redirect port: the port the device asked is not known
	if d == nil || d.Cidr == nil || (ports != nil && len(*ports) > 1) || (ranges != nil && len(*ranges) > 0) {
		return ""
	}
	addr, err := netip.ParseAddr(*d.Cidr)
	if err != nil {
		if p, perr := netip.ParsePrefix(*d.Cidr); perr == nil && p.Bits() == p.Addr().BitLen() {
			addr = p.Addr()
		} else {
			return ""
		}
	}
	port := 123
	if ports != nil && len(*ports) == 1 {
		port = (*ports)[0]
	}
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)).String()
}

// faultFor returns the fault that decides a query from client that arrived on port: of the entries of the port whose
// sources contain the client, the one with the longest matching prefix (the address of a device against the stretch of its
// network), the first among equals. The compiler lists the more specific group first as well; this does not depend on it.
func (p *parsedConfig) faultFor(client netip.Addr, port int) *parsedFault {
	var best *parsedFault
	bits := -1
	for i := range p.faults {
		f := &p.faults[i]
		if f.port != port {
			continue
		}
		for _, s := range f.sources {
			if s.Contains(client) && s.Bits() > bits {
				best, bits = f, s.Bits()
			}
		}
	}
	return best
}

// gatewayOf is the reference id of an answer: the gateway address of the client's network, which is where the client
// believes it got the time from. A network has an entry for its own range and one for each network behind it, so the entry
// with the longest prefix that contains the client decides (a network behind a router may lie inside a wider route of
// another). A client outside every network (it should not reach the server) gets the gateway of the only network, and zero when
// there are several networks or none.
func (p *parsedConfig) gatewayOf(client netip.Addr) [4]byte {
	best, bits := [4]byte{}, -1
	for _, n := range p.networks {
		if n.clients.IsValid() && n.clients.Contains(client) && n.clients.Bits() > bits {
			best, bits = n.gateway, n.clients.Bits()
		}
	}
	if bits >= 0 {
		return best
	}
	// the entries of one network share its gateway
	for _, n := range p.networks {
		if n.gateway != p.networks[0].gateway {
			return [4]byte{}
		}
	}
	if len(p.networks) > 0 {
		return p.networks[0].gateway
	}
	return [4]byte{}
}

// serve is the time the server shows when the host clock reads real: with `absolute` that instant plus the time since
// the fault took effect, else the host clock plus the offset, and in both cases the drift of the skew since then. The
// activation is a jump, and so is the removal: nothing here depends on an earlier answer.
func (f *parsedFault) serve(real time.Time) time.Time {
	if f == nil {
		return real
	}
	// a fault that took effect "in the future" (the host clock was stepped back) has not run yet
	elapsed := max(real.Sub(f.since), 0)
	var t time.Time
	if !f.absolute.IsZero() {
		t = f.absolute.Add(elapsed)
	} else {
		t = real.Add(f.offset)
	}
	if f.skewPpm != 0 {
		t = t.Add(time.Duration(float64(elapsed) * f.skewPpm * 1e-6))
	}
	// a time before the NTP epoch cannot be carried by the protocol (TimestampOf clamps it), and the offset logged is
	// that of the time the device gets
	if t.Unix()+ntpToUnix < 1 {
		t = time.Unix(-ntpToUnix+1, 0)
	}
	return t
}

// offsetMs is served minus real in milliseconds, exact across the whole range of years.
func offsetMs(served, real time.Time) int64 {
	return (served.Unix()-real.Unix())*1000 + int64(served.Nanosecond()-real.Nanosecond())/1_000_000
}
