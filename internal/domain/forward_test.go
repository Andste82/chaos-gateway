package domain

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// The port forward of api/examples/configuration.yaml (M35a): tcp/8080 on the uplink to esp32-42:80.
const idForward = "6d7e8f9a-0b1c-4d2e-8f3a-4b5c6d7e8f9a"

// fwdPath is the pointer of a field of a forward.
func fwdPath(id, field string) string { return schema.Pointer("/port_forwards", id) + "/" + field }

// withForward adds a forward (a YAML flow literal) under idNew to the example and validates it.
func withForward(t *testing.T, f string, mod ...func(d doc)) ValidationErrors {
	t.Helper()
	d := baseDoc(t)
	d.set(t, obj(t, f), "port_forwards", idNew)
	for _, m := range mod {
		m(d)
	}
	return d.validate(t)
}

// twoPorts makes the management interface the uplink interface.
func twoPorts(t *testing.T) func(d doc) {
	return func(d doc) { d.set(t, obj(t, `{interface: {name: wan0}}`), "management") }
}

func TestTheExampleForwardIsValidAndFillsItsDefaults(t *testing.T) {
	cfg := exampleConfiguration(t)
	f, ok := deref(cfg.PortForwards)[idForward]
	if !ok {
		t.Fatal("the example has no forward")
	}
	got := ForwardOf(idForward, f)
	if got.ExtFrom != 8080 || got.ExtTo != 8080 || got.TgtFrom != 80 || got.TgtTo != 80 || !got.Enabled || got.Entry != EntryUplink || got.Protocol != "tcp" {
		t.Fatalf("forward = %+v", got)
	}
	// a range is shifted one to one, and the target port defaults to the external one
	r := ForwardOf(idNew, model.PortForward{Protocol: "udp", Entry: "uplink", ExternalPort: 8000, ExternalPortEnd: ptrTo(8010), TargetPort: ptrTo(9000)})
	if r.TgtTo != 9010 || r.TargetPortOf(8003) != 9003 || r.ExternalPorts() != "8000-8010" || r.TargetPorts() != "9000-9010" || !r.Covers(8010) || r.Covers(8011) {
		t.Fatalf("range = %+v", r)
	}
	same := ForwardOf(idNew, model.PortForward{Protocol: "tcp", Entry: "uplink", ExternalPort: 5000, ExternalPortEnd: ptrTo(5002)})
	if same.TgtFrom != 5000 || same.TgtTo != 5002 {
		t.Fatalf("default target range = %+v", same)
	}
}

func TestTwoForwardsOnTheSameProtocolMustNotOverlap(t *testing.T) {
	// the example forwards tcp/8080
	errs := withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8100, target: {address: 10.10.0.20}}`)
	// idNew sorts after idForward: the later one is reported, with the earlier one named
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeOverlappingForward)
	if !strings.Contains(dump(errs), "/port_forwards/"+idForward) {
		t.Fatalf("the message does not name the other forward:\n%s", dump(errs))
	}
	// whatever the entry side
	errs = withForward(t, `{protocol: tcp, entry: management, external_port: 8080, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeOverlappingForward)
	// a disabled forward still holds its port
	errs = withForward(t, `{protocol: tcp, entry: uplink, external_port: 8080, enabled: false, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeOverlappingForward)
	// another protocol, or a range next to it, is fine
	wantValid(t, withForward(t, `{protocol: udp, entry: uplink, external_port: 8080, target: {address: 10.10.0.20}}`))
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 8081, external_port_end: 8090, target: {address: 10.10.0.20}}`))
}

func TestForwardRangesMustBeOrderedAndOfTheSameLength(t *testing.T) {
	errs := withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8010, target: {address: 10.10.0.20}, target_port: 9000, target_port_end: 9005}`)
	wantError(t, errs, fwdPath(idNew, "target_port_end"), CodePortRangeLength)

	errs = withForward(t, `{protocol: tcp, entry: uplink, external_port: 8010, external_port_end: 8000, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "external_port_end"), CodeInvalidPortRange)

	errs = withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8010, target: {address: 10.10.0.20}, target_port: 9010, target_port_end: 9000}`)
	wantError(t, errs, fwdPath(idNew, "target_port_end"), CodeInvalidPortRange)

	// the implied target range must end in the port space
	errs = withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8010, target: {address: 10.10.0.20}, target_port: 65530}`)
	wantError(t, errs, fwdPath(idNew, "target_port"), CodeInvalidPortRange)

	// a range with a target range of the same length, or with only its start
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8010, target: {address: 10.10.0.20}, target_port: 9000, target_port_end: 9010}`))
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 8000, external_port_end: 8010, target: {address: 10.10.0.20}, target_port: 9000}`))
}

