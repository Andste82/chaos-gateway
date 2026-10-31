package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const deployDir = "../../deploy"

// service is the part of a compose service these tests pin.
type service struct {
	Image       string    `yaml:"image"`
	Privileged  bool      `yaml:"privileged"`
	ReadOnly    bool      `yaml:"read_only"`
	NetworkMode string    `yaml:"network_mode"`
	Restart     string    `yaml:"restart"`
	User        string    `yaml:"user"`
	Profiles    []string  `yaml:"profiles"`
	CapAdd      []string  `yaml:"cap_add"`
	CapDrop     []string  `yaml:"cap_drop"`
	SecurityOpt []string  `yaml:"security_opt"`
	Volumes     []mount   `yaml:"volumes"`
	DependsOn   depends   `yaml:"depends_on"`
	Healthcheck *struct{} `yaml:"healthcheck"`
}

// mount is a volume entry; a short entry ("name:/path") is a named volume.
type mount struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
	Bind     struct {
		CreateHostPath *bool  `yaml:"create_host_path"`
		Propagation    string `yaml:"propagation"`
	} `yaml:"bind"`
}

func (m *mount) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		src, rest, _ := strings.Cut(n.Value, ":")
		m.Source, m.Target = src, rest
		m.Type = "volume"
		return nil
	}
	type plain mount
	return n.Decode((*plain)(m))
}

// depends is depends_on in the long syntax: the service and the condition.
type depends map[string]struct {
	Condition string `yaml:"condition"`
	Required  *bool  `yaml:"required"`
	Restart   bool   `yaml:"restart"`
}

type composeFile struct {
	Name     string             `yaml:"name"`
	Services map[string]service `yaml:"services"`
	Volumes  map[string]any     `yaml:"volumes"`
}

