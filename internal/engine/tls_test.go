package engine_test

import (
	"context"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The TLS family in the engine (plan §2.8, M21): TLS overlays and the tls part of profiles are accepted, reach the snapshot,
// and the responder asks for the decision of its connections.

// tlsHarness is an engine with a service namespace, the device esp32-42 on 10.10.0.31 (which trusts the test CA) and its network.
func tlsHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.k.ServiceNamespace("cgsvc")
	h.k.AddHolder(100)
	ex, err := executor.New(h.k, executor.WithNetnsInode(h.k.NetnsInode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	var pid atomic.Int64
	pid.Store(100)
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: ex}, Clock: h.clk, DHCP: &fakeDHCP{}, ServiceNS: "cgsvc", ServiceHolderPID: func() int { return int(pid.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e, h.ex = e, ex
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		d.TrustsTestCa = ptr(true)
		(*c.Devices)[devID] = d
	}))
	h.observe()
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	return h
}

func (h *harness) kernelChain(name string) (rules int, ok bool) {
	h.t.Helper()
	rs, err := apply.ReadSets(context.Background(), apply.Local{E: h.ex}, "")
	if err != nil {
		h.t.Fatal(err)
	}
	for _, o := range rs.Objects {
		if o.Chain != nil && o.Chain.Name == name {
			ok = true
		}
		if o.Rule != nil && o.Rule.Chain == name {
			rules++
		}
	}
	return rules, ok
}

// A TLS overlay is accepted, is a case of the snapshot and of the kernel (the chain that selects its connections for the
// responder), and takes both away when it goes.
func TestATLSOverlayIsACaseOfTheSnapshotAndSelectsItsConnectionsInTheKernel(t *testing.T) {
	h := tlsHarness(t)
	if _, ok := h.kernelChain(compiler.TLSChain); ok {
		t.Fatal("a TLS chain without a case")
	}
	res := h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: expired, ports: [8883]}")
	if res.Overlay.Kind != model.OverlayKindTls {
		t.Fatalf("%+v", res.Overlay)
	}
	s := h.barrier()
	if len(s.TLSCases) != 1 || s.TLSCases[0].Overlay != res.Overlay.Id.String() || s.TLSCases[0].Case.Case != model.TlsCaseCaseExpired || s.TLSCases[0].Sources[0] != netip.MustParsePrefix("10.10.0.31/32") {
		t.Fatalf("%+v", s.TLSCases)
	}
	if n, ok := h.kernelChain(compiler.TLSChain); !ok || n != 4 {
		t.Errorf("the kernel's chain: %d rules, there %v", n, ok)
	}
	if n, ok := h.kernelChain("forward"); !ok || n == 0 {
		t.Errorf("forward: %d %v", n, ok)
	}

	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if s := h.barrier(); len(s.TLSCases) != 0 {
		t.Errorf("%+v", s.TLSCases)
	}
	if _, ok := h.kernelChain(compiler.TLSChain); ok {
		t.Error("the chain stays without a case")
	}
}

// "TLS broken" activates (the tls family is not unavailable any more, P2-M11-01) and competes in its family: a case on the
// device beats the profile part of the network.
func TestTheTLSBrokenProfileActivatesAndCompetesInTheTLSFamily(t *testing.T) {
	h := tlsHarness(t)
	prof := h.mustPut(alice, "target: {network: IoT}\nprofile: tls-broken")
	s := h.barrier()
	if len(s.TLSCases) == 0 || s.TLSCases[0].Case.Case != model.TlsCaseCaseHandshakeReset || s.TLSCases[0].Profile != "tls-broken" {
		t.Fatalf("%+v", s.TLSCases)
	}
	dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883")})
	if err != nil || dec.Case == nil || dec.Case.ID != prof.Overlay.Id.String() || dec.Case.TLS.Case != model.TlsCaseCaseHandshakeReset {
		t.Fatalf("%+v %v", dec, err)
	}
	dev := h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: self_signed, ports: [8883]}")
	dec, err = h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883")})
	if err != nil || dec.Case == nil || dec.Case.ID != dev.Overlay.Id.String() {
		t.Fatalf("the device's case is the more specific one: %+v %v", dec, err)
	}
	// another port of the device: the profile's part still applies (443 is a default TLS port)
	dec, err = h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:443")})
	if err != nil || dec.Case == nil || dec.Case.ID != prof.Overlay.Id.String() {
		t.Fatalf("%+v %v", dec, err)
	}
}

