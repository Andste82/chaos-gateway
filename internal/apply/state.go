package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Exec is the executor as the apply package uses it: a request of operations, answered in one
// outcome. *executor.Client and *executor.Executor (through Local) both fit.
type Exec interface {
	Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error)
}

// Watcher is implemented by an Exec that can also stream events (M6a-04): *executor.Client,
// *executor.Redialing and Local all fit; the engine falls back to polling alone for an Exec that
// is not a Watcher.
type Watcher interface {
	Watch(ctx context.Context, what, ns string) (<-chan json.RawMessage, func(), error)
}

// State is the kernel state relevant to Chaos Gateway, read through the executor.
type State struct {
	// Generation is the executor's generation when the state was read.
	Generation uint64
	Assigned   []string
	Links      map[string]linux.Link
	Addrs      map[string][]linux.Address
	Rules      []linux.Rule
	Routes     []linux.Route
	Nft        *linux.Ruleset
	Offloads   map[string]linux.Features
	// Sysctl holds "ip_forward" and "accept_ra:<dev>" for the interfaces that exist.
	Sysctl     map[string]int
	DockerUser linux.DockerUserState
	// WireGuard holds the interfaces of kind wireguard with their peers (no secret).
	WireGuard map[string]*linux.WGInfo
	// Bird is the BIRD instance; nil when the executor has no BIRD directory.
	Bird *executor.BirdState
	// Service is the service namespace; nil when none was asked for.
	Service *ServiceState
	// TC is the tc state of every interface that holds a qdisc tree of Chaos Gateway's own (a root
	// qdisc with the handle 1:), whole and with counters; an interface without one is not in the map.
	TC map[string]*linux.NormTree
	// TCRetiring are the classes (and trees) that the target no longer wants but that still stand
	// because the make-before-break deletion has not come yet; Verify accepts them (Apply sets it).
	TCRetiring map[string]bool
}

// ServiceState is the service namespace as it is now.
type ServiceState struct {
	// Exists is false when the namespace is missing (its holder died, or it was never created).
	Exists bool
	// PeerAddrs are the IPv4 addresses of the peer interface ("ip/len"), PeerUp its state.
	PeerAddrs []string
	PeerUp    bool
	// DefaultVia is the next hop of the namespace's default route.
	DefaultVia string
	// HolderMatches is false when the namespace is not the one of the holder process that was asked
	// for: the holder container restarted and left the old namespace behind.
	HolderMatches bool
}

// Want names what to read besides the basics: sysctls and offloads exist per interface.
type Want struct {
	Sysctls  []executor.SysctlEntry
	Offloads []string
	// TCDevs are the interfaces the target puts its tc tree on (compiler.Target.TCCandidates). The tc
	// state is read for them and for every other assigned interface that holds a tree of Chaos
	// Gateway's own, so a tree that is no longer wanted is found.
	TCDevs []string
	// BirdInstance is the instance to read; empty reads none.
	BirdInstance string
	// ServiceNS is the service namespace to read and ServicePeerIf the interface inside it; empty
	// reads none. ServiceHolderPID is the holder the namespace should belong to (0: any).
	ServiceNS, ServicePeerIf string
	ServiceHolderPID         int
}

func read(ns, what, dev string) *executor.Read {
	return &executor.Read{Target: executor.Target{NS: ns}, What: what, Dev: dev}
}

func decode[T any](out executor.Outcome, i int, what string) (T, error) {
	var v T
	if i >= len(out.Data) {
		return v, fmt.Errorf("read %s: no data", what)
	}
	if err := json.Unmarshal(out.Data[i], &v); err != nil {
		return v, fmt.Errorf("read %s: %w", what, err)
	}
	return v, nil
}

// ReadRouteGet asks the kernel which route a packet takes: the destination dst, from the source
// address src when it is not empty, arriving on interface iif when that is not empty (`ip route
// get dst from src iif iif`: the policy rules and the tables they select decide, as they do for
// forwarded traffic). A destination the kernel has no route for is an answer, not an error
// (RouteGet.Unreachable). One executor round trip.
func ReadRouteGet(ctx context.Context, ex Exec, ns, dst, src, iif string) (*linux.RouteGet, error) {
	out, err := ex.Do(ctx, &executor.Read{Target: executor.Target{NS: ns}, What: executor.ReadRouteGet, Dst: dst, Src: src, Dev: iif})
	if err != nil {
		return nil, fmt.Errorf("look up the route to %s: %w", dst, err)
	}
	return decode[*linux.RouteGet](out, 0, "route get")
}

