package ctl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The application-layer families (M23b, M34): `chaosctl dns set`, `chaosctl time set`, `chaosctl tls set` and `chaosctl dhcp set`
// make a DNS fault, a time fault, a TLS case and a DHCP test action as overlays owned by the token, with the flags of the forms
// of the web UI. They check what is
// certain without the gateway (an action that needs a field the command line lacks, a field that the action does not use);
// what the gateway knows (a name, an address, a delay, Kea's configuration) is left to it, and its refusal is printed with
// the pointer and exits with 2, as for every other command.

// actionName is the API's name of an action or a case as typed: "wrong-answer" and "Wrong_Answer" are `wrong_answer`.
func actionName(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
}

// oneOf reports whether s is one of the values.
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// unusedFlags is the error of flags the action does not use: the gateway refuses them too, and says it less plainly.
func unusedFlags(action string, given map[string]bool) error {
	var names []string
	for _, f := range []string{"--delay", "--answer", "--answer-ttl", "--lease-time", "--ip", "--router", "--dns-server", "--domain", "--no-domain", "--ntp-server", "--option"} {
		if given[f] {
			names = append(names, f)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return failf(ExitError, "the action %s does not use %s", action, strings.Join(names, ", "))
}

// ---- DNS

var dnsActions = []string{"nxdomain", "servfail", "timeout", "delay", "wrong_answer", "truncate", "short_ttl"}

// dnsFlags are the fields of a DNS fault on the command line.
type dnsFlags struct {
	names, answers []string
	delay, ttl     string
}

func (d *dnsFlags) add(fs *pflag.FlagSet) {
	fs.StringSliceVar(&d.names, "name", nil, "only queries for this host name (exact or *.suffix; repeat or comma separated); without it every name")
	fs.StringVar(&d.delay, "delay", "", "delay: how long the answer is held back (500ms, 2s)")
	fs.StringSliceVar(&d.answers, "answer", nil, "wrong-answer: the IPv4 address the name is answered with (repeat or comma separated)")
	fs.StringVar(&d.ttl, "answer-ttl", "", "short-ttl: the TTL every answer is given (5s; 0s: must not be cached)")
}

// body is the DNS fault of the action and the flags. `fs` tells which flags were given.
func (d *dnsFlags) body(action string, fs *pflag.FlagSet) (map[string]any, error) {
	if !oneOf(action, dnsActions...) {
		return nil, failf(ExitError, "%q is no DNS action: one of %s", action, strings.Join(dnsActions, ", "))
	}
	given := map[string]bool{"--delay": fs.Changed("delay"), "--answer": fs.Changed("answer"), "--answer-ttl": fs.Changed("answer-ttl")}
	need := map[string]string{"delay": "--delay", "wrong_answer": "--answer", "short_ttl": "--answer-ttl"}
	flag := need[action]
	if flag != "" && !given[flag] {
		return nil, failf(ExitError, "the action %s needs %s", action, flag)
	}
	if flag != "" {
		given[flag] = false
	}
	if err := unusedFlags(action, given); err != nil {
		return nil, err
	}
	body := map[string]any{"action": action}
	if len(d.names) > 0 {
		body["names"] = d.names
	}
	switch action {
	case "delay":
		body["delay"] = d.delay
	case "wrong_answer":
		body["answers"] = d.answers
	case "short_ttl":
		body["ttl"] = d.ttl
	}
	return body, nil
}

func (a *app) dnsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dns", Short: "DNS faults of the gateway's DNS proxy"}
	var tf targetFlags
	var lf lifetimeFlags
	var df dnsFlags
	set := &cobra.Command{
		Use:   "set <action>",
		Short: "Make the DNS proxy fail, delay or falsify its answers for a device, group or network, with a TTL",
		Long: `Creates a DNS fault as an overlay owned by the token and prints the id of the overlay. The call returns once the
gateway has verified the change. The action is one of nxdomain, servfail, timeout (no answer), delay (--delay),
wrong-answer (--answer) and truncate (the client falls back to TCP) and short-ttl (--answer-ttl). Without --name the
fault applies to every name; the same names set again replace the earlier fault. (--ttl is the lifetime of the
overlay, --answer-ttl the TTL of the answers.)`,
		Example: `  chaosctl dns set nxdomain --device esp32-42 --name broker.example.com --ttl 2m
  chaosctl dns set delay --network IoT --delay 2s --ttl 30s
  chaosctl dns set wrong-answer --device esp32-42 --name broker.example.com --answer 10.9.0.1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := actionName(args[0])
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			body, err := df.body(action, cmd.Flags())
			if err != nil {
				return err
			}
			req := map[string]any{"target": scope, "dns": body}
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
			a.printOverlay(ov, fmt.Sprintf("dns %s set on %s", action, tf.describe()))
			return nil
		},
	}
	tf.add(set.Flags())
	df.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with --overlay' renews it")
	cmd.AddCommand(set)
	return cmd
}

// ---- Time

// timeKinds are the faults of the NTP server by the name of `chaosctl time set`: the first four serve a time (with the fields
// of the same name, which may be combined), the others are the whole fault (the API's `answer`).
var timeKinds = []string{"offset", "absolute", "skew", "leap", "unsynchronized", "kiss_of_death", "silence", "invalid"}

var (
	timeKissCodes = []string{"RATE", "DENY", "RSTR"}
	timeInvalids  = []string{"origin_mismatch", "zero_transmit", "wrong_mode"}
)

// timeFlags are the fields of a time fault on the command line.
type timeFlags struct {
	offset, absolute, leap, kiss, invalid, to string
	skew                                      float64
	ports                                     []int
}

func (t *timeFlags) add(fs *pflag.FlagSet) {
	fs.StringVar(&t.offset, "offset", "", "offset: how far the served time is from the correct one, signed (+400d, -90s, -1y2d; units ms s m h d w y)")
	fs.StringVar(&t.absolute, "absolute", "", "absolute: the instant the served time shows when the fault takes effect, RFC 3339 (1970-01-01T00:00:00Z)")
	fs.Float64Var(&t.skew, "skew-ppm", 0, "skew: parts per million the served clock runs fast (negative: slow) from the moment the fault takes effect")
	fs.StringVar(&t.leap, "leap", "", "leap: insert (leap indicator 1, 23:59:60) or delete (leap indicator 2)")
	fs.StringVar(&t.kiss, "kiss-code", "", "kiss-of-death: RATE (poll less often), DENY or RSTR (stop using the server)")
	fs.StringVar(&t.invalid, "invalid", "", "invalid: origin-mismatch, zero-transmit or wrong-mode")
	fs.StringVar(&t.to, "to", "", "only queries for this server: an address or prefix, or a hostname (time.example.com, *.pool.ntp.org); without it every server")
	fs.IntSliceVar(&t.ports, "port", nil, "only these destination ports (repeat or comma separated); without them 123")
}

// body is the time fault of the kind and the flags. `fs` tells which flags were given.
func (t *timeFlags) body(kind string, fs *pflag.FlagSet) (map[string]any, error) {
	if !oneOf(kind, timeKinds...) {
		return nil, failf(ExitError, "%q is no time fault: one of %s", kind, strings.Join(timeKinds, ", "))
	}
	given := map[string]bool{"--offset": fs.Changed("offset"), "--absolute": fs.Changed("absolute"), "--skew-ppm": fs.Changed("skew-ppm"),
		"--leap": fs.Changed("leap"), "--kiss-code": fs.Changed("kiss-code"), "--invalid": fs.Changed("invalid")}
	// the flag each kind needs, and the others it may be combined with
	need := map[string]string{"offset": "--offset", "absolute": "--absolute", "skew": "--skew-ppm", "leap": "--leap", "kiss_of_death": "--kiss-code", "invalid": "--invalid"}
	served := oneOf(kind, "offset", "absolute", "skew", "leap")
	if flag := need[kind]; flag != "" && !given[flag] {
		return nil, failf(ExitError, "the fault %s needs %s", kind, flag)
	}
	var unused []string
	for _, f := range []string{"--offset", "--absolute", "--skew-ppm", "--leap", "--kiss-code", "--invalid"} {
		if given[f] && f != need[kind] && (!served || !oneOf(f, "--offset", "--absolute", "--skew-ppm", "--leap")) {
			unused = append(unused, f)
		}
	}
	if len(unused) > 0 {
		return nil, failf(ExitError, "the fault %s does not use %s", kind, strings.Join(unused, ", "))
	}
	if given["--offset"] && given["--absolute"] {
		return nil, failf(ExitError, "--offset and --absolute exclude each other: the served time is either shifted or fixed")
	}
	body := map[string]any{}
	if given["--offset"] {
		body["offset"] = t.offset
	}
	if given["--absolute"] {
		body["absolute"] = t.absolute
	}
	if given["--skew-ppm"] {
		body["skew_ppm"] = t.skew
	}
	if given["--leap"] {
		leap := strings.ToLower(strings.TrimSpace(t.leap))
		if !oneOf(leap, "insert", "delete") {
			return nil, failf(ExitError, "--leap %q is insert or delete", t.leap)
		}
		body["leap"] = leap
	}
	switch kind {
	case "unsynchronized", "silence":
		body["answer"] = kind
	case "kiss_of_death":
		code := strings.ToUpper(strings.TrimSpace(t.kiss))
		if !oneOf(code, timeKissCodes...) {
			return nil, failf(ExitError, "--kiss-code %q is one of %s", t.kiss, strings.Join(timeKissCodes, ", "))
		}
		body["answer"], body["kiss_code"] = kind, code
	case "invalid":
		answer := actionName(t.invalid)
		if !oneOf(answer, timeInvalids...) {
			return nil, failf(ExitError, "--invalid %q is one of %s", t.invalid, strings.ReplaceAll(strings.Join(timeInvalids, ", "), "_", "-"))
		}
		body["answer"] = answer
	}
	if len(t.ports) > 0 {
		body["ports"] = t.ports
	}
	if t.to != "" {
		body["destination"] = destinationOf(t.to)
	}
	return body, nil
}

func (a *app) timeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "time", Short: "Time faults of the gateway's NTP server"}
	var tf targetFlags
	var lf lifetimeFlags
	var xf timeFlags
	set := &cobra.Command{
		Use:   "set <fault>",
		Short: "Make the NTP server serve a device, group or network a wrong time, or no usable answer, with a TTL",
		Long: `Creates a time fault as an overlay owned by the token and prints the id of the overlay. The call returns once the
gateway has verified the change. The fault is one of
  offset            the correct time plus --offset (+400d, -90s)
  absolute          the instant --absolute at the moment the fault takes effect, running on from there
  skew              a clock that runs fast or slow by --skew-ppm, on top of --offset or --absolute if you give one
  leap              leap indicator 1 or 2 (--leap insert or delete); with --absolute a few minutes before midnight UTC
  unsynchronized    leap indicator 3 and stratum 16: a correct client ignores the server
  kiss-of-death     stratum 0 with --kiss-code RATE, DENY or RSTR as the reference id
  silence           no answer; the query is counted
  invalid           a malformed answer (--invalid origin-mismatch, zero-transmit or wrong-mode)
The first four may be combined (--offset or --absolute with --skew-ppm and --leap); the others are the whole fault. Without
--to the fault applies to every NTP server the device asks, the gateway's own address included, and the gateway redirects the
NTP traffic of the target to its server whether or not the network redirects hardcoded servers; with --to only to that server.
The target is a device, a group or a network. The served time jumps when the fault starts and when it ends, and a device sees
either only when it polls next ('chaosctl wait ntp <device>' waits for that). The same selection set again replaces the earlier
fault. (--ttl is the lifetime of the overlay.)`,
		Example: `  chaosctl time set offset --offset +400d --device esp32-42 --ttl 5m
  chaosctl time set absolute --absolute 2038-01-19T03:09:07Z --network IoT --ttl 2m
  chaosctl time set skew --skew-ppm 1000 --device esp32-42
  chaosctl time set kiss-of-death --kiss-code RATE --group sensors --to time.google.com
  chaosctl time set silence --device esp32-42 --ttl 10m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := actionName(args[0])
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			body, err := xf.body(kind, cmd.Flags())
			if err != nil {
				return err
			}
			req := map[string]any{"target": scope, "time": body}
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
			a.printOverlay(ov, fmt.Sprintf("time %s set on %s", kind, tf.describe()))
			return nil
		},
	}
	tf.addNotGlobal(set.Flags())
	xf.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with --overlay' renews it")
	cmd.AddCommand(set)
	return cmd
}

