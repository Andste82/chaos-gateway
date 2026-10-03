package appliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestParseSums(t *testing.T) {
	h := strings.Repeat("a", 64)
	got := ParseSums(h + " *ubuntu-24.04-server-cloudimg-amd64.img\n" + strings.Repeat("b", 64) + "  other.img\nnot a line\n" + "short *x\n")
	if got["ubuntu-24.04-server-cloudimg-amd64.img"] != h || got["other.img"] != strings.Repeat("b", 64) || len(got) != 2 {
		t.Errorf("%v", got)
	}
}

func TestTheReleasesAreTheOnesOfThePlan(t *testing.T) {
	for _, name := range []string{"24.04", "26.04"} {
		r, ok := Releases[name]
		if !ok || r.File != "ubuntu-"+name+"-server-cloudimg-amd64.img" || !strings.HasSuffix(r.BaseURL, "/"+name+"/release/") || !strings.HasPrefix(r.BaseURL, "https://") {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func fakeMirror(t *testing.T, content string) (*httptest.Server, *atomic.Int32, Release) {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	var downloads atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + " *img.img\n"))
	})
	mux.HandleFunc("/img.img", func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte(content))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &downloads, Release{Name: "t", BaseURL: ts.URL + "/", File: "img.img"}
}

func TestFetchDownloadsVerifiesAndCaches(t *testing.T) {
	ts, downloads, rel := fakeMirror(t, "the image")
	dir := t.TempDir()
	p, err := Fetch(context.Background(), ts.Client(), dir, rel)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "the image" {
		t.Errorf("%q", b)
	}
	// a second call uses the cache
	if _, err := Fetch(context.Background(), ts.Client(), dir, rel); err != nil || downloads.Load() != 1 {
		t.Errorf("%v, %d downloads", err, downloads.Load())
	}
	// a corrupted cache file is downloaded again
	if err := os.WriteFile(p, []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), ts.Client(), dir, rel); err != nil || downloads.Load() != 2 {
		t.Errorf("%v, %d downloads", err, downloads.Load())
	}
	if b, _ := os.ReadFile(p); string(b) != "the image" {
		t.Errorf("%q", b)
	}
}

func TestFetchRefusesAWrongChecksumAndKeepsNothing(t *testing.T) {
	ts, _, rel := fakeMirror(t, "the image")
	// the mirror serves other bytes than the sums promise
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte("something else"))
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + " *img.img\n"))
	})
	mux.HandleFunc("/img.img", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("the image")) })
	bad := httptest.NewServer(mux)
	defer bad.Close()
	_ = ts
	rel.BaseURL = bad.URL + "/"
	dir := t.TempDir()
	if _, err := Fetch(context.Background(), bad.Client(), dir, rel); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("%v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files stay after a failed download: %v", ents)
	}
	// an image the sums do not list
	rel.File = "missing.img"
	if _, err := Fetch(context.Background(), bad.Client(), dir, rel); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Errorf("%v", err)
	}
}

func TestTheSeedDocumentsAreValidCloudInit(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.Authorized)); err != nil {
		t.Fatalf("the authorized_keys line: %v", err)
	}
	s := Seed{Hostname: "gw-test", SSHPublicKey: key.Authorized}
	if !strings.HasPrefix(s.UserData(), "#cloud-config\n") {
		t.Error("user-data lacks the #cloud-config header")
	}
	var ud struct {
		Hostname string `yaml:"hostname"`
		Users    []struct {
			Name string   `yaml:"name"`
			Sudo string   `yaml:"sudo"`
			Keys []string `yaml:"ssh_authorized_keys"`
		} `yaml:"users"`
		SSHPwauth bool `yaml:"ssh_pwauth"`
	}
	if err := yaml.Unmarshal([]byte(s.UserData()), &ud); err != nil {
		t.Fatal(err)
	}
	if ud.Hostname != "gw-test" || len(ud.Users) != 1 || ud.Users[0].Name != GuestUser || ud.Users[0].Keys[0] != key.Authorized || ud.SSHPwauth {
		t.Errorf("%+v", ud)
	}
	if !strings.Contains(s.MetaData(), "instance-id: gw-test") {
		t.Errorf("%s", s.MetaData())
	}
}

