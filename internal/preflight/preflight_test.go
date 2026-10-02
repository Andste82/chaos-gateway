package preflight

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRequiredModulesMatchThePlan(t *testing.T) {
	// plan §3.4: sch_netem, sch_htb, cls_fw, cls_u32, cls_flower, act_mirred, ifb, nf_conntrack,
	// nf_tables with NAT/ct/dup/reject, veth, bridge, wireguard (M4b), later 8021q.
	want := []string{
		"sch_netem", "sch_htb", "cls_fw", "cls_u32", "cls_flower", "act_mirred", "ifb",
		"nf_conntrack", "nf_tables", "nft_ct", "nft_nat", "nft_reject", "nft_dup_netdev",
		"veth", "bridge", "wireguard",
	}
	have := map[string]bool{}
	for _, m := range Required() {
		have[m.Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("required module %s missing from the shared list", name)
		}
	}
	if have["8021q"] {
		t.Error("8021q is needed only after V1 and must not be required")
	}
	found := false
	for _, m := range Modules() {
		if m.Name == "8021q" && m.Later {
			found = true
		}
	}
	if !found {
		t.Error("8021q must be listed as a later module")
	}
}

func TestModuleListHasNoDuplicatesAndEveryEntryIsDescribed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Modules() {
		if seen[m.Name] {
			t.Errorf("duplicate module %s", m.Name)
		}
		seen[m.Name] = true
		if m.Feature == "" || m.Milestone == "" {
			t.Errorf("module %s needs a feature and a milestone", m.Name)
		}
		if strings.ContainsAny(m.Name, "- ") {
			t.Errorf("module name %q must use underscores", m.Name)
		}
	}
}

func TestModulesReturnsACopy(t *testing.T) {
	a := Modules()
	a[0].Name = "changed"
	if Modules()[0].Name == "changed" {
		t.Fatal("Modules must return a copy")
	}
}

func TestParseRelease(t *testing.T) {
	tests := []struct {
		in      string
		want    Release
		wantErr bool
	}{
		{"6.8.0-142-generic", Release{6, 8}, false},
		{"7.0.0-38-generic\n", Release{7, 0}, false},
		{"6.18.44", Release{6, 18}, false},
		{"6.1-rc3", Release{6, 1}, false},
		{"", Release{}, true},
		{"six.eight", Release{}, true},
		{"6", Release{}, true},
	}
	for _, tt := range tests {
		got, err := ParseRelease(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseRelease(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestCheckKernel(t *testing.T) {
	for _, ok := range []string{"6.8.0-142-generic", "6.9.1", "7.0.0-38-generic", "6.18.44"} {
		if err := CheckKernel(ok); err != nil {
			t.Errorf("CheckKernel(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"6.1.0-13-amd64", "5.15.0", "garbage"} {
		if err := CheckKernel(bad); err == nil {
			t.Errorf("CheckKernel(%q) must fail", bad)
		}
	}
}

func testEnv() Env {
	return Env{
		Release: "6.8.0-142-generic",
		FS: fstest.MapFS{
			"lib/modules/6.8.0-142-generic/modules.dep": {Data: []byte(
				"kernel/net/sched/sch_netem.ko.zst:\n" +
					"kernel/net/sched/sch_htb.ko.zst:\n" +
					"kernel/net/netfilter/nft_chain_nat.ko.zst: kernel/net/netfilter/nf_tables.ko.zst\n" +
					"kernel/drivers/net/wireguard/wireguard.ko.zst: kernel/lib/crypto/libchacha.ko.zst\n")},
			"lib/modules/6.8.0-142-generic/modules.builtin": {Data: []byte(
				"kernel/net/bridge/bridge.ko\n")},
			"sys/module/veth/refcnt":      {Data: []byte("0")},
			"sys/module/nf_tables/refcnt": {Data: []byte("1")},
		},
	}
}

func TestCheckModulesStatuses(t *testing.T) {
	reports := CheckModules(testEnv(), []Module{
		{Name: "sch_netem"}, // on disk only
		{Name: "veth"},      // loaded (sysfs)
		{Name: "bridge"},    // built into the kernel
		{Name: "nft_chain_nat"},
		{Name: "cls_fw"},    // nowhere
		{Name: "nf-tables"}, // dash spelling of a loaded module
	})
	want := map[string]Status{
		"sch_netem": Available, "veth": Loaded, "bridge": Loaded,
		"nft_chain_nat": Available, "cls_fw": Missing, "nf-tables": Loaded,
	}
	for _, r := range reports {
		if r.Status != want[r.Name] {
			t.Errorf("%s: %v, want %v", r.Name, r.Status, want[r.Name])
		}
	}
}

func TestCheckModulesWithoutModuleFilesReportsMissing(t *testing.T) {
	// a minimal kernel without /lib/modules, like a sandbox VM
	reports := CheckModules(Env{FS: fstest.MapFS{}, Release: "6.18.44"}, []Module{{Name: "sch_netem"}})
	if reports[0].Status != Missing {
		t.Fatalf("status = %v", reports[0].Status)
	}
}

func TestMissingModulesIgnoresLaterModules(t *testing.T) {
	env := Env{FS: fstest.MapFS{}, Release: "6.8.0"}
	miss := MissingModules(CheckModules(env, Modules()))
	for _, m := range miss {
		if m.Later {
			t.Errorf("later module %s reported as missing", m.Name)
		}
	}
	if len(miss) != len(Required()) {
		t.Fatalf("%d missing, want all %d required", len(miss), len(Required()))
	}
}

func TestStatusString(t *testing.T) {
	if Loaded.String() != "loaded" || Available.String() != "available" || Missing.String() != "missing" {
		t.Fatal("unexpected status names")
	}
}

func TestReportOKAndProblems(t *testing.T) {
	good := Report{Release: "6.8.0", Modules: CheckModules(Env{
		FS: fstest.MapFS{"sys/module/x/y": {}}, Release: "6.8.0"}, []Module{{Name: "x"}})}
	if !good.OK() || len(good.Problems()) != 0 {
		t.Fatalf("expected OK, got %v", good.Problems())
	}
	if !strings.Contains(good.String(), "ok: the namespace testbed can run here") {
		t.Fatalf("report: %s", good)
	}

	bad := Report{
		Release:      "5.15.0",
		KernelErr:    errors.New("too old"),
		Modules:      CheckModules(Env{FS: fstest.MapFS{}, Release: "5.15.0"}, []Module{{Name: "sch_netem"}}),
		MissingTools: []string{"tc"},
		NamespaceErr: errors.New("operation not permitted"),
	}
	if bad.OK() {
		t.Fatal("expected not OK")
	}
	joined := strings.Join(bad.Problems(), "\n")
	for _, want := range []string{"kernel: too old", "sch_netem", "tools missing: tc", "operation not permitted"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
}

func TestModprobeArgs(t *testing.T) {
	got := ModprobeArgs([]Module{{Name: "sch_netem"}, {Name: "veth"}})
	want := []string{"-a", "-q", "sch_netem", "veth"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v", got)
	}
}

func TestRunOnTheHost(t *testing.T) {
	r, err := Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Release == "" {
		t.Fatal("no kernel release")
	}
	if len(r.Modules) != len(Modules()) {
		t.Fatalf("%d module reports, want %d", len(r.Modules), len(Modules()))
	}
	// OK must be consistent with Problems, whatever machine this runs on
	if r.OK() != (len(r.Problems()) == 0) {
		t.Fatalf("OK() = %v but problems = %v", r.OK(), r.Problems())
	}
}
