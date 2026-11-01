package preflight

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
)

const hostRelease = "6.8.0-142-generic"

// hostFS is a machine on which every check passes; edit adds or removes files.
func hostFS(edit func(fstest.MapFS)) fstest.MapFS {
	fsys := fstest.MapFS{
		"proc/sys/net/ipv4/ip_forward":       {Data: []byte("1\n")},
		"proc/sys/net/ipv4/ping_group_range": {Data: []byte("0 2147483647\n")},
		"proc/self/mountinfo": {Data: []byte("" +
			"24 1 0:21 / /proc rw,nosuid - proc proc rw\n" +
			"30 24 0:21 /sys /proc/sys rw,nosuid - proc proc rw\n" +
			"41 24 8:1 /run/netns /run/netns rw,nosuid shared:7 - tmpfs tmpfs rw\n")},
		"lib/modules/" + hostRelease + "/modules.dep": {Data: nil},
	}
	for _, m := range Required() {
		fsys["sys/module/"+m.Name+"/x"] = &fstest.MapFile{}
	}
	if edit != nil {
		edit(fsys)
	}
	return fsys
}

// hostRun records the commands of a scripted machine; answers maps "tool args" to its output, a value starting with "!" is a
// failure with that output.
type hostRun struct {
	answers map[string]string
	ran     []string
}

func (h *hostRun) run(_ context.Context, tool string, args ...string) (string, error) {
	cmd := tool + " " + strings.Join(args, " ")
	h.ran = append(h.ran, strings.TrimSpace(cmd))
	for prefix, out := range h.answers {
		if strings.HasPrefix(cmd, prefix) {
			if strings.HasPrefix(out, "!") {
				return out[1:], errors.New("exit status 1")
			}
			return out, nil
		}
	}
	return "", nil
}

func versions(extra map[string]string) *hostRun {
	h := &hostRun{answers: map[string]string{
		"ip -V":             "ip utility, iproute2-6.1.0\n",
		"tc -V":             "tc utility, iproute2-6.1.0\n",
		"nft --version":     "nftables v1.0.9 (Old Doris #3)\n",
		"ethtool --version": "ethtool version 6.7\n",
		"conntrack -V":      "conntrack v1.4.8 (conntrack-tools)\n",
		"iptables -V":       "iptables v1.8.10 (nf_tables)\n",
		"wg --version":      "wireguard-tools v1.0.20210914\n",
		"bird --version":    "BIRD version 2.18\n",
		"birdc --version":   "BIRD client version 2.18\n",
		"nft list tables":   "table inet chaosgw\ntable ip filter\ntable ip nat\ntable ip6 filter\n",
		"iptables -w 5 -S":  "-P INPUT ACCEPT\n-P FORWARD DROP\n-N DOCKER-USER\n",
	}}
	for k, v := range extra {
		h.answers[k] = v
	}
	return h
}

func probe(fsys fstest.MapFS, h *hostRun, dirs ...DataDir) HostProbe {
	return HostProbe{FS: fsys, Release: hostRelease, HasTool: func(string) bool { return true }, Run: h.run, DataDirs: dirs}
}

func byID(t *testing.T, checks []HostCheck, id string) HostCheck {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %v", id, checks)
	return HostCheck{}
}

func TestEveryCheckPassesOnAPreparedHost(t *testing.T) {
	checks := HostChecks(context.Background(), probe(hostFS(nil), versions(nil)))
	for _, c := range checks {
		if c.Status != "ok" {
			t.Errorf("%s is %s: %s (%s)", c.ID, c.Status, c.Message, c.Fix)
		}
	}
	for _, id := range []string{"host.tools", "host.modules", "host.ip_forward", "host.ping_group_range", "host.proc_sys", "host.netns", "host.firewall"} {
		byID(t, checks, id)
	}
	if got := byID(t, checks, "host.tools").Message; !strings.Contains(got, "nftables v1.0.9") || !strings.Contains(got, "BIRD version 2.18") || !strings.Contains(got, "iproute2-6.1.0") {
		t.Errorf("the tool versions are not in the message: %q", got)
	}
}

