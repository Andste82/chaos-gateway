package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// observeTimeout bounds one reading.
const observeTimeout = 10 * time.Second

// PollObserved reads what the gateway sees (the neighbor table, the connections, the DHCP leases)
// every interval and whenever TriggerObserve asks for it, and hands it to the state owner, which works
// out identity and device state and emits the device events (plan §2.3). It runs until ctx or the
// engine ends.
func (e *Engine) PollObserved(ctx context.Context, interval time.Duration) error {
	if !e.started {
		return fmt.Errorf("engine: not started")
	}
	if !e.pollingObserved.CompareAndSwap(false, true) {
		return fmt.Errorf("engine: the observed state is polled already")
	}
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	e.sup.Go(ctx, "engine.poll-observed", func(ctx context.Context) error {
		tick := e.cfg.Clock.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C():
			case <-e.observeNow:
				// a burst of triggers is one reading: wait a moment for the rest of the burst
				select {
				case <-ctx.Done():
					return nil
				case <-e.cfg.Clock.After(debounce):
				}
				for drained := false; !drained; {
					select {
					case <-e.observeNow:
					default:
						drained = true
					}
				}
			}
			if err := e.readObserved(ctx); err != nil && ctx.Err() == nil {
				e.cfg.Log.Warn("cannot read the observed state", "error", err)
			}
		}
	})
	return nil
}

// debounce is how long a trigger waits for further ones.
const debounce = 100 * time.Millisecond

// TriggerObserve asks the poller to read now. Many calls in a short time cost one reading.
func (e *Engine) TriggerObserve() {
	select {
	case e.observeNow <- struct{}{}:
	default:
	}
}

// ObserveNow reads and hands the observation to the state owner, returning when it has taken it
// (tests; the poller does the same on its own).
func (e *Engine) ObserveNow(ctx context.Context) error { return e.readObservedWait(ctx, true) }

func (e *Engine) readObserved(ctx context.Context) error { return e.readObservedWait(ctx, false) }

func (e *Engine) readObservedWait(ctx context.Context, wait bool) error {
	ctx, cancel := context.WithTimeout(ctx, observeTimeout)
	defer cancel()
	snap := e.Snapshot()
	obs := observation{At: e.cfg.Clock.Now(), Active: map[netip.Addr]bool{}, Peers: snap.WireGuard}
	ns := executor.Target{NS: e.cfg.Namespace}

	out, err := e.cfg.Exec.Do(ctx, &executor.Read{Target: ns, What: executor.ReadNeighbors})
	if err != nil {
		return err
	}
	var neigh []linux.Neighbor
	if len(out.Data) > 0 {
		if err := json.Unmarshal(out.Data[0], &neigh); err != nil {
			return err
		}
	}
	nets := bridgeNetworks(snap.Bridges)
	for _, n := range neigh {
		nid, isTest := nets[n.Dev]
		if !isTest || !n.Usable() {
			continue
		}
		ip, err := netip.ParseAddr(n.Dst)
		if err != nil {
			continue
		}
		obs.Neighbors = append(obs.Neighbors, domain.Neighbor{IP: ip, MAC: n.LLAddr, Interface: n.Dev, Network: nid, Stale: n.Stale()})
	}
	// the connections: addresses that are in use, and hosts behind tunnels
	if cout, err := e.cfg.Exec.Do(ctx, &executor.Read{Target: ns, What: executor.ReadConntrack}); err == nil && len(cout.Data) > 0 {
		var flows []linux.Conntrack
		if json.Unmarshal(cout.Data[0], &flows) == nil {
			behind := tunnelPrefixes(snap)
			seen := map[netip.Addr]bool{}
			obs.Traffic = map[netip.Addr]AddrTraffic{}
			for _, f := range flows {
				ip, err := netip.ParseAddr(f.Original.Src)
				if err != nil {
					continue
				}
				obs.Active[ip] = true
				if !seen[ip] && inAny(behind, ip) {
					seen[ip] = true
					obs.UnknownSources = append(obs.UnknownSources, ip)
				}
				up := Traffic{f.Original.Packets, f.Original.Bytes}
				down := Traffic{f.Reply.Packets, f.Reply.Bytes}
				addTraffic(obs.Traffic, ip, up, down)
				if dst, err := netip.ParseAddr(f.Original.Dst); err == nil {
					addTraffic(obs.Traffic, dst, down, up)
				}
			}
			sort.Slice(obs.UnknownSources, func(i, j int) bool { return obs.UnknownSources[i].Less(obs.UnknownSources[j]) })
		}
		e.setActive(obs.Active)
	} else if err != nil {
		// a failed read is not "no connections": the addresses that were in use stay in use
		e.logObserveError(err)
		obs.ObserveError = err.Error()
		obs.Active = e.lastActive()
	}
	if e.cfg.DHCP != nil {
		if ls, err := e.cfg.DHCP.Leases(ctx); err == nil {
			obs.Leases = leasesOf(ls, snap.KeaNetworks, obs.At)
		} else {
			e.cfg.Log.Debug("cannot read the DHCP leases", "error", err)
		}
	}
	var reply chan struct{}
	if wait {
		reply = make(chan struct{}, 1)
	}
	if err := e.send(ctx, cmdObserved{obs: obs, reply: reply}); err != nil {
		return err
	}
	if wait {
		_, err := wait2(ctx, e, reply)
		return err
	}
	return nil
}

