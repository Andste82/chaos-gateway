package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

const (
	netID   = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	peerID  = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	pubA    = "FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo="
	pubB    = "cFTvlxJ49hG8zsQ91QN8ATdE5+4QELIulw1yJEvpdGY="
	secPriv = "SECRETPRIVATEKEYSECRETPRIVATEKEYSECRETPRIVA="
	secPSK  = "SECRETPSHAREDKEYSECRETPSHAREDKEYSECRETPSHAR="
)

const wgOp = `{"type":"wireguard","action":"ensure","name":"wg-hub","listen_port":51820,"mtu":1420,"key_ref":"` + netID + `","peers":[` +
	`{"public_key":"` + pubA + `","preshared_key_ref":"` + peerID + `","allowed_ips":["10.99.0.2/32","10.50.0.0/24"],"keepalive":25},` +
	`{"public_key":"` + pubB + `","allowed_ips":["0.0.0.0/0"],"endpoint":"203.0.113.40:51821"}]}`

func TestDecodeWireGuard(t *testing.T) {
	op, err := Decode([]byte(wgOp))
	if err != nil {
		t.Fatal(err)
	}
	w := op.(*WireGuard)
	if w.Name != "wg-hub" || len(w.Peers) != 2 || w.Peers[0].Keepalive != 25 || w.Peers[1].Endpoint != "203.0.113.40:51821" {
		t.Fatalf("%+v", w)
	}
	enc, _ := Encode(op)
	if strings.Contains(string(enc), "private") {
		t.Errorf("the encoding names a private key: %s", enc)
	}
	if _, err := Decode([]byte(`{"type":"wireguard","action":"delete","name":"wg-hub"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeWireGuardRejects(t *testing.T) {
	peer := func(fields string) string {
		return `{"type":"wireguard","action":"ensure","name":"wg0","listen_port":51820,"key_ref":"` + netID + `","peers":[{"public_key":"` + pubA + `","allowed_ips":["10.0.0.2/32"]` + fields + `}]}`
	}
	head := func(fields string) string {
		return `{"type":"wireguard","action":"ensure","name":"wg0","key_ref":"` + netID + `"` + fields + `}`
	}
	for name, in := range map[string]string{
		"private key in the operation": head(`,"listen_port":1,"private_key":"` + secPriv + `"`),
		"unknown action":               `{"type":"wireguard","action":"flush","name":"wg0"}`,
		"bad name":                     `{"type":"wireguard","action":"delete","name":"wg 0"}`,
		"delete with peers":            `{"type":"wireguard","action":"delete","name":"wg0","listen_port":1}`,
		"no port":                      head(``),
		"port 0":                       head(`,"listen_port":0`),
		"port 70000":                   head(`,"listen_port":70000`),
		"mtu 100":                      head(`,"listen_port":1,"mtu":100`),
		"mtu 70000":                    head(`,"listen_port":1,"mtu":70000`),
		"key ref not a uuid":           `{"type":"wireguard","action":"ensure","name":"wg0","listen_port":1,"key_ref":"../etc/passwd"}`,
		"peer key short":               strings.Replace(peer(``), pubA, "abc=", 1),
		"peer key with newline":        strings.Replace(peer(``), pubA, pubA[:43]+"\\n=", 1),
		"psk ref not a uuid":           peer(`,"preshared_key_ref":"x"`),
		"allowed ip not a prefix":      strings.Replace(peer(``), "10.0.0.2/32", "10.0.0.2", 1),
		"allowed ip with host bits":    strings.Replace(peer(``), "10.0.0.2/32", "10.0.0.2/24", 1),
		"allowed ip injection":         strings.Replace(peer(``), "10.0.0.2/32", "10.0.0.2/32\\n[Peer]", 1),
		"allowed ip v6":                strings.Replace(peer(``), "10.0.0.2/32", "fd00::/64", 1),
		"negative keepalive":           peer(`,"keepalive":-1`),
		"endpoint without port":        peer(`,"endpoint":"203.0.113.1"`),
		"endpoint v6":                  peer(`,"endpoint":"[::1]:51820"`),
		"endpoint injection":           peer(`,"endpoint":"a.example\\nPrivateKey = x:51820"`),
		"endpoint port 0":              peer(`,"endpoint":"203.0.113.1:0"`),
		"duplicate peer":               strings.Replace(peer(``), `}]}`, `},{"public_key":"`+pubA+`","allowed_ips":[]}]}`, 1),
		"read without dev":             `{"type":"read","what":"wireguard"}`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: accepted: %s", name, in)
		}
	}
}

func TestPlanWireGuard(t *testing.T) {
	steps := mustPlan(t, `{"type":"wireguard","action":"ensure","namespace":"gw","name":"wg-hub","listen_port":51820,"key_ref":"`+netID+`"}`)
	var got []string
	for _, s := range steps {
		line := s.Cmd.String()
		if s.Probe != nil {
			line = "unless " + s.Probe.String() + ": " + line
		}
		if s.NeedsConfig {
			line += " <config>"
		}
		got = append(got, line)
	}
	want := []string{
		"unless [gw] ip link show dev wg-hub: [gw] ip link add dev wg-hub type wireguard",
		"[gw] ip link set dev wg-hub mtu 1420",
		"[gw] wg syncconf wg-hub /dev/stdin <config>",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range steps {
		if s.Cmd.Stdin != "" {
			t.Error("a plan holds no standard input: it would hold a secret")
		}
	}
	del := mustPlan(t, `{"type":"wireguard","action":"delete","name":"wg-hub"}`)
	if len(del) != 1 || !del[0].RunIfProbeOK || strings.Join(del[0].Cmd.Args, " ") != "link delete dev wg-hub type wireguard" {
		t.Fatalf("%+v", del)
	}
	if got := ReadCommand(mustDecode(t, `{"type":"read","what":"wireguard","dev":"wg-hub"}`).(*Read)).String(); got != "wg show wg-hub dump" {
		t.Error(got)
	}
}

func keys(id string) (string, string, error) {
	switch id {
	case netID:
		return secPriv, "", nil
	case peerID:
		return "", secPSK, nil
	}
	return "", "", errors.New("unknown key " + id)
}

func TestWireGuardConfigIsBuiltFromTheKeyProviderAndGoesToStdinOnly(t *testing.T) {
	fr := &fakeRunner{}
	e, err := New(fr, WithKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Do(ctx, mustDecode(t, wgOp)); err != nil {
		t.Fatal(err)
	}
	var sync *Command
	for _, c := range fr.commands() {
		if c.Tool == ToolWg {
			c := c
			sync = &c
		}
		// no secret in any argument
		if strings.Contains(strings.Join(c.Args, " "), "SECRET") {
			t.Errorf("a secret in the arguments: %s", c)
		}
	}
	if sync == nil {
		t.Fatal("no wg syncconf")
	}
	want := "[Interface]\nPrivateKey = " + secPriv + "\nListenPort = 51820\n" +
		"\n[Peer]\nPublicKey = " + pubA + "\nPresharedKey = " + secPSK + "\nAllowedIPs = 10.99.0.2/32, 10.50.0.0/24\nPersistentKeepalive = 25\n" +
		"\n[Peer]\nPublicKey = " + pubB + "\nAllowedIPs = 0.0.0.0/0\nEndpoint = 203.0.113.40:51821\n"
	if sync.Stdin != want {
		t.Errorf("config:\n%s\nwant\n%s", sync.Stdin, want)
	}
}

func TestWireGuardWithoutKeysFailsWithoutRunningTheTool(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr) // no key provider
	ctx := context.Background()
	_, _ = e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub"]}`))
	if _, err := e.Do(ctx, mustDecode(t, wgOp)); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("got %v", err)
	}
	for _, c := range fr.commands() {
		if c.Tool == ToolWg {
			t.Error("wg ran without keys")
		}
	}
	// an unknown reference
	e2, _ := New(&fakeRunner{}, WithKeys(func(string) (string, string, error) { return "", "", errors.New("nope") }))
	t.Cleanup(e2.Close)
	_, _ = e2.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub"]}`))
	if _, err := e2.Do(ctx, mustDecode(t, wgOp)); err == nil {
		t.Fatal("a key that cannot be found must fail")
	}
}

