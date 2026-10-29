package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/probe"
)

// appliedGateway applies the compiler's test configuration to a simulated kernel through an executor that keeps its state in
// stateFile (so that a later executor knows which interfaces belong to the gateway), and makes the commands run on that
// kernel. It returns the kernel, the socket of the executor that did it (it is serving until the test ends) and a function
// that lists what of the gateway is left.
func appliedGateway(t *testing.T, stateFile string) (*kernelsim.Kernel, string, func() []string) {
	t.Helper()
	k, sock, _ := startExecutorWithKeys(t, "", executor.WithStateFile(stateFile))
	if code, out, errOut := runCmd("apply", "--file", configFile(t), "--socket", sock, "--executor-uid", uid()); code != 0 {
		t.Fatalf("apply: code %d\n%s\n%s", code, out, errOut)
	}
	old := newExecRunner
	newExecRunner = func() executor.Runner { return k }
	t.Cleanup(func() { newExecRunner = old })
	left := func() []string {
		t.Helper()
		ex, err := executor.New(k)
		if err != nil {
			t.Fatal(err)
		}
		defer ex.Close()
		s, err := apply.ReadState(context.Background(), apply.Local{E: ex}, "", apply.Want{})
		if err != nil {
			t.Fatal(err)
		}
		return apply.Leftovers(s, []string{"br-iot", "br-lab", "svc0"})
	}
	if l := left(); len(l) == 0 {
		t.Fatal("the applied gateway left nothing to remove")
	}
	return k, sock, left
}