type netplan struct {
	Version   int `yaml:"version"`
	Ethernets map[string]struct {
		Match struct {
			MAC string `yaml:"macaddress"`
		} `yaml:"match"`
		SetName   string                     `yaml:"set-name"`
		Addresses []string                   `yaml:"addresses"`
		Routes    []struct{ To, Via string } `yaml:"routes"`
		DHCP4     bool                       `yaml:"dhcp4"`
	} `yaml:"ethernets"`
}

func TestTheNetworkConfigIsNetplanForTheThreePortTopology(t *testing.T) {
	var np netplan
	if err := yaml.Unmarshal([]byte(Seed{}.NetworkConfig()), &np); err != nil {
		t.Fatal(err)
	}
	if np.Version != 2 || len(np.Ethernets) != 3 {
		t.Fatalf("%+v", np)
	}
	wan, lan, mgmt := np.Ethernets["wan0"], np.Ethernets["lan0"], np.Ethernets["mgmt0"]
	if wan.Match.MAC != MACs.Uplink || len(wan.Addresses) != 1 || wan.Addresses[0] != GatewayUp+"/24" || len(wan.Routes) != 0 {
		t.Errorf("wan0 %+v", wan)
	}
	// the test port has no address: Chaos Gateway owns it
	if lan.Match.MAC != MACs.LAN || len(lan.Addresses) != 0 || lan.DHCP4 {
		t.Errorf("lan0 %+v", lan)
	}
	// the management interface carries the default route of the main table, as on a real host
	if mgmt.Match.MAC != MACs.Mgmt || mgmt.Addresses[0] != GatewayMgmt+"/24" || len(mgmt.Routes) != 1 || mgmt.Routes[0].Via != HostMgmt {
		t.Errorf("mgmt0 %+v", mgmt)
	}
}

func TestTheTwoPortNetworkConfigPutsManagementBehindTheUplink(t *testing.T) {
	var np netplan
	if err := yaml.Unmarshal([]byte(Seed{TwoPorts: true}.NetworkConfig()), &np); err != nil {
		t.Fatal(err)
	}
	if len(np.Ethernets) != 2 || np.Ethernets["mgmt0"].Match.MAC != "" {
		t.Fatalf("%+v", np)
	}
	wan := np.Ethernets["wan0"]
	if strings.Join(wan.Addresses, ",") != GatewayUp+"/24,"+GatewayMgmt+"/24" || len(wan.Routes) != 1 || wan.Routes[0].Via != HostMgmt {
		t.Errorf("%+v", wan)
	}
}

func TestTheSeedImageIsBuiltFromTheDocuments(t *testing.T) {
	if !have("cloud-localds") && !have("genisoimage") {
		_, err := WriteSeed(context.Background(), t.TempDir(), Seed{})
		if err == nil || !strings.Contains(err.Error(), "cloud-localds") {
			t.Errorf("without a tool the error names the packages: %v", err)
		}
		return
	}
	dir := t.TempDir()
	iso, err := WriteSeed(context.Background(), dir, Seed{Hostname: "x", SSHPublicKey: "ssh-ed25519 AAAA"})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(iso); err != nil || fi.Size() == 0 {
		t.Errorf("%v", err)
	}
	for _, f := range []string{"user-data", "meta-data", "network-config"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
}

func TestQEMUArgs(t *testing.T) {
	nics := []NIC{{Tap: "ap-t0", MAC: MACs.Uplink}, {Tap: "ap-t1", MAC: MACs.LAN}, {Tap: "ap-t2", MAC: MACs.Mgmt}}
	args := strings.Join(QEMUArgs(VMConfig{Disk: "/d.qcow2", Seed: "/s.iso", NICs: nics, SerialLog: "/s.log", KVM: true}), " ")
	for _, want := range []string{"-accel kvm", "-cpu host", "-m 2048", "file=/d.qcow2,if=virtio,format=qcow2", "file=/s.iso", "media=cdrom",
		"-serial file:/s.log", "tap,id=net0,ifname=ap-t0,script=no,downscript=no", "virtio-net-pci,netdev=net2,mac=" + MACs.Mgmt, "-nographic"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in\n%s", want, args)
		}
	}
	if strings.Count(args, "virtio-net-pci") != 3 {
		t.Errorf("%s", args)
	}
	tcg := strings.Join(QEMUArgs(VMConfig{Disk: "/d", Seed: "/s", SerialLog: "/l"}), " ")
	if !strings.Contains(tcg, "-accel tcg") || strings.Contains(tcg, "kvm") {
		t.Errorf("%s", tcg)
	}
}