// The decision of a connection: the device and what it trusts, the case by port, destination and server name, nothing for
// what no case selects.
func TestTheEngineDecidesAConnectionOfTheResponder(t *testing.T) {
	h := tlsHarness(t)
	if _, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883")}); err != nil {
		t.Fatalf("without a case: %v", err)
	}
	ov := h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: hostname_mismatch, ports: [8883], sni: ['*.example.org']}")
	ask := func(client, dst, sni string, names ...string) engine.TLSDecision {
		t.Helper()
		dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr(client), Dst: netip.MustParseAddrPort(dst), SNI: sni, Names: names})
		if err != nil {
			t.Fatal(err)
		}
		return dec
	}
	dec := ask("10.10.0.31", "203.0.113.10:8883", "Mqtt.Example.ORG")
	if dec.Case == nil || dec.Case.ID != ov.Overlay.Id.String() || dec.Device != devID || dec.DeviceName != "esp32-42" || !dec.TrustsTestCA {
		t.Fatalf("%+v", dec)
	}
	for name, d := range map[string]engine.TLSDecision{
		"another server name": ask("10.10.0.31", "203.0.113.10:8883", "mqtt.example.com"),
		"no server name":      ask("10.10.0.31", "203.0.113.10:8883", ""),
		"another port":        ask("10.10.0.31", "203.0.113.10:443", "mqtt.example.org"),
		"another client":      ask("10.10.0.99", "203.0.113.10:8883", "mqtt.example.org"),
	} {
		if d.Case != nil {
			t.Errorf("%s: %+v", name, d.Case)
		}
	}
	// a client no device owns is still a client of its network, without a device
	h.mustPut(bob, "target: {network: IoT}\ntls: {case: untrusted_ca}")
	dec = ask("10.10.0.99", "203.0.113.10:8883", "")
	if dec.Case == nil || dec.Case.TLS.Case != model.TlsCaseCaseUntrustedCa || dec.Device != "" || dec.TrustsTestCA {
		t.Errorf("%+v", dec)
	}
	// a destination that names a host matches the names the DNS answered
	h.mustPut(bob, "target: {device: esp32-42}\ntls: {case: self_signed, destination: {hostname: cdn.example.net}}")
	if d := ask("10.10.0.31", "203.0.113.50:443", "", "cdn.example.net"); d.Case == nil || d.Case.TLS.Case != model.TlsCaseCaseSelfSigned {
		t.Errorf("%+v", d)
	}
	if d := ask("10.10.0.31", "203.0.113.50:443", ""); d.Case == nil || d.Case.TLS.Case != model.TlsCaseCaseUntrustedCa {
		t.Errorf("without the name the network's case applies: %+v", d)
	}
}

// A connection that no case decides is passed on only if the gateway would have forwarded it: the access matrix is asked about the
// destination the device meant (the responder's own path to it does not pass the matrix).
func TestAConnectionNoCaseDecidesMayPassThroughOnlyWhereTheGatewayWouldForwardIt(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {network: IoT}\ntls: {case: expired, ports: [8883], sni: [broker.example.com]}")
	ask := func() engine.TLSDecision {
		t.Helper()
		dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883"), SNI: "other.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return dec
	}
	if dec := ask(); dec.Case != nil || !dec.Passthrough {
		t.Fatalf("a local network reaches the uplink by default: %+v", dec)
	}
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		d.TrustsTestCa = ptr(true)
		(*c.Devices)[devID] = d
		c.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{{
			From: model.MatrixEndpoint{Network: ptr(iotID)}, To: model.MatrixEndpoint{Uplink: ptr(model.MatrixEndpointUplink(true))}, Policy: model.MatrixEntryPolicyDeny}}}
	}))
	if dec := ask(); dec.Case != nil || dec.Passthrough {
		t.Errorf("the matrix keeps the network from the uplink: %+v", dec)
	}
	// a connection a case decides is the responder's whatever the matrix says: the answer comes from the gateway
	if dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:8883"), SNI: "broker.example.com"}); err != nil || dec.Case == nil || dec.Passthrough {
		t.Errorf("%+v %v", dec, err)
	}
}

