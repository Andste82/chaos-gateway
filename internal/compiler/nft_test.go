package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func chainOf(t *testing.T, tg *Target, name string) Chain {
	t.Helper()
	for _, c := range tg.Nft.Chains {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no chain %s", name)
	return Chain{}
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestGatewayProtectionOnlyAnswersDHCPDNSAndPing(t *testing.T) {
	tg := compileBasic(t, nil)
	in := chainOf(t, tg, "input")
	if in.Base == nil || in.Base.Hook != "input" || in.Base.Policy != "accept" {
		t.Fatalf("the input chain accepts by default: other traffic (uplink, management, Docker) is not ours: %+v", in.Base)
	}
	var verdicts []string
	anti := -1
	drop := -1
	for i, r := range in.Rules {
		s := js(r.Expr)
		verdicts = append(verdicts, s)
		if strings.Contains(s, `"dport"`) && strings.Contains(s, "mgmt_src") && strings.Contains(s, "22,443") {
			anti = i
		}
		if strings.Contains(s, "input_drop") {
			drop = i
		}
	}
	if anti < 0 || drop < 0 || anti > drop {
		t.Fatalf("the anti-lockout rule (SSH and the UI port from the management sources) must come before the drop: %d %d\n%v", anti, drop, verdicts)
	}
	// the drop applies to the test interfaces only
	if !strings.Contains(js(in.Rules[drop].Expr), "ifs_test") {
		t.Errorf("drop rule: %s", js(in.Rules[drop].Expr))
	}
	// what test networks may reach: ICMP echo, UDP 67, UDP 53, TCP 53
	allowed := map[string]bool{}
	for _, r := range in.Rules {
		s := js(r.Expr)
		if !strings.Contains(s, "ifs_test") || !strings.Contains(s, "accept") {
			continue
		}
		switch {
		case strings.Contains(s, `"echo-request"`):
			allowed["icmp"] = true
		case strings.Contains(s, `"udp"`) && strings.Contains(s, `"right":67`):
			allowed["udp67"] = true
		case strings.Contains(s, `"udp"`) && strings.Contains(s, `"right":53`):
			allowed["udp53"] = true
		case strings.Contains(s, `"tcp"`) && strings.Contains(s, `"right":53`):
			allowed["tcp53"] = true
		default:
			t.Errorf("an unexpected accept for test networks: %s", s)
		}
	}
	if len(allowed) != 4 {
		t.Errorf("allowed %v", allowed)
	}
}

func TestTheUIPortComesFromTheConfiguration(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) { cfg.Management.UiPort = ptr(8443) })
	if !strings.Contains(js(chainOf(t, tg, "input").Rules), "22,8443") {
		t.Error("the configured UI port is not in the anti-lockout rule")
	}
	if tg.Management.UIPort != 8443 {
		t.Error(tg.Management)
	}
}

func TestForwardingDefaultsDropEverythingButTestToUplink(t *testing.T) {
	tg := compileBasic(t, nil)
	fw := chainOf(t, tg, "forward")
	if fw.Base.Policy != "accept" {
		t.Fatalf("the chain accepts by default: Docker's and other traffic is none of our business")
	}
	var order []string
	for _, r := range fw.Rules {
		s := js(r.Expr)
		switch {
		case strings.Contains(s, `"established"`):
			order = append(order, "established")
		case strings.Contains(s, `"invalid"`):
			order = append(order, "invalid")
		case strings.Contains(s, `"ipv6"`):
			order = append(order, "ipv6")
		case strings.Contains(s, `"wan0"`) && strings.Contains(s, "accept"):
			order = append(order, "test->uplink")
		case strings.Contains(s, "forward_drop") && strings.Contains(s, `"iifname"`):
			order = append(order, "drop-from-test")
		case strings.Contains(s, "forward_drop") && strings.Contains(s, `"oifname"`):
			order = append(order, "drop-to-test")
		default:
			order = append(order, "?"+s)
		}
	}
	want := "established invalid invalid ipv6 ipv6 test->uplink drop-from-test drop-to-test"
	if strings.Join(order, " ") != want {
		t.Errorf("forward chain order:\n%s\nwant\n%s", strings.Join(order, " "), want)
	}
}

func TestIPv6IsDroppedInBothDirectionsOfTestNetworks(t *testing.T) {
	fw := chainOf(t, compileBasic(t, nil), "forward")
	var in, out bool
	for _, r := range fw.Rules {
		s := js(r.Expr)
		if strings.Contains(s, `"nfproto"`) {
			in = in || strings.Contains(s, `"iifname"`)
			out = out || strings.Contains(s, `"oifname"`)
			if !strings.Contains(s, "ifs_test") {
				t.Errorf("the IPv6 drop must stay on test interfaces: %s", s)
			}
		}
	}
	if !in || !out {
		t.Error("IPv6 must be dropped from and to test networks")
	}
}

