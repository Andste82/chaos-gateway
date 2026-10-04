package bird

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func sample() Config {
	return Config{
		RouterID: "10.255.0.0", ASN: 65001, KernelTable: 100,
		Protected: []string{"10.10.0.0/24", "192.168.56.0/24", "203.0.113.0/24", "10.255.0.0/31"},
		Protocols: []Protocol{
			{Name: "bgp_site_b", Type: "bgp", Interface: "wg-site-b", LocalAddress: "10.255.0.0", NeighborAddress: "10.255.0.1",
				Announce: []string{"10.10.0.0/24", "10.99.0.0/24"},
				Import:   Import{Allowed: []Allowed{{Prefix: "10.60.0.0/22", MaxLength: 24}}, MaxPrefixes: 10},
				BGP:      &BGP{NeighborASN: 65002, HoldTime: 9, KeepaliveTime: 3}},
			{Name: "ospf_site_c", Type: "ospf", Interface: "wg-site-c", LocalAddress: "10.255.1.0", NeighborAddress: "10.255.1.1",
				Announce: []string{"10.10.0.0/24"},
				Import:   Import{AllowDefault: true, Allowed: []Allowed{{Prefix: "10.70.0.0/16"}}},
				OSPF:     &OSPF{Area: "0", Cost: 20, HelloInterval: 2, DeadInterval: 8},
				Custom:   "# a note\ndescription \"kept by hand\";"},
			{Name: "babel_site_d", Type: "babel", Interface: "wg-site-d", LocalAddress: "10.255.2.0", NeighborAddress: "10.255.2.1",
				Babel: &Babel{HelloInterval: 4, RxCost: 96}},
		},
		External: &External{Table: 200, Import: Import{MaxPrefixes: 50}},
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file (run with -update):\n%s", name, got)
	}
}

// birdParses runs `bird -p` on the text: the real parser is the arbiter of the syntax.
func birdParses(t *testing.T, text string) error {
	t.Helper()
	bin, err := exec.LookPath("bird")
	if err != nil {
		t.Skip("bird is not installed")
	}
	f := filepath.Join(t.TempDir(), "x.conf")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-p", "-c", f).CombinedOutput()
	if err != nil {
		return &parseError{string(out)}
	}
	return nil
}

type parseError struct{ out string }

func (e *parseError) Error() string { return strings.TrimSpace(e.out) }

func TestRenderGolden(t *testing.T) {
	text, err := sample().Render()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.conf", text)
	if err := birdParses(t, text); err != nil {
		t.Fatalf("bird rejects the generated configuration: %v\n%s", err, text)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, _ := sample().Render()
	b, _ := sample().Render()
	if a != b {
		t.Fatal("two renderings differ")
	}
}

func TestEveryProtocolAloneParses(t *testing.T) {
	full := sample()
	for i, p := range full.Protocols {
		c := full
		c.Protocols = []Protocol{p}
		c.External = nil
		text, err := c.Render()
		if err != nil {
			t.Fatal(err)
		}
		if err := birdParses(t, text); err != nil {
			t.Errorf("%s: %v\n%s", p.Name, err, text)
		}
		_ = i
	}
	// nothing to run: still a valid file
	empty := Config{RouterID: "10.0.0.1", KernelTable: 100}
	text, err := empty.Render()
	if err != nil || !empty.Empty() {
		t.Fatal(err)
	}
	if err := birdParses(t, text); err != nil {
		t.Errorf("%v\n%s", err, text)
	}
}

func TestTheGeneratedFiltersFollowSpikeS15(t *testing.T) {
	text, _ := sample().Render()
	for _, want := range []string{
		"learned routes are exported into table 100 only",
		"kernel table 100;",
		"learn off;",
		"import none; export where source ~ [ RTS_BABEL, RTS_BGP, RTS_INHERIT, RTS_OSPF, RTS_OSPF_EXT1, RTS_OSPF_EXT2, RTS_OSPF_IA ];",
		// BGP: no default, protected prefixes (and everything inside them) rejected, then the allowed list
		"if net = 0.0.0.0/0 then reject;",
		"if net ~ [ 10.10.0.0/24+, 192.168.56.0/24+, 203.0.113.0/24+, 10.255.0.0/31+ ] then reject;",
		"if net ~ [ 10.60.0.0/22{22,24} ] then accept;",
		"import limit 10 action disable;",
		"export where source = RTS_STATIC && net ~ [ 10.10.0.0/24, 10.99.0.0/24 ];",
		// OSPF: an allowed list combined with AllowDefault still lets the default route through
		"if net = 0.0.0.0/0 then accept;",
		"if net ~ [ 10.70.0.0/16 ] then accept;",
		"route 10.10.0.0/24 unreachable;",
		"route 10.10.0.0/24 blackhole;", // OSPF announces blackhole routes: a device route is not exported
		"hold time 9;", "keepalive time 3;",
		"interface \"wg-site-c\" { type ptp; hello 2; dead 8; cost 20; };",
		"interface \"wg-site-d\" { type tunnel; hello interval 4 s; rxcost 96; };",
		// the custom snippet sits inside its protocol and is marked
		"# --- custom snippet (unmanaged) ---\n  # a note\n  description \"kept by hand\";\n}",
		// external mode: kernel table 200, same protection
		"protocol kernel ext_import {\n  kernel table 200;\n  learn;",
		"protocol pipe ext_pipe {\n  table ext4;\n  peer table master4;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the configuration lacks %q", want)
		}
	}
	// a default route may be allowed explicitly, and only there
	if strings.Count(text, "if net = 0.0.0.0/0 then reject;") != 3 {
		t.Errorf("the default route is rejected by BGP, Babel and the external import, allowed for OSPF:\n%s", text)
	}
	// nothing but table 100 and the external table is written or read
	if n := strings.Count(text, "\n  kernel table"); n != 2 {
		t.Errorf("%d kernel protocols", n)
	}
}

