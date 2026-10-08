// Package kernelsim simulates the part of a Linux kernel that the executor's tools talk to: links,
// addresses, policy routing, nftables, sysctls, offloads and the DOCKER-USER chain. It implements
// executor.Runner, so the real executor with its decoder, scope checks and command planning sits on
// top of it, and tests of the apply package, the engine and the commands see a kernel that answers
// like `ip -j`, `nft -j`, `ethtool` and `iptables` do.
//
// The simulation is as strict as the tools are where it matters for Chaos Gateway: an nftables
// transaction is atomic, `add set` of an existing set of another type fails, a set or counter that
// a rule still uses cannot be deleted, and a chain must be empty to go. It is a test double, not a
// kernel: it knows only what the compiler produces.
package kernelsim

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Andste82/chaos-gateway/internal/linux"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

type link struct {
	// peerNS and peerName name the other end of a veth pair that lives in a service namespace
	peerNS, peerName        string
	name, mac, kind, master string
	up                      bool
	addrs                   []string // "ip/len"
	index                   int
	mtu                     int
	wg                      *wgState
	tc                      *simTC
}

type route struct {
	table, dst, via, dev, typ, proto string
}

type rule struct {
	prio               int
	iif, oif, from, to string
	table, proto       string
	fwmark, fwmask     string
}

type nftSet struct {
	typ   string
	flags []string
	elems []json.RawMessage
}

// nftMap is a named map (plan §3.3): a key type (possibly several, concatenated) to a value type
// ("mark" for a plain integer, "verdict" for an element that goes to a chain). Elements are the
// raw `{"elem":{"key":...,"val":...}}` objects nft itself uses.
type nftMap struct {
	keyType   json.RawMessage
	valueType string
	flags     []string
	elems     []json.RawMessage
}

type nftChain struct {
	base  map[string]any
	rules []nftRule
}

type nftRule struct {
	expr    []any
	comment string
}

type nftTable struct {
	sets     map[string]*nftSet
	maps     map[string]*nftMap
	counters map[string]int64
	chains   map[string]*nftChain
}

func (t *nftTable) clone() *nftTable {
	n := &nftTable{sets: map[string]*nftSet{}, maps: map[string]*nftMap{}, counters: map[string]int64{}, chains: map[string]*nftChain{}}
	for k, m := range t.maps {
		c := *m
		c.elems = append([]json.RawMessage(nil), m.elems...)
		n.maps[k] = &c
	}
	for k, s := range t.sets {
		c := *s
		c.elems = append([]json.RawMessage(nil), s.elems...)
		n.sets[k] = &c
	}
	for k, v := range t.counters {
		n.counters[k] = v
	}
	for k, c := range t.chains {
		cc := &nftChain{base: c.base, rules: append([]nftRule(nil), c.rules...)}
		n.chains[k] = cc
	}
	return n
}

// Kernel is the simulated kernel of one network namespace.
type Kernel struct {
	mu       sync.Mutex
	links    map[string]*link
	nextIdx  int
	routes   []route
	rules    []rule
	nft      *nftTable
	sysctl   map[string]int
	features map[string]map[string]bool // dev → feature → on
	// docker
	neighbors []linux.Neighbor
	conntrack string
	// conntrackEvents is the open conntrack watch's channel, if any (M6a-04); nil when none is open.
	conntrackEvents chan string
	birdShow        string // output of `show protocols all` set by a test
	birdRunning     bool   // a configure has reached the simulated BIRD
	dockerChain     bool
	docker          []dockerRule
	defaultMain     []route // default routes of the main table (OS-owned)
	// svcNames are the names of service namespaces the simulator knows; a namespace exists once
	// `ip netns add` created it, and has a kernel of its own
	svcNames map[string]bool
	svc      map[string]*Kernel
	// inode is the identity of this kernel as a network namespace; holders are the processes whose
	// namespaces can be attached
	inode     uint64
	holders   map[int]uint64
	nextInode uint64

	// Fail is consulted before every command; a non-nil result is returned as the command's
	// outcome (an injected failure). It receives the command with the tool name first.
	Fail func(argv []string, stdin string) *executor.Result
	// after is called after every command that was run (not after an injected failure), outside the
	// simulator's lock: it may run commands of its own, to change the kernel behind the caller's back
	// (a drift that verify has to find). SetAfter sets it.
	after func(argv []string, stdin string)
	// Log records every command that was run.
	Log []string
	// tcSeed numbers the netem qdiscs created: the seed of each (a re-created qdisc has another)
	tcSeed uint64
}

type dockerRule struct {
	dir, dev string
	ours     bool
}