func TestMasqueradePerNetwork(t *testing.T) {
	tg := compileBasic(t, nil)
	post := chainOf(t, tg, "postrouting")
	if post.Base.Type != "nat" || post.Base.Hook != "postrouting" || post.Base.Prio != 100 {
		t.Fatalf("%+v", post.Base)
	}
	// the Lab network has nat: false
	if len(post.Rules) != 1 || !strings.Contains(js(post.Rules[0].Expr), `"addr":"10.10.0.0"`) || !strings.Contains(js(post.Rules[0].Expr), `"wan0"`) || !strings.Contains(js(post.Rules[0].Expr), "masquerade") {
		t.Fatalf("%s", js(post.Rules))
	}
	if !contains(tg.Nft.Counters, "nat_"+iotID[:8]) || contains(tg.Nft.Counters, "nat_"+labID[:8]) {
		t.Errorf("counters %v", tg.Nft.Counters)
	}
	// nat is on by default
	tg = compileBasic(t, func(cfg *model.Configuration, h *Host) {
		n := (*cfg.Networks)[labID]
		lan, _ := n.AsLanNetwork()
		lan.Nat = nil
		_ = n.FromLanNetwork(lan)
		(*cfg.Networks)[labID] = n
	})
	if len(chainOf(t, tg, "postrouting").Rules) != 2 {
		t.Error("masquerade is on by default for test networks")
	}
}

func TestSetNamesCarryTheHashOfTheirDefinition(t *testing.T) {
	tg := compileBasic(t, nil)
	names := map[string]SetDef{}
	for _, s := range tg.Nft.Sets {
		names[s.Name] = s
	}
	a := hashName("ifs_test", "ifname", nil)
	b := hashName("ifs_test", "ifname", []string{"interval"})
	c := hashName("ifs_test", "ipv4_addr", nil)
	if a == b || a == c || b == c {
		t.Fatalf("different definitions, the same name: %s %s %s", a, b, c)
	}
	if hashName("ifs_test", "ifname", nil) != a {
		t.Fatal("the name is not stable")
	}
	if _, ok := names[a]; !ok {
		t.Errorf("sets %v do not contain %s", names, a)
	}
	// the elements are content, not definition: the name does not change with them
	other := compileBasic(t, func(cfg *model.Configuration, h *Host) { delete(*cfg.Networks, labID) })
	for _, s := range other.Nft.Sets {
		if strings.HasPrefix(s.Name, "ifs_test") && s.Name != a {
			t.Errorf("the name of ifs_test changed with its elements: %s", s.Name)
		}
	}
}

func matrixEntry(from, to model.MatrixEndpoint, p model.MatrixEntryPolicy) model.MatrixEntry {
	return model.MatrixEntry{From: from, To: to, Policy: p}
}

func lanEP(id string) model.MatrixEndpoint { return model.MatrixEndpoint{Network: ptr(id)} }
func uplinkEP() model.MatrixEndpoint {
	u := model.MatrixEndpointUplink(true)
	return model.MatrixEndpoint{Uplink: &u}
}
func mgmtEP() model.MatrixEndpoint {
	m := model.MatrixEndpointManagement(true)
	return model.MatrixEndpoint{Management: &m}
}

