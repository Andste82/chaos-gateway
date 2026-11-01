package preflight

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// HostCheck is one line of the executor's preflight (plan §3.4): what the privileged process sees of the host it runs on. The
// API adds them to its own report (kernel, modules, the operating system's configuration of the test interfaces) in
// GET /system/preflight. Status is ok, warn or fail.
type HostCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// APIGID is the group of the API container (the image's user 65532:65532).
const APIGID = 65532

// DataDir is a directory of the persistence layout (plan §3.6) the executor's container mounts from the host.
type DataDir struct {
	// Path is where the container mounts it; What says what it holds.
	Path, What string
	// UID is the user of the API container (65532), which has to be able to write the directory, because it makes the
	// content (the capture directories) the executor then writes into; negative checks nothing about the owner.
	UID int
	// MaxMode is the widest permission the directory may have (0o700 for the secrets); zero checks nothing.
	MaxMode fs.FileMode
}

// HostProbe is what the checks look at; the executor passes its own view of the machine, the tests a scripted one.
type HostProbe struct {
	// FS is the executor's own root file system (/proc, /sys, /lib/modules and the data directories below it).
	FS fs.FS
	// Release is the kernel release (uname -r).
	Release string
	// HasTool says whether the executor can run the tool (one of Tools, modprobe).
	HasTool func(tool string) bool
	// Run runs a tool with a fixed argument list and returns what it printed (standard output and error); the error is set
	// when it did not run or exited with a status other than 0.
	Run func(ctx context.Context, tool string, args ...string) (string, error)
	// HostFS is the host's file system (the executor's /host); nil when it is not mounted. Docker's configuration is read from it.
	HostFS fs.FS
	// DataDirs are the directories to check.
	DataDirs []DataDir
}

// HostTool is a tool the executor runs, with the arguments that make it print its version.
type HostTool struct {
	Name    string
	Version []string
}

// Tools are the tools the executor needs (plan §3.4, §3.7). tcpdump is in the image for the administrator's shell and not
// run by the gateway (the captures are the executor's own AF_PACKET sockets, M17).
var Tools = []HostTool{
	{"ip", []string{"-V"}},
	{"tc", []string{"-V"}},
	{"nft", []string{"--version"}},
	{"ethtool", []string{"--version"}},
	{"conntrack", []string{"-V"}},
	{"iptables", []string{"-V"}},
	{"wg", []string{"--version"}},
	{"bird", []string{"--version"}},
	{"birdc", []string{"--version"}},
}

// namespaceDir is where named network namespaces live.
const namespaceDir = "/run/netns"

func hostFile(p HostProbe, name string) (string, bool) {
	raw, err := fs.ReadFile(p.FS, path.Clean(strings.TrimPrefix(name, "/")))
	return strings.TrimSpace(string(raw)), err == nil
}