// New returns a kernel with a loopback interface.
func New() *Kernel {
	// a real BIRD instance is already running, on whatever configuration EnsureBirdConfig wrote,
	// before the executor ever sends it a configure (M4c-02): birdRunning starts true, not tied to
	// a configure having happened yet.
	k := &Kernel{links: map[string]*link{}, sysctl: map[string]int{}, features: map[string]map[string]bool{}, birdRunning: true}
	k.AddLink("lo", "00:00:00:00:00:00", "", true)
	return k
}

// ServiceNamespace tells the simulator that commands for the namespace name run in a kernel of
// their own, which exists after `ip netns add` (and not before: like the real tool, a command for
// a namespace that is missing fails).
func (k *Kernel) ServiceNamespace(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.svcNames == nil {
		k.svcNames = map[string]bool{}
		k.svc = map[string]*Kernel{}
		k.holders = map[int]uint64{}
		k.nextInode = 4026531000
	}
	k.svcNames[name] = true
}

// AddHolder registers a process whose network namespace can be attached and returns the namespace's
// identity; a second call for the same pid is a new namespace (the holder restarted under the same
// number).
func (k *Kernel) AddHolder(pid int) uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextInode++
	k.holders[pid] = k.nextInode
	return k.nextInode
}

// NetnsInode identifies a network namespace file the way the executor asks for it
// (executor.WithNetnsInode): /run/netns/NAME and /proc/PID/ns/net.
func (k *Kernel) NetnsInode(path string) (uint64, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if name, ok := strings.CutPrefix(path, "/run/netns/"); ok {
		if sub := k.svc[name]; sub != nil {
			return sub.inode, true
		}
		return 0, false
	}
	if rest, ok := strings.CutPrefix(path, "/proc/"); ok {
		if pid, ok2 := strings.CutSuffix(rest, "/ns/net"); ok2 {
			n, err := strconv.Atoi(pid)
			if err == nil {
				ino, found := k.holders[n]
				return ino, found
			}
		}
	}
	return 0, false
}

// InService returns the kernel of a service namespace, nil while it does not exist.
func (k *Kernel) InService(name string) *Kernel {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.svc[name]
}

// DropService removes a service namespace with everything in it: its holder died. The veth end
// outside goes with it, as in the kernel.
func (k *Kernel) DropService(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.svc, name)
	for n, l := range k.links {
		if l.peerNS == name {
			delete(k.links, n)
		}
	}
}

// AddLink adds a physical (kind "") or virtual interface.
func (k *Kernel) AddLink(name, mac, kind string, up bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.addLink(name, mac, kind, up)
}

func (k *Kernel) addLink(name, mac, kind string, up bool) *link {
	k.nextIdx++
	l := &link{name: name, mac: mac, kind: kind, up: up, index: k.nextIdx, mtu: 1500}
	k.links[name] = l
	k.features[name] = map[string]bool{"generic-receive-offload": true, "generic-segmentation-offload": true, "tcp-segmentation-offload": true}
	return l
}

// SetAddr gives an interface an address ("203.0.113.1/24").
func (k *Kernel) SetAddr(dev, cidr string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[dev]
	for _, a := range l.addrs {
		if a == cidr {
			return
		}
	}
	l.addrs = append(l.addrs, cidr)
}

// DelAddr removes an address.
func (k *Kernel) DelAddr(dev, cidr string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[dev]
	var keep []string
	for _, a := range l.addrs {
		if a != cidr {
			keep = append(keep, a)
		}
	}
	l.addrs = keep
}

// RenameLink changes the name of an interface (a USB adapter that comes back as another name).
func (k *Kernel) RenameLink(from, to string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[from]
	delete(k.links, from)
	l.name = to
	k.links[to] = l
	k.features[to] = k.features[from]
	delete(k.features, from)
	for _, o := range k.links {
		if o.master == from {
			o.master = to
		}
	}
}

// RemoveLink removes an interface (an unplugged adapter).
func (k *Kernel) RemoveLink(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.links, name)
	delete(k.features, name)
	for _, o := range k.links {
		if o.master == name {
			o.master = ""
		}
	}
}

// SetMainDefault sets the OS-owned default route of the main table; without a device the main
// table has none.
func (k *Kernel) SetMainDefault(via, dev string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if dev == "" {
		k.defaultMain = nil
		return
	}
	k.defaultMain = []route{{table: "", dst: "default", via: via, dev: dev}}
}

// AddDockerChain creates the DOCKER-USER chain with Docker's own RETURN rule.
func (k *Kernel) AddDockerChain() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dockerChain = true
	k.docker = append(k.docker, dockerRule{dir: "", dev: "", ours: false})
}

// ForeignRule adds a policy rule with another protocol (not ours).
func (k *Kernel) ForeignRule(prio int, iif, table string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rules = append(k.rules, rule{prio: prio, iif: iif, table: table, proto: "static"})
}

// ForeignRoute adds a route with another protocol in a table.
func (k *Kernel) ForeignRoute(table, dst, via, dev string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.routes = append(k.routes, route{table: table, dst: dst, via: via, dev: dev, proto: "static"})
}

