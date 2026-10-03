package executor_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// service is the part of a compose service this test pins.
type service struct {
	Pid         string    `yaml:"pid"`
	DependsOn   yaml.Node `yaml:"depends_on"`
	Privileged  bool      `yaml:"privileged"`
	ReadOnly    bool      `yaml:"read_only"`
	NetworkMode string    `yaml:"network_mode"`
	Restart     string    `yaml:"restart"`
	User        string    `yaml:"user"`
	SecurityOpt []string  `yaml:"security_opt"`
	Command     []string  `yaml:"command"`
	Tmpfs       []string  `yaml:"tmpfs"`
	CapAdd      []string  `yaml:"cap_add"`
	CapDrop     []string  `yaml:"cap_drop"`
	Healthcheck struct {
		Test []string `yaml:"test"`
	} `yaml:"healthcheck"`
	Volumes []yaml.Node `yaml:"volumes"`
}

// The hardening profile (plan M3): the executor is the only privileged container and is otherwise
// closed down. A change to these settings must be deliberate.
func TestExecutorContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.executor.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 1 {
		t.Fatalf("the profile describes exactly the executor, got %d services", len(doc.Services))
	}
	s, ok := doc.Services["exec"]
	if !ok {
		t.Fatal("no service exec")
	}
	if !s.Privileged {
		t.Error("the executor is the privileged container (plan §3.8)")
	}
	if !s.ReadOnly {
		t.Error("the root file system must be read-only")
	}
	if !contains(s.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("no-new-privileges missing: %v", s.SecurityOpt)
	}
	if s.NetworkMode != "host" {
		t.Errorf("network_mode %q: the executor configures the host's network stack", s.NetworkMode)
	}
	if s.Pid != "host" {
		t.Errorf("pid %q: the executor attaches the namespace of the service holder by its PID", s.Pid)
	}
	if s.Restart != "unless-stopped" {
		t.Errorf("restart %q", s.Restart)
	}
	if len(s.CapAdd) != 0 {
		t.Errorf("cap_add %v is pointless next to privileged and hides what is needed", s.CapAdd)
	}
	if len(s.Healthcheck.Test) < 3 || s.Healthcheck.Test[1] != "chaosgw" || !contains(s.Healthcheck.Test, "--health") {
		t.Errorf("health check: %v", s.Healthcheck.Test)
	}

	// the command is the real subcommand with flags it understands
	if len(s.Command) < 1 || s.Command[0] != "exec" {
		t.Fatalf("command %v: the image's entrypoint is chaosgw, the command starts with the subcommand", s.Command)
	}
	known := map[string]bool{"--socket": true, "--state": true, "--allow-uid": true, "--socket-owner": true, "--secrets-dir": true, "--bird-dir": true}
	for _, a := range s.Command[1:] {
		if strings.HasPrefix(a, "--") && !known[a] {
			t.Errorf("flag %s is not a flag of `chaosgw exec`", a)
		}
	}
	if !contains(s.Command, "--allow-uid") {
		t.Error("the API container's user must be allowed explicitly: the socket is not world-accessible")
	}

	// writable places: the socket volume, the state volume and a private tmpfs; the host's modules
	// are read-only; nothing else is mounted (no Docker socket, no host root)
	var mounts []string
	for _, v := range s.Volumes {
		switch v.Kind {
		case yaml.ScalarNode:
			mounts = append(mounts, v.Value)
		case yaml.MappingNode:
			var m struct {
				Source   string `yaml:"source"`
				Target   string `yaml:"target"`
				ReadOnly bool   `yaml:"read_only"`
			}
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			if (m.Target == "/lib/modules" || m.Target == "/var/lib/chaosgw/secrets") && !m.ReadOnly {
				t.Error("/lib/modules must be mounted read-only")
			}
			mounts = append(mounts, m.Source+":"+m.Target)
		}
	}
	for _, m := range mounts {
		if strings.Contains(m, "docker.sock") || strings.HasPrefix(m, "/:") || strings.HasPrefix(m, "/etc") || strings.HasPrefix(m, "/var/run:") {
			t.Errorf("unexpected mount %q", m)
		}
	}
	if len(mounts) != 6 {
		t.Errorf("mounts %v: want the socket, BIRD, state and secrets volumes, /run/netns and /lib/modules", mounts)
	}
	if len(s.Tmpfs) != 2 || !strings.Contains(s.Tmpfs[0], "mode=0700") {
		t.Errorf("tmpfs %v", s.Tmpfs)
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

// The BIRD container is not privileged: the network capabilities it needs and nothing else, a
// read-only root and the volume it shares with the executor.
func TestBirdContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.bird.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Services["bird"]
	if len(doc.Services) != 1 || !ok {
		t.Fatalf("services %v", doc.Services)
	}
	if s.Privileged || !s.ReadOnly || s.NetworkMode != "host" || !contains(s.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("%+v", s)
	}
	for _, c := range s.CapAdd {
		if c != "NET_ADMIN" && c != "NET_RAW" && c != "NET_BIND_SERVICE" {
			t.Errorf("capability %s is not needed by a routing daemon", c)
		}
	}
	// BIRD starts from the file the executor keeps and listens where the executor reconfigures it
	if !contains(s.Command, "/run/chaosgw/bird/chaosgw.conf") || !contains(s.Command, "/run/chaosgw/bird/chaosgw.ctl") {
		t.Errorf("command %v", s.Command)
	}
	if len(s.Volumes) != 1 {
		t.Errorf("volumes %v", s.Volumes)
	}
}

