package executor_test

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// M29: the review of the containers (docs/development.md, "Security hardening (M29)", the table of the review). The tests
// above pin each container's own file; these look at compose.yaml, the file that runs, as a whole and pin what the review
// decided about the set: who runs as whom, what each container may mount from the host, and what none of them may ask for.

type reviewedService struct {
	User         string      `yaml:"user"`
	Pid          string      `yaml:"pid"`
	Ipc          string      `yaml:"ipc"`
	Privileged   bool        `yaml:"privileged"`
	ReadOnly     bool        `yaml:"read_only"`
	NetworkMode  string      `yaml:"network_mode"`
	CapAdd       []string    `yaml:"cap_add"`
	CapDrop      []string    `yaml:"cap_drop"`
	SecurityOpt  []string    `yaml:"security_opt"`
	Ports        []yaml.Node `yaml:"ports"`
	Devices      []yaml.Node `yaml:"devices"`
	Sysctls      yaml.Node   `yaml:"sysctls"`
	UsernsMode   string      `yaml:"userns_mode"`
	CgroupParent string      `yaml:"cgroup_parent"`
	Cgroup       string      `yaml:"cgroup"`
	Restart      string      `yaml:"restart"`
	Logging      struct {
		Options map[string]string `yaml:"options"`
	} `yaml:"logging"`
	Volumes []yaml.Node `yaml:"volumes"`
}

func loadComposeYAML(t *testing.T) map[string]reviewedService {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]reviewedService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) < 8 {
		t.Fatalf("compose.yaml has %d services", len(doc.Services))
	}
	return doc.Services
}

// binds lists the bind mounts of a service as "source -> target ro|rw"; the volumes of the other types are not host paths.
func (s reviewedService) binds(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, v := range s.Volumes {
		if v.Kind != yaml.MappingNode {
			continue
		}
		var m struct {
			Type     string `yaml:"type"`
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
		}
		if err := v.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if m.Type != "bind" {
			continue
		}
		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		out = append(out, m.Source+" -> "+m.Target+" "+mode)
	}
	sort.Strings(out)
	return out
}

// Every container names the user it runs as, and the image's own default is the unprivileged one: a container started from the
// image without a `user` (a manual `docker run`, a new service whose author forgot it) is not root.
func TestEveryContainerNamesItsUserAndTheImageDefaultsToTheUnprivilegedOne(t *testing.T) {
	root := map[string]bool{"exec": true, "bird": true, "kea": true, "teardown": true} // the review's list of the containers that are root
	for name, s := range loadComposeYAML(t) {
		if s.User == "" {
			t.Errorf("%s: no user: it would run as the image's default", name)
		}
		isRoot := strings.HasPrefix(s.User, "0:") || s.User == "0"
		if isRoot != root[name] {
			t.Errorf("%s runs as %q: the review lists exec, bird, kea and teardown as the containers that are root", name, s.User)
		}
		if !isRoot && !strings.HasPrefix(s.User, "65532:") {
			t.Errorf("%s runs as %q, not as 65532", name, s.User)
		}
	}
	raw, err := os.ReadFile("../../deploy/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.EqualFold(f[0], "USER") {
			users = append(users, f[1])
		}
	}
	if len(users) == 0 || users[len(users)-1] != "65532:65532" {
		t.Errorf("the last USER of the Dockerfile is %v, want 65532:65532 (M1: the image has a non-root user)", users)
	}
	// nothing but the final stage's USER is in effect at run time: it comes after the last FROM
	last := strings.LastIndex(string(raw), "\nFROM ")
	if i := strings.LastIndex(string(raw), "\nUSER "); i < last {
		t.Error("the USER line is not in the final stage of the Dockerfile")
	}
}