func TestWireGuardIsLimitedToAssignedInterfacesAndDeleteChecksTheKind(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolIP && len(c.Args) > 3 && c.Args[0] == "-j" {
			dev := c.Args[len(c.Args)-1]
			switch dev {
			case "wg-hub":
				return Result{Stdout: `[{"ifindex":3,"ifname":"wg-hub","flags":["UP"],"link_type":"none","linkinfo":{"info_kind":"wireguard"}}]`}, nil
			case "br-x":
				return Result{Stdout: `[{"ifindex":4,"ifname":"br-x","flags":["UP"],"link_type":"ether","linkinfo":{"info_kind":"bridge"}}]`}, nil
			}
			return Result{Exit: 1, Stderr: "Device does not exist.\n"}, nil
		}
		return Result{}, nil
	}}
	e, _ := New(fr, WithKeys(keys))
	t.Cleanup(e.Close)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, wgOp)); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("an interface that is not assigned: %v", err)
	}
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub","br-x","gone0"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"wireguard","action":"delete","name":"br-x"}`)); err == nil || !strings.Contains(err.Error(), "not a wireguard") {
		t.Fatalf("a bridge must not be deleted as WireGuard: %v", err)
	}
	for _, name := range []string{"wg-hub", "gone0"} {
		if _, err := e.Do(ctx, mustDecode(t, `{"type":"wireguard","action":"delete","name":"`+name+`"}`)); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestReadWireGuardKeepsNoSecret(t *testing.T) {
	dump := secPriv + "\t" + pubA + "\t51820\toff\n" + pubB + "\t" + secPSK + "\t203.0.113.30:51820\t10.99.0.2/32\t1760000000\t1\t2\t25\n"
	fr := &fakeRunner{respond: func(Command) (Result, error) { return Result{Stdout: dump}, nil }}
	e := newExec(t, fr)
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"wireguard","dev":"wg-hub"}`))
	if err != nil || len(out.Data) != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	if strings.Contains(string(out.Data[0]), "SECRET") {
		t.Fatalf("a secret in the read result: %s", out.Data[0])
	}
	var info linux.WGInfo
	if err := jsonUnmarshal(out.Data[0], &info); err != nil || info.ListenPort != 51820 || len(info.Peers) != 1 || !info.Peers[0].HasPresharedKey {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestAMalformedKeyIsRefusedAndNeverEchoedByAnErrorMessage(t *testing.T) {
	bad := "not-a-key-but-sensitive"
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolWg {
			// the real tool quotes the key it rejects
			return Result{Exit: 1, Stderr: "Key is not the correct length or format: `" + secPriv + "'\n"}, nil
		}
		return Result{}, nil
	}}
	e, _ := New(fr, WithKeys(func(id string) (string, string, error) { return bad, "", nil }))
	t.Cleanup(e.Close)
	ctx := context.Background()
	_, _ = e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub"]}`))
	_, err := e.Do(ctx, mustDecode(t, wgOp))
	if err == nil || strings.Contains(err.Error(), bad) {
		t.Fatalf("a malformed key must be refused without being echoed: %v", err)
	}
	for _, c := range fr.commands() {
		if c.Tool == ToolWg {
			t.Error("wg ran with a malformed key")
		}
	}
	// a well-formed key that wg rejects: the error does not quote the tool's message
	e2, _ := New(fr, WithKeys(keys))
	t.Cleanup(e2.Close)
	_, _ = e2.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wg-hub"]}`))
	_, err = e2.Do(ctx, mustDecode(t, wgOp))
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "correct length") {
		t.Fatalf("the message of wg must not reach the error: %v", err)
	}
}
