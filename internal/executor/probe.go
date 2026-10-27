package executor

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/probe"
)

// Probes (plan §2.12, M25): a virtual client is a network namespace whose veth interface is a port of the bridge of a test
// network (spike S12). The names follow the probe's id: the first eight hex digits of its UUID make the namespace
// `cgp-xxxxxxxx` and the gateway's end of the pair `pvxxxxxxxx`; the end in the namespace is `eth0`, as on a device.
const (
	ProbeNSPrefix   = "cgp-"
	ProbeHostPrefix = "pv"
	ProbePeerIf     = "eth0"
)

var (
	probeNSName   = regexp.MustCompile(`^cgp-[0-9a-f]{8}$`)
	probeHostName = regexp.MustCompile(`^pv[0-9a-f]{8}$`)
	probeHostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ProbeNS makes a probe (`ensure`) or removes it (`delete`). `ensure` creates the namespace and the pair, makes the
// gateway's end a port of Bridge, gives the end in the namespace its MAC, brings both up and starts the DHCP client
// that gives the probe its address (the executor keeps it running; a probe whose client died is started again by the next
// `ensure`). `delete` stops the client and removes the pair and the namespace. Everything is fixed: the operation takes
// names, never a command.
type ProbeNS struct {
	Target
	Action string `json:"action"` // ensure | delete
	// Name is the probe's namespace and HostIf the gateway's end of the pair; HostIf is the Name's suffix with the prefix `pv`.
	Name   string `json:"name"`
	HostIf string `json:"host_if"`
	// Bridge is the bridge of the probe's network (assigned to Chaos Gateway); only with `ensure`.
	Bridge string `json:"bridge,omitempty"`
	// MAC is the hardware address of the probe's interface: derived from its id, so it is the same after a restart
	// (a locally administered unicast address); only with `ensure`.
	MAC string `json:"mac,omitempty"`
	// Hostname is what the probe's DHCP client sends as its host name (a DNS label); empty sends none.
	Hostname string `json:"hostname,omitempty"`
}

// Kinds of a ProbeRun.
const (
	// ProbeDiagnostic runs a diagnostic of M24 (Request) from the probe's network stack.
	ProbeDiagnostic = "diagnostic"
	// ProbeEcho sends Count UDP datagrams to Host:Port, Interval apart, to an echo server and collects the round trips
	// (probe.EchoResult).
	ProbeEcho = "echo"
	// ProbeRate sends data to the TCP echo server at Host:Port for Duration and measures what comes back (probe.RateResult).
	ProbeRate = "rate"
)

// ProbeRun runs a measurement from a probe's network stack: a diagnostic, the echo of the self-test or its throughput
// test. It changes nothing in the kernel, so it runs beside the writer like a read; its result is the operation's data.
type ProbeRun struct {
	Target
	// Probe is the probe's namespace; an echo run may leave it out to run from the gateway's own namespace (the unimpaired
	// reference of a calibration: the faults of a device do not touch the gateway's traffic).
	Probe string `json:"probe,omitempty"`
	Kind  string `json:"kind"`
	// Request is the diagnostic. Servers are the DNS servers that resolve a host name, Proxy the server a `dns` diagnostic
	// without a server asks (the gateway address of the probe's network: what a device asks); RootsPEM are the certificates
	// that verify a server's certificate besides the system's (the test CA, when the probe trusts it).
	Request   *model.DiagnosticRequest `json:"request,omitempty"`
	Servers   []string                 `json:"servers,omitempty"`
	Proxy     string                   `json:"proxy,omitempty"`
	RootsPEM  string                   `json:"roots_pem,omitempty"`
	UserAgent string                   `json:"user_agent,omitempty"`
	// Host and Port are the echo server of an echo or rate run.
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	// Count, IntervalUs and SettleMs shape an echo run: the datagrams, the time between them and the wait for the last answers.
	Count      int `json:"count,omitempty"`
	IntervalUs int `json:"interval_us,omitempty"`
	SettleMs   int `json:"settle_ms,omitempty"`
	// DurationMs is how long a rate run sends.
	DurationMs int `json:"duration_ms,omitempty"`
}

// Limits of a ProbeRun.
const (
	MaxProbeEchoCount = 20000
	MaxProbeRunMs     = 6 * 60 * 1000
)

// ProbeNSState is a probe as the kernel and the executor have it now (a read of `probes`).
type ProbeNSState struct {
	Name   string `json:"name"`
	HostIf string `json:"host_if"`
	// Master is the bridge the gateway's end is a port of, empty when it is none.
	Master string `json:"master,omitempty"`
	HostUp bool   `json:"host_up"`
	PeerUp bool   `json:"peer_up"`
	MAC    string `json:"mac,omitempty"`
	// Addrs are the IPv4 addresses of the probe's interface, with their prefix length.
	Addrs []string `json:"addrs,omitempty"`
	// DHCP says the executor runs the probe's DHCP client; Lease is what it holds.
	DHCP  bool         `json:"dhcp"`
	Lease *probe.Lease `json:"lease,omitempty"`
}

// ReadProbes is the read that lists the probes (ProbeNSState).
const ReadProbes = "probes"

func (ProbeNS) OpType() string  { return TypeProbeNS }
func (ProbeRun) OpType() string { return TypeProbeRun }
func (ProbeNS) Mutates() bool   { return true }
func (ProbeRun) Mutates() bool  { return false }

func (o ProbeNS) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("action", o.Action, "ensure", "delete"); err != nil {
		return err
	}
	if !probeNSName.MatchString(o.Name) || !probeHostName.MatchString(o.HostIf) || strings.TrimPrefix(o.Name, ProbeNSPrefix) != strings.TrimPrefix(o.HostIf, ProbeHostPrefix) {
		return fmt.Errorf("the probe is %s<8 hex digits> with the pair %s<the same digits> and %s, not %q and %q", ProbeNSPrefix, ProbeHostPrefix, ProbePeerIf, o.Name, o.HostIf)
	}
	if o.Action == "delete" {
		if o.Bridge != "" || o.MAC != "" || o.Hostname != "" {
			return errors.New("a delete takes the names only")
		}
		return nil
	}
	if err := checkDev(o.Bridge); err != nil {
		return fmt.Errorf("bridge: %w", err)
	}
	mac, err := net.ParseMAC(o.MAC)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac[0]&2 == 0 || o.MAC != strings.ToLower(o.MAC) {
		return fmt.Errorf("mac %q is not a locally administered unicast address in lower case", o.MAC)
	}
	if o.Hostname != "" && !probeHostname.MatchString(o.Hostname) {
		return fmt.Errorf("hostname %q is not a DNS label", o.Hostname)
	}
	return nil
}

