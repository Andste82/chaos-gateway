package ctl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The probes (M26a): `chaosctl probe list` shows the virtual clients of the gateway with the address they got, `probe add` makes
// one in a test network and `probe remove` takes one away. A probe is an object of the configuration, not an overlay, so add and
// remove make a revision from a merge patch and apply it, the way the web UI does after its preview; that needs a token with the
// scope full (list needs read). A probe change is never lockout-relevant, so the apply does not wait for a confirmation.

// probeAddress is the first address of a probe, "-" until its DHCP client has one.
func probeAddress(p model.ProbeView) string {
	if p.Observed.Addresses != nil && len(*p.Observed.Addresses) > 0 {
		return (*p.Observed.Addresses)[0]
	}
	return "-"
}

// probeState says in a word whether the probe has its address and is there, as the web UI does.
func probeState(p model.ProbeView) string {
	switch {
	case probeAddress(p) == "-":
		return "no address yet"
	case p.Observed.Online:
		return "online"
	default:
		return "offline"
	}
}

// findProbe is the probe with this name (ignoring case) or UUID.
func findProbe(probes []model.ProbeView, ref string) (model.ProbeView, bool) {
	for _, p := range probes {
		if strings.EqualFold(p.Config.Name, ref) || strings.EqualFold(p.Id.String(), ref) {
			return p, true
		}
	}
	return model.ProbeView{}, false
}

// applyConfig makes a revision from the merge patch against the active one and applies it (the preview of the web UI is the
// patch itself here). A candidate that cannot be applied is discarded again, so none is left behind. force removes the overlays
// and aborts the runs that refer to what the revision deletes.
func (a *app) applyConfig(ctx context.Context, cl *client, patch map[string]any, message string, force bool) (model.ApplyResult, error) {
	// the ETag names the revision the patch is made against
	h, err := cl.call(ctx, http.MethodGet, "/revisions/active", nil, nil, nil)
	if err != nil {
		return model.ApplyResult{}, apiFail(err)
	}
	extra := http.Header{}
	extra.Set("Content-Type", "application/merge-patch+json")
	extra.Set("If-Match", h.Get("ETag"))
	var rev model.Revision
	if _, err := cl.callWith(ctx, http.MethodPost, "/revisions", url.Values{"message": {message}}, extra, patch, &rev); err != nil {
		return model.ApplyResult{}, apiFail(err)
	}
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	var res model.ApplyResult
	id := strconv.FormatInt(rev.Id, 10)
	if _, err := cl.call(ctx, http.MethodPost, "/revisions/"+id+"/apply", q, nil, &res); err != nil {
		cctx, cancel := cleanupContext()
		defer cancel()
		_, _ = cl.call(cctx, http.MethodDelete, "/revisions/"+id, nil, nil, nil)
		return model.ApplyResult{}, apiFail(err)
	}
	if res.Status == model.ApplyResultStatusPendingConfirm {
		return res, failf(ExitError, "revision %d waits for confirmation and is rolled back unless it is confirmed in the web UI in time", res.Revision)
	}
	return res, nil
}

func (a *app) probeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "probe", Short: "The probes: virtual clients of the test networks"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the probes with the address they got",
		Long: `Lists the probes of the active revision: the virtual clients that are ports of the bridges of the test networks, with the
network, the address and MAC they got by DHCP, and whether they are online. A probe without an address has not got its DHCP lease
yet. A probe is also a device: faults, rules and profiles target it by its name, and a diagnostic runs from it.`,
		Example: `  chaosctl probe list
  chaosctl probe list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			probes, err := listAll[model.ProbeView](cmd.Context(), cl, "/probes", nil)
			if err != nil {
				return apiFail(err)
			}
			if a.asJSON {
				a.printJSON(model.ProbeViewPage{Items: append([]model.ProbeView{}, probes...)})
				return nil
			}
			if len(probes) == 0 {
				fmt.Fprintln(a.env.Stderr, "no probes; make one with `chaosctl probe add NAME --network NETWORK`")
				return nil
			}
			// the networks by name; a gateway that cannot be asked again shows the ids
			names := map[string]string{}
			if nets, err := listAll[struct {
				Id     string `json:"id"`
				Config struct {
					Name string `json:"name"`
				} `json:"config"`
			}](cmd.Context(), cl, "/networks", nil); err == nil {
				for _, n := range nets {
					names[n.Id] = n.Config.Name
				}
			}
			tw := tabwriter.NewWriter(a.env.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tNETWORK\tADDRESS\tMAC\tSTATE\tID")
			for _, p := range probes {
				network := p.Config.Network
				if n := names[network]; n != "" {
					network = n
				}
				mac := "-"
				if p.Observed.Macs != nil && len(*p.Observed.Macs) > 0 {
					mac = (*p.Observed.Macs)[0]
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", printable(p.Config.Name), printable(network), probeAddress(p), mac, probeState(p), p.Id)
			}
			_ = tw.Flush()
			return nil
		},
	}

	var network string
	var trustsCA bool
	add := &cobra.Command{
		Use:   "add <name> --network <network>",
		Short: "Make a probe in a test network",
		Long: `Adds a probe to the configuration and applies it: a virtual client that is a port of the test network's bridge, in a