// What a container may mount from the host. A bind mount outside this table is a change of the review; the data directories of
// plan §3.6 are the gateway's own, the rest is read-only except the named network namespaces the executor shares with the host.
func TestTheContainersMountFromTheHostOnlyWhatTheReviewAllows(t *testing.T) {
	data := "/var/lib/chaosgw/"
	want := map[string][]string{
		"exec": {data + "captures -> " + data + "captures rw", data + "exec -> " + data + "exec rw", data + "secrets -> " + data + "secrets ro",
			"/etc -> /host/etc ro", "/lib/modules -> /lib/modules ro", "/run/netns -> /run/netns rw", "/usr/lib/systemd/network -> /host/usr/lib/systemd/network ro"},
		"api": {data + "api -> " + data + "api rw", data + "captures -> " + data + "captures rw", data + "secrets -> " + data + "secrets rw", data + "service -> " + data + "service rw",
			data + "state -> " + data + "state rw", data + "tlsca -> " + data + "tlsca rw", "/run/systemd/resolve -> /run/systemd/resolve ro"},
		"kea":       {data + "kea -> /var/lib/kea rw", data + "service -> " + data + "service ro"},
		"dns":       {data + "service -> " + data + "service ro"},
		"tls":       {data + "service -> " + data + "service ro", data + "tlsca -> " + data + "tlsca ro"},
		"tls-proxy": {data + "service -> " + data + "service ro", data + "tlsca -> " + data + "tlsca ro"},
		"svcns":     nil,
		"bird":      nil,
		// the stop on demand empties the data directories and removes what the executor left in the kernel
		"teardown": {data + "api -> " + data + "api rw", data + "captures -> " + data + "captures rw", data + "exec -> " + data + "exec rw", data + "kea -> /var/lib/kea rw",
			data + "secrets -> " + data + "secrets rw", data + "service -> " + data + "service rw", data + "state -> " + data + "state rw", data + "tlsca -> " + data + "tlsca rw",
			"/run/netns -> /run/netns rw"},
	}
	forbidden := regexp.MustCompile(`^(/|/proc|/sys|/dev|/boot|/root|/home|/var|/var/run|/run|/var/lib|/var/lib/docker|.*docker\.sock)(/.*)?$`)
	for name, s := range loadComposeYAML(t) {
		got := s.binds(t)
		w, known := want[name]
		if !known {
			t.Errorf("%s is a container the review does not know", name)
			continue
		}
		sort.Strings(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s mounts from the host\n  %v\nthe review allows\n  %v", name, got, w)
		}
		for _, b := range got {
			src, _, _ := strings.Cut(b, " -> ")
			if forbidden.MatchString(src) && !strings.HasPrefix(src, data) && !strings.HasPrefix(src, "/run/netns") && !strings.HasPrefix(src, "/run/systemd/resolve") {
				t.Errorf("%s mounts %s", name, src)
			}
		}
	}
	// the secrets (the WireGuard keys, the credentials, the HTTPS key) are the API's; the executor reads them, nobody else sees them
	for name, s := range loadComposeYAML(t) {
		for _, b := range s.binds(t) {
			if strings.HasPrefix(b, data+"secrets") && name != "api" && name != "exec" && name != "teardown" {
				t.Errorf("%s mounts the secrets directory", name)
			}
		}
	}
}

// The capabilities of the set that runs, as an allow-list: whether a container is privileged, the exact capabilities it adds
// after dropping all of them, and the network it joins. A change to any of them is a change of the review, not a line in a diff.
func TestEveryContainerHasExactlyTheCapabilitiesTheReviewGrants(t *testing.T) {
	want := map[string]struct {
		privileged bool
		capAdd     []string
		network    string
	}{
		"exec":      {privileged: true, network: "host"}, // privileged includes every capability; there is nothing to drop (spike S7)
		"teardown":  {privileged: true, network: "host"},
		"api":       {network: "host"},
		"kea":       {capAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "NET_BIND_SERVICE", "NET_RAW"}, network: "host"},
		"bird":      {capAdd: []string{"NET_ADMIN", "NET_BIND_SERVICE", "NET_RAW"}, network: "host"},
		"svcns":     {network: "none"},
		"dns":       {capAdd: []string{"NET_BIND_SERVICE"}, network: "service:svcns"},
		"tls":       {network: "service:svcns"},
		"tls-proxy": {network: "service:svcns"},
	}
	services := loadComposeYAML(t)
	for name, s := range services {
		w, known := want[name]
		if !known {
			t.Errorf("%s is a container the review does not know", name)
			continue
		}
		if s.Privileged != w.privileged {
			t.Errorf("%s: privileged is %v, the review decided %v", name, s.Privileged, w.privileged)
		}
		if s.NetworkMode != w.network {
			t.Errorf("%s: network_mode is %q, the review decided %q", name, s.NetworkMode, w.network)
		}
		got, exp := slices.Sorted(slices.Values(s.CapAdd)), w.capAdd
		if !slices.Equal(got, exp) && (len(got) != 0 || len(exp) != 0) {
			t.Errorf("%s adds the capabilities %v, the review decided %v", name, got, exp)
		}
		if w.privileged {
			if len(s.CapAdd) != 0 || len(s.CapDrop) != 0 {
				t.Errorf("%s is privileged and lists capabilities %v / %v", name, s.CapAdd, s.CapDrop)
			}
		} else if !slices.Equal(s.CapDrop, []string{"ALL"}) {
			t.Errorf("%s: cap_drop is %v, want [ALL]", name, s.CapDrop)
		}
	}
	for name := range want {
		if _, ok := services[name]; !ok {
			t.Errorf("the review lists %s and compose.yaml has no such container", name)
		}
	}
}