func TestThePlanBuildsTheThreePortTopologyAndTearsItDownInReverse(t *testing.T) {
	top := Topology{Prefix: "ap1"}
	if err := top.Validate(); err != nil {
		t.Fatal(err)
	}
	p := top.Plan()
	all := strings.Join(cmdStrings(p.Setup), "\n")
	for _, want := range []string{
		"ip link add ap1-up type bridge", "ip link add ap1-lan type bridge", "ip link add ap1-mg type bridge",
		"ip tuntap add dev ap1-t0 mode tap", "ip link set ap1-t1 master ap1-lan", "ip link set ap1-t2 master ap1-mg",
		"ip netns add ap1-server", "ip -n ap1-server addr add " + ServerAddr + "/24 dev eth0",
		"ip netns add ap1-client", "ip -n ap1-client link set eth0 address " + ClientMAC, "ip -n ap1-client route add default via " + GatewayLAN,
		"ip addr add " + HostMgmt + "/24 dev ap1-mg",
		"iptables -t nat -A POSTROUTING -s " + MgmtNetwork,
		"iptables -I FORWARD -i ap1-up -j ACCEPT",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the setup lacks %q", want)
		}
	}
	if n := len(top.NICs()); n != 3 {
		t.Errorf("%d NICs", n)
	}
	// every object that is created is removed, the last created first
	down := cmdStrings(p.Teardown)
	if len(down) == 0 || !strings.HasPrefix(down[0], "iptables -t nat -D POSTROUTING") {
		t.Errorf("the teardown starts with %v", down)
	}
	idx := func(list []string, s string) int {
		for i, l := range list {
			if l == s {
				return i
			}
		}
		return -1
	}
	if idx(down, "ip link del ap1-vs") > idx(down, "ip netns del ap1-server") || idx(down, "ip link del ap1-vs") < 0 {
		t.Errorf("the veth goes before its namespace: %v", down)
	}
	for _, br := range []string{"ap1-up", "ap1-lan", "ap1-mg"} {
		if idx(down, "ip link del "+br) < 0 {
			t.Errorf("bridge %s is not removed", br)
		}
	}
}

func TestTheTwoPortPlanHasNoManagementBridge(t *testing.T) {
	top := Topology{Prefix: "ap2", TwoPorts: true}
	p := top.Plan()
	all := strings.Join(cmdStrings(p.Setup), "\n")
	if strings.Contains(all, "ap2-mg") || strings.Contains(all, "ap2-t2") {
		t.Errorf("a management bridge in the two-port topology:\n%s", all)
	}
	// the host's management address is on the uplink bridge
	if !strings.Contains(all, "ip addr add "+HostMgmt+"/24 dev ap2-up") {
		t.Errorf("%s", all)
	}
	if n := len(top.NICs()); n != 2 {
		t.Errorf("%d NICs", n)
	}
}

func TestNamesStayWithinTheKernelLimit(t *testing.T) {
	if err := (Topology{Prefix: "a-very-long-prefix"}).Validate(); err == nil {
		t.Error("a name longer than 15 characters is accepted")
	}
	if err := (Topology{}).Validate(); err == nil {
		t.Error("an empty prefix is accepted")
	}
}

func cmdStrings(cs []Cmd) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

func TestHostCommandsGetSudoOnlyWhenNotRoot(t *testing.T) {
	h := Host{Sudo: []string{"sudo", "-n"}}
	if got := h.argv(Cmd{"ip", "link"}); strings.Join(got, " ") != "sudo -n ip link" {
		t.Errorf("%v", got)
	}
	h = Host{Sudo: []string{}}
	if got := h.argv(Cmd{"ip", "link"}); strings.Join(got, " ") != "ip link" {
		t.Errorf("%v", got)
	}
}

func TestShellQuoting(t *testing.T) {
	if got := shq(`it's a "path"`); got != `'it'\''s a "path"'` {
		t.Errorf("%s", got)
	}
}
