package compiler

import (
	"encoding/json"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	iotID = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	labID = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"
)

func loadConfig(t *testing.T, name string) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// testbedHost is the host of the testbed topology: the physical ports with their MACs, the uplink
// with its address and the management interface with a default route.
func testbedHost() Host {
	return Host{
		Links: []HostLink{
			{Name: "lo", MAC: "00:00:00:00:00:00"},
			{Name: "lan0", MAC: "02:00:00:00:00:01", Kind: "veth"},
			{Name: "lan1", MAC: "02:00:00:00:01:01", Kind: "veth"},
			{Name: "wan0", MAC: "02:00:00:00:02:01", Kind: "veth", Addrs: []netip.Prefix{pfx("203.0.113.1/24")}},
			{Name: "mgmt0", MAC: "02:00:00:00:03:01", Kind: "veth", Addrs: []netip.Prefix{pfx("192.168.56.1/24")}},
		},
		Defaults: []HostRoute{{Dev: "mgmt0", Gateway: netip.MustParseAddr("192.168.56.254"), Metric: 0}},
	}
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from the golden file (run with -update to accept):\n%s", name, diffLines(string(want), string(got)))
	}
}

func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			out = append(out, "line "+itoa(i+1)+":\n  want "+a+"\n  got  "+b)
			if len(out) >= 8 {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func compileBasic(t *testing.T, mod func(*model.Configuration, *Host)) *Target {
	t.Helper()
	cfg := loadConfig(t, "gateway.yaml")
	h := testbedHost()
	if mod != nil {
		mod(cfg, &h)
	}
	return Compile(Input{Config: cfg, Host: h, Generation: Generation{Revision: 3, Seq: 7}})
}

func TestGoldenRoutedGateway(t *testing.T) {
	tg := compileBasic(t, nil)
	if tg.HasErrors() || len(tg.Problems) != 0 {
		t.Fatalf("problems: %+v", tg.Problems)
	}
	golden(t, "routed", tg)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pretty any
	_ = json.Unmarshal(tx, &pretty)
	golden(t, "routed.nft", pretty)
}

func TestGoldenTwoPortTopology(t *testing.T) {
	// management on the uplink side: one interface is both (plan §2.2)
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		cfg.Management.Interface = model.InterfaceRef{Name: ptr("wan0")}
		cfg.Management.AllowedSources = nil // default: the connected subnet of the interface
	})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.Management.Name != "wan0" || len(tg.Management.Sources) != 1 || tg.Management.Sources[0].String() != "203.0.113.0/24" {
		t.Errorf("management %+v", tg.Management)
	}
	golden(t, "twoport", tg)
}

func ptr[T any](v T) *T { return &v }

func TestInterfacesAreFoundByMACEvenWhenRenamed(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		for i := range h.Links {
			if h.Links[i].Name == "lan0" {
				h.Links[i].Name = "enp3s0" // the configuration still says lan0, the MAC is the same
			}
		}
	})
	for _, b := range tg.Bridges {
		if b.NetworkName == "IoT" && (len(b.Ports) != 1 || b.Ports[0] != "enp3s0") {
			t.Errorf("IoT ports %v, want the renamed interface", b.Ports)
		}
	}
	if !contains(tg.Interfaces, "enp3s0") || contains(tg.Interfaces, "lan0") {
		t.Errorf("interfaces %v", tg.Interfaces)
	}
}

func TestAMACThatIsNotPresentDoesNotFallBackToTheName(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		for i := range h.Links {
			if h.Links[i].Name == "lan0" {
				h.Links[i].MAC = "02:ff:ff:ff:ff:ff" // another device now carries the name
			}
		}
	})
	var found bool
	for _, p := range tg.Problems {
		found = found || p.Code == CodePortMissing && p.Network == "IoT"
	}
	if !found || tg.HasErrors() {
		t.Fatalf("a missing port degrades the network, it is no error: %+v", tg.Problems)
	}
	for _, b := range tg.Bridges {
		if b.NetworkName == "IoT" && len(b.Ports) != 0 {
			t.Errorf("ports %v", b.Ports)
		}
	}
}

