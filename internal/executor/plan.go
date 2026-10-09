package executor

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Tool names a binary. The runner maps each to a fixed path.
type Tool string

// The tools the executor runs.
const (
	ToolIP        Tool = "ip"
	ToolNft       Tool = "nft"
	ToolTC        Tool = "tc"
	ToolEthtool   Tool = "ethtool"
	ToolIptables  Tool = "iptables"
	ToolSysctl    Tool = "sysctl"
	ToolWg        Tool = "wg"
	ToolBird      Tool = "bird"
	ToolBirdc     Tool = "birdc"
	ToolConntrack Tool = "conntrack"
)

// Command is one invocation: a tool, an argument array (never a shell string), optional standard
// input and the network namespace to run in.
type Command struct {
	Tool  Tool
	Args  []string
	Stdin string
	NS    string
}

// String renders the command for logs and error messages.
func (c Command) String() string {
	s := string(c.Tool) + " " + strings.Join(c.Args, " ")
	if c.NS != "" {
		s = "[" + c.NS + "] " + s
	}
	return s
}

// Step is one command of a plan. A step with a probe runs the probe first and then the command
// only if the probe's success matches RunIfProbeOK (idempotent ensure/remove).
type Step struct {
	// NeedsConfig marks the `wg syncconf` step: the executor fills in its standard input from the
	// key provider when it runs, so the plan itself never holds a secret.
	NeedsConfig bool
	// Guard, when set, runs first; the step is skipped unless the guard succeeds.
	Guard *Command
	// Idempotent runs the command with the tool's "continue on error" mode and treats "already
	// exists" and "does not exist" answers as success (ip -batch -force).
	Idempotent   bool
	Probe        *Command
	RunIfProbeOK bool
	Cmd          Command
}

// Plan turns a validated operation into steps. It is pure: no execution, no state. Read
// operations have their own plan (ReadCommand).
func Plan(op Operation) ([]Step, error) {
	switch o := op.(type) {
	case *NftApply:
		return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(o.Ruleset), NS: o.NS}}}, nil
	case *NftDup:
		return planNftDup(o)
	case *NftAddElements:
		return planElements("add", o.Target, o.Set, o.Elements, o.TimeoutSeconds)
	case *NftDelElements:
		return planElements("delete", o.Target, o.Set, o.Elements, 0)
	case *NftAddMapElements:
		return planMapElements("add", o.Target, o.Map, o.Elements)
	case *NftDelMapElements:
		var elems []NftMapElement
		for _, k := range o.Keys {
			elems = append(elems, NftMapElement{Key: k})
		}
		return planMapElements("delete", o.Target, o.Map, elems)
	case *Routing:
		return planRouting(o), nil
	case *TC:
		return planTC(o), nil
	case *Offloads:
		var steps []Step
		for _, d := range o.Devs {
			steps = append(steps, Step{Cmd: Command{Tool: ToolEthtool, Args: []string{"-K", d, "gro", "off", "gso", "off", "tso", "off", "lro", "off"}, NS: o.NS}})
		}
		return steps, nil
	case *DockerUser:
		return planDockerUser(o), nil
	case *WireGuard:
		return planWireGuard(o), nil
	case *Bird:
		return nil, nil // handled by the executor itself: it writes a file and runs two tools
	case *Links:
		return planLinks(o), nil
	case *ServiceNS:
		return planServiceNS(o), nil
	case *Sysctl:
		var steps []Step
		for _, e := range o.Entries {
			steps = append(steps, Step{Cmd: Command{Tool: ToolSysctl, Args: []string{"-w", sysctlPath(e.Name, e.Dev) + "=" + strconv.Itoa(e.Value)}, NS: o.NS}})
		}
		return steps, nil
	case *ConntrackDelete:
		return planConntrackDelete(o), nil
	case *AssignInterfaces:
		return nil, nil // changes the executor's own scope, not the kernel
	}
	return nil, fmt.Errorf("no plan for %T", op)
}

// planConntrackDelete is one `conntrack -D` per flow, by the original tuple. A flow that is not there
// (any more) makes the tool exit 1 with "0 flow entries have been deleted": benign (Idempotent).
func planConntrackDelete(o *ConntrackDelete) []Step {
	steps := make([]Step, 0, len(o.Flows))
	for _, f := range o.Flows {
		args := []string{"-D", "-f", "ipv4", "-p", f.Proto, "--orig-src", f.Src, "--orig-dst", f.Dst}
		if f.Proto == "icmp" {
			args = append(args, "--icmp-type", strconv.Itoa(*f.ICMPType), "--icmp-code", strconv.Itoa(*f.ICMPCode), "--icmp-id", strconv.Itoa(*f.ICMPID))
		} else {
			args = append(args, "--orig-port-src", strconv.Itoa(f.SPort), "--orig-port-dst", strconv.Itoa(f.DPort))
		}
		steps = append(steps, Step{Idempotent: true, Cmd: Command{Tool: ToolConntrack, Args: args, NS: o.NS}})
	}
	return steps
}