func load(t *testing.T, name string) composeFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(deployDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

// compose.yaml is what the compose files of the containers give; a change to one of them is made in the file and then in
// compose.yaml by `go run ./tools/composegen`.
func TestComposeYAMLIsMadeFromTheComposeFilesOfTheContainers(t *testing.T) {
	want, err := Generate(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(filepath.Join(deployDir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != string(want) {
		t.Fatal("deploy/compose.yaml is not current: run `go run ./tools/composegen`")
	}
}

// Every service of every file is in compose.yaml with the settings it has in its own file (the files are what the lower levels
// start); only the dependencies and the profile of the proxy differ, as the package comment says.
func TestEveryServiceOfEveryFileIsInComposeYAMLUnchanged(t *testing.T) {
	all := load(t, "compose.yaml")
	for _, f := range files {
		for name, svc := range load(t, f).Services {
			got, ok := all.Services[name]
			if !ok {
				t.Errorf("%s: the service %s is not in compose.yaml", f, name)
				continue
			}
			svc.DependsOn, got.DependsOn = nil, nil
			svc.Profiles, got.Profiles = nil, nil
			if !equalService(svc, got) {
				t.Errorf("%s: the service %s differs in compose.yaml:\n%+v\n%+v", f, name, svc, got)
			}
		}
	}
}

func equalService(a, b service) bool {
	ya, _ := yaml.Marshal(a)
	yb, _ := yaml.Marshal(b)
	return string(ya) == string(yb)
}

// The containers and privileges of plan §3.8: the executor is the only privileged container of the gateway (the teardown service
// is the executor's profile for the cleanup), every other one drops all capabilities and adds back what the table names, runs
// with a read-only root file system and with no-new-privileges.
func TestOnlyTheExecutorIsPrivilegedAndTheCapabilitiesAreThoseOfThePlan(t *testing.T) {
	f := load(t, "compose.yaml")
	wantCaps := map[string][]string{
		"api": nil, "svcns": nil, "tls": nil, "tls-proxy": nil,
		"dns":  {"NET_BIND_SERVICE"},
		"kea":  {"CHOWN", "DAC_OVERRIDE", "FOWNER", "NET_BIND_SERVICE", "NET_RAW"},
		"bird": {"NET_ADMIN", "NET_BIND_SERVICE", "NET_RAW"},
	}
	for name, s := range f.Services {
		if !s.ReadOnly {
			t.Errorf("%s: the root file system is not read-only", name)
		}
		if name == "exec" || name == "teardown" {
			if !s.Privileged {
				t.Errorf("%s must be privileged (plan §3.8)", name)
			}
			continue
		}
		if s.Privileged {
			t.Errorf("%s is privileged: only the executor is (plan §3.8)", name)
		}
		want, ok := wantCaps[name]
		if !ok {
			t.Errorf("%s is a container the table of plan §3.8 does not know", name)
			continue
		}
		if !slices.Equal(s.CapDrop, []string{"ALL"}) {
			t.Errorf("%s: cap_drop %v, want [ALL]", name, s.CapDrop)
		}
		got := slices.Clone(s.CapAdd)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: cap_add %v, want %v", name, got, want)
		}
		if !slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("%s: no-new-privileges is missing", name)
		}
		if name != "bird" && name != "kea" && !strings.HasPrefix(s.User, "65532:") {
			t.Errorf("%s runs as %q, not as the unprivileged user 65532", name, s.User)
		}
	}
	for name := range wantCaps {
		if _, ok := f.Services[name]; !ok {
			t.Errorf("the service %s of plan §3.8 is not in compose.yaml", name)
		}
	}
	// the table of plan §3.8 names every capability a container has: one the plan does not name is a deviation from the plan, not
	// only from this test (a container may have fewer than its row, as tls, which listens above 1024)
	plan, err := os.ReadFile("../../docs/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	caps := regexp.MustCompile("`([A-Z_]+)`")
	for name, want := range wantCaps {
		var cols []string
		for _, l := range strings.Split(string(plan), "\n") {
			if c := strings.Split(l, "|"); len(c) > 3 && strings.HasPrefix(l, "| `") && strings.Contains(c[1], "`"+name+"`") {
				cols = c
				break
			}
		}
		if cols == nil {
			t.Errorf("the table of plan §3.8 has no row for %s", name)
			continue
		}
		var listed []string
		for _, m := range caps.FindAllStringSubmatch(cols[2], -1) {
			listed = append(listed, m[1])
		}
		for _, c := range want {
			if !slices.Contains(listed, c) {
				t.Errorf("compose.yaml gives %s the capability %s, plan §3.8 lists %v", name, c, listed)
			}
		}
	}
	if f.Services["svcns"].NetworkMode != "none" {
		t.Errorf("the holder has network %q, want none", f.Services["svcns"].NetworkMode)
	}
	for _, n := range []string{"dns", "tls", "tls-proxy"} {
		if f.Services[n].NetworkMode != "service:svcns" {
			t.Errorf("%s: network_mode %q, want service:svcns", n, f.Services[n].NetworkMode)
		}
	}
	for _, n := range []string{"exec", "api", "kea", "bird", "teardown"} {
		if f.Services[n].NetworkMode != "host" {
			t.Errorf("%s: network_mode %q, want host", n, f.Services[n].NetworkMode)
		}
	}
}

// Start order and health checks (plan §3.8): the executor first, then the holder, the API and Kea, then the services that join the
// service namespace; every container that stays has a health check and a restart policy, which is what the dependencies
// wait for.
func TestTheStartOrderFollowsPlan38AndEveryServiceHasAHealthCheck(t *testing.T) {
	f := load(t, "compose.yaml")
	for name, s := range f.Services {
		if name == "teardown" {
			if s.Restart != "" || len(s.Profiles) != 1 || s.Profiles[0] != "teardown" {
				t.Errorf("teardown runs once and on demand: restart %q, profiles %v", s.Restart, s.Profiles)
			}
			continue
		}
		if s.Restart != "unless-stopped" {
			t.Errorf("%s: restart %q: containers come back after a reboot", name, s.Restart)
		}
		if s.Healthcheck == nil {
			t.Errorf("%s has no health check", name)
		}
		for dep, d := range s.DependsOn {
			if _, ok := f.Services[dep]; !ok {
				t.Errorf("%s depends on %s, which compose.yaml does not define", name, dep)
			}
			if d.Required != nil {
				t.Errorf("%s: the dependency on %s is optional in compose.yaml, which has every service", name, dep)
			}
			if d.Condition != "service_healthy" {
				t.Errorf("%s waits for %s with %q, want service_healthy", name, dep, d.Condition)
			}
		}
	}
	if len(f.Services["tls-proxy"].Profiles) != 1 || f.Services["tls-proxy"].Profiles[0] != "interception" {
		t.Errorf("the interception proxy is optional: profiles %v", f.Services["tls-proxy"].Profiles)
	}
	// the executor is an ancestor of every container of the gateway: nothing starts before its socket answers
	var ancestors func(name string, seen map[string]bool)
	ancestors = func(name string, seen map[string]bool) {
		for dep := range f.Services[name].DependsOn {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			ancestors(dep, seen)
		}
	}
	for name := range f.Services {
		if name == "exec" || name == "teardown" {
			continue
		}
		seen := map[string]bool{}
		ancestors(name, seen)
		if !seen["exec"] {
			t.Errorf("%s does not wait for the executor", name)
		}
		if seen[name] {
			t.Errorf("%s depends on itself", name)
		}
	}
	for _, n := range []string{"dns", "tls", "tls-proxy"} {
		d := f.Services[n].DependsOn
		if d["svcns"].Condition == "" || !d["svcns"].Restart || d["api"].Condition == "" {
			t.Errorf("%s joins the holder's namespace and uses the API: depends_on %+v", n, d)
		}
	}
	if len(f.Services["exec"].DependsOn) != 0 {
		t.Errorf("the executor starts first: %v", f.Services["exec"].DependsOn)
	}
}

// Persistence (plan §3.6, decided 2026-10-04, M2-08): the data of the gateway is in bind mounts at the paths of the layout, which
// the host setup makes with their owners; Compose must not create a missing one (root's, which the API's user could not
// write). The sockets between the containers are the only named volumes.
func TestPersistentDataIsInBindMountsAtThePathsOfThePlan(t *testing.T) {
	f := load(t, "compose.yaml")
	dataDirs := map[string]bool{}
	for _, d := range []string{"state", "secrets", "api", "service", "tlsca", "captures", "exec", "kea"} {
		dataDirs["/var/lib/chaosgw/"+d] = true
	}
	hostFiles := map[string]bool{"/run/netns": true, "/lib/modules": true, "/etc": true, "/usr/lib/systemd/network": true, "/run/systemd/resolve": true}
	used := map[string]bool{}
	boundData := map[string]bool{}
	for name, s := range f.Services {
		for _, m := range s.Volumes {
			switch {
			case m.Type == "bind" && dataDirs[m.Source]:
				boundData[m.Source] = true
				if m.Bind.CreateHostPath == nil || *m.Bind.CreateHostPath {
					t.Errorf("%s: %s may be created by Compose as root's directory: create_host_path must be false", name, m.Source)
				}
				if m.Source != m.Target && m.Source != "/var/lib/chaosgw/kea" {
					t.Errorf("%s: %s is mounted at %s", name, m.Source, m.Target)
				}
			case m.Type == "bind" && hostFiles[m.Source]:
			case m.Type == "bind":
				t.Errorf("%s: unexpected bind mount of %s", name, m.Source)
			case m.Type == "volume":
				used[m.Source] = true
				if _, ok := f.Volumes[m.Source]; !ok {
					t.Errorf("%s mounts the volume %s, which compose.yaml does not declare", name, m.Source)
				}
				if strings.Contains(m.Target, "/var/lib/chaosgw") {
					t.Errorf("%s keeps %s in the named volume %s: the layout of plan §3.6 is a bind mount", name, m.Target, m.Source)
				}
			default:
				t.Errorf("%s: mount of type %q", name, m.Type)
			}
		}
	}
	for v := range f.Volumes {
		if !used[v] {
			t.Errorf("the volume %s is declared and no service mounts it", v)
		}
	}
	for d := range dataDirs {
		if !boundData[d] {
			t.Errorf("no service mounts %s", d)
		}
	}
	// the writers: the API user's directories are the API's alone, the others read them
	rw := func(svc, dir string) bool {
		for _, m := range f.Services[svc].Volumes {
			if m.Source == dir {
				return !m.ReadOnly
			}
		}
		return false
	}
	for _, d := range []string{"state", "secrets", "api", "service", "tlsca", "captures"} {
		if !rw("api", "/var/lib/chaosgw/"+d) {
			t.Errorf("the API does not write /var/lib/chaosgw/%s", d)
		}
	}
	for _, s := range []string{"dns", "tls", "tls-proxy", "kea"} {
		if rw(s, "/var/lib/chaosgw/service") || rw(s, "/var/lib/chaosgw/tlsca") {
			t.Errorf("%s writes the service token or the CAs", s)
		}
	}
	if rw("exec", "/var/lib/chaosgw/secrets") {
		t.Error("the executor writes the secrets: it only reads the keys")
	}
}

func TestEveryContainerRunsTheSameImageWhichTheEnvironmentSelects(t *testing.T) {
	f := load(t, "compose.yaml")
	for name, s := range f.Services {
		if s.Image != "${CHAOSGW_IMAGE:-chaos-gateway}:${CHAOSGW_VERSION:-latest}" {
			t.Errorf("%s: image %q", name, s.Image)
		}
	}
	if f.Name != "chaosgw" {
		t.Errorf("project name %q: the volumes and the instructions use chaosgw", f.Name)
	}
}

// The port of the UI and the API is the one the setup configured, and .env names it (CHAOSGW_UI_PORT, env.example): the health check of
// the API asks that port, not the default of the flag, or an installation with another port has an unhealthy API container and the
// containers that wait for it (dns, tls, tls-proxy, and `docker compose up --wait` of update.sh) never start.
func TestThePortOfTheAPIFollowsTheEnvironmentInTheCommandAndTheHealthCheck(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(deployDir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Services map[string]struct {
			Command     []string `yaml:"command"`
			Healthcheck struct {
				Test []string `yaml:"test"`
			} `yaml:"healthcheck"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	const port = "${CHAOSGW_UI_PORT:-8443}"
	hasPort := func(args []string) bool {
		for i, a := range args {
			if a == "--port" && i+1 < len(args) && args[i+1] == port {
				return true
			}
		}
		return false
	}
	api := f.Services["api"]
	if !hasPort(api.Command) {
		t.Errorf("the API listens on the port of the environment: command %v", api.Command)
	}
	if !hasPort(api.Healthcheck.Test) {
		t.Errorf("the health check of the API asks the port of the environment: %v", api.Healthcheck.Test)
	}
	// no container names the default port as a literal
	for name, s := range f.Services {
		for _, a := range append(append([]string{}, s.Command...), s.Healthcheck.Test...) {
			if strings.Contains(strings.ReplaceAll(a, port, ""), "8443") {
				t.Errorf("%s: %q names port 8443 instead of %s", name, a, port)
			}
		}
	}
}

// A file that composegen does not know how to merge, a dependency that nothing defines and a service defined twice are
// refused, not guessed.
func TestGenerateRefusesWhatItCannotMerge(t *testing.T) {
	copyDeploy := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, f := range files {
			raw, err := os.ReadFile(filepath.Join(deployDir, f))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, f), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	edit := func(t *testing.T, dir, file, old, new string) {
		t.Helper()
		raw, _ := os.ReadFile(filepath.Join(dir, file))
		if !strings.Contains(string(raw), old) {
			t.Fatalf("%s has no %q", file, old)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(strings.Replace(string(raw), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := copyDeploy(t)
	if _, err := Generate(dir); err != nil {
		t.Fatalf("the files as they are: %v", err)
	}
	dir = copyDeploy(t)
	edit(t, dir, "compose.bird.yaml", "      exec:\n        condition: service_healthy", "      nothere:\n        condition: service_healthy")
	if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), "depends on nothere") {
		t.Errorf("a dependency nothing defines: %v", err)
	}
	dir = copyDeploy(t)
	edit(t, dir, "compose.bird.yaml", "services:\n  bird:", "services:\n  exec:")
	if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("a service defined twice: %v", err)
	}
	dir = copyDeploy(t)
	edit(t, dir, "compose.bird.yaml", "\nvolumes:\n", "\nnetworks:\n  x: {}\nvolumes:\n")
	if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), "networks") {
		t.Errorf("a top-level key composegen does not merge: %v", err)
	}
}
