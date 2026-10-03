package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	health := fs.Bool("health", false, "check that a proxy answers on --listen and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *health {
		return dnsHealth(*listen, stdout, stderr)
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	c := &dnsproxy.APIClient{Base: *api, TokenFile: *tokenFile, CertFile: *certFile}
	srv := dnsproxy.New(dnsproxy.Options{Log: log, Sink: c})
	go srv.Follow(ctx, c, log)
	go srv.RunLog(ctx)
	log.Info("the DNS proxy starts", "listen", *listen, "api", *api)
	if err := srv.Serve(ctx, *listen); err != nil {
		fmt.Fprintf(stderr, "chaosgw dns: %v\n", err)
		return 1
	}
	log.Info("the DNS proxy stopped")
	return 0
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
