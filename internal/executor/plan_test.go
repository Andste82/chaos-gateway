package executor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, in string) Operation {
	t.Helper()
	op, err := Decode([]byte(in))
	if err != nil {
		t.Fatalf("%v\n%s", err, in)
	}
	return op
}

func mustPlan(t *testing.T, in string) []Step {
	t.Helper()
	steps, err := Plan(mustDecode(t, in))
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

func TestPlanNftApplyPipesTheRulesetToNft(t *testing.T) {
	steps := mustPlan(t, `{"type":"nft_apply","namespace":"gw","ruleset":`+goodNft+`}`)
	if len(steps) != 1 {
		t.Fatalf("%d steps", len(steps))
	}
	c := steps[0].Cmd
	if c.Tool != ToolNft || strings.Join(c.Args, " ") != "-j -f -" || c.NS != "gw" {
		t.Fatalf("%v", c)
	}
	if !json.Valid([]byte(c.Stdin)) || !strings.Contains(c.Stdin, `"chaosgw"`) {
		t.Fatalf("stdin: %s", c.Stdin)
	}
}

func TestPlanAddElements(t *testing.T) {
	steps := mustPlan(t, `{"type":"nft_add_elements","set":"dns_a","elements":["192.0.2.1","10.0.0.0/8"],"timeout_seconds":90}`)
	var doc struct {
		Nftables []struct {
			Add struct {
				Element struct {
					Family, Table, Name string
					Elem                []map[string]any
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(steps[0].Cmd.Stdin), &doc); err != nil {
		t.Fatal(err)
	}
	el := doc.Nftables[0].Add.Element
	if el.Family != "inet" || el.Table != "chaosgw" || el.Name != "dns_a" || len(el.Elem) != 2 {
		t.Fatalf("%+v", el)
	}
	first := el.Elem[0]["elem"].(map[string]any)
	if first["val"] != "192.0.2.1" || first["timeout"] != float64(90) {
		t.Errorf("first element: %v", first)
	}
	second := el.Elem[1]["elem"].(map[string]any)["val"].(map[string]any)["prefix"].(map[string]any)
	if second["addr"] != "10.0.0.0" || second["len"] != float64(8) {
		t.Errorf("prefix element: %v", second)
	}
	// without a timeout the elements are plain values
	plain := mustPlan(t, `{"type":"nft_add_elements","set":"s","elements":["aa:bb:cc:dd:ee:ff"]}`)[0].Cmd.Stdin
	if !strings.Contains(plain, `"elem":["aa:bb:cc:dd:ee:ff"]`) {
		t.Errorf("plain elements: %s", plain)
	}
}

func TestPlanRoutingGolden(t *testing.T) {
	steps := mustPlan(t, `{"type":"routing","namespace":"gw","routes":[
	  {"action":"replace","family":4,"table":100,"dst":"default","via":"10.0.0.1","dev":"wan0","metric":10},
	  {"action":"replace","family":4,"table":102,"dst":"10.9.0.0/16","type":"prohibit"},
	  {"action":"delete","family":4,"table":101,"dst":"10.8.0.0/16","dev":"lan0"},
	  {"action":"replace","family":6,"table":100,"dst":"::/0","via":"2001:db8::1"}],
	 "rules":[
	  {"action":"add","family":4,"priority":1000,"from":"10.10.0.0/24","fwmark":"0x10/0xff","iif":"lan0","table":100},
	  {"action":"delete","family":4,"priority":1001,"to":"203.0.113.0/24","oif":"wan0","table":101}]}`)
	if len(steps) != 2 {
		t.Fatalf("one batch per family expected, got %d", len(steps))
	}
	want4 := `route replace default table 100 proto 201 via 10.0.0.1 dev wan0 metric 10
route replace prohibit 10.9.0.0/16 table 102 proto 201
rule add priority 1000 from 10.10.0.0/24 fwmark 0x10/0xff iif lan0 table 100 protocol 201
rule del priority 1001 to 203.0.113.0/24 oif wan0 table 101 protocol 201
route del 10.8.0.0/16 table 101 proto 201 dev lan0
`
	if got := steps[0].Cmd; got.Tool != ToolIP || strings.Join(got.Args, " ") != "-4 -force -batch -" || got.Stdin != want4 || got.NS != "gw" {
		t.Errorf("IPv4 batch:\n%v\n%s", got.Args, got.Stdin)
	}
	want6 := "route replace ::/0 table 100 proto 201 via 2001:db8::1\n"
	if got := steps[1].Cmd; strings.Join(got.Args, " ") != "-6 -force -batch -" || got.Stdin != want6 {
		t.Errorf("IPv6 batch:\n%v\n%s", got.Args, got.Stdin)
	}
}

func TestEveryRoutingLineCarriesTheProtocolTag(t *testing.T) {
	for _, step := range mustPlan(t, `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","dev":"wan0"},{"action":"delete","family":4,"table":100,"dst":"10.0.0.0/8"}],"rules":[{"action":"delete","family":4,"priority":5,"table":100}]}`) {
		for _, line := range strings.Split(strings.TrimSpace(step.Cmd.Stdin), "\n") {
			if !strings.Contains(line, " proto 201") && !strings.Contains(line, " protocol 201") {
				t.Errorf("line without the protocol tag: %q", line)
			}
		}
	}
}

func TestPlanTCGolden(t *testing.T) {
	steps := mustPlan(t, `{"type":"tc","namespace":"gw","entries":[
	  {"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","10"]},
	  {"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:10","args":["htb","rate","1mbit"]},
	  {"object":"qdisc","action":"replace","dev":"wan0","parent":"1:10","handle":"10:","args":["netem","delay","50ms","loss","1%"]},
	  {"object":"filter","action":"add","dev":"wan0","parent":"1:","handle":"0x10","args":["protocol","ip","prio","1","fw","flowid","1:10"]},
	  {"object":"qdisc","action":"add","dev":"lan0","parent":"ingress"},
	  {"object":"qdisc","action":"delete","dev":"wan0","parent":"root"}]}`)
	want := `qdisc replace dev wan0 root handle 1: htb default 10
class replace dev wan0 parent 1: classid 1:10 htb rate 1mbit
qdisc replace dev wan0 parent 1:10 handle 10: netem delay 50ms loss 1%
filter add dev wan0 parent 1: handle 0x10 protocol ip prio 1 fw flowid 1:10
qdisc add dev lan0 ingress
qdisc delete dev wan0 root
`
	if len(steps) != 1 || steps[0].Cmd.Tool != ToolTC || strings.Join(steps[0].Cmd.Args, " ") != "-batch -" || steps[0].Cmd.Stdin != want || steps[0].Cmd.NS != "gw" {
		t.Fatalf("%+v\n%s", steps[0].Cmd, steps[0].Cmd.Stdin)
	}
}

func TestPlanOffloadsAndDockerUser(t *testing.T) {
	steps := mustPlan(t, `{"type":"offloads","devs":["wan0","lan0"]}`)
	if len(steps) != 2 || strings.Join(steps[0].Cmd.Args, " ") != "-K wan0 gro off gso off tso off lro off" {
		t.Fatalf("%+v", steps)
	}

	ens := mustPlan(t, `{"type":"docker_user","action":"ensure","devs":["br-lan0"]}`)
	if len(ens) != 2 {
		t.Fatalf("one step per direction expected: %d", len(ens))
	}
	if got := strings.Join(ens[0].Probe.Args, " "); got != "-w 5 -C DOCKER-USER -i br-lan0 -m comment --comment chaosgw -j ACCEPT" {
		t.Errorf("probe: %s", got)
	}
	if got := strings.Join(ens[0].Cmd.Args, " "); got != "-w 5 -I DOCKER-USER 1 -i br-lan0 -m comment --comment chaosgw -j ACCEPT" {
		t.Errorf("insert: %s", got)
	}
	if ens[0].RunIfProbeOK || !strings.Contains(strings.Join(ens[1].Cmd.Args, " "), "-o br-lan0") {
		t.Errorf("ensure inserts only when the rule is absent, for both directions: %+v", ens)
	}
	rem := mustPlan(t, `{"type":"docker_user","action":"remove","devs":["br-lan0"]}`)
	if !rem[0].RunIfProbeOK || !strings.Contains(strings.Join(rem[0].Cmd.Args, " "), "-D DOCKER-USER") {
		t.Errorf("remove deletes only when the rule is present: %+v", rem[0])
	}
	for _, s := range append(ens, rem...) {
		if s.Cmd.Tool != ToolIptables || !strings.Contains(strings.Join(s.Cmd.Args, " "), DockerUserChain) {
			t.Errorf("a docker_user step outside DOCKER-USER: %+v", s)
		}
	}
}

func TestReadCommands(t *testing.T) {
	for in, want := range map[string]string{
		`{"type":"read","what":"links"}`:                             "ip -j -d link show",
		`{"type":"read","what":"links","dev":"lan0"}`:                "ip -j -d link show dev lan0",
		`{"type":"read","what":"addrs"}`:                             "ip -j addr show",
		`{"type":"read","what":"routes"}`:                            "ip -j route show table all",
		`{"type":"read","what":"routes","table":"100","dev":"wan0"}`: "ip -j route show table 100 dev wan0",
		`{"type":"read","what":"rules"}`:                             "ip -j rule show",
		`{"type":"read","what":"nft"}`:                               "nft -j list table inet chaosgw",
		`{"type":"read","what":"qdiscs","dev":"wan0"}`:               "tc -j qdisc show dev wan0",
		`{"type":"read","what":"classes"}`:                           "tc -j class show",
		`{"type":"read","what":"filters","dev":"wan0"}`:              "tc -j filter show dev wan0",
		`{"type":"read","what":"offloads","dev":"wan0"}`:             "ethtool -k wan0",
		`{"type":"read","namespace":"gw","what":"rules"}`:            "[gw] ip -j rule show",
	} {
		if got := ReadCommand(mustDecode(t, in).(*Read)).String(); got != want {
			t.Errorf("%s:\n got %q\nwant %q", in, got, want)
		}
	}
}

// Whatever the decoder accepts, no command may contain a way out of its argument: no argument
// has whitespace or control characters, and stdin lines of batches start with a known verb.
func TestPlannedCommandsAreInert(t *testing.T) {
	for _, in := range []string{
		`{"type":"offloads","devs":["wan0"]}`,
		`{"type":"docker_user","action":"ensure","devs":["a.b-c_d"]}`,
		`{"type":"read","what":"routes","table":"main","dev":"wan0"}`,
	} {
		op := mustDecode(t, in)
		var cmds []Command
		if r, ok := op.(*Read); ok {
			cmds = append(cmds, ReadCommand(r))
		} else {
			steps, _ := Plan(op)
			for _, s := range steps {
				cmds = append(cmds, s.Cmd)
				if s.Probe != nil {
					cmds = append(cmds, *s.Probe)
				}
			}
		}
		for _, c := range cmds {
			for _, a := range c.Args {
				if strings.ContainsAny(a, " \t\r\n\x00;|&$`<>\"'\\") {
					t.Errorf("argument %q of %s is not inert", a, c)
				}
			}
		}
	}
}

func TestPlanLinksGolden(t *testing.T) {
	steps := mustPlan(t, `{"type":"links","namespace":"gw","entries":[
	  {"action":"add_bridge","name":"br-lan0"},{"action":"enslave","name":"lan0","master":"br-lan0"},
	  {"action":"up","name":"br-lan0"},{"action":"addr_replace","name":"br-lan0","cidr":"10.10.0.1/24"},
	  {"action":"addr_delete","name":"br-lan0","cidr":"10.9.0.1/24"},{"action":"release","name":"lan1"},
	  {"action":"down","name":"lan1"},{"action":"delete_bridge","name":"br-old"}]}`)
	var got []string
	for _, s := range steps {
		line := s.Cmd.String()
		if s.Probe != nil {
			line = fmt.Sprintf("if %v %s: %s", s.RunIfProbeOK, s.Probe, line)
		}
		got = append(got, line)
	}
	want := []string{
		"if false [gw] ip link show dev br-lan0: [gw] ip link add name br-lan0 type bridge",
		"[gw] ip link set dev lan0 master br-lan0",
		"[gw] ip link set dev br-lan0 up",
		"[gw] ip addr replace 10.10.0.1/24 dev br-lan0",
		"[gw] ip addr delete 10.9.0.1/24 dev br-lan0",
		"[gw] ip link set dev lan1 nomaster",
		"[gw] ip link set dev lan1 down",
		"if true [gw] ip link show dev br-old: [gw] ip link delete dev br-old type bridge",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !steps[4].Idempotent {
		t.Error("deleting an address that is gone must not fail")
	}
}

func TestPlanSysctlAndReads(t *testing.T) {
	steps := mustPlan(t, `{"type":"sysctl","entries":[{"name":"ip_forward","value":1},{"name":"accept_ra","dev":"br.1","value":0}]}`)
	if len(steps) != 2 || strings.Join(steps[0].Cmd.Args, " ") != "-w net/ipv4/ip_forward=1" || strings.Join(steps[1].Cmd.Args, " ") != "-w net/ipv6/conf/br.1/accept_ra=0" {
		t.Fatalf("%+v", steps)
	}
	for in, want := range map[string]string{
		`{"type":"read","what":"sysctl","name":"accept_ra","dev":"br-lan0"}`: "sysctl -n net/ipv6/conf/br-lan0/accept_ra",
		`{"type":"read","what":"docker_user"}`:                               "iptables -w 5 -S DOCKER-USER",
	} {
		if got := ReadCommand(mustDecode(t, in).(*Read)).String(); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

func TestOptionalDockerChainIsGuarded(t *testing.T) {
	steps := mustPlan(t, `{"type":"docker_user","action":"ensure","devs":["lan0"],"optional_chain":true}`)
	for _, s := range steps {
		if s.Guard == nil || strings.Join(s.Guard.Args, " ") != "-w 5 -S DOCKER-USER" {
			t.Fatalf("step without a guard: %+v", s)
		}
	}
	for _, s := range mustPlan(t, `{"type":"docker_user","action":"ensure","devs":["lan0"]}`) {
		if s.Guard != nil {
			t.Fatal("a required chain has no guard")
		}
	}
}

const goodServiceNS = `{"type":"service_ns","action":"ensure","namespace":"gw","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30"`

func TestPlanServiceNamespaceGolden(t *testing.T) {
	steps := mustPlan(t, goodServiceNS+`}`)
	var got []string
	for _, s := range steps {
		line := s.Cmd.String()
		if s.Probe != nil {
			line = fmt.Sprintf("if probe %q %v: %s", s.Probe.String(), s.RunIfProbeOK, line)
		}
		got = append(got, line)
	}
	want := []string{
		`if probe "[cgsvc] ip link show dev lo" false: ip netns add cgsvc`,
		`if probe "[gw] ip link show dev svc0" false: [gw] ip link add svc0 type veth peer name svc1 netns cgsvc`,
		`[gw] ip addr replace 169.254.100.1/30 dev svc0`,
		`[gw] ip link set dev svc0 up`,
		`[cgsvc] ip link set dev lo up`,
		`[cgsvc] ip addr replace 169.254.100.2/30 dev svc1`,
		`[cgsvc] ip link set dev svc1 up`,
		`[cgsvc] ip route replace default via 169.254.100.1 dev svc1`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// a holder container's namespace is attached instead of created
	steps = mustPlan(t, goodServiceNS+`,"holder_pid":77}`)
	if c := steps[0].Cmd.String(); c != "ip netns attach cgsvc 77" {
		t.Errorf("%s", c)
	}
	// delete only removes a veth
	steps = mustPlan(t, `{"type":"service_ns","action":"delete","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30"}`)
	if len(steps) != 1 || steps[0].Cmd.String() != "ip link delete dev svc0 type veth" || !steps[0].RunIfProbeOK {
		t.Errorf("%+v", steps)
	}
}

func TestServiceNamespaceRefusesWhatIsNotLinkLocal(t *testing.T) {
	for name, mut := range map[string]string{
		"routable host address": `"host_cidr":"10.0.0.1/30"`,
		"routable peer":         `"peer_cidr":"192.0.2.2/30"`,
		"different subnets":     `"peer_cidr":"169.254.101.2/30"`,
		"same address":          `"peer_cidr":"169.254.100.1/30"`,
		"wide prefix":           `"host_cidr":"169.254.100.1/16","peer_cidr":"169.254.100.2/16"`,
		"same names":            `"peer_if":"svc0"`,
		"bad namespace":         `"name":"a b"`,
		"negative pid":          `"holder_pid":-1`,
		"namespace is target":   `"name":"gw"`,
	} {
		in := goodServiceNS + `}`
		// the later key wins in Go, but duplicates are refused: replace the field instead
		in = strings.Replace(in, `"name":"cgsvc"`, `"name":"cgsvc"`, 1)
		var m map[string]any
		_ = json.Unmarshal([]byte(in), &m)
		var over map[string]any
		_ = json.Unmarshal([]byte("{"+mut+"}"), &over)
		for k, v := range over {
			m[k] = v
		}
		raw, _ := json.Marshal(m)
		if _, err := Decode(raw); err == nil {
			t.Errorf("%s: accepted\n%s", name, raw)
		}
	}
}