func planElements(verb string, tg Target, set string, elements []string, timeout int) ([]Step, error) {
	elems := make([]any, len(elements))
	for i, e := range elements {
		var val any = e
		if p, err := netip.ParsePrefix(e); err == nil {
			val = map[string]any{"prefix": map[string]any{"addr": p.Addr().String(), "len": p.Bits()}}
		}
		if timeout > 0 {
			val = map[string]any{"elem": map[string]any{"val": val, "timeout": timeout}}
		}
		elems[i] = val
	}
	doc := map[string]any{"nftables": []any{map[string]any{verb: map[string]any{"element": map[string]any{
		"family": NftFamily, "table": NftTable, "name": set, "elem": elems,
	}}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(b), NS: tg.NS}}}, nil
}

// planMapElements builds the add/delete transaction for elements of a named map (plan §3.3, §3.4):
// the map counterpart of planElements. On delete, every NftMapElement carries only its Key.
func planMapElements(verb string, tg Target, mapName string, elements []NftMapElement) ([]Step, error) {
	elems := make([]any, len(elements))
	for i, e := range elements {
		key := mapKeyExpr(e.Key)
		if verb == "delete" {
			elems[i] = key
			continue
		}
		// A map element is a [key, value] pair (libnftables-json's SET_ELEM: "for mappings, an
		// array of arrays with exactly two elements is expected"), not an object with "key"/"val"
		// fields - confirmed against a real captured `nft -j list map` ("elem": [[9001, {"drop":
		// null}], ...]) after the object form was rejected by the real kernel ("Invalid argument").
		elems[i] = []any{key, mapValueExpr(e.Value)}
	}
	doc := map[string]any{"nftables": []any{map[string]any{verb: map[string]any{"element": map[string]any{
		"family": NftFamily, "table": NftTable, "name": mapName, "elem": elems,
	}}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(b), NS: tg.NS}}}, nil
}

// mapKeyExpr renders a map key: a single part as itself (an address becomes a prefix object, like
// planElements), several " . "-joined parts as a concatenation.
func mapKeyExpr(key string) any {
	parts := strings.Split(key, " . ")
	if len(parts) == 1 {
		return mapKeyPartExpr(parts[0])
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = mapKeyPartExpr(p)
	}
	return map[string]any{"concat": out}
}

func mapKeyPartExpr(part string) any {
	if p, err := netip.ParsePrefix(part); err == nil {
		return map[string]any{"prefix": map[string]any{"addr": p.Addr().String(), "len": p.Bits()}}
	}
	// a range "first-last" of addresses or ports (an interval map element)
	if lo, hi, ok := strings.Cut(part, "-"); ok {
		if _, err := netip.ParseAddr(lo); err == nil {
			return map[string]any{"range": []any{lo, hi}}
		}
		a, e1 := strconv.Atoi(lo)
		b, e2 := strconv.Atoi(hi)
		if e1 == nil && e2 == nil {
			return map[string]any{"range": []any{a, b}}
		}
	}
	if n, err := strconv.Atoi(part); err == nil {
		return n
	}
	return part
}

// mapValueExpr renders a map element's data: a decimal value as the integer it names (a "mark"
// map, such as the identity map), anything else as a goto to the chain it names (a "verdict" map,
// one of the classification maps).
func mapValueExpr(value string) any {
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	return map[string]any{"goto": map[string]any{"target": value}}
}

// planRouting writes one `ip -batch` per address family: routes first (replace) or last (delete),
// then rules. Every route and rule carries the executor's protocol tag.
func planRouting(o *Routing) []Step {
	var steps []Step
	for _, family := range []int{4, 6} {
		var lines []string
		for _, r := range o.Routes {
			if r.Family == family && r.Action == "replace" {
				lines = append(lines, routeLine(r))
			}
		}
		for _, r := range o.Rules {
			if r.Family == family && r.Action == "add" {
				lines = append(lines, ruleLine(r))
			}
		}
		for _, r := range o.Rules {
			if r.Family == family && r.Action == "delete" {
				lines = append(lines, ruleLine(r))
			}
		}
		for _, r := range o.Routes {
			if r.Family == family && r.Action == "delete" {
				lines = append(lines, routeLine(r))
			}
		}
		if len(lines) > 0 {
			steps = append(steps, Step{Cmd: Command{Tool: ToolIP, Args: []string{"-" + strconv.Itoa(family), "-force", "-batch", "-"}, Stdin: strings.Join(lines, "\n") + "\n", NS: o.NS}, Idempotent: true})
		}
	}
	return steps
}

