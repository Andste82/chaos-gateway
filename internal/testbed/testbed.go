// Package testbed builds network-namespace topologies for the Linux integration tests (plan
// §4.2): a gateway, switches, clients and servers as namespaces joined by veth pairs, so the
// whole stack can be tested without touching the host network. It is the Go successor of
// spikes/lib/testbed.sh.
//
// A Bed needs root and the kernel modules of internal/preflight. Where it does not have them,
// tests run in a VM instead (level 1b, internal/testbed/vmrun).
package testbed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// EmulatedEnv is set to "1" by the VM runner when the VM runs without hardware virtualization.
// Timing is then too noisy for accuracy assertions (plan §4.3).
const EmulatedEnv = "CHAOSGW_TESTBED_EMULATED"

// Emulated reports whether the tests run under software emulation.
func Emulated() bool { return os.Getenv(EmulatedEnv) == "1" }

// Accurate reports whether accuracy and timing assertions are meaningful: native execution or
// KVM. Under emulation the same tests assert only the functional effect.
func Accurate() bool { return !Emulated() }

// config holds the options of New.
type config struct {
	prefix                 string
	plainGateway           bool
	managementDefaultRoute bool
	gatewayBridges         bool
	remotes                bool
}

// Option changes how a Bed or a default Topology is built.
type Option func(*config)

// WithPrefix fixes the namespace name prefix (for debugging). The default is random, so parallel
// runs never destroy each other's topology.
func WithPrefix(p string) Option { return func(c *config) { c.prefix = p } }

// WithPlainGateway chooses whether the default topology's gateway is a plain forwarder: IPv4
// forwarding on, masquerade towards the uplink. Product tests (M4 on) switch it off and let the
// product configure the gateway.
func WithPlainGateway(on bool) Option { return func(c *config) { c.plainGateway = on } }

// WithManagementDefaultRoute chooses whether the gateway gets a default route through its
// management interface in the main routing table, as an OS-owned management interface has
// (spike S12). The default topology has it.
func WithManagementDefaultRoute(on bool) Option {
	return func(c *config) { c.managementDefaultRoute = on }
}

// WithGatewayBridges chooses whether the default topology's gateway already has its bridges
// br-lan0 and br-lan1 with their addresses, as the plain gateway of M1 does. Product tests (M4 on)
// switch it off: the gateway then has the bare ports lan0 and lan1, and the product builds the
// bridges, as it does on a real host.
func WithGatewayBridges(on bool) Option { return func(c *config) { c.gatewayBridges = on } }

// WithRemotes adds the remote machines of the WireGuard tests (plan §4.2): a switch on the uplink
// side that joins the gateway, the server, a remote client (203.0.113.30) with a network behind it
// (10.50.0.10) and a remote site (203.0.113.40) with its own network (10.60.0.10), and a second remote site (203.0.113.50, network 10.70.0.10). They are an
// option because every namespace costs time under emulation.
func WithRemotes(on bool) Option { return func(c *config) { c.remotes = on } }

func newConfig(opts []Option) config {
	c := config{plainGateway: true, managementDefaultRoute: true, gatewayBridges: true}
	for _, o := range opts {
		o(&c)
	}
	return c
}

var (
	prefixMu   sync.Mutex
	prefixUsed = map[string]bool{}
)

// RandomPrefix returns a namespace prefix that is unique: "tb" and eight random hex digits. It
// never returns the same prefix twice in one process, so the beds of one test run cannot
// collide; separate processes (parallel runs) are kept apart by the 32 random bits.
func RandomPrefix() string {
	prefixMu.Lock()
	defer prefixMu.Unlock()
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(err) // crypto/rand does not fail on Linux
		}
		p := "tb" + hex.EncodeToString(b[:])
		if !prefixUsed[p] {
			prefixUsed[p] = true
			return p
		}
	}
}

// proxyVars are removed from the environment of everything run in a namespace: testbed traffic
// must never go through an HTTP proxy from the environment (spike S1).
var proxyVars = map[string]bool{
	"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
}

// CleanEnv returns env without proxy variables.
func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !proxyVars[name] {
			out = append(out, kv)
		}
	}
	return out
}

var moduleOnce sync.Once

// loadModules loads the kernel modules once per process. A module that cannot be loaded is not
// an error here: the preflight in New reports what is actually missing.
func loadModules() {
	moduleOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = preflight.Load(ctx, preflight.Modules())
	})
}

// Bed owns the namespaces and background processes of one test and removes them at the end.
type Bed struct {
	t      testing.TB
	prefix string
	cfg    config

	mu    sync.Mutex
	nss   []*Namespace
	procs []*Process
}

// New returns an empty Bed. It fails the test, with the way out, if the machine cannot create
// namespaces or lacks kernel modules: such a test belongs in a VM (`make test-vm`), and silently
// skipping it would hide it.
func New(t testing.TB, opts ...Option) *Bed {
	t.Helper()
	cfg := newConfig(opts)
	if cfg.prefix == "" {
		cfg.prefix = RandomPrefix()
	}
	loadModules()
	rep, err := preflight.Run(context.Background())
	if err != nil {
		t.Fatalf("testbed preflight: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("the namespace testbed cannot run here:\n  %s\nrun the tests in a VM with `make test-vm`, or in a privileged container with `make test-privileged`",
			strings.Join(rep.Problems(), "\n  "))
	}
	b := &Bed{t: t, prefix: cfg.prefix, cfg: cfg}
	t.Cleanup(b.Destroy)
	return b
}