// The namespace's connection reaches the other test networks since P5-M21-02, so the matrix decides there as it does for the
// uplink: a server in another test network is passed on only where an entry allows it (the default keeps the networks apart), and
// the management network stays out of reach whatever the matrix says for the other networks.
func TestAConnectionNoCaseDecidesIsPassedToAnotherTestNetworkOnlyWhereTheMatrixAllowsIt(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {network: IoT}\ntls: {case: expired, ports: [8883], sni: [broker.example.com]}")
	ask := func(dst string) engine.TLSDecision {
		t.Helper()
		dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort(dst), SNI: "other.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return dec
	}
	if dec := ask("10.20.0.13:8883"); dec.Case != nil || dec.Passthrough {
		t.Fatalf("the default keeps the test networks apart: %+v", dec)
	}
	setMatrix := func(entries ...model.MatrixEntry) {
		h.mustApply(h.revision(func(c *model.Configuration) {
			withDHCPAndDevice(c)
			d := (*c.Devices)[devID]
			d.TrustsTestCa = ptr(true)
			(*c.Devices)[devID] = d
			c.AccessMatrix = &model.AccessMatrix{Entries: &entries}
		}))
	}
	iot, lab := iotID, "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"
	setMatrix(model.MatrixEntry{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &lab}, Policy: model.MatrixEntryPolicyAllow})
	if dec := ask("10.20.0.13:8883"); dec.Case != nil || !dec.Passthrough {
		t.Errorf("an entry allows the network IoT to reach the network Lab: %+v", dec)
	}
	// the management network stays closed to a device under test (the guard), whatever is allowed towards the other networks
	if dec := ask("192.168.56.10:8883"); dec.Case != nil || dec.Passthrough {
		t.Errorf("a connection to the management network is passed on: %+v", dec)
	}
	setMatrix(model.MatrixEntry{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &lab}, Policy: model.MatrixEntryPolicyDeny})
	if dec := ask("10.20.0.13:8883"); dec.Case != nil || dec.Passthrough {
		t.Errorf("a deny entry keeps the network IoT from the network Lab: %+v", dec)
	}
}

func TestExplainSaysTheTLSResponderAnswersWhenACaseDecides(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: expired, ports: [8883]}")
	got, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 8883})
	if err != nil || got.Service != "tls_responder" {
		t.Fatalf("%+v %v", got, err)
	}
	found := false
	for _, f := range got.Faults {
		found = found || (f.Family == "tls" && f.Winner != nil)
	}
	if !found {
		t.Errorf("%+v", got.Faults)
	}
	// another port: no case, nothing answers
	if got, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 22}); err != nil || got.Service != "none" {
		t.Errorf("%+v %v", got, err)
	}
	// E11: an access rule stands in front of the redirect
	h.mustPut(alice, "target: {device: esp32-42}\nrule: {action: drop, protocol: tcp, ports: [8883]}")
	if got, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 8883}); err != nil || got.Service != "none" || got.Access.Verdict != "drop" {
		t.Errorf("%+v %v", got, err)
	}
}

func TestAnOverlayWithoutAServiceNamespaceStaysACaseButRedirectsNothing(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, "target: {network: IoT}\ntls: {case: expired}")
	s := h.barrier()
	if len(s.TLSCases) != 1 {
		t.Fatalf("%+v", s.TLSCases)
	}
	if _, ok := h.kernelChain(compiler.TLSChain); ok {
		t.Error("a redirect without a service namespace")
	}
	found := false
	for _, p := range s.Problems {
		found = found || strings.Contains(p.Message, "TLS responder")
	}
	if !found {
		t.Errorf("%+v", s.Problems)
	}
}

// ---- the case intercept (M22)