func routeLine(r Route) string {
	typ := r.Type
	if typ == "" || typ == "unicast" {
		typ = ""
	} else {
		typ += " "
	}
	f := []string{"route", map[string]string{"replace": "replace", "delete": "del"}[r.Action], typ + r.Dst, "table", strconv.Itoa(r.Table), "proto", strconv.Itoa(ProtoTag)}
	if r.Via != "" {
		f = append(f, "via", r.Via)
	}
	if r.Dev != "" {
		f = append(f, "dev", r.Dev)
	}
	if r.Metric != nil {
		f = append(f, "metric", strconv.Itoa(*r.Metric))
	}
	if r.MTU != 0 && r.Action == "replace" {
		f = append(f, "mtu", "lock", strconv.Itoa(r.MTU))
	}
	return strings.Join(f, " ")
}

func ruleLine(r Rule) string {
	f := []string{"rule", map[string]string{"add": "add", "delete": "del"}[r.Action], "priority", strconv.Itoa(r.Priority)}
	if r.From != "" {
		f = append(f, "from", r.From)
	}
	if r.To != "" {
		f = append(f, "to", r.To)
	}
	if r.Fwmark != "" {
		f = append(f, "fwmark", r.Fwmark)
	}
	if r.Iif != "" {
		f = append(f, "iif", r.Iif)
	}
	if r.Oif != "" {
		f = append(f, "oif", r.Oif)
	}
	f = append(f, "table", strconv.Itoa(r.Table), "protocol", strconv.Itoa(ProtoTag))
	return strings.Join(f, " ")
}

// planTC turns the entries into `tc -batch` steps. Entries run in the order given; a run of deletions
// is a step of its own, `tc -force -batch`, marked idempotent: deleting what is already gone (a class a
// previous apply removed, a filter that was never there) is the state the caller asked for, and with
// -force one such answer does not keep the lines after it from running. Every other answer of a
// deletion, and every failure of the other entries, is an error (see onlyBenignTC).
func planTC(o *TC) []Step {
	var steps []Step
	var lines []string
	var deleting bool
	flush := func() {
		if len(lines) == 0 {
			return
		}
		args := []string{"-batch", "-"}
		if deleting {
			args = []string{"-force", "-batch", "-"}
		}
		steps = append(steps, Step{Idempotent: deleting, Cmd: Command{Tool: ToolTC, Args: args, Stdin: strings.Join(lines, "\n") + "\n", NS: o.NS}})
		lines = nil
	}
	for _, e := range o.Entries {
		if (e.Action == "delete") != deleting {
			flush()
			deleting = e.Action == "delete"
		}
		lines = append(lines, tcLine(e))
	}
	flush()
	return steps
}

// tcLine is one entry as a line of a tc batch (without the leading "tc").
func tcLine(e TCEntry) string {
	l := []string{e.Object, e.Action, "dev", e.Dev}
	switch e.Parent {
	case "":
	case "root", "ingress", "clsact":
		l = append(l, e.Parent)
	default:
		l = append(l, "parent", e.Parent)
	}
	if e.Handle != "" {
		l = append(l, "handle", e.Handle)
	}
	if e.ClassID != "" {
		l = append(l, "classid", e.ClassID)
	}
	l = append(l, e.Args...)
	return strings.Join(l, " ")
}

func planDockerUser(o *DockerUser) []Step {
	steps := dockerUserSteps(o)
	if o.OptionalChain {
		g := Command{Tool: ToolIptables, Args: []string{"-w", "5", "-S", DockerUserChain}, NS: o.NS}
		for i := range steps {
			steps[i].Guard = &g
		}
	}
	return steps
}

func dockerUserSteps(o *DockerUser) []Step {
	var steps []Step
	for _, d := range o.Devs {
		for _, dir := range []string{"-i", "-o"} {
			rule := []string{DockerUserChain, dir, d, "-m", "comment", "--comment", DockerUserComment, "-j", "ACCEPT"}
			probe := Command{Tool: ToolIptables, Args: append([]string{"-w", "5", "-C"}, rule...), NS: o.NS}
			if o.Action == "ensure" {
				steps = append(steps, Step{Probe: &probe, RunIfProbeOK: false,
					Cmd: Command{Tool: ToolIptables, Args: append([]string{"-w", "5", "-I", DockerUserChain, "1"}, rule[1:]...), NS: o.NS}})
			} else {
				steps = append(steps, Step{Probe: &probe, RunIfProbeOK: true,
					Cmd: Command{Tool: ToolIptables, Args: append([]string{"-w", "5", "-D"}, rule...), NS: o.NS}})
			}
		}
	}
	return steps
}