func TestExplicitMatrixEntriesComeBeforeTheDefaults(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		cfg.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
			matrixEntry(lanEP(iotID), lanEP(labID), model.MatrixEntryPolicyAllow),
			matrixEntry(lanEP(labID), uplinkEP(), model.MatrixEntryPolicyDeny),
			matrixEntry(mgmtEP(), lanEP(iotID), model.MatrixEntryPolicyAllow),
			matrixEntry(lanEP(iotID), mgmtEP(), model.MatrixEntryPolicyDeny),
		}}
	})
	if tg.HasErrors() || len(tg.Problems) != 0 {
		t.Fatalf("%+v", tg.Problems)
	}
	fw := chainOf(t, tg, "forward")
	var explicit []string
	defaultAt := -1
	for i, r := range fw.Rules {
		s := js(r.Expr)
		if strings.Contains(s, `"br-iot"`) || strings.Contains(s, `"br-lab"`) || strings.Contains(s, `"mgmt0"`) {
			explicit = append(explicit, s)
		}
		if strings.Contains(s, "ifs_test") && strings.Contains(s, `"wan0"`) && strings.Contains(s, "accept") {
			defaultAt = i
		}
	}
	if len(explicit) != 4 {
		t.Fatalf("%d explicit rules:\n%s", len(explicit), strings.Join(explicit, "\n"))
	}
	for i, r := range fw.Rules {
		if i >= defaultAt {
			break
		}
		s := js(r.Expr)
		if strings.Contains(s, `"br-iot"`) || strings.Contains(s, `"br-lab"`) || strings.Contains(s, `"mgmt0"`) {
			continue
		}
	}
	// the management endpoint is told apart by address: it carries the set
	for _, s := range explicit {
		if strings.Contains(s, `"mgmt0"`) && !strings.Contains(s, "mgmt_src") {
			t.Errorf("a management endpoint without its source or destination set: %s", s)
		}
	}
	// the deny entries count in forward_drop
	var drops int
	for _, s := range explicit {
		if strings.Contains(s, `"drop"`) {
			drops++
			if !strings.Contains(s, "forward_drop") {
				t.Errorf("deny without counter: %s", s)
			}
		}
	}
	if drops != 2 {
		t.Errorf("%d deny rules", drops)
	}
	// entries between WireGuard clients or unknown endpoints are not compiled yet and reported
	tg = compileBasic(t, func(cfg *model.Configuration, h *Host) {
		cfg.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{matrixEntry(model.MatrixEndpoint{Client: ptr("00000000-0000-4000-8000-000000000001")}, uplinkEP(), model.MatrixEntryPolicyDeny)}}
	})
	if len(tg.Problems) != 1 || tg.Problems[0].Code != CodeUnsupported {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestTwoPortTopologyDeniesTestToManagementByDefault(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		cfg.Management.Interface = model.InterfaceRef{Name: ptr("wan0")}
	})
	var guard, accept = -1, -1
	for i, r := range chainOf(t, tg, "forward").Rules {
		s := js(r.Expr)
		if strings.Contains(s, "ifs_test") && strings.Contains(s, `"wan0"`) {
			if strings.Contains(s, "mgmt_src") && strings.Contains(s, "drop") {
				guard = i
			} else if strings.Contains(s, "accept") {
				accept = i
			}
		}
	}
	if guard < 0 || accept < 0 || guard > accept {
		t.Fatalf("with the management network behind the uplink interface the default must deny test -> management before it allows test -> uplink (%d, %d)", guard, accept)
	}
	// with a separate management interface no such rule is needed
	tg = compileBasic(t, nil)
	for _, r := range chainOf(t, tg, "forward").Rules {
		if strings.Contains(js(r.Expr), "mgmt_src") {
			t.Errorf("an unneeded management guard: %s", js(r.Expr))
		}
	}
}

func TestRulesCarryAHashOfTheirExpressionAsComment(t *testing.T) {
	tg := compileBasic(t, nil)
	seen := map[string]string{}
	for _, c := range tg.Nft.Chains {
		for _, r := range c.Rules {
			if r.Comment != exprHash(r.Expr) || len(r.Comment) != 12 {
				t.Errorf("comment %q", r.Comment)
			}
			if old, ok := seen[r.Comment]; ok && old != js(r.Expr) {
				t.Errorf("hash collision: %s", r.Comment)
			}
			seen[r.Comment] = js(r.Expr)
		}
	}
	// a rule that changes changes its comment
	if newRule(counter("a")).Comment == newRule(counter("b")).Comment {
		t.Error("equal hashes for different rules")
	}
}

// ---- the transaction -----------------------------------------------------------------------

const currentTable = `{"nftables":[{"metainfo":{"json_schema_version":1}},
 {"table":{"family":"inet","name":"chaosgw","handle":1}},
 {"set":{"family":"inet","table":"chaosgw","name":"ifs_test_old111","type":"ifname","handle":2}},
 {"set":{"family":"inet","table":"chaosgw","name":"dns_hosts_aa11","type":"ipv4_addr","handle":3,"elem":["192.0.2.1"]}},
 {"counter":{"family":"inet","table":"chaosgw","name":"cnt_gone","handle":4,"packets":5,"bytes":300}},
 {"counter":{"family":"inet","table":"chaosgw","name":"input_drop","handle":5,"packets":9,"bytes":900}},
 {"chain":{"family":"inet","table":"chaosgw","name":"old_chain","handle":6}},
 {"chain":{"family":"inet","table":"chaosgw","name":"input","handle":7,"type":"filter","hook":"input","prio":0,"policy":"accept"}}]}`

