package engine

import (
	"errors"
	"net/netip"
	"strings"
	"sync"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is what the TLS responder asks of the engine (plan §2.8, M21): the case that decides a connection it
// holds. The kernel redirects the connections that some case selects by source, destination and port; which of the
// cases decides depends on what only the connection knows, the server name of its ClientHello, so the responder
// reads it and asks. The decision is the one of the precedence rules of §2.4 (domain.World.Resolve), the same one
// `explain` shows.

// TLSConnection is a connection the responder holds: the client's address, the original destination (the address
// and port the device meant, read with SO_ORIGINAL_DST) and the server name of its ClientHello, empty without one.
type TLSConnection struct {
	Client netip.Addr
	Dst    netip.AddrPort
	SNI    string
	// Names are the names the gateway's DNS answered the client for the destination address.
	Names []string
}

// TLSDecision is what the engine says of a connection: the case that decides it, nil when no case does (the
// connection is not the responder's: the selection of the kernel was wider than the case, or the case went since)
// with the device it belongs to and whether the device's firmware trusts the test CA (plan §2.3).
type TLSDecision struct {
	Case *domain.Candidate
	// Device is the device's UUID, empty for an address no device owns; DeviceName its name.
	Device, DeviceName string
	TrustsTestCA       bool
	// Passthrough is meaningful without a case and with the case `intercept` (M22): whether the gateway would have forwarded the
	// connection if the redirect had not taken it, judged by the access rules, the gateway's protection and the access matrix on
	// the destination the device meant (the same judgment `explain` makes). The responder passes a connection on only then: its
	// own connection to the destination (or the proxy's) leaves the namespace by the gateway's service path, which the matrix
	// does not see, so a connection the matrix would have dropped must not get through it.
	Passthrough bool
}

// ErrNoTLSWorld is returned by ResolveTLS before a configuration is applied.
var ErrNoTLSWorld = errors.New("engine: no configuration to resolve TLS cases with")

// tlsWorld caches the world of one snapshot: a connection asks for a decision each, and the world of a big
// configuration is not cheap.
type tlsWorld struct {
	mu    sync.Mutex
	snap  *Snapshot
	world *domain.World
}

func (c *tlsWorld) of(snap *Snapshot) (*domain.World, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap == snap && c.world != nil {
		return c.world, nil
	}
	w, err := domain.NewWorld(snap.Config, snap.Overlays)
	if err != nil {
		return nil, err
	}
	c.snap, c.world = snap, w
	return w, nil
}

// ResolveTLS decides a connection of the responder.
func (e *Engine) ResolveTLS(c TLSConnection) (TLSDecision, error) {
	snap := e.Snapshot()
	if snap == nil || snap.Config == nil {
		return TLSDecision{}, ErrNoTLSWorld
	}
	world, err := e.tlsWorld.of(snap)
	if err != nil {
		return TLSDecision{}, err
	}
	sub := domain.Subject{IP: c.Client}
	var dec TLSDecision
	if id, ok := snap.Identity.OwnerOf(c.Client); ok {
		sub.Device = strings.ToLower(id)
		dec.Device = sub.Device
		dec.DeviceName = sub.Device
		if d, ok := world.Index.Devices[sub.Device]; ok {
			dec.DeviceName = d.Name
		}
		for i := range snap.Devices {
			if strings.EqualFold(snap.Devices[i].ID, sub.Device) {
				dec.DeviceName = snap.Devices[i].Name
				sub.Network = strings.ToLower(snap.Devices[i].Network)
			}
		}
		if d, ok := deref(snap.Config.Devices)[sub.Device]; ok && d.TrustsTestCa != nil {
			dec.TrustsTestCA = *d.TrustsTestCa
		}
	}
	if sub.Network == "" {
		if n, ok := world.NetworkOf(c.Client); ok {
			sub.Network = n
		}
	}
	q := domain.Query{Source: sub, DestIP: c.Dst.Addr(), DestNames: c.Names, Protocol: "tcp", Port: int(c.Dst.Port()), SNI: strings.ToLower(c.SNI)}
	dec.Case = domain.Winner(world.Resolve(q), domain.FamilyTLS)
	if (dec.Case == nil || (dec.Case.TLS != nil && dec.Case.TLS.Case == model.TlsCaseCaseIntercept)) && sub.IP.IsValid() {
		dec.Passthrough = world.AccessDecision(q, e.accessFacts(snap)).Verdict == "allow"
	}
	return dec, nil
}
