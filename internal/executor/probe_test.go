package executor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/probe"
)

// M25: the executor makes probes (a namespace, a veth pair that is a port of a bridge, a DHCP client) and runs measurements in
// their namespaces. The tests drive it with a recording runner, a scripted DHCP server and the loopback as the probe's stack;
// the same on the real kernel is in internal/engine/integration_probes_test.go.

const (
	pNS  = "cgp-3a1f7c52"
	pIf  = "pv3a1f7c52"
	pMAC = "02:28:b4:ce:f6:08"
)

const (
	pEnsure = `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"02:28:b4:ce:f6:08","hostname":"probe-a"}`
	pDelete = `{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52"}`
)

func TestProbeOperationsAreDecodedStrictly(t *testing.T) {
	for _, in := range []string{
		pEnsure, pDelete,
		`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"203.0.113.10","port":80},"servers":["10.10.0.1:53"],"proxy":"10.10.0.1:53","user_agent":"x"}`,
		`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"count":2000,"interval_us":10000,"settle_ms":3000}`,
		`{"type":"probe_run","kind":"echo","namespace":"gw","host":"203.0.113.10","port":7,"count":100,"interval_us":10000}`,
		`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"rate","host":"203.0.113.10","port":7,"duration_ms":10000}`,
		`{"type":"read","what":"probes","namespace":"gw"}`,
	} {
		op, err := Decode([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		enc, _ := Encode(op)
		if again, err := Decode(enc); err != nil {
			t.Errorf("re-decode: %v", err)
		} else if enc2, _ := Encode(again); string(enc) != string(enc2) {
			t.Errorf("the encoding is not stable: %s %s", enc, enc2)
		}
	}
	for name, in := range map[string]string{
		"a namespace that is no probe's": `{"type":"probe_ns","action":"delete","name":"cgsvc","host_if":"pv3a1f7c52"}`,
		"a pair of another probe":        `{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"pv00000000"}`,
		"an upper case id":               `{"type":"probe_ns","action":"delete","name":"cgp-3A1F7C52","host_if":"pv3A1F7C52"}`,
		"a pair that is no pv":           `{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"wan0"}`,
		"an unknown action":              `{"type":"probe_ns","action":"restart","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52"}`,
		"no bridge":                      `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","mac":"02:28:b4:ce:f6:08"}`,
		"a bridge that is a path":        `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"../x","mac":"02:28:b4:ce:f6:08"}`,
		"a multicast MAC":                `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"03:28:b4:ce:f6:08"}`,
		"a global MAC":                   `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"00:28:b4:ce:f6:08"}`,
		"a MAC in upper case":            `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"02:28:B4:CE:F6:08"}`,
		"a host name that is no label":   `{"type":"probe_ns","action":"ensure","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot","mac":"02:28:b4:ce:f6:08","hostname":"a b"}`,
		"a delete with a bridge":         `{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","bridge":"br-iot"}`,
		"an unknown field":               `{"type":"probe_ns","action":"delete","name":"cgp-3a1f7c52","host_if":"pv3a1f7c52","command":"sh"}`,
		"a run in the executor's own namespace for a diagnostic": `{"type":"probe_run","kind":"diagnostic","request":{"type":"tcp","host":"203.0.113.10","port":80}}`,
		"a run in a namespace that is no probe's":                `{"type":"probe_run","probe":"cgsvc","kind":"echo","host":"203.0.113.10","port":7,"count":1,"interval_us":1000}`,
		"a diagnostic without its request":                       `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic"}`,
		"a diagnostic that cannot run":                           `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"203.0.113.10"}}`,
		"a server that is no address":                            `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"ping","host":"203.0.113.10"},"servers":["10.10.0.1"]}`,
		"an echo host that is a name":                            `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"broker.example.com","port":7,"count":1,"interval_us":1000}`,
		"an echo without a count":                                `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"interval_us":1000}`,
		"too many datagrams":                                     `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"count":20001,"interval_us":1000}`,
		"a run that takes hours":                                 `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"count":20000,"interval_us":10000000}`,
		"a duration on an echo":                                  `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"count":1,"interval_us":1000,"duration_ms":5}`,
		"a rate without a duration":                              `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"rate","host":"203.0.113.10","port":7}`,
		"a rate with datagrams":                                  `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"rate","host":"203.0.113.10","port":7,"duration_ms":1000,"count":5}`,
		"diagnostic fields on an echo":                           `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"203.0.113.10","port":7,"count":1,"interval_us":1000,"proxy":"10.0.0.1:53"}`,
		"an unknown kind":                                        `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"shell","host":"203.0.113.10","port":7}`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// linkJSON is `ip -j -d -s -s link show` with the gateway end of a probe's pair as a port of br-iot.
func linkJSON(master string, up bool) string {
	flags := `"BROADCAST","MULTICAST"`
	if up {
		flags += `,"UP","LOWER_UP"`
	}
	m := ""
	if master != "" {
		m = `,"master":"` + master + `"`
	}
	return fmt.Sprintf(`[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP"]},{"ifindex":7,"ifname":%q,"flags":[%s]%s,"linkinfo":{"info_kind":"veth"}},{"ifindex":8,"ifname":"pvzzzzzzzz","flags":[],"linkinfo":{"info_kind":"veth"}},{"ifindex":9,"ifname":"pv00000000","flags":[],"linkinfo":{"info_kind":"dummy"}}]`, pIf, flags, m)
}

const addrJSON = `[{"ifindex":3,"ifname":"eth0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"address":"02:28:B4:CE:F6:08","addr_info":[{"family":"inet","local":"10.10.0.77","prefixlen":24},{"family":"inet6","local":"fe80::1","prefixlen":64}]}]`

// isLinkRead is the read of the links in the executor's own namespace.
func isLinkRead(c Command) bool {
	return c.Tool == ToolIP && c.NS == "" && len(c.Args) >= 6 && c.Args[0] == "-j" && c.Args[4] == "link" && c.Args[5] == "show"
}

// probeRunner answers the reads of a probe that is made.
func probeRunner() *fakeRunner {
	return &fakeRunner{respond: func(c Command) (Result, error) {
		switch {
		case isLinkRead(c):
			return Result{Stdout: linkJSON("br-iot", true)}, nil
		case c.Tool == ToolIP && c.NS == pNS && len(c.Args) >= 3 && c.Args[0] == "-j" && c.Args[1] == "addr":
			return Result{Stdout: addrJSON}, nil
		case c.Tool == ToolIP && c.NS == "" && len(c.Args) >= 3 && c.Args[0] == "link" && c.Args[1] == "show":
			return Result{Exit: 1}, nil // nothing exists yet
		case c.Tool == ToolIP && c.NS == pNS && len(c.Args) >= 3 && c.Args[0] == "link" && c.Args[1] == "show":
			return Result{Exit: 1}, nil
		}
		return Result{}, nil
	}}
}

// idleSock is a DHCP socket nobody answers.
type idleSock struct{ done chan struct{} }

func (s *idleSock) Send([]byte, netip.AddrPort) error { return nil }
func (s *idleSock) Recv() ([]byte, error)             { <-s.done; return nil, net.ErrClosed }
func (s *idleSock) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func idleOpen(string, string) (probe.Socket, error) { return &idleSock{done: make(chan struct{})}, nil }

func argvs(fr *fakeRunner) []string {
	var out []string
	for _, c := range fr.commands() {
		out = append(out, strings.TrimSpace(c.NS+" "+string(c.Tool)+" "+strings.Join(c.Args, " ")))
	}
	return out
}

func hasCmd(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

func TestAProbeIsMadeFromFixedCommandsAndItsBridgeMustBeAssigned(t *testing.T) {
	fr := probeRunner()
	e := newExec(t, fr, WithProbeSocket(idleOpen))
	// the bridge is not assigned: refused before anything runs
	if _, err := e.DoBatch(context.Background(), ops(t, pEnsure)); err == nil || !strings.Contains(err.Error(), "not assigned") {
		t.Fatalf("%v", err)
	}
	if len(fr.commands()) != 0 {
		t.Fatalf("commands ran: %v", argvs(fr))
	}
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot","wan0"],"os_owned":["wan0"]}`, pEnsure)); err != nil {
		t.Fatal(err)
	}
	cmds := argvs(fr)
	for _, want := range []string{
		"ip netns add " + pNS,
		"ip link add " + pIf + " type veth peer name eth0 address " + pMAC + " netns " + pNS,
		"ip link set dev " + pIf + " master br-iot",
		"ethtool -K " + pIf + " gro off gso off tso off lro off",
		pNS + " ethtool -K eth0 gro off gso off tso off lro off",
		pNS + " sysctl -w net/ipv4/conf/all/rp_filter=0", // the DHCP client's answers come from an address it has no route to
		pNS + " sysctl -w net/ipv4/conf/eth0/rp_filter=0",
		"ip link set dev " + pIf + " up",
		pNS + " ip link set dev lo up",
		pNS + " ip link set dev eth0 up",
	} {
		if !hasCmd(cmds, want) {
			t.Errorf("no %q in\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	// the OS-owned bridge is no bridge for a probe
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot","wan0"],"os_owned":["br-iot"]}`, pEnsure)); err == nil || !strings.Contains(err.Error(), "OS-owned") {
		t.Errorf("%v", err)
	}
}

func TestTheReadOfProbesListsTheGatewayEndsWithTheirNamespaces(t *testing.T) {
	fr := probeRunner()
	e := newExec(t, fr, WithProbeSocket(idleOpen))
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure)); err != nil {
		t.Fatal(err)
	}
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"probes"}`))
	if err != nil {
		t.Fatal(err)
	}
	var st []ProbeNSState
	if err := json.Unmarshal(out.Data[0], &st); err != nil {
		t.Fatal(err)
	}
	// the dummy and the veth that is no probe's are left out
	if len(st) != 1 {
		t.Fatalf("%+v", st)
	}
	p := st[0]
	if p.Name != pNS || p.HostIf != pIf || p.Master != "br-iot" || !p.HostUp || !p.PeerUp || p.MAC != pMAC || !p.DHCP || len(p.Addrs) != 1 || p.Addrs[0] != "10.10.0.77/24" {
		t.Errorf("%+v", p)
	}
}

// The list of namespaces is the host's: it names the namespace of a pair that belongs to another gateway on the same host too
// (the tests of the testbed run several with the same probe ids, and one of them removed the namespace of another, CI run
// 40741696470). Only a namespace in which the interface is certainly missing is listed as a probe without its pair.
func TestANamespaceOfAnotherGatewaysPairIsNotListedAsOneWithoutItsPair(t *testing.T) {
	const other = "cgp-9d2e4b61"
	for _, tc := range []struct {
		name   string
		result Result
		listed bool
	}{
		{"the interface is there", Result{Stdout: addrJSON}, false},
		{"the interface is missing", Result{Exit: 1, Stderr: `Device "eth0" does not exist.`}, true},
		{"the command fails otherwise", Result{Exit: 1, Stderr: "setting the network namespace failed"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{respond: func(c Command) (Result, error) {
				switch {
				case isLinkRead(c):
					return Result{Stdout: linkJSON("br-iot", true)}, nil
				case c.Tool == ToolIP && c.NS == "" && len(c.Args) == 2 && c.Args[0] == "netns" && c.Args[1] == "list":
					return Result{Stdout: other + " (id: 3)\n"}, nil
				case c.Tool == ToolIP && c.NS == other && len(c.Args) >= 4 && c.Args[1] == "link" && c.Args[2] == "show":
					return tc.result, nil
				}
				return Result{}, nil
			}}
			e := newExec(t, fr, WithProbeSocket(idleOpen))
			out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"probes"}`))
			if err != nil {
				t.Fatal(err)
			}
			var st []ProbeNSState
			if err := json.Unmarshal(out.Data[0], &st); err != nil {
				t.Fatal(err)
			}
			listed := false
			for _, s := range st {
				listed = listed || (s.Name == other && s.NoPair)
			}
			if listed != tc.listed {
				t.Errorf("listed: %v, want %v: %+v", listed, tc.listed, st)
			}
		})
	}
}

