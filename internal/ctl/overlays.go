package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// targetFlags select what an overlay acts on: exactly one of a device, a group, a network or everything.
type targetFlags struct {
	device, group, network string
	global                 bool
}

func (t *targetFlags) add(fs *pflag.FlagSet) {
	fs.StringVar(&t.device, "device", "", "target: a device (name or UUID)")
	fs.StringVar(&t.group, "group", "", "target: a group")
	fs.StringVar(&t.network, "network", "", "target: a network")
	fs.BoolVar(&t.global, "global", false, "target: every test, WireGuard and remote network")
}

// scope is the target as the API takes it.
func (t *targetFlags) scope() (map[string]any, error) {
	out := map[string]any{}
	n := 0
	for k, v := range map[string]string{"device": t.device, "group": t.group, "network": t.network} {
		if v != "" {
			out[k] = v
			n++
		}
	}
	if t.global {
		out["global"] = true
		n++
	}
	if n != 1 {
		return nil, failf(ExitError, "name exactly one target: --device, --group, --network or --global")
	}
	return out, nil
}

func (t *targetFlags) describe() string {
	switch {
	case t.device != "":
		return "device " + t.device
	case t.group != "":
		return "group " + t.group
	case t.network != "":
		return "network " + t.network
	}
	return "everything"
}

// lifetimeFlags are the TTL and the lease of an overlay.
type lifetimeFlags struct {
	ttl, lease string
}

func (l *lifetimeFlags) add(fs *pflag.FlagSet, leaseDefault, leaseHelp string) {
	fs.StringVar(&l.ttl, "ttl", "", "remove the overlay after this time (5m, 90s); without it it stays until it is deleted")
	fs.StringVar(&l.lease, "lease", leaseDefault, leaseHelp)
}

// apply puts the lifetime flags that were given into an overlay request, replacing the ones the request has; a lease
// of 0 means none and removes the request's own.
func (l *lifetimeFlags) apply(req map[string]any) error {
	if l.ttl != "" {
		if _, err := time.ParseDuration(l.ttl); err != nil {
			return failf(ExitError, "--ttl %q is not a duration like 5m", l.ttl)
		}
		req["ttl"] = l.ttl
	}
	if l.lease != "" {
		d, err := time.ParseDuration(l.lease)
		if err != nil {
			return failf(ExitError, "--lease %q is not a duration like 30s", l.lease)
		}
		if d < 0 {
			return failf(ExitError, "--lease %q must not be negative", l.lease)
		}
		if d > 0 {
			req["lease"] = l.lease
		} else {
			delete(req, "lease")
		}
	}
	return nil
}

// createOverlay writes an overlay and returns it as the gateway has it once it is verified.
func (a *app) createOverlay(ctx context.Context, cl *client, req map[string]any) (*model.Overlay, error) {
	var ov model.Overlay
	if _, err := cl.call(ctx, http.MethodPost, "/overlays", nil, req, &ov); err != nil {
		return nil, err
	}
	return &ov, nil
}

// printOverlay says what an overlay is on stderr and prints its id (or, with --json, the overlay) on stdout.
func (a *app) printOverlay(ov *model.Overlay, what string) {
	line := fmt.Sprintf("%s: overlay %s", what, ov.Id)
	if ov.ExpiresAt != nil {
		line += ", removed at " + ov.ExpiresAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintln(a.env.Stderr, line)
	if a.asJSON {
		a.printJSON(ov)
		return
	}
	fmt.Fprintln(a.env.Stdout, ov.Id)
}

func (a *app) printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(a.env.Stdout, string(b))
}