// HostChecks runs the checks of the executor's preflight. Loading the kernel modules that are available but not loaded and
// creating and removing a throw-away network namespace are the only changes it makes.
func HostChecks(ctx context.Context, p HostProbe) []HostCheck {
	var out []HostCheck
	add := func(id, status, msg, fix string) {
		out = append(out, HostCheck{ID: id, Status: status, Message: msg, Fix: fix})
	}

	out = append(out, toolChecks(ctx, p)...)
	out = append(out, moduleCheck(ctx, p))

	// forwarding: the first apply switches it on, the host setup keeps it on at every boot
	switch v, ok := hostFile(p, "/proc/sys/net/ipv4/ip_forward"); {
	case !ok:
		add("host.ip_forward", "warn", "net.ipv4.ip_forward cannot be read", "")
	case v == "1":
		add("host.ip_forward", "ok", "IPv4 forwarding is on", "")
	default:
		add("host.ip_forward", "warn", "IPv4 forwarding is off; the gateway switches it on with the first configuration it applies, but a reboot switches it off again until then",
			"run the host setup (deploy/host-setup.sh): it writes /etc/sysctl.d/90-chaos-gateway.conf")
	}
	// the ping of the diagnostics (M24) is the API's unprivileged ICMP socket, and the kernel gives it to the processes whose group
	// lies inside the range: the API container's group, not any group
	if v, ok := hostFile(p, "/proc/sys/net/ipv4/ping_group_range"); ok {
		if f := strings.Fields(v); len(f) != 2 || atoi(f[0]) > APIGID || atoi(f[1]) < APIGID {
			add("host.ping_group_range", "warn", "unprivileged ICMP sockets are not allowed to the group of the API container ("+strconv.Itoa(APIGID)+"; net.ipv4.ping_group_range is \""+v+"\"): the ping of the diagnostics cannot run",
				"run the host setup (deploy/host-setup.sh): it writes /etc/sysctl.d/90-chaos-gateway.conf")
		} else {
			add("host.ping_group_range", "ok", "unprivileged ICMP sockets are allowed to the group of the API container (ping of the diagnostics)", "")
		}
	}

	out = append(out, procSysCheck(p), namespaceCheck(ctx, p))
	out = append(out, firewallChecks(ctx, p)...)
	if p.HasTool("ip") {
		out = append(out, dockerPoolCheck(ctx, p))
	}
	out = append(out, dataChecks(p)...)
	return out
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// toolChecks says which tools are missing and which versions the rest are, and whether iptables is the nftables-backed one: the
// chain DOCKER-USER the executor keeps its accept rule in is Docker's, and Docker on Ubuntu uses the nf_tables backend.
func toolChecks(ctx context.Context, p HostProbe) []HostCheck {
	var missing, found []string
	backend := ""
	for _, t := range Tools {
		if !p.HasTool(t.Name) {
			missing = append(missing, t.Name)
			continue
		}
		line := ""
		if txt, err := p.Run(ctx, t.Name, t.Version...); err == nil || txt != "" {
			line, _, _ = strings.Cut(strings.TrimSpace(txt), "\n")
		}
		if t.Name == "iptables" {
			backend = line
		}
		if t.Name != "birdc" && line != "" {
			found = append(found, strings.TrimSpace(line))
		}
	}
	if len(missing) > 0 {
		return []HostCheck{{ID: "host.tools", Status: "fail", Message: "tools missing: " + strings.Join(missing, ", "),
			Fix: "pull the image again: it holds every tool the executor runs"}}
	}
	out := []HostCheck{{ID: "host.tools", Status: "ok", Message: strings.Join(found, "; ")}}
	if strings.Contains(backend, "(legacy)") {
		out = append(out, HostCheck{ID: "host.iptables", Status: "warn",
			Message: "iptables uses the legacy backend (" + backend + "): Docker's FORWARD policy and its DOCKER-USER chain are in the nf_tables backend on Ubuntu, so the accept rule for the gateway's interfaces would not be seen",
			Fix:     "use the nf_tables backend of iptables (update-alternatives --set iptables /usr/sbin/iptables-nft on the host, and restart Docker)"})
	}
	return out
}

// moduleCheck loads the required modules that exist but are not loaded (the host setup loads them at boot; this is for a host
// that did not run it) and reports the ones that cannot be had. The API reports the state of each module in its own checks.
func moduleCheck(ctx context.Context, p HostProbe) HostCheck {
	env := Env{FS: p.FS, Release: p.Release}
	var load, gone []Module
	for _, m := range CheckModules(env, Required()) {
		switch m.Status {
		case Available:
			load = append(load, m.Module)
		case Missing:
			gone = append(gone, m.Module)
		}
	}
	var loaded []string
	if len(load) > 0 {
		if p.HasTool("modprobe") {
			_, _ = p.Run(ctx, "modprobe", ModprobeArgs(load)...)
			for _, m := range CheckModules(env, load) {
				switch m.Status {
				case Loaded:
					loaded = append(loaded, m.Name)
				case Available:
					gone = append(gone, m.Module)
				}
			}
		} else {
			gone = append(gone, load...)
		}
	}
	if len(gone) > 0 {
		names := make([]string, len(gone))
		for i, m := range gone {
			names[i] = m.Name
		}
		return HostCheck{ID: "host.modules", Status: "fail", Message: "kernel modules that are missing or cannot be loaded: " + strings.Join(names, ", "),
			Fix: "install the extra kernel modules for the host's kernel (linux-modules-extra-$(uname -r)), or boot a standard Ubuntu kernel; mount the host's /lib/modules into the executor"}
	}
	if len(loaded) > 0 {
		return HostCheck{ID: "host.modules", Status: "ok", Message: "loaded kernel modules that were not loaded yet: " + strings.Join(loaded, ", ")}
	}
	return HostCheck{ID: "host.modules", Status: "ok", Message: "all required kernel modules are loaded"}
}

// mount is a line of /proc/self/mountinfo.
type mount struct {
	point, options string
	shared         bool
}

func mounts(p HostProbe) []mount {
	raw, err := fs.ReadFile(p.FS, "proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []mount
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		f := strings.Fields(sc.Text())
		if len(f) < 7 {
			continue
		}
		m := mount{point: f[4], options: f[5]}
		for _, opt := range f[6:] {
			if opt == "-" {
				break
			}
			if strings.HasPrefix(opt, "shared:") {
				m.shared = true
			}
		}
		out = append(out, m)
	}
	return out
}

func hasOption(options, want string) bool {
	for _, o := range strings.Split(options, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// procSysCheck: the per-interface sysctls of the bridges the gateway makes need a writable /proc/sys, which a container has
// only when it is privileged (spike S7).
func procSysCheck(p HostProbe) HostCheck {
	for _, m := range mounts(p) {
		if m.point == "/proc/sys" && hasOption(m.options, "ro") {
			return HostCheck{ID: "host.proc_sys", Status: "fail", Message: "/proc/sys is mounted read-only: the executor cannot set the sysctls of the interfaces it makes",
				Fix: "run the executor container privileged (deploy/compose.executor.yaml)"}
		}
	}
	return HostCheck{ID: "host.proc_sys", Status: "ok", Message: "/proc/sys is writable"}
}

// namespaceCheck makes a named network namespace and removes it again, and says whether /run/netns is shared with the host: the
// service namespace and the probes' namespaces are named there, and the stop cleanup of a later container (`chaosgw teardown`)
// finds them by those names.
func namespaceCheck(ctx context.Context, p HostProbe) HostCheck {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return HostCheck{ID: "host.netns", Status: "warn", Message: "no random name for the test namespace: " + err.Error()}
	}
	// the checks run one at a time (the executor's lock), so a namespace of this name that is there is a leftover
	RemoveCheckNamespaces(ctx, p.Run)
	name := "pfchk-" + hex.EncodeToString(b[:])
	if out, err := p.Run(ctx, "ip", "netns", "add", name); err != nil {
		return HostCheck{ID: "host.netns", Status: "fail", Message: "cannot create a network namespace: " + firstLine(out, err),
			Fix: "run the executor container privileged with the host's /run/netns mounted (deploy/compose.executor.yaml)"}
	}
	if out, err := p.Run(ctx, "ip", "netns", "del", name); err != nil {
		return HostCheck{ID: "host.netns", Status: "warn", Message: "a test namespace could not be removed again: " + firstLine(out, err)}
	}
	for _, m := range mounts(p) {
		if m.point == namespaceDir && !m.shared {
			return HostCheck{ID: "host.netns", Status: "warn", Message: namespaceDir + " is mounted but not shared with the host: the namespaces of the probes and of the service are not visible there, and a namespace left behind by a killed executor stays in this mount",
				Fix: "mount the host's /run/netns with propagation rshared (deploy/compose.executor.yaml)"}
		}
	}
	return HostCheck{ID: "host.netns", Status: "ok", Message: "network namespaces can be created, and " + namespaceDir + " is shared with the host"}
}

// checkNamespaceName is the name of the throw-away namespace of namespaceCheck.
var checkNamespaceName = regexp.MustCompile(`^pfchk-[0-9a-f]{8}$`)

// RemoveCheckNamespaces removes the throw-away namespaces of the namespace check that an executor killed between the creation and
// the removal, or a removal that failed, left in the host's /run/netns (which the mount propagates to the host), and returns the
// names it removed. The preflight calls it before it makes a new one; the stop cleanup and `chaosgw teardown` call it too, so that
// a stopped gateway leaves no name of its own behind.
func RemoveCheckNamespaces(ctx context.Context, run func(ctx context.Context, tool string, args ...string) (string, error)) []string {
	out, err := run(ctx, "ip", "netns", "list")
	if err != nil {
		return nil
	}
	var removed []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line) // pfchk-0123abcd (id: 3)
		if len(f) == 0 || !checkNamespaceName.MatchString(f[0]) {
			continue
		}
		if _, err := run(ctx, "ip", "netns", "del", f[0]); err == nil {
			removed = append(removed, f[0])
		}
	}
	return removed
}

