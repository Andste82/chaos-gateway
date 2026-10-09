package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The IFB device and the flower filters of the tunnel faults (M10, plan §2.2.1): what the executor accepts of them, what it
// plans, and what it refuses. What the kernel does with them is in integration_ifb_test.go.

const (
	flowerSel      = `"protocol","ip","prio","1","flower","ip_proto","udp","src_ip","198.51.100.2","src_port","51820"`
	flowerIngress  = `{"object":"filter","action":"replace","dev":"wan0","parent":"ffff:","handle":"7","args":[` + flowerSel + `,"action","mirred","egress","redirect","dev","ifb-cgw"]}`
	flowerIFB      = `{"object":"filter","action":"replace","dev":"ifb-cgw","parent":"1:","handle":"7","args":[` + flowerSel + `,"flowid","1:1e"]}`
	deleteIngress  = `{"object":"filter","action":"delete","dev":"wan0","parent":"ffff:","handle":"7","args":["protocol","ip","prio","1","flower"]}`
	ingressQdiscOp = `{"object":"qdisc","action":"replace","dev":"wan0","parent":"ingress"}`
)

func TestTCAcceptsTheFlowerFiltersOfTheTunnelFaults(t *testing.T) {
	for name, e := range map[string]string{
		"redirect on the ingress":   flowerIngress,
		"select a class of the IFB": flowerIFB,
		"delete a flower filter":    deleteIngress,
		"the ingress qdisc":         ingressQdiscOp,
		"delete the ingress qdisc":  `{"object":"qdisc","action":"delete","dev":"wan0","parent":"ingress"}`,
		"the highest port":          strings.Replace(flowerIFB, `"51820"`, `"65535"`, 1),
	} {
		if _, err := Decode([]byte(tcEntries(e))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTCRejectsAFlowerFilterThatLeavesWhatTheTunnelFaultsNeed(t *testing.T) {
	mod := func(from, to string) string { return strings.Replace(flowerIngress, from, to, 1) }
	modIFB := func(from, to string) string { return strings.Replace(flowerIFB, from, to, 1) }
	for name, e := range map[string]string{
		// the redirect goes to the IFB and nowhere else
		"redirect to another device": mod(`"ifb-cgw"]`, `"wan0"]`),
		"redirect to a bridge":       mod(`"ifb-cgw"]`, `"br-lab"]`),
		"mirror instead":             mod(`"redirect"`, `"mirror"`),
		"ingress direction":          mod(`"egress","redirect"`, `"ingress","redirect"`),
		"redirect with a drop":       mod(`"redirect","dev","ifb-cgw"]`, `"redirect","dev","ifb-cgw","action","drop"]`),
		"no action at all":           mod(`,"action","mirred","egress","redirect","dev","ifb-cgw"]`, `]`),
		"a class on the ingress":     mod(`"action","mirred","egress","redirect","dev","ifb-cgw"`, `"flowid","1:1e"`),
		"a drop on the ingress":      mod(`"action","mirred","egress","redirect","dev","ifb-cgw"`, `"action","drop"`),
		// a class of the own tree and nothing else below the root
		"a class of another major":  modIFB(`"1:1e"`, `"8001:1"`),
		"no class":                  modIFB(`,"flowid","1:1e"]`, `]`),
		"a redirect below the root": modIFB(`"flowid","1:1e"`, `"action","mirred","egress","redirect","dev","ifb-cgw"`),
		"classid":                   modIFB(`"flowid"`, `"classid"`),
		// the selector is the outer UDP of one peer
		"tcp":              mod(`"udp"`, `"tcp"`),
		"no ip_proto":      mod(`"ip_proto","udp",`, ``),
		"a destination":    mod(`"src_port","51820"`, `"dst_port","51820"`),
		"a prefix":         mod(`"198.51.100.2"`, `"198.51.100.0/24"`),
		"an ipv6 address":  mod(`"198.51.100.2"`, `"2001:db8::1"`),
		"a host name":      mod(`"198.51.100.2"`, `"example.org"`),
		"port zero":        mod(`"51820"`, `"0"`),
		"port 65536":       mod(`"51820"`, `"65536"`),
		"a port range":     mod(`"51820"`, `"51820-51830"`),
		"another protocol": mod(`"ip","prio"`, `"all","prio"`),
		"an extra key":     mod(`"src_port","51820"`, `"src_port","51820","dst_ip","10.0.0.1"`),
		"a skip option":    mod(`"flower"`, `"flower","skip_hw"`),
		// the handle names the filter: a decimal number
		"a hex handle":      strings.Replace(flowerIngress, `"handle":"7"`, `"handle":"0x7"`, 1),
		"a mark handle":     strings.Replace(flowerIngress, `"handle":"7"`, `"handle":"0x00010/0x1fff0"`, 1),
		"handle zero":       strings.Replace(flowerIngress, `"handle":"7"`, `"handle":"0"`, 1),
		"no handle":         strings.Replace(flowerIngress, `"handle":"7",`, ``, 1),
		"a delete by hex":   strings.Replace(deleteIngress, `"handle":"7"`, `"handle":"0xa0"`, 1),
		"a delete of an fw": strings.Replace(deleteIngress, `"flower"`, `"bpf"`, 1),
		// the parent
		"flower below the mq":   strings.Replace(flowerIFB, `"parent":"1:"`, `"parent":"8001:"`, 1),
		"flower below clsact":   strings.Replace(flowerIngress, `"parent":"ffff:"`, `"parent":"clsact"`, 1),
		"the ingress with args": `{"object":"qdisc","action":"replace","dev":"wan0","parent":"ingress","args":["netem"]}`,
	} {
		op, err := Decode([]byte(tcEntries(e)))
		if err == nil {
			t.Errorf("%s: accepted %T", name, op)
		} else if !errors.Is(err, ErrDecode) {
			t.Errorf("%s: %v does not wrap ErrDecode", name, err)
		}
	}
}

func TestTheLinesOfTheFlowerFiltersAreWhatTheKernelTakes(t *testing.T) {
	steps := mustPlan(t, tcEntries(ingressQdiscOp, flowerIngress, flowerIFB, deleteIngress,
		`{"object":"qdisc","action":"delete","dev":"wan0","parent":"ingress"}`))
	if len(steps) != 2 {
		t.Fatalf("%d steps", len(steps))
	}
	want := `qdisc replace dev wan0 ingress
filter replace dev wan0 parent ffff: handle 7 protocol ip prio 1 flower ip_proto udp src_ip 198.51.100.2 src_port 51820 action mirred egress redirect dev ifb-cgw
filter replace dev ifb-cgw parent 1: handle 7 protocol ip prio 1 flower ip_proto udp src_ip 198.51.100.2 src_port 51820 flowid 1:1e
`
	if steps[0].Cmd.Stdin != want || steps[0].Idempotent {
		t.Errorf("create step:\n%s", steps[0].Cmd.Stdin)
	}
	wantDel := `filter delete dev wan0 parent ffff: handle 7 protocol ip prio 1 flower
qdisc delete dev wan0 ingress
`
	if steps[1].Cmd.Stdin != wantDel || !steps[1].Idempotent {
		t.Errorf("delete step:\n%s", steps[1].Cmd.Stdin)
	}
	for _, s := range steps {
		for _, l := range strings.Split(strings.TrimSpace(s.Cmd.Stdin), "\n") {
			checkTCLineInScope(t, l)
		}
	}
}

// The mirred target is an interface name inside the arguments: the scope sees it, too. The IFB has to be assigned like any
// interface of the entries.
func TestTheFlowerFiltersNeedBothInterfacesToBeAssigned(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	do := func(op string) error {
		_, err := e.Do(context.Background(), mustDecode(t, op))
		return err
	}
	if err := do(`{"type":"assign_interfaces","devs":["wan0"],"os_owned":["wan0"]}`); err != nil {
		t.Fatal(err)
	}
	err := do(tcEntries(flowerIngress))
	if !errors.Is(err, ErrOutOfScope) || !strings.Contains(err.Error(), "ifb-cgw") {
		t.Errorf("a redirect to an interface that is not assigned: %v", err)
	}
	if err := do(tcEntries(ingressQdiscOp)); err != nil {
		t.Errorf("the ingress qdisc of the OS-owned uplink: %v", err)
	}
	if err := do(`{"type":"assign_interfaces","devs":["wan0","ifb-cgw"],"os_owned":["wan0"]}`); err != nil {
		t.Fatal(err)
	}
	if err := do(tcEntries(flowerIngress, flowerIFB)); err != nil {
		t.Errorf("both assigned: %v", err)
	}
}

func TestTheIFBActionsTakeTheNameOfTheIFBOnly(t *testing.T) {
	for name, e := range map[string]string{
		"add":    `{"action":"add_ifb","name":"ifb-cgw"}`,
		"delete": `{"action":"delete_ifb","name":"ifb-cgw"}`,
		"up":     `{"action":"up","name":"ifb-cgw"}`,
	} {
		if _, err := Decode([]byte(`{"type":"links","entries":[` + e + `]}`)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, e := range map[string]string{
		"add another":    `{"action":"add_ifb","name":"ifb0"}`,
		"delete another": `{"action":"delete_ifb","name":"wan0"}`,
		"add a bridge":   `{"action":"add_ifb","name":"br-lan0"}`,
		"master":         `{"action":"add_ifb","name":"ifb-cgw","master":"br-lan0"}`,
		"cidr":           `{"action":"add_ifb","name":"ifb-cgw","cidr":"10.0.0.1/24"}`,
	} {
		if op, err := Decode([]byte(`{"type":"links","entries":[` + e + `]}`)); err == nil {
			t.Errorf("%s: accepted %T", name, op)
		}
	}
	steps := mustPlan(t, `{"type":"links","namespace":"gw","entries":[{"action":"add_ifb","name":"ifb-cgw"},{"action":"up","name":"ifb-cgw"},{"action":"delete_ifb","name":"ifb-cgw"}]}`)
	var got []string
	for _, s := range steps {
		line := s.Cmd.String()
		if s.Probe != nil {
			line = fmt.Sprintf("if %v %s: %s", s.RunIfProbeOK, s.Probe, line)
		}
		got = append(got, line)
	}
	want := []string{
		"if false [gw] ip link show dev ifb-cgw: [gw] ip link add name ifb-cgw type ifb",
		"[gw] ip link set dev ifb-cgw up",
		"if true [gw] ip link show dev ifb-cgw: [gw] ip link delete dev ifb-cgw type ifb",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// `ip link delete dev X type ifb` deletes a WireGuard interface (found in the VM while M10 was built): the executor reads the
// kind first, as it does for the bridge.
func TestDeleteIFBNeverDeletesAnotherKindOfDevice(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolIP && len(c.Args) > 3 && c.Args[0] == "-j" && c.Args[len(c.Args)-1] == "ifb-cgw" {
			return Result{Stdout: `[{"ifindex":4,"ifname":"ifb-cgw","flags":["UP"],"link_type":"ether","linkinfo":{"info_kind":"wireguard"}}]`}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["ifb-cgw"]}`)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(ctx, mustDecode(t, `{"type":"links","entries":[{"action":"delete_ifb","name":"ifb-cgw"}]}`))
	if err == nil || !strings.Contains(err.Error(), "not a ifb") {
		t.Fatalf("a WireGuard interface must not be deleted as an IFB: %v", err)
	}
	for _, c := range fr.commands() {
		if len(c.Args) > 1 && c.Args[0] == "link" && c.Args[1] == "delete" {
			t.Fatalf("the delete ran: %s", c)
		}
	}
}

func TestIFBActionsNeedTheDeviceToBeAssignedAndNotOSOwned(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	ctx := context.Background()
	_, err := e.Do(ctx, mustDecode(t, `{"type":"links","entries":[{"action":"add_ifb","name":"ifb-cgw"}]}`))
	if !errors.Is(err, ErrOutOfScope) {
		t.Errorf("an IFB that is not assigned: %v", err)
	}
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["ifb-cgw"],"os_owned":["ifb-cgw"]}`)); err != nil {
		t.Fatal(err)
	}
	_, err = e.Do(ctx, mustDecode(t, `{"type":"links","entries":[{"action":"add_ifb","name":"ifb-cgw"}]}`))
	if !errors.Is(err, ErrOutOfScope) {
		t.Errorf("an IFB the host owns: %v", err)
	}
}

// `filter show` lists the filters of the egress side only: a device with an ingress qdisc gets the listing of the ingress
// filters as well, one without does not (the listing is a tool run).
func TestTheTCReadListsTheIngressFiltersOfADeviceThatHasAnIngressQdisc(t *testing.T) {
	var ingressRuns int
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool != ToolTC {
			return Result{}, nil
		}
		line := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(line, "qdisc show"):
			return Result{Stdout: `[{"kind":"noqueue","handle":"0:","root":true,"refcnt":2,"options":{}},{"kind":"ingress","handle":"ffff:","parent":"ffff:fff1","options":{}}]`}, nil
		case strings.HasSuffix(line, "filter show dev wan0 ingress"):
			ingressRuns++
			return Result{Stdout: `[{"parent":"ffff:","protocol":"ip","pref":10,"kind":"flower","chain":0},{"parent":"ffff:","protocol":"ip","pref":10,"kind":"flower","chain":0,"options":{"handle":7,"keys":{"eth_type":"ipv4","ip_proto":"udp","src_ip":"198.51.100.2","src_port":51820},"not_in_hw":true,"actions":[{"order":1,"kind":"mirred","mirred_action":"redirect","direction":"egress","to_dev":"ifb-cgw","control_action":{"type":"stolen"},"index":2,"ref":1,"bind":1,"installed":3,"last_used":0,"stats":{"bytes":180,"packets":3,"drops":0,"overlimits":0,"requeues":0,"backlog":0,"qlen":0}}]}}]`}, nil
		case strings.Contains(line, "filter show"), strings.Contains(line, "class show"):
			return Result{Stdout: `[]`}, nil
		}
		return Result{Exit: 1}, nil
	}}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"tc","dev":"wan0"}`))
	if err != nil {
		t.Fatal(err)
	}
	var tree struct {
		Filters []struct {
			Parent string
			Kind   string
			Flower *struct {
				Handle   int
				SrcIP    string `json:"src_ip"`
				SrcPort  int    `json:"src_port"`
				Redirect string
			}
			Stats *struct{ Packets uint64 }
		}
	}
	if err := jsonUnmarshal(out.Data[0], &tree); err != nil {
		t.Fatal(err)
	}
	if ingressRuns != 1 || len(tree.Filters) != 1 || tree.Filters[0].Parent != "ingress" || tree.Filters[0].Flower == nil ||
		tree.Filters[0].Flower.Handle != 7 || tree.Filters[0].Flower.SrcIP != "198.51.100.2" || tree.Filters[0].Flower.SrcPort != 51820 ||
		tree.Filters[0].Flower.Redirect != "ifb-cgw" || tree.Filters[0].Stats == nil || tree.Filters[0].Stats.Packets != 3 {
		t.Fatalf("%d runs, %+v", ingressRuns, tree)
	}
}