func (a *app) profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Profiles of the gateway"}
	var tf targetFlags
	var lf lifetimeFlags
	apply := &cobra.Command{
		Use:   "apply <profile>",
		Short: "Activate a profile on a device, group, network or everything",
		Long: `Activates a profile (bad-lte, offline, a profile of the configuration, …) as an overlay owned by the
token, and prints the id of the overlay. The call returns once the gateway has verified the change in the
kernel. Take it away again with 'chaosctl overlay delete <id>' or let the TTL do it.`,
		Example: `  chaosctl profile apply bad-lte --device esp32-42 --ttl 5m
  id=$(chaosctl profile apply offline --network IoT --ttl 30s)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			req := map[string]any{"target": scope, "profile": args[0]}
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
			a.printOverlay(ov, fmt.Sprintf("profile %s applied to %s", args[0], tf.describe()))
			return nil
		},
	}
	tf.add(apply.Flags())
	lf.add(apply.Flags(), "", "the owner must renew the overlay within this time (a CI job that dies leaves no fault behind); 'chaosctl with' renews it")
	cmd.AddCommand(apply)
	return cmd
}

// faultFlags describe one fault of the impairment family on the command line.
type faultFlags struct {
	body                                           string
	latency, jitter, loss, rate, corrupt, dup, reo string
	flap                                           string
	blackout                                       bool
	protocol                                       string
	ports                                          []int
	to                                             string
}

func (f *faultFlags) add(fs *pflag.FlagSet) {
	fs.StringVar(&f.latency, "latency", "", "delay in each direction (200ms)")
	fs.StringVar(&f.jitter, "jitter", "", "variation of the delay (50ms)")
	fs.StringVar(&f.loss, "loss", "", "packet loss (10%)")
	fs.StringVar(&f.rate, "rate", "", "bit rate per device (2Mbit)")
	fs.StringVar(&f.corrupt, "corrupt", "", "corrupted packets (0.5%)")
	fs.StringVar(&f.dup, "duplicate", "", "duplicated packets (1%)")
	fs.StringVar(&f.reo, "reorder", "", "reordered packets (5%, needs --latency)")
	fs.BoolVar(&f.blackout, "blackout", false, "drop everything")
	fs.StringVar(&f.flap, "flapping", "", "alternate up and down, 'up/down' (20s/10s)")
	fs.StringVar(&f.protocol, "protocol", "", "only this protocol: tcp, udp, icmp")
	fs.IntSliceVar(&f.ports, "port", nil, "only these destination ports (repeat or comma separated)")
	fs.StringVar(&f.to, "to", "", "only traffic to this destination: an address or prefix, a hostname (mqtt.example.com, *.example.com), 'uplink', or a network (network:<name> / hostname:<name> when the form is ambiguous)")
	fs.StringVar(&f.body, "body", "", "the fault as a JSON or YAML document, or @file (the flags above are added to it)")
}

// given reports whether any flag of the fault was set.
func (f *faultFlags) given() bool {
	return f.latency != "" || f.jitter != "" || f.loss != "" || f.rate != "" || f.corrupt != "" || f.dup != "" || f.reo != "" ||
		f.flap != "" || f.blackout || f.protocol != "" || len(f.ports) > 0 || f.to != "" || f.body != ""
}

// faultBody is the fault the flags describe.
func (f *faultFlags) faultBody(read func(string) (any, error)) (map[string]any, error) {
	body := map[string]any{}
	if f.body != "" {
		doc, err := read(f.body)
		if err != nil {
			return nil, failf(ExitError, "--body: %v", err)
		}
		m, ok := doc.(map[string]any)
		if !ok {
			return nil, failf(ExitError, "--body is not an object")
		}
		body = m
	}
	for k, v := range map[string]string{"latency": f.latency, "jitter": f.jitter, "loss": f.loss, "rate": f.rate, "corrupt": f.corrupt,
		"duplicate": f.dup, "reorder": f.reo, "protocol": f.protocol} {
		if v != "" {
			body[k] = v
		}
	}
	if f.blackout {
		body["blackout"] = true
	}
	if f.flap != "" {
		up, down, ok := strings.Cut(f.flap, "/")
		if !ok || up == "" || down == "" {
			return nil, failf(ExitError, "--flapping %q is not 'up/down', e.g. 20s/10s", f.flap)
		}
		body["flapping"] = map[string]any{"up": up, "down": down}
	}
	if len(f.ports) > 0 {
		body["ports"] = f.ports
	}
	if f.to != "" {
		body["destination"] = destinationOf(f.to)
	}
	if len(body) == 0 {
		return nil, failf(ExitError, "a fault needs at least one of --latency, --jitter, --loss, --rate, --corrupt, --duplicate, --reorder, --blackout, --flapping or --body")
	}
	return body, nil
}

// destinationOf reads a destination: `uplink`, an address or prefix (a bare address is a host route, /32 or /128 by
// family), a hostname (a valid name with a dot, or `*.suffix`), else the name of a network. `network:<name>` and
// `hostname:<name>` say which one is meant where the form is ambiguous (a network may be called lab.net).
func destinationOf(s string) map[string]any {
	switch {
	case s == "uplink":
		return map[string]any{"uplink": true}
	case strings.HasPrefix(s, "network:"):
		return map[string]any{"network": strings.TrimPrefix(s, "network:")}
	case strings.HasPrefix(s, "hostname:"):
		return map[string]any{"hostname": strings.TrimPrefix(s, "hostname:")}
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return map[string]any{"cidr": p.String()}
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return map[string]any{"cidr": netip.PrefixFrom(ip, ip.BitLen()).String()}
	}
	switch {
	case strings.Contains(s, "/") && !strings.ContainsAny(s, " "), strings.Contains(s, ".") && strings.Trim(s, "0123456789.") == "":
		// an address or prefix that does not parse (10.0.0, 10.0.0.0/33): the gateway says what is wrong with it
		return map[string]any{"cidr": s}
	case hostnamePattern.MatchString(s) && (strings.Contains(s, ".") || strings.HasPrefix(s, "*.")):
		return map[string]any{"hostname": s}
	}
	return map[string]any{"network": s}
}

// hostnamePattern is the Hostname of the API: an exact name or `*.suffix`.
var hostnamePattern = regexp.MustCompile(`^(\*\.)?([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

func (a *app) faultCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "fault", Short: "Faults of the gateway"}
	var tf targetFlags
	var lf lifetimeFlags
	var ff faultFlags
	set := &cobra.Command{
		Use:   "set",
		Short: "Impair the traffic of a device, group or network, with a TTL",
		Long: `Creates a fault as an overlay owned by the token and prints the id of the overlay. The call returns once the
gateway has verified the change in the kernel, so the fault is on when the command returns. The same fault
(same target, same match) set again replaces the earlier one with its complete new parameters.`,
		Example: `  chaosctl fault set --device esp32-42 --latency 200ms --jitter 50ms --loss 5% --ttl 2m
  chaosctl fault set --network IoT --blackout --protocol tcp --port 8883 --ttl 30s`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			body, err := ff.faultBody(a.readDocument)
			if err != nil {
				return err
			}
			req := map[string]any{"target": scope, "fault": body}
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
			a.printOverlay(ov, "fault set on "+tf.describe())
			return nil
		},
	}
	tf.add(set.Flags())
	ff.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with' renews it")
	cmd.AddCommand(set)
	return cmd
}