func TestAForwardNeverTakesTheUIPort(t *testing.T) {
	setUI := func(t *testing.T) func(d doc) {
		return func(d doc) { d.set(t, 9443, "management", "ui_port") }
	}
	errs := withForward(t, `{protocol: tcp, entry: uplink, external_port: 9443, target: {address: 10.10.0.20}}`, setUI(t))
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	// inside a range, and on udp too
	errs = withForward(t, `{protocol: udp, entry: uplink, external_port: 9400, external_port_end: 9500, target: {address: 10.10.0.20}}`, setUI(t))
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)

	// without ui_port the port the API listens on stands in, which only the caller knows
	d := baseDoc(t)
	d.set(t, obj(t, `{protocol: tcp, entry: uplink, external_port: 8443, target: {address: 10.10.0.20}}`), "port_forwards", idNew)
	raw, _ := json.Marshal(d)
	cfg, err := NewCandidate(nil, raw, FormatJSON, CandidateFull, t0, WithUIPort(8443))
	var ve ValidationErrors
	if !asValidation(err, &ve) {
		t.Fatalf("cfg %v, err %v", cfg != nil, err)
	}
	wantError(t, ve, fwdPath(idNew, "external_port"), CodeReservedPort)
	if _, err := NewCandidate(nil, raw, FormatJSON, CandidateFull, t0, WithUIPort(443)); err != nil {
		t.Fatalf("another listen port: %v", err)
	}
	// management.ui_port wins over the listen port
	d.set(t, 9443, "management", "ui_port")
	raw, _ = json.Marshal(d)
	if _, err := NewCandidate(nil, raw, FormatJSON, CandidateFull, t0, WithUIPort(8443)); err != nil {
		t.Fatalf("ui_port names another port: %v", err)
	}
}

func TestAForwardNeverTakesAWireGuardListenPort(t *testing.T) {
	// the hub listens on 51820, the link on 51821
	for _, port := range []string{"51820", "51821"} {
		errs := withForward(t, `{protocol: udp, entry: uplink, external_port: `+port+`, target: {address: 10.10.0.20}}`)
		wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	}
	// tcp on the same port is not WireGuard's
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 51820, target: {address: 10.10.0.20}}`))
}

func TestAForwardTakesSSHOnlyOnAnUplinkOfItsOwn(t *testing.T) {
	errs := withForward(t, `{protocol: tcp, entry: management, external_port: 22, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	errs = withForward(t, `{protocol: tcp, entry: management, external_port: 20, external_port_end: 30, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	// the two-port topology's shared interface is the management interface too
	errs = withForward(t, `{protocol: tcp, entry: uplink, external_port: 22, target: {address: 10.10.0.20}}`, twoPorts(t))
	wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	// an uplink of its own (three ports) may forward 22, and udp/22 is no SSH
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 22, target: {address: 10.10.0.20}}`))
	wantValid(t, withForward(t, `{protocol: udp, entry: management, external_port: 22, target: {address: 10.10.0.20}}`))
}

func TestAForwardLeavesTheBGPPortWhileBGPRuns(t *testing.T) {
	// the example runs BGP on the link site-b
	for _, entry := range []string{"uplink", "management"} {
		errs := withForward(t, `{protocol: tcp, entry: `+entry+`, external_port: 179, target: {address: 10.10.0.20}}`)
		wantError(t, errs, fwdPath(idNew, "external_port"), CodeReservedPort)
	}
	wantValid(t, withForward(t, `{protocol: udp, entry: uplink, external_port: 179, target: {address: 10.10.0.20}}`))
	// a disabled BGP protocol, or none, leaves the port free
	disable := func(d doc) { d.set(t, false, "routing", "protocols", idProtocol, "enabled") }
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 179, target: {address: 10.10.0.20}}`, disable))
	ospf := func(d doc) {
		p := d.node(t, "routing", "protocols", idProtocol).(map[string]any)
		p["type"] = "ospf"
		delete(p, "bgp")
	}
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 179, target: {address: 10.10.0.20}}`, ospf))
}