// ---- TLS

var tlsCases = []string{"untrusted_ca", "self_signed", "expired", "not_yet_valid", "hostname_mismatch", "handshake_reset", "handshake_close", "handshake_stall"}

// tlsFlags are the selection of a TLS case on the command line.
type tlsFlags struct {
	ports []int
	sni   []string
	to    string
	cut   bool
}

func (t *tlsFlags) add(fs *pflag.FlagSet) {
	fs.IntSliceVar(&t.ports, "port", nil, "only these destination ports (repeat or comma separated); without them 443 and 8883")
	fs.StringSliceVar(&t.sni, "sni", nil, "only connections whose ClientHello asks for this server name (exact or *.suffix; repeat or comma separated)")
	fs.StringVar(&t.to, "to", "", "only connections to this destination: an address or prefix, a hostname, 'uplink', or a network (network:<name> / hostname:<name> when the form is ambiguous)")
	fs.BoolVar(&t.cut, "cut-existing", false, "also reset the connections that are open already (they are selected at their next packet)")
}

// body is the TLS case of the case and the flags.
func (t *tlsFlags) body(c string) (map[string]any, error) {
	if c == "intercept" {
		return nil, failf(ExitError, "the case intercept has settings and HTTP rules: make it with 'chaosctl intercept set', or from a document with 'chaosctl overlay create -f <file>' (schema TlsCase)")
	}
	if !oneOf(c, tlsCases...) {
		return nil, failf(ExitError, "%q is no TLS case: one of %s", c, strings.Join(tlsCases, ", "))
	}
	body := t.selection()
	body["case"] = c
	return body, nil
}