func TestABridgeIsNotCandidateForAMAC(t *testing.T) {
	// a bridge takes the MAC of its first port: it must not be mistaken for the port
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		h.Links = append([]HostLink{{Name: "br-iot", MAC: "02:00:00:00:00:01", Kind: "bridge"}}, h.Links...)
	})
	for _, b := range tg.Bridges {
		if b.NetworkName == "IoT" && (len(b.Ports) != 1 || b.Ports[0] != "lan0") {
			t.Errorf("ports %v", b.Ports)
		}
	}
}

func TestUplinkProblems(t *testing.T) {
	for name, c := range map[string]struct {
		mod  func(*model.Configuration, *Host)
		code string
	}{
		"missing":     {func(cfg *model.Configuration, h *Host) { cfg.Uplink.Interface = model.InterfaceRef{Name: ptr("nope0")} }, CodeUplinkMissing},
		"no address":  {func(cfg *model.Configuration, h *Host) { h.Links[3].Addrs = nil }, CodeUplinkNoAddress},
		"no gateway":  {func(cfg *model.Configuration, h *Host) { cfg.Uplink.Gateway = nil }, CodeUplinkNoGateway},
		"bad gateway": {func(cfg *model.Configuration, h *Host) { cfg.Uplink.Gateway = ptr("not-an-ip") }, CodeUplinkNoGateway},
	} {
		tg := compileBasic(t, c.mod)
		var found bool
		for _, p := range tg.Errors() {
			found = found || p.Code == c.code
		}
		if !found {
			t.Errorf("%s: want error %s, got %+v", name, c.code, tg.Problems)
		}
	}
}

func TestTheGatewayFollowsTheOSDefaultRouteOnTheUplink(t *testing.T) {
	tg := compileBasic(t, func(cfg *model.Configuration, h *Host) {
		cfg.Uplink.Gateway = nil
		h.Defaults = append(h.Defaults,
			HostRoute{Dev: "wan0", Gateway: netip.MustParseAddr("203.0.113.254"), Metric: 200},
			HostRoute{Dev: "wan0", Gateway: netip.MustParseAddr("203.0.113.253"), Metric: 100})
	})
	if tg.Uplink.Gateway.String() != "203.0.113.253" {
		t.Errorf("gateway %v: the lowest metric on the uplink interface wins, other interfaces do not count", tg.Uplink.Gateway)
	}
}

func TestPolicyRoutingTable100(t *testing.T) {
	tg := compileBasic(t, nil)
	var got []string
	for _, r := range tg.Routes {
		s := r.Dst
		if r.Via != "" {
			s += " via " + r.Via
		}
		got = append(got, s+" dev "+r.Dev)
		if r.Table != PolicyTable || r.Action != "replace" || r.Family != 4 {
			t.Errorf("route %+v", r)
		}
	}
	want := []string{
		"10.10.0.0/24 dev br-iot", "10.20.0.0/24 dev br-lab", "203.0.113.0/24 dev wan0", // connected
		"10.30.0.0/24 via 10.10.0.2 dev br-iot", // downstream
		"default via 203.0.113.10 dev wan0",     // last
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("routes:\n%v\nwant\n%v", got, want)
	}
	// one rule per bridge, and one for the destination of every route behind a router: replies
	// from the uplink to such a network arrive on the uplink interface
	if len(tg.Rules) != 3 || tg.Rules[0].Iif != "br-iot" || tg.Rules[1].Iif != "br-lab" || tg.Rules[0].Priority != 1000 || tg.Rules[1].Priority != 1000 || tg.Rules[0].Table != 100 || tg.Rules[2].To != "10.30.0.0/24" || tg.Rules[2].Iif != "" {
		t.Errorf("rules %+v", tg.Rules)
	}
	for _, r := range tg.Rules {
		if r.Priority >= 32766 || r.Priority < 1 {
			t.Errorf("priority %d is outside what the executor accepts", r.Priority)
		}
	}
}