func firstLine(out string, err error) string {
	if l, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); l != "" {
		return l
	}
	return err.Error()
}

// firewallChecks reports other firewalls on the host (plan §3.4): a drop verdict in another table still drops packets. ufw
// and firewalld are named; every other table of nftables that is neither this gateway's nor the ones Docker and iptables make
// is listed, because a rule there can drop test traffic too. Docker's own FORWARD policy is DROP and handled through
// DOCKER-USER (plan §3.8), so it is no conflict.
func firewallChecks(ctx context.Context, p HostProbe) []HostCheck {
	if !p.HasTool("nft") {
		return nil
	}
	out, err := p.Run(ctx, "nft", "list", "tables")
	if err != nil {
		return []HostCheck{{ID: "host.firewall", Status: "warn", Message: "the tables of nftables cannot be listed: " + firstLine(out, err)}}
	}
	var others []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line) // table inet firewalld
		if len(f) != 3 || f[0] != "table" {
			continue
		}
		switch f[2] {
		case "chaosgw", "chaosgw_dup", "filter", "nat", "mangle", "raw", "security", "docker-bridges":
			// Chaos Gateway's own (chaosgw_dup is the netdev table of an active duplication fault, executor.NftDupTable, which this
			// package cannot import), and the tables of iptables-nft and of Docker
			continue
		}
		others = append(others, f[1]+" "+f[2])
	}
	if ipt, err := p.Run(ctx, "iptables", "-w", "5", "-S"); err == nil && strings.Contains(ipt, "-N ufw-") {
		others = append(others, "ufw (iptables chains ufw-*)")
	}
	if len(others) == 0 {
		return []HostCheck{{ID: "host.firewall", Status: "ok", Message: "no other firewall besides Docker's rules"}}
	}
	return []HostCheck{{ID: "host.firewall", Status: "warn",
		Message: "another firewall is active: " + strings.Join(others, ", ") + "; a drop verdict in its rules still drops test traffic",
		Fix:     "allow the traffic of the test networks in that firewall, or stop it (ufw disable, systemctl disable --now firewalld)"}}
}