// The case intercept is accepted with its settings, is a case of the snapshot (the proxy fetches its rules from it), selects
// its connections in the kernel and drops UDP 443 of its clients (block_quic), and takes all of it away when it goes.
func TestAnInterceptionCaseIsACaseOfTheSnapshotAndDropsQUICInTheKernel(t *testing.T) {
	h := tlsHarness(t)
	res := h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: intercept, ports: [443], intercept: {http_rules: [{match: {host: api.example.com, path_prefix: /v1}, action: status, status: 503}]}}")
	s := h.barrier()
	if len(s.TLSCases) != 1 || s.TLSCases[0].Overlay != res.Overlay.Id.String() || s.TLSCases[0].Case.Case != model.TlsCaseCaseIntercept {
		t.Fatalf("%+v", s.TLSCases)
	}
	rules := s.TLSCases[0].Case.Intercept.HttpRules
	if rules == nil || len(*rules) != 1 || (*rules)[0].Action != model.HttpRuleActionStatus || *(*rules)[0].Status != 503 {
		t.Errorf("the rules are in the snapshot: %+v", s.TLSCases[0].Case.Intercept)
	}
	// the selection (two rules) and the drop of QUIC
	if n, ok := h.kernelChain(compiler.TLSChain); !ok || n != 5 {
		t.Errorf("the kernel's chain: %d rules, there %v", n, ok)
	}
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if s := h.barrier(); len(s.TLSCases) != 0 {
		t.Errorf("%+v", s.TLSCases)
	}
	if _, ok := h.kernelChain(compiler.TLSChain); ok {
		t.Error("the chain stays without a case")
	}
}

// A profile with the part intercept activates and its case decides the connections of the network.
func TestAProfileWithAnInterceptPartActivatesAndItsCaseDecides(t *testing.T) {
	h := tlsHarness(t)
	const prof = "1f2e3d4c-5b6a-4798-8a9b-0c1d2e3f4a5b"
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		d.TrustsTestCa = ptr(true)
		(*c.Devices)[devID] = d
		c.Profiles = &map[string]model.Profile{prof: {Name: "inspect", Parts: model.ProfileParts{Tls: &model.TlsCase{Case: "intercept"}}}}
	}))
	res := h.mustPut(alice, "target: {network: IoT}\nprofile: inspect")
	dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:443"), SNI: "api.example.com"})
	if err != nil || dec.Case == nil || dec.Case.ID != res.Overlay.Id.String() || dec.Case.TLS.Case != model.TlsCaseCaseIntercept {
		t.Fatalf("%+v %v", dec, err)
	}
}

// The proxy's own connection to the server leaves through the gateway's service path, so the case intercept is judged like a
// passed-through connection: the responder hands the connection over only where the gateway would have forwarded it.
func TestAnInterceptedConnectionIsJudgedByTheAccessMatrixLikeAPassedThroughOne(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {network: IoT}\ntls: {case: intercept}")
	ask := func() engine.TLSDecision {
		t.Helper()
		dec, err := h.e.ResolveTLS(engine.TLSConnection{Client: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddrPort("203.0.113.10:443"), SNI: "api.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return dec
	}
	if dec := ask(); dec.Case == nil || !dec.Passthrough {
		t.Fatalf("a local network reaches the uplink by default: %+v", dec)
	}
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		d.TrustsTestCa = ptr(true)
		(*c.Devices)[devID] = d
		c.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{{
			From: model.MatrixEndpoint{Network: ptr(iotID)}, To: model.MatrixEndpoint{Uplink: ptr(model.MatrixEndpointUplink(true))}, Policy: model.MatrixEntryPolicyDeny}}}
	}))
	if dec := ask(); dec.Case == nil || dec.Passthrough {
		t.Errorf("the matrix keeps the network from the uplink, so the proxy must not be the way through: %+v", dec)
	}
}

// explain names the proxy as what answers when the winning case is an interception.
func TestExplainSaysTheProxyAnswersWhenAnInterceptionCaseDecides(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {device: esp32-42}\ntls: {case: intercept, ports: [443]}")
	got, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 443})
	if err != nil || got.Service != "tls_proxy" {
		t.Fatalf("%+v %v", got, err)
	}
	// a drop rule in front of the redirect (E11) stops it as it does for the responder
	h.mustPut(alice, "target: {device: esp32-42}\nrule: {action: drop, protocol: tcp, ports: [443]}")
	if got, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 443}); err != nil || got.Service != "none" {
		t.Errorf("%+v %v", got, err)
	}
}