func (o ProbeRun) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("kind", o.Kind, ProbeDiagnostic, ProbeEcho, ProbeRate); err != nil {
		return err
	}
	if o.Probe == "" && o.Kind != ProbeEcho {
		return errors.New("only an echo run may run from the gateway")
	}
	if o.Probe != "" && !probeNSName.MatchString(o.Probe) {
		return fmt.Errorf("%q is not a probe namespace", o.Probe)
	}
	if o.Kind == ProbeDiagnostic {
		if o.Request == nil {
			return errors.New("a diagnostic needs its request")
		}
		if ve := domain.ValidateDiagnosticRequest(o.Request); len(ve) > 0 {
			return fmt.Errorf("the request: %s", ve[0].Message)
		}
		for _, s := range append([]string{o.Proxy}, o.Servers...) {
			if s == "" {
				continue
			}
			if _, err := netip.ParseAddrPort(s); err != nil {
				return fmt.Errorf("%q is not an address and port", s)
			}
		}
		if len(o.RootsPEM) > 1<<20 || len(o.Servers) > 16 {
			return errors.New("too many servers or too large a set of roots")
		}
		return nil
	}
	if o.Request != nil || len(o.Servers) > 0 || o.Proxy != "" || o.RootsPEM != "" || o.UserAgent != "" {
		return fmt.Errorf("a %s run takes no diagnostic fields", o.Kind)
	}
	if a, err := netip.ParseAddr(o.Host); err != nil || !a.Is4() {
		return fmt.Errorf("host %q is not an IPv4 address", o.Host)
	}
	if o.Port < 1 || o.Port > 65535 {
		return fmt.Errorf("port %d out of range", o.Port)
	}
	if o.Kind == ProbeEcho {
		if o.Count < 1 || o.Count > MaxProbeEchoCount || o.IntervalUs < 100 || o.IntervalUs > 10_000_000 || o.SettleMs < 0 || o.SettleMs > 60_000 {
			return fmt.Errorf("an echo run takes 1..%d datagrams, 100 us to 10 s apart, and a settle time up to 60 s", MaxProbeEchoCount)
		}
		if total := (int64(o.Count)*int64(o.IntervalUs))/1000 + int64(o.SettleMs); total > MaxProbeRunMs {
			return fmt.Errorf("the echo run takes %d ms, at most %d", total, MaxProbeRunMs)
		}
		if o.DurationMs != 0 {
			return errors.New("an echo run takes no duration")
		}
		return nil
	}
	if o.DurationMs < 100 || o.DurationMs > MaxProbeRunMs || o.Count != 0 || o.IntervalUs != 0 || o.SettleMs != 0 {
		return fmt.Errorf("a rate run takes a duration of 100 ms to %d ms and nothing else", MaxProbeRunMs)
	}
	return nil
}