// ReadCommand returns the command that reads the requested state.
func ReadCommand(o *Read) Command {
	c := Command{NS: o.NS}
	switch o.What {
	case ReadLinks:
		c.Tool, c.Args = ToolIP, []string{"-j", "-d", "link", "show"}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
	case ReadAddrs:
		c.Tool, c.Args = ToolIP, []string{"-j", "addr", "show"}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
	case ReadRoutes:
		table := o.Table
		if table == "" {
			table = "all"
		}
		c.Tool, c.Args = ToolIP, []string{"-j", "route", "show", "table", table}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
	case ReadRouteGet:
		c.Tool, c.Args = ToolIP, []string{"-4", "-j", "route", "get", o.Dst}
		if o.Src != "" {
			c.Args = append(c.Args, "from", o.Src)
		}
		if o.Dev != "" {
			c.Args = append(c.Args, "iif", o.Dev)
		}
	case ReadRules:
		c.Tool, c.Args = ToolIP, []string{"-j", "rule", "show"}
	case ReadNft:
		c.Tool, c.Args = ToolNft, []string{"-j", "list", "table", NftFamily, NftTable}
	case ReadNftDup:
		c.Tool, c.Args = ToolNft, []string{"-j", "list", "table", NftDupFamily, NftDupTable}
	case ReadQdiscs, ReadClasses, ReadFilters:
		kind := map[string]string{ReadQdiscs: "qdisc", ReadClasses: "class", ReadFilters: "filter"}[o.What]
		c.Tool, c.Args = ToolTC, []string{"-j", kind, "show"}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
	case ReadTC:
		// the qdisc listing; readTC asks for the other two with tcListing
		return tcListing(o, "qdisc")
	case ReadOffloads:
		c.Tool, c.Args = ToolEthtool, []string{"-k", o.Dev}
	case ReadWireGuard:
		c.Tool, c.Args = ToolWg, []string{"show", o.Dev, "dump"}
	case ReadNeighbors:
		c.Tool, c.Args = ToolIP, []string{"-4", "-j", "neigh", "show"}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
	case ReadConntrack:
		// M6a-03: `ktimestamp` adds each entry's start time (nanoseconds since the epoch), read
		// only when `nf_conntrack_timestamp` is on; otherwise the field is simply absent.
		c.Tool, c.Args = ToolConntrack, []string{"-L", "-f", "ipv4", "-o", "ktimestamp"}
	case ReadSysctl:
		c.Tool, c.Args = ToolSysctl, []string{"-n", sysctlPath(o.Name, o.Dev)}
	case ReadDockerUser:
		c.Tool, c.Args = ToolIptables, []string{"-w", "5", "-S", DockerUserChain}
	}
	return c
}

// tcListing is one of the three listings a tc read consists of: qdisc, class or filter, with
// counters and JSON output, for the one interface of the read.
func tcListing(o *Read, kind string) Command {
	return Command{Tool: ToolTC, Args: []string{"-s", "-j", kind, "show", "dev", o.Dev}, NS: o.NS}
}

func sysctlPath(name, dev string) string {
	switch name {
	case "ip_forward":
		return "net/ipv4/ip_forward"
	case "nf_conntrack_acct":
		return "net/netfilter/nf_conntrack_acct"
	case "nf_conntrack_timestamp":
		return "net/netfilter/nf_conntrack_timestamp"
	}
	return "net/ipv6/conf/" + dev + "/" + name
}

// watchCommand builds the invocation for a watch (M6a-04). `-o id` names each event's own entry
// id (never to be confused with an ICMP echo id); `-o ktimestamp` carries each entry's start time
// (M6a-03), the same field a ReadConntrack read also asks for.
func watchCommand(what, ns string) (Command, error) {
	if what != WhatConntrack {
		return Command{}, fmt.Errorf("watch: unknown kind %q", what)
	}
	return Command{Tool: ToolConntrack, Args: []string{"-E", "-o", "id,ktimestamp"}, NS: ns}, nil
}