// DockerOursLast moves the accept rules of Chaos Gateway behind Docker's RETURN rule, which makes
// them ineffective: what a Docker restart or another tool can do to the chain.
func (k *Kernel) DockerOursLast() {
	k.mu.Lock()
	defer k.mu.Unlock()
	var ours, other []dockerRule
	for _, r := range k.docker {
		if r.ours {
			ours = append(ours, r)
		} else {
			other = append(other, r)
		}
	}
	k.docker = append(other, ours...)
}

// BumpCounter adds to a named counter, as traffic would.
func (k *Kernel) BumpCounter(name string, n int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nft.counters[name] += n
}

// Counter returns a named counter's value.
func (k *Kernel) Counter(name string) (int64, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.nft == nil {
		return 0, false
	}
	v, ok := k.nft.counters[name]
	return v, ok
}

// AddElement adds an element to a set, as the DNS proxy would.
func (k *Kernel) AddElement(set, elem string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.nft.sets[set]
	b, _ := json.Marshal(elem)
	s.elems = append(s.elems, b)
}

// SetElements returns the raw elements of a set as strings.
func (k *Kernel) SetElements(set string) []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.nft == nil || k.nft.sets[set] == nil {
		return nil
	}
	var out []string
	for _, e := range k.nft.sets[set].elems {
		out = append(out, string(e))
	}
	return out
}

// RemoveSetElement removes an element: a manipulation verify has to detect.
func (k *Kernel) RemoveSetElement(set string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	s := k.nft.sets[set]
	if len(s.elems) > 0 {
		s.elems = s.elems[1:]
	}
}

// DeleteNftRule removes the last rule of a chain: another manipulation.
func (k *Kernel) DeleteNftRule(chain string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	c := k.nft.chains[chain]
	c.rules = c.rules[:len(c.rules)-1]
}

// Reset returns the Chaos Gateway state to what a host without it looks like: no bridges of ours,
// no nftables table, no routes or rules of ours.
func (k *Kernel) Reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nft = nil
}

// Commands returns the log of commands.
func (k *Kernel) Commands() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.Log...)
}

// ClearLog empties the command log.
func (k *Kernel) ClearLog() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.Log = nil
}

// ---- executor.Runner -------------------------------------------------------------------------

// SetAfter sets the function that is called after every command that was run (nil removes it). It is
// called outside the simulator's lock and may run commands of its own, to change the kernel behind the
// caller's back (a drift that verify has to find).
func (k *Kernel) SetAfter(f func(argv []string, stdin string)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.after = f
}

// Run implements executor.Runner.
func (k *Kernel) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	res, ran, err := k.run(ctx, c)
	if ran {
		k.mu.Lock()
		after := k.after
		k.mu.Unlock()
		if after != nil {
			after(append([]string{string(c.Tool)}, c.Args...), c.Stdin)
		}
	}
	return res, err
}

// run runs the command under the lock; ran is false for an injected failure.
func (k *Kernel) run(ctx context.Context, c executor.Command) (res executor.Result, ran bool, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	argv := append([]string{string(c.Tool)}, c.Args...)
	k.Log = append(k.Log, strings.Join(argv, " "))
	if k.Fail != nil {
		if r := k.Fail(argv, c.Stdin); r != nil {
			return *r, false, nil
		}
	}
	res, err = k.dispatch(ctx, c)
	return res, true, err
}

func (k *Kernel) dispatch(ctx context.Context, c executor.Command) (executor.Result, error) {
	if c.NS != "" && k.svcNames[c.NS] {
		sub := k.svc[c.NS]
		if sub == nil {
			return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Cannot open network namespace \"%s\": No such file or directory\n", c.NS)}, nil
		}
		c.NS = ""
		return sub.Run(ctx, c)
	}
	switch c.Tool {
	case executor.ToolIP:
		return k.ip(c)
	case executor.ToolNft:
		return k.nftCmd(c)
	case executor.ToolSysctl:
		return k.sysctlCmd(c)
	case executor.ToolEthtool:
		return k.ethtool(c)
	case executor.ToolIptables:
		return k.iptables(c)
	case executor.ToolWg:
		return k.wg(c)
	case executor.ToolTC:
		return k.tcCmd(c)
	case executor.ToolBird, executor.ToolBirdc:
		return k.birdCmd(c)
	case executor.ToolConntrack:
		return k.conntrackCmd(c.Args)
	}
	return executor.Result{Exit: 127, Stderr: "unknown tool"}, nil
}

func fail(format string, a ...any) (executor.Result, error) {
	return executor.Result{Exit: 2, Stderr: fmt.Sprintf(format, a...) + "\n"}, nil
}

func okr(stdout string) (executor.Result, error) { return executor.Result{Stdout: stdout}, nil }

