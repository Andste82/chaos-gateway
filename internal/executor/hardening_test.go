package executor_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// networkdDefaults is the mount of systemd-networkd's default files (I64-02): source and target.
const networkdDefaults = "/usr/lib/systemd/network:/host/usr/lib/systemd/network"

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
	Entrypoint  []string  `yaml:"entrypoint"`
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
	known := map[string]bool{"--socket": true, "--state": true, "--allow-uid": true, "--socket-owner": true, "--secrets-dir": true, "--bird-dir": true, "--host-root": true, "--captures-dir": true}
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
			if (m.Target == "/lib/modules" || m.Target == "/var/lib/chaosgw/secrets" || strings.HasPrefix(m.Target, "/host/")) && !m.ReadOnly {
				t.Errorf("%s must be mounted read-only", m.Target)
			}
			mounts = append(mounts, m.Source+":"+m.Target)
		}
	}
	for _, m := range mounts {
		// the host's /etc only read-only at /host/etc, for the netplan, NetworkManager and systemd-networkd files the
		// preflight reads (P3-M12-07), and systemd-networkd's defaults at /host/usr/lib/systemd/network (I64-02);
		// no other part of /usr, and not /run with its sockets
		if strings.Contains(m, "docker.sock") || strings.HasPrefix(m, "/:") || (strings.HasPrefix(m, "/etc") && m != "/etc:/host/etc") ||
			strings.HasPrefix(m, "/var/run:") || strings.HasPrefix(m, "/run:") || strings.HasPrefix(m, "/run/systemd") || (strings.HasPrefix(m, "/usr") && m != networkdDefaults) {
			t.Errorf("unexpected mount %q", m)
		}
	}
	if len(mounts) != 9 || !contains(mounts, "/etc:/host/etc") || !contains(mounts, networkdDefaults) || !contains(s.Command, "/host") {
		t.Errorf("mounts %v: want the socket, BIRD, state, captures and secrets volumes, /run/netns, /lib/modules, the host's /etc and its systemd-networkd defaults (read by --host-root /host)", mounts)
	}
	// the preflight reads the defaults where the mount puts them: below the host root, in a directory of NetworkdDirs
	if dir := strings.TrimPrefix(strings.SplitN(networkdDefaults, ":", 2)[1], "/host/"); !slices.Contains(preflight.NetworkdDirs, dir) {
		t.Errorf("%s is no directory the preflight reads below the host root: %v", dir, preflight.NetworkdDirs)
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
	if !contains(s.Command, "/run/chaosgw-bird/chaosgw.conf") || !contains(s.Command, "/run/chaosgw-bird/chaosgw.ctl") {
		t.Errorf("command %v", s.Command)
	}
	if len(s.Volumes) != 1 {
		t.Errorf("volumes %v", s.Volumes)
	}
	// without the executor's idle chaosgw.conf, BIRD crash-loops (M4c-07)
	var deps map[string]struct {
		Condition string `yaml:"condition"`
	}
	if err := s.DependsOn.Decode(&deps); err != nil || deps["exec"].Condition != "service_healthy" {
		t.Errorf("depends_on: %v, %v", deps, err)
	}
	if len(s.Healthcheck.Test) < 2 || !contains(s.Healthcheck.Test, "birdc") || !contains(s.Healthcheck.Test, "show") || !contains(s.Healthcheck.Test, "status") {
		t.Errorf("health check: %v", s.Healthcheck.Test)
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
	known := map[string]bool{"--socket": true, "--state-dir": true, "--secrets-dir": true, "--data-dir": true, "--port": true, "--executor-uid": true, "--kea-socket": true, "--kea-events-socket": true, "--service-token-file": true, "--service-ns": true, "--service-holder-pid-file": true, "--captures-dir": true, "--tls-ca-dir": true}
	for _, a := range s.Command[1:] {
		if strings.HasPrefix(a, "--") && !known[a] {
			t.Errorf("flag %s is not a flag of `chaosgw api`", a)
		}
	}
	if len(s.Volumes) != 11 {
		t.Errorf("volumes %d: the socket, the revisions, the secrets, the audit log, the captures, the CAs of the TLS responder, Kea's socket, the Kea events socket, the service token, the host's resolvers and the holder's PID", len(s.Volumes))
	}
	// issue #57 F3: the API creates the Kea events socket in a directory of its own, and the Kea hook sends to it there
	if !containsPair(s.Command, "--kea-events-socket", keaEventsSocket) {
		t.Errorf("the API does not serve the Kea events socket at %s: %v", keaEventsSocket, s.Command)
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
	if len(s.Volumes) != 4 {
		t.Errorf("volumes %d", len(s.Volumes))
	}
	readOnly := map[string]bool{}
	for _, v := range s.Volumes {
		if v.Kind == yaml.MappingNode {
			var m struct {
				Target   string `yaml:"target"`
				ReadOnly bool   `yaml:"read_only"`
			}
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			readOnly[m.Target] = m.ReadOnly
		}
	}
	if !readOnly["/var/lib/chaosgw/service"] {
		t.Error("Kea only reads the service token")
	}
	// issue #57 F3: the directory of the events socket is the API's; Kea only sends to the socket
	if !readOnly[filepath.Dir(keaEventsSocket)] {
		t.Errorf("Kea mounts the API's directory of the events socket (%s) read-only", filepath.Dir(keaEventsSocket))
	}
	var env struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if got := env.Services["kea"].Environment["CHAOSGW_KEA_EVENTS_SOCKET"]; got != keaEventsSocket {
		t.Errorf("the hook sends the lease events to %q, the API serves them at %s", got, keaEventsSocket)
	}
}

// keaEventsSocket is where the API serves the Kea events socket and the hook sends to (issue #57 F3).
const keaEventsSocket = "/run/chaosgw-kea-events/events.sock"

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

// The TLS responder joins the namespace of the holder like the DNS proxy, drops every capability (its port is above
// 1024) and mounts the service token and the CAs read-only; the CAs are a volume of their own, so nothing of the API's secrets
// is within its reach.
func TestTLSContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.tls.yaml")
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
		t.Fatalf("services %v", doc.Services)
	}
	tls := doc.Services["tls"]
	if tls.Privileged || !tls.ReadOnly || !contains(tls.CapDrop, "ALL") || len(tls.CapAdd) != 0 || !contains(tls.SecurityOpt, "no-new-privileges:true") || tls.User != "65532:65532" {
		t.Errorf("%+v", tls)
	}
	if tls.NetworkMode != "service:svcns" {
		t.Errorf("the responder joins the namespace of the holder: %+v", tls)
	}
	if len(tls.Command) < 1 || tls.Command[0] != "tls" {
		t.Fatalf("command %v", tls.Command)
	}
	known := map[string]bool{"--listen": true, "--api": true, "--service-token-file": true, "--api-cert-file": true, "--ca-dir": true}
	for _, a := range tls.Command[1:] {
		if strings.HasPrefix(a, "--") && !known[a] {
			t.Errorf("flag %s is not a flag of `chaosgw tls`", a)
		}
	}
	mounted := map[string]bool{}
	for _, v := range tls.Volumes {
		var m struct {
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
		}
		if v.Kind != yaml.MappingNode {
			t.Fatalf("a short volume: %v", v.Value)
		}
		if err := v.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if !m.ReadOnly {
			t.Errorf("the responder only reads %s", m.Target)
		}
		mounted[m.Source+":"+m.Target] = true
	}
	if len(tls.Volumes) != 2 || !mounted["chaosgw-service:/var/lib/chaosgw/service"] || !mounted["chaosgw-tlsca:/var/lib/chaosgw/tlsca"] {
		t.Errorf("volumes: %v", mounted)
	}
	// the responder reads the directory the API writes: the same volume, at the path both flags name
	api, err := os.ReadFile("../../deploy/compose.api.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(api), "chaosgw-tlsca:/var/lib/chaosgw/tlsca") || !strings.Contains(string(api), `"--tls-ca-dir", "/var/lib/chaosgw/tlsca"`) {
		t.Error("the API does not make the CAs where the responder reads them")
	}
}

