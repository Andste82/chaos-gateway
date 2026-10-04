package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// defaultPort is the port of the UI and the API until the configuration names another.
const defaultPort = 8443

// runAPI is `chaosgw api`: the REST API server (plan §3.1). It is unprivileged: it owns the
// engine, which applies through the executor's socket, and the stores of revisions, secrets, the
// audit log and the credentials. With -health it checks a running server instead.
func runAPI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chaosgw api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "/run/chaosgw/exec.sock", "path of the executor's Unix socket")
	execUID := fs.Int("executor-uid", 0, "uid the executor runs as (it must be root or this user)")
	namespace := fs.String("namespace", "", "network namespace to manage (default: the executor's own; for tests)")
	stateDir := fs.String("state-dir", "/var/lib/chaosgw/state", "directory of the revisions")
	secretsDir := fs.String("secrets-dir", "/var/lib/chaosgw/secrets", "directory of the secrets (WireGuard keys, credentials, the HTTPS key)")
	dataDir := fs.String("data-dir", "/var/lib/chaosgw/api", "directory of the audit log and the idempotency keys")
	port := fs.Int("port", defaultPort, "port of the UI and the API when the configuration names none")
	listen := fs.String("listen", "", "listen on exactly this address instead of the management network (development, tests)")
	poll := fs.Duration("poll-interval", 5*time.Second, "how often WireGuard and routing state are read")
	keaSocket := fs.String("kea-socket", kea.ControlSocket, "Kea's control socket; empty runs without DHCP")
	serviceNS := fs.String("service-ns", "", "name of the service namespace of the gateway services (the DNS proxy); empty runs without one")
	holderPID := fs.String("service-holder-pid-file", "", "file with the PID (as the executor sees it) of the process whose network namespace becomes the service namespace")
	serviceToken := fs.String("service-token-file", "/var/lib/chaosgw/service/token", "where the token of the service containers (Kea's hook) is written; empty creates none")
	confirm := fs.Duration("confirm-timeout", 0, "override the commit-confirm window (tests)")
	health := fs.Bool("health", false, "check a running server (https://127.0.0.1:<port>/api/v1/system/health) and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *execUID < 0 || *port < 1 || *port > 65535 {
		fmt.Fprintln(stderr, "usage: chaosgw api [--socket <path>] [--state-dir <dir>] [--secrets-dir <dir>] [--data-dir <dir>] [--port <n>] [--health]")
		return 2
	}
	if *health {
		return apiHealth(*port, *listen, stdout, stderr)
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := serveAPI(ctx, log, stderr, apiOptions{socket: *socket, execUID: uint32(*execUID), namespace: *namespace, stateDir: *stateDir,
		secretsDir: *secretsDir, dataDir: *dataDir, port: *port, listen: *listen, poll: *poll, confirm: *confirm, keaSocket: *keaSocket, serviceToken: *serviceToken, serviceNS: *serviceNS, holderPIDFile: *holderPID}); err != nil {
		fmt.Fprintf(stderr, "chaosgw api: %v\n", err)
		return 1
	}
	log.Info("the API stopped")
	return 0
}

type apiOptions struct {
	socket       string
	execUID      uint32
	namespace    string
	stateDir     string
	secretsDir   string
	dataDir      string
	port         int
	listen       string
	poll         time.Duration
	confirm      time.Duration
	keaSocket    string
	serviceToken string
	// serviceNS and holderPIDFile configure the service namespace (plan §3.3)
	serviceNS     string
	holderPIDFile string
}