func planLinks(o *Links) []Step {
	ip := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.NS} }
	var steps []Step
	for _, e := range o.Entries {
		switch e.Action {
		case "add_bridge":
			exists := ip("link", "show", "dev", e.Name)
			steps = append(steps, Step{Probe: &exists, RunIfProbeOK: false, Cmd: ip("link", "add", "name", e.Name, "type", "bridge")})
		case "delete_bridge":
			exists := ip("link", "show", "dev", e.Name)
			// `type bridge` makes ip refuse any other kind of device
			steps = append(steps, Step{Probe: &exists, RunIfProbeOK: true, Cmd: ip("link", "delete", "dev", e.Name, "type", "bridge")})
		case "enslave":
			steps = append(steps, Step{Cmd: ip("link", "set", "dev", e.Name, "master", e.Master)})
		case "release":
			steps = append(steps, Step{Cmd: ip("link", "set", "dev", e.Name, "nomaster")})
		case "up", "down":
			steps = append(steps, Step{Cmd: ip("link", "set", "dev", e.Name, e.Action)})
		case "addr_replace":
			steps = append(steps, Step{Cmd: ip("addr", "replace", e.CIDR, "dev", e.Name)})
		case "addr_delete":
			steps = append(steps, Step{Idempotent: true, Cmd: ip("addr", "delete", e.CIDR, "dev", e.Name)})
		}
	}
	return steps
}

func planServiceNS(o *ServiceNS) []Step {
	ip := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.NS} }
	inside := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.Name} }
	hostIP := strings.SplitN(o.HostCIDR, "/", 2)[0]
	exists := ip("link", "show", "dev", o.HostIf)
	if o.Action == "delete" {
		// `type veth` makes ip refuse any other kind of device
		return []Step{{Probe: &exists, RunIfProbeOK: true, Cmd: ip("link", "delete", "dev", o.HostIf, "type", "veth")}}
	}
	nsThere := inside("link", "show", "dev", "lo")
	var steps []Step
	if o.recreate {
		// the namespace is the one of a holder that is gone: the pair that leads into it goes first,
		// then the name, so that the namespace is freed and the new holder's is attached
		steps = append(steps,
			Step{Probe: &exists, RunIfProbeOK: true, Cmd: ip("link", "delete", "dev", o.HostIf, "type", "veth")},
			Step{Cmd: Command{Tool: ToolIP, Args: []string{"netns", "delete", o.Name}}})
	}
	mk := Command{Tool: ToolIP, Args: []string{"netns", "add", o.Name}}
	if o.HolderPID > 0 {
		mk = Command{Tool: ToolIP, Args: []string{"netns", "attach", o.Name, strconv.Itoa(o.HolderPID)}}
	}
	return append(steps, []Step{
		// a pair whose namespace is gone (the name was lost, the holder's namespace lives on elsewhere) is
		// replaced: it would lead into the wrong namespace
		{Guard: &exists, Probe: &nsThere, RunIfProbeOK: false, Cmd: ip("link", "delete", "dev", o.HostIf, "type", "veth")},
		{Probe: &nsThere, RunIfProbeOK: false, Cmd: mk},
		{Probe: &exists, RunIfProbeOK: false, Cmd: ip("link", "add", o.HostIf, "type", "veth", "peer", "name", o.PeerIf, "netns", o.Name)},
		{Cmd: ip("addr", "replace", o.HostCIDR, "dev", o.HostIf)},
		{Cmd: ip("link", "set", "dev", o.HostIf, "up")},
		{Cmd: inside("link", "set", "dev", "lo", "up")},
		{Cmd: inside("addr", "replace", o.PeerCIDR, "dev", o.PeerIf)},
		{Cmd: inside("link", "set", "dev", o.PeerIf, "up")},
		{Cmd: inside("route", "replace", "default", "via", hostIP, "dev", o.PeerIf)},
	}...)
}

func planWireGuard(o *WireGuard) []Step {
	ip := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.NS} }
	exists := ip("link", "show", "dev", o.Name)
	if o.Action == "delete" {
		// the kind is checked by the executor first: `type wireguard` does not stop ip from deleting
		// a device of another kind
		return []Step{{Probe: &exists, RunIfProbeOK: true, Cmd: ip("link", "delete", "dev", o.Name, "type", "wireguard")}}
	}
	mtu := o.MTU
	if mtu == 0 {
		mtu = 1420
	}
	return []Step{
		{Probe: &exists, RunIfProbeOK: false, Cmd: ip("link", "add", "dev", o.Name, "type", "wireguard")},
		{Cmd: ip("link", "set", "dev", o.Name, "mtu", strconv.Itoa(mtu))},
		{NeedsConfig: true, Cmd: Command{Tool: ToolWg, Args: []string{"syncconf", o.Name, "/dev/stdin"}, NS: o.NS}},
	}
}