func (a *app) tlsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tls", Short: "TLS cases of the gateway's TLS responder"}
	var tf targetFlags
	var lf lifetimeFlags
	var xf tlsFlags
	set := &cobra.Command{
		Use:   "set <case>",
		Short: "Show a device, group or network a certificate it must reject, or a handshake that fails, with a TTL",
		Long: `Creates a TLS case as an overlay owned by the token and prints the id of the overlay. The call returns once the
gateway has verified the change. The case is one of untrusted-ca and self-signed (any device must reject them), expired,
not-yet-valid and hostname-mismatch (signed by the test CA: only a device that trusts it can tell them from an unknown
issuer), and handshake-reset, handshake-close and handshake-stall. Without --port the case selects the connections to the
ports 443 and 8883; only new connections meet it unless --cut-existing says otherwise. The interception case has its own
rules and is made with 'intercept set'.`,
		Example: `  chaosctl tls set expired --device esp32-42 --port 8883 --ttl 2m
  chaosctl tls set untrusted-ca --network IoT --sni broker.example.com --cut-existing --ttl 30s`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := actionName(args[0])
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			body, err := xf.body(c)
			if err != nil {
				return err
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
			a.printOverlay(ov, fmt.Sprintf("tls %s set on %s", c, tf.describe()))
			return nil
		},
	}
	tf.add(set.Flags())
	xf.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with --overlay' renews it")
	cmd.AddCommand(set, a.tlsCaCmd())
	return cmd
}

