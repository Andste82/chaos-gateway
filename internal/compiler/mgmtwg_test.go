package compiler

import (
	"os"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// P31-AUDIT-11 (decided 2026-10-18, option (b)): the WireGuard networks of the role management are part of the
// management network for the management guard, in both topologies. The guard drops what a device under test
// sends out through such a network's interface (its clients and the networks behind them included), and an
// explicit matrix entry towards the network, towards one of its clients or towards the management network
// returns from the guard by its destination, as the entry `management` does since P31-AUDIT-01. A client's
// `reachable` list is no explicit entry: it does not open the guard (plan §2.16).

// adminWG is the WireGuard scenario with a client "adm" (and the network 10.97.0.0/24 behind it) in the
// management hub, the entries "IoT -> admin" (the network) and "site-b -> management", and the test client rA
// naming the admin network in its reachable list.
func adminWG(t *testing.T, twoPort bool) *Target {
	t.Helper()
	return adminWGWith(t, twoPort, `    - from: {network: IoT}
      to: {network: admin}
      policy: allow
    - from: {network: site-b}
      to: {management: true}
      policy: allow
`)
}

// adminWGWith is the scenario of adminWG with other matrix entries (YAML, in front of "IoT -> lab-hub"); edits
// change the YAML text further.
func adminWGWith(t *testing.T, twoPort bool, entries string, edits ...func(string) string) *Target {
	t.Helper()
	raw, err := os.ReadFile("testdata/wireguard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(raw), "    listen_port: 51822\n", `    listen_port: 51822
    clients:
      1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9:
        name: adm
        address: 10.98.0.2
        client_networks: [10.97.0.0/24]
        key: {mode: provided, public_key: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='}
`, 1)
	text = strings.Replace(text, "          - network: IoT\n          - uplink: true\n", "          - network: IoT\n          - uplink: true\n          - network: admin\n", 1)
	text = strings.Replace(text, "access_matrix:\n  entries:\n", "access_matrix:\n  entries:\n"+entries, 1)
	if twoPort {
		text = strings.Replace(text, "  interface: {name: mgmt0}\n", "  interface: {name: wan0}\n", 1)
	}
	for _, edit := range edits {
		text = edit(text)
	}
	cfg, err := domain.DecodeConfiguration([]byte(text), domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs := domain.Normalize(cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 2, Seq: 5}, Keys: wgKeys})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg
}

// describeGuard is the readable form of the forward chain and the guard for the golden files.
func describeGuard(t *testing.T, tg *Target) string {
	var b strings.Builder
	for _, name := range []string{"forward", MgmtGuardChain} {
		b.WriteString("# chain " + name + "\n")
		for _, r := range renderRules(chainOf(t, tg, name)) {
			b.WriteString("  " + r + "\n")
		}
	}
	return b.String()
}

func TestGoldenTheGuardOfManagementRoleWireGuardNetworks(t *testing.T) {
	goldenText(t, "access-threeport-wg", describeGuard(t, adminWG(t, false)))
	goldenText(t, "access-twoport-wg", describeGuard(t, adminWG(t, true)))
}

func TestAMatrixEntryTowardsAManagementRoleWireGuardNetworkReturnsFromTheGuard(t *testing.T) {
	for _, twoPort := range []bool{false, true} {
		tg := adminWG(t, twoPort)
		guard := renderRules(chainOf(t, tg, MgmtGuardChain))
		test := setName(t, tg, "ifs_test")
		has := func(rule string) bool {
			for _, r := range guard {
				if r == rule {
					return true
				}
			}
			return false
		}
		for _, want := range []string{
			// the entry towards the network returns by its interface
			"meta iifname br-iot meta oifname wg-admin return",
			// the entry towards the management network returns for the management-role network too
			"meta iifname wg-site-b meta oifname wg-admin return",
			// whatever leaves through the management-role network's interface is dropped, in both topologies
			"meta iifname @" + test + ` meta oifname wg-admin counter "forward_drop" drop`,
		} {
			if !has(want) {
				t.Errorf("two ports %v: no %q in the guard:\n%s", twoPort, want, strings.Join(guard, "\n"))
			}
		}
		// the returns stand in front of the drops
		last := -1
		for i, r := range guard {
			if strings.HasSuffix(r, " return") {
				last = i
			}
		}
		for i, r := range guard {
			if strings.HasSuffix(r, " drop") && i < last {
				t.Errorf("two ports %v: a drop in front of a return: %s", twoPort, r)
			}
		}
		// rA's reachable list is no explicit entry: the matrix has its accept, but nothing returns from the guard
		// for the test hub, so the guard drops it
		fwd := renderRules(chainOf(t, tg, "forward"))
		if !slicesContain(fwd, "meta iifname wg-lab-hub ip saddr { 10.99.0.2/32, 10.50.0.0/24 } meta oifname wg-admin accept") {
			t.Errorf("two ports %v: no implicit entry of rA:\n%s", twoPort, strings.Join(fwd, "\n"))
		}
		for _, r := range guard {
			if strings.HasPrefix(r, "meta iifname wg-lab-hub") {
				t.Errorf("two ports %v: the reachable list opens the guard: %s", twoPort, r)
			}
		}
		// the entry "site-b -> management" also accepts towards the management-role network in the matrix
		if !slicesContain(fwd, "meta iifname wg-site-b meta oifname wg-admin accept") || !slicesContain(fwd, "meta iifname br-iot meta oifname wg-admin accept") {
			t.Errorf("two ports %v: the matrix entries:\n%s", twoPort, strings.Join(fwd, "\n"))
		}
	}
	// without an entry nothing opens it, and nothing of the guard is about it without such a network
	tg := compileWG(t, func(c *model.Configuration, _ *Input) {})
	if g := renderRules(chainOf(t, tg, MgmtGuardChain)); slicesContain(g, "meta iifname br-iot meta oifname wg-admin return") {
		t.Errorf("a return without an entry: %v", g)
	}
	plain := compileBasic(t, nil)
	for _, r := range renderRules(chainOf(t, plain, MgmtGuardChain)) {
		if strings.Contains(r, "wg-") {
			t.Errorf("a WireGuard rule without WireGuard networks: %s", r)
		}
	}
}

func slicesContain(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// The review of the decisions of 2026-10-18: the extra matches of the endpoint `management` for the WireGuard
// networks of the role management rank after the networks, as a destination and as a source, so the more
// specific entry (towards or from the network itself) decides, in either order of the policies and in both
// topologies. In the three-port topology the address match of `management` leaves those interfaces out, since
// their tunnel subnets are among the allowed sources.
const (
	// the specific entries deny, the management entries allow
	specificDeny = `    - from: {network: IoT}
      to: {network: admin}
      policy: deny
    - from: {network: IoT}
      to: {management: true}
      policy: allow
    - from: {network: admin}
      to: {network: IoT}
      policy: deny
    - from: {management: true}
      to: {network: IoT}
      policy: allow
`
	// the reverse
	specificAllow = `    - from: {network: IoT}
      to: {network: admin}
      policy: allow
    - from: {network: IoT}
      to: {management: true}
      policy: deny
    - from: {network: admin}
      to: {network: IoT}
      policy: allow
    - from: {management: true}
      to: {network: IoT}
      policy: deny
`
)

func TestGoldenTheSpecificEntryWinsOverTheManagementEntryForAManagementRoleWireGuardNetwork(t *testing.T) {
	goldenText(t, "access-threeport-wg-specific-deny", describeGuard(t, adminWGWith(t, false, specificDeny)))
	goldenText(t, "access-twoport-wg-specific-deny", describeGuard(t, adminWGWith(t, true, specificDeny)))
	goldenText(t, "access-threeport-wg-specific-allow", describeGuard(t, adminWGWith(t, false, specificAllow)))
	goldenText(t, "access-twoport-wg-specific-allow", describeGuard(t, adminWGWith(t, true, specificAllow)))
}

func TestTheSpecificEntryWinsOverTheManagementEntryForAManagementRoleWireGuardNetwork(t *testing.T) {
	first := func(rules []string, prefix string) string {
		for _, r := range rules {
			if strings.HasPrefix(r, prefix) {
				return r
			}
		}
		return ""
	}
	for _, twoPort := range []bool{false, true} {
		for name, sc := range map[string]struct {
			entries string
			want    string // the verdict of the specific entries
		}{"specific deny": {specificDeny, "drop"}, "specific allow": {specificAllow, "accept"}} {
			tg := adminWGWith(t, twoPort, sc.entries)
			fwd := renderRules(chainOf(t, tg, "forward"))
			mgmt := setName(t, tg, "mgmt_src")
			// IoT -> admin: the first rule that matches what leaves through wg-admin is the specific one
			if r := first(fwd, "meta iifname br-iot meta oifname wg-admin "); !strings.HasSuffix(r, " "+sc.want) {
				t.Errorf("two ports %v, %s: IoT -> admin is decided by %q:\n%s", twoPort, name, r, strings.Join(fwd, "\n"))
			}
			// admin -> IoT: the same for what comes in through wg-admin
			if r := first(fwd, "meta iifname wg-admin meta oifname br-iot "); !strings.HasSuffix(r, " "+sc.want) {
				t.Errorf("two ports %v, %s: admin -> IoT is decided by %q:\n%s", twoPort, name, r, strings.Join(fwd, "\n"))
			}
			// the management entries keep their own matches for the rest of the management network
			toMgmt, fromMgmt := "meta iifname br-iot meta oifname wan0 ip daddr @"+mgmt+" ", "meta iifname wan0 ip saddr @"+mgmt+" meta oifname br-iot "
			if !twoPort {
				// the three-port address match leaves the management-role interface out
				toMgmt, fromMgmt = "meta iifname br-iot ip daddr @"+mgmt+" meta oifname != wg-admin ", "meta iifname mgmt0 ip saddr @"+mgmt+" meta oifname br-iot "
			}
			if first(fwd, toMgmt) == "" || first(fwd, fromMgmt) == "" {
				t.Errorf("two ports %v, %s: no %q or %q:\n%s", twoPort, name, toMgmt, fromMgmt, strings.Join(fwd, "\n"))
			}
		}
	}
}

// Management as a source (the review of 2026-10-18): an entry `management -> X` matches what comes in through a
// WireGuard network of the role management too, in both topologies, as explain counts its tunnel subnet and its
// clients as the management network.
func TestAManagementEntryAlsoMatchesTrafficFromAManagementRoleWireGuardNetwork(t *testing.T) {
	entry := `    - from: {management: true}
      to: {network: IoT}
      policy: allow
`
	for _, twoPort := range []bool{false, true} {
		tg := adminWGWith(t, twoPort, entry)
		fwd := renderRules(chainOf(t, tg, "forward"))
		if !slicesContain(fwd, "meta iifname wg-admin meta oifname br-iot accept") {
			t.Errorf("two ports %v: no accept from wg-admin:\n%s", twoPort, strings.Join(fwd, "\n"))
		}
		// it is no device under test: nothing of it returns from the guard
		for _, r := range renderRules(chainOf(t, tg, MgmtGuardChain)) {
			if strings.HasPrefix(r, "meta iifname wg-admin") {
				t.Errorf("two ports %v: a return for the management-role network: %s", twoPort, r)
			}
		}
	}
}
