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
}

// Want names what to read besides the basics: sysctls and offloads exist per interface.
type Want struct {
	Sysctls  []executor.SysctlEntry
	Offloads []string
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
				hl.Addrs = append(hl.Addrs, p)
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

func sortedNames(m map[string]linux.Link) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}