// The question "is the namespace of the probe there?" is asked by a command inside it. A command that fails for a moment
// must not be read as "no": the pair would be deleted and made again, and the probe would lose its address (CI run
// 40721700824). Only the message of a namespace that cannot be opened says it is gone.
func TestAProbesPairIsDeletedOnlyWhenItsNamespaceIsGone(t *testing.T) {
	const transient = "setting the network namespace failed: Invalid argument"
	const gone = `Cannot open network namespace "cgp-3a1f7c52": No such file or directory`
	for _, tc := range []struct {
		name    string
		failing int // how many asks fail, 0: all of them
		stderr  string
		deleted bool
		err     bool
	}{
		{"a failure for a moment", 1, transient, false, false},
		{"a failure that stays", 0, transient, false, true},
		{"a namespace that is gone", 0, gone, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asks int
			fr := &fakeRunner{respond: func(c Command) (Result, error) {
				switch {
				case isLinkRead(c):
					return Result{Stdout: linkJSON("br-iot", true)}, nil
				case c.Tool == ToolIP && c.NS == pNS && len(c.Args) >= 3 && c.Args[0] == "-j" && c.Args[1] == "addr":
					return Result{Stdout: addrJSON}, nil
				case c.Tool == ToolIP && c.NS == pNS && len(c.Args) >= 3 && c.Args[0] == "link" && c.Args[1] == "show":
					asks++
					if tc.failing == 0 || asks <= tc.failing {
						return Result{Exit: 1, Stderr: tc.stderr}, nil
					}
					return Result{}, nil
				}
				return Result{}, nil // the pair exists, everything else works
			}}
			e := newExec(t, fr, WithProbeSocket(idleOpen))
			_, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure))
			if (err != nil) != tc.err {
				t.Fatalf("error %v, want error: %v", err, tc.err)
			}
			if got := hasCmd(argvs(fr), "ip link delete dev "+pIf+" type veth"); got != tc.deleted {
				t.Errorf("pair deleted: %v, want %v\n%s", got, tc.deleted, strings.Join(argvs(fr), "\n"))
			}
		})
	}
}

