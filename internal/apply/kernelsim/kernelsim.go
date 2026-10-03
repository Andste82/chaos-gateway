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
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

type link struct {
	name, mac, kind, master string
	up                      bool
	addrs                   []string // "ip/len"
	index                   int
}

type route struct {
	table, dst, via, dev, typ, proto string
}

type rule struct {
	prio               int
	iif, oif, from, to string
	table, proto       string
}

type nftSet struct {
	typ   string
	flags []string
	elems []json.RawMessage
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
	counters map[string]int64
	chains   map[string]*nftChain
}

func (t *nftTable) clone() *nftTable {
	n := &nftTable{sets: map[string]*nftSet{}, counters: map[string]int64{}, chains: map[string]*nftChain{}}
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
	dockerChain bool
	docker      []dockerRule
	defaultMain []route // default routes of the main table (OS-owned)

	// Fail is consulted before every command; a non-nil result is returned as the command's
	// outcome (an injected failure). It receives the command with the tool name first.
	Fail func(argv []string, stdin string) *executor.Result
	// Log records every command that was run.
	Log []string
}

type dockerRule struct {
	dir, dev string
	ours     bool
}

// New returns a kernel with a loopback interface.
func New() *Kernel {
	k := &Kernel{links: map[string]*link{}, sysctl: map[string]int{}, features: map[string]map[string]bool{}}
	k.AddLink("lo", "00:00:00:00:00:00", "", true)
	return k
}

// AddLink adds a physical (kind "") or virtual interface.
func (k *Kernel) AddLink(name, mac, kind string, up bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.addLink(name, mac, kind, up)
}

func (k *Kernel) addLink(name, mac, kind string, up bool) *link {
	k.nextIdx++
	l := &link{name: name, mac: mac, kind: kind, up: up, index: k.nextIdx}
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

// SetMainDefault sets the OS-owned default route of the main table.
func (k *Kernel) SetMainDefault(via, dev string) {
	k.mu.Lock()
	defer k.mu.Unlock()
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

// Run implements executor.Runner.
func (k *Kernel) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	argv := append([]string{string(c.Tool)}, c.Args...)
	k.Log = append(k.Log, strings.Join(argv, " "))
	if k.Fail != nil {
		if r := k.Fail(argv, c.Stdin); r != nil {
			return *r, nil
		}
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
	case executor.ToolTC:
		return executor.Result{}, nil
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
		for _, l := range k.sortedLinks() {
			flags := []string{"BROADCAST", "MULTICAST"}
			if l.up {
				flags = append(flags, "UP", "LOWER_UP")
			}
			m := map[string]any{"ifindex": l.index, "ifname": l.name, "flags": flags, "address": l.mac, "link_type": "ether"}
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
			out = append(out, m)
		}
		out = append(out, map[string]any{"priority": 32766, "src": "all", "table": "main"}, map[string]any{"priority": 32767, "src": "all", "table": "default"})
		return jsonOut(out)
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
	case "link show":
		if len(a) >= 4 && a[2] == "dev" {
			if _, ok := k.links[a[3]]; ok {
				return ok2()
			}
			return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Device \"%s\" does not exist.\n", a[3])}, nil
		}
	case "link add":
		// ip link add name X type bridge
		if len(a) == 6 && a[2] == "name" && a[4] == "type" && a[5] == "bridge" {
			if _, exists := k.links[a[3]]; exists {
				return fail("RTNETLINK answers: File exists")
			}
			k.addLink(a[3], fmt.Sprintf("02:ff:00:00:00:%02x", k.nextIdx+1), "bridge", false)
			return ok2()
		}
	case "link delete":
		if len(a) == 6 && a[2] == "dev" && a[4] == "type" && a[5] == "bridge" {
			l, exists := k.links[a[3]]
			if !exists {
				return fail("Cannot find device \"%s\"", a[3])
			}
			if l.kind != "bridge" {
				return fail("RTNETLINK answers: Operation not supported")
			}
			for _, o := range k.links {
				if o.master == a[3] {
					o.master = ""
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
			if x.table == r.table && x.dst == r.dst && x.proto == r.proto {
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
