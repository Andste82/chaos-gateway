//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The acceptance tests of M9 on the real kernel, through the real engine, with NAT (the server has no
// route back to the test networks): the behavior matrix of spike S3 (C0 to C6), what a cut takes and what
// it leaves, the order of overlay and configured rules together with `explain` against the kernel's
// verdicts, and the anti-lockout rule against a rule that selects the management network. Nothing here
// is statistical: the functional assertions hold under emulation.

// matrixServerScript echoes on tcp 9101 to 9120 and on udp 5353.
const matrixServerScript = `
import socket, threading
def tcp(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(50)
    while True:
        c, _ = s.accept()
        def h(c):
            try:
                while True:
                    d = c.recv(100)
                    if not d: break
                    c.sendall(d)
            except Exception: pass
        threading.Thread(target=h, args=(c,), daemon=True).start()
for p in range(9101, 9121):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.bind(("0.0.0.0", 5353))
while True:
    d, a = u.recvfrom(100); u.sendto(d, a)
`

func startMatrixServer(t *testing.T, ns *testbed.Namespace) {
	t.Helper()
	ns.Start("python3", "-c", matrixServerScript)
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, _ := ns.Run(context.Background(), "ss", "-ltn", "sport = :9120")
		if strings.Contains(out, ":9120") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the echo server does not listen: %s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func tryTo(from *testbed.Namespace, proto, addr string, port int) string {
	out, _ := from.Run(context.Background(), "python3", "-c", accessTry, proto, addr, strconv.Itoa(port), "2")
	return strings.TrimSpace(out)
}

// establishedCount counts the established TCP connections of a namespace whose local (sport) or remote
// (dport) port is port.
func establishedCount(ns *testbed.Namespace, side string, port int) int {
	out, _ := ns.Run(context.Background(), "ss", "-Htn", "state", "established", fmt.Sprintf("( %s = :%d )", side, port))
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// trackedTCP reports whether the gateway's table has a TCP connection with this original destination port.
func trackedTCP(gw *testbed.Namespace, src string, port int) bool {
	out, _ := gw.Run(context.Background(), "conntrack", "-L", "-p", "tcp", "--orig-src", src, "--orig-port-dst", strconv.Itoa(port))
	return strings.Contains(out, "dport="+strconv.Itoa(port))
}

// clientPort is the local port of the one established connection of ns towards port.
func clientPort(t *testing.T, ns *testbed.Namespace, port int) int {
	t.Helper()
	out, _ := ns.Run(context.Background(), "ss", "-Htn", "state", "established", fmt.Sprintf("( dport = :%d )", port))
	f := strings.Fields(out)
	if len(f) < 4 {
		t.Fatalf("no established connection towards %d: %q", port, out)
	}
	local := f[2]
	n, err := strconv.Atoi(local[strings.LastIndex(local, ":")+1:])
	if err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	return n
}

// streamOutcome says what happened to a held connection after mark (an offset in its output): reset,
// timeout, or continues (it kept answering for longer than the 3 s its client waits for an answer).
func streamOutcome(t *testing.T, p *testbed.Process, mark int) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	start := time.Now()
	for time.Now().Before(deadline) {
		out := p.Output()[mark:]
		switch {
		case strings.Contains(out, "ConnectionResetError"):
			return "reset"
		case strings.Contains(strings.ToLower(out), "timeout"):
			return "timeout"
		case strings.Contains(out, "Error"):
			return "error: " + out
		}
		if time.Since(start) > 5*time.Second && strings.Count(out, "ok") >= 5 {
			return "continues"
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "undecided: " + p.Output()[mark:]
}

// afterAnAnswer returns right after the connection of p has been answered once more. A connection
// that sends every second is idle for the rest of the second: nothing of it is in flight on the
// gateway, so a conntrack deletion in that second cannot catch an echo on its way back (such an echo
// reaches the gateway without an entry, is answered with a reset and ends the server's side by chance).
func afterAnAnswer(t *testing.T, p *testbed.Process) {
	t.Helper()
	have := strings.Count(p.Output(), "ok")
	deadline := time.Now().Add(30 * time.Second)
	for strings.Count(p.Output(), "ok") <= have {
		if time.Now().After(deadline) {
			t.Fatalf("the connection is not answered any more: %s", p.Output())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func lanRuleOverlay(t *testing.T, r *real, body string) engine.OverlayResult {
	t.Helper()
	return r.mustPut(model.Owner{Type: "user", Id: "test"}, body)
}

func (r *real) deleteOverlay(res engine.OverlayResult) {
	r.t.Helper()
	if _, err := r.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		r.t.Fatal(err)
	}
}

// deleteFlow deletes one tracked TCP connection by its original tuple, through the executor's closed
// operation (the one the cut uses).
func (r *real) deleteFlow(src string, sport int, dst string, dport int) {
	r.t.Helper()
	_, err := r.exec.Do(context.Background(), &executor.ConntrackDelete{
		Target: executor.Target{NS: r.top.GW.Name},
		Flows:  []executor.ConntrackFlow{{Proto: "tcp", Src: src, Dst: dst, SPort: sport, DPort: dport}},
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// Spike S3's behavior matrix (plan §2.4, M9 "Tests"), case by case, on the compiled rules of the engine.
// Each case holds one TCP connection of device A through the NAT to the server (a message every 100 ms),
// makes the change of the case, and looks at what the stream does, what a new connection does and what
// the server's side of the old one is. What the spike measured and what the product does:
//
//	C0 no change                      the stream continues, a new connection works
//	C1 drop every packet              not a product ruleset: the established accept is first (D11), see C2
//	C2 established accept, then drop  the stream continues, new connections hang        (the rule `drop`)
//	C3 reject with tcp reset          new connections are refused at once, the stream continues; with
//	                                  `cut_existing` it is reset too                    (the rule `reset`)
//	C4 C2 plus a conntrack deletion   the stream hangs (its next packet is new and meets the rule), the
//	                                  server's side stays established, new connections hang
//	C5 a conntrack deletion alone     the stream continues: the flow is re-created behind NAT
//	C6 a one-shot cut                 the stream gets a reset at once, the server's side stays half-open,
//	                                  and when the rule is gone a new connection works   (`cut_existing`)
func TestTheBehaviorMatrixOfS3OnTheRealKernel(t *testing.T) {
	r := startFaultLab(t, nil)
	top := r.top
	startMatrixServer(t, top.Server)
	for try := 0; tryTo(top.A, "udp", testbed.ServerAddr, 5353) != "ok"; try++ {
		if try > 300 {
			t.Fatal("the server does not answer")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// hold starts a connection of A towards the port and waits until it runs.
	hold := func(port int, pause ...string) *testbed.Process {
		t.Helper()
		p := top.A.Start("python3", append([]string{"-c", accessHold, testbed.ServerAddr, strconv.Itoa(port)}, pause...)...)
		waitText(t, p, "connected", 60*time.Second)
		deadline := time.Now().Add(60 * time.Second)
		for strings.Count(p.Output(), "ok") < 3 {
			if time.Now().After(deadline) {
				t.Fatalf("the connection does not run: %s", p.Output())
			}
			time.Sleep(100 * time.Millisecond)
		}
		return p
	}
	selector := func(port int, action string, cut bool) string {
		return fmt.Sprintf("target: {device: %s}\nrule: {protocol: tcp, ports: [%d], action: %s, cut_existing: %v}", flDevA, port, action, cut)
	}
	counterOf := func(res engine.OverlayResult) int64 {
		t.Helper()
		for _, rule := range r.e.Snapshot().Access.Rules {
			if rule.Key == "overlay:"+res.Overlay.Id.String() {
				return r.counters()[rule.Counter].Packets
			}
		}
		t.Fatalf("no rule for the overlay %s", res.Overlay.Id)
		return 0
	}

	t.Run("C0_no_change", func(t *testing.T) {
		p := hold(9101)
		defer p.Stop()
		mark := len(p.Output())
		if got := streamOutcome(t, p, mark); got != "continues" {
			t.Errorf("stream: %s", got)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9101); got != "ok" {
			t.Errorf("new connection: %s", got)
		}
	})

	// C1 and C2: a drop rule arrives while a connection runs. The ruleset the product compiles accepts
	// established traffic before the rules, which is C2; C1 (a rule that sees every packet) would hang the
	// stream, and the product never builds it: the jump to the rules stands behind the established accept.
	t.Run("C1_C2_a_drop_rule_changes_new_connections_only", func(t *testing.T) {
		p := hold(9102)
		defer p.Stop()
		mark := len(p.Output())
		res := lanRuleOverlay(t, r, selector(9102, "drop", false))
		if got := streamOutcome(t, p, mark); got != "continues" {
			t.Errorf("stream: %s (C2)", got)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9102); got != "timeout" {
			t.Errorf("new connection: %s (C2: hangs)", got)
		}
		if got := tryTo(top.B, "tcp", testbed.ServerAddr, 9102); got != "ok" {
			t.Errorf("B, which the rule does not name: %s", got)
		}
		if counterOf(res) == 0 {
			t.Error("the rule's counter counts nothing")
		}
		fwd := top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "forward")
		est := strings.Index(fwd, "ct state established,related accept")
		jump := strings.Index(fwd, "jump access_forward")
		if est < 0 || jump < 0 || est > jump {
			t.Errorf("the established accept must stand in front of the rules (C1 is not a product ruleset):\n%s", fwd)
		}
		r.deleteOverlay(res)
	})

	t.Run("C3_a_reset_rule_refuses_new_connections_and_cuts_when_asked", func(t *testing.T) {
		p := hold(9103)
		defer p.Stop()
		mark := len(p.Output())
		res := lanRuleOverlay(t, r, selector(9103, "reset", false))
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9103); got != "refused" {
			t.Errorf("new connection: %s (C3: refused at once)", got)
		}
		if got := streamOutcome(t, p, mark); got != "continues" {
			t.Errorf("stream without cut_existing: %s", got)
		}
		r.deleteOverlay(res)

		// the same rule, asked to cut: the stream gets its reset
		mark = len(p.Output())
		res = lanRuleOverlay(t, r, selector(9103, "reset", true))
		if got := streamOutcome(t, p, mark); got != "reset" {
			t.Errorf("stream with cut_existing: %s (C3: reset)", got)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9103); got != "refused" {
			t.Errorf("new connection: %s", got)
		}
		r.deleteOverlay(res)
	})

	t.Run("C4_a_drop_rule_and_a_conntrack_deletion_hang_the_stream", func(t *testing.T) {
		p := hold(9104, "1")
		defer p.Stop()
		sport := clientPort(t, top.A, 9104)
		if !trackedTCP(top.GW, testbed.ClientAAddr, 9104) {
			t.Fatal("the gateway does not track the connection")
		}
		res := lanRuleOverlay(t, r, selector(9104, "drop", false))
		afterAnAnswer(t, p)
		mark := len(p.Output())
		r.deleteFlow(testbed.ClientAAddr, sport, testbed.ServerAddr, 9104)
		if got := streamOutcome(t, p, mark); got != "timeout" {
			t.Errorf("stream: %s (C4: hangs)", got)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9104); got != "timeout" {
			t.Errorf("new connection: %s", got)
		}
		if trackedTCP(top.GW, testbed.ClientAAddr, 9104) {
			t.Error("the deleted connection is tracked again although the rule drops it")
		}
		time.Sleep(time.Second)
		if n := establishedCount(top.Server, "sport", 9104); n != 1 {
			t.Errorf("the server's side of the connection: %d established, want 1 (half-open)", n)
		}
		r.deleteOverlay(res)
	})

	t.Run("C5_a_conntrack_deletion_alone_does_not_cut_behind_NAT", func(t *testing.T) {
		p := hold(9105, "1")
		defer p.Stop()
		sport := clientPort(t, top.A, 9105)
		afterAnAnswer(t, p)
		mark := len(p.Output())
		r.deleteFlow(testbed.ClientAAddr, sport, testbed.ServerAddr, 9105)
		if got := streamOutcome(t, p, mark); got != "continues" {
			t.Errorf("stream: %s (C5: the flow is re-created)", got)
		}
		deadline := time.Now().Add(10 * time.Second)
		for !trackedTCP(top.GW, testbed.ClientAAddr, 9105) {
			if time.Now().After(deadline) {
				t.Fatal("the flow was not re-created")
			}
			time.Sleep(200 * time.Millisecond)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9105); got != "ok" {
			t.Errorf("new connection: %s", got)
		}
	})

	t.Run("C6_a_cut_resets_the_stream_and_leaves_the_server_half_open", func(t *testing.T) {
		p := hold(9106, "1")
		defer p.Stop()
		afterAnAnswer(t, p)
		mark := len(p.Output())
		res := lanRuleOverlay(t, r, selector(9106, "drop", true))
		// PutOverlay answers when the cut is done: the window has been open and is closed again
		if out := top.GW.Must("nft", "list", "chain", "inet", "chaosgw", compiler.CutForwardChain); strings.Contains(out, "reject") {
			t.Errorf("the window is still open after the write was answered:\n%s", out)
		}
		if got := streamOutcome(t, p, mark); got != "reset" {
			t.Errorf("stream: %s (C6: reset)", got)
		}
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9106); got != "timeout" {
			t.Errorf("new connection while the rule stands: %s", got)
		}
		if n := establishedCount(top.Server, "sport", 9106); n != 1 {
			t.Errorf("the server's side of the cut connection: %d established, want 1 (half-open)", n)
		}
		if trackedTCP(top.GW, testbed.ClientAAddr, 9106) {
			t.Error("the cut connection is still tracked")
		}
		// the rule goes (C6's new connection): the device reconnects
		r.deleteOverlay(res)
		if got := tryTo(top.A, "tcp", testbed.ServerAddr, 9106); got != "ok" {
			t.Errorf("new connection after the rule: %s", got)
		}
	})
}

// A cut takes what the rule's selector owns and nothing else: of two connections of the device only the
// one the rule names is reset, another device's connection to the same port goes on, and so does the
// conntrack entry of every one of them. A cutting rule for every protocol also deletes the tracked ping of
// its device (a window of TCP resets cannot reach it): the device's pings stop, the other device's go on.
func TestACutTakesOnlyWhatTheSelectorOwnsOnTheRealKernel(t *testing.T) {
	r := startFaultLab(t, nil)
	top := r.top
	startMatrixServer(t, top.Server)
	for try := 0; tryTo(top.A, "tcp", testbed.ServerAddr, 9101) != "ok"; try++ {
		if try > 300 {
			t.Fatal("the server does not answer")
		}
		time.Sleep(200 * time.Millisecond)
	}
	start := func(ns *testbed.Namespace, port int) *testbed.Process {
		p := ns.Start("python3", "-c", accessHold, testbed.ServerAddr, strconv.Itoa(port))
		waitText(t, p, "ok", 60*time.Second)
		return p
	}
	aNamed, aOther, bNamed := start(top.A, 9107), start(top.A, 9108), start(top.B, 9107)
	pingA := top.A.Start("ping", "-n", "-i", "0.2", testbed.ServerAddr)
	pingB := top.B.Start("ping", "-n", "-i", "0.2", testbed.ServerAddr)
	replies := func(p *testbed.Process) int { return strings.Count(p.Output(), "bytes from") }
	deadline := time.Now().Add(60 * time.Second)
	for replies(pingA) < 3 || replies(pingB) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the pings do not run: %s %s", pingA.Output(), pingB.Output())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// TCP: the device's connection to 9107 and nothing else
	lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {protocol: tcp, ports: [9107], action: drop, cut_existing: true}", flDevA))
	waitText(t, aNamed, "ConnectionResetError", 30*time.Second)
	for name, p := range map[string]*testbed.Process{"the device's other port": aOther, "another device, same port": bNamed} {
		before := strings.Count(p.Output(), "ok")
		time.Sleep(2 * time.Second)
		if strings.Contains(p.Output(), "Error") || strings.Count(p.Output(), "ok") <= before {
			t.Errorf("%s was disturbed by the cut: %s", name, p.Output())
		}
	}
	if !trackedTCP(top.GW, testbed.ClientAAddr, 9108) || !trackedTCP(top.GW, testbed.ClientBAddr, 9107) {
		t.Error("the cut deleted tracked connections that it does not own")
	}

	// ICMP: with the protocol "any" the rule also cuts what a window of TCP resets cannot reach; the
	// tracked ping flow of A is deleted, its next echo is new and meets the rule (validation refuses
	// cut_existing for icmp alone: cut_existing_requires_tcp)
	before := replies(pingA)
	lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {destination: {cidr: %s/32}, action: drop, cut_existing: true}", flDevA, testbed.ServerAddr))
	afterCut := replies(pingA)
	time.Sleep(3 * time.Second)
	if got := replies(pingA); got > afterCut+1 || afterCut < before {
		t.Errorf("A's ping goes on after the cut: %d replies at the cut, %d three seconds later", afterCut, got)
	}
	waitText(t, aOther, "ConnectionResetError", 30*time.Second) // the same rule resets the device's other connection
	b0 := replies(pingB)
	time.Sleep(2 * time.Second)
	if replies(pingB) <= b0 {
		t.Errorf("B's ping stopped with A's rule: %s", pingB.Output())
	}
	if strings.Contains(bNamed.Output(), "Error") {
		t.Errorf("B's connection was disturbed by A's rule: %s", bNamed.Output())
	}
	for _, p := range []*testbed.Process{aOther, bNamed, pingA, pingB} {
		p.Stop()
	}
}

// yamlRule decodes a configured rule.
func yamlRule(t *testing.T, body string) model.AccessRule {
	t.Helper()
	doc, err := domain.ParseDocument([]byte(body), domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var rule model.AccessRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return rule
}

// Configured rules in their order, overlay rules in front of them (newest first), an allow rule as an
// exception to the access matrix, and `explain` against the kernel: for every probe the verdict that
// `explain` gives is the one the packets get, and the rule it names is the one whose counter moved.
func TestRuleOrderOverlaysAndExplainAgreeWithTheKernel(t *testing.T) {
	ruleAllowLab := uuid.MustParse("a1000000-0000-4000-8000-000000000011").String()
	ruleDropUp := uuid.MustParse("a1000000-0000-4000-8000-000000000012").String()
	ruleRejectUDP := uuid.MustParse("a1000000-0000-4000-8000-000000000013").String()
	r := startFaultLab(t, func(c *model.Configuration) {
		withConfigRules(map[string]model.AccessRule{
			// an exception to the matrix: IoT may open TCP 9101 on a host in Lab
			ruleAllowLab: yamlRule(t, `{name: iot-to-lab, source: {network: IoT}, destination: {network: Lab}, protocol: tcp, ports: [9101], action: allow}`),
			// the uplink is open by default; this one closes TCP 9102 for IoT
			ruleDropUp: yamlRule(t, `{name: no-9102, source: {network: IoT}, destination: {uplink: true}, protocol: tcp, ports: [9102], action: drop}`),
			// and this one is behind the drop rule for the same traffic and in front of nothing else
			ruleRejectUDP: yamlRule(t, `{name: no-echo, source: {network: IoT}, protocol: udp, ports: [5353], action: reject}`),
		}, ruleAllowLab, ruleDropUp, ruleRejectUDP)(c)
	})
	top := r.top
	startMatrixServer(t, top.Server)
	startMatrixServer(t, top.C)
	startMatrixServer(t, top.A)

	type probe struct {
		name  string
		from  *testbed.Namespace
		src   string
		dst   string
		proto string
		port  int
	}
	probes := []probe{
		{"A to the server, tcp 9101", top.A, testbed.ClientAAddr, testbed.ServerAddr, "tcp", 9101},
		{"A to the server, tcp 9102", top.A, testbed.ClientAAddr, testbed.ServerAddr, "tcp", 9102},
		{"A to the server, tcp 9103", top.A, testbed.ClientAAddr, testbed.ServerAddr, "tcp", 9103},
		{"A to the server, udp 5353", top.A, testbed.ClientAAddr, testbed.ServerAddr, "udp", 5353},
		{"B to the server, tcp 9102", top.B, testbed.ClientBAddr, testbed.ServerAddr, "tcp", 9102},
		{"B to the server, udp 5353", top.B, testbed.ClientBAddr, testbed.ServerAddr, "udp", 5353},
		{"C to the server, tcp 9102", top.C, testbed.ClientCAddr, testbed.ServerAddr, "tcp", 9102},
		{"C to the server, udp 5353", top.C, testbed.ClientCAddr, testbed.ServerAddr, "udp", 5353},
		{"A to Lab, tcp 9101 (allow rule)", top.A, testbed.ClientAAddr, testbed.ClientCAddr, "tcp", 9101},
		{"A to Lab, tcp 9102 (matrix)", top.A, testbed.ClientAAddr, testbed.ClientCAddr, "tcp", 9102},
		{"C to IoT, tcp 9101 (matrix)", top.C, testbed.ClientCAddr, testbed.ClientAAddr, "tcp", 9101},
	}
	kernelVerdict := map[string]string{"ok": "allow", "timeout": "drop", "refused": "reject", "reset": "reject"}
	check := func(stage string, want map[string]struct{ verdict, layer, rule string }) {
		t.Helper()
		for _, p := range probes {
			ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(p.src), Dst: p.dst, Protocol: p.proto, Port: p.port})
			if err != nil {
				t.Fatal(err)
			}
			got := kernelVerdict[tryTo(p.from, p.proto, p.dst, p.port)]
			if got != ex.Access.Verdict {
				t.Errorf("%s, %s: the kernel gave %q, explain says %+v", stage, p.name, got, ex.Access)
			}
			if w, ok := want[p.name]; ok && (ex.Access.Verdict != w.verdict || ex.Access.Layer != w.layer || ex.Access.Rule != w.rule) {
				t.Errorf("%s, %s: explain = %+v, want %+v", stage, p.name, ex.Access, w)
			}
		}
	}

	type w = struct{ verdict, layer, rule string }
	// the configured rules alone: the first match decides, the matrix decides the rest
	check("configured rules", map[string]w{
		"A to the server, tcp 9101":       {"allow", "access_matrix", ""},
		"A to the server, tcp 9102":       {"drop", "config_rule", ruleDropUp},
		"B to the server, tcp 9102":       {"drop", "config_rule", ruleDropUp},
		"C to the server, tcp 9102":       {"allow", "access_matrix", ""},
		"A to the server, udp 5353":       {"reject", "config_rule", ruleRejectUDP},
		"C to the server, udp 5353":       {"allow", "access_matrix", ""},
		"A to Lab, tcp 9101 (allow rule)": {"allow", "config_rule", ruleAllowLab},
		"A to Lab, tcp 9102 (matrix)":     {"drop", "access_matrix", ""},
		"C to IoT, tcp 9101 (matrix)":     {"drop", "access_matrix", ""},
	})

	// an overlay stands in front of the configuration: B may use 9102 again (allow), A's UDP is dropped
	// instead of rejected, and the newer of two overlays on the same traffic (the network-wide reject) wins
	allowB := lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {protocol: tcp, ports: [9102], action: allow}", flDevB))
	dropUDP := lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {protocol: udp, ports: [5353], action: drop}", flDevA))
	check("overlays", map[string]w{
		"A to the server, tcp 9102": {"drop", "config_rule", ruleDropUp},
		"B to the server, tcp 9102": {"allow", "overlay_rule", allowB.Overlay.Id.String()},
		"A to the server, udp 5353": {"drop", "overlay_rule", dropUDP.Overlay.Id.String()},
		"B to the server, udp 5353": {"reject", "config_rule", ruleRejectUDP},
	})
	newer := lanRuleOverlay(t, r, "target: {network: IoT}\nrule: {protocol: udp, ports: [5353], action: reject}")
	check("a newer overlay", map[string]w{
		"A to the server, udp 5353": {"reject", "overlay_rule", newer.Overlay.Id.String()},
		"B to the server, udp 5353": {"reject", "overlay_rule", newer.Overlay.Id.String()},
	})

	// the counters: the rule that decided is the rule that counted
	counters := r.counters()
	for _, rule := range r.e.Snapshot().Access.Rules {
		if counters[rule.Counter].Packets == 0 {
			t.Errorf("rule %s decided traffic of the probes and counted nothing", rule.Key)
		}
	}
	// all overlays go: the configured rules decide alone again
	r.deleteOverlay(allowB)
	r.deleteOverlay(dropUDP)
	r.deleteOverlay(newer)
	check("configured rules again", map[string]w{
		"B to the server, tcp 9102": {"drop", "config_rule", ruleDropUp},
		"A to the server, udp 5353": {"reject", "config_rule", ruleRejectUDP},
	})
}

// The anti-lockout rule on the engine's own ruleset: a device that covers the whole management network
// gets drop, reset and reject rules (a rule and overlays, with "also cut existing connections"), and the
// management host still reaches SSH and the UI, keeps its established connection, and is not counted by any
// rule. The same rules do refuse the host everything else the gateway offers, so they are in force.
func TestNoRuleOrOverlayLocksTheManagementNetworkOut(t *testing.T) {
	const devMgmt = "a1111111-0000-4000-8000-0000000000f1"
	r := startFaultLab(t, func(c *model.Configuration) {
		devs := *c.Devices
		devs[devMgmt] = model.Device{Name: "mgmt-net", Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{"192.168.56.0/24"}}}
		c.Devices = &devs
		withConfigRules(map[string]model.AccessRule{
			"a1000000-0000-4000-8000-000000000021": yamlRule(t, `{name: drop-mgmt, source: {device: mgmt-net}, action: drop, cut_existing: true}`),
		}, "a1000000-0000-4000-8000-000000000021")(c)
	})
	top := r.top
	// the gateway's own services: the control plane and something that is not
	top.GW.Start("python3", "-c", `
import socket, threading
def tcp(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(50)
    while True:
        c, _ = s.accept()
        def h(c):
            try:
                while True:
                    d = c.recv(100)
                    if not d: break
                    c.sendall(d)
            except Exception: pass
        threading.Thread(target=h, args=(c,), daemon=True).start()
for p in (22, 443, 8000):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
import time
time.sleep(10**6)
`)
	deadline := time.Now().Add(90 * time.Second)
	for _, port := range []string{"22", "443", "8000"} {
		for {
			out, _ := top.GW.Run(context.Background(), "ss", "-ltn", "sport = :"+port)
			if strings.Contains(out, ":"+port) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the gateway's service on %s does not listen", port)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	mgmt := func(port int) string { return tryTo(top.Mgmt, "tcp", testbed.MgmtGateway, port) }
	// the configured drop rule is in force for everything but the control plane
	if got := mgmt(8000); got != "timeout" {
		t.Fatalf("the rule that selects the management network does not drop its traffic: %s", got)
	}
	ssh := top.Mgmt.Start("python3", "-c", accessHold, testbed.MgmtGateway, "22")
	waitText(t, ssh, "ok", 60*time.Second)

	// overlays on top: reject and reset the control plane's ports, with a cut
	lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {action: reject}", devMgmt))
	lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {protocol: tcp, ports: [22, 443], action: reset, cut_existing: true}", devMgmt))
	lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {action: drop, cut_existing: true}", devMgmt))

	if got := mgmt(8000); got == "ok" {
		t.Errorf("a port that is not the control plane: %s, the rules must refuse it", got)
	}
	for _, port := range []int{22, 443} {
		if got := mgmt(port); got != "ok" {
			t.Errorf("management host to the gateway's port %d: %s, want ok", port, got)
		}
	}
	before := strings.Count(ssh.Output(), "ok")
	time.Sleep(2 * time.Second)
	if strings.Contains(ssh.Output(), "Error") || strings.Count(ssh.Output(), "ok") <= before {
		t.Errorf("the management host's established connection was cut: %s", ssh.Output())
	}
	counters := r.counters()
	if counters[compiler.AntiLockoutCounter].Packets == 0 {
		t.Errorf("the anti-lockout rule counted nothing: %+v", counters[compiler.AntiLockoutCounter])
	}
	ssh.Stop()
}
