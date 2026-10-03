package main

import (
	"bytes"
	"context"
	"crypto/tls"
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
	ev, err := kea.EventFromHook(args[0], os.Getenv)
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
	body, _ := json.Marshal(map[string]any{"event": ev.Name, "ip": ev.IP.String(), "mac": ev.MAC, "subnet_id": ev.SubnetID,
		"valid_lifetime": ev.ValidLifetime, "hostname": ev.Hostname, "client_id": ev.ClientID})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(api, "/")+"/api/v1/internal/dhcp/lease-events", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-hook: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
	// the API is on the host's loopback with a self-signed certificate
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // the loopback
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw kea-hook: %v\n", err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		fmt.Fprintf(stderr, "chaosgw kea-hook: the API answered %d: %s\n", res.StatusCode, strings.TrimSpace(string(b)))
		return 1
	}
	return 0
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
