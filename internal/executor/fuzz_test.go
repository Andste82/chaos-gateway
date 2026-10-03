package executor

import (
	"encoding/json"
	"strings"
	"testing"
)

var fuzzSeeds = []string{
	`{"type":"nft_apply","ruleset":` + goodNft + `}`,
	`{"type":"nft_add_elements","namespace":"ns1","set":"dns_a","elements":["192.0.2.1","10.0.0.0/8","aa:bb:cc:dd:ee:ff"],"timeout_seconds":60}`,
	`{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"default","via":"10.0.0.1","dev":"wan0","metric":10},{"action":"replace","family":6,"table":102,"dst":"::/0","type":"prohibit"}],"rules":[{"action":"add","family":4,"priority":1000,"from":"10.10.0.0/24","fwmark":"0x10/0xff","iif":"lan0","table":100}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","10"]},{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["protocol","ip","prio","1","u32","match","ip","src","10.0.0.0/24","flowid","1:10"]},{"object":"qdisc","action":"add","dev":"lan0","parent":"ingress"}]}`,
	`{"type":"offloads","devs":["wan0"]}`,
	`{"type":"docker_user","action":"ensure","devs":["br-lan0"]}`,
	`{"type":"assign_interfaces","devs":["wan0","lan0"]}`,
	`{"type":"read","what":"routes","table":"100"}`,
	`{"type":"nft_apply","ruleset":{"nftables":[{"flush":{"ruleset":null}}]}}`,
	`{"type":"routing","rules":[{"action":"delete","family":4,"priority":32766,"table":254}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"add","dev":"wan0\n","parent":"root","args":["netem;id"]}]}`,
	`{"type":"offloads","devs":["a"],"devs":["b"]}`,
	`{"type":"links","entries":[{"action":"add_bridge","name":"br-lan0"},{"action":"enslave","name":"lan0","master":"br-lan0"},{"action":"addr_replace","name":"br-lan0","cidr":"10.10.0.1/24"}]}`,
	`{"type":"sysctl","entries":[{"name":"accept_ra","dev":"br-lan0","value":0}]}`,
	`{"type":"wireguard","action":"ensure","name":"wg-hub","listen_port":51820,"mtu":1420,"key_ref":"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21","peers":[{"public_key":"FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=","allowed_ips":["10.99.0.2/32"],"keepalive":25,"endpoint":"203.0.113.40:51821"}]}`,
	`{"type":"wireguard","action":"delete","name":"wg-hub"}`,
	`{"type":"nft_del_elements","set":"dev_a","elements":["10.10.0.5","192.0.2.0/24"]}`,
	`{"type":"bird","action":"apply","instance":"chaosgw","config":"router id 10.10.0.1;\nprotocol device { }\n","import_tables":[10]}`,
	`{"type":"bird","action":"check","instance":"chaosgw","config":"include \"/etc/shadow\";"}`,
	`{"type":"read","what":"bird","instance":"chaosgw"}`,
	`{"type":"nft_apply","ruleset":{"nftables":[{"flush":{"table":{"family":"ip","name":"nat","Family":"inet","Name":"chaosgw"}}}]}}`,
	`{"type":"tc","entries":[{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:10","args":["htb","rate","1mbit"]}]}`,
	``, `{}`, `[]`, `null`, `{"type":null}`, `{"type":"read"`, "\x00",
}

// FuzzDecode feeds arbitrary bytes to the operation decoder. Whatever it accepts must be inert:
// the commands built from it carry no shell syntax, stay inside the executor's scope and the wire
// form is stable.
func FuzzDecode(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		op, err := Decode(data)
		if err != nil {
			return
		}
		enc, err := Encode(op)
		if err != nil {
			t.Fatalf("encode of an accepted operation: %v", err)
		}
		again, err := Decode(enc)
		if err != nil {
			t.Fatalf("the encoding of an accepted operation is rejected: %v\n%s", err, enc)
		}
		if enc2, _ := Encode(again); string(enc2) != string(enc) {
			t.Fatalf("unstable encoding:\n%s\n%s", enc, enc2)
		}
		if r, ok := op.(*Read); ok {
			checkInert(t, ReadCommand(r))
			return
		}
		steps, err := Plan(op)
		if err != nil {
			t.Fatalf("an accepted operation has no plan: %v\n%s", err, data)
		}
		for _, s := range steps {
			checkInert(t, s.Cmd)
			if s.Probe != nil {
				checkInert(t, *s.Probe)
			}
			checkStdin(t, op, s.Cmd)
		}
	})
}