// The interception proxy (M22) joins the namespace of the holder like the other services, drops every capability, mounts the service token and
// the CAs read-only and listens where the responder looks for it: the addresses of the compose file are the defaults of the responder, and the
// flags are those of the launcher.
func TestTLSProxyContainerHardeningProfile(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/compose.tlsproxy.yaml")
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
		t.Fatalf("services %v", doc.Services)
	}
	p := doc.Services["tls-proxy"]
	if p.Privileged || !p.ReadOnly || !contains(p.CapDrop, "ALL") || len(p.CapAdd) != 0 || !contains(p.SecurityOpt, "no-new-privileges:true") || p.User != "65532:65532" {
		t.Errorf("%+v", p)
	}
	if p.NetworkMode != "service:svcns" {
		t.Errorf("the proxy joins the namespace of the holder: %+v", p)
	}
	if len(p.Entrypoint) != 3 || !strings.HasSuffix(p.Entrypoint[0], "/venv/bin/python") || p.Entrypoint[1] != "-m" || p.Entrypoint[2] != "chaosgw_tlsproxy.launcher" {
		t.Fatalf("entrypoint %v", p.Entrypoint)
	}
	launcher, err := os.ReadFile("../../sidecars/tls-proxy/chaosgw_tlsproxy/launcher.py")
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]string{}
	for i := 0; i < len(p.Command); i++ {
		a := p.Command[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		if !strings.Contains(string(launcher), `"`+a+`"`) {
			t.Errorf("flag %s is not a flag of the launcher", a)
		}
		if i+1 < len(p.Command) {
			args[a] = p.Command[i+1]
		}
	}
	// the responder hands the connections to the address the proxy listens on and the proxy asks the responder where it listens
	if args["--listen"] != tlsresponder.DefaultProxyAddr || args["--control"] != "http://"+tlsresponder.DefaultControlAddr {
		t.Errorf("the proxy listens on %q and asks %q, the responder defaults to %s and %s", args["--listen"], args["--control"], tlsresponder.DefaultProxyAddr, tlsresponder.DefaultControlAddr)
	}
	if !strings.HasPrefix(args["--listen"], "127.0.0.1:") {
		t.Errorf("the proxy listens beyond the loopback of its namespace: %s", args["--listen"])
	}
	if args["--ca-dir"] != "/var/lib/chaosgw/tlsca" || args["--service-token-file"] != "/var/lib/chaosgw/service/token" {
		t.Errorf("%v", args)
	}
	mounted := map[string]bool{}
	for _, v := range p.Volumes {
		var m struct {
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
		}
		if v.Kind != yaml.MappingNode {
			t.Fatalf("a short volume: %v", v.Value)
		}
		if err := v.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if !m.ReadOnly {
			t.Errorf("the proxy only reads %s", m.Target)
		}
		mounted[m.Source+":"+m.Target] = true
	}
	if len(p.Volumes) != 2 || !mounted["chaosgw-service:/var/lib/chaosgw/service"] || !mounted["chaosgw-tlsca:/var/lib/chaosgw/tlsca"] {
		t.Errorf("volumes: %v", mounted)
	}
	// the root file system is read-only, so what mitmproxy writes has a tmpfs, which is the configuration directory
	if len(p.Tmpfs) != 1 || !strings.HasPrefix(p.Tmpfs[0], "/tmp:") || args["--confdir"] != "/tmp/mitmproxy" {
		t.Errorf("tmpfs %v, confdir %q", p.Tmpfs, args["--confdir"])
	}
	// the tmpfs is the proxy's: it runs as 65532, and a tmpfs that is root's with mode 0700 could not be written (found by the smoke test of the container)
	if len(p.Tmpfs) == 1 && (!strings.Contains(p.Tmpfs[0], "uid=65532") || !strings.Contains(p.Tmpfs[0], "gid=65532") || p.User != "65532:65532") {
		t.Errorf("the tmpfs %v is not owned by the user the proxy runs as (%q)", p.Tmpfs, p.User)
	}
	// and the image has the virtualenv and the addon where the file says
	dockerfile, err := os.ReadFile("../../deploy/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/opt/chaosgw/tls-proxy/venv", "mitmproxy==${MITMPROXY_VERSION}", "COPY sidecars/tls-proxy/chaosgw_tlsproxy /opt/chaosgw/tls-proxy/chaosgw_tlsproxy", "COPY sidecars/tls-proxy/entry.py /opt/chaosgw/tls-proxy/entry.py"} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("the Dockerfile lacks %q", want)
		}
	}
	// the version the image installs is the one the testbed image runs the tests against
	var imageVersion, devVersion string
	for _, f := range []struct {
		file string
		out  *string
	}{{"../../deploy/Dockerfile", &imageVersion}, {"../../.devcontainer/Dockerfile", &devVersion}} {
		b, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "ARG MITMPROXY_VERSION="); ok {
				*f.out = v
			}
		}
	}
	if imageVersion == "" || imageVersion != devVersion {
		t.Errorf("mitmproxy %q in the image, %q in the testbed image", imageVersion, devVersion)
	}
}