func serveAPI(ctx context.Context, log *slog.Logger, stderr io.Writer, o apiOptions) error {
	sec, err := secrets.Open(o.secretsDir)
	if err != nil {
		return err
	}
	au, err := auth.Open(filepath.Join(o.secretsDir, "auth"))
	if err != nil {
		return err
	}
	lg, err := audit.Open(o.dataDir, &clock.Real{})
	if err != nil {
		return err
	}
	defer func() { _ = lg.Close() }()
	go lg.Run(ctx)
	st, err := store.Open(o.stateDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ex := executor.NewRedialing(o.socket, executor.DialOptions{Auth: executor.AllowUIDs(o.execUID)})
	defer func() { _ = ex.Close() }()
	var dhcp engine.DHCP
	if o.keaSocket != "" {
		dhcp = &engine.KeaDHCP{Client: &kea.Client{Socket: o.keaSocket}, Base: kea.Config{Script: kea.HookScript}}
	}
	eng, err := engine.New(engineConfig(st, ex, o, sec, log, dhcp))
	if err != nil {
		return err
	}
	// the executor may start after us: wait for it
	for {
		err = eng.Start(ctx)
		if err == nil {
			break
		}
		log.Warn("the engine cannot start yet", "error", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
		if eng, err = engine.New(engineConfig(st, ex, o, sec, log, dhcp)); err != nil {
			return err
		}
	}
	defer eng.Close()
	eng.WatchService(ctx, 2*time.Second)
	if err := eng.FollowHost(ctx, 500*time.Millisecond); err != nil {
		log.Warn("the host is not followed", "error", err)
	}
	_ = eng.PollWireGuard(ctx, o.poll)
	_ = eng.PollRouting(ctx, o.poll)
	if err := eng.PollObserved(ctx, time.Second); err != nil {
		log.Warn("the observed state is not polled", "error", err)
	}
	if err := eng.FollowNeighbors(ctx, 100*time.Millisecond); err != nil {
		log.Warn("the neighbor table is not followed", "error", err)
	}
	if o.serviceToken != "" {
		if err := au.EnsureServiceToken(o.serviceToken); err != nil {
			return fmt.Errorf("the service token: %w", err)
		}
	}

	var report atomic.Pointer[api.PreflightReport]
	go func() { report.Store(buildPreflight()) }()
	srv, err := api.New(api.Config{Engine: eng, Store: st, Auth: au, Audit: lg, Secrets: sec, Exec: ex, Namespace: o.namespace, Log: log,
		StateDir: o.dataDir, BootID: uuid.NewString(), Started: time.Now(), Version: version.Version, Commit: version.Commit, BuildDate: version.Date,
		Preflight: report.Load, ConfirmTimeout: o.confirm})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	if !au.SetupCompleted() {
		tok, err := au.NewSetupToken()
		if err != nil {
			return err
		}
		// the one place where the token is shown: the container log (plan §2.16)
		fmt.Fprintf(stderr, "\n  First start: finish the setup in the browser (https://<address of this machine>:%d)\n  Setup token: %s\n\n", o.port, tok)
	}
	_, _ = lg.Append(audit.Entry{Actor: audit.Actor{Type: "system", ID: "system"}, Via: "system", Action: "service.start", Detail: version.Version})

	names, ips := certNames(eng)
	cert, err := api.LoadOrCreateCertificate(filepath.Join(o.secretsDir, "tls"), names, ips, time.Now())
	if err != nil {
		return err
	}
	b := &api.Binder{
		Addrs:   func() []netip.Addr { return listenAddrs(eng.Snapshot(), au.SetupCompleted(), o.listen) },
		Port:    func() int { return uiPort(eng, o.port, o.listen) },
		Handler: srv.Handler(),
		TLS:     &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		Log:     log,
	}
	done := make(chan struct{})
	go func() { b.Run(ctx, 2*time.Second); close(done) }()
	log.Info("the API is up", "version", version.Version)
	select {
	case <-ctx.Done():
	case <-eng.Done():
		return fmt.Errorf("the engine stopped")
	}
	<-done
	return nil
}

// uiPort is the port the configuration names (management.ui_port), else the flag's.
func uiPort(eng *engine.Engine, def int, listen string) int {
	if listen != "" {
		if _, p, err := net.SplitHostPort(listen); err == nil {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				return n
			}
		}
	}
	if cfg := eng.Snapshot().Config; cfg != nil && cfg.Management.UiPort != nil && *cfg.Management.UiPort > 0 {
		return *cfg.Management.UiPort
	}
	return def
}

// listenAddrs are the addresses to serve on (plan §2.16): the loopback for the health check, and
// until the setup is finished every address of the host, afterwards the management network's: the
// management interface and the tunnel addresses of WireGuard networks with the role management.
func listenAddrs(snap *engine.Snapshot, setupDone bool, explicit string) []netip.Addr {
	if explicit != "" {
		host, _, err := net.SplitHostPort(explicit)
		if err != nil {
			return nil
		}
		if a, err := netip.ParseAddr(host); err == nil {
			return []netip.Addr{a}
		}
		return nil
	}
	out := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	// the gateway services in the service namespace reach the API on the gateway's end of the pair
	if snap.Service != nil && snap.Service.HostCIDR.IsValid() {
		out = append(out, snap.Service.HostCIDR.Addr())
	}
	if !setupDone {
		for _, l := range snap.Host.Links {
			if l.Name == "lo" {
				continue
			}
			for _, p := range l.Addrs {
				if p.Addr().Is4() {
					out = append(out, p.Addr())
				}
			}
		}
		return out
	}
	if snap.Config == nil {
		return out // set up but nothing active (yet): the loopback only, never the test networks
	}
	if l, ok := snap.Host.Resolve(snap.Config.Management.Interface); ok {
		for _, p := range l.Addrs {
			if p.Addr().Is4() {
				out = append(out, p.Addr())
			}
		}
	}
	for _, w := range snap.WireGuardInterfaces {
		if w.Role == "management" && w.Address.IsValid() {
			out = append(out, w.Address.Addr())
		}
	}
	return out
}

// certNames are the names and addresses the self-signed certificate is made for.
func certNames(eng *engine.Engine) ([]string, []net.IP) {
	var names []string
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
	}
	var ips []net.IP
	for _, l := range eng.Snapshot().Host.Links {
		if l.Name == "lo" {
			continue
		}
		for _, p := range l.Addrs {
			if p.Addr().Is4() {
				ips = append(ips, net.IP(p.Addr().AsSlice()))
			}
		}
	}
	return names, ips
}

