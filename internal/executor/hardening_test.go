package executor_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// service is the part of a compose service this test pins.
type service struct {
	Privileged  bool     `yaml:"privileged"`
	ReadOnly    bool     `yaml:"read_only"`
	NetworkMode string   `yaml:"network_mode"`
	Restart     string   `yaml:"restart"`
	User        string   `yaml:"user"`
	SecurityOpt []string `yaml:"security_opt"`
	Command     []string `yaml:"command"`
	Tmpfs       []string `yaml:"tmpfs"`
	CapAdd      []string `yaml:"cap_add"`
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
	known := map[string]bool{"--socket": true, "--state": true, "--allow-uid": true, "--socket-owner": true}
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
			if m.Target == "/lib/modules" && !m.ReadOnly {
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
	if len(mounts) != 4 {
		t.Errorf("mounts %v: want the socket and state volumes, /run/netns and /lib/modules", mounts)
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