// ReadSets reads only the kernel's nft table (one executor round trip), for checking device set
// elements after an identity-only update (M6a-11): the rest of the state is not expected to have
// changed, and reading it too would cost several more round trips for nothing.
func ReadSets(ctx context.Context, ex Exec, ns string) (*linux.Ruleset, error) {
	out, err := ex.Do(ctx, read(ns, executor.ReadNft, ""))
	if err != nil {
		return nil, fmt.Errorf("read the kernel's nft sets: %w", err)
	}
	rs, err := decode[*linux.Ruleset](out, 0, "nft")
	if err != nil {
		return nil, err
	}
	if rs == nil {
		rs = &linux.Ruleset{}
	}
	return rs, nil
}

// ReadState reads the state in the namespace ns ("" for the executor's own).
func ReadState(ctx context.Context, ex Exec, ns string, want Want) (*State, error) {
	out, err := ex.Do(ctx,
		read(ns, executor.ReadAssigned, ""),
		read(ns, executor.ReadLinks, ""),
		read(ns, executor.ReadAddrs, ""),
		read(ns, executor.ReadRules, ""),
		read(ns, executor.ReadRoutes, ""),
		read(ns, executor.ReadNft, ""),
		&executor.Read{Target: executor.Target{NS: ns}, What: executor.ReadDockerUser},
	)
	if err != nil {
		return nil, fmt.Errorf("read the kernel state: %w", err)
	}
	s := &State{Generation: out.Generation, Links: map[string]linux.Link{}, Addrs: map[string][]linux.Address{},
		Offloads: map[string]linux.Features{}, Sysctl: map[string]int{}}
	if s.Assigned, err = decode[[]string](out, 0, "assigned"); err != nil {
		return nil, err
	}
	links, err := decode[[]linux.Link](out, 1, "links")
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		s.Links[l.Name] = l
	}
	addrs, err := decode[[]linux.Addrs](out, 2, "addrs")
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		s.Addrs[a.Name] = a.Addrs
	}
	if s.Rules, err = decode[[]linux.Rule](out, 3, "rules"); err != nil {
		return nil, err
	}
	if s.Routes, err = decode[[]linux.Route](out, 4, "routes"); err != nil {
		return nil, err
	}
	if s.Nft, err = decode[*linux.Ruleset](out, 5, "nft"); err != nil {
		return nil, err
	}
	if s.Nft == nil {
		s.Nft = &linux.Ruleset{}
	}
	du, err := decode[linux.DockerUserState](out, 6, "docker_user")
	if err != nil {
		return nil, err
	}
	s.DockerUser = du

	// WireGuard interfaces and their peers
	s.WireGuard = map[string]*linux.WGInfo{}
	var wgDevs []string
	var wgOps []executor.Operation
	for _, n := range sortedNames(s.Links) {
		if s.Links[n].Kind() == "wireguard" {
			wgDevs = append(wgDevs, n)
			wgOps = append(wgOps, read(ns, executor.ReadWireGuard, n))
		}
	}
	if len(wgOps) > 0 {
		wout, err := ex.Do(ctx, wgOps...)
		if err != nil {
			return nil, fmt.Errorf("read the WireGuard interfaces: %w", err)
		}
		for i, n := range wgDevs {
			info, err := decode[*linux.WGInfo](wout, i, "wireguard "+n)
			if err != nil {
				return nil, err
			}
			s.WireGuard[n] = info
		}
	}

	if want.BirdInstance != "" {
		if s.Bird, err = readBird(ctx, ex, want.BirdInstance); err != nil {
			return nil, err
		}
	}

	if s.TC, err = readTC(ctx, ex, ns, s, want.TCDevs); err != nil {
		return nil, err
	}

	if want.ServiceNS != "" {
		s.Service = readService(ctx, ex, want.ServiceNS, want.ServicePeerIf, want.ServiceHolderPID)
	}

	// per-interface reads only for interfaces that exist
	var ops []executor.Operation
	var keys []string
	var offDevs []string
	for _, d := range want.Offloads {
		if _, ok := s.Links[d]; ok {
			ops = append(ops, read(ns, executor.ReadOffloads, d))
			offDevs = append(offDevs, d)
		}
	}
	for _, e := range want.Sysctls {
		if e.Dev != "" {
			if _, ok := s.Links[e.Dev]; !ok {
				continue
			}
		}
		ops = append(ops, &executor.Read{Target: executor.Target{NS: ns}, What: executor.ReadSysctl, Name: e.Name, Dev: e.Dev})
		keys = append(keys, sysctlKey(e))
	}
	if len(ops) == 0 {
		return s, nil
	}
	out, err = ex.Do(ctx, ops...)
	if err != nil {
		return nil, fmt.Errorf("read offloads and sysctls: %w", err)
	}
	for i, d := range offDevs {
		f, err := decode[linux.Features](out, i, "offloads "+d)
		if err != nil {
			return nil, err
		}
		s.Offloads[d] = f
	}
	for i, k := range keys {
		n, err := decode[int](out, len(offDevs)+i, "sysctl "+k)
		if err != nil {
			return nil, err
		}
		s.Sysctl[k] = n
	}
	return s, nil
}