// A read of the probe's interface that fails for a moment (an `ip netns exec` that raced with another) does not make the probe
// look like one without an address: it is repeated, and only a read that keeps failing leaves the probe without one.
func TestTheReadOfAProbesInterfaceIsRepeatedWhenItFailsForAMoment(t *testing.T) {
	for _, tc := range []struct {
		failures int
		addrs    int
	}{{0, 1}, {2, 1}, {3, 0}} {
		var calls int
		armed := false
		fr := &fakeRunner{respond: func(c Command) (Result, error) {
			switch {
			case isLinkRead(c):
				return Result{Stdout: linkJSON("br-iot", true)}, nil
			case c.Tool == ToolIP && c.NS == pNS && len(c.Args) >= 3 && c.Args[0] == "-j" && c.Args[1] == "addr":
				if !armed {
					return Result{Stdout: addrJSON}, nil
				}
				calls++
				if calls <= tc.failures {
					return Result{Exit: 1, Stderr: "setting the network namespace failed"}, nil
				}
				return Result{Stdout: addrJSON}, nil
			case c.Tool == ToolIP && c.NS == "" && len(c.Args) >= 3 && c.Args[0] == "link" && c.Args[1] == "show":
				return Result{Exit: 1}, nil
			}
			return Result{}, nil
		}}
		e := newExec(t, fr, WithProbeSocket(idleOpen))
		if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure)); err != nil {
			t.Fatal(err)
		}
		armed = true // only the read fails
		out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"probes"}`))
		if err != nil {
			t.Fatal(err)
		}
		var st []ProbeNSState
		if err := json.Unmarshal(out.Data[0], &st); err != nil {
			t.Fatal(err)
		}
		if len(st) != 1 || len(st[0].Addrs) != tc.addrs || (tc.addrs == 1 && !st[0].PeerUp) {
			t.Errorf("%d failures: %+v", tc.failures, st)
		}
	}
}

func TestDeletingAProbeStopsItsClientAndRemovesThePairAndTheNamespace(t *testing.T) {
	fr := probeRunner()
	closed := make(chan struct{})
	var once sync.Once
	e := newExec(t, fr, WithProbeSocket(func(string, string) (probe.Socket, error) {
		return &watchedSock{idleSock: idleSock{done: make(chan struct{})}, onClose: func() { once.Do(func() { close(closed) }) }}, nil
	}))
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure)); err != nil {
		t.Fatal(err)
	}
	// the pair is a veth in the listing, so the executor deletes it
	fr.respond = func(c Command) (Result, error) {
		if isLinkRead(c) {
			return Result{Stdout: linkJSON("br-iot", true)}, nil
		}
		return Result{}, nil
	}
	if _, err := e.Do(context.Background(), mustDecode(t, pDelete)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the client of the probe still runs")
	}
	cmds := argvs(fr)
	if !hasCmd(cmds, "ip link delete dev "+pIf+" type veth") || !hasCmd(cmds, "ip netns delete "+pNS) {
		t.Errorf("%v", cmds)
	}
	// a pair that is no veth is not deleted
	fr2 := &fakeRunner{respond: func(c Command) (Result, error) {
		if isLinkRead(c) {
			return Result{Stdout: fmt.Sprintf(`[{"ifindex":7,"ifname":%q,"flags":[],"linkinfo":{"info_kind":"dummy"}}]`, pIf)}, nil
		}
		return Result{}, nil
	}}
	e2 := newExec(t, fr2)
	if _, err := e2.Do(context.Background(), mustDecode(t, pDelete)); err == nil || !strings.Contains(err.Error(), "not a veth") {
		t.Errorf("%v", err)
	}
}

type watchedSock struct {
	idleSock
	onClose func()
}

func (s *watchedSock) Close() error { s.onClose(); return s.idleSock.Close() }

// dhcpServer answers a probe's DHCP client: an offer for a discover, an ack for a request.
type dhcpServer struct {
	rx   chan []byte
	done chan struct{}
	ip   string
	dns  []string
	sent chan [2]string
}

func newDHCPServer(ip string, dns ...string) *dhcpServer {
	return &dhcpServer{rx: make(chan []byte, 8), done: make(chan struct{}), ip: ip, dns: dns, sent: make(chan [2]string, 8)}
}

func (s *dhcpServer) Send(p []byte, dst netip.AddrPort) error {
	typ := byte(0)
	for o := 240; o+2 < len(p); {
		if p[o] == 255 {
			break
		}
		if p[o] == 53 {
			typ = p[o+2]
		}
		o += 2 + int(p[o+1])
	}
	reply := byte(2) // offer
	if typ == 3 {
		reply = 5 // ack
	}
	b := make([]byte, 240)
	b[0], b[1], b[2] = 2, 1, 6
	copy(b[4:8], p[4:8])
	a := netip.MustParseAddr(s.ip).As4()
	copy(b[16:20], a[:])
	copy(b[28:44], p[28:44])
	binary.BigEndian.PutUint32(b[236:240], 0x63825363)
	b = append(b, 53, 1, reply, 54, 4, 10, 10, 0, 1, 1, 4, 255, 255, 255, 0, 3, 4, 10, 10, 0, 1, 51, 4, 0, 0, 14, 16)
	if len(s.dns) > 0 {
		b = append(b, 6, byte(4*len(s.dns)))
		for _, d := range s.dns {
			x := netip.MustParseAddr(d).As4()
			b = append(b, x[:]...)
		}
	}
	b = append(b, 255)
	select {
	case s.sent <- [2]string{map[byte]string{2: "offer", 5: "ack"}[reply], dst.String()}:
	default:
	}
	s.rx <- b
	return nil
}

func (s *dhcpServer) Recv() ([]byte, error) {
	select {
	case b := <-s.rx:
		return b, nil
	case <-s.done:
		return nil, net.ErrClosed
	}
}

func (s *dhcpServer) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func TestTheLeaseOfTheClientConfiguresTheProbesInterface(t *testing.T) {
	fr := probeRunner()
	srv := newDHCPServer("10.10.0.77", "10.10.0.1")
	e := newExec(t, fr, WithProbeSocket(func(ns, ifname string) (probe.Socket, error) {
		if ns != pNS || ifname != "eth0" {
			t.Errorf("the client opens %s in %s", ifname, ns)
		}
		return srv, nil
	}))
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !hasCmd(argvs(fr), pNS+" ip route replace default via 10.10.0.1 dev eth0") {
		if time.Now().After(deadline) {
			t.Fatalf("the interface was not configured:\n%s", strings.Join(argvs(fr), "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !hasCmd(argvs(fr), pNS+" ip addr replace 10.10.0.77/24 dev eth0") {
		t.Errorf("no address:\n%s", strings.Join(argvs(fr), "\n"))
	}
	// the lease is in the read
	out, _ := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"probes"}`))
	var st []ProbeNSState
	_ = json.Unmarshal(out.Data[0], &st)
	if len(st) != 1 || st[0].Lease == nil || st[0].Lease.Addr.String() != "10.10.0.77/24" || st[0].Lease.Duration != 3600*time.Second {
		t.Errorf("%+v", st)
	}
	// a second ensure leaves the client as it is
	if _, err := e.Do(context.Background(), mustDecode(t, pEnsure)); err != nil {
		t.Fatal(err)
	}
	e.probes.mu.Lock()
	n := len(e.probes.m)
	e.probes.mu.Unlock()
	if n != 1 {
		t.Errorf("%d clients", n)
	}
}