func TestAMissingToolIsAFailureThatNamesIt(t *testing.T) {
	p := probe(hostFS(nil), versions(nil))
	p.HasTool = func(tool string) bool { return tool != "conntrack" && tool != "wg" }
	c := byID(t, HostChecks(context.Background(), p), "host.tools")
	if c.Status != "fail" || !strings.Contains(c.Message, "conntrack, wg") || c.Fix == "" {
		t.Errorf("%+v", c)
	}
}

func TestTheLegacyIptablesBackendIsWarnedAbout(t *testing.T) {
	h := versions(map[string]string{"iptables -V": "iptables v1.8.10 (legacy)\n"})
	checks := HostChecks(context.Background(), probe(hostFS(nil), h))
	if c := byID(t, checks, "host.iptables"); c.Status != "warn" || !strings.Contains(c.Message, "DOCKER-USER") || !strings.Contains(c.Fix, "iptables-nft") {
		t.Errorf("%+v", c)
	}
}

func TestForwardingAndThePingSocketAreWarnedAboutWithTheFix(t *testing.T) {
	fsys := hostFS(func(m fstest.MapFS) {
		m["proc/sys/net/ipv4/ip_forward"] = &fstest.MapFile{Data: []byte("0\n")}
		m["proc/sys/net/ipv4/ping_group_range"] = &fstest.MapFile{Data: []byte("1\t0\n")}
	})
	checks := HostChecks(context.Background(), probe(fsys, versions(nil)))
	for _, id := range []string{"host.ip_forward", "host.ping_group_range"} {
		if c := byID(t, checks, id); c.Status != "warn" || !strings.Contains(c.Fix, "host-setup") {
			t.Errorf("%+v", c)
		}
	}
}

// The kernel allows the unprivileged ICMP socket to the groups inside the range, and the ping of the diagnostics runs as the group of
// the API container: a range that is on but does not hold it is as good as off.
func TestThePingRangeMustHoldTheGroupOfTheAPIContainer(t *testing.T) {
	for rng, want := range map[string]string{
		"0\t2147483647\n": "ok",
		"0 65532\n":       "ok",
		"65532 65532\n":   "ok",
		"0 2000\n":        "warn",
		"65533 70000\n":   "warn",
		"1\t0\n":          "warn",
	} {
		fsys := hostFS(func(m fstest.MapFS) { m["proc/sys/net/ipv4/ping_group_range"] = &fstest.MapFile{Data: []byte(rng)} })
		c := byID(t, HostChecks(context.Background(), probe(fsys, versions(nil))), "host.ping_group_range")
		if c.Status != want || (want == "warn" && !strings.Contains(c.Fix, "host-setup")) {
			t.Errorf("range %q: %+v, want %s", rng, c, want)
		}
	}
}

func TestMissingModulesAreLoadedAndTheOnesThatCannotBeHadFail(t *testing.T) {
	dep := "kernel/net/sched/sch_netem.ko: \nkernel/net/sched/sch_htb.ko: \n"
	fsys := hostFS(func(m fstest.MapFS) {
		delete(m, "sys/module/sch_netem/x")
		delete(m, "sys/module/sch_htb/x")
		m["lib/modules/"+hostRelease+"/modules.dep"] = &fstest.MapFile{Data: []byte(dep)}
	})
	h := versions(nil)
	p := probe(fsys, h)
	inner := p.Run
	p.Run = func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "modprobe" { // the kernel loads what modprobe is asked for
			for _, a := range args[2:] {
				fsys["sys/module/"+a+"/x"] = &fstest.MapFile{}
			}
		}
		return inner(ctx, tool, args...)
	}
	c := byID(t, HostChecks(context.Background(), p), "host.modules")
	if c.Status != "ok" || !strings.Contains(c.Message, "sch_netem") || !strings.Contains(c.Message, "sch_htb") {
		t.Errorf("modules that can be loaded: %+v", c)
	}
	if !slices.Contains(h.ran, "modprobe -a -q sch_netem sch_htb") {
		t.Errorf("modprobe was not asked for the two modules at once: %v", h.ran)
	}

	// a module that is not on the disk cannot be had, and without modprobe nothing can be loaded
	fsys = hostFS(func(m fstest.MapFS) {
		delete(m, "sys/module/ifb/x")
		delete(m, "sys/module/sch_netem/x")
		m["lib/modules/"+hostRelease+"/modules.dep"] = &fstest.MapFile{Data: []byte("kernel/net/sched/sch_netem.ko: \n")}
	})
	p = probe(fsys, versions(nil))
	p.HasTool = func(tool string) bool { return tool != "modprobe" }
	c = byID(t, HostChecks(context.Background(), p), "host.modules")
	if c.Status != "fail" || !strings.Contains(c.Message, "ifb") || !strings.Contains(c.Message, "sch_netem") || !strings.Contains(c.Fix, "linux-modules-extra") {
		t.Errorf("modules that cannot be had: %+v", c)
	}
}

