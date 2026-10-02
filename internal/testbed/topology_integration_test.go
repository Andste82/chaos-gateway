//go:build testbed

package testbed_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return c
}

// M1 test: a client pings the server through a plain forwarding gateway. The server has no route
// back to the test networks, so the ping only works because the gateway forwards and masquerades.
func TestClientPingsServerThroughPlainForwardingGateway(t *testing.T) {
	top := testbed.NewDefault(t)
	for _, c := range []struct {
		name string
		from *testbed.Namespace
	}{{"A (network 0)", top.A}, {"B (network 0)", top.B}, {"C (network 1)", top.C}} {
		r := testbed.MustPing(t, c.from, testbed.ServerAddr, 5, 200*time.Millisecond)
		if r.Loss() != 0 {
			t.Errorf("%s: loss %.0f%% to the server", c.name, r.Loss()*100)
		}
		for _, ttl := range r.TTLs {
			if ttl != 63 {
				t.Errorf("%s: ttl %d, want 63 (one routing hop)", c.name, ttl)
			}
		}
	}
}

func TestServerHasNoRouteBackSoNATIsRequired(t *testing.T) {
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false))
	top.GW.Sysctl("net.ipv4.ip_forward", "1") // forwarding without masquerade
	r := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond)
	if r.Received != 0 {
		t.Fatalf("the ping must fail without NAT: the server has no route back, got %d replies", r.Received)
	}
	top.GW.MustStdin(`table ip cgnat {
  chain post {
    type nat hook postrouting priority srcnat; policy accept;
    oifname "wan0" masquerade
  }
}
`, "nft", "-f", "-")
	if r := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("with masquerade the ping must work, loss %.0f%%", r.Loss()*100)
	}
}

// udpSource sends a datagram from `from` to a listener in `to` and returns the source address
// the listener saw. It is the plain way to observe whether NAT happened.
func udpSource(t *testing.T, to *testbed.Namespace, dst string, from *testbed.Namespace) string {
	t.Helper()
	listener := to.Start("python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", 9000))
data, addr = s.recvfrom(64)
print(addr[0], flush=True)
`)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// the listener needs a moment to bind: send until it has answered
		_, _ = from.Run(ctx(t), "python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.sendto(b"x", ("`+dst+`", 9000))
`)
		select {
		case <-listener.Done():
			return strings.TrimSpace(listener.Output())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("the listener in %s received nothing (output %q)", to.Short, listener.Output())
	return ""
}

// The checks that only read the topology share one: building it takes about a minute under
// software emulation (plan §4.5, level 1b).
func TestDefaultTopology(t *testing.T) {
	top := testbed.NewDefault(t)
	t.Run("NAT address and routing between test networks", func(t *testing.T) {
		if got := udpSource(t, top.Server, testbed.ServerAddr, top.A); got != testbed.UplinkGateway {
			t.Errorf("the server saw %s, want the masqueraded %s", got, testbed.UplinkGateway)
		}
		// masquerade applies to the uplink only: between test networks the source stays as it is
		if got := udpSource(t, top.C, testbed.ClientCAddr, top.A); got != testbed.ClientAAddr {
			t.Errorf("C saw %s, want A's own address %s", got, testbed.ClientAAddr)
		}
	})
	t.Run("devices on one network are switched, not routed", func(t *testing.T) {
		r := testbed.MustPing(t, top.A, testbed.ClientBAddr, 3, 200*time.Millisecond)
		if r.Loss() != 0 {
			t.Fatalf("A -> B: loss %.0f%%", r.Loss()*100)
		}
		for _, ttl := range r.TTLs {
			if ttl != 64 {
				t.Fatalf("ttl %d: traffic on one test network must not pass the gateway's routing path (plan §2.2)", ttl)
			}
		}
	})
	t.Run("attached through gateway bridges", func(t *testing.T) {
		// the gateway address lives on the bridge, the physical port is a bridge port (spike S12)
		if out := top.GW.Must("ip", "-o", "addr", "show", "dev", "br-lan0"); !strings.Contains(out, testbed.LAN0Gateway+"/24") {
			t.Errorf("br-lan0 lacks the gateway address:\n%s", out)
		}
		if out := top.GW.Must("ip", "-o", "link", "show", "dev", "lan0"); !strings.Contains(out, "master br-lan0") {
			t.Errorf("lan0 is not a port of br-lan0:\n%s", out)
		}
		if out := top.GW.Must("ip", "-o", "addr", "show", "dev", "lan0"); strings.Contains(out, "inet ") {
			t.Errorf("the port itself must carry no address:\n%s", out)
		}
		if out := top.GW.Must("ip", "-o", "link", "show", "dev", "lan1"); !strings.Contains(out, "master br-lan1") {
			t.Errorf("lan1 is not a port of br-lan1:\n%s", out)
		}
	})
	t.Run("MACs are fixed", func(t *testing.T) {
		for _, c := range []struct {
			ns   *testbed.Namespace
			dev  string
			want string
		}{{top.A, "eth0", testbed.ClientAMAC}, {top.B, "eth0", testbed.ClientBMAC}, {top.C, "eth0", testbed.ClientCMAC},
			{top.GW, "lan0", testbed.GWLan0MAC}, {top.GW, "lan1", testbed.GWLan1MAC}} {
			if got := c.ns.MAC(c.dev); got != c.want {
				t.Errorf("%s %s: MAC %s, want %s", c.ns.Short, c.dev, got, c.want)
			}
		}
	})
	t.Run("management interface has the default route", func(t *testing.T) {
		if out := top.GW.Must("ip", "route", "show", "default"); !strings.Contains(out, "via "+testbed.MgmtPeer+" dev mgmt0") {
			t.Fatalf("default route: %q", out)
		}
		if out := top.GW.Must("ip", "route", "get", testbed.ServerAddr); !strings.Contains(out, "dev wan0") {
			t.Errorf("the connected uplink subnet must win over the default route: %q", out)
		}
		if out := top.GW.Must("ip", "route", "get", testbed.InternetAddr); !strings.Contains(out, "dev mgmt0") {
			t.Errorf("an address behind a default route must use the management route: %q", out)
		}
	})
}