// The API container is unprivileged: the user the executor lets through, no capabilities, a
// read-only root and exactly the volumes it needs.
func TestApiContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.api.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Services["api"]
	if len(doc.Services) != 1 || !ok {
		t.Fatalf("services %v", doc.Services)
	}
	if s.Privileged || !s.ReadOnly || !contains(s.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("%+v", s)
	}
	if len(s.CapAdd) != 0 {
		t.Errorf("the API container needs no capability: %v", s.CapAdd)
	}
	if !contains(s.CapDrop, "ALL") {
		t.Errorf("cap_drop %v", s.CapDrop)
	}
	// the user is the one the executor's socket lets through (compose.executor.yaml: --allow-uid)
	if s.User != "65532:65532" {
		t.Errorf("user %q", s.User)
	}
	if s.NetworkMode != "host" {
		t.Errorf("network_mode %q: the API listens on the management network of the host", s.NetworkMode)
	}
	if len(s.Command) < 1 || s.Command[0] != "api" {
		t.Fatalf("command %v", s.Command)
	}
	known := map[string]bool{"--socket": true, "--state-dir": true, "--secrets-dir": true, "--data-dir": true, "--port": true, "--executor-uid": true, "--kea-socket": true, "--service-token-file": true, "--service-ns": true, "--service-holder-pid-file": true}
	for _, a := range s.Command[1:] {
		if strings.HasPrefix(a, "--") && !known[a] {
			t.Errorf("flag %s is not a flag of `chaosgw api`", a)
		}
	}
	if len(s.Volumes) != 8 {
		t.Errorf("volumes %d: the socket, the revisions, the secrets, the audit log, Kea's socket, the service token, the host's resolvers and the holder's PID", len(s.Volumes))
	}
	if len(s.Healthcheck.Test) < 4 || s.Healthcheck.Test[1] != "chaosgw" || !contains(s.Healthcheck.Test, "--health") {
		t.Errorf("health check: %v", s.Healthcheck.Test)
	}

	// the executor accepts exactly that user
	exec, err := os.ReadFile("../../deploy/compose.executor.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var edoc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(exec, &edoc); err != nil {
		t.Fatal(err)
	}
	cmd := edoc.Services["exec"].Command
	if !containsPair(cmd, "--allow-uid", "65532") {
		t.Errorf("the executor does not allow uid 65532: %v", cmd)
	}
}