func TestAReadOnlyProcSysIsAFailure(t *testing.T) {
	fsys := hostFS(func(m fstest.MapFS) {
		m["proc/self/mountinfo"] = &fstest.MapFile{Data: []byte("30 24 0:21 /sys /proc/sys ro,nosuid - proc proc rw\n")}
	})
	c := byID(t, HostChecks(context.Background(), probe(fsys, versions(nil))), "host.proc_sys")
	if c.Status != "fail" || !strings.Contains(c.Fix, "privileged") {
		t.Errorf("%+v", c)
	}
}

func TestNamespacesAreTriedAndTheMountOfRunNetnsMustBeShared(t *testing.T) {
	h := versions(nil)
	HostChecks(context.Background(), probe(hostFS(nil), h))
	var add, del bool
	for _, c := range h.ran {
		add = add || strings.HasPrefix(c, "ip netns add pfchk-")
		del = del || strings.HasPrefix(c, "ip netns del pfchk-")
	}
	if !add || !del {
		t.Errorf("the check must make and remove a throw-away namespace: %v", h.ran)
	}

	// a namespace of an earlier check that was never removed (the executor was killed) goes before the new one is made, and only
	// a name of that form
	left := versions(map[string]string{"ip netns list": "cgsvc (id: 0)\npfchk-0123abcd (id: 3)\npfchk-zzzz\n"})
	HostChecks(context.Background(), probe(hostFS(nil), left))
	var dels []string
	for _, c := range left.ran {
		if strings.HasPrefix(c, "ip netns del ") {
			dels = append(dels, strings.TrimPrefix(c, "ip netns del "))
		}
	}
	if len(dels) != 2 || dels[0] != "pfchk-0123abcd" || !strings.HasPrefix(dels[1], "pfchk-") {
		t.Errorf("the leftover is removed first, then the check's own: %v", dels)
	}

	fsys := hostFS(func(m fstest.MapFS) {
		m["proc/self/mountinfo"] = &fstest.MapFile{Data: []byte("41 24 8:1 /run/netns /run/netns rw,nosuid - tmpfs tmpfs rw\n")}
	})
	if c := byID(t, HostChecks(context.Background(), probe(fsys, versions(nil))), "host.netns"); c.Status != "warn" || !strings.Contains(c.Fix, "rshared") {
		t.Errorf("a mount that is not shared: %+v", c)
	}
	// the executor on the host itself has no mount of its own at /run/netns
	fsys = hostFS(func(m fstest.MapFS) { delete(m, "proc/self/mountinfo") })
	if c := byID(t, HostChecks(context.Background(), probe(fsys, versions(nil))), "host.netns"); c.Status != "ok" {
		t.Errorf("no mount information: %+v", c)
	}

	failing := versions(map[string]string{"ip netns add": "!mount --make-shared /run/netns failed: Operation not permitted\n"})
	c := byID(t, HostChecks(context.Background(), probe(hostFS(nil), failing)), "host.netns")
	if c.Status != "fail" || !strings.Contains(c.Message, "Operation not permitted") {
		t.Errorf("a namespace that cannot be made: %+v", c)
	}
}

