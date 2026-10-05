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
	case *NftAddElements:
		return planElements("add", o.Target, o.Set, o.Elements, o.TimeoutSeconds)
	case *NftDelElements:
		return planElements("delete", o.Target, o.Set, o.Elements, 0)
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
	case *AssignInterfaces:
		return nil, nil // changes the executor's own scope, not the kernel
	}
	return nil, fmt.Errorf("no plan for %T", op)
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

func planTC(o *TC) []Step {
	var lines []string
	for _, e := range o.Entries {
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
		lines = append(lines, strings.Join(l, " "))
	}
	return []Step{{Cmd: Command{Tool: ToolTC, Args: []string{"-batch", "-"}, Stdin: strings.Join(lines, "\n") + "\n", NS: o.NS}}}
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
	case ReadRules:
		c.Tool, c.Args = ToolIP, []string{"-j", "rule", "show"}
	case ReadNft:
		c.Tool, c.Args = ToolNft, []string{"-j", "list", "table", NftFamily, NftTable}
	case ReadQdiscs, ReadClasses, ReadFilters:
		kind := map[string]string{ReadQdiscs: "qdisc", ReadClasses: "class", ReadFilters: "filter"}[o.What]
		c.Tool, c.Args = ToolTC, []string{"-j", kind, "show"}
		if o.Dev != "" {
			c.Args = append(c.Args, "dev", o.Dev)
		}
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