// The stop of the executor removes everything the gateway created, not only the overlays (plan §3.8, D35).
func TestExecRemovesEverythingItCreatedWhenItStops(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the executor refuses to run unprivileged")
	}
	dir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	state := filepath.Join(dir, "state.json")
	_, _, left := appliedGateway(t, state)

	sock := filepath.Join(dir, "e.sock")
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"exec", "-socket", sock, "-state", state}, &out, &errb) }()
	for i := 0; ; i++ {
		var o, e bytes.Buffer
		if run([]string{"exec", "-health", "-socket", sock}, &o, &e) == 0 {
			break
		}
		if i > 100 {
			t.Fatalf("the executor did not come up: %s", errb.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(left()) == 0 {
		t.Fatal("starting the executor removed the gateway's state")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-done:
		if c != 0 {
			t.Fatalf("exit code %d: %s", c, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the executor did not stop on SIGTERM")
	}
	if l := left(); len(l) != 0 {
		t.Errorf("left after the stop: %v\nlog: %s", l, errb.String())
	}
	if !strings.Contains(errb.String(), "stop cleanup") {
		t.Errorf("the log does not name the cleanup:\n%s", errb.String())
	}
}

func TestTeardownRemovesWhatAKilledGatewayLeftAndRefusesWhileAnExecutorServes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("teardown refuses to run unprivileged")
	}
	state := filepath.Join(t.TempDir(), "state.json")
	_, sock, left := appliedGateway(t, state)

	// an executor is serving on the socket: two writers of the kernel are refused
	code, _, errOut := runCmd("teardown", "-socket", sock, "-state", state)
	if code != 1 || !strings.Contains(errOut, "an executor is serving") {
		t.Fatalf("code %d: %s", code, errOut)
	}
	if len(left()) == 0 {
		t.Fatal("the refused teardown changed the kernel")
	}

	code, out, errOut := runCmd("teardown", "-socket", filepath.Join(t.TempDir(), "none.sock"), "-state", state)
	if code != 0 || !strings.Contains(out, "Everything Chaos Gateway created in the kernel is removed.") || !strings.Contains(out, "delete bridge br-iot") {
		t.Fatalf("code %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	if l := left(); len(l) != 0 {
		t.Errorf("left: %v", l)
	}
	// it can be repeated
	if code, _, errOut := runCmd("teardown", "-socket", filepath.Join(t.TempDir(), "none.sock"), "-state", state); code != 0 {
		t.Errorf("the second teardown: code %d: %s", code, errOut)
	}
}

// --purge empties the volumes only once the kernel is clean: a teardown that fails leaves them (the state file names the
// interfaces assigned to the executor, which a second run needs) and says so, and the second run purges.
func TestPurgeLeavesTheVolumesWhenTheKernelCleanupFails(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("teardown refuses to run unprivileged")
	}
	root := t.TempDir()
	state := filepath.Join(root, "exec", "state.json")
	k, _, left := appliedGateway(t, state)
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "revision"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := dataDirs
	dataDirs = []struct{ path, what string }{{data, "the revisions"}}
	t.Cleanup(func() { dataDirs = old })

	// a bridge that cannot be deleted (busy)
	k.SetFail(func(argv []string, stdin string) *executor.Result {
		if s := strings.Join(argv, " ") + stdin; strings.Contains(s, "br-iot") && strings.Contains(s, "del") {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Device or resource busy\n"}
		}
		return nil
	})
	none := filepath.Join(t.TempDir(), "none.sock")
	code, out, errOut := runCmd("teardown", "-purge", "-socket", none, "-state", state)
	if code != 1 || !strings.Contains(errOut, "the volumes are not removed because the kernel is not clean") || strings.Contains(out, "Removed "+data) {
		t.Fatalf("code %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(data, "revision")); err != nil {
		t.Errorf("the volume was emptied although the kernel is not clean: %v", err)
	}
	if len(left()) == 0 {
		t.Fatal("the failing teardown removed everything, so the test checks nothing")
	}

	k.SetFail(nil)
	code, out, errOut = runCmd("teardown", "-purge", "-socket", none, "-state", state)
	if code != 0 || !strings.Contains(out, "Removed "+data) {
		t.Fatalf("code %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	if entries, err := os.ReadDir(data); err != nil || len(entries) != 0 {
		t.Errorf("%v %v", entries, err)
	}
	if l := left(); len(l) != 0 {
		t.Errorf("left: %v", l)
	}
}

func TestTeardownArguments(t *testing.T) {
	if code, _, _ := runCmd("teardown", "extra"); code != 2 {
		t.Errorf("positional argument: code %d", code)
	}
	if code, _, _ := runCmd("teardown", "-bogus"); code != 2 {
		t.Errorf("unknown flag: code %d", code)
	}
	if code, _, _ := runCmd("teardown", "-timeout", "0s"); code != 2 {
		t.Errorf("zero timeout: code %d", code)
	}
}

func TestPurgeEmptiesTheVolumesThatAreMountedAndNamesTheOthers(t *testing.T) {
	root := t.TempDir()
	state, secrets := filepath.Join(root, "state"), filepath.Join(root, "secrets")
	for _, d := range []string{state, secrets} {
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "sub", "file"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "top"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	code := purgeVolumes([]struct{ path, what string }{{state, "the revisions"}, {secrets, "the secrets"}, {filepath.Join(root, "api"), "the audit log"}}, &out, &errb)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	for _, d := range []string{state, secrets} {
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) != 0 {
			t.Errorf("%s: %v %v", d, entries, err)
		}
	}
	if !strings.Contains(out.String(), "Removed "+state) || !strings.Contains(out.String(), "Not mounted in this container, so not removed: "+filepath.Join(root, "api")) {
		t.Errorf("output:\n%s", out.String())
	}
	// the data directories are the host's (bind mounts), which `down -v` leaves alone: the message must not send the administrator
	// there for secrets that stay
	if !strings.Contains(out.String(), "rm -rf on the host") || strings.Contains(out.String(), "removes the volume") {
		t.Errorf("the message about a directory that stays points to the wrong command:\n%s", out.String())
	}
}

func TestPurgeNamesAReadOnlyDirectoryAsOneToRemoveOnTheHost(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory whose mode forbids it")
	}
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := purgeVolumes([]struct{ path, what string }{{dir, "the secrets"}}, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "rm -rf on the host") || strings.Contains(errb.String(), "removes the volume") {
		t.Errorf("message:\n%s", errb.String())
	}
}

// orderSocket is the DHCP socket of a probe in a test: nobody answers, and closing it (the client ended) says when.
type orderSocket struct {
	done    chan struct{}
	onClose func()
}

func (s *orderSocket) Send([]byte, netip.AddrPort) error { return nil }
func (s *orderSocket) Recv() ([]byte, error)             { <-s.done; return nil, net.ErrClosed }
func (s *orderSocket) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
		s.onClose()
	}
	return nil
}

// The first step of the stop is to stop the DHCP clients of the probes (plan §3.8): no command of the cleanup has run when
// the client of a probe ends, so that no client configures a probe's interface while its namespace and pair are removed.
func TestTheStopCleanupStopsTheDHCPClientsOfTheProbesBeforeItRunsAnyCommand(t *testing.T) {
	var cmds atomic.Int64
	var mu sync.Mutex
	var closedAfter []int64
	state := filepath.Join(t.TempDir(), "state.json")
	k, sock, ex := startExecutorWithKeys(t, "", executor.WithStateFile(state), executor.WithProbeSocket(func(ns, ifname string) (probe.Socket, error) {
		return &orderSocket{done: make(chan struct{}), onClose: func() {
			mu.Lock()
			defer mu.Unlock()
			closedAfter = append(closedAfter, cmds.Load())
		}}, nil
	}))
	raw, err := os.ReadFile(configFile(t))
	if err != nil {
		t.Fatal(err)
	}
	withProbe := filepath.Join(t.TempDir(), "probes.yaml")
	probes := "\nprobes:\n  3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8:\n    name: probe-a\n    network: 0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21\n"
	if err := os.WriteFile(withProbe, append(raw, probes...), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCmd("apply", "--file", withProbe, "--socket", sock, "--executor-uid", uid()); code != 0 {
		t.Fatalf("apply: code %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(strings.Join(k.Log, "\n"), "cgp-3a1f7c52") {
		t.Fatal("the probe was not made, so the test checks nothing")
	}
	mu.Lock()
	n := len(closedAfter)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the client of the probe ended before the stop (%d)", n)
	}

	k.SetAfter(func([]string, string) { cmds.Add(1) })
	if err := stopCleanup(ex, "cgsvc", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(closedAfter) != 1 || closedAfter[0] != 0 {
		t.Errorf("the client of the probe ended after %v commands of the cleanup: it is stopped before any", closedAfter)
	}
	if cmds.Load() == 0 {
		t.Error("the cleanup ran no command")
	}
}

// `docker compose run teardown --purge` can only remove what the container mounts: the teardown service of the shipped deployment
// mounts every directory --purge empties, writable, from the directory of the host (plan §3.6) that the containers which write
// it mount (the executor's own container mounts the secrets read-only and has no revisions, audit log, service token or TLS CAs).
func TestTheTeardownServiceMountsEveryDirectoryPurgeEmptiesWritable(t *testing.T) {
	type composeFile struct {
		Services map[string]struct {
			Entrypoint []string    `yaml:"entrypoint"`
			Privileged bool        `yaml:"privileged"`
			Profiles   []string    `yaml:"profiles"`
			Volumes    []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	read := func(name string) composeFile {
		t.Helper()
		raw, err := os.ReadFile("../../deploy/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var f composeFile
		if err := yaml.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	td := read("compose.teardown.yaml")
	s, ok := td.Services["teardown"]
	if !ok || len(td.Services) != 1 {
		t.Fatalf("services %v: want exactly teardown", td.Services)
	}
	if !s.Privileged || len(s.Profiles) != 1 || s.Profiles[0] != "teardown" {
		t.Errorf("privileged %v, profiles %v: it removes interfaces and tables, and `up` must never start it", s.Privileged, s.Profiles)
	}
	if len(s.Entrypoint) < 2 || s.Entrypoint[0] != "chaosgw" || s.Entrypoint[1] != "teardown" {
		t.Errorf("entrypoint %v: `compose run teardown --purge` replaces the command, so the subcommand is in the entrypoint", s.Entrypoint)
	}
	mount := func(v yaml.Node) (source, target string, readOnly bool) {
		if v.Kind == yaml.ScalarNode {
			parts := strings.Split(v.Value, ":")
			return parts[0], parts[1], len(parts) > 2 && parts[2] == "ro"
		}
		var m struct {
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
		}
		if err := v.Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m.Source, m.Target, m.ReadOnly
	}
	// mountedBy holds the places the other compose files mount a source at ("source target"): the teardown service must mount a
	// directory where the container that writes to it does, or it empties another directory
	mountedBy := map[string]bool{}
	for _, f := range []string{"compose.executor.yaml", "compose.api.yaml", "compose.bird.yaml", "compose.kea.yaml"} {
		other := read(f)
		for _, svc := range other.Services {
			for _, v := range svc.Volumes {
				source, target, _ := mount(v)
				mountedBy[source+" "+target] = true
			}
		}
	}
	mounted := map[string]string{}
	for _, v := range s.Volumes {
		source, target, readOnly := mount(v)
		if readOnly {
			t.Errorf("%s is mounted read-only", target)
		}
		mounted[target] = source
	}
	for _, d := range dataDirs {
		src, ok := mounted[d.path]
		if !ok {
			t.Errorf("%s (%s) is not mounted in the teardown service", d.path, d.what)
			continue
		}
		if !strings.HasPrefix(src, "/var/lib/chaosgw/") {
			t.Errorf("%s: mounted from %q, which is no directory of the layout of plan §3.6", d.path, src)
		}
		if !mountedBy[src+" "+d.path] {
			t.Errorf("%s: no other compose file mounts %q at that path", d.path, src)
		}
	}
	// the executor's socket and the directory shared with BIRD are the executor's places too
	for target, source := range map[string]string{"/run/chaosgw": "chaosgw-run", "/run/chaosgw-bird": "chaosgw-bird"} {
		if mounted[target] != source || !mountedBy[source+" "+target] {
			t.Errorf("%s: the teardown service mounts %q (want %q, at the place the executor mounts it)", target, mounted[target], source)
		}
	}
}

// The API process ends in the order of plan §3.11: when the signal comes, the engine is still there to remove the overlays
// (the last apply is one without them), and the stop is recorded in the audit log after that.
func TestTheAPIRemovesItsOverlaysWhenItStopsAndRecordsTheStop(t *testing.T) {
	root := t.TempDir()
	secretsDir := filepath.Join(root, "secrets")
	k, sock, _ := startExecutorWithKeys(t, secretsDir)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	logs := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveAPI(ctx, slog.New(slog.NewTextHandler(logs, nil)), logs, apiOptions{socket: sock, execUID: uint32(os.Getuid()), stateDir: filepath.Join(root, "state"),
			secretsDir: secretsDir, dataDir: filepath.Join(root, "data"), tlsCADir: filepath.Join(root, "tlsca"), port: port, listen: fmt.Sprintf("127.0.0.1:%d", port), poll: time.Second, confirm: 20 * time.Second})
	}()
	stopped := false
	defer func() {
		if !stopped {
			cancel()
			<-done
		}
	}()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	base := fmt.Sprintf("https://127.0.0.1:%d/api/v1", port)
	call := func(method, path string, body any, hdr map[string]string) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return res.StatusCode, m
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if res, err := client.Get(base + "/system/health"); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server does not come up:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	tok := regexp.MustCompile(`Setup token: (setup_\S+)`).FindStringSubmatch(logs.String())
	if tok == nil {
		t.Fatalf("no setup token in the log:\n%s", logs.String())
	}
	cfgRaw, err := os.ReadFile(wgConfigFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if code, res := call("POST", "/setup", map[string]any{"admin_password": "a long enough password", "configuration": yamlToJSON(t, cfgRaw)}, map[string]string{"X-Setup-Token": tok[1]}); code != 200 {
		t.Fatalf("setup: %d %v\n%s", code, res, logs.String())
	}
	code, sess := call("POST", "/auth/login", map[string]any{"password": "a long enough password"}, nil)
	if code != 200 {
		t.Fatalf("login: %d %v", code, sess)
	}
	csrf := map[string]string{"X-CSRF-Token": sess["csrf_token"].(string)}
	if code, cf := call("POST", "/revisions/1/confirm", nil, csrf); code != 200 {
		t.Fatalf("confirming the setup: %d %v", code, cf)
	}
	if code, ov := call("POST", "/overlays", map[string]any{"target": map[string]any{"network": "IoT"}, "fault": map[string]any{"latency": "50ms"}}, csrf); code != 201 {
		t.Fatalf("the overlay: %d %v", code, ov)
	}
	faults := func() bool {
		t.Helper()
		res, err := k.Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "list", "table", "inet", "chaosgw"}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(res.Stdout, "mark_")
	}
	if !faults() {
		t.Fatal("the kernel has no fault of the overlay before the stop")
	}

	// the last apply, the one that removes the overlay, is held for a while after it ran: plan §3.11 stops the observers after
	// it, so the poll of the observed state (every second) must still read the kernel meanwhile
	var armed, holding atomic.Bool
	var observed atomic.Int32
	k.SetAfter(func(argv []string, _ string) {
		switch {
		case holding.Load():
			observed.Add(1)
		case armed.Load() && argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") && armed.CompareAndSwap(true, false):
			holding.Store(true)
			time.Sleep(2500 * time.Millisecond)
			holding.Store(false)
		}
	})
	armed.Store(true)
	cancel()
	stopped = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAPI: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server does not stop")
	}
	if armed.Load() {
		t.Error("the stop made no nftables apply: the overlay was not removed by the last apply")
	}
	if observed.Load() == 0 {
		t.Error("the observers had stopped before the last apply was through (plan §3.11: stop accepting requests, abort runs, the last apply, then the observers)")
	}
	if strings.Contains(logs.String(), "were not removed") {
		t.Errorf("the overlays were not removed by the API:\n%s", logs.String())
	}
	if faults() {
		t.Error("the fault of the overlay is still in the kernel after the stop")
	}
	raw, err := os.ReadFile(filepath.Join(root, "data", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	start, stop := strings.Index(string(raw), "service.start"), strings.Index(string(raw), "service.stop")
	if start < 0 || stop < start {
		t.Errorf("the audit log does not say start and stop in order:\n%s", raw)
	}
}
