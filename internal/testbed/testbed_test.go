package testbed

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const pingOutput = `PING 203.0.113.10 (203.0.113.10) 56(84) bytes of data.
64 bytes from 203.0.113.10: icmp_seq=1 ttl=63 time=0.412 ms
64 bytes from 203.0.113.10: icmp_seq=2 ttl=63 time=51.5 ms
64 bytes from 203.0.113.10: icmp_seq=4 ttl=63 time=50.25 ms

--- 203.0.113.10 ping statistics ---
4 packets transmitted, 3 received, 25% packet loss, time 612ms
rtt min/avg/max/mdev = 0.412/34.054/51.500/24.026 ms
`

func TestParsePing(t *testing.T) {
	r, err := ParsePing(pingOutput)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sent != 4 || r.Received != 3 || len(r.RTTs) != 3 || len(r.TTLs) != 3 {
		t.Fatalf("result = %+v", r)
	}
	if r.Loss() != 0.25 {
		t.Fatalf("loss = %v", r.Loss())
	}
	if r.RTTs[1] != 51500*time.Microsecond || r.TTLs[0] != 63 {
		t.Fatalf("rtt/ttl = %v / %v", r.RTTs[1], r.TTLs[0])
	}
	if r.Median() != 50250*time.Microsecond {
		t.Fatalf("median = %v", r.Median())
	}
}

func TestParsePingTotalLoss(t *testing.T) {
	r, err := ParsePing("--- x ping statistics ---\n3 packets transmitted, 0 received, 100% packet loss, time 2040ms\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.Loss() != 1 || len(r.RTTs) != 0 || r.Median() != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestParsePingErrorsOnBrokenOutput(t *testing.T) {
	if _, err := ParsePing("ping: connect: Network is unreachable"); err == nil {
		t.Fatal("output without a summary must be an error, not 100 % loss")
	}
}

func TestParsePingBusyboxStyleReceivedWord(t *testing.T) {
	r, err := ParsePing("2 packets transmitted, 2 packets received, 0% packet loss")
	if err != nil || r.Received != 2 {
		t.Fatalf("%+v, %v", r, err)
	}
}

func TestLossWithoutPacketsIsZero(t *testing.T) {
	if (PingResult{}).Loss() != 0 {
		t.Fatal("no packets sent must not divide by zero")
	}
}

func TestMedian(t *testing.T) {
	ms := func(v ...int) []time.Duration {
		out := make([]time.Duration, len(v))
		for i, x := range v {
			out[i] = time.Duration(x) * time.Millisecond
		}
		return out
	}
	tests := []struct {
		in   []time.Duration
		want time.Duration
	}{
		{nil, 0},
		{ms(7), 7 * time.Millisecond},
		{ms(9, 1, 5), 5 * time.Millisecond},
		{ms(4, 1, 3, 2), 2500 * time.Microsecond},
	}
	for _, tt := range tests {
		in := append([]time.Duration(nil), tt.in...)
		if got := Median(tt.in); got != tt.want {
			t.Errorf("Median(%v) = %v, want %v", in, got, tt.want)
		}
		for i := range in {
			if tt.in[i] != in[i] {
				t.Errorf("Median modified its input: %v -> %v", in, tt.in)
			}
		}
	}
}

func TestPercentile(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	if got := Percentile(ds, 90); got != 90*time.Millisecond {
		t.Errorf("p90 = %v", got)
	}
	if got := Percentile(ds, 100); got != 100*time.Millisecond {
		t.Errorf("p100 = %v", got)
	}
	if got := Percentile(ds, 0); got != time.Millisecond {
		t.Errorf("p0 = %v", got)
	}
	if got := Percentile(nil, 50); got != 0 {
		t.Errorf("empty = %v", got)
	}
}

func TestWithin(t *testing.T) {
	// plan §4.3 latency tolerance: ±2 ms + 5 %
	want := 100 * time.Millisecond
	if !Within(106900*time.Microsecond, want, 2*time.Millisecond, 0.05) {
		t.Error("106.9 ms is within 100 ms ± 7 ms")
	}
	if Within(107100*time.Microsecond, want, 2*time.Millisecond, 0.05) {
		t.Error("107.1 ms is outside 100 ms ± 7 ms")
	}
	if !Within(93*time.Millisecond, want, 2*time.Millisecond, 0.05) {
		t.Error("the tolerance is symmetric")
	}
}

func TestCleanEnvRemovesProxyVariables(t *testing.T) {
	in := []string{"PATH=/bin", "http_proxy=http://p:3128", "HTTPS_PROXY=x", "no_proxy=localhost", "ALL_PROXY=y", "HOME=/root", "all_proxy=z", "https_proxy=a", "HTTP_PROXY=b", "NO_PROXY=c"}
	got := CleanEnv(in)
	if strings.Join(got, " ") != "PATH=/bin HOME=/root" {
		t.Fatalf("env = %v", got)
	}
}

func TestRandomPrefixIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		p := RandomPrefix()
		if !strings.HasPrefix(p, "tb") || len(p) != 8 {
			t.Fatalf("prefix %q", p)
		}
		if seen[p] {
			t.Fatalf("duplicate prefix %q", p)
		}
		seen[p] = true
	}
}