func TestWithoutManagementDefaultRoute(t *testing.T) {
	top := testbed.NewDefault(t, testbed.WithManagementDefaultRoute(false))
	if out := top.GW.Must("ip", "route", "show", "default"); out != "" {
		t.Fatalf("no default route expected, got %q", out)
	}
}

// M1 test: a netem delay is visible. Under emulation only the effect is asserted; with KVM or on
// native hardware the delay must also be accurate (plan §4.3).
func TestNetemDelayIsVisible(t *testing.T) {
	top := testbed.NewDefault(t)
	base := testbed.MustPing(t, top.A, testbed.ServerAddr, 20, 100*time.Millisecond)
	if base.Loss() != 0 {
		t.Fatalf("baseline loss %.0f%%", base.Loss()*100)
	}

	top.GW.Must("tc", "qdisc", "add", "dev", "wan0", "root", "netem", "delay", "50ms")
	delayed := testbed.MustPing(t, top.A, testbed.ServerAddr, 20, 100*time.Millisecond)
	if delayed.Loss() != 0 {
		t.Fatalf("loss with netem: %.0f%%", delayed.Loss()*100)
	}
	got, ref := delayed.Median(), base.Median()
	t.Logf("median RTT: baseline %v, with 50 ms netem on wan0 %v (accurate=%v)", ref, got, testbed.Accurate())
	if got < ref+40*time.Millisecond {
		t.Fatalf("netem 50 ms is not visible: %v -> %v", ref, got)
	}
	if testbed.Accurate() && !testbed.Within(got, ref+50*time.Millisecond, 2*time.Millisecond, 0.05) {
		t.Fatalf("netem 50 ms off: %v -> %v, want %v ± (2 ms + 5 %%)", ref, got, ref+50*time.Millisecond)
	}

	// isolation: a fault on the uplink must not affect traffic between the test networks
	if iso := testbed.MustPing(t, top.A, testbed.ClientCAddr, 10, 100*time.Millisecond); iso.Median() > ref+25*time.Millisecond {
		t.Fatalf("a fault on the uplink delays A -> C: %v", iso.Median())
	}

	// removing the qdisc brings the baseline back
	top.GW.Must("tc", "qdisc", "del", "dev", "wan0", "root")
	after := testbed.MustPing(t, top.A, testbed.ServerAddr, 10, 100*time.Millisecond)
	if after.Median() > ref+20*time.Millisecond {
		t.Fatalf("delay remains after the qdisc was removed: %v", after.Median())
	}
}

func TestPrefixesAreUniquePerBed(t *testing.T) {
	a := testbed.New(t)
	b := testbed.New(t)
	if a.Prefix() == b.Prefix() {
		t.Fatalf("two beds share the prefix %s", a.Prefix())
	}
	a.Add("x")
	b.Add("x") // same short name, different namespace
	if a.NS("x").Name == b.NS("x").Name {
		t.Fatal("namespace names collide")
	}
}

func TestProxyVariablesAreNotPassedIntoNamespaces(t *testing.T) {
	t.Setenv("http_proxy", "http://127.0.0.1:3128")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:3128")
	bed := testbed.New(t)
	ns := bed.Add("env")
	if out := ns.Must("env"); strings.Contains(strings.ToLower(out), "proxy") {
		t.Fatalf("proxy variables leaked into the namespace:\n%s", out)
	}
}

// Processes in a namespace are killed before the namespace is deleted, and nothing is left over
// (spike S1: a live process keeps its namespace alive).
func TestDestroyKillsProcessesAndRemovesNamespaces(t *testing.T) {
	var names []string
	var strayPID string
	var proc *testbed.Process
	t.Run("bed", func(t *testing.T) {
		top := testbed.NewDefault(t)
		for _, ns := range []*testbed.Namespace{top.GW, top.A, top.Server} {
			names = append(names, ns.Name)
		}
		proc = top.Server.Start("sleep", "300")
		// a process that was not started through the bed must not keep the namespace alive either
		strayPID = top.A.Must("sh", "-c", "nohup sleep 300 >/dev/null 2>&1 & echo $!")
		select {
		case <-proc.Done():
			t.Fatal("the process exited early")
		case <-time.After(100 * time.Millisecond):
		}
	})
	select {
	case <-proc.Done():
	default:
		t.Error("the background process is still running after the test")
	}
	// the stray process must be dead too: it would keep a deleted namespace alive, invisible to
	// `ip netns list`
	if raw, err := os.ReadFile("/proc/" + strayPID + "/stat"); err == nil {
		stat := string(raw)
		if rest := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:]); len(rest) > 0 && rest[0] != "Z" {
			t.Errorf("process %s started inside a namespace survived the test", strayPID)
		}
	}
	listed, err := exec.Command("ip", "netns", "list").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	out := string(listed)
	for _, n := range names {
		if strings.Contains(out, n) {
			t.Errorf("namespace %s still exists after the test:\n%s", n, out)
		}
	}
}
