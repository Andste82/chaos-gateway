package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/kea"
)

// runKeaHook is `chaosgw kea-hook <hook point>`: what Kea's run_script hook starts (through the shell
// script chaosgw-kea-hook). It turns the hook's environment into a lease event and posts it to the
// API's internal endpoint with the service token (plan §2.7). Kea does not wait for it and ignores
// its exit status; a failure is printed, so it shows in Kea's log.
func runKeaHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: chaosgw kea-hook <hook point> (called by Kea's run_script hook)")
		return 2
	}
	evs, err := kea.EventsFromHook(args[0], os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-hook: %v\n", err)
		return 1
	}
	api := envOr("CHAOSGW_API", "https://127.0.0.1:8443")
	tokenFile := envOr("CHAOSGW_SERVICE_TOKEN_FILE", "/var/lib/chaosgw/service/token")
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-hook: cannot read the service token: %v\n", err)
		return 1
	}
	tc, err := keaHookTLSConfig(os.Getenv("CHAOSGW_API_CERT"))
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-hook: %v\n", err)
		return 1
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}
	status := 0
	for _, ev := range evs {
		if err := postLeaseEvent(client, strings.TrimRight(api, "/"), strings.TrimSpace(string(raw)), ev); err != nil {
			fmt.Fprintf(stderr, "chaosgw kea-hook: %v\n", err)
			status = 1
		}
	}
	return status
}

// keaHookTLSConfig builds the TLS configuration the hook talks to the API with. With certFile it
// verifies the API's certificate against it; empty trusts whatever is presented (the API is reached
// over the host's loopback, which is hard to intercept, but M6b-08 asks for verification once the API
// publishes its certificate to the hook).
func keaHookTLSConfig(certFile string) (*tls.Config, error) {
	if certFile == "" {
		return &tls.Config{InsecureSkipVerify: true}, nil //nolint:gosec // the loopback; see CHAOSGW_API_CERT
	}
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("CHAOSGW_API_CERT: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("CHAOSGW_API_CERT: %s holds no certificate", certFile)
	}
	return &tls.Config{RootCAs: pool}, nil
}

func postLeaseEvent(client *http.Client, api, token string, ev kea.Event) error {
	body, _ := json.Marshal(map[string]any{"event": ev.Name, "ip": ev.IP.String(), "mac": ev.MAC, "subnet_id": ev.SubnetID,
		"valid_lifetime": ev.ValidLifetime, "hostname": ev.Hostname, "client_id": ev.ClientID})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/v1/internal/dhcp/lease-events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("the API answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// runKeaConfig is `chaosgw kea-config`: the configuration Kea starts with, before the API has sent
// the scopes of the networks: no subnet, the control socket and the lease hook.
func runKeaConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: chaosgw kea-config")
		return 2
	}
	text, err := kea.Config{Script: kea.HookScript}.Render()
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-config: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, text)
	return 0
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