func TestCheckRejectsWhatCannotBeRendered(t *testing.T) {
	bad := map[string]func(*Config){
		"router id":          func(c *Config) { c.RouterID = "x" },
		"table":              func(c *Config) { c.KernelTable = 254 },
		"protected":          func(c *Config) { c.Protected = []string{"nonsense"} },
		"duplicate name":     func(c *Config) { c.Protocols[1].Name = c.Protocols[0].Name },
		"name injection":     func(c *Config) { c.Protocols[0].Name = "x { } protocol y" },
		"interface":          func(c *Config) { c.Protocols[0].Interface = `a"b` },
		"address":            func(c *Config) { c.Protocols[0].NeighborAddress = "10.0.0.1; include" },
		"announce":           func(c *Config) { c.Protocols[0].Announce = []string{"10.0.0.1/24"} },
		"hold < 3 keepalive": func(c *Config) { c.Protocols[0].BGP.HoldTime = 8 },
		"no asn":             func(c *Config) { c.ASN = 0 },
		"ospf area":          func(c *Config) { c.Protocols[1].OSPF.Area = "0 { } " },
		"dead <= hello":      func(c *Config) { c.Protocols[1].OSPF.DeadInterval = 2 },
		"unknown type":       func(c *Config) { c.Protocols[2].Type = "rip" },
		"allowed max length": func(c *Config) { c.Protocols[0].Import.Allowed[0].MaxLength = 8 },
		"external == own":    func(c *Config) { c.External.Table = 100 },
		"snippet include":    func(c *Config) { c.Protocols[1].Custom = `include "/etc/passwd";` },
		"snippet close":      func(c *Config) { c.Protocols[1].Custom = "} protocol static x { ipv4;" },
		"snippet protocol":   func(c *Config) { c.Protocols[1].Custom = "protocol static y { }" },
		"snippet kernel":     func(c *Config) { c.Protocols[1].Custom = "kernel table 254;" },
	}
	for name, mod := range bad {
		c := sample()
		mod(&c)
		if _, err := c.Render(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLexicalChecks(t *testing.T) {
	for in, ok := range map[string]bool{
		"export limit 100 action warn;":         true,
		"# include is only a word in a comment": true,
		`description "include";`:                true,
		"/* protocol */ preference 100;":        true,
		"{ nested { } }":                        true,
		`include "x";`:                          false,
		"} {":                                   false,
		"{ unbalanced":                          false,
		"/* unterminated":                       false,
		`"unterminated`:                         false,
		"nul\x00":                               false,
		"tab\tok":                               true,
		"é":                                     false,
		"eval x;":                               false,
		"router id 1.1.1.1;":                    false,
	} {
		if err := CheckSnippet(in); (err == nil) != ok {
			t.Errorf("snippet %q: %v", in, err)
		}
	}
	text, _ := sample().Render()
	if err := CheckText(text, []int{100, 200}); err != nil {
		t.Errorf("a generated configuration passes: %v", err)
	}
	if err := CheckText(text, []int{100}); err == nil {
		t.Error("the external table is not allowed here")
	}
	for _, bad := range []string{"include \"/etc/bird.conf\";", "protocol kernel k { kernel table 254; }", "protocol x {", "x }"} {
		if err := CheckText(bad, []int{100}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := CheckText("# kernel table 254 in a comment\nprotocol device { }", []int{100}); err != nil {
		t.Errorf("a comment is no statement: %v", err)
	}
	if err := CheckText(strings.Repeat("a", maxConfigBytes+1), nil); err == nil {
		t.Error("too long")
	}
}

func TestParseProtocols(t *testing.T) {
	down, _ := os.ReadFile("testdata/show_protocols_down.txt")
	ps, err := ParseProtocols(string(down))
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %v", ps, err)
	}
	p := ps[0]
	if p.Name != "pa" || p.Proto != "BGP" || p.State != "down" || p.Info != "Error: Invalid next hop" || p.Neighbor != "127.0.0.2" || p.LastError != "Error: Invalid next hop" || p.ImportLimit != 3 || p.Established() {
		t.Errorf("%+v", p)
	}
	est, _ := os.ReadFile("testdata/show_protocols_established.txt")
	ps, err = ParseProtocols(string(est))
	if err != nil || len(ps) != 5 {
		t.Fatalf("%v %v", ps, err)
	}
	by := map[string]ProtocolStatus{}
	for _, p := range ps {
		by[p.Name] = p
	}
	bgp := by["bgp_rb"]
	if !bgp.Established() || bgp.Imported != 2 || bgp.Filtered != 3 || bgp.Exported != 1 || bgp.Neighbor != "10.255.0.1" || bgp.ImportLimit != 10 || bgp.Since != "2026-10-02" {
		t.Errorf("%+v", bgp)
	}
	if o := by["ospf_rb"]; !o.Established() || o.Imported != 1 || o.Exported != 2 {
		t.Errorf("%+v", o)
	}
	if !by["device1"].Established() || by["device1"].Info != "" {
		t.Errorf("%+v", by["device1"])
	}
	for _, bad := range []string{"", "nothing here", "Name Proto Table State Since Info\nbroken line"} {
		if _, err := ParseProtocols(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRenderRemote(t *testing.T) {
	c := sample()
	for _, p := range c.Protocols {
		text, err := RenderRemote(c, p, "wg0")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "router id "+p.NeighborAddress+";") || !strings.Contains(text, "protocol static announce") {
			t.Errorf("%s:\n%s", p.Type, text)
		}
		if err := birdParses(t, text); err != nil {
			t.Errorf("%s: bird rejects the remote configuration: %v\n%s", p.Type, err, text)
		}
	}
	bgp, _ := RenderRemote(c, c.Protocols[0], "wg0")
	for _, want := range []string{"local 10.255.0.1 as 65002;", "neighbor 10.255.0.0 as 65001;", "passive on;", "hold time 9;"} {
		if !strings.Contains(bgp, want) {
			t.Errorf("the BGP side lacks %q:\n%s", want, bgp)
		}
	}
	if _, err := RenderRemote(c, c.Protocols[0], `wg"0`); err == nil {
		t.Error("interface injection")
	}
}

func TestIdent(t *testing.T) {
	for in, want := range map[string]string{"bgp_site-b": "bgp_site_b", "1x": "p_1x", "": "p_", "a b.c": "a_b_c"} {
		if got := Ident(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if len(Ident(strings.Repeat("x", 100))) != 40 {
		t.Error("long names are cut")
	}
	if ProtocolName("bgp", "Site B") != "bgp_Site_B" {
		t.Error(ProtocolName("bgp", "Site B"))
	}
}