// Issue #57: the API container runs as 65532, and a named volume that is still empty takes its owner and
// mode from the image's directory. Every named volume the API mounts writable must therefore exist in the
// image with that owner, or the API cannot write to it on a fresh host and does not start. The two
// volumes another container prepares at its start are the exception: the executor's socket directory and
// Kea's control socket directory (deploy/kea-start.sh gives it to the API's group).
func TestTheImageGivesTheApiContainerItsWritableVolumes(t *testing.T) {
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
	preparedByOthers := map[string]bool{"/run/chaosgw": true, "/run/kea": true}
	var writable []string
	for _, v := range doc.Services["api"].Volumes {
		typ, target, readOnly := "volume", "", false
		switch v.Kind {
		case yaml.ScalarNode: // source:target[:ro]
			parts := strings.Split(v.Value, ":")
			if strings.HasPrefix(parts[0], "/") || strings.HasPrefix(parts[0], ".") {
				typ = "bind"
			}
			if len(parts) > 1 {
				target = parts[1]
			}
			readOnly = len(parts) > 2 && parts[2] == "ro"
		case yaml.MappingNode:
			var m struct {
				Type     string `yaml:"type"`
				Target   string `yaml:"target"`
				ReadOnly bool   `yaml:"read_only"`
			}
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			typ, target, readOnly = m.Type, m.Target, m.ReadOnly
		}
		if typ == "volume" && !readOnly && !preparedByOthers[target] {
			writable = append(writable, target)
		}
	}
	if len(writable) != 7 {
		t.Errorf("the API's writable volumes %v: the revisions, the secrets, the audit log, the captures, the CAs of the TLS responder, the service token and the Kea events socket", writable)
	}

	dockerfile, err := os.ReadFile("../../deploy/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	// the arguments of every `chown 65532:65532` of a RUN instruction, continuation lines joined
	owned := map[string]bool{}
	for _, cmd := range strings.Split(strings.ReplaceAll(string(dockerfile), "\\\n", " "), "&&") {
		f := strings.Fields(cmd)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "chown" && f[i+1] == "65532:65532" {
				for _, p := range f[i+2:] {
					owned[p] = true
				}
			}
		}
	}
	for _, target := range writable {
		if !owned[target] {
			t.Errorf("deploy/Dockerfile does not create %s owned by 65532:65532: on a fresh host the API cannot write to it", target)
		}
	}
	if !strings.Contains(string(dockerfile), "chmod 0700 /var/lib/chaosgw/secrets") {
		t.Error("deploy/Dockerfile does not keep the secrets directory 0700")
	}
	// the Kea container (group 65532) searches the directory of the events socket, and nobody else
	if !strings.Contains(string(dockerfile), "chmod 0750 "+filepath.Dir(keaEventsSocket)) {
		t.Errorf("deploy/Dockerfile does not make %s 0750", filepath.Dir(keaEventsSocket))
	}
}