// ---- DHCP

var dhcpActions = []string{"short_lease", "delete_lease", "force_new_ip", "set_options", "silence"}

// dhcpFlags are the fields of a DHCP action on the command line.
type dhcpFlags struct {
	leaseTime, ip, router, domain string
	noDomain                      bool
	dns, ntp, options             []string
}

func (d *dhcpFlags) add(fs *pflag.FlagSet) {
	fs.StringVar(&d.leaseTime, "lease-time", "", "short-lease: the lease time (7s; at least 1s)")
	fs.StringVar(&d.ip, "ip", "", "force-new-ip: the address the device is moved to (in its network); without it the gateway chooses one")
	fs.StringVar(&d.router, "router", "", "set-options: the router (option 3)")
	fs.StringSliceVar(&d.dns, "dns-server", nil, "set-options: a DNS server (option 6; repeat or comma separated)")
	fs.StringVar(&d.domain, "domain", "", "set-options: the domain (option 15)")
	fs.BoolVar(&d.noDomain, "no-domain", false, "set-options: take the domain away")
	fs.StringSliceVar(&d.ntp, "ntp-server", nil, "set-options: an NTP server (option 42; repeat or comma separated)")
	fs.StringArrayVar(&d.options, "option", nil, "set-options: a custom option as code=value (66=tftp.lab; repeat)")
}

// customOptions reads --option code=value.
func customOptions(specs []string) ([]map[string]any, error) {
	var out []map[string]any
	for _, s := range specs {
		code, value, ok := strings.Cut(s, "=")
		n, err := strconv.Atoi(strings.TrimSpace(code))
		if !ok || err != nil || strings.TrimSpace(value) == "" {
			return nil, failf(ExitError, "--option %q is not code=value, such as 66=tftp.lab", s)
		}
		out = append(out, map[string]any{"code": n, "value": strings.TrimSpace(value)})
	}
	return out, nil
}

