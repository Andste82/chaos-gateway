package ctl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The interception (M23c): `chaosctl intercept set` makes an interception case with its HTTP rules, `chaosctl intercept flows` reads
// the exchanges the proxy saw, and `chaosctl tls ca` downloads the test CA that a device must trust for the interception and replaces it.
// Like the commands of the other application-layer families they check what is certain without the gateway (a rule that has two actions,
// a field the action does not use); what the gateway knows (a status code, a delay, a header name) is left to it, and its refusal is
// printed with the pointer and exits with 2.

// upstreamPolicies are the values of `intercept.upstream` (P5-M22-03).
var upstreamPolicies = []string{"verify", "own_ca", "insecure"}

// interceptFlags are the settings and the rules of an interception case on the command line.
type interceptFlags struct {
	noKeylog, allowQUIC bool
	upstream            string
	rules               []string
}

func (i *interceptFlags) add(fs *pflag.FlagSet) {
	fs.BoolVar(&i.noKeylog, "no-keylog", false, "do not give the TLS secrets of the intercepted connections to the captures that run (they are sensitive)")
	fs.BoolVar(&i.allowQUIC, "allow-quic", false, "keep UDP 443 open: a client that tries QUIC / HTTP/3 is not forced back to TCP, and is not intercepted")
	fs.StringVar(&i.upstream, "upstream", "", "how the proxy treats the certificate of the real server: verify, own_ca (the installation's CA file) or insecure; without it the installation's default")
	fs.StringArrayVar(&i.rules, "rule", nil, "an HTTP rule, repeat for more, applied in the order given: ACTION and match fields separated by ';' (see the examples)")
}