func TestAProbeRunMeasuresFromTheProbesStack(t *testing.T) {
	var asked []string
	e := newExec(t, probeRunner(), WithProbeNet(func(ns string) diag.Net { asked = append(asked, ns); return diag.HostNet{} }))
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = c.Write([]byte("x"))
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	pc, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skipf("the UDP port of the TCP server is taken: %v", err)
	}
	defer func() { _ = pc.Close() }()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	run := func(in string) json.RawMessage {
		t.Helper()
		out, err := e.Do(context.Background(), mustDecode(t, in))
		if err != nil {
			t.Fatal(err)
		}
		return out.Data[0]
	}

	var d struct {
		From string `json:"from"`
		Ok   bool   `json:"ok"`
	}
	if err := json.Unmarshal(run(fmt.Sprintf(`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"127.0.0.1","port":%d}}`, port)), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Ok || d.From != "probe:cgp-3a1f7c52" {
		t.Errorf("%+v", d)
	}
	var echo probe.EchoResult
	if err := json.Unmarshal(run(fmt.Sprintf(`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"echo","host":"127.0.0.1","port":%d,"count":50,"interval_us":1000,"settle_ms":500}`, port)), &echo); err != nil {
		t.Fatal(err)
	}
	if echo.Sent != 50 || echo.Received != 50 {
		t.Errorf("%+v", echo)
	}
	// from the gateway: the namespace of the operation (here its own)
	if err := json.Unmarshal(run(fmt.Sprintf(`{"type":"probe_run","namespace":"gw","kind":"echo","host":"127.0.0.1","port":%d,"count":5,"interval_us":1000,"settle_ms":500}`, port)), &echo); err != nil {
		t.Fatal(err)
	}
	var rate probe.RateResult
	if err := json.Unmarshal(run(fmt.Sprintf(`{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"rate","host":"127.0.0.1","port":%d,"duration_ms":600}`, port)), &rate); err != nil {
		t.Fatal(err)
	}
	if rate.RateBps <= 0 {
		t.Errorf("%+v", rate)
	}
	if strings.Join(asked, ",") != "cgp-3a1f7c52,cgp-3a1f7c52,gw,cgp-3a1f7c52" {
		t.Errorf("the stacks asked for: %v", asked)
	}
}