func commandsOf(t *testing.T, tx []byte) []map[string]map[string]map[string]any {
	t.Helper()
	var doc struct {
		Nftables []map[string]map[string]map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal(tx, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Nftables
}

// position of the first command of that kind and object name, -1 when absent
func find(cmds []map[string]map[string]map[string]any, command, kind, name string) int {
	for i, c := range cmds {
		if obj, ok := c[command][kind]; ok {
			if n, _ := obj["name"].(string); n == name {
				return i
			}
		}
	}
	return -1
}

func TestTransactionKeepsDynamicDataAndDeletesWhatIsGone(t *testing.T) {
	dyn := SetDef{Name: "dns_hosts_aa11", Type: "ipv4_addr", Dynamic: true}
	cfg := loadConfig(t, "gateway.yaml")
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{1, 1}, DynamicSets: []SetDef{dyn}})
	tx, err := tg.Nft.Transaction(parseRuleset(t, currentTable))
	if err != nil {
		t.Fatal(err)
	}
	cmds := commandsOf(t, tx)

	// the dynamic set is declared (a no-op as it exists) but never flushed or refilled
	if find(cmds, "add", "set", "dns_hosts_aa11") < 0 {
		t.Error("the dynamic set is part of the structure")
	}
	if find(cmds, "flush", "set", "dns_hosts_aa11") >= 0 || find(cmds, "add", "element", "dns_hosts_aa11") >= 0 || find(cmds, "delete", "set", "dns_hosts_aa11") >= 0 {
		t.Error("a dynamic set survives every apply with its elements")
	}
	// the counter that stays is added (no-op) but never deleted or reset; the one that is gone is deleted
	if find(cmds, "add", "counter", "input_drop") < 0 || find(cmds, "delete", "counter", "input_drop") >= 0 {
		t.Error("a counter that stays must survive")
	}
	if find(cmds, "delete", "counter", "cnt_gone") < 0 {
		t.Error("the counter of a removed rule is deleted")
	}
	// the old set with another definition goes, the new hashed one exists
	if find(cmds, "delete", "set", "ifs_test_old111") < 0 {
		t.Error("the set with the old name is deleted")
	}
	// a removed chain is emptied and then deleted, after the new rules are in
	fl, del := find(cmds, "flush", "chain", "old_chain"), find(cmds, "delete", "chain", "old_chain")
	lastRule := -1
	for i, c := range cmds {
		if _, ok := c["add"]["rule"]; ok {
			lastRule = i
		}
	}
	if fl < 0 || del < fl || fl < lastRule {
		t.Errorf("old_chain: flush %d, delete %d, last rule %d", fl, del, lastRule)
	}
	// deletes of sets and counters come after the chains were flushed (they may be referenced)
	flushInput := find(cmds, "flush", "chain", "input")
	if flushInput < 0 || find(cmds, "delete", "set", "ifs_test_old111") < flushInput {
		t.Error("a set may only be deleted after the chains that referred to it were flushed")
	}
	// the table itself is never deleted or flushed
	for _, c := range cmds {
		for command, objs := range c {
			if _, ok := objs["table"]; ok && command != "add" {
				t.Errorf("%s table", command)
			}
		}
	}
	// the first command creates the table, the generation rule is the last add
	if _, ok := cmds[0]["add"]["table"]; !ok {
		t.Errorf("first command %v", cmds[0])
	}
}

func TestTransactionFromNothingCreatesEverythingAndDeletesNothing(t *testing.T) {
	tg := compileBasic(t, nil)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commandsOf(t, tx) {
		if _, ok := c["delete"]; ok {
			t.Errorf("a delete without anything to delete: %v", c)
		}
	}
	// the same target against its own result deletes nothing either
	own := `{"nftables":[{"table":{"family":"inet","name":"chaosgw"}}`
	for _, s := range tg.Nft.Sets {
		own += `,{"set":{"family":"inet","table":"chaosgw","name":"` + s.Name + `","type":"` + s.Type + `"}}`
	}
	for _, c := range tg.Nft.Counters {
		own += `,{"counter":{"family":"inet","table":"chaosgw","name":"` + c + `"}}`
	}
	for _, c := range tg.Nft.AllChains() {
		own += `,{"chain":{"family":"inet","table":"chaosgw","name":"` + c.Name + `"}}`
	}
	own += `]}`
	tx, err = tg.Nft.Transaction(parseRuleset(t, own))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commandsOf(t, tx) {
		if _, ok := c["delete"]; ok {
			t.Errorf("a re-apply deletes: %v", c)
		}
	}
}

func TestTransactionStaysInsideTheExecutorsScope(t *testing.T) {
	tg := compileBasic(t, nil)
	tx, _ := tg.Nft.Transaction(parseRuleset(t, currentTable))
	if err := executorCheck(tx); err != nil {
		t.Fatal(err)
	}
}