func TestOtherFirewallsAreReportedAndDockersTablesAreNot(t *testing.T) {
	for name, tc := range map[string]struct {
		answers map[string]string
		want    string
	}{
		"firewalld":                    {map[string]string{"nft list tables": "table inet chaosgw\ntable inet firewalld\ntable ip filter\n"}, "inet firewalld"},
		"a table of the administrator": {map[string]string{"nft list tables": "table inet chaosgw\ntable inet corp_rules\n"}, "inet corp_rules"},
		"ufw":                          {map[string]string{"iptables -w 5 -S": "-P FORWARD DROP\n-N ufw-before-input\n"}, "ufw"},
	} {
		t.Run(name, func(t *testing.T) {
			c := byID(t, HostChecks(context.Background(), probe(hostFS(nil), versions(tc.answers))), "host.firewall")
			if c.Status != "warn" || !strings.Contains(c.Message, tc.want) || strings.Contains(c.Message, "chaosgw") || c.Fix == "" {
				t.Errorf("%+v", c)
			}
		})
	}
	// the netdev table of an active duplication fault (executor.NftDupTable) is the gateway's own
	h := versions(map[string]string{"nft list tables": "table ip docker-bridges\ntable inet chaosgw\ntable netdev chaosgw_dup\ntable ip nat\n"})
	if c := byID(t, HostChecks(context.Background(), probe(hostFS(nil), h)), "host.firewall"); c.Status != "ok" {
		t.Errorf("Docker's and the gateway's own tables are no conflict: %+v", c)
	}
	h = versions(map[string]string{"nft list tables": "!Operation not permitted\n"})
	if c := byID(t, HostChecks(context.Background(), probe(hostFS(nil), h)), "host.firewall"); c.Status != "warn" || !strings.Contains(c.Message, "cannot be listed") {
		t.Errorf("nft failing: %+v", c)
	}
}

func TestTheDataDirectoriesMustBeThereOwnedByTheAPIUserAndNotTooOpen(t *testing.T) {
	mk := func(uid uint32, perm fs.FileMode) *fstest.MapFile {
		return &fstest.MapFile{Mode: fs.ModeDir | perm, Sys: &syscall.Stat_t{Uid: uid}}
	}
	fsys := hostFS(func(m fstest.MapFS) {
		m["var/lib/chaosgw/captures"] = mk(65532, 0o755)
		m["var/lib/chaosgw/secrets"] = mk(65532, 0o700)
	})
	dirs := []DataDir{
		{Path: "/var/lib/chaosgw/captures", What: "the captures", UID: 65532},
		{Path: "/var/lib/chaosgw/secrets", What: "the secrets", UID: 65532, MaxMode: 0o700},
	}
	for _, c := range HostChecks(context.Background(), probe(fsys, versions(nil), dirs...)) {
		if strings.HasPrefix(c.ID, "host.data.") && c.Status != "ok" {
			t.Errorf("%+v", c)
		}
	}

	fsys = hostFS(func(m fstest.MapFS) {
		m["var/lib/chaosgw/captures"] = mk(0, 0o755)    // root's: Docker made it
		m["var/lib/chaosgw/secrets"] = mk(65532, 0o755) // too open
	})
	checks := HostChecks(context.Background(), probe(fsys, versions(nil), append(dirs, DataDir{Path: "/var/lib/chaosgw/api", What: "the audit log", UID: 65532})...))
	if c := byID(t, checks, "host.data.captures"); c.Status != "fail" || !strings.Contains(c.Message, "belongs to user 0") || !strings.Contains(c.Fix, "host-setup") {
		t.Errorf("a directory of the wrong owner: %+v", c)
	}
	if c := byID(t, checks, "host.data.secrets"); c.Status != "fail" || !strings.Contains(c.Message, "0755") {
		t.Errorf("a directory that is too open: %+v", c)
	}
	if c := byID(t, checks, "host.data.api"); c.Status != "fail" || !strings.Contains(c.Message, "is not there") {
		t.Errorf("a directory that is missing: %+v", c)
	}
}