// buildPreflight checks what the API container can see of the host: the kernel and its modules.
// (The tools are the executor's: it has them.)
func buildPreflight() *api.PreflightReport {
	env, err := preflight.HostEnv()
	if err != nil {
		return &api.PreflightReport{Status: "warn", Checks: []api.PreflightCheck{{ID: "host", Status: "warn", Message: "the host cannot be inspected from here: " + err.Error()}}}
	}
	now := time.Now().UTC()
	r := &api.PreflightReport{At: &now, Status: "ok"}
	add := func(c api.PreflightCheck) {
		r.Checks = append(r.Checks, c)
		switch {
		case c.Status == "fail":
			r.Status = "fail"
		case c.Status == "warn" && r.Status == "ok":
			r.Status = "warn"
		}
	}
	if err := preflight.CheckKernel(env.Release); err != nil {
		add(api.PreflightCheck{ID: "kernel.version", Status: "fail", Message: err.Error(), Fix: "boot a kernel of " + preflight.MinKernel + " or newer"})
	} else {
		add(api.PreflightCheck{ID: "kernel.version", Status: "ok", Message: "kernel " + strings.TrimSpace(env.Release)})
	}
	for _, m := range preflight.CheckModules(env, preflight.Modules()) {
		c := api.PreflightCheck{ID: "module." + m.Name, Status: "ok", Message: m.Name + " " + m.Status.String() + " (" + m.Feature + ")"}
		if m.Status == preflight.Missing {
			c.Status = "fail"
			if m.Later {
				c.Status = "warn"
			}
			c.Fix = "install the kernel modules for your kernel, e.g. linux-modules-extra"
		}
		add(c)
	}
	return r
}

// apiHealth checks a running server through the loopback (the container health check).
func apiHealth(port int, listen string, stdout, stderr io.Writer) int {
	host := "127.0.0.1"
	if listen != "" {
		if h, p, err := net.SplitHostPort(listen); err == nil {
			host = h
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // the loopback, a self-signed certificate
	res, err := client.Get(fmt.Sprintf("https://%s/api/v1/system/health", net.JoinHostPort(host, fmt.Sprint(port))))
	if err != nil {
		fmt.Fprintf(stderr, "API unhealthy: %v\n", err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "API unhealthy: status %d\n", res.StatusCode)
		return 1
	}
	fmt.Fprintln(stdout, "API healthy")
	return 0
}

func engineConfig(st *store.Store, ex apply.Exec, o apiOptions, sec *secrets.Store, log *slog.Logger, dhcp engine.DHCP) engine.Config {
	cfg := engine.Config{Store: st, Exec: ex, Namespace: o.namespace, Secrets: sec, Log: log, DHCP: dhcp, ServiceNS: o.serviceNS, DefaultUIPort: o.port,
		GenerationFile: filepath.Join(o.stateDir, "generation")}
	if o.holderPIDFile != "" {
		cfg.ServiceHolderPID = func() int {
			raw, err := os.ReadFile(o.holderPIDFile)
			if err != nil {
				return 0
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil || n < 0 {
				return 0
			}
			return n
		}
	}
	return cfg
}