// dialRecorder is a stack that connects to nothing and records where it was asked to.
type dialRecorder struct {
	mu    sync.Mutex
	addrs []string
}

func (d *dialRecorder) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, network+" "+addr)
	d.mu.Unlock()
	return nil, fmt.Errorf("connect %s: network is unreachable", addr)
}

func (d *dialRecorder) ListenICMP() (net.PacketConn, bool, error) {
	return nil, false, fmt.Errorf("no ICMP")
}

func (d *dialRecorder) first() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.addrs) == 0 {
		return ""
	}
	return d.addrs[0]
}

func TestAProbeDiagnosticAsksTheServersOfTheLease(t *testing.T) {
	rec := &dialRecorder{}
	srv := newDHCPServer("10.10.0.77", "10.10.0.1", "9.9.9.9")
	e := newExec(t, probeRunner(), WithProbeSocket(func(string, string) (probe.Socket, error) { return srv, nil }), WithProbeNet(func(string) diag.Net { return rec }))
	diagnose := func() {
		t.Helper()
		out, err := e.Do(context.Background(), mustDecode(t, `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"broker.example.com","port":80,"timeout":"300ms"},"servers":["127.0.0.1:9"],"proxy":"127.0.0.1:9"}`))
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Ok    bool   `json:"ok"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(out.Data[0], &d)
		if d.Ok || !strings.Contains(d.Error, "cannot resolve") {
			t.Errorf("%+v", d)
		}
	}
	// no client, no lease: the servers of the request are asked
	diagnose()
	if rec.first() != "udp 127.0.0.1:9" {
		t.Fatalf("%v", rec.addrs)
	}
	if _, err := e.DoBatch(context.Background(), ops(t, `{"type":"assign_interfaces","devs":["br-iot"]}`, pEnsure)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.probeLease(pNS) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the probe got no lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec.mu.Lock()
	rec.addrs = nil
	rec.mu.Unlock()
	diagnose()
	if rec.first() != "udp 10.10.0.1:53" {
		t.Errorf("the lease's server is not the first asked: %v", rec.addrs)
	}
}

func TestAProbeRunDoesNotWaitForTheWriter(t *testing.T) {
	fr := probeRunner()
	e := newExec(t, fr, WithProbeNet(func(string) diag.Net { return diag.HostNet{} }))
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	// The writer's tc command stays in the runner until the test lets it go, so a run that waited for the writer would
	// never end: no bound on the time of the run has to tell the two apart.
	inTC, gate := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	respond := fr.respond
	fr.respond = func(c Command) (Result, error) {
		if c.Tool == ToolTC {
			select {
			case inTC <- struct{}{}:
			default:
			}
			<-gate
		}
		return respond(c)
	}
	slow := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, tcWan)); slow <- err }()
	select {
	case <-inTC:
	case <-time.After(10 * time.Second):
		t.Fatal("the writer does not reach tc")
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	ran := make(chan error, 1)
	go func() {
		_, err := e.Do(context.Background(), mustDecode(t, fmt.Sprintf(`{"type":"probe_run","kind":"echo","host":"127.0.0.1","port":%d,"count":5,"interval_us":1000,"settle_ms":300}`, port)))
		ran <- err
	}()
	select {
	case err := <-ran:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run waits for the writer")
	}
	select {
	case err := <-slow:
		t.Fatalf("the writer ended before it was released: %v", err)
	default:
	}
	release()
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}