func containsPair(l []string, k, v string) bool {
	for i := 0; i+1 < len(l); i++ {
		if l[i] == k && l[i+1] == v {
			return true
		}
	}
	return false
}

// The Kea container has the capabilities of raw DHCP and nothing else, and no other volume than its
// own state, the control socket and the service token (read-only).
func TestKeaContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.kea.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Services["kea"]
	if len(doc.Services) != 1 || !ok {
		t.Fatalf("services %v", doc.Services)
	}
	if s.Privileged || !s.ReadOnly || s.NetworkMode != "host" || !contains(s.CapDrop, "ALL") {
		t.Errorf("%+v", s)
	}
	allowed := map[string]bool{"NET_RAW": true, "NET_BIND_SERVICE": true, "CHOWN": true, "FOWNER": true, "DAC_OVERRIDE": true}
	for _, c := range s.CapAdd {
		if !allowed[c] {
			t.Errorf("capability %s is not needed by a DHCP server", c)
		}
	}
	if !contains(s.CapAdd, "NET_RAW") || !contains(s.CapAdd, "NET_BIND_SERVICE") {
		t.Errorf("cap_add %v", s.CapAdd)
	}
	// the control socket is shared with the API's group
	if s.User != "0:65532" {
		t.Errorf("user %q", s.User)
	}
	if len(s.Volumes) != 3 {
		t.Errorf("volumes %d", len(s.Volumes))
	}
	for _, v := range s.Volumes {
		if v.Kind == yaml.MappingNode {
			var m struct {
				Target   string `yaml:"target"`
				ReadOnly bool   `yaml:"read_only"`
			}
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			if m.Target == "/var/lib/chaosgw/service" && !m.ReadOnly {
				t.Error("Kea only reads the service token")
			}
		}
	}
}

// The holder of the service namespace has no network, no capability and no privilege; the DNS proxy
// joins its namespace with the one capability that port 53 needs and mounts the service token
// read-only.
func TestDNSContainersHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.dns.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 2 {
		t.Fatalf("services %v", doc.Services)
	}
	holder, dns := doc.Services["svcns"], doc.Services["dns"]
	for name, s := range map[string]service{"svcns": holder, "dns": dns} {
		if s.Privileged || !s.ReadOnly || !contains(s.CapDrop, "ALL") || !contains(s.SecurityOpt, "no-new-privileges:true") || s.User != "65532:65532" {
			t.Errorf("%s: %+v", name, s)
		}
	}
	if holder.NetworkMode != "none" || holder.Pid != "host" || len(holder.CapAdd) != 0 {
		t.Errorf("the holder has no network, sees the host's PIDs and needs no capability: %+v", holder)
	}
	if dns.NetworkMode != "service:svcns" || len(dns.CapAdd) != 1 || dns.CapAdd[0] != "NET_BIND_SERVICE" {
		t.Errorf("the proxy joins the namespace of the holder and binds port 53: %+v", dns)
	}
	if len(dns.Command) < 1 || dns.Command[0] != "dns" {
		t.Fatalf("command %v", dns.Command)
	}
	known := map[string]bool{"--listen": true, "--api": true, "--service-token-file": true, "--api-cert-file": true}
	for _, a := range dns.Command[1:] {
		if strings.HasPrefix(a, "--") && !known[a] {
			t.Errorf("flag %s is not a flag of `chaosgw dns`", a)
		}
	}
	if len(holder.Command) < 1 || holder.Command[0] != "svcns" {
		t.Errorf("command %v", holder.Command)
	}
	for _, v := range dns.Volumes {
		var m struct {
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
		}
		if v.Kind == yaml.MappingNode {
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			if m.Target == "/var/lib/chaosgw/service" && !m.ReadOnly {
				t.Error("the proxy only reads the service token")
			}
		}
	}
	if len(dns.Volumes) != 1 || len(holder.Volumes) != 1 {
		t.Errorf("volumes: the proxy %d, the holder %d", len(dns.Volumes), len(holder.Volumes))
	}
}
