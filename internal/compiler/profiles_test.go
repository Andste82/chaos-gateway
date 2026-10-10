package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The compiler's side of the profiles (plan M11): a profile expands into one candidate per part, each
// competing in its own family like a fault on the scope it is activated on (plan §2.4). The domain tests
// state the precedence; these tests state what the kernel is given for it.

const (
	profLTEMTU = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
	profFlaky  = "6d7e8f9a-0b1c-4d2e-8f3a-4b5c6d7e8f9a"
)

// addProfile adds a custom profile (a YAML body) to the configuration of the fixture.
func (w *faultWorld) addProfile(id, body string) {
	w.t.Helper()
	doc, err := domain.ParseDocument([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	var p model.Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		w.t.Fatal(err)
	}
	if w.cfg.Profiles == nil {
		m := map[string]model.Profile{}
		w.cfg.Profiles = &m
	}
	(*w.cfg.Profiles)[id] = p
	norm, errs := domain.Normalize(w.cfg)
	if len(errs) != 0 {
		w.t.Fatalf("normalize: %v", errs)
	}
	w.cfg = norm
}

// effectiveOf returns the effective entry of a source and family.
func effectiveOf(t *testing.T, tg *Target, source, family string) EffectiveFault {
	t.Helper()
	for _, e := range tg.Effective {
		if e.Source == source && e.Family == family {
			return e
		}
	}
	t.Fatalf("no effective %s of %s in %+v", family, source, tg.Effective)
	return EffectiveFault{}
}

func hasEffective(tg *Target, source, family string) bool {
	for _, e := range tg.Effective {
		if e.Source == source && e.Family == family {
			return true
		}
	}
	return false
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// Plan M11 "Tests": activating a profile yields the configured parameters. Every built-in profile of the
// catalogue that this build can activate, on one device, compiled to the netem configuration of both
// directions (the values of plan §2.9, one-way).
func TestEveryBuiltinProfileCompilesToItsConfiguredParameters(t *testing.T) {
	type want struct {
		delay, jitter time.Duration
		loss          float64
		rate          int64
		gemodel       *Gemodel
		flapping      *FlapSpec
	}
	for name, w := range map[string]want{
		"lte":            {delay: ms(50), jitter: ms(10), loss: 0.1},
		"bad-lte":        {delay: ms(150), jitter: ms(50), loss: 3, rate: 2_000_000},
		"satellite":      {delay: ms(600), jitter: ms(30), loss: 1},
		"congested-wifi": {delay: ms(30), jitter: ms(20), gemodel: &Gemodel{P: 0.5, R: 24.5, LossBad: 100}},
		"offline":        {loss: 100},
		"intermittent":   {flapping: &FlapSpec{Up: 20 * time.Second, Down: 10 * time.Second}},
	} {
		t.Run(name, func(t *testing.T) {
			fw := newFaultWorld(t)
			o := fw.overlay(fmt.Sprintf(`{target: {device: esp32-42}, profile: %s}`, name), 0)
			tg := fw.compile(nil)
			if tg.HasErrors() {
				t.Fatalf("%+v", tg.Problems)
			}
			if len(tg.Faults) != 1 {
				t.Fatalf("%d faults, want 1: %+v", len(tg.Faults), tg.Faults)
			}
			dev := ""
			if w.rate > 0 {
				dev = devESP42 // a rate is per device (D18)
			}
			f := faultOf(t, tg, o.Id.String(), dev)
			if f.Profile != name {
				t.Errorf("the fault does not name its profile: %q", f.Profile)
			}
			for _, d := range []Direction{Upload, Download} {
				n := f.netem(d)
				if n == nil {
					t.Fatalf("%s is not impaired", d)
				}
				if n.Delay != w.delay || n.Jitter != w.jitter || n.Loss != w.loss || n.Rate != w.rate {
					t.Errorf("%s: %s", d, n.Summary())
				}
				if (n.Gemodel == nil) != (w.gemodel == nil) || (w.gemodel != nil && *n.Gemodel != *w.gemodel) {
					t.Errorf("%s: burst loss %+v, want %+v", d, n.Gemodel, w.gemodel)
				}
				if (n.Flapping == nil) != (w.flapping == nil) || (w.flapping != nil && *n.Flapping != *w.flapping) {
					t.Errorf("%s: flapping %+v, want %+v", d, n.Flapping, w.flapping)
				}
			}
			if e := effectiveOf(t, tg, o.Id.String(), domain.FamilyImpairment); e.Profile != name || e.Layer != "overlay" || e.Scope != "device esp32-42" {
				t.Errorf("%+v", e)
			}
		})
	}
}

// "Normal" impairs nothing, and still wins its family: it takes away the impairment of a scope it is
// more specific than. The preview lists it as the origin ("no impairment").
func TestTheNormalProfileWinsItsFamilyAndImpairsNothing(t *testing.T) {
	w := newFaultWorld(t)
	net := w.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, 0)
	normal := w.overlay(`{target: {device: esp32-42}, profile: normal}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	// esp32-43 and the unknown addresses of the network share the network's id; esp32-42 has none
	if len(tg.Faults) != 1 || tg.Faults[0].Source != net.Id.String() {
		t.Fatalf("%+v", tg.Faults)
	}
	m := findMap(tg, tg.ClassifyMaps["dev"])
	for _, e := range m.Elements {
		if e.Key == "10.10.0.42" && e.Value != MarkChainName(0) {
			t.Errorf("esp32-42 is classified into %s, want no fault", e.Value)
		}
	}
	if e := effectiveOf(t, tg, normal.Id.String(), domain.FamilyImpairment); e.Profile != "normal" || e.Summary != "no impairment" {
		t.Errorf("%+v", e)
	}
	if e := effectiveOf(t, tg, net.Id.String(), domain.FamilyImpairment); e.Profile != "" || e.Summary != "latency 100ms" {
		t.Errorf("%+v", e)
	}
}

// Plan M11 "Tests": switching profiles yields the configured parameters. Replacing the activation on a
// scope (the same overlay, another profile) changes the parameters and, while the queue kind stays, the
// fault id: the leaf is changed in place. Going to a profile with a rate takes a queue per device.
func TestSwitchingTheProfileOfAnActivationYieldsTheNewParameters(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {network: IoT}, profile: lte}`, 0)
	first := w.compile(nil)
	lte := faultOf(t, first, o.Id.String(), "")

	switchTo := func(name string, age time.Duration) *Target {
		t.Helper()
		id, _, _, ok := domain.ProfileRef(w.cfg, name)
		if !ok {
			t.Fatalf("no profile %s", name)
		}
		w.overlays[0].Profile = &id
		w.overlays[0].UpdatedAt = tFault0.Add(age)
		tg := w.compile(nil)
		if tg.HasErrors() {
			t.Fatalf("%+v", tg.Problems)
		}
		return tg
	}

	sat := faultOf(t, switchTo("satellite", time.Second), o.Id.String(), "")
	if sat.ID != lte.ID || sat.Upload.Delay != ms(600) || sat.Upload.Jitter != ms(30) || sat.Upload.Loss != 1 || sat.Profile != "satellite" {
		t.Errorf("lte %d -> satellite %d: %s", lte.ID, sat.ID, sat.Upload.Summary())
	}

	bad := switchTo("bad-lte", 2*time.Second)
	if len(bad.Faults) != 3 { // esp32-42, esp32-43 and the addresses of the network no device owns
		t.Fatalf("%d faults, want one queue per device: %+v", len(bad.Faults), bad.Faults)
	}
	for _, d := range []string{devESP42, devESP43, ""} {
		f := faultOf(t, bad, o.Id.String(), d)
		if f.Upload.Rate != 2_000_000 || f.Download.Rate != 2_000_000 || f.Upload.Delay != ms(150) || f.Profile != "bad-lte" {
			t.Errorf("device %q: %s", d, f.Upload.Summary())
		}
	}

	off := switchTo("offline", 3*time.Second)
	if len(off.Faults) != 1 || off.Faults[0].Upload.Loss != 100 || off.Faults[0].Profile != "offline" {
		t.Errorf("%+v", off.Faults)
	}
	back := faultOf(t, switchTo("lte", 4*time.Second), o.Id.String(), "")
	if back.Upload.Delay != ms(50) || back.Upload.Rate != 0 || back.Profile != "lte" {
		t.Errorf("%s", back.Upload.Summary())
	}
}

// Plan M11 "Tests", first bullet of precedence: a device fault overrides the network profile's
// impairment part (same layer). The device with the fault gets the fault alone - no merging, so not the
// profile's rate or loss - and every other device of the network gets the profile.
func TestADeviceFaultOverridesTheNetworkProfilesImpairmentPart(t *testing.T) {
	w := newFaultWorld(t)
	prof := w.overlay(`{target: {network: IoT}, profile: bad-lte}`, 0)
	fault := w.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := faultOf(t, tg, fault.Id.String(), "")
	if f.Upload.Delay != ms(300) || f.Upload.Loss != 0 || f.Upload.Rate != 0 || f.Upload.Jitter != 0 || f.Profile != "" {
		t.Errorf("esp32-42: %s", f.Upload.Summary())
	}
	b := faultOf(t, tg, prof.Id.String(), devESP43)
	if b.Upload.Delay != ms(150) || b.Upload.Rate != 2_000_000 || b.Profile != "bad-lte" {
		t.Errorf("esp32-43: %s", b.Upload.Summary())
	}
	// the profile has no queue for esp32-42, and the classification says so
	for _, f := range tg.Faults {
		if f.Source == prof.Id.String() && f.Device == devESP42 {
			t.Errorf("the overridden profile part keeps a queue for esp32-42: %+v", f)
		}
	}
	m := findMap(tg, tg.ClassifyMaps["dev"])
	for _, e := range m.Elements {
		switch e.Key {
		case "10.10.0.42":
			if e.Value != MarkChainName(f.ID) {
				t.Errorf("esp32-42 is classified into %s, want the fault's %s", e.Value, MarkChainName(f.ID))
			}
		case "10.10.0.43":
			if e.Value != MarkChainName(b.ID) {
				t.Errorf("esp32-43 is classified into %s, want the profile's %s", e.Value, MarkChainName(b.ID))
			}
		}
	}
}

// Plan M11 "Tests": a fault on the same scope replaces only the profile part of its family; the other
// families of the profile stay active. The profile has an impairment and an MTU part on the device; an
// impairment fault on the device wins the impairment family, and the MTU part still lowers the MTU.
func TestAFaultOnTheSameScopeReplacesOnlyTheProfilePartOfItsFamily(t *testing.T) {
	w := newFaultWorld(t)
	w.addProfile(profLTEMTU, `{name: lte-small-mtu, parts: {impairment: {latency: 50ms, jitter: 10ms, loss: 0.1%}, mtu: {size: 1400, mode: mss_clamp}}}`)
	prof := w.overlay(`{target: {device: esp32-42}, profile: lte-small-mtu}`, 2*time.Second) // the newer of the two
	fault := w.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := faultOf(t, tg, fault.Id.String(), "")
	if f.Upload.Delay != ms(300) || f.Upload.Loss != 0 {
		t.Errorf("impairment: %s", f.Upload.Summary())
	}
	if hasEffective(tg, prof.Id.String(), domain.FamilyImpairment) {
		t.Errorf("the impairment part of the profile still wins: %+v", tg.Effective)
	}
	mtu := effectiveOf(t, tg, prof.Id.String(), domain.FamilyMTU)
	if mtu.Profile != "lte-small-mtu" || mtu.Summary != "mtu 1400 (mss_clamp)" {
		t.Errorf("%+v", mtu)
	}
	if len(tg.PMTU) != 1 || tg.PMTU[0].Source != prof.Id.String() || tg.PMTU[0].Profile != "lte-small-mtu" || tg.PMTU[0].Size != 1400 {
		t.Errorf("the MTU part of the profile is not compiled: %+v", tg.PMTU)
	}
	// and the other way round: an MTU fault on the device replaces only the MTU part
	w2 := newFaultWorld(t)
	w2.addProfile(profLTEMTU, `{name: lte-small-mtu, parts: {impairment: {latency: 50ms}, mtu: {size: 1400, mode: mss_clamp}}}`)
	p2 := w2.overlay(`{target: {device: esp32-42}, profile: lte-small-mtu}`, 0)
	mf := w2.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1200, mode: blackhole}}}`, time.Second)
	tg2 := w2.compile(nil)
	if tg2.HasErrors() {
		t.Fatalf("%+v", tg2.Problems)
	}
	if len(tg2.PMTU) != 1 || tg2.PMTU[0].Source != mf.Id.String() || tg2.PMTU[0].Size != 1200 {
		t.Errorf("the MTU fault does not win: %+v", tg2.PMTU)
	}
	if got := faultOf(t, tg2, p2.Id.String(), "").Upload.Delay; got != ms(50) {
		t.Errorf("the impairment part of the profile was lost: %v", got)
	}
}

// Plan M11 "Tests": an overlay profile beats a configuration fault, however specific the fault is (D24).
func TestAnOverlayProfileBeatsAConfigurationFault(t *testing.T) {
	w := newFaultWorld(t)
	w.addConfigFault("7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b", `{name: device-20, source: {device: esp32-42}, latency: 20ms}`, 0)
	prof := w.overlay(`{target: {network: IoT}, profile: satellite}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 1 {
		t.Fatalf("the configuration fault is still in the kernel: %+v", tg.Faults)
	}
	f := faultOf(t, tg, prof.Id.String(), "")
	if f.Layer != "overlay" || f.Profile != "satellite" || f.Upload.Delay != ms(600) {
		t.Errorf("%+v", f)
	}
	// once the activation is gone, the configuration fault applies again
	w.overlays = nil
	tg = w.compile(nil)
	if len(tg.Faults) != 1 || tg.Faults[0].Layer != "config" || tg.Faults[0].Upload.Delay != ms(20) {
		t.Errorf("%+v", tg.Faults)
	}
}

