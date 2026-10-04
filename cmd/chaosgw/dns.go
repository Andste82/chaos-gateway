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
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
)

// runDNS is `chaosgw dns`: the DNS proxy in the service namespace (plan §2.6). It keeps no state:
// it registers with the API at start, follows its configuration and posts its query log.
func runDNS(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":53", "address to answer on, UDP and TCP")
	api := fs.String("api", envOr("CHAOSGW_API", "https://169.254.100.1:8443"), "the API's address (the gateway's end of the link to the service namespace)")
	tokenFile := fs.String("service-token-file", envOr("CHAOSGW_SERVICE_TOKEN_FILE", "/var/lib/chaosgw/service/token"), "file with the service token")
	certFile := fs.String("api-cert-file", os.Getenv("CHAOSGW_API_CERT"), "the API's certificate to trust; empty trusts any")
	nsCheck := fs.Bool("namespace-check", true, "exit if this process's namespace loses the service address (plan §3.8); a holder restart replaces the namespace under the same name")
	serviceAddr := fs.String("service-addr", "169.254.100.2", "the service address --namespace-check expects on this namespace's interfaces")
	health := fs.Bool("health", false, "check that a proxy answers on --listen and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *health {
		return dnsHealth(*listen, stdout, stderr)
	}
	var addr netip.Addr
	if *nsCheck {
		a, err := netip.ParseAddr(*serviceAddr)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw dns: --service-addr: %v\n", err)
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

	c := &dnsproxy.APIClient{Base: *api, TokenFile: *tokenFile, CertFile: *certFile}
	srv := dnsproxy.New(dnsproxy.Options{Log: log, Sink: c})
	go srv.Follow(runCtx, c, log)
	logDone := make(chan struct{})
	go func() { srv.RunLog(runCtx); close(logDone) }()
	log.Info("the DNS proxy starts", "listen", *listen, "api", *api)
	serveErr := srv.Serve(runCtx, *listen)
	select {
	case <-logDone: // the last entries are sent
	case <-time.After(6 * time.Second):
	}
	if *nsCheck {
		select {
		case err := <-watchDone:
			if err != nil {
				fmt.Fprintf(stderr, "chaosgw dns: %v\n", err)
				return 3
			}
		default:
			// still running, or ended for the same reason Serve did (a signal): not a namespace problem
		}
	}
	if serveErr != nil {
		fmt.Fprintf(stderr, "chaosgw dns: %v\n", serveErr)
		return 1
	}
	log.Info("the DNS proxy stopped")
	return 0
}

// interfaceAddrs is dnsproxy.WatchNamespace's real address source.
func interfaceAddrs() ([]netip.Addr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipNet.IP); ok {
			out = append(out, ip.Unmap())
		}
	}
	return out, nil
}

// dnsHealth asks the proxy for a name it never forwards: a proxy that answers at all is up.
func dnsHealth(listen string, stdout, stderr io.Writer) int {
	addr := listen
	if len(addr) > 0 && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	if err := dnsproxy.Probe(addr); err != nil {
		fmt.Fprintf(stderr, "chaosgw dns: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}