func TestAccurateFollowsTheEmulationFlag(t *testing.T) {
	t.Setenv(EmulatedEnv, "1")
	if !Emulated() || Accurate() {
		t.Fatal("emulated runs are not accurate")
	}
	t.Setenv(EmulatedEnv, "")
	if Emulated() || !Accurate() {
		t.Fatal("native runs are accurate")
	}
}

func TestNewConfigDefaults(t *testing.T) {
	c := newConfig(nil)
	if !c.plainGateway || !c.managementDefaultRoute || c.prefix != "" {
		t.Fatalf("defaults = %+v", c)
	}
	c = newConfig([]Option{WithPrefix("x"), WithPlainGateway(false), WithManagementDefaultRoute(false)})
	if c.plainGateway || c.managementDefaultRoute || c.prefix != "x" {
		t.Fatalf("options = %+v", c)
	}
}

func TestDefaultTopologyAddressesAreConsistent(t *testing.T) {
	lan0 := netip.MustParsePrefix(LAN0Subnet)
	lan1 := netip.MustParsePrefix(LAN1Subnet)
	for name, a := range map[string]string{"gateway0": LAN0Gateway, "A": ClientAAddr, "B": ClientBAddr} {
		if !lan0.Contains(netip.MustParseAddr(a)) {
			t.Errorf("%s %s is outside %s", name, a, lan0)
		}
	}
	for name, a := range map[string]string{"gateway1": LAN1Gateway, "C": ClientCAddr} {
		if !lan1.Contains(netip.MustParseAddr(a)) {
			t.Errorf("%s %s is outside %s", name, a, lan1)
		}
	}
	uplink := netip.MustParsePrefix("203.0.113.0/24")
	for _, a := range []string{UplinkGateway, ServerAddr, ServerAddr2} {
		if !uplink.Contains(netip.MustParseAddr(a)) {
			t.Errorf("%s is outside the uplink subnet", a)
		}
	}
	if uplink.Contains(netip.MustParseAddr(InternetAddr)) {
		t.Error("the internet host must be reachable only by a default route")
	}
	mgmt := netip.MustParsePrefix("192.168.56.0/24")
	if !mgmt.Contains(netip.MustParseAddr(MgmtGateway)) || !mgmt.Contains(netip.MustParseAddr(MgmtPeer)) {
		t.Error("management addresses outside the management subnet")
	}
	if lan0.Overlaps(lan1) || lan0.Overlaps(uplink) || lan1.Overlaps(uplink) {
		t.Error("networks overlap")
	}
}

func TestDefaultTopologyMACsAreValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range []string{ClientAMAC, ClientBMAC, ClientCMAC, GWLan0MAC, GWLan1MAC} {
		hw, err := net.ParseMAC(m)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if hw[0]&0x02 == 0 || hw[0]&0x01 != 0 {
			t.Errorf("%s must be locally administered unicast", m)
		}
		if seen[m] {
			t.Errorf("duplicate MAC %s", m)
		}
		seen[m] = true
	}
}

// failTB is a testing.TB whose Fatalf panics, so a test can check that a helper calls it.
type failTB struct {
	testing.TB
	msg string
}

func (f *failTB) Helper() {}
func (f *failTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f)
}

func TestAddingANamespaceTwiceFailsTheTest(t *testing.T) {
	ft := &failTB{TB: t}
	bed := &Bed{t: ft, prefix: "tbunit", nss: []*Namespace{{Short: "dup", Name: "tbunit-dup"}}}
	defer func() {
		if r := recover(); r != ft {
			t.Fatalf("expected Fatalf, got %v", r)
		}
		if !strings.Contains(ft.msg, `"dup" added twice`) {
			t.Fatalf("message = %q", ft.msg)
		}
	}()
	bed.Add("dup")
	t.Fatal("Add must not return for a duplicate name")
}

func TestNSFindsNamespacesByShortName(t *testing.T) {
	a := &Namespace{Short: "a", Name: "tbunit-a"}
	bed := &Bed{prefix: "tbunit", nss: []*Namespace{a}}
	if bed.NS("a") != a || bed.NS("missing") != nil {
		t.Fatal("NS lookup failed")
	}
}

func TestCommandRunsInsideTheNamespaceWithACleanEnvironment(t *testing.T) {
	t.Setenv("http_proxy", "http://p:3128")
	ns := &Namespace{Short: "a", Name: "tbunit-a"}
	cmd := ns.Command(context.Background(), "ping", "-c", "1", "10.0.0.1")
	if strings.Join(cmd.Args, " ") != "ip netns exec tbunit-a ping -c 1 10.0.0.1" {
		t.Fatalf("args = %v", cmd.Args)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(strings.ToLower(kv), "http_proxy=") {
			t.Fatalf("proxy variable in %v", kv)
		}
	}
}