namespace of its own, with an address from the network's DHCP. The name is shared with the devices and WireGuard clients. The
network is a local test network, by name or UUID. --trusts-test-ca makes a diagnostic from the probe verify certificates that
the gateway's test CA signed.

The change goes into a new revision that is applied at once, as the web UI does after its preview. It needs a token with the scope
full. The probe has no address until its DHCP client got a lease: wait for it with 'chaosctl wait device <name> --online'.`,
		Example: `  chaosctl probe add ci-probe --network IoT
  chaosctl probe add tls-probe --network IoT --trusts-test-ca`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if network == "" {
				return failf(ExitError, "--network is required: the test network the probe is a port of")
			}
			p := map[string]any{"name": args[0], "network": network}
			if trustsCA {
				p["trusts_test_ca"] = true
			}
			id := uuid.NewString()
			cl, err := a.client()
			if err != nil {
				return err
			}
			res, err := a.applyConfig(cmd.Context(), cl, map[string]any{"probes": map[string]any{id: p}}, "chaosctl: add probe "+args[0], false)
			if err != nil {
				return err
			}
			if a.asJSON {
				a.printJSON(map[string]any{"id": id, "name": args[0], "revision": res.Revision})
				return nil
			}
			fmt.Fprintf(a.env.Stdout, "added probe %s (%s) in revision %d\n", printable(args[0]), id, res.Revision)
			return nil
		},
	}
	add.Flags().StringVar(&network, "network", "", "the test network the probe is a port of, by name or UUID")
	add.Flags().BoolVar(&trustsCA, "trusts-test-ca", false, "a diagnostic from the probe trusts the gateway's test CA")

	var force bool
	remove := &cobra.Command{
		Use:   "remove <name|id>",
		Short: "Remove a probe",
		Long: `Removes a probe from the configuration and applies it: its namespace and its address go. The change goes into a new
revision that is applied at once; it needs a token with the scope full.

The gateway refuses a configuration that names a probe it does not have: while an access rule, a configured fault, a group, a port
forward or a scenario names the probe, the removal is refused with the objects that do (change them first, in the web UI or with
the API). An overlay or a run that targets the probe also blocks it; --force removes those overlays and aborts those runs
together with the probe.`,
		Example: `  chaosctl probe remove ci-probe
  chaosctl probe remove ci-probe --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			probes, err := listAll[model.ProbeView](cmd.Context(), cl, "/probes", nil)
			if err != nil {
				return apiFail(err)
			}
			p, ok := findProbe(probes, args[0])
			if !ok {
				return failf(ExitError, "there is no probe %q", args[0])
			}
			res, err := a.applyConfig(cmd.Context(), cl, map[string]any{"probes": map[string]any{p.Id.String(): nil}}, "chaosctl: remove probe "+p.Config.Name, force)
			if err != nil {
				return err
			}
			if a.asJSON {
				a.printJSON(res)
				return nil
			}
			fmt.Fprintf(a.env.Stdout, "removed probe %s (%s) in revision %d\n", printable(p.Config.Name), p.Id, res.Revision)
			if res.RemovedOverlays != nil && len(*res.RemovedOverlays) > 0 {
				fmt.Fprintf(a.env.Stderr, "removed %d overlay(s) that targeted it\n", len(*res.RemovedOverlays))
			}
			if res.AbortedRuns != nil && len(*res.AbortedRuns) > 0 {
				fmt.Fprintf(a.env.Stderr, "aborted %d run(s) that targeted it\n", len(*res.AbortedRuns))
			}
			return nil
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "also remove the overlays and abort the runs that target the probe")

	cmd.AddCommand(list, add, remove)
	return cmd
}