func (a *app) overlayCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "overlay", Short: "The overlays (faults, profile activations, rules) that are active"}

	var file string
	var tf targetFlags
	var lf lifetimeFlags
	create := &cobra.Command{
		Use:   "create -f <file>",
		Short: "Create an overlay from a request document (any kind: rule, DNS, TLS, WireGuard action, …)",
		Long: `Sends a complete overlay request (schema OverlayRequest in api/openapi.yaml, YAML or JSON; '-' reads the
standard input). --device, --group, --network, --global, --ttl and --lease replace the document's target, ttl
and lease when given.`,
		Example: `  chaosctl overlay create -f cut-mqtt.yaml --device esp32-42 --ttl 1m`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return failf(ExitError, "-f <file> is required")
			}
			doc, err := a.readDocument(file)
			if err != nil {
				return failf(ExitError, "%v", err)
			}
			req, ok := doc.(map[string]any)
			if !ok {
				return failf(ExitError, "%s is not an overlay request (an object)", file)
			}
			if tf.device != "" || tf.group != "" || tf.network != "" || tf.global {
				scope, err := tf.scope()
				if err != nil {
					return err
				}
				req["target"] = scope
			}
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
			a.printOverlay(ov, "overlay created ("+string(ov.Kind)+")")
			return nil
		},
	}
	create.Flags().StringVarP(&file, "file", "f", "", "the overlay request, a YAML or JSON file ('-': standard input)")
	tf.add(create.Flags())
	lf.add(create.Flags(), "", "the owner must renew the overlay within this time")

	var owner, device, network string
	list := &cobra.Command{
		Use:   "list",
		Short: "List the active overlays",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			q := url.Values{}
			for k, v := range map[string]string{"owner": owner, "device": device, "network": network} {
				if v != "" {
					q.Set(k, v)
				}
			}
			items, err := listAll[model.Overlay](cmd.Context(), cl, "/overlays", q)
			if err != nil {
				return apiFail(err)
			}
			if a.asJSON {
				a.printJSON(items)
				return nil
			}
			sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
			tw := tabwriter.NewWriter(a.env.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tKIND\tOWNER\tEXPIRES")
			for _, o := range items {
				exp := "-"
				if o.ExpiresAt != nil {
					exp = o.ExpiresAt.UTC().Format(time.RFC3339)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", o.Id, o.Kind, ownerString(o.Owner), exp)
			}
			return tw.Flush()
		},
	}
	list.Flags().StringVar(&owner, "owner", "", "'self', or the id of a token or run")
	list.Flags().StringVar(&device, "device", "", "only overlays that act on this device")
	list.Flags().StringVar(&network, "network", "", "only overlays on this network")

	del := &cobra.Command{
		Use:   "delete <id>...",
		Short: "Remove overlays",
		Long:  "Removes overlays and returns once the gateway has verified the change. An overlay that is gone already (its TTL ran out) is not an error.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var failed []string
			for _, id := range args {
				if _, err := cl.call(cmd.Context(), http.MethodDelete, "/overlays/"+url.PathEscape(id), nil, nil, nil); err != nil {
					if ae := asAPIError(err); ae != nil && ae.Status == http.StatusNotFound {
						fmt.Fprintf(a.env.Stderr, "overlay %s is gone already\n", id)
						continue
					}
					failed = append(failed, fmt.Sprintf("%s: %v", id, err))
					continue
				}
				fmt.Fprintf(a.env.Stderr, "overlay %s removed\n", id)
			}
			if len(failed) > 0 {
				return failf(ExitError, "%s", strings.Join(failed, "\n"))
			}
			return nil
		},
	}
	renew := &cobra.Command{
		Use:   "renew <id>...",
		Short: "Renew the lease of overlays",
		Long: `Renews the lease of overlays that were created with --lease (the owner must renew an overlay within its lease, else the
gateway removes it). 'chaosctl with' does this itself; a job that creates overlays with 'profile apply --lease' or
'fault set --lease' renews them from a loop of its own.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			for _, id := range args {
				if _, err := cl.call(cmd.Context(), http.MethodPost, "/overlays/"+url.PathEscape(id)+"/renew", nil, nil, nil); err != nil {
					return apiFail(err)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(create, list, del, renew)
	return cmd
}

func ownerString(o model.Owner) string {
	s := string(o.Type)
	if o.Name != nil && *o.Name != "" {
		return s + ":" + *o.Name
	}
	return s + ":" + o.Id
}

func (a *app) resetCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Remove the caller's overlays and abort its runs",
		Long: `Removes the overlays the token owns and aborts its runs. With --all it does that for every owner, which
needs a token with scope full. A CI job's first step can call it to start from a clean gateway.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			q := url.Values{}
			if all {
				q.Set("owner", "all")
			}
			var res model.ResetResult
			if _, err := cl.call(cmd.Context(), http.MethodPost, "/reset", q, nil, &res); err != nil {
				return apiFail(err)
			}
			if a.asJSON {
				a.printJSON(res)
				return nil
			}
			fmt.Fprintf(a.env.Stdout, "removed %d overlays, aborted %d runs (generation %d)\n", res.RemovedOverlays, res.AbortedRuns, res.Generation)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every owner's overlays and runs (scope full)")
	return cmd
}

// readDocument reads a YAML or JSON document from a file, from the standard input ("-") or from the
// argument itself when it starts with `{` or `[`. `@file` is a file too.
func (a *app) readDocument(spec string) (any, error) {
	var raw []byte
	var err error
	switch t := strings.TrimSpace(spec); {
	case spec == "-":
		raw, err = io.ReadAll(a.env.Stdin)
	case strings.HasPrefix(t, "{") || strings.HasPrefix(t, "["):
		raw = []byte(spec)
	default:
		raw, err = os.ReadFile(strings.TrimPrefix(spec, "@"))
	}
	if err != nil {
		return nil, err
	}
	return decodeYAML(raw)
}