// Issue #57 (F4): no container mounts a volume or bind inside another of its volumes or binds. The
// executor shares /run/netns with the host (rshared), and a volume mounted inside another volume then
// propagated into the host's mount table: BIRD's directory inside the executor's socket volume stayed
// mounted on the host and `docker compose down -v` failed. The CI job `mounts` checks the real mounts.
func TestNoContainerMountsAVolumeInsideAnother(t *testing.T) {
	files, err := filepath.Glob("../../deploy/compose.*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("compose files %v: %v", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]service `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for name, s := range doc.Services {
			var targets []string
			for _, v := range s.Volumes {
				targets = append(targets, volumeTarget(t, v))
			}
			for _, outer := range targets {
				for _, inner := range targets {
					if inner != outer && strings.HasPrefix(inner, strings.TrimSuffix(outer, "/")+"/") {
						t.Errorf("%s: service %s mounts %s inside %s", filepath.Base(f), name, inner, outer)
					}
				}
			}
		}
	}
}

// volumeTarget is the path in the container of a compose volume entry, short (source:target[:mode]) or long.
func volumeTarget(t *testing.T, v yaml.Node) string {
	t.Helper()
	if v.Kind == yaml.ScalarNode {
		parts := strings.Split(v.Value, ":")
		if len(parts) < 2 {
			return parts[0] // an anonymous volume: the target alone
		}
		return parts[1]
	}
	var m struct {
		Target string `yaml:"target"`
	}
	if err := v.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m.Target
}