func wait2[T any](ctx context.Context, e *Engine, ch <-chan T) (T, error) { return wait(ctx, e, ch) }

func (e *Engine) setActive(a map[netip.Addr]bool) {
	e.activeMu.Lock()
	e.active = a
	e.activeMu.Unlock()
}

func (e *Engine) lastActive() map[netip.Addr]bool {
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	out := make(map[netip.Addr]bool, len(e.active))
	for a := range e.active {
		out[a] = true
	}
	return out
}

// bridgeNetworks maps the bridge of each local test network to its network.
func bridgeNetworks(bridges []compiler.Bridge) map[string]string {
	m := map[string]string{}
	for _, b := range bridges {
		m[b.Name] = b.NetworkID
	}
	return m
}

// tunnelPrefixes are the networks behind WireGuard peers: the networks of clients and the routes of
// links. A source address in one of them is a host behind a tunnel.
func tunnelPrefixes(s *Snapshot) []netip.Prefix {
	var out []netip.Prefix
	for _, w := range s.WireGuardInterfaces {
		for _, p := range w.Peers {
			for _, r := range p.Routes {
				if pf, err := netip.ParsePrefix(r); err == nil && pf.Bits() < 32 {
					out = append(out, pf)
				}
			}
		}
	}
	return out
}

// addTraffic adds one flow's share of the traffic to the given address's running total: up is what
// the address sent on this flow, down is what it received.
func addTraffic(m map[netip.Addr]AddrTraffic, addr netip.Addr, up, down Traffic) {
	t := m[addr]
	t.Upload.Packets += up.Packets
	t.Upload.Bytes += up.Bytes
	t.Download.Packets += down.Packets
	t.Download.Bytes += down.Bytes
	t.Flows++
	m[addr] = t
}

// wireGuardNetworkOf finds the WireGuard network an address belongs to: the interface's own subnet
// (a hub's clients), or a peer's routed prefixes (a client network, a link's static routes).
func wireGuardNetworkOf(ifaces []compiler.WGInterface, addr netip.Addr) string {
	for _, w := range ifaces {
		if w.Address.Masked().Contains(addr) {
			return w.NetworkID
		}
		for _, p := range w.Peers {
			for _, r := range p.Routes {
				if pf, err := netip.ParsePrefix(r); err == nil && pf.Contains(addr) {
					return w.NetworkID
				}
			}
		}
	}
	return ""
}