// E8 (plan §2.4): on the same scope the fault beats the profile part, whichever was activated last.
func TestE8AFaultBeatsTheProfilePartOnTheSameScopeInTheCompiledTarget(t *testing.T) {
	w := newFaultWorld(t)
	fault := w.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, 0)
	prof := w.overlay(`{target: {device: esp32-42}, profile: bad-lte}`, time.Minute)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 1 || tg.Faults[0].Source != fault.Id.String() || tg.Faults[0].Upload.Delay != ms(300) || tg.Faults[0].Upload.Rate != 0 {
		t.Errorf("%+v", tg.Faults)
	}
	if hasEffective(tg, prof.Id.String(), domain.FamilyImpairment) {
		t.Error("the profile part wins")
	}
	// a fault that selects only a port leaves the profile for the rest of the traffic
	w2 := newFaultWorld(t)
	w2.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`, 0)
	prof2 := w2.overlay(`{target: {device: esp32-42}, profile: lte}`, time.Second)
	tg2 := w2.compile(nil)
	if tg2.HasErrors() {
		t.Fatalf("%+v", tg2.Problems)
	}
	if !hasEffective(tg2, prof2.Id.String(), domain.FamilyImpairment) || len(tg2.Faults) != 2 {
		t.Errorf("the profile does not apply to the traffic the port fault does not select: %+v", tg2.Effective)
	}
}

// A profile that is defined in the configuration is activated by name or UUID, and its activation follows the
// definition: the same overlay, a new revision of the profile, new parameters in the kernel (plan §2.1.1).
func TestAnActivationFollowsTheNewDefinitionOfItsProfile(t *testing.T) {
	w := newFaultWorld(t)
	w.addProfile(profFlaky, `{name: flaky, parts: {impairment: {latency: 100ms, loss: 2%}}}`)
	o := w.overlay(`{target: {group: sensors}, profile: flaky}`, 0)
	before := faultOf(t, w.compile(nil), o.Id.String(), "")
	if before.Upload.Delay != ms(100) || before.Upload.Loss != 2 || before.Profile != "flaky" {
		t.Fatalf("%s", before.Upload.Summary())
	}
	w.addProfile(profFlaky, `{name: flaky, parts: {impairment: {upload: {latency: 300ms}, download: {latency: 20ms, rate: 1Mbit}}}}`)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := faultOf(t, tg, o.Id.String(), devESP42) // the download has a rate: a queue per device
	if f.Upload.Delay != ms(300) || f.Upload.Rate != 0 || f.Upload.Loss != 0 || f.Download.Delay != ms(20) || f.Download.Rate != 1_000_000 {
		t.Fatalf("upload %s, download %s", f.Upload.Summary(), f.Download.Summary())
	}
	// a profile that is gone from the configuration activates nothing (the engine removes the activation first)
	delete(*w.cfg.Profiles, profFlaky)
	if tg := w.compile(nil); len(tg.Faults) != 0 {
		t.Errorf("%+v", tg.Faults)
	}
}

// DNS and TLS profiles arrive with M20 and M21: their parts are not compiled and break nothing (the
// engine refuses the activation; a target built from an older overlay still compiles).
func TestAProfileWithOnlyALaterFamilyCompilesToNothing(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, profile: dns-broken}`, 0)
	w.overlay(`{target: {network: Lab}, profile: tls-broken}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() || len(tg.Faults) != 0 || len(tg.PMTU) != 0 || len(tg.Effective) != 0 {
		t.Errorf("%+v %+v", tg.Problems, tg.Faults)
	}
	// a profile with an impairment and a DNS part compiles the impairment part
	w.addProfile(profFlaky, `{name: slow-dns-lte, parts: {impairment: {latency: 50ms}, dns: {action: delay, delay: 2s}}}`)
	o := w.overlay(`{target: {device: lab-host}, profile: slow-dns-lte}`, 2*time.Second)
	tg = w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if f := faultOf(t, tg, o.Id.String(), ""); f.Upload.Delay != ms(50) || f.Profile != "slow-dns-lte" {
		t.Errorf("%+v", f)
	}
}

// ---- goldens -----------------------------------------------------------------------------------

// describeEffective is the readable form of the effective faults of a target and of the map that
// classifies the devices: which fault or profile part each device is given.
func describeEffective(tg *Target) string {
	var b strings.Builder
	b.WriteString("# effective\n")
	for _, e := range tg.Effective {
		origin := "-"
		if e.Profile != "" {
			origin = "profile " + e.Profile
		}
		fmt.Fprintf(&b, "%s  %s  %s  %q  %s\n", e.Family, e.Layer, origin, e.Scope, e.Summary)
	}
	return b.String()
}

// scenarioProfilePrecedence is the golden scenario of the profile precedence (E8 and the bullets of plan
// M11): a network profile with a device fault above it, a profile under a fault of the same scope, a
// configuration fault under an overlay profile, and a profile on a network under all of them. With an MTU
// part the profile of the second case has two families, and the fault replaces only one of them; the MTU
// family compiles into the second lookup of the classification (jump), so that variant is a scenario of the
// nftables gate and not of the impairment scenarios.
func scenarioProfilePrecedence(t *testing.T, withMTU bool) *Target {
	t.Helper()
	w := newFaultWorld(t)
	parts := `impairment: {latency: 50ms, jitter: 10ms, loss: 0.1%}`
	if withMTU {
		parts += `, mtu: {size: 1400, mode: mss_clamp}`
	}
	w.addProfile(profLTEMTU, `{name: lte-small-mtu, parts: {`+parts+`}}`)
	w.addConfigFault("7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b", `{name: lab-20, source: {device: lab-host}, latency: 20ms}`, 0)
	w.overlay(`{target: {network: IoT}, profile: bad-lte}`, 0)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, profile: lte-small-mtu}`, 2*time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {loss: 7%}}`, 3*time.Second)
	w.overlay(`{target: {network: Lab}, profile: satellite}`, 4*time.Second)
	return w.compile(nil)
}

// scenarioBuiltinProfiles activates the built-in profiles that this build can activate, each on a scope of
// its own, the catalogue except lte, which the other tests cover: every kind of netem leaf the catalogue needs (a per-device rate, burst loss, a flapping
// leaf, a blackout, plain delay with jitter and loss) side by side on the interfaces of the tree.
func scenarioBuiltinProfiles(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, profile: bad-lte}`, 0)
	w.overlay(`{target: {device: esp32-43}, profile: intermittent}`, time.Second)
	w.overlay(`{target: {device: lab-host}, profile: congested-wifi}`, 2*time.Second)
	w.overlay(`{target: {network: Lab}, profile: offline}`, 3*time.Second)
	return w.compile(nil)
}