// ipAddrJSON is `ip -j -4 addr show` for the links "name address/prefix ...".
func ipAddrJSON(links ...string) string {
	var out []string
	for _, l := range links {
		f := strings.Fields(l)
		var addrs []string
		for _, a := range f[1:] {
			ip, bits, _ := strings.Cut(a, "/")
			addrs = append(addrs, `{"family":"inet","local":"`+ip+`","prefixlen":`+bits+`}`)
		}
		out = append(out, `{"ifname":"`+f[0]+`","addr_info":[`+strings.Join(addrs, ",")+`]}`)
	}
	return "[" + strings.Join(out, ",") + "]"
}

// Plan §3.8: the gateway's networks must not overlap Docker's own address pools. The bridges Docker made and the pools set in
// daemon.json are compared with the addresses of every other interface of the host.
func TestAnAddressOfTheHostThatOverlapsDockersRangesIsWarnedAbout(t *testing.T) {
	const lo, docker0 = "lo 127.0.0.1/8", "docker0 172.17.0.1/16"
	run := func(ip string, daemon string) HostCheck {
		p := probe(hostFS(nil), versions(map[string]string{"ip -j -4 addr show": ip}))
		if daemon != "" {
			p.HostFS = fstest.MapFS{"etc/docker/daemon.json": {Data: []byte(daemon)}}
		}
		return byID(t, HostChecks(context.Background(), p), "host.docker_pools")
	}
	if c := run(ipAddrJSON(lo, docker0, "br-0123456789ab 172.18.0.1/16", "eth0 10.0.0.5/24", "br-iot 192.168.50.1/24"), ""); c.Status != "ok" {
		t.Errorf("no overlap: %+v", c)
	}
	// a test network on docker0's range
	c := run(ipAddrJSON(lo, docker0, "eth0 10.0.0.5/24", "br-iot 172.17.5.1/24"), "")
	if c.Status != "warn" || !strings.Contains(c.Message, "br-iot 172.17.5.0/24 overlaps docker0 172.17.0.0/16") || c.Fix == "" {
		t.Errorf("a network on the range of docker0: %+v", c)
	}
	// the uplink inside the range of a user-defined Docker network
	if c := run(ipAddrJSON(lo, "br-0123456789ab 192.168.1.1/24", "eth0 192.168.1.20/24"), ""); c.Status != "warn" || !strings.Contains(c.Message, "eth0 192.168.1.0/24 overlaps br-0123456789ab") {
		t.Errorf("the uplink in the range of a Docker network: %+v", c)
	}
	// a pool and the bip of daemon.json count before Docker has made a network of them
	daemon := `{"bip": "10.200.0.1/24", "default-address-pools": [{"base": "10.100.0.0/16", "size": 24}]}`
	if c := run(ipAddrJSON(lo, "eth0 10.0.0.5/24", "br-iot 10.100.7.1/24"), daemon); c.Status != "warn" || !strings.Contains(c.Message, "br-iot 10.100.7.0/24 overlaps daemon.json 10.100.0.0/16") {
		t.Errorf("a network in a pool of daemon.json: %+v", c)
	}
	if c := run(ipAddrJSON(lo, "br-iot 10.200.0.77/24"), daemon); c.Status != "warn" || !strings.Contains(c.Message, "daemon.json 10.200.0.0/24") {
		t.Errorf("a network on the bip of daemon.json: %+v", c)
	}
	if c := run(ipAddrJSON(lo, "eth0 10.0.0.5/24", "br-iot 10.1.0.1/24"), daemon); c.Status != "ok" {
		t.Errorf("outside the pools: %+v", c)
	}
	// Docker's built-in pools are not counted: only the bridges that exist are
	if c := run(ipAddrJSON(lo, "eth0 172.20.0.5/24"), ""); c.Status != "ok" {
		t.Errorf("an address in a built-in pool of Docker with no network there: %+v", c)
	}
	if c := run("!no", ""); c.Status != "warn" || !strings.Contains(c.Message, "cannot be listed") {
		t.Errorf("ip failing: %+v", c)
	}
}