func TestTheManagementSideIsRefusedInTheTwoPortTopology(t *testing.T) {
	errs := withForward(t, `{protocol: tcp, entry: management, external_port: 9000, target: {address: 10.10.0.20}}`, twoPorts(t))
	wantError(t, errs, fwdPath(idNew, "entry"), CodeManagementIsUplink)
	wantValid(t, withForward(t, `{protocol: tcp, entry: management, external_port: 9000, target: {address: 10.10.0.20}}`))
	wantValid(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}}`, twoPorts(t)))
}

func TestAForwardTargetLiesInALocalTestNetwork(t *testing.T) {
	addr := func(a string) string {
		return `{protocol: tcp, entry: uplink, external_port: 9000, target: {address: ` + a + `}}`
	}
	for _, c := range []struct{ addr, why string }{
		{"10.10.0.1", "the gateway's own address in IoT"},
		{"10.99.0.1", "the gateway's own address in the hub"},
		{"10.99.0.2", "a WireGuard client"},
		{"10.50.0.10", "a client network"},
		{"192.168.88.5", "the management network"},
		{"203.0.113.9", "behind the uplink"},
		{"10.10.0.0", "the network address of IoT"},
		{"10.10.0.255", "the broadcast address of IoT"},
	} {
		wantError(t, withForward(t, addr(c.addr)), fwdPath(idNew, "target/address"), CodeTargetKind)
	}
	wantValid(t, withForward(t, addr("10.10.0.20")))

	dev := func(ref string) string {
		return `{protocol: tcp, entry: uplink, external_port: 9000, target: {device: ` + ref + `}}`
	}
	wantError(t, withForward(t, dev("lab-rA")), fwdPath(idNew, "target/device"), CodeTargetKind)
	wantError(t, withForward(t, dev("nobody")), fwdPath(idNew, "target/device"), CodeUnknownReference)
	// a device in a local test network, a device without a network (its address decides) and a probe
	for _, ref := range []string{"esp32-42", "lab-host", "probe-iot", idESP} {
		wantValid(t, withForward(t, dev(ref)))
	}
	// a device configured in a WireGuard network
	inHub := func(d doc) { d.set(t, "lab-hub", "devices", idLab, "network") }
	wantError(t, withForward(t, dev("lab-host"), inHub), fwdPath(idNew, "target/device"), CodeTargetKind)

	// exactly one of device and address
	both := `{protocol: tcp, entry: uplink, external_port: 9000, target: {device: esp32-42, address: 10.10.0.20}}`
	wantError(t, withForward(t, both), fwdPath(idNew, "target"), schema.CodeMaxProperties)
	wantError(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 9000, target: {}}`), fwdPath(idNew, "target"), schema.CodeMinProperties)
}