// planProbeNS is the steps of a ProbeNS. The pair is made once; a pair whose namespace is gone is replaced (it would lead
// into the wrong one).
func planProbeNS(o *ProbeNS) []Step {
	ip := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.NS} }
	inside := func(args ...string) Command { return Command{Tool: ToolIP, Args: args, NS: o.Name} }
	exists := ip("link", "show", "dev", o.HostIf)
	if o.Action == "delete" {
		return []Step{
			// `type veth` makes ip refuse any other kind of device; the namespace goes with its end of the pair
			{Probe: &exists, RunIfProbeOK: true, Cmd: ip("link", "delete", "dev", o.HostIf, "type", "veth")},
			{Idempotent: true, Cmd: Command{Tool: ToolIP, Args: []string{"netns", "delete", o.Name}}},
		}
	}
	nsThere := inside("link", "show", "dev", "lo")
	off := func(ns, dev string) Step {
		return Step{Cmd: Command{Tool: ToolEthtool, Args: []string{"-K", dev, "gro", "off", "gso", "off", "tso", "off", "lro", "off"}, NS: ns}}
	}
	return []Step{
		{Guard: &exists, Probe: &nsThere, RunIfProbeOK: false, Cmd: ip("link", "delete", "dev", o.HostIf, "type", "veth")},
		{Probe: &nsThere, RunIfProbeOK: false, Cmd: Command{Tool: ToolIP, Args: []string{"netns", "add", o.Name}}},
		{Probe: &exists, RunIfProbeOK: false, Cmd: ip("link", "add", o.HostIf, "type", "veth", "peer", "name", ProbePeerIf, "address", o.MAC, "netns", o.Name)},
		{Cmd: ip("link", "set", "dev", o.HostIf, "master", o.Bridge)},
		off(o.NS, o.HostIf),
		off(o.Name, ProbePeerIf),
		{Cmd: ip("link", "set", "dev", o.HostIf, "up")},
		{Cmd: inside("link", "set", "dev", "lo", "up")},
		{Cmd: inside("link", "set", "dev", ProbePeerIf, "up")},
	}
}

// probeClients are the DHCP clients of the probes the executor runs.
type probeClients struct {
	mu sync.Mutex
	m  map[string]*probeClient
}

type probeClient struct {
	cancel context.CancelFunc
	done   chan struct{}
	client *probe.Client
}

// WithProbeSocket replaces how the DHCP client of a probe opens its socket (the tests of the executor have no
// namespaces); the default is probe.ListenDHCP when the executor runs the real tools, and no client otherwise.
func WithProbeSocket(open func(ns, ifname string) (probe.Socket, error)) Option {
	return func(e *Executor) { e.probeSock = open }
}

// WithProbeNet replaces the network stack a ProbeRun runs on (the tests of the executor have no namespaces); the default is
// the probe's namespace.
func WithProbeNet(f func(ns string) diag.Net) Option { return func(e *Executor) { e.probeNet = f } }

func (e *Executor) probeNetOf(ns string) diag.Net {
	if e.probeNet != nil {
		return e.probeNet(ns)
	}
	return diag.HostNet{Namespace: ns}
}