// body is the DHCP action of the action and the flags. `fs` tells which flags were given.
func (d *dhcpFlags) body(action string, fs *pflag.FlagSet) (map[string]any, error) {
	if !oneOf(action, dhcpActions...) {
		return nil, failf(ExitError, "%q is no DHCP action: one of %s", action, strings.Join(dhcpActions, ", "))
	}
	given := map[string]bool{
		"--lease-time": fs.Changed("lease-time"), "--ip": fs.Changed("ip"), "--router": fs.Changed("router"), "--dns-server": fs.Changed("dns-server"),
		"--domain": fs.Changed("domain"), "--no-domain": d.noDomain, "--ntp-server": fs.Changed("ntp-server"), "--option": fs.Changed("option"),
	}
	body := map[string]any{"action": action}
	switch action {
	case "short_lease":
		if !given["--lease-time"] {
			return nil, failf(ExitError, "the action short_lease needs --lease-time")
		}
		given["--lease-time"] = false
		body["lease_time"] = d.leaseTime
	case "force_new_ip":
		if given["--ip"] {
			body["ip"] = d.ip
		}
		given["--ip"] = false
	case "set_options":
		if given["--domain"] && d.noDomain {
			return nil, failf(ExitError, "--domain and --no-domain exclude each other")
		}
		opts := map[string]any{}
		if given["--router"] {
			opts["router"] = d.router
		}
		if len(d.dns) > 0 {
			opts["dns_servers"] = d.dns
		}
		if given["--domain"] {
			opts["domain"] = d.domain
		}
		if d.noDomain {
			opts["domain"] = ""
		}
		if len(d.ntp) > 0 {
			opts["ntp_servers"] = d.ntp
		}
		custom, err := customOptions(d.options)
		if err != nil {
			return nil, err
		}
		if len(custom) > 0 {
			opts["custom"] = custom
		}
		if len(opts) == 0 {
			return nil, failf(ExitError, "the action set_options needs one of --router, --dns-server, --domain, --no-domain, --ntp-server or --option")
		}
		for _, f := range []string{"--router", "--dns-server", "--domain", "--no-domain", "--ntp-server", "--option"} {
			given[f] = false
		}
		body["options"] = opts
	}
	if err := unusedFlags(action, given); err != nil {
		return nil, err
	}
	return body, nil
}

func (a *app) dhcpCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dhcp", Short: "DHCP test actions of the gateway's DHCP server"}
	var tf targetFlags
	var lf lifetimeFlags
	var df dhcpFlags
	set := &cobra.Command{
		Use:   "set <action>",
		Short: "Change what the DHCP server does for a device or a network, with a TTL",
		Long: `Creates a DHCP test action as an overlay owned by the token and prints the id of the overlay. The call returns once the
DHCP server has taken the configuration. The action is one of short-lease (--lease-time; a client that asks for a longer one
is given the short one), delete-lease (acts once, when it is created; a renewing client gets the same address again),
force-new-ip (a device only: its reservation moves, so the renewing client is refused and asks for the new address; --ip names
it, else the gateway chooses and the command says which), set-options (--router, --dns-server, --domain or --no-domain,
--ntp-server, --option) and silence (the server does not answer). The server cannot make a client renew at once: a short lease
brings the renewal forward. The target is a device or a network; the more specific one wins for a device.`,
		Example: `  chaosctl dhcp set short-lease --network IoT --lease-time 7s --ttl 5m
  chaosctl dhcp set force-new-ip --device esp32-42 --ttl 10m
  chaosctl dhcp set set-options --device esp32-42 --dns-server 192.0.2.53 --option 66=tftp.lab
  chaosctl dhcp set silence --device esp32-42 --ttl 1m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := actionName(args[0])
			scope, err := tf.scope()
			if err != nil {
				return err
			}
			if action == "force_new_ip" && tf.device == "" {
				return failf(ExitError, "force-new-ip moves a device's reservation: name the device with --device")
			}
			body, err := df.body(action, cmd.Flags())
			if err != nil {
				return err
			}
			req := map[string]any{"target": scope, "dhcp": body}
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
			what := fmt.Sprintf("dhcp %s set on %s", action, tf.describe())
			// the address the gateway chose is only in the overlay as it reads back
			if ov.Dhcp != nil && ov.Dhcp.Ip != nil {
				what += ": moves to " + *ov.Dhcp.Ip
			}
			a.printOverlay(ov, what)
			return nil
		},
	}
	tf.addDeviceOrNetwork(set.Flags())
	df.add(set.Flags())
	lf.add(set.Flags(), "", "the owner must renew the overlay within this time; 'chaosctl with --overlay' renews it")
	cmd.AddCommand(set)
	return cmd
}