// Prefix returns the prefix of the namespace names of this bed.
func (b *Bed) Prefix() string { return b.prefix }

// Namespace is a network namespace of a Bed.
type Namespace struct {
	bed *Bed
	// Short is the name inside the bed ("gw", "a"); Name is the namespace's real name.
	Short, Name string
}

// Add creates a namespace with its loopback interface up.
func (b *Bed) Add(short string) *Namespace {
	b.t.Helper()
	ns := &Namespace{bed: b, Short: short, Name: b.prefix + "-" + short}
	b.mu.Lock()
	for _, o := range b.nss {
		if o.Short == short {
			b.mu.Unlock()
			b.t.Fatalf("testbed: namespace %q added twice", short)
		}
	}
	b.nss = append(b.nss, ns)
	b.mu.Unlock()
	if out, err := exec.Command("ip", "netns", "add", ns.Name).CombinedOutput(); err != nil {
		b.t.Fatalf("testbed: ip netns add %s: %v: %s", ns.Name, err, out)
	}
	ns.Must("ip", "link", "set", "lo", "up")
	return ns
}

// NS returns the namespace added under short, or nil.
func (b *Bed) NS(short string) *Namespace {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ns := range b.nss {
		if ns.Short == short {
			return ns
		}
	}
	return nil
}

// Command returns a command that runs name in the namespace, with a clean environment.
func (n *Namespace) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ip", append([]string{"netns", "exec", n.Name, name}, args...)...)
	cmd.Env = CleanEnv(os.Environ())
	return cmd
}

// Run runs a command in the namespace and returns its combined output. A failing command's error
// contains the output.
func (n *Namespace) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := n.Command(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%s: %s %s: %w: %s", n.Short, name, strings.Join(args, " "), err, s)
	}
	return s, nil
}

// Must runs a command in the namespace and fails the test if it fails.
func (n *Namespace) Must(name string, args ...string) string {
	n.bed.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := n.Run(ctx, name, args...)
	if err != nil {
		n.bed.t.Fatal(err)
	}
	return out
}

// MustStdin runs a command in the namespace with stdin and fails the test if it fails.
func (n *Namespace) MustStdin(stdin, name string, args ...string) string {
	n.bed.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := n.Command(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		n.bed.t.Fatalf("%s: %s %s: %v: %s", n.Short, name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// Sysctl sets a sysctl in the namespace.
func (n *Namespace) Sysctl(key, value string) {
	n.bed.t.Helper()
	n.Must("sysctl", "-qw", key+"="+value)
}

// Process is a command running in the background in a namespace.
type Process struct {
	cmd  *exec.Cmd
	out  bytes.Buffer
	mu   sync.Mutex
	done chan struct{}
	err  error
}

// Start runs a command in the background. The bed kills it at the end of the test, before the
// namespace is deleted.
func (n *Namespace) Start(name string, args ...string) *Process {
	n.bed.t.Helper()
	p := &Process{done: make(chan struct{})}
	// no context: the process lives until the bed kills it
	p.cmd = n.Command(context.Background(), name, args...)
	w := &lockedWriter{p: p}
	p.cmd.Stdout, p.cmd.Stderr = w, w
	p.cmd.WaitDelay = 5 * time.Second
	if err := p.cmd.Start(); err != nil {
		n.bed.t.Fatalf("testbed: start %s in %s: %v", name, n.Short, err)
	}
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	}()
	n.bed.mu.Lock()
	n.bed.procs = append(n.bed.procs, p)
	n.bed.mu.Unlock()
	return p
}

type lockedWriter struct{ p *Process }

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.out.Write(b)
}

// Output returns what the process has written so far.
func (p *Process) Output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// Done is closed when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stop kills the process and waits for it.
func (p *Process) Stop() {
	_ = p.cmd.Process.Kill()
	<-p.done
}

// Destroy kills all processes of the bed and deletes its namespaces. New registers it with
// t.Cleanup; calling it again is harmless. Processes must be dead before their namespace goes,
// or they keep it alive (spike S1).
func (b *Bed) Destroy() {
	b.mu.Lock()
	procs, nss := b.procs, b.nss
	b.procs, b.nss = nil, nil
	b.mu.Unlock()

	for _, p := range procs {
		p.Stop()
	}
	for i := len(nss) - 1; i >= 0; i-- {
		ns := nss[i]
		if out, err := exec.Command("ip", "netns", "pids", ns.Name).Output(); err == nil {
			for _, pid := range strings.Fields(string(out)) {
				_ = exec.Command("kill", "-9", pid).Run()
			}
		}
		if out, err := exec.Command("ip", "netns", "del", ns.Name).CombinedOutput(); err != nil {
			b.t.Logf("testbed: ip netns del %s: %v: %s", ns.Name, err, strings.TrimSpace(string(out)))
		}
	}
}