// What no container asks for: published ports (the API listens in the host's network namespace itself), devices, a PID or IPC
// namespace of the host beyond the two that need it, a user namespace or cgroup of its own, an unconfined security profile, the
// capabilities that give the host away, and a missing log limit (a container that cannot fill the disk with its log).
func TestNoContainerWidensItsSandbox(t *testing.T) {
	hostPID := map[string]bool{"exec": true, "svcns": true} // the executor attaches the service namespace by the holder's host PID
	never := []string{"SYS_ADMIN", "SYS_PTRACE", "SYS_MODULE", "SYS_RAWIO", "SYS_BOOT", "SYS_TIME", "SYS_CHROOT", "DAC_READ_SEARCH", "SETUID", "SETGID", "SETPCAP", "MKNOD",
		"AUDIT_CONTROL", "AUDIT_WRITE", "MAC_ADMIN", "MAC_OVERRIDE", "NET_BROADCAST", "SYS_RESOURCE", "SYS_NICE", "SYS_PACCT", "LINUX_IMMUTABLE", "SYSLOG", "BPF", "PERFMON", "CHECKPOINT_RESTORE"}
	for name, s := range loadComposeYAML(t) {
		if len(s.Ports) != 0 {
			t.Errorf("%s publishes ports", name)
		}
		if len(s.Devices) != 0 {
			t.Errorf("%s gets devices", name)
		}
		if s.Ipc != "" || s.UsernsMode != "" || s.Cgroup != "" || s.CgroupParent != "" || s.Sysctls.Kind != 0 {
			t.Errorf("%s changes its IPC, user namespace, cgroup or sysctls: %+v", name, s)
		}
		if s.Pid != "" && (s.Pid != "host" || !hostPID[name]) && name != "teardown" {
			t.Errorf("%s has pid %q", name, s.Pid)
		}
		if name == "teardown" && s.Pid != "" {
			t.Errorf("the teardown needs no host PID namespace, it has %q", s.Pid)
		}
		for _, o := range s.SecurityOpt {
			if strings.Contains(o, "unconfined") || strings.HasPrefix(o, "label=disable") {
				t.Errorf("%s runs with %s", name, o)
			}
		}
		for _, c := range s.CapAdd {
			if slices.Contains(never, c) {
				t.Errorf("%s adds %s", name, c)
			}
		}
		// Kea is the one container without no-new-privileges (decision 7, P7-M29-02): its package may use file capabilities
		if !slices.Contains(s.SecurityOpt, "no-new-privileges:true") && name != "kea" {
			t.Errorf("%s does not set no-new-privileges; only kea is excepted (P7-M29-02)", name)
		}
		if name == "kea" && slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("kea sets no-new-privileges: remove the exception from this test, the container table and the level 2 deployment test (P7-M29-02)")
		}
		if s.Restart == "" && name != "teardown" {
			t.Errorf("%s has no restart policy", name)
		}
		// the teardown is a one-shot `run --rm`: its log goes with the container
		if name != "teardown" && (s.Logging.Options["max-size"] == "" || s.Logging.Options["max-file"] == "") {
			t.Errorf("%s has no limit for its log", name)
		}
		// a container whose root file system is read-only and that is not privileged is closed down by the other tests; the exceptions
		// are named here so that a new one cannot appear unnoticed
		if !s.ReadOnly {
			t.Errorf("%s: the root file system is writable", name)
		}
	}
}
