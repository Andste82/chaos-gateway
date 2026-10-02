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
	ToolIP       Tool = "ip"
	ToolNft      Tool = "nft"
	ToolTC       Tool = "tc"
	ToolEthtool  Tool = "ethtool"
	ToolIptables Tool = "iptables"
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
		return planAddElements(o)
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
	case *AssignInterfaces:
		return nil, nil // changes the executor's own scope, not the kernel
	}
	return nil, fmt.Errorf("no plan for %T", op)
}

func planAddElements(o *NftAddElements) ([]Step, error) {
	elems := make([]any, len(o.Elements))
	for i, e := range o.Elements {
		var val any = e
		if p, err := netip.ParsePrefix(e); err == nil {
			val = map[string]any{"prefix": map[string]any{"addr": p.Addr().String(), "len": p.Bits()}}
		}
		if o.TimeoutSeconds > 0 {
			val = map[string]any{"elem": map[string]any{"val": val, "timeout": o.TimeoutSeconds}}
		}
		elems[i] = val
	}
	doc := map[string]any{"nftables": []any{map[string]any{"add": map[string]any{"element": map[string]any{
		"family": NftFamily, "table": NftTable, "name": o.Set, "elem": elems,
	}}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(b), NS: o.NS}}}, nil
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
	}
	return c
}