func checkInert(t *testing.T, c Command) {
	t.Helper()
	for _, a := range c.Args {
		if a == "" || strings.ContainsAny(a, " \t\r\n\x00;|&$`<>\"'\\") {
			t.Fatalf("argument %q of %s is not inert", a, c)
		}
	}
	if c.NS != "" && !nsName.MatchString(c.NS) {
		t.Fatalf("namespace %q", c.NS)
	}
}

func checkStdin(t *testing.T, op Operation, c Command) {
	t.Helper()
	switch c.Tool {
	case ToolNft:
		if err := CheckNftRuleset(json.RawMessage(c.Stdin)); err != nil {
			t.Fatalf("the nft input of an accepted operation leaves the scope: %v\n%s", err, c.Stdin)
		}
		nftOracle(t, c.Stdin)
	case ToolIP, ToolTC:
		if c.Stdin == "" {
			return // single-command steps (links) carry no batch
		}
		verbs := map[string]bool{"route": true, "rule": true, "qdisc": true, "class": true, "filter": true}
		lines := strings.Split(strings.TrimSuffix(c.Stdin, "\n"), "\n")
		for _, l := range lines {
			f := strings.Fields(l)
			if len(f) < 2 || !verbs[f[0]] || strings.ContainsAny(l, "\r\x00;|&$`<>\"'\\") {
				t.Fatalf("batch line %q of %T is not a plain command", l, op)
			}
			if c.Tool == ToolIP && !strings.Contains(l, "proto 201") && !strings.Contains(l, "protocol 201") {
				t.Fatalf("routing line without the protocol tag: %q", l)
			}
			if c.Tool == ToolIP {
				if i := indexOf(f, "table"); i < 0 || len(f) <= i+1 || !ownTable(f[i+1]) {
					t.Fatalf("routing line outside Chaos Gateway's tables: %q", l)
				}
			}
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func ownTable(s string) bool {
	for t := OwnTableFirst; t <= OwnTableLast; t++ {
		if s == itoa(t) {
			return true
		}
	}
	return false
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// FuzzFrame feeds arbitrary lines to the server's request handling: it must neither panic nor run
// anything for a request the decoder rejects.
func FuzzFrame(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(`{"request":{"id":1,"ops":[` + s + `]}}`))
	}
	f.Add([]byte(`{"hello":{"protocol":1}}`))
	f.Add([]byte(`{"request":{"id":1,"ops":null}}`))
	f.Fuzz(func(t *testing.T, line []byte) {
		var fr Frame
		if json.Unmarshal(line, &fr) != nil || fr.Request == nil {
			return
		}
		fake := &fakeRunner{}
		e, err := New(fake)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		s := &Server{Exec: e}
		resp := s.serve(t.Context(), fr.Request)
		if resp.ID != fr.Request.ID {
			t.Fatal("response id differs")
		}
		if !resp.OK && resp.Error == nil {
			t.Fatal("a failed response without an error")
		}
		for _, c := range fake.commands() {
			checkInert(t, c)
		}
	})
}

// nftOracle is an independent check of what nft will see: it reads the document with exact,
// case-sensitive keys into generic values (as jansson does), not with the code under test.
func nftOracle(t *testing.T, stdin string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdin), &doc); err != nil {
		t.Fatalf("nft input is not JSON: %v", err)
	}
	items, _ := doc["nftables"].([]any)
	if len(doc) != 1 || len(items) == 0 {
		t.Fatalf("nft input shape: %s", stdin)
	}
	for _, it := range items {
		obj, _ := it.(map[string]any)
		for k, v := range obj {
			inner, _ := v.(map[string]any)
			kind := k
			if nftCommands[k] {
				for k2, v2 := range inner {
					kind = k2
					inner, _ = v2.(map[string]any)
				}
			}
			table := inner["table"]
			if kind == "table" {
				table = inner["name"]
			}
			if inner["family"] != "inet" || table != "chaosgw" {
				t.Fatalf("nft object %q leaves inet chaosgw: %s", kind, stdin)
			}
		}
	}
}