// startProbeClient starts the DHCP client of the probe unless one runs.
func (e *Executor) startProbeClient(o *ProbeNS) error {
	e.probes.mu.Lock()
	defer e.probes.mu.Unlock()
	if _, ok := e.probes.m[o.Name]; ok {
		return nil
	}
	open := e.probeSock
	if open == nil {
		if _, real := e.run.(*ExecRunner); !real {
			return nil
		}
		open = probe.ListenDHCP
	}
	sock, err := open(o.Name, ProbePeerIf)
	if err != nil {
		return fmt.Errorf("the DHCP client of %s: %w", o.Name, err)
	}
	mac, _ := net.ParseMAC(o.MAC)
	ctx, cancel := context.WithCancel(context.Background())
	pc := &probeClient{cancel: cancel, done: make(chan struct{})}
	pc.client = &probe.Client{MAC: mac, Hostname: o.Hostname, Sock: sock, Clock: e.clock, Log: e.log.With("probe", o.Name),
		Bound: func(l probe.Lease) { e.configureProbe(ctx, o.Name, &l) }, Lost: func(l probe.Lease) { e.deconfigureProbe(ctx, o.Name) }}
	if e.probeRetry > 0 {
		pc.client.Retry, pc.client.MaxRetry = e.probeRetry, e.probeRetry
	}
	if e.probes.m == nil {
		e.probes.m = map[string]*probeClient{}
	}
	e.probes.m[o.Name] = pc
	go func() {
		defer close(pc.done)
		_ = pc.client.Run(ctx)
	}()
	return nil
}

// stopProbeClient stops the DHCP client of the probe and waits for it.
func (e *Executor) stopProbeClient(name string) {
	e.probes.mu.Lock()
	pc := e.probes.m[name]
	delete(e.probes.m, name)
	e.probes.mu.Unlock()
	if pc != nil {
		pc.cancel()
		<-pc.done
	}
}

func (e *Executor) stopProbeClients() {
	e.probes.mu.Lock()
	var names []string
	for n := range e.probes.m {
		names = append(names, n)
	}
	e.probes.mu.Unlock()
	for _, n := range names {
		e.stopProbeClient(n)
	}
}

// configureProbe gives the probe's interface the leased address and the default route, and takes the addresses it had.
func (e *Executor) configureProbe(ctx context.Context, ns string, l *probe.Lease) {
	ip := func(args ...string) { e.probeCmd(ctx, ns, args...) }
	want := l.Addr.String()
	if r, err := e.run.Run(ctx, Command{Tool: ToolIP, Args: []string{"-j", "addr", "show", "dev", ProbePeerIf}, NS: ns}); err == nil && r.Exit == 0 {
		if as, err := linux.ParseAddrs([]byte(r.Stdout)); err == nil {
			for _, a := range as {
				for _, x := range a.Addrs {
					if x.Family == "inet" && fmt.Sprintf("%s/%d", x.Local, x.PrefixLen) != want {
						ip("addr", "delete", fmt.Sprintf("%s/%d", x.Local, x.PrefixLen), "dev", ProbePeerIf)
					}
				}
			}
		}
	}
	ip("addr", "replace", want, "dev", ProbePeerIf)
	if l.Router.IsValid() {
		ip("route", "replace", "default", "via", l.Router.String(), "dev", ProbePeerIf)
	} else {
		ip("route", "delete", "default", "dev", ProbePeerIf)
	}
}

// deconfigureProbe takes the address and the default route of a lease that is gone.
func (e *Executor) deconfigureProbe(ctx context.Context, ns string) {
	e.probeCmd(ctx, ns, "route", "delete", "default", "dev", ProbePeerIf)
	e.probeCmd(ctx, ns, "-4", "addr", "flush", "dev", ProbePeerIf)
}

func (e *Executor) probeCmd(ctx context.Context, ns string, args ...string) {
	if ctx.Err() != nil {
		return
	}
	r, err := e.run.Run(ctx, Command{Tool: ToolIP, Args: args, NS: ns})
	if err != nil {
		e.log.Warn("probe: cannot configure the interface", "probe", ns, "error", err)
		return
	}
	if r.Exit != 0 && !onlyBenign(r.Stderr) {
		e.log.Warn("probe: ip failed", "probe", ns, "args", args, "stderr", strings.TrimSpace(r.Stderr))
	}
}

