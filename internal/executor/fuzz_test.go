package executor

import (
	"encoding/json"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

var fuzzSeeds = []string{
	`{"type":"nft_apply","ruleset":` + goodNft + `}`,
	`{"type":"nft_add_elements","namespace":"ns1","set":"dns_a","elements":["192.0.2.1","10.0.0.0/8","aa:bb:cc:dd:ee:ff"],"timeout_seconds":60}`,
	`{"type":"nft_dns_elements","namespace":"ns1","updates":[{"set":"dns_0123456789_abcdef","elements":[{"client":"10.10.0.5","addr":"203.0.113.7","timeout_seconds":600},{"client":"10.10.0.6","addr":"203.0.113.8","timeout_seconds":60,"replace":true}]}]}`,
	`{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"default","via":"10.0.0.1","dev":"wan0","metric":10},{"action":"replace","family":6,"table":102,"dst":"::/0","type":"prohibit"}],"rules":[{"action":"add","family":4,"priority":1000,"from":"10.10.0.0/24","fwmark":"0x10/0xff","iif":"lan0","table":100}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","10"]},{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["protocol","ip","prio","1","u32","match","ip","src","10.0.0.0/24","flowid","1:10"]},{"object":"qdisc","action":"add","dev":"lan0","parent":"ingress"}]}`,
	`{"type":"offloads","devs":["wan0"]}`,
	`{"type":"docker_user","action":"ensure","devs":["br-lan0"]}`,
	`{"type":"assign_interfaces","devs":["wan0","lan0"]}`,
	`{"type":"assign_interfaces","devs":["wan0","lan0"],"os_owned":["wan0"]}`,
	`{"type":"read","what":"routes","table":"100"}`,
	`{"type":"read","what":"route_get","dst":"203.0.113.9","src":"10.10.0.31","dev":"br-lan0"}`,
	`{"type":"read","what":"route_get","dst":"203.0.113.9 oif lo","src":"10.10.0.31"}`,
	`{"type":"nft_apply","ruleset":{"nftables":[{"flush":{"ruleset":null}}]}}`,
	`{"type":"routing","rules":[{"action":"delete","family":4,"priority":32766,"table":254}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"add","dev":"wan0\n","parent":"root","handle":"1:","args":["netem;id"]}]}`,
	`{"type":"offloads","devs":["a"],"devs":["b"]}`,
	`{"type":"links","entries":[{"action":"add_bridge","name":"br-lan0"},{"action":"enslave","name":"lan0","master":"br-lan0"},{"action":"addr_replace","name":"br-lan0","cidr":"10.10.0.1/24"}]}`,
	`{"type":"sysctl","entries":[{"name":"accept_ra","dev":"br-lan0","value":0}]}`,
	`{"type":"wireguard","action":"ensure","name":"wg-hub","listen_port":51820,"mtu":1420,"key_ref":"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21","peers":[{"public_key":"FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=","allowed_ips":["10.99.0.2/32"],"keepalive":25,"endpoint":"203.0.113.40:51821"}]}`,
	`{"type":"wireguard","action":"ensure","name":"wg-site-b","listen_port":51821,"key_ref":"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21","peers":[{"public_key":"FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=","allowed_ips":["0.0.0.0/0","::/0"]}]}`,
	`{"type":"wireguard","action":"delete","name":"wg-hub"}`,
	`{"type":"nft_del_elements","set":"dev_a","elements":["10.10.0.5","192.0.2.0/24"]}`,
	`{"type":"bird","action":"apply","instance":"chaosgw","config":"router id 10.10.0.1;\nprotocol device { }\n","import_tables":[10]}`,
	`{"type":"bird","action":"check","instance":"chaosgw","config":"include \"/etc/shadow\";"}`,
	`{"type":"read","what":"bird","instance":"chaosgw"}`,
	`{"type":"bird","action":"apply","instance":"chaosgw","config":"router id 10.10.0.1;\nprotocol kernel k { kernel table 100; learn off; ipv4 { export all; }; }\n"}`,
	`{"type":"bird","action":"apply","instance":"chaosgw","config":"protocol kernel k { ipv4 { export all; }; }\nprotocol static s { ipv4; route 0.0.0.0/0 via 10.0.0.1; }\n"}`,
	`{"type":"bird","action":"check","instance":"chaosgw","config":"template kernel t { ipv4; }\nprotocol kernel k from t { kernel table 254; }\n"}`,
	// the string fields no seed above has (TestEveryStringFieldOfEveryOperationIsInASeed): the namespace of every operation that takes
	// one, and the fields of the diagnostics, the rules and the reads that the seeds left out. metacharSeeds makes the variants of each
	// field with ; $ ` + "`" + ` a space, a line break and a leading '-' (TestNoStringFieldOfAnyOperationTakesAMetacharacter)
	`{"type":"routing","namespace":"gw","routes":[{"action":"replace","family":4,"table":100,"dst":"default","via":"10.0.0.1","dev":"wan0"}],"rules":[{"action":"add","family":4,"priority":1000,"from":"10.10.0.0/24","to":"203.0.113.0/24","fwmark":"0x10/0xff","iif":"lan0","oif":"wan0","table":100}]}`,
	`{"type":"docker_user","namespace":"gw","action":"ensure","devs":["br-lan0"]}`,
	`{"type":"sysctl","namespace":"gw","entries":[{"name":"accept_ra","dev":"br-lan0","value":0}]}`,
	`{"type":"wireguard","namespace":"gw","action":"ensure","name":"wg-hub","listen_port":51820,"key_ref":"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21","peers":[{"public_key":"FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=","preshared_key_ref":"1c8d7b4f-2a3e-4d6c-8b9f-0e1d2c3b4a59","allowed_ips":["10.99.0.2/32"],"endpoint":"203.0.113.40:51821"}]}`,
	`{"type":"nft_del_elements","namespace":"gw","set":"dev_a","elements":["10.10.0.5"]}`,
	`{"type":"nft_del_map_elements","namespace":"gw","map":"ident4","keys":["10.10.0.31"]}`,
	`{"type":"offloads","namespace":"gw","devs":["wan0"]}`,
	`{"type":"assign_interfaces","namespace":"gw","devs":["wan0","lan0"]}`,
	`{"type":"nft_apply","namespace":"gw","ruleset":` + goodNft + `}`,
	`{"type":"probe_ns","namespace":"gw","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"02:28:b4:ce:f6:08","hostname":"probe-a"}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"http","host":"203.0.113.10","port":80,"url":"http://203.0.113.10/status","timeout":"5s","from":{"probe":"probe-a"}},"user_agent":"chaosgw-diagnostic","proxy":"10.10.0.1:53"}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tls","host":"203.0.113.10","port":443,"sni":"broker.example.com"}}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"udp","host":"203.0.113.10","port":7,"payload":"hello"}}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"dns","host":"10.10.0.1","port":53,"dns_name":"broker.example.com"}}`,
	`{"type":"read","what":"sysctl","name":"accept_ra","dev":"wan0"}`,
	`{"type":"read","what":"service_ns","service":"cgsvc","pid":4242}`,
	// the operations the seeds above did not start from (M29, TestEveryOperationTypeHasAFuzzSeedThatTheDecoderAccepts): the elements of
	// the maps, the deletion of connections, the service namespace and its pair
	`{"type":"nft_add_map_elements","namespace":"ns1","map":"ident4_ab12cd","elements":[{"key":"10.10.0.5","value":"3"},{"key":"10.10.0.31 . 203.0.113.10 . 6 . 443","value":"mark_7"},{"key":"10.0.0.5-10.0.0.9 . 198.51.100.0-198.51.100.77 . tcp . 80-443","value":"mark_0"}]}`,
	`{"type":"nft_del_map_elements","map":"ident4","keys":["10.10.0.31","10.10.0.31 . 203.0.113.10 . 6 . 443"]}`,
	`{"type":"nft_add_map_elements","map":"ident4; flush ruleset","elements":[{"key":"10.10.0.5","value":"3"}]}`,
	`{"type":"conntrack_delete","namespace":"ns1","flows":[{"proto":"tcp","src":"10.10.0.31","dst":"203.0.113.10","sport":45566,"dport":8883},{"proto":"icmp","src":"10.10.0.31","dst":"203.0.113.10","icmp_type":8,"icmp_code":0,"icmp_id":4711}]}`,
	`{"type":"conntrack_delete","flows":[{"proto":"udp","src":"0.0.0.0","dst":"203.0.113.10 -d 1.1.1.1","sport":1,"dport":2}]}`,
	`{"type":"service_ns","action":"ensure","namespace":"gw","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30","holder_pid":4242}`,
	`{"type":"service_ns","action":"delete","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30"}`,
	`{"type":"service_ns","action":"ensure","name":"cgsvc","host_if":"eth0","peer_if":"svc1","host_cidr":"10.0.0.1/30","peer_cidr":"10.0.0.2/30"}`,
	// M25: the probes and the measurements from them
	`{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"02:28:b4:ce:f6:08","hostname":"probe-a"}`,
	`{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52"}`,
	`{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot; id","mac":"02:28:b4:ce:f6:08"}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"203.0.113.10","port":80},"servers":["10.10.0.1:53"],"roots_pem":"x"}`,
	`{"type":"probe_run","namespace":"gw","kind":"echo","host":"203.0.113.10","port":7,"count":100,"interval_us":10000}`,
	`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"rate","host":"203.0.113.10","port":7,"duration_ms":10000}`,
	`{"type":"read","what":"probes"}`,
	`{"type":"nft_apply","ruleset":{"nftables":[{"flush":{"table":{"family":"ip","name":"nat","Family":"inet","Name":"chaosgw"}}}]}}`,
	`{"type":"tc","entries":[{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:10","args":["htb","rate","1mbit"]}]}`,
	// M8b: the operations of the fault engine, complete netem sets, in-place changes, deletions
	`{"type":"tc","namespace":"gw","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","1"]},{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","10gbit","quantum","60000"]},{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":["netem","limit","5000","delay","50ms","10ms","0%","distribution","normal","loss","random","1%","25%","reorder","0%","0%","duplicate","0%","0%","corrupt","0%","0%","rate","0bit"]},{"object":"filter","action":"replace","dev":"wan0","parent":"1:","handle":"0x000a0/0x1fff0","args":["protocol","ip","prio","1","fw","flowid","1:24"]}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"change","dev":"wan0","parent":"1:24","handle":"24:","args":["netem","limit","1000","delay","0ms","0ms","0%","loss","gemodel","1%","10%","70%","0.1%","reorder","25%","0%","duplicate","0%","0%","corrupt","0.1%","0%","rate","2Mbit"]}]}`,
	`{"type":"tc","entries":[{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0x100a0/0x1fff0","args":["protocol","ip","prio","1","fw"]},{"object":"class","action":"delete","dev":"wan0","classid":"1:25"},{"object":"qdisc","action":"delete","dev":"wan0","parent":"root","handle":"1:"}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":["netem","limit","1000","delay","1ms","distribution","../../etc/passwd"]}]}`,
	`{"type":"tc","entries":[{"object":"qdisc","action":"delete","dev":"wan0","parent":"root","handle":"8001:"}]}`,
	`{"type":"read","what":"tc","dev":"wan0"}`,
	// the qdisc listing of all interfaces, one tool run (apply's readTC, P2-M8b-04)
	`{"type":"read","namespace":"gw","what":"qdiscs"}`,
	// M10: the duplication hook and its read
	`{"type":"nft_dup","namespace":"gw","devs":["br-iot","br-lab","wan0"]}`,
	`{"type":"nft_dup","devs":[]}`,
	`{"type":"nft_dup","devs":["wan0","wan0"]}`,
	`{"type":"nft_dup","devs":["wan0; drop"],"ruleset":{"nftables":[]}}`,
	`{"type":"read","what":"nft_dup"}`,
	// M10: the IFB of the tunnel faults and the flower filters that feed and select it
	`{"type":"links","namespace":"gw","entries":[{"action":"add_ifb","name":"ifb-cgw"},{"action":"up","name":"ifb-cgw"},{"action":"delete_ifb","name":"ifb-cgw"}]}`,
	`{"type":"links","entries":[{"action":"add_ifb","name":"ifb0"},{"action":"delete_ifb","name":"wan0"}]}`,
	`{"type":"tc","namespace":"gw","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"ingress"},{"object":"filter","action":"replace","dev":"wan0","parent":"ffff:","handle":"7","args":["protocol","ip","prio","10","flower","ip_proto","udp","src_ip","198.51.100.2","src_port","51820","action","mirred","egress","redirect","dev","ifb-cgw"]},{"object":"filter","action":"replace","dev":"ifb-cgw","parent":"1:","handle":"7","args":["protocol","ip","prio","1","flower","ip_proto","udp","src_ip","198.51.100.2","src_port","51820","flowid","1:1e"]},{"object":"filter","action":"delete","dev":"wan0","parent":"ffff:","handle":"7","args":["protocol","ip","prio","10","flower"]},{"object":"qdisc","action":"delete","dev":"wan0","parent":"ingress"}]}`,
	`{"type":"tc","entries":[{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","handle":"7","args":["protocol","ip","prio","10","flower","ip_proto","udp","src_ip","198.51.100.2","src_port","51820","action","mirred","egress","redirect","dev","lan0"]}]}`,
	`{"type":"tc","entries":[{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","handle":"7","args":["protocol","ip","prio","10","flower","src_ip","1.2.3.4/8","action","drop"]}]}`,
	``, `{}`, `[]`, `null`, `{"type":null}`, `{"type":"read"`, "\x00",
}

// FuzzDecode feeds arbitrary bytes to the operation decoder. Whatever it accepts must be inert:
// the commands built from it carry no shell syntax, stay inside the executor's scope and the wire
// form is stable.
func FuzzDecode(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	// every string field with a metacharacter in it: a validator that lets one through fails the seed run
	for _, s := range metacharSeeds(f) {
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
		if _, ok := op.(*Bird); ok {
			checkStdin(t, op, Command{}) // the bird operation has no step: its input is the configuration
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
	if b, ok := op.(*Bird); ok {
		checkBirdConfig(t, b)
		return
	}
	switch c.Tool {
	case ToolNft:
		if _, ok := op.(*NftDup); ok {
			checkDupTransaction(t, c.Stdin)
			return
		}
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
			if c.Tool == ToolTC {
				checkTCLineInScope(t, l)
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

// birdUncommented removes what BIRD does not read; birdKernelHead is the head of a kernel protocol or
// template. The oracle reads the text on its own, not through the checker it tests.
var (
	birdUncommented = regexp.MustCompile(`(?s)/\*.*?\*/|#[^\n]*|"[^"\n]*"`)
	birdKernelHead  = regexp.MustCompile(`\b(?:protocol|template)\s+kernel\b`)
	birdKernelTable = regexp.MustCompile(`\bkernel\s+table\s+(\d+)\b`)
)

// checkBirdConfig: whatever the bird operation is given, every kernel protocol and kernel template of
// the configuration it writes names a table of Chaos Gateway or an import table itself; BIRD 2 syncs the
// host's main table for one that does not.
func checkBirdConfig(t *testing.T, b *Bird) {
	t.Helper()
	code := birdUncommented.ReplaceAllString(b.Config, " ")
	for _, h := range birdKernelHead.FindAllStringIndex(code, -1) {
		open := strings.IndexByte(code[h[1]:], '{')
		if open < 0 {
			t.Fatalf("a kernel protocol without a block was accepted:\n%s", b.Config)
		}
		open += h[1]
		depth, end := 0, len(code)
		for i := open; i < len(code); i++ {
			if code[i] == '{' {
				depth++
			} else if code[i] == '}' {
				if depth--; depth == 0 {
					end = i
					break
				}
			}
		}
		m := birdKernelTable.FindStringSubmatch(code[open:end])
		if m == nil {
			t.Fatalf("a kernel protocol without a table was accepted:\n%s", b.Config)
		}
		n, _ := strconv.Atoi(m[1])
		if !slices.Contains(b.allowedTables(), n) {
			t.Fatalf("a kernel protocol on table %d was accepted:\n%s", n, b.Config)
		}
	}
}

// checkDupTransaction: whatever nft_dup is given, the transaction it makes touches the table of the hook and
// nothing else: every object is in the family netdev and the table chaosgw_dup, a chain is an egress filter
// chain on an interface, a rule is the one rule of the hook.
func checkDupTransaction(t *testing.T, stdin string) {
	t.Helper()
	var doc struct {
		Nftables []map[string]map[string]map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(stdin), &doc); err != nil {
		t.Fatalf("the hook's transaction is no JSON: %v", err)
	}
	str := func(f map[string]json.RawMessage, k string) string {
		var v string
		_ = json.Unmarshal(f[k], &v)
		return v
	}
	for _, item := range doc.Nftables {
		if len(item) != 1 {
			t.Fatalf("an item with %d commands: %s", len(item), stdin)
		}
		for cmd, objs := range item {
			if cmd != "add" && cmd != "delete" {
				t.Fatalf("the hook's transaction has the command %q", cmd)
			}
			for kind, f := range objs {
				if str(f, "family") != NftDupFamily {
					t.Fatalf("%s %s in the family %q", cmd, kind, str(f, "family"))
				}
				switch kind {
				case "table":
					if str(f, "name") != NftDupTable {
						t.Fatalf("table %q", str(f, "name"))
					}
				case "chain":
					dev := str(f, "dev")
					if str(f, "table") != NftDupTable || str(f, "hook") != "egress" || str(f, "type") != "filter" || str(f, "name") != DupChainName(dev) || checkDev(dev) != nil {
						t.Fatalf("chain %s", stdin)
					}
				case "rule":
					if str(f, "table") != NftDupTable || !strings.HasPrefix(str(f, "chain"), "egress_") {
						t.Fatalf("rule %s", stdin)
					}
				default:
					t.Fatalf("the hook's transaction has the object %q", kind)
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

// socketpair returns a connected pair of *net.UnixConn, so PeerCred (which needs a real Unix
// socket, unlike net.Pipe) works on them.
func socketpair(t *testing.T) (a, b *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	toConn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		defer func() { _ = f.Close() }()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return c.(*net.UnixConn)
	}
	return toConn(fds[0]), toConn(fds[1])
}

// FuzzConn drives the connection handler itself over a real Unix socketpair: the hello handshake,
// the maxLine-bounded scanner and the request loop, none of which FuzzFrame reaches (it calls
// Server.serve directly, skipping the connection entirely). It must never panic, the connection
// must always end, and whatever the fake runner saw must still be inert.
func FuzzConn(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(`{"request":{"id":1,"ops":[` + s + `]}}`))
	}
	f.Add([]byte(`{"hello":{"protocol":1,"version":"x","role":"client"}}`))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		fake := &fakeRunner{}
		e, err := New(fake)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		s := &Server{Exec: e, Auth: func(Cred) error { return nil }, HelloTimeout: time.Second}

		client, server := socketpair(t)
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.handle(t.Context(), server)
		}()

		cc := newCodec(client)
		if _, err := cc.read(); err != nil { // the server's own hello
			_ = client.Close()
			<-done
			return
		}
		if err := cc.write(&Frame{Hello: &Hello{Protocol: s.proto(), Version: "fuzz", Role: "client"}}); err != nil {
			_ = client.Close()
			<-done
			return
		}
		_, _ = client.Write(data)
		_, _ = client.Write([]byte("\n"))
		_ = client.Close()
		<-done
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