// readTC reads the tc state of the interfaces that may hold a tree of Chaos Gateway's own: the ones
// the target names and every assigned one (a tree on an interface the target no longer uses has to be
// found). The qdisc listing of each is one tool run; only an interface with a root qdisc 1: gets the
// whole read (qdiscs, classes, filters, with counters).
func readTC(ctx context.Context, ex Exec, ns string, s *State, wanted []string) (map[string]*linux.NormTree, error) {
	var devs []string
	for _, d := range union(wanted, s.Assigned) {
		if _, ok := s.Links[d]; ok {
			devs = append(devs, d)
		}
	}
	trees := map[string]*linux.NormTree{}
	if len(devs) == 0 {
		return trees, nil
	}
	ops := make([]executor.Operation, len(devs))
	for i, d := range devs {
		ops[i] = read(ns, executor.ReadQdiscs, d)
	}
	out, err := ex.Do(ctx, ops...)
	if err != nil {
		return nil, fmt.Errorf("read the qdiscs: %w", err)
	}
	var withTree []string
	for i, d := range devs {
		qs, err := decode[[]linux.Qdisc](out, i, "qdiscs "+d)
		if err != nil {
			return nil, err
		}
		for _, q := range qs {
			// the entries of a listing that names a device belong to it; `dev` is empty when a
			// version does not print it
			if q.Handle == compiler.TCRootHandle && q.Root && (q.Dev == "" || q.Dev == d) {
				withTree = append(withTree, d)
				break
			}
		}
	}
	if len(withTree) == 0 {
		return trees, nil
	}
	ops = ops[:0]
	for _, d := range withTree {
		ops = append(ops, read(ns, executor.ReadTC, d))
	}
	if out, err = ex.Do(ctx, ops...); err != nil {
		return nil, fmt.Errorf("read the tc state: %w", err)
	}
	for i, d := range withTree {
		t, err := decode[*linux.NormTree](out, i, "tc "+d)
		if err != nil {
			return nil, err
		}
		if t != nil {
			trees[d] = t
		}
	}
	return trees, nil
}

// readService reads the service namespace. A namespace that cannot be read is a namespace that does
// not exist (the executor answers "cannot open network namespace"): the plan creates it.
func readService(ctx context.Context, ex Exec, ns, peer string, pid int) *ServiceState {
	probe, err := ex.Do(ctx, &executor.Read{What: executor.ReadServiceNS, Service: ns, PID: pid})
	if err != nil {
		return &ServiceState{}
	}
	ss, err := decode[executor.ServiceNSState](probe, 0, "service namespace")
	if err != nil || !ss.Exists {
		return &ServiceState{}
	}
	out, err := ex.Do(ctx, read(ns, executor.ReadLinks, ""), read(ns, executor.ReadAddrs, ""), read(ns, executor.ReadRoutes, ""))
	if err != nil {
		return &ServiceState{}
	}
	st := &ServiceState{Exists: true, HolderMatches: ss.HolderMatches}
	if links, err := decode[[]linux.Link](out, 0, "service links"); err == nil {
		for _, l := range links {
			if l.Name == peer {
				st.PeerUp = l.Up()
			}
		}
	}
	if addrs, err := decode[[]linux.Addrs](out, 1, "service addrs"); err == nil {
		for _, a := range addrs {
			if a.Name != peer {
				continue
			}
			for _, x := range a.Addrs {
				if x.Family == "inet" {
					st.PeerAddrs = append(st.PeerAddrs, fmt.Sprintf("%s/%d", x.Local, x.PrefixLen))
				}
			}
		}
	}
	if routes, err := decode[[]linux.Route](out, 2, "service routes"); err == nil {
		for _, r := range routes {
			if r.Dst == "default" && (r.Table == "" || r.Table == "main") && r.Dev == peer {
				st.DefaultVia = r.Gateway
			}
		}
	}
	return st
}

