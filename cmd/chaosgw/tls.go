package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// runTLS is `chaosgw tls`: the TLS responder in the service namespace (plan §2.8). It keeps no state: it reads the two
// CAs the API made, follows its configuration, asks the API for the decision of every connection and posts the outcomes of
// the handshakes.
func runTLS(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":9443", "address to answer on: the port the service namespace redirects the selected connections to")
	api := fs.String("api", envOr("CHAOSGW_API", "https://169.254.100.1:8443"), "the API's address (the gateway's end of the link to the service namespace)")
	tokenFile := fs.String("service-token-file", envOr("CHAOSGW_SERVICE_TOKEN_FILE", "/var/lib/chaosgw/service/token"), "file with the service token")
	certFile := fs.String("api-cert-file", os.Getenv("CHAOSGW_API_CERT"), "the API's certificate to trust; empty trusts any")
	caDir := fs.String("ca-dir", envOr("CHAOSGW_TLS_CA_DIR", "/var/lib/chaosgw/tlsca"), "the directory the API made the test CA and the unknown CA in")
	nsCheck := fs.Bool("namespace-check", true, "exit if this process's namespace loses the service address (plan §3.8); a holder restart replaces the namespace under the same name")
	serviceAddr := fs.String("service-addr", "169.254.100.2", "the service address --namespace-check expects on this namespace's interfaces")
	health := fs.Bool("health", false, "check that a responder answers on --listen and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *health {
		addr := *listen
		if len(addr) > 0 && addr[0] == ':' {
			addr = "127.0.0.1" + addr
		}
		if err := tlsresponder.Probe(addr); err != nil {
			fmt.Fprintf(stderr, "chaosgw tls: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "ok")
		return 0
	}
	var addr netip.Addr
	if *nsCheck {
		a, err := netip.ParseAddr(*serviceAddr)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw tls: --service-addr: %v\n", err)
			return 2
		}
		addr = a
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	clk := clock.NewReal()

	watchDone := make(chan error, 1)
	if *nsCheck {
		go func() {
			watchDone <- dnsproxy.WatchNamespace(ctx, clk, addr, 2*time.Second, 60*time.Second, interfaceAddrs)
			cancelRun()
		}()
	}

	cas, err := tlsresponder.WaitForCAs(runCtx, clk, *caDir, time.Second, log)
	if err != nil {
		if ctx.Err() == nil && *nsCheck {
			// the namespace check ended the wait: say why
			select {
			case werr := <-watchDone:
				if werr != nil {
					fmt.Fprintf(stderr, "chaosgw tls: %v\n", werr)
					return 3
				}
			default:
			}
		}
		return 0
	}
	issuer, err := tlsresponder.NewIssuer(cas, clk)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw tls: %v\n", err)
		return 1
	}
	go tlsresponder.WatchCAs(runCtx, clk, *caDir, issuer, 5*time.Second, log)

	c := &tlsresponder.APIClient{Base: *api, TokenFile: *tokenFile, CertFile: *certFile}
	srv := tlsresponder.New(tlsresponder.Options{Clock: clk, Log: log, Issuer: issuer, Resolver: c, Sink: c})
	go srv.Follow(runCtx, c, log)
	reportsDone := make(chan struct{})
	go func() { srv.RunReports(runCtx); close(reportsDone) }()
	log.Info("the TLS responder starts", "listen", *listen, "api", *api)
	serveErr := srv.Serve(runCtx, *listen)
	select {
	case <-reportsDone: // the last reports are sent
	case <-time.After(6 * time.Second):
	}
	if *nsCheck {
		select {
		case err := <-watchDone:
			if err != nil {
				fmt.Fprintf(stderr, "chaosgw tls: %v\n", err)
				return 3
			}
		default:
			// still running, or ended for the same reason Serve did (a signal): not a namespace problem
		}
	}
	if serveErr != nil {
		fmt.Fprintf(stderr, "chaosgw tls: %v\n", serveErr)
		return 1
	}
	log.Info("the TLS responder stopped")
	return 0
}