func (k *Kernel) sortedLinks() []*link {
	var out []*link
	for _, l := range k.links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].index < out[j].index })
	return out
}

func jsonOut(v any) (executor.Result, error) {
	b, _ := json.Marshal(v)
	return okr(string(b))
}

func (k *Kernel) ip(c executor.Command) (executor.Result, error) {
	a := c.Args
	for len(a) > 0 && (a[0] == "-4" || a[0] == "-6" || a[0] == "-force" || a[0] == "-o") {
		a = a[1:]
	}
	if len(a) > 0 && a[0] == "-j" {
		a = a[1:]
		if len(a) > 0 && a[0] == "-d" {
			a = a[1:]
		}
		return k.ipRead(a)
	}
	if len(a) >= 2 && a[0] == "-batch" {
		return k.ipBatch(c, strings.Split(strings.TrimSpace(c.Stdin), "\n"))
	}
	return k.ipCmd(a)
}

func (k *Kernel) ipRead(a []string) (executor.Result, error) {
	switch {
	case len(a) >= 2 && a[0] == "link" && a[1] == "show":
		var out []map[string]any
		if len(a) >= 4 && a[2] == "dev" {
			if _, exists := k.links[a[3]]; !exists {
				return executor.Result{Exit: 1, Stderr: "Device \"" + a[3] + "\" does not exist.\n"}, nil
			}
		}
		for _, l := range k.sortedLinks() {
			if len(a) >= 4 && a[2] == "dev" && l.name != a[3] {
				continue
			}
			flags := []string{"BROADCAST", "MULTICAST"}
			if l.up {
				flags = append(flags, "UP", "LOWER_UP")
			}
			m := map[string]any{"ifindex": l.index, "ifname": l.name, "flags": flags, "address": l.mac, "link_type": "ether", "mtu": l.mtu}
			if l.master != "" {
				m["master"] = l.master
			}
			if l.kind != "" {
				m["linkinfo"] = map[string]any{"info_kind": l.kind}
			}
			out = append(out, m)
		}
		return jsonOut(out)
	case len(a) >= 2 && a[0] == "addr" && a[1] == "show":
		var out []map[string]any
		for _, l := range k.sortedLinks() {
			var infos []map[string]any
			for _, ad := range l.addrs {
				ip, plen, _ := strings.Cut(ad, "/")
				n, _ := strconv.Atoi(plen)
				infos = append(infos, map[string]any{"family": "inet", "local": ip, "prefixlen": n})
			}
			out = append(out, map[string]any{"ifindex": l.index, "ifname": l.name, "addr_info": infos})
		}
		return jsonOut(out)
	case len(a) >= 2 && a[0] == "neigh" && a[1] == "show":
		out := []map[string]any{}
		for _, n := range k.neighbors {
			if len(a) >= 4 && a[2] == "dev" && n.Dev != a[3] {
				continue
			}
			m := map[string]any{"dst": n.Dst, "dev": n.Dev, "state": n.State}
			if n.LLAddr != "" {
				m["lladdr"] = n.LLAddr
			}
			out = append(out, m)
		}
		return jsonOut(out)
	case len(a) >= 2 && a[0] == "rule" && a[1] == "show":
		out := []map[string]any{{"priority": 0, "src": "all", "table": "local"}}
		rs := append([]rule(nil), k.rules...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].prio < rs[j].prio })
		for _, r := range rs {
			m := map[string]any{"priority": r.prio, "src": "all", "table": r.table}
			if r.iif != "" {
				m["iif"] = r.iif
			}
			if r.oif != "" {
				m["oif"] = r.oif
			}
			if r.from != "" {
				ip, plen, _ := strings.Cut(r.from, "/")
				m["src"] = ip
				if n, err := strconv.Atoi(plen); err == nil {
					m["srclen"] = n
				}
			}
			if r.to != "" {
				ip, plen, _ := strings.Cut(r.to, "/")
				m["dst"] = ip
				if n, err := strconv.Atoi(plen); err == nil {
					m["dstlen"] = n
				}
			}
			if r.proto != "" {
				m["protocol"] = r.proto
			}
			if r.fwmark != "" {
				m["fwmark"], m["fwmask"] = r.fwmark, r.fwmask
			}
			out = append(out, m)
		}
		out = append(out, map[string]any{"priority": 32766, "src": "all", "table": "main"}, map[string]any{"priority": 32767, "src": "all", "table": "default"})
		return jsonOut(out)
	case len(a) >= 3 && a[0] == "route" && a[1] == "get":
		return k.routeGet(a[2:])
	case len(a) >= 2 && a[0] == "route" && a[1] == "show":
		var out []map[string]any
		for _, r := range k.defaultMain {
			out = append(out, map[string]any{"dst": "default", "gateway": r.via, "dev": r.dev, "flags": []string{}})
		}
		for _, r := range k.routes {
			// `ip -j` prints a host route as the bare address
			m := map[string]any{"dst": strings.TrimSuffix(r.dst, "/32"), "table": r.table, "flags": []string{}}
			if r.via != "" {
				m["gateway"] = r.via
			}
			if r.dev != "" {
				m["dev"] = r.dev
			}
			if r.typ != "" {
				m["type"] = r.typ
			}
			if r.proto != "" {
				m["protocol"] = r.proto
			}
			out = append(out, m)
		}
		return jsonOut(out)
	}
	return fail("unsupported ip read %v", a)
}

