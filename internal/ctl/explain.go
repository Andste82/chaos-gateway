package ctl

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func (a *app) explainCmd() *cobra.Command {
	var device, src, dst, protocol string
	var port int
	cmd := &cobra.Command{
		Use:   "explain (--device <name> | --src <ip>) --dst <ip|hostname> [--protocol tcp] [--port 8883]",
		Short: "Say what the gateway does with the traffic of a device towards a destination",
		Long: `Asks the gateway (GET /explain) for the verdict of the access rules on one traffic tuple and for the fault that
wins in each family, with the faults it overrides and the route the kernel takes. It reads the configuration and
the overlays as they are now; it sends no packet. Use it to find out why a connection is refused or slow while a
test runs, or to check what a profile or fault does to a destination before running the test.

--dst is an IPv4 address or a hostname. Without --protocol and --port the tuple has none, and only the rules and
faults that do not select by protocol or port are considered.`,
		Example: `  chaosctl explain --device esp32-42 --dst 203.0.113.10 --protocol tcp --port 8883
  chaosctl explain --src 10.10.0.31 --dst mqtt.example.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (device == "") == (src == "") {
				return failf(ExitError, "name the traffic's source: --device or --src, not both")
			}
			if dst == "" {
				return failf(ExitError, "--dst is required: an IPv4 address or a hostname")
			}
			q := url.Values{"dst": {dst}}
			if device != "" {
				q.Set("device", device)
			}
			if src != "" {
				q.Set("src", src)
			}
			if protocol != "" {
				switch protocol {
				case "any", "tcp", "udp", "icmp":
					q.Set("protocol", protocol)
				default:
					return failf(ExitError, "--protocol is tcp, udp, icmp or any")
				}
			}
			if cmd.Flags().Changed("port") {
				if port < 1 || port > 65535 {
					return failf(ExitError, "--port is a port number, 1 to 65535")
				}
				q.Set("port", strconv.Itoa(port))
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			var ex model.Explanation
			if _, err := cl.call(cmd.Context(), http.MethodGet, "/explain", q, nil, &ex); err != nil {
				return apiFail(err)
			}
			if a.asJSON {
				a.printJSON(ex)
				return nil
			}
			printExplanation(a.env.Stdout, &ex, dst, protocol, port)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&device, "device", "", "the device the traffic comes from (name or UUID)")
	f.StringVar(&src, "src", "", "the source address instead of a device (IPv4)")
	f.StringVar(&dst, "dst", "", "the destination: an IPv4 address or a hostname")
	f.StringVar(&protocol, "protocol", "", "tcp, udp, icmp or any (default: none)")
	f.IntVar(&port, "port", 0, "the destination port")
	return cmd
}

// printExplanation prints what the gateway says about a traffic tuple, in the order of the questions one asks: who,
// is it allowed, what happens to it on the way.
func printExplanation(w io.Writer, ex *model.Explanation, dst, protocol string, port int) {
	from := "-"
	if ex.Source.Device != nil && deref(ex.Source.Device.Name) != "" {
		from = deref(ex.Source.Device.Name)
	}
	var detail []string
	if ex.Source.Ip != nil {
		detail = append(detail, *ex.Source.Ip)
	}
	if ex.Source.Network != nil && deref(ex.Source.Network.Name) != "" {
		detail = append(detail, "network "+deref(ex.Source.Network.Name))
	}
	if len(detail) > 0 {
		from += " (" + strings.Join(detail, ", ") + ")"
	}
	to := dst
	if ex.Destination != nil && ex.Destination.Resolved != nil && len(*ex.Destination.Resolved) > 0 {
		to += " (" + strings.Join(*ex.Destination.Resolved, ", ") + ")"
	}
	tuple := ""
	if protocol != "" && protocol != "any" {
		tuple = " " + protocol
	}
	if port > 0 {
		tuple += fmt.Sprintf("/%d", port)
	}
	fmt.Fprintf(w, "%s -> %s%s\n", from, to, tuple)

	acc := ex.Access
	fmt.Fprintf(w, "access: %s (%s)", strings.ToUpper(string(acc.Verdict)), acc.Layer)
	if acc.Reason != nil && *acc.Reason != "" {
		fmt.Fprintf(w, ": %s", *acc.Reason)
	}
	if acc.Rule != nil {
		fmt.Fprintf(w, " [rule %s]", *acc.Rule)
	}
	fmt.Fprintln(w)
	if ex.Service != nil {
		fmt.Fprintf(w, "service: %s\n", *ex.Service)
	}
	if fw := ex.Forward; fw != nil {
		fmt.Fprintf(w, "port forward: %s to port %d", deref(fw.Name), fw.TargetPort)
		if fw.TargetIp != nil {
			fmt.Fprintf(w, " on %s", *fw.TargetIp)
		}
		fmt.Fprintf(w, "; %s\n", fw.Note)
	}
	if len(ex.Faults) == 0 {
		fmt.Fprintln(w, "faults: none")
	} else {
		fmt.Fprintln(w, "faults:")
		for _, fe := range ex.Faults {
			line := string(fe.Family)
			if fe.Tunnel != nil {
				line += " (tunnel " + *fe.Tunnel + ")"
			}
			if fe.Winner != nil {
				line += ": " + faultRefString(fe.Winner)
			}
			fmt.Fprintf(w, "  %s\n", line)
			if fe.Overridden != nil {
				for i := range *fe.Overridden {
					o := &(*fe.Overridden)[i]
					fmt.Fprintf(w, "    overridden: %s", faultRefString(o))
					if o.Reason != nil && *o.Reason != "" {
						fmt.Fprintf(w, " (%s)", *o.Reason)
					}
					fmt.Fprintln(w)
				}
			}
		}
		if acc.Verdict != model.AccessActionAllow {
			fmt.Fprintln(w, "  (the faults take effect only when the access verdict is allow)")
		}
	}
	if r := ex.Route; r != nil {
		switch {
		case deref(r.Unreachable):
			fmt.Fprintf(w, "route: unreachable %s\n", deref(r.Error))
		default:
			line := "route:"
			if r.Gateway != nil {
				line += " via " + *r.Gateway
			}
			if r.Interface != nil {
				line += " dev " + *r.Interface
			}
			fmt.Fprintln(w, line)
		}
	}
}

// faultRefString names a fault or profile part: its name (else its id), the layer it is on, and what it does.
func faultRefString(r *model.FaultRef) string {
	name := deref(r.Name)
	if name == "" {
		name = r.Id.String()
	}
	if r.Profile != nil && deref(r.Profile.Name) != "" {
		name += " (profile " + deref(r.Profile.Name) + ")"
	}
	s := fmt.Sprintf("%s [%s]", name, r.Layer)
	if r.Summary != nil && *r.Summary != "" {
		s += " " + *r.Summary
	}
	return s
}