func TestGoldenProfilePrecedence(t *testing.T) {
	tg := scenarioProfilePrecedence(t, true)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	// the tc tree and the maps of the same target are the golden file faults-profiles
	goldenText(t, "profiles-effective", describeEffective(tg))
	// the facts the golden file shows, stated: the overlay profile beats the configuration fault of lab-host
	// (D24), esp32-43 keeps the MTU part of its profile under its own loss fault, esp32-42 is not rate limited
	for _, f := range tg.Faults {
		if f.Layer == "config" {
			t.Errorf("a configuration fault is in the kernel: %+v", f)
		}
		if f.Device == devESP42 && f.Upload.Rate != 0 {
			t.Errorf("esp32-42 is rate limited by the overridden profile: %+v", f)
		}
	}
	var mtu bool
	for _, e := range tg.Effective {
		mtu = mtu || (e.Family == domain.FamilyMTU && e.Profile == "lte-small-mtu")
	}
	if !mtu {
		t.Error("the MTU part of the profile is not effective")
	}
}

func TestProfileOverlaysAreStableAcrossCompiles(t *testing.T) {
	a, b := scenarioProfilePrecedence(t, true), scenarioProfilePrecedence(t, true)
	if describeEffective(a)+describeFaults(a) != describeEffective(b)+describeFaults(b) {
		t.Error("two compiles of the same world differ")
	}
}