func (k *Kernel) ipCmd(a []string) (executor.Result, error) {
	if len(a) < 2 {
		return fail("ip: bad command")
	}
	switch a[0] + " " + a[1] {
	case "netns add", "netns attach":
		if len(a) < 3 || !k.svcNames[a[2]] {
			return fail("netns: unknown namespace")
		}
		if k.svc[a[2]] != nil {
			return fail("Cannot create namespace file \"/run/netns/%s\": File exists", a[2])
		}
		sub := New()
		k.nextInode++
		sub.inode = k.nextInode
		if a[1] == "attach" {
			if len(a) != 4 {
				return fail("usage: ip netns attach NAME PID")
			}
			pid, err := strconv.Atoi(a[3])
			ino, ok := k.holders[pid]
			if err != nil || !ok {
				return fail("Cannot open network namespace of pid %s: No such file or directory", a[3])
			}
			sub.inode = ino
		}
		k.svc[a[2]] = sub
		return ok2()
	case "netns delete":
		if len(a) != 3 || k.svc[a[2]] == nil {
			return fail("Cannot remove namespace file: No such file or directory")
		}
		delete(k.svc, a[2])
		for n, l := range k.links {
			if l.peerNS == a[2] {
				delete(k.links, n)
			}
		}
		return ok2()
	case "route replace":
		// ip route replace default via V dev D (the main table of a service namespace)
		if len(a) == 7 && a[2] == "default" && a[3] == "via" && a[5] == "dev" {
			if _, exists := k.links[a[6]]; !exists {
				return fail("Cannot find device \"%s\"", a[6])
			}
			for i, x := range k.routes {
				if x.table == "main" && x.dst == "default" {
					k.routes = append(k.routes[:i], k.routes[i+1:]...)
					break
				}
			}
			k.routes = append(k.routes, route{table: "main", dst: "default", via: a[4], dev: a[6]})
			return ok2()
		}
	case "link show":
		if len(a) >= 4 && a[2] == "dev" {
			if _, ok := k.links[a[3]]; ok {
				return ok2()
			}
			return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Device \"%s\" does not exist.\n", a[3])}, nil
		}
	case "link add":
		// ip link add dev X type wireguard
		if len(a) == 6 && a[2] == "dev" && a[4] == "type" && a[5] == "wireguard" {
			if _, exists := k.links[a[3]]; exists {
				return fail("RTNETLINK answers: File exists")
			}
			l := k.addLink(a[3], "", "wireguard", false)
			l.mtu = 1420
			l.wg = &wgState{peers: map[string]*wgPeer{}}
			return ok2()
		}
		// ip link add X type veth peer name Y netns NS
		if len(a) == 10 && a[3] == "type" && a[4] == "veth" && a[5] == "peer" && a[6] == "name" && a[8] == "netns" {
			if _, exists := k.links[a[2]]; exists {
				return fail("RTNETLINK answers: File exists")
			}
			sub := k.svc[a[9]]
			if sub == nil {
				return fail("Cannot open network namespace \"%s\": No such file or directory", a[9])
			}
			l := k.addLink(a[2], fmt.Sprintf("02:ee:00:00:00:%02x", k.nextIdx+1), "veth", false)
			l.peerNS, l.peerName = a[9], a[7]
			sub.addLink(a[7], fmt.Sprintf("02:ee:00:01:00:%02x", sub.nextIdx+1), "veth", false)
			return ok2()
		}
		// ip link add name X type bridge
		if len(a) == 6 && a[2] == "name" && a[4] == "type" && a[5] == "bridge" {
			if _, exists := k.links[a[3]]; exists {
				return fail("RTNETLINK answers: File exists")
			}
			k.addLink(a[3], fmt.Sprintf("02:ff:00:00:00:%02x", k.nextIdx+1), "bridge", false)
			return ok2()
		}
	case "link delete":
		if len(a) == 6 && a[2] == "dev" && a[4] == "type" && (a[5] == "bridge" || a[5] == "wireguard" || a[5] == "veth") {
			_, exists := k.links[a[3]]
			if !exists {
				return fail("Cannot find device \"%s\"", a[3])
			}
			// like the real tool, the simulator does not check the kind: the executor must
			for _, o := range k.links {
				if o.master == a[3] {
					o.master = ""
				}
			}
			if pl := k.links[a[3]]; pl.peerNS != "" {
				if sub := k.svc[pl.peerNS]; sub != nil {
					delete(sub.links, pl.peerName)
				}
			}
			delete(k.links, a[3])
			return ok2()
		}
	case "link set":
		if len(a) >= 5 && a[2] == "dev" {
			l, exists := k.links[a[3]]
			if !exists {
				return fail("Cannot find device \"%s\"", a[3])
			}
			switch {
			case a[4] == "master" && len(a) == 6:
				m, mok := k.links[a[5]]
				if !mok || m.kind != "bridge" {
					return fail("Error: Device does not exist or is not a bridge")
				}
				l.master = a[5]
				return ok2()
			case a[4] == "nomaster":
				l.master = ""
				return ok2()
			case a[4] == "mtu" && len(a) == 6:
				n, err := strconv.Atoi(a[5])
				if err != nil {
					return fail("Error: argument \"%s\" is wrong: mtu", a[5])
				}
				l.mtu = n
				return ok2()
			case a[4] == "up":
				l.up = true
				return ok2()
			case a[4] == "down":
				l.up = false
				return ok2()
			}
		}
	case "addr replace":
		// ip addr replace CIDR dev X
		if len(a) == 5 && a[3] == "dev" {
			l, exists := k.links[a[4]]
			if !exists {
				return fail("Cannot find device \"%s\"", a[4])
			}
			for _, x := range l.addrs {
				if x == a[2] {
					return ok2()
				}
			}
			l.addrs = append(l.addrs, a[2])
			return ok2()
		}
	case "addr delete":
		if len(a) == 5 && a[3] == "dev" {
			l, exists := k.links[a[4]]
			if !exists {
				return fail("Cannot find device \"%s\"", a[4])
			}
			for i, x := range l.addrs {
				if x == a[2] {
					l.addrs = append(l.addrs[:i], l.addrs[i+1:]...)
					return ok2()
				}
			}
			return fail("RTNETLINK answers: Cannot assign requested address")
		}
	}
	return fail("unsupported ip command %v", a)
}