func sysctlKey(e executor.SysctlEntry) string {
	if e.Dev == "" {
		return e.Name
	}
	return e.Name + ":" + e.Dev
}

// Host turns the state into the observed host the compiler needs.
func (s *State) Host() compiler.Host {
	var h compiler.Host
	names := make([]string, 0, len(s.Links))
	for n := range s.Links {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		l := s.Links[n]
		// bridges are what Chaos Gateway builds: they are never an uplink or a port, and leaving
		// them out keeps the compiler's own work from showing up as a change of the host
		if l.Kind() == "bridge" || dockerNoise(l.Name) {
			continue
		}
		hl := compiler.HostLink{Name: l.Name, MAC: l.MAC, Kind: l.Kind()}
		for _, a := range s.Addrs[n] {
			if a.Family != "inet" {
				continue
			}
			if p, err := netip.ParsePrefix(a.Local + "/" + strconv.Itoa(a.PrefixLen)); err == nil {
				hl.Addrs = append(hl.Addrs, compiler.HostAddr{Prefix: p, Secondary: a.Secondary})
			}
		}
		h.Links = append(h.Links, hl)
	}
	for _, r := range s.Routes {
		if r.Dst != "default" || (r.Table != "" && r.Table != "main") || r.Gateway == "" {
			continue
		}
		gw, err := netip.ParseAddr(r.Gateway)
		if err != nil || !gw.Is4() {
			continue
		}
		metric := 0
		if r.Metric != nil {
			metric = *r.Metric
		}
		h.Defaults = append(h.Defaults, compiler.HostRoute{Dev: r.Dev, Gateway: gw, Metric: metric})
	}
	return h
}

// dockerNoise matches the interfaces Docker creates and removes all the time: they are never an
// uplink or a port, and a change of one must not look like a change of the host.
func dockerNoise(name string) bool {
	if strings.HasPrefix(name, "docker") {
		return true
	}
	if strings.HasPrefix(name, "veth") && len(name) == 11 {
		return true
	}
	return strings.HasPrefix(name, "br-") && len(name) == 15 && strings.Trim(name[3:], "0123456789abcdef") == ""
}

// ReadHost reads the observed host: the compiler's input.
func ReadHost(ctx context.Context, ex Exec, ns string) (compiler.Host, error) {
	s, err := ReadState(ctx, ex, ns, Want{})
	if err != nil {
		return compiler.Host{}, err
	}
	return s.Host(), nil
}

// Local lets the executor type be used as an Exec in the same process (tests, the CLI in a
// container that runs the executor itself).
type Local struct{ E *executor.Executor }

// Do runs the operations as one request.
func (l Local) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	return l.E.DoBatch(ctx, ops)
}

// Watch implements Watcher: in the same process there is no connection to open, so this goes
// straight to the executor's own Watch (M6a-04).
func (l Local) Watch(ctx context.Context, what, ns string) (<-chan json.RawMessage, func(), error) {
	events, innerStop, err := l.E.Watch(ctx, what, ns)
	if err != nil {
		return nil, nil, err
	}
	out := make(chan json.RawMessage)
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		for ev := range events {
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			select {
			case out <- b:
			case <-stopCh:
				// stop below is about to end the underlying watch too: abandoning the rest of
				// `events` here does not leak it, since closing stopCh always precedes that.
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	stop := func() {
		close(stopCh)
		innerStop()
		<-done
	}
	return out, stop, nil
}

func sortedNames(m map[string]linux.Link) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}