func TestHostStateOfTheTarget(t *testing.T) {
	tg := compileBasic(t, nil)
	if strings.Join(tg.Interfaces, ",") != "br-iot,br-lab,lan0,lan1,wan0" {
		t.Errorf("interfaces %v", tg.Interfaces)
	}
	if strings.Join(tg.DockerUser, ",") != "br-iot,br-lab" {
		t.Errorf("DOCKER-USER interfaces %v: the bridges of the test networks (a packet to or from the uplink has a bridge on its other side)", tg.DockerUser)
	}
	if strings.Join(tg.Offloads, ",") != "br-iot,br-lab,lan0,lan1,wan0" {
		t.Errorf("offloads %v", tg.Offloads)
	}
	var ra []string
	var fwd bool
	for _, s := range tg.Sysctls {
		if s.Name == "ip_forward" && s.Value == 1 {
			fwd = true
		}
		if s.Name == "accept_ra" {
			if s.Value != 0 {
				t.Errorf("accept_ra %+v", s)
			}
			ra = append(ra, s.Dev)
		}
	}
	if !fwd || strings.Join(ra, ",") != "br-iot,br-lab,lan0,lan1" {
		t.Errorf("sysctls %+v: forwarding on, RA off on the interfaces Chaos Gateway owns, not on the OS-owned uplink", tg.Sysctls)
	}
}

func TestBridgeNames(t *testing.T) {
	used := map[string]bool{}
	for in, want := range map[string]string{
		"IoT":           "br-iot",
		"Lab Network 1": "br-lab-network-",
		"Büro":          "br-b-ro",
		"":              "br-0b7c6a3e",
		"0123456789ab":  "br-0b7c6a3e", // br-<12 hex> would look like a Docker network
	} {
		got := bridgeName("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", in, used)
		if len(got) > 15 {
			t.Errorf("%q: %q is longer than 15", in, got)
		}
		if in != "" && in != "0123456789ab" && got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
		if in == "" && got == "br-" {
			t.Errorf("empty name gives %q", got)
		}
	}
	// two networks whose names give the same bridge name keep distinct names
	u := map[string]bool{}
	a := bridgeName("11111111-0000-4000-8000-000000000000", "Lab", u)
	b := bridgeName("22222222-0000-4000-8000-000000000000", "lab", u)
	if a == b {
		t.Errorf("both networks got %q", a)
	}
}

func TestCompileIsDeterministicAndTheHashIgnoresTheGeneration(t *testing.T) {
	a := compileBasic(t, nil)
	b := compileBasic(t, nil)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("two compilations of the same input differ")
	}
	cfg := loadConfig(t, "gateway.yaml")
	c := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 9, Seq: 99}})
	if c.Hash != a.Hash {
		t.Errorf("hash %s vs %s: the generation is not content", a.Hash, c.Hash)
	}
	if c.Nft.Generation != "gen=99 rev=9" {
		t.Errorf("generation comment %q", c.Nft.Generation)
	}
	// a change of the content changes the hash
	d := compileBasic(t, func(cfg *model.Configuration, h *Host) { cfg.Uplink.Gateway = ptr("203.0.113.20") })
	if d.Hash == a.Hash {
		t.Error("another gateway, the same hash")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func parseRuleset(t *testing.T, doc string) *linux.Ruleset {
	t.Helper()
	rs, err := linux.ParseNft([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// M2-04 test: the compiler's own routing tables stay inside the range the domain reserves against
// external routing daemons.
func TestPolicyAndServiceTablesAreWithinTheReservedRange(t *testing.T) {
	if PolicyTable < domain.OwnTableFirst || PolicyTable > domain.OwnTableLast {
		t.Errorf("PolicyTable %d is outside %d-%d", PolicyTable, domain.OwnTableFirst, domain.OwnTableLast)
	}
	if ServiceTable < domain.OwnTableFirst || ServiceTable > domain.OwnTableLast {
		t.Errorf("ServiceTable %d is outside %d-%d", ServiceTable, domain.OwnTableFirst, domain.OwnTableLast)
	}
}