func ok2() (executor.Result, error) { return executor.Result{}, nil }

// ipBatch runs `ip -force -batch -`: every line on its own, failures are reported but do not stop.
func (k *Kernel) ipBatch(c executor.Command, lines []string) (executor.Result, error) {
	force := false
	for _, a := range c.Args {
		if a == "-force" {
			force = true
		}
	}
	var stderr strings.Builder
	exit := 0
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		var msg string
		switch f[0] {
		case "route":
			msg = k.batchRoute(f[1:])
		case "rule":
			msg = k.batchRule(f[1:])
		default:
			msg = "Object \"" + f[0] + "\" is unknown"
		}
		if msg != "" {
			stderr.WriteString("RTNETLINK answers: " + msg + "\n")
			fmt.Fprintf(&stderr, "Command failed -:%d\n", i+1)
			exit = 1
			if !force {
				break
			}
		}
	}
	return executor.Result{Stderr: stderr.String(), Exit: exit}, nil
}

func (k *Kernel) batchRoute(f []string) string {
	// route replace|del [type] DST table N proto P [via V] [dev D] [metric M]
	verb := f[0]
	f = f[1:]
	r := route{}
	if len(f) > 0 && (f[0] == "blackhole" || f[0] == "unreachable" || f[0] == "prohibit") {
		r.typ, f = f[0], f[1:]
	}
	r.dst, f = f[0], f[1:]
	for i := 0; i+1 < len(f); i += 2 {
		switch f[i] {
		case "table":
			r.table = f[i+1]
		case "proto":
			r.proto = f[i+1]
		case "via":
			r.via = f[i+1]
		case "dev":
			r.dev = f[i+1]
		}
	}
	if r.dev != "" {
		if _, exists := k.links[r.dev]; !exists {
			return "No such device"
		}
	}
	idx := -1
	for i, x := range k.routes {
		if x.table == r.table && x.dst == r.dst && x.proto == r.proto && x.typ == r.typ && (verb == "replace" || (x.via == r.via && x.dev == r.dev)) {
			idx = i
		}
	}
	if verb == "replace" {
		// a route of the same table and prefix is replaced, whatever its next hop
		for i, x := range k.routes {
			if x.table == r.table && x.dst == r.dst && x.proto == r.proto && x.typ == r.typ {
				idx = i
			}
		}
		if idx >= 0 {
			k.routes[idx] = r
		} else {
			k.routes = append(k.routes, r)
		}
		return ""
	}
	if idx < 0 {
		return "No such process"
	}
	k.routes = append(k.routes[:idx], k.routes[idx+1:]...)
	return ""
}