func inAny(ps []netip.Prefix, ip netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// LeaseEvent takes a lease event from the DHCP server's hook (plan §2.7): it becomes an event of
// the stream and makes the poller read at once, so the device and its address appear within a moment.
func (e *Engine) LeaseEvent(ev kea.Event) {
	data := map[string]any{"event": ev.Name, "ip": ev.IP.String(), "mac": ev.MAC}
	if ev.Hostname != "" {
		data["hostname"] = ev.Hostname
	}
	if nid, ok := e.Snapshot().KeaNetworks[ev.SubnetID]; ok {
		data["network"] = nid
	}
	if ev.ValidLifetime > 0 {
		data["lease_time"] = ev.ValidLifetime
	}
	e.Emit(EventDHCPLease, data)
	e.TriggerObserve()
}

// Flow is a tracked connection with the device it belongs to.
type Flow struct {
	ID       string
	Device   string
	Network  string
	Protocol string
	Src, Dst netip.Addr
	// NatSrc is the address the connection has after NAT, when it differs from Src.
	NatSrc       netip.Addr
	SPort, DPort int
	State        string
	Upload       Traffic
	Download     Traffic
	// Service is set when the flow is redirected into the service namespace (plan §3.3).
	Service string
}

// Traffic is the volume of one direction of a flow.
type Traffic struct{ Packets, Bytes int64 }

// Flows returns the tracked connections of the test networks, each with its device (plan §2.3).
func (e *Engine) Flows(ctx context.Context) ([]Flow, error) {
	ctx, cancel := context.WithTimeout(ctx, observeTimeout)
	defer cancel()
	out, err := e.cfg.Exec.Do(ctx, &executor.Read{Target: executor.Target{NS: e.cfg.Namespace}, What: executor.ReadConntrack})
	if err != nil {
		return nil, err
	}
	var raw []linux.Conntrack
	if len(out.Data) > 0 {
		if err := json.Unmarshal(out.Data[0], &raw); err != nil {
			return nil, err
		}
	}
	snap := e.Snapshot()
	var flows []Flow
	for _, c := range raw {
		src, err1 := netip.ParseAddr(c.Original.Src)
		dst, err2 := netip.ParseAddr(c.Original.Dst)
		if err1 != nil || err2 != nil {
			continue
		}
		f := Flow{Protocol: c.Proto, Src: src, Dst: dst, SPort: c.Original.SPort, DPort: c.Original.DPort, State: flowState(c),
			Upload: Traffic{c.Original.Packets, c.Original.Bytes}, Download: Traffic{c.Reply.Packets, c.Reply.Bytes}}
		if f.Protocol != "tcp" && f.Protocol != "udp" && f.Protocol != "icmp" {
			f.Protocol = "other"
		}
		if r, err := netip.ParseAddr(c.Reply.Dst); err == nil && r != src {
			f.NatSrc = r
		}
		if dev, ok := snap.Identity.OwnerOf(src); ok {
			f.Device = dev
		}
		for _, b := range snap.Bridges {
			if b.Address.Masked().Contains(src) {
				f.Network = b.NetworkID
			}
		}
		if f.Network == "" {
			f.Network = wireGuardNetworkOf(snap.WireGuardInterfaces, src)
		}
		if r, err := netip.ParseAddr(c.Reply.Src); err == nil && r == compiler.ServicePeerCIDR.Addr() {
			f.Service = "dns_proxy"
		}
		if f.Device == "" && f.Network == "" {
			continue // not a flow of a test network
		}
		f.ID = flowID(c)
		flows = append(flows, f)
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })
	return flows, nil
}

func flowState(c linux.Conntrack) string {
	if c.State != "" {
		return c.State
	}
	for _, f := range c.Flags {
		if f == "ASSURED" || f == "UNREPLIED" {
			return f
		}
	}
	return ""
}

func flowID(c linux.Conntrack) string {
	if c.Proto == "icmp" {
		// icmp has no ports: concurrent pings would otherwise share "icmp/src>dst" and collide in the
		// flow list's cursor paging (M6a-13). The echo id (and type/code, for completeness) tells them
		// apart; -1 marks a field conntrack did not report, so it still differs from a real 0.
		return fmt.Sprintf("%s/%s>%s/%d.%d.%d", c.Proto, c.Original.Src, c.Original.Dst,
			intOr(c.Original.ICMPType, -1), intOr(c.Original.ICMPCode, -1), intOr(c.Original.ICMPID, -1))
	}
	return c.Proto + "/" + c.Original.Src + ":" + strconv.Itoa(c.Original.SPort) + ">" + c.Original.Dst + ":" + strconv.Itoa(c.Original.DPort)
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
