package apply_test

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// P3-M14-03: the technical view of one object is the kernel state cut down to the names the selection
// gives: a counter with the rules that count into it (in their chains), whole chains and sets, the map
// elements the selection takes, and the tc classes with their netem leaves and the filters leading to
// them on every interface. Nothing else of the table or the trees is shown.
func TestRenderObjectShowsOnlyWhatTheSelectionNames(t *testing.T) {
	rs, err := linux.ParseNft([]byte(`{"nftables":[
	  {"table":{"family":"inet","name":"chaosgw","handle":1}},
	  {"counter":{"family":"inet","table":"chaosgw","name":"rule_a","handle":2,"packets":3,"bytes":180}},
	  {"counter":{"family":"inet","table":"chaosgw","name":"rule_b","handle":3,"packets":9,"bytes":540}},
	  {"counter":{"family":"inet","table":"chaosgw","name":"fault_7_up","handle":4,"packets":1,"bytes":60}},
	  {"set":{"family":"inet","table":"chaosgw","name":"asrc_a","type":"ipv4_addr","handle":5,"elem":["10.10.0.31"]}},
	  {"set":{"family":"inet","table":"chaosgw","name":"asrc_b","type":"ipv4_addr","handle":6,"elem":["10.10.0.32"]}},
	  {"map":{"family":"inet","table":"chaosgw","name":"cls_dev_x","type":"ipv4_addr","map":"verdict","handle":7,"flags":["interval"],
	    "elem":[["10.10.0.31",{"goto":{"target":"mark_7"}}],["10.10.0.32",{"goto":{"target":"mark_8"}}]]}},
	  {"chain":{"family":"inet","table":"chaosgw","name":"access_forward","handle":8}},
	  {"rule":{"family":"inet","table":"chaosgw","chain":"access_forward","handle":9,"comment":"ca","expr":[{"counter":"rule_a"},{"last":{"used":2000}},{"drop":null}]}},
	  {"rule":{"family":"inet","table":"chaosgw","chain":"access_forward","handle":10,"comment":"cb","expr":[{"counter":"rule_b"},{"last":null},{"accept":null}]}},
	  {"chain":{"family":"inet","table":"chaosgw","name":"mark_7","handle":11}},
	  {"rule":{"family":"inet","table":"chaosgw","chain":"mark_7","handle":12,"comment":"m7","expr":[{"counter":"fault_7_up"},{"return":null}]}},
	  {"chain":{"family":"inet","table":"chaosgw","name":"mark_8","handle":13}},
	  {"rule":{"family":"inet","table":"chaosgw","chain":"mark_8","handle":14,"comment":"m8","expr":[{"return":null}]}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	stats := &linux.NormStats{Packets: 5, Bytes: 300, Drops: 1}
	tree := &linux.NormTree{Dev: "br-iot",
		Qdiscs: []linux.NormQdisc{
			{Handle: "1:", Parent: "root", Kind: "htb"},
			{Handle: "1e:", Parent: "1:1e", Kind: "netem", Netem: &linux.NetemSpec{Limit: 1000, Delay: 0.05}, Stats: stats},
			{Handle: "20:", Parent: "1:20", Kind: "netem", Netem: &linux.NetemSpec{Limit: 1000, Loss: 0.01}},
		},
		Classes: []linux.NormClass{{ID: "1:1e", Parent: "1:", Kind: "htb", Leaf: "1e:"}, {ID: "1:20", Parent: "1:", Kind: "htb", Leaf: "20:"}},
		Filters: []linux.NormFilter{{Parent: "1:", Kind: "fw", Pref: 1, Mark: 0x70, Mask: 0x1fff0, Flowid: "1:1e"}, {Parent: "1:", Kind: "fw", Pref: 1, Mark: 0x80, Mask: 0x1fff0, Flowid: "1:20"}},
	}
	st := &apply.State{Nft: rs, TC: map[string]*linux.NormTree{"br-iot": tree}}

	rule := apply.RenderObject(st, apply.ObjectSel{Counters: map[string]bool{"rule_a": true}, Sets: map[string]bool{"asrc_a": true}})
	for _, want := range []string{"counter rule_a { packets 3 bytes 180 }", "set asrc_a {", "chain access_forward {", `counter name "rule_a" last used 2s drop`} {
		if !strings.Contains(rule.Nftables, want) {
			t.Errorf("rule: no %q in\n%s", want, rule.Nftables)
		}
	}
	for _, not := range []string{"rule_b", "asrc_b", "cls_dev_x", "mark_7"} {
		if strings.Contains(rule.Nftables, not) {
			t.Errorf("rule: %q is shown:\n%s", not, rule.Nftables)
		}
	}
	if rule.TC != "" || strings.Join(rule.Keys, ",") != "counter rule_a,set asrc_a,rule access_forward ca" {
		t.Errorf("rule: keys %v, tc %q", rule.Keys, rule.TC)
	}

	fault := apply.RenderObject(st, apply.ObjectSel{Counters: map[string]bool{"fault_7_up": true}, Chains: map[string]bool{"mark_7": true},
		Element: func(_, _, v string) bool { return v == "mark_7" }, Classes: map[string]bool{"1:1e": true}})
	for _, want := range []string{"chain mark_7 {", "10.10.0.31 : goto mark_7", "counter fault_7_up {"} {
		if !strings.Contains(fault.Nftables, want) {
			t.Errorf("fault: no %q in\n%s", want, fault.Nftables)
		}
	}
	if strings.Contains(fault.Nftables, "mark_8") || strings.Contains(fault.Nftables, "10.10.0.32") {
		t.Errorf("fault: the other fault is shown:\n%s", fault.Nftables)
	}
	for _, want := range []string{"br-iot: class 1:1e", "br-iot: qdisc 1e: parent 1:1e netem", "sent 300 bytes 5 pkt (dropped 1", "flowid=1:1e"} {
		if !strings.Contains(fault.TC, want) {
			t.Errorf("fault: no %q in\n%s", want, fault.TC)
		}
	}
	if strings.Contains(fault.TC, "1:20") || strings.Contains(fault.TC, "qdisc 1: ") {
		t.Errorf("fault: another class or the root is shown:\n%s", fault.TC)
	}
	if strings.Join(fault.Keys, ",") != "counter fault_7_up,map cls_dev_x 10.10.0.31,chain mark_7,class br-iot 1:1e,qdisc br-iot 1e:,filter br-iot 1: pref 1" {
		t.Errorf("fault keys: %v", fault.Keys)
	}

	// nothing selected, nothing shown
	if none := apply.RenderObject(st, apply.ObjectSel{}); none.Nftables != "" || none.TC != "" || len(none.Keys) != 0 {
		t.Errorf("an empty selection: %+v", none)
	}
}