func (k *Kernel) batchRule(f []string) string {
	verb := f[0]
	f = f[1:]
	r := rule{}
	for i := 0; i+1 < len(f); i += 2 {
		switch f[i] {
		case "priority":
			r.prio, _ = strconv.Atoi(f[i+1])
		case "from":
			r.from = f[i+1]
		case "to":
			r.to = f[i+1]
		case "iif":
			r.iif = f[i+1]
		case "oif":
			r.oif = f[i+1]
		case "table":
			r.table = f[i+1]
		case "protocol":
			r.proto = f[i+1]
		case "fwmark":
			v, m, _ := strings.Cut(f[i+1], "/")
			r.fwmark, r.fwmask = hexMark(v), "0xffffffff"
			if m != "" {
				r.fwmask = hexMark(m)
			}
		}
	}
	for i, x := range k.rules {
		if x == r {
			if verb == "add" {
				return "File exists"
			}
			k.rules = append(k.rules[:i], k.rules[i+1:]...)
			return ""
		}
	}
	if verb == "add" {
		k.rules = append(k.rules, r)
		return ""
	}
	return "No such file or directory"
}

func (k *Kernel) sysctlCmd(c executor.Command) (executor.Result, error) {
	a := c.Args
	if len(a) == 2 && a[0] == "-n" {
		v, ok := k.sysctl[a[1]]
		if !ok {
			return okr("0\n")
		}
		return okr(strconv.Itoa(v) + "\n")
	}
	if len(a) == 2 && a[0] == "-w" {
		key, val, _ := strings.Cut(a[1], "=")
		n, err := strconv.Atoi(val)
		if err != nil {
			return fail("bad value")
		}
		if dev := devOfSysctl(key); dev != "" {
			if _, exists := k.links[dev]; !exists {
				return fail("sysctl: cannot stat /proc/sys/%s: No such file or directory", key)
			}
		}
		k.sysctl[key] = n
		return ok2()
	}
	return fail("unsupported sysctl %v", a)
}

func devOfSysctl(key string) string {
	p := strings.Split(key, "/")
	if len(p) == 5 && p[0] == "net" && p[2] == "conf" {
		return p[3]
	}
	return ""
}

func (k *Kernel) ethtool(c executor.Command) (executor.Result, error) {
	a := c.Args
	if len(a) == 2 && a[0] == "-k" {
		f, ok := k.features[a[1]]
		if !ok {
			return fail("Cannot get device features: No such device")
		}
		var b strings.Builder
		b.WriteString("Features for " + a[1] + ":\n")
		for _, n := range []string{"generic-receive-offload", "generic-segmentation-offload", "tcp-segmentation-offload"} {
			b.WriteString(n + ": " + map[bool]string{true: "on", false: "off"}[f[n]] + "\n")
		}
		b.WriteString("large-receive-offload: off [fixed]\n")
		return ok2s(b.String())
	}
	if len(a) >= 2 && a[0] == "-K" {
		f, ok := k.features[a[1]]
		if !ok {
			return fail("Cannot get device features: No such device")
		}
		for i := 2; i+1 < len(a); i += 2 {
			name := map[string]string{"gro": "generic-receive-offload", "gso": "generic-segmentation-offload", "tso": "tcp-segmentation-offload"}[a[i]]
			if name != "" {
				f[name] = a[i+1] == "on"
			}
		}
		return ok2()
	}
	return fail("unsupported ethtool %v", a)
}

func ok2s(s string) (executor.Result, error) { return executor.Result{Stdout: s}, nil }

func (k *Kernel) iptables(c executor.Command) (executor.Result, error) {
	a := c.Args
	if len(a) >= 2 && a[0] == "-w" {
		a = a[2:]
	}
	if !k.dockerChain {
		return executor.Result{Exit: 1, Stderr: "iptables: No chain/target/match by that name.\n"}, nil
	}
	switch a[0] {
	case "-S":
		var b strings.Builder
		b.WriteString("-N DOCKER-USER\n")
		for _, r := range k.docker {
			switch {
			case r.dev == "":
				b.WriteString("-A DOCKER-USER -j RETURN\n")
			case r.ours:
				fmt.Fprintf(&b, "-A DOCKER-USER -%s %s -m comment --comment chaosgw -j ACCEPT\n", r.dir, r.dev)
			default:
				fmt.Fprintf(&b, "-A DOCKER-USER -%s %s -j ACCEPT\n", r.dir, r.dev)
			}
		}
		return ok2s(b.String())
	case "-C", "-D":
		d, dev := ruleOf(a)
		for i, r := range k.docker {
			if r.ours && r.dir == d && r.dev == dev {
				if a[0] == "-D" {
					k.docker = append(k.docker[:i], k.docker[i+1:]...)
				}
				return ok2()
			}
		}
		return executor.Result{Exit: 1, Stderr: "iptables: Bad rule (does a matching rule exist in that chain?).\n"}, nil
	case "-I":
		d, v := ruleOf(a)
		k.docker = append([]dockerRule{{dir: d, dev: v, ours: true}}, k.docker...)
		return ok2()
	}
	return fail("unsupported iptables %v", a)
}

