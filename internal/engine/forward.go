package engine

import (
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ForwardFacts is what the status of the port forwards of cfg needs from the observed state (M35a): the
// devices' addresses, whether the host resolves the management and the uplink interface to the same
// interface (two-port topology), and whether each entry side has an IPv4 address now. The entry sides
// are judged only when the host is known (after the first observation). The addresses are returned
// too, by entry side, for explain.
func (e *Engine) ForwardFacts(cfg *model.Configuration) (domain.ForwardFacts, map[string]netip.Addr) {
	return forwardFacts(e.Snapshot(), cfg)
}

func forwardFacts(snap *Snapshot, cfg *model.Configuration) (domain.ForwardFacts, map[string]netip.Addr) {
	f := domain.ForwardFacts{Identity: snap.Identity}
	addrs := map[string]netip.Addr{}
	if cfg == nil || len(snap.Host.Links) == 0 {
		return f, addrs
	}
	f.Entry = map[string]bool{}
	up, upOK := snap.Host.Resolve(cfg.Uplink.Interface)
	mg, mgOK := snap.Host.Resolve(cfg.Management.Interface)
	f.TwoPort = upOK && mgOK && up.Name == mg.Name
	for side, l := range map[string]struct {
		ok   bool
		link func() (netip.Prefix, bool)
	}{
		domain.EntryUplink:     {upOK, up.FirstV4},
		domain.EntryManagement: {mgOK, mg.FirstV4},
	} {
		if !l.ok {
			continue
		}
		if p, ok := l.link(); ok {
			f.Entry[side] = true
			addrs[side] = p.Addr()
		}
	}
	return f, addrs
}

// DefaultUIPort is the port the API listens on: the UI/API port when the configuration names none, which
// a port forward must not take (M35a). Without one the compiler's default stands in, as it does there.
func (e *Engine) DefaultUIPort() int {
	if e.cfg.DefaultUIPort > 0 {
		return e.cfg.DefaultUIPort
	}
	return compiler.DefaultUIPort
}