// readProbes lists the probes: the gateway's ends of the pairs, with what is inside their namespaces.
func (e *Executor) readProbes(ctx context.Context, tg Target) ([]ProbeNSState, error) {
	r, err := e.run.Run(ctx, ReadCommand(&Read{Target: tg, What: ReadLinks}))
	if err != nil {
		return nil, err
	}
	if r.Exit != 0 {
		return nil, &CommandError{Cmd: ReadCommand(&Read{Target: tg, What: ReadLinks}), Exit: r.Exit, Stderr: r.Stderr}
	}
	links, err := linux.ParseLinks([]byte(r.Stdout))
	if err != nil {
		return nil, err
	}
	e.probes.mu.Lock()
	running := map[string]*probeClient{}
	for n, c := range e.probes.m {
		running[n] = c
	}
	e.probes.mu.Unlock()
	var out []ProbeNSState
	for _, l := range links {
		if !probeHostName.MatchString(l.Name) || l.Kind() != "veth" {
			continue
		}
		st := ProbeNSState{Name: ProbeNSPrefix + strings.TrimPrefix(l.Name, ProbeHostPrefix), HostIf: l.Name, Master: l.Master, HostUp: l.Up()}
		if c, ok := running[st.Name]; ok {
			st.DHCP, st.Lease = true, c.client.Lease()
		}
		a, err := e.run.Run(ctx, Command{Tool: ToolIP, Args: []string{"-j", "addr", "show", "dev", ProbePeerIf}, NS: st.Name})
		if err == nil && a.Exit == 0 {
			if as, err := linux.ParseAddrs([]byte(a.Stdout)); err == nil && len(as) == 1 {
				st.MAC, st.PeerUp = strings.ToLower(as[0].MAC), hasString(as[0].Flags, "UP")
				for _, x := range as[0].Addrs {
					if x.Family == "inet" {
						st.Addrs = append(st.Addrs, fmt.Sprintf("%s/%d", x.Local, x.PrefixLen))
					}
				}
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func hasString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// runProbe runs a measurement from the probe's network stack.
func (e *Executor) runProbe(ctx context.Context, o *ProbeRun) (json.RawMessage, error) {
	ns := o.Probe
	if ns == "" {
		ns = o.NS
	}
	n := e.probeNetOf(ns)
	switch o.Kind {
	case ProbeDiagnostic:
		var servers []netip.AddrPort
		for _, s := range o.Servers {
			servers = append(servers, netip.MustParseAddrPort(s))
		}
		var proxy netip.AddrPort
		if o.Proxy != "" {
			proxy = netip.MustParseAddrPort(o.Proxy)
		}
		// a device asks the servers its lease names; the ones of the request are for a probe that has none
		if l := e.probeLease(o.Probe); l != nil && len(l.DNS) > 0 {
			servers = nil
			for _, a := range l.DNS {
				servers = append(servers, netip.AddrPortFrom(a, 53))
			}
			proxy = servers[0]
		}
		cfg := diag.Config{Net: n, Resolvers: func() []netip.AddrPort { return servers }, ProxyServer: proxy, UserAgent: o.UserAgent, Now: e.clock.Now}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if o.RootsPEM != "" {
			roots.AppendCertsFromPEM([]byte(o.RootsPEM))
		}
		cfg.Roots = func() *x509.CertPool { return roots }
		dctx, cancel := context.WithTimeout(ctx, domain.MaxDiagnosticDuration)
		defer cancel()
		res, err := diag.New(cfg).Run(dctx, *o.Request)
		if err != nil {
			return nil, err
		}
		from := "probe:" + o.Probe
		res.From = &from
		return json.Marshal(res)
	case ProbeEcho:
		res, err := probe.Echo(ctx, n, probe.EchoSpec{Host: netip.MustParseAddr(o.Host), Port: o.Port, Count: o.Count,
			Interval: time.Duration(o.IntervalUs) * time.Microsecond, Settle: time.Duration(o.SettleMs) * time.Millisecond})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	default:
		res, err := probe.Rate(ctx, n, netip.AddrPortFrom(netip.MustParseAddr(o.Host), uint16(o.Port)), time.Duration(o.DurationMs)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// probeLease is the lease the DHCP client of the probe holds, nil when there is none.
func (e *Executor) probeLease(ns string) *probe.Lease {
	e.probes.mu.Lock()
	defer e.probes.mu.Unlock()
	if c, ok := e.probes.m[ns]; ok {
		return c.client.Lease()
	}
	return nil
}