// ruleOf extracts the direction and interface of "-C DOCKER-USER -i X ..." or "-I DOCKER-USER 1 -i X ...".
func ruleOf(a []string) (dir, dev string) {
	for i, w := range a {
		if (w == "-i" || w == "-o") && i+1 < len(a) {
			return strings.TrimPrefix(w, "-"), a[i+1]
		}
	}
	return "", ""
}

// hexMark prints a mark the way `ip -j rule` does: lower-case hex without leading zeros.
func hexMark(v string) string {
	n, err := strconv.ParseUint(v, 0, 32)
	if err != nil {
		return v
	}
	return fmt.Sprintf("0x%x", n)
}

// routeGet answers `ip -j route get DST [from SRC] [iif DEV]` by walking the policy rules in
// priority order and taking the longest matching prefix of the table a matching rule selects, the
// way the kernel's FIB lookup does. Rules with a mark never match (a route get has no mark); the
// connected routes of the links' addresses count as routes of the main table.
func (k *Kernel) routeGet(a []string) (executor.Result, error) {
	dst, rest := a[0], a[1:]
	var src, iif string
	for len(rest) >= 2 {
		switch rest[0] {
		case "from":
			src = rest[1]
		case "iif":
			iif = rest[1]
		default:
			return fail("unsupported route get argument %q", rest[0])
		}
		rest = rest[2:]
	}
	dstIP, err := netip.ParseAddr(dst)
	if err != nil {
		return fail("route get: bad destination %q", dst)
	}
	var srcIP netip.Addr
	if src != "" {
		if srcIP, err = netip.ParseAddr(src); err != nil {
			return fail("route get: bad source %q", src)
		}
	}
	if iif != "" {
		if _, ok := k.links[iif]; !ok {
			return executor.Result{Exit: 1, Stderr: "Cannot find device \"" + iif + "\"\n"}, nil
		}
	}
	rs := append([]rule(nil), k.rules...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].prio < rs[j].prio })
	rs = append(rs, rule{prio: 32766, table: "main"})
	inPrefix := func(spec string, ip netip.Addr) bool {
		if spec == "" {
			return true
		}
		if !strings.Contains(spec, "/") {
			spec += "/32"
		}
		p, err := netip.ParsePrefix(spec)
		return err == nil && ip.IsValid() && p.Contains(ip)
	}
	for _, r := range rs {
		if r.fwmark != "" || r.oif != "" || (r.iif != "" && r.iif != iif) || !inPrefix(r.from, srcIP) || !inPrefix(r.to, dstIP) {
			continue
		}
		best, ok := k.lookup(r.table, dstIP)
		if !ok {
			continue
		}
		switch best.typ {
		case "blackhole":
			return executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Invalid argument\n"}, nil
		case "unreachable":
			return executor.Result{Exit: 2, Stderr: "RTNETLINK answers: No route to host\n"}, nil
		case "prohibit":
			return executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Permission denied\n"}, nil
		}
		m := map[string]any{"dst": dst, "dev": best.dev, "flags": []string{}, "cache": []string{}}
		if best.via != "" {
			m["gateway"] = best.via
		}
		if src != "" {
			m["from"] = src
		}
		if iif != "" {
			m["iif"] = iif
		}
		if r.table != "main" && r.table != "" {
			m["table"] = r.table
		}
		return jsonOut([]map[string]any{m})
	}
	return executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Network is unreachable\n"}, nil
}

// lookup finds the longest-prefix route of a table for an address.
func (k *Kernel) lookup(table string, ip netip.Addr) (route, bool) {
	var cands []route
	switch table {
	case "main", "254", "":
		cands = append(cands, k.defaultMain...)
		for _, l := range k.sortedLinks() {
			for _, ad := range l.addrs {
				if p, err := netip.ParsePrefix(ad); err == nil {
					cands = append(cands, route{dst: p.Masked().String(), dev: l.name})
				}
			}
		}
	}
	for _, r := range k.routes {
		if r.table == table || (table == "main" && r.table == "") {
			cands = append(cands, r)
		}
	}
	best, bits, found := route{}, -1, false
	for _, r := range cands {
		spec := r.dst
		if spec == "default" {
			spec = "0.0.0.0/0"
		}
		if !strings.Contains(spec, "/") {
			spec += "/32"
		}
		p, err := netip.ParsePrefix(spec)
		if err != nil || !p.Contains(ip) || p.Bits() <= bits {
			continue
		}
		best, bits, found = r, p.Bits(), true
	}
	return best, found
}