func TestAForwardIsOfTcpOrUdpWithAKnownEntrySide(t *testing.T) {
	wantError(t, withForward(t, `{protocol: any, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}}`), fwdPath(idNew, "protocol"), schema.CodeEnum)
	wantError(t, withForward(t, `{protocol: tcp, entry: lan, external_port: 9000, target: {address: 10.10.0.20}}`), fwdPath(idNew, "entry"), schema.CodeEnum)
	wantError(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 0, target: {address: 10.10.0.20}}`), fwdPath(idNew, "external_port"), schema.CodeMinimum)
	wantError(t, withForward(t, `{protocol: tcp, entry: uplink, target: {address: 10.10.0.20}}`), fwdPath(idNew, "external_port"), schema.CodeRequired)
	wantError(t, withForward(t, `{protocol: tcp, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}, color: red}`), fwdPath(idNew, "color"), schema.CodeUnknownField)
}

func TestForwardNamesAreUniqueAndTheKeysAreUUIDs(t *testing.T) {
	errs := withForward(t, `{name: ESP-WEB, protocol: tcp, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}}`)
	wantError(t, errs, fwdPath(idNew, "name"), CodeDuplicateName)
	// another kind may use the name
	wantValid(t, withForward(t, `{name: no-dot, protocol: tcp, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}}`))

	d := baseDoc(t)
	d.set(t, obj(t, `{protocol: tcp, entry: uplink, external_port: 9000, target: {address: 10.10.0.20}}`), "port_forwards", "web")
	wantError(t, d.validate(t), "/port_forwards/web", CodeInvalidID)
}

func TestForwardsAreChangedThroughAMergePatchLikeAccessRules(t *testing.T) {
	base, err := NewCandidate(nil, readExample(t, examplePath), FormatYAML, CandidateFull, t0)
	if err != nil {
		t.Fatal(err)
	}
	// add one, naming the target device by name: the candidate stores its UUID
	patch := `{"port_forwards": {"` + idNew + `": {"name": "mqtt", "protocol": "tcp", "entry": "uplink", "external_port": 1883, "target": {"device": "esp32-42"}}}}`
	cfg, err := NewCandidate(base, []byte(patch), FormatJSON, CandidatePatch, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := deref(cfg.PortForwards)[idNew]
	if got.Target.Device == nil || *got.Target.Device != idESP || len(deref(cfg.PortForwards)) != 2 {
		t.Fatalf("forwards = %+v", deref(cfg.PortForwards))
	}
	changes := Diff(base, cfg)
	if len(changes) != 1 || changes[0].Kind != model.DomainChangeKindPortForward || changes[0].Op != "added" ||
		changes[0].Summary != `port forward "mqtt" (tcp/1883 from the uplink to device esp32-42) added` {
		t.Fatalf("diff = %v", summaries(changes))
	}
	// disable it, then delete the example's
	next, err := NewCandidate(cfg, []byte(`{"port_forwards": {"`+idNew+`": {"enabled": false}, "`+idForward+`": null}}`), FormatJSON, CandidatePatch, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := deref(next.PortForwards)[idForward]; ok || deref(deref(next.PortForwards)[idNew].Enabled) {
		t.Fatalf("forwards = %+v", deref(next.PortForwards))
	}
	changes = Diff(cfg, next)
	if len(changes) != 2 || changes[0].Op != "removed" || changes[1].Op != "changed" || !strings.Contains(changes[1].Summary, "enabled (unset) → false") {
		t.Fatalf("diff = %v", summaries(changes))
	}
	// a forward whose target device goes is an unknown reference
	_, err = NewCandidate(base, []byte(`{"devices": {"`+idESP+`": null}, "groups": null, "faults": null, "profiles": null, "scenarios": null}`), FormatJSON, CandidatePatch, t0)
	var ve ValidationErrors
	if !asValidation(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	wantError(t, ve, fwdPath(idForward, "target/device"), CodeUnknownReference)
}

// forwardWorld is the example with its forward and the rules and overlays that mod adds.
func forwardWorld(t *testing.T, mod func(*model.Configuration), overlays ...model.Overlay) (*World, Forward) {
	t.Helper()
	cfg, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if mod != nil {
		mod(cfg)
	}
	w, err := NewWorld(cfg, overlays)
	if err != nil {
		t.Fatal(err)
	}
	return w, ForwardOf(idForward, deref(cfg.PortForwards)[idForward])
}

func TestAForwardFollowsItsDeviceAndSaysWhyItIsInactive(t *testing.T) {
	w, f := forwardWorld(t, nil)
	a := netip.MustParseAddr
	known := ForwardFacts{Identity: Identity{Addresses: map[string][]netip.Addr{idESP: {a("10.10.0.42")}}}}
	st := w.Index.ForwardStatus(f, known)
	if !st.Active || st.Target != a("10.10.0.42") || st.Network != idIoT {
		t.Fatalf("status = %+v", st)
	}
	// a new address is followed
	moved := ForwardFacts{Identity: Identity{Addresses: map[string][]netip.Addr{idESP: {a("10.10.0.77")}}}}
	if st := w.Index.ForwardStatus(f, moved); !st.Active || st.Target != a("10.10.0.77") {
		t.Fatalf("moved = %+v", st)
	}
	for _, c := range []struct {
		name   string
		f      func(Forward) Forward
		facts  ForwardFacts
		reason string
	}{
		{"no address", nil, ForwardFacts{}, ForwardDeviceNoAddress},
		{"outside", nil, ForwardFacts{Identity: Identity{Addresses: map[string][]netip.Addr{idESP: {a("10.50.0.3")}}}}, ForwardDeviceOutside},
		{"disabled", func(f Forward) Forward { f.Enabled = false; return f }, known, ForwardDisabled},
		{"two ports", func(f Forward) Forward { f.Entry = EntryManagement; return f }, ForwardFacts{Identity: known.Identity, TwoPort: true}, ForwardManagementIsUplink},
		{"no uplink address", nil, ForwardFacts{Identity: known.Identity, Entry: map[string]bool{EntryManagement: true}}, ForwardEntryWithoutAddress},
	} {
		g := f
		if c.f != nil {
			g = c.f(f)
		}
		st := w.Index.ForwardStatus(g, c.facts)
		if st.Active || st.Reason != c.reason || !strings.HasPrefix(st.Message, "inactive: ") {
			t.Errorf("%s: %+v", c.name, st)
		}
	}
	if st := w.Index.ForwardStatus(f, ForwardFacts{Identity: known.Identity, Entry: map[string]bool{EntryUplink: true}}); !st.Active {
		t.Fatalf("with an uplink address: %+v", st)
	}
	// an address target needs no observed state
	addr := Forward{ID: idNew, Protocol: "tcp", Entry: EntryUplink, ExtFrom: 9000, ExtTo: 9000, TgtFrom: 9000, TgtTo: 9000, Enabled: true, Address: a("10.10.0.20")}
	if st := w.Index.ForwardStatus(addr, ForwardFacts{}); !st.Active || st.Target != a("10.10.0.20") || st.Network != idIoT {
		t.Fatalf("address target = %+v", st)
	}
	if w.Index.TargetLabel(f) != "device esp32-42" || w.Index.TargetLabel(addr) != "10.10.0.20" {
		t.Fatalf("labels %q %q", w.Index.TargetLabel(f), w.Index.TargetLabel(addr))
	}
}

func TestMatchForwardFindsTheEnabledForwardOfAPort(t *testing.T) {
	fs := []Forward{
		{ID: "a", Protocol: "tcp", Entry: EntryUplink, ExtFrom: 8000, ExtTo: 8010, Enabled: true},
		{ID: "b", Protocol: "udp", Entry: EntryUplink, ExtFrom: 8000, ExtTo: 8000, Enabled: false},
		{ID: "c", Protocol: "tcp", Entry: EntryManagement, ExtFrom: 9000, ExtTo: 9000, Enabled: true},
	}
	if f, ok := MatchForward(fs, EntryUplink, "tcp", 8005); !ok || f.ID != "a" {
		t.Fatalf("tcp/8005 = %+v %v", f, ok)
	}
	for _, c := range []struct {
		entry, proto string
		port         int
	}{{EntryUplink, "udp", 8000}, {EntryUplink, "tcp", 8011}, {EntryUplink, "tcp", 9000}, {EntryManagement, "tcp", 8000}} {
		if f, ok := MatchForward(fs, c.entry, c.proto, c.port); ok {
			t.Errorf("%+v matched %+v", c, f)
		}
	}
}

// A forwarded connection is judged by the access rules with the forward's target as responder (plan §2.4,
// D34): a rule's source against the target, its destination against the peer, its ports against the target
// port. Without a deciding rule the forward itself accepts it.
func TestAccessRulesJudgeAForwardedConnectionWithTheDeviceAsResponder(t *testing.T) {
	a := netip.MustParseAddr
	mgmt := []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}
	rule := func(id string, r model.AccessRule) func(*model.Configuration) {
		return func(c *model.Configuration) {
			rules := deref(c.AccessRules)
			rules[id] = r
			c.AccessRules = &rules
			order := append([]uuid.UUID{uuid.MustParse(id)}, deref(c.AccessRuleOrder)...)
			c.AccessRuleOrder = &order
		}
	}
	idR := "a1000000-0000-4000-8000-000000000001"
	esp, iot := idESP, idIoT
	up := model.DestinationUplink(true)
	tcp := model.Protocol("tcp")
	decide := func(w *World, f Forward, peer string) AccessExplanation {
		return w.ForwardedDecision(ForwardedQuery{Forward: f, Target: a("10.10.0.42"), Network: idIoT, Peer: a(peer), Protocol: "tcp", Port: 8080}, AccessFacts{Management: mgmt})
	}

	// without a rule that selects it: the forward accepts (the example's rule is about tcp/853)
	w, f := forwardWorld(t, nil)
	if got := decide(w, f, "198.51.100.7"); got.Verdict != "allow" || got.Layer != AccessPortForward || got.Rule != "" {
		t.Fatalf("no rule: %+v", got)
	}

	// an existing rule "device → uplink: drop" (a scenario's offline step) also stops it
	w, f = forwardWorld(t, rule(idR, model.AccessRule{Source: model.Scope{Device: &esp}, Action: "drop", Destination: &model.Destination{Uplink: &up}}))
	if got := decide(w, f, "198.51.100.7"); got.Verdict != "drop" || got.Layer != AccessConfigRule || got.Rule != idR {
		t.Fatalf("device to uplink: %+v", got)
	}
	// a peer in the management network is not behind the uplink
	if got := decide(w, f, "192.168.56.9"); got.Verdict != "allow" || got.Layer != AccessPortForward {
		t.Fatalf("management peer: %+v", got)
	}

	// a network rule without a destination, on the target port (80), not on the external one (8080)
	w, f = forwardWorld(t, rule(idR, model.AccessRule{Source: model.Scope{Network: &iot}, Action: "reset", Protocol: &tcp, Ports: &[]int{80}}))
	if got := decide(w, f, "192.168.56.9"); got.Verdict != "reset" || got.Rule != idR {
		t.Fatalf("network rule on the target port: %+v", got)
	}
	w, f = forwardWorld(t, rule(idR, model.AccessRule{Source: model.Scope{Network: &iot}, Action: "reject", Protocol: &tcp, Ports: &[]int{8080}}))
	if got := decide(w, f, "198.51.100.7"); got.Layer != AccessPortForward {
		t.Fatalf("a rule on the external port must not match: %+v", got)
	}

	// an overlay rule comes first
	ov := model.Overlay{Id: uuid.MustParse(idNew2), Kind: "rule", UpdatedAt: t0, Target: &model.Scope{Device: &esp},
		Rule: &model.AccessRuleBody{Action: "reject"}}
	w, f = forwardWorld(t, nil, ov)
	if got := decide(w, f, "198.51.100.7"); got.Verdict != "reject" || got.Layer != AccessOverlayRule || got.Rule != idNew2 {
		t.Fatalf("overlay: %+v", got)
	}

	// an address target that a device owns is that device for the rules
	w, _ = forwardWorld(t, rule(idR, model.AccessRule{Source: model.Scope{Device: &esp}, Action: "drop"}))
	addr := Forward{ID: idNew, Protocol: "tcp", Entry: EntryUplink, ExtFrom: 9000, ExtTo: 9000, TgtFrom: 9000, TgtTo: 9000, Enabled: true, Address: a("10.10.0.42")}
	got := w.ForwardedDecision(ForwardedQuery{Forward: addr, Target: addr.Address, Network: idIoT, Device: idESP, Peer: a("198.51.100.7"), Protocol: "tcp", Port: 9000}, AccessFacts{})
	if got.Verdict != "drop" || got.Rule != idR {
		t.Fatalf("owned address: %+v", got)
	}
}

func TestThePreviewNotesTheFaultsThatDoNotApplyToAForward(t *testing.T) {
	a := netip.MustParseAddr
	known := ForwardFacts{Identity: Identity{Addresses: map[string][]netip.Addr{idESP: {a("10.10.0.42")}}}}
	// the example's faults select esp32-42: its own latency wins, and the mtu fault names a destination
	w, f := forwardWorld(t, nil)
	notes := w.ForwardFaultNotes([]Forward{f}, known)
	if len(notes) != 1 || notes[0].Fault.ID != idFaultDev || notes[0].Fault.Family != FamilyImpairment || notes[0].Forward.ID != idForward {
		t.Fatalf("notes = %+v", notes)
	}
	// without faults, or for a disabled forward, there is nothing to say
	w, f = forwardWorld(t, func(c *model.Configuration) { c.Faults, c.Scenarios, c.Profiles = nil, nil, nil })
	if notes := w.ForwardFaultNotes([]Forward{f}, known); len(notes) != 0 {
		t.Fatalf("without faults: %+v", notes)
	}
	w, f = forwardWorld(t, nil)
	f.Enabled = false
	if notes := w.ForwardFaultNotes([]Forward{f}, known); len(notes) != 0 {
		t.Fatalf("disabled: %+v", notes)
	}
}
