package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
	"github.com/Andste82/chaos-gateway/internal/ntpserver"
)

// runNTP is `chaosgw ntp`: the NTP server in the service namespace (plan §2.18). It keeps no state: it registers with
// the API at start, follows its configuration (the time faults and the ports to listen on) and posts its query log.
func runNTP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ntp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":123", "address to answer on, UDP; the port is the base port, the redirect ports of the time faults come from the configuration")
	ntsKE := fs.String("nts-ke-listen", ":4460", "address to refuse and report NTS key-establishment connections on, TCP; empty turns it off")
	api := fs.String("api", envOr("CHAOSGW_API", "https://169.254.100.1:8443"), "the API's address (the gateway's end of the link to the service namespace)")
	tokenFile := fs.String("service-token-file", envOr("CHAOSGW_SERVICE_TOKEN_FILE", "/var/lib/chaosgw/service/token"), "file with the service token")
	certFile := fs.String("api-cert-file", os.Getenv("CHAOSGW_API_CERT"), "the API's certificate to trust; empty trusts any")
	rate := fs.Float64("rate-limit", ntpserver.DefaultRateLimit, "queries per second one device may ask; beyond it the server drops")
	nsCheck := fs.Bool("namespace-check", true, "exit if this process's namespace loses the service address (plan §3.8); a holder restart replaces the namespace under the same name")
	serviceAddr := fs.String("service-addr", "169.254.100.2", "the service address --namespace-check expects on this namespace's interfaces")
	health := fs.Bool("health", false, "check that a server answers on --listen and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	host, port, err := splitListen(*listen)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw ntp: --listen: %v\n", err)
		return 2
	}
	if *health {
		return ntpHealth(host, port, stdout, stderr)
	}
	var addr netip.Addr
	if *nsCheck {
		a, err := netip.ParseAddr(*serviceAddr)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw ntp: --service-addr: %v\n", err)
			return 2
		}
		addr = a
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	watchDone := make(chan error, 1)
	if *nsCheck {
		go func() {
			watchDone <- dnsproxy.WatchNamespace(ctx, clock.NewReal(), addr, 2*time.Second, 60*time.Second, interfaceAddrs)
			cancelRun()
		}()
	}

	c := &ntpserver.APIClient{Base: *api, TokenFile: *tokenFile, CertFile: *certFile}
	opts := ntpserver.Options{Log: log, Sink: c, RateLimit: *rate, BasePort: port}
	if a, err := netip.ParseAddr(host); err == nil && !a.IsUnspecified() {
		opts.LocalAddr = a
	}
	srv := ntpserver.New(opts)
	go srv.Follow(runCtx, c, log)
	logDone := make(chan struct{})
	go func() { srv.RunLog(runCtx); close(logDone) }()
	if *ntsKE != "" {
		go serveNTSKE(runCtx, srv, *ntsKE, log)
	}
	sync := ntpserver.HostSync{}.State()
	log.Info("the NTP server starts", "listen", *listen, "api", *api, "host_clock_synchronized", sync.Synchronized,
		"host_clock_max_error", sync.MaxError.String())
	if !sync.Synchronized {
		log.Warn("the host clock is not synchronized: the server answers with leap indicator 3 and stratum 16 until it is",
			"hint", "run chrony or systemd-timesyncd on the host (the preflight check host.clock says the same)")
	}
	serveErr := srv.Serve(runCtx, ntpserver.ListenUDP(host))
	select {
	case <-logDone: // the last entries are sent
	case <-time.After(6 * time.Second):
	}
	if *nsCheck {
		select {
		case err := <-watchDone:
			if err != nil {
				fmt.Fprintf(stderr, "chaosgw ntp: %v\n", err)
				return 3
			}
		default:
			// still running, or ended for the same reason Serve did (a signal): not a namespace problem
		}
	}
	if serveErr != nil {
		fmt.Fprintf(stderr, "chaosgw ntp: %v\n", serveErr)
		return 1
	}
	log.Info("the NTP server stopped")
	return 0
}

// serveNTSKE runs the NTS-KE listener; a port that cannot be opened is no reason to stop the time service.
func serveNTSKE(ctx context.Context, srv *ntpserver.Server, addr string, log *slog.Logger) {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Warn("cannot listen for NTS key establishment: NTS connections of devices will not be reported", "error", err)
		return
	}
	if err := srv.ServeNTSKE(ctx, ln); err != nil {
		log.Warn("the NTS key-establishment listener stopped", "error", err)
	}
}

// splitListen reads ":123" or "10.0.0.1:123".
func splitListen(listen string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("%q is not a port", p)
	}
	return h, n, nil
}

// ntpHealth sends the probe query to the server: one that answers at all is up (before its first configuration it
// answers the probe only).
func ntpHealth(host string, port int, stdout, stderr io.Writer) int {
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if err := ntpserver.Probe(net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
		fmt.Fprintf(stderr, "chaosgw ntp: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}