// dataChecks checks the directories of the host that the container mounts (plan §3.6): the compose files bind-mount them, and
// Docker creates a directory that is missing as root's, which the API container (a user without any capability) could not
// write. The host setup makes them with the right owner.
func dataChecks(p HostProbe) []HostCheck {
	var out []HostCheck
	for _, d := range p.DataDirs {
		id := "host.data." + path.Base(d.Path)
		fi, err := fs.Stat(p.FS, strings.TrimPrefix(d.Path, "/"))
		if err != nil {
			out = append(out, HostCheck{ID: id, Status: "fail", Message: d.Path + " (" + d.What + ") is not there: " + err.Error(), Fix: "run the host setup (deploy/host-setup.sh): it makes the directories of the gateway"})
			continue
		}
		st, _ := fi.Sys().(*syscall.Stat_t)
		switch {
		case !fi.IsDir():
			out = append(out, HostCheck{ID: id, Status: "fail", Message: d.Path + " is not a directory"})
		case d.UID >= 0 && st != nil && int(st.Uid) != d.UID && fi.Mode().Perm()&0o002 == 0:
			out = append(out, HostCheck{ID: id, Status: "fail",
				Message: fmt.Sprintf("%s (%s) belongs to user %d, but the API container runs as user %d and cannot write it", d.Path, d.What, st.Uid, d.UID),
				Fix:     fmt.Sprintf("run the host setup (deploy/host-setup.sh), or chown %d:%d the directory", d.UID, d.UID)})
		case d.MaxMode != 0 && fi.Mode().Perm()&^d.MaxMode != 0:
			out = append(out, HostCheck{ID: id, Status: "fail", Message: fmt.Sprintf("%s (%s) has the permissions %04o, more open than %04o", d.Path, d.What, fi.Mode().Perm(), d.MaxMode),
				Fix: fmt.Sprintf("chmod %04o the directory", d.MaxMode)})
		default:
			out = append(out, HostCheck{ID: id, Status: "ok", Message: d.Path + " (" + d.What + ") is there with the right owner"})
		}
	}
	return out
}