// settings is the `intercept` part of the case; nil when the flags leave everything to the defaults.
func (i *interceptFlags) settings() (map[string]any, error) {
	out := map[string]any{}
	if i.noKeylog {
		out["keylog"] = false
	}
	if i.allowQUIC {
		out["block_quic"] = false
	}
	if i.upstream != "" {
		u := actionName(i.upstream)
		if !oneOf(u, upstreamPolicies...) {
			return nil, failf(ExitError, "--upstream %q is not one of %s", i.upstream, strings.Join(upstreamPolicies, ", "))
		}
		out["upstream"] = u
	}
	if len(i.rules) > 0 {
		rules := make([]map[string]any, 0, len(i.rules))
		for n, spec := range i.rules {
			r, err := parseHTTPRule(spec)
			if err != nil {
				return nil, failf(ExitError, "--rule %d (%q): %v", n+1, spec, err)
			}
			rules = append(rules, r)
		}
		out["http_rules"] = rules
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// parseHTTPRule reads one --rule. The items are separated by ';':
//
//	host=NAME  path=PREFIX  method=GET                 what the rule selects (all given must match; none: every request)
//	block  abort  status=503  delay=2s  throttle=64kbit  the action: exactly one
//	request-header=Name:Value  response-header=Name:Value  remove-request-header=Name  remove-response-header=Name
//	replace=SEARCH=>REPLACEMENT                          the changes of a modify rule (the action modify: no other action)
func parseHTTPRule(spec string) (map[string]any, error) {
	match := map[string]any{}
	modify := map[string]any{}
	var action string
	var rule = map[string]any{}
	setAction := func(a string) error {
		if action != "" {
			return fmt.Errorf("a rule has one action, this one has %s and %s", action, a)
		}
		action = a
		return nil
	}
	header := func(kind string, remove bool, v string) error {
		key := "request_headers"
		if kind == "response-header" {
			key = "response_headers"
		}
		name, value := v, ""
		if !remove {
			var ok bool
			name, value, ok = strings.Cut(v, ":")
			if !ok {
				return fmt.Errorf("%s=%s is not Name:Value", kind, v)
			}
			value = strings.TrimPrefix(value, " ")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("%s needs a header name", kind)
		}
		m, _ := modify[key].(map[string]string)
		if m == nil {
			m = map[string]string{}
			modify[key] = m
		}
		for have := range m {
			if strings.EqualFold(have, name) {
				return fmt.Errorf("the header %s is named twice", name)
			}
		}
		m[name] = value
		return nil
	}
	items := 0
	for _, raw := range strings.Split(spec, ";") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		items++
		key, value, hasValue := strings.Cut(item, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		needValue := func() error {
			if !hasValue || value == "" {
				return fmt.Errorf("%s needs a value (%s=...)", key, key)
			}
			return nil
		}
		switch key {
		case "host", "path", "method":
			if err := needValue(); err != nil {
				return nil, err
			}
			field := key
			if key == "path" {
				field = "path_prefix"
			}
			if _, dup := match[field]; dup {
				return nil, fmt.Errorf("%s is given twice", key)
			}
			match[field] = value
		case "block", "abort":
			if hasValue {
				return nil, fmt.Errorf("%s takes no value", key)
			}
			if err := setAction(key); err != nil {
				return nil, err
			}
		case "status":
			if err := needValue(); err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("status=%s is not a status code like 503", value)
			}
			if err := setAction("status"); err != nil {
				return nil, err
			}
			rule["status"] = n
		case "delay":
			if err := needValue(); err != nil {
				return nil, err
			}
			if err := setAction("delay"); err != nil {
				return nil, err
			}
			rule["delay"] = value
		case "throttle":
			if err := needValue(); err != nil {
				return nil, err
			}
			if err := setAction("throttle"); err != nil {
				return nil, err
			}
			rule["rate"] = value
		case "request-header", "response-header":
			if err := needValue(); err != nil {
				return nil, err
			}
			if err := header(key, false, value); err != nil {
				return nil, err
			}
		case "remove-request-header", "remove-response-header":
			if err := needValue(); err != nil {
				return nil, err
			}
			if err := header(strings.TrimPrefix(key, "remove-"), true, value); err != nil {
				return nil, err
			}
		case "replace":
			if err := needValue(); err != nil {
				return nil, err
			}
			search, replace, ok := strings.Cut(value, "=>")
			if !ok || search == "" {
				return nil, fmt.Errorf("replace=%s is not SEARCH=>REPLACEMENT", value)
			}
			list, _ := modify["response_body_replace"].([]map[string]string)
			modify["response_body_replace"] = append(list, map[string]string{"search": search, "replace": replace})
		default:
			return nil, fmt.Errorf("%q is not a field of a rule: host, path, method, block, abort, status, delay, throttle, request-header, response-header, remove-request-header, remove-response-header or replace", item)
		}
	}
	if items == 0 {
		return nil, fmt.Errorf("the rule is empty")
	}
	if len(modify) > 0 {
		if err := setAction("modify"); err != nil {
			return nil, err
		}
		rule["modify"] = modify
	}
	if action == "" {
		return nil, fmt.Errorf("the rule has no action: block, abort, status=, delay=, throttle= or a change (request-header=, response-header=, remove-request-header=, remove-response-header=, replace=)")
	}
	rule["action"] = action
	if len(match) > 0 {
		rule["match"] = match
	}
	return rule, nil
}

// selection is the part of a TLS case that selects connections: ports, server names, destination, cutting what is open.
func (t *tlsFlags) selection() map[string]any {
	body := map[string]any{}
	if len(t.ports) > 0 {
		body["ports"] = t.ports
	}
	if len(t.sni) > 0 {
		body["sni"] = t.sni
	}
	if t.to != "" {
		body["destination"] = destinationOf(t.to)
	}
	if t.cut {
		body["cut_existing"] = true
	}
	return body
}

func (a *app) interceptCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "intercept", Short: "The interception proxy: its cases and the exchanges it saw"}
	var tf targetFlags
	var lf lifetimeFlags
	var xf tlsFlags
	var inf interceptFlags
	set := &cobra.Command{
		Use:   "set",
		Short: "Intercept the TLS connections of a device, group or network, with HTTP rules and a TTL",
		Long: `Creates an interception case as an overlay owned by the token and prints the id of the overlay. The call returns once the
gateway has verified the change. The selected new connections (without --port: 443 and 8883) are handed to the interception proxy,
which terminates the device's TLS with a certificate of the test CA ('chaosctl tls ca' downloads it: the device must trust it,
or it rejects the certificate and the proxy sees nothing), reads HTTP/1.1, HTTP/2 and WebSocket, and applies the --rule options in
the order given. Without --rule it only watches ('chaosctl intercept flows' lists what it saw). The same selection set again
replaces the case.

A rule is a list of items separated by ';'. What it selects: host=NAME (exact or *.suffix), path=PREFIX and method=GET; a rule
without them selects every request. Its action, exactly one: block (answers 403 and never reaches the server), status=CODE
(answers with this code), delay=DURATION (holds the request back), throttle=RATE (paces the bytes to the device, 64kbit),
abort (resets the connection), or changes: request-header=Name:Value, response-header=Name:Value, remove-request-header=Name,
remove-response-header=Name and replace=SEARCH=>REPLACEMENT (in the body of the response). block, status and abort end the
rules for a request; the others go on with the next matching rule. The proxy checks the server's certificate as --upstream says
(verify against its bundle, own_ca against the installation's CA file, or not at all).`,
		Example: `  chaosctl intercept set --device esp32-42 --rule 'host=api.example.com;path=/v1/;status=503' --ttl 5m
  chaosctl intercept set --network IoT --sni api.example.com --rule 'delay=2s' --rule 'host=api.example.com;method=POST;block'
  chaosctl intercept set --device esp32-42 --rule 'response-header=Cache-Control: no-store;replace=ok=>fail' --upstream insecure
  chaosctl intercept set --device esp32-42 --no-keylog   # only watch`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			settings, err := inf.settings()
			if err != nil {
				return err
			}
			body := xf.selection()
			body["case"] = "intercept"
			if settings != nil {
				body["intercept"] = settings
			}
			req := map[string]any{"target": scope, "tls": body}
			if err := lf.apply(req); err != nil {
				return err
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			ov, err := a.createOverlay(cmd.Context(), cl, req)
			if err != nil {
				return apiFail(err)
			}
			a.printOverlay(ov, fmt.Sprintf("intercept set on %s", tf.describe()))
			return nil
		},
	}
	tf.add(set.Flags())
	xf.add(set.Flags())
	inf.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with --overlay' renews it")

	var device, host, since string
	var faulted bool
	var limit int
	var all bool
	flows := &cobra.Command{
		Use:   "flows",
		Short: "List the exchanges the interception proxy saw, newest first",
		Long: `Lists what the proxy saw of the connections an interception case selected: one line per HTTP exchange, per WebSocket
connection and per connection that is no HTTP, with the rules that acted on it. The gateway keeps the newest 20000 in memory; the
log is empty after a restart. --since takes a time (2026-10-25T12:00:00Z) or how long ago (90s, 5m).`,
		Example: `  chaosctl intercept flows --device esp32-42 --faulted
  chaosctl intercept flows --host '*.example.com' --since 5m --limit 200
  chaosctl intercept flows --json --all > flows.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if device != "" {
				q.Set("device", device)
			}
			if host != "" {
				q.Set("host", host)
			}
			if since != "" {
				t, err := a.sinceTime(since)
				if err != nil {
					return err
				}
				q.Set("since", t.UTC().Format(time.RFC3339Nano))
			}
			if faulted {
				q.Set("faulted", "true")
			}
			if limit < 1 || limit > 500 {
				return failf(ExitError, "--limit is from 1 to 500")
			}
			q.Set("limit", strconv.Itoa(limit))
			cl, err := a.client()
			if err != nil {
				return err
			}
			var items []model.InterceptFlow
			for {
				var page model.InterceptFlowPage
				if _, err := cl.call(cmd.Context(), http.MethodGet, "/intercept/flows", q, nil, &page); err != nil {
					return apiFail(err)
				}
				items = append(items, page.Items...)
				if !all || page.NextCursor == nil || *page.NextCursor == "" {
					break
				}
				q.Set("cursor", *page.NextCursor)
			}
			if a.asJSON {
				a.printJSON(model.InterceptFlowPage{Items: items})
				return nil
			}
			a.printFlows(cmd.Context(), cl, items)
			return nil
		},
	}
	flows.Flags().StringVar(&device, "device", "", "only exchanges of this device (name or UUID)")
	flows.Flags().StringVar(&host, "host", "", "only exchanges for this host name (exact or *.suffix)")
	flows.Flags().StringVar(&since, "since", "", "only exchanges since this time, or this long ago (5m)")
	flows.Flags().BoolVar(&faulted, "faulted", false, "only exchanges a rule acted on")
	flows.Flags().IntVar(&limit, "limit", 50, "how many exchanges (1 to 500; with --all: per request)")
	flows.Flags().BoolVar(&all, "all", false, "follow the cursor to the oldest exchange the gateway keeps")

	cmd.AddCommand(set, flows)
	return cmd
}

// sinceTime reads --since: a time, or how long ago.
func (a *app) sinceTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return a.clk().Now().Add(-d), nil
	}
	return time.Time{}, failf(ExitError, "--since %q is neither a time like 2026-10-25T12:00:00Z nor a duration like 5m", s)
}

// printFlows writes the exchanges as a table: when, which device, what, the answer, the rules that acted, the bytes.
func (a *app) printFlows(ctx context.Context, cl *client, items []model.InterceptFlow) {
	names := map[string]string{}
	for _, f := range items {
		if f.Device != nil {
			// one read of the devices, only when an exchange has one; a gateway that cannot be asked again shows the ids
			if devs, err := listAll[model.DeviceView](ctx, cl, "/devices", nil); err == nil {
				for _, d := range devs {
					names[d.Id.String()] = d.Name
				}
			}
			break
		}
	}
	tw := tabwriter.NewWriter(a.env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tDEVICE\tKIND\tREQUEST\tSTATUS\tACTED\tBYTES")
	for _, f := range items {
		dev := f.Client
		if f.Device != nil {
			if n := names[f.Device.String()]; n != "" {
				dev = n
			} else {
				dev = f.Device.String()
			}
		}
		acted := "-"
		if f.Actions != nil && len(*f.Actions) > 0 {
			parts := make([]string, len(*f.Actions))
			for i, x := range *f.Actions {
				parts[i] = string(x)
			}
			acted = strings.Join(parts, ",")
		}
		status := "-"
		if f.Status != nil {
			status = strconv.Itoa(*f.Status)
		}
		size := fmt.Sprintf("%d/%d", deref64(f.RequestBytes), deref64(f.ResponseBytes))
		if f.Protocol == model.InterceptFlowProtocol("websocket") {
			size += fmt.Sprintf(" %d msg", deref64(f.Messages))
		}
		req := flowRequest(f)
		if f.Error != nil && *f.Error != "" {
			req += "  [" + *f.Error + "]"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.Time.UTC().Format(time.RFC3339), dev, f.Protocol, req, status, acted, size)
	}
	_ = tw.Flush()
}

// flowRequest is what the exchange asked for: "GET api.example.com/v1/a", or the host (or address) of a stream.
func flowRequest(f model.InterceptFlow) string {
	host := f.OriginalDst
	if f.Host != nil && *f.Host != "" {
		host = *f.Host
	}
	if f.Method == nil && f.Path == nil {
		return host
	}
	var b strings.Builder
	if f.Method != nil && *f.Method != "" {
		b.WriteString(*f.Method + " ")
	}
	b.WriteString(host)
	if f.Path != nil {
		b.WriteString(*f.Path)
	}
	return b.String()
}

func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (a *app) tlsCaCmd() *cobra.Command {
	var out string
	ca := &cobra.Command{
		Use:   "ca",
		Short: "Download the test CA that a development firmware installs",
		Long: `Downloads the certificate of the test CA (PEM) from the gateway: the CA that signs the expired, not-yet-valid and
hostname-mismatch certificates of the TLS cases, and the certificates the interception proxy shows. A development firmware that
trusts it can tell those cases from an unknown issuer, and a device must trust it for the interception. The unknown CA, which is never
distributed, and every key stay on the gateway. Without --out the certificate goes to standard output.`,
		Example: `  chaosctl tls ca --out test-ca.pem
  chaosctl tls ca regenerate --yes --out test-ca.pem`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if _, err := cl.download(cmd.Context(), "/tls/ca", nil, &buf); err != nil {
				return apiFail(err)
			}
			return a.writeCA(out, buf.Bytes())
		},
	}
	ca.Flags().StringVarP(&out, "out", "o", "", "write to this file instead of standard output")
	var yes bool
	var regenOut string
	regen := &cobra.Command{
		Use:   "regenerate",
		Short: "Replace the test CA with a new one (devices must install the new certificate)",
		Long: `Replaces the test CA (certificate and key) with a new one and prints the new certificate like 'chaosctl tls ca'. The TLS
responder and the interception proxy load it within seconds. Every device that installed the old certificate trusts nothing the
gateway shows it until it installs the new one: the cases signed by the test CA and the interception are rejected as an unknown issuer
until then. The unknown CA is not changed. The call needs a token with scope full and is recorded in the audit log, so it is only made
with --yes.`,
		Example: `  chaosctl tls ca regenerate --yes --out test-ca.pem`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return failf(ExitError, "this replaces the test CA: every device that installed the old certificate trusts nothing the gateway shows it until it installs the new one; run it again with --yes")
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			pem, err := cl.postRaw(cmd.Context(), "/tls/ca/regenerate")
			if err != nil {
				return apiFail(err)
			}
			fmt.Fprintln(a.env.Stderr, "the test CA is replaced: install the new certificate on the devices that trust it")
			return a.writeCA(regenOut, pem)
		},
	}
	regen.Flags().BoolVar(&yes, "yes", false, "confirm that the devices have to install the new certificate")
	regen.Flags().StringVarP(&regenOut, "out", "o", "", "write the new certificate to this file instead of standard output")
	ca.AddCommand(regen)
	return ca
}

// writeCA writes the certificate to a file (it is public: mode 0644) or to standard output.
func (a *app) writeCA(out string, pem []byte) error {
	if out == "" {
		_, err := a.env.Stdout.Write(pem)
		return err
	}
	if err := writeFileAtomic(out, publicFileMode, func(w io.Writer) error { _, err := w.Write(pem); return err }); err != nil {
		return failf(ExitError, "%v", err)
	}
	fmt.Fprintf(a.env.Stderr, "wrote %s\n", out)
	return nil
}

// postRaw sends a POST without a body and returns the body of the answer, which is not JSON.
func (c *client) postRaw(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res, err := c.send(ctx, http.MethodPost, path, nil, nil, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, problemOf(res.StatusCode, raw)
	}
	return raw, nil
}
