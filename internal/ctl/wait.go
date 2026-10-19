package ctl

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// connSpec is a connection a device must have: `tcp/8883`, `udp/5683`, or with the address of the peer
// `tcp/10.0.0.5:8883`.
type connSpec struct {
	proto string
	port  int
	addr  netip.Addr
}

func (c connSpec) String() string {
	if c.addr.IsValid() {
		return fmt.Sprintf("%s/%s:%d", c.proto, c.addr, c.port)
	}
	return fmt.Sprintf("%s/%d", c.proto, c.port)
}

func parseConnSpec(s string) (connSpec, error) {
	bad := func() (connSpec, error) {
		return connSpec{}, failf(ExitError, "--connected %q is not proto/port, e.g. tcp/8883 or udp/10.0.0.5:5683", s)
	}
	proto, rest, ok := strings.Cut(s, "/")
	proto = strings.ToLower(proto)
	if !ok || (proto != "tcp" && proto != "udp") {
		return bad()
	}
	var c connSpec
	c.proto = proto
	if host, port, found := strings.Cut(rest, ":"); found {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return bad()
		}
		c.addr, rest = addr, port
	}
	p, err := strconv.Atoi(rest)
	if err != nil || p < 1 || p > 65535 {
		return bad()
	}
	c.port = p
	return c, nil
}

// established reports whether a tracked connection is established now: a TCP connection in the state
// ESTABLISHED (not one in its handshake, and not one that is closing), any other flow that has been answered.
func established(f model.Flow) bool {
	if f.Protocol == model.FlowProtocolTcp {
		return strings.EqualFold(deref(f.State), "ESTABLISHED")
	}
	return !strings.EqualFold(deref(f.State), "UNREPLIED") && f.Download != nil && deref(f.Download.Packets) > 0
}

func (c connSpec) matches(f model.Flow) bool {
	if string(f.Protocol) != c.proto || deref(f.Dport) != c.port {
		return false
	}
	if c.addr.IsValid() {
		if a, err := netip.ParseAddr(f.Dst); err != nil || a != c.addr {
			return false
		}
	}
	return established(f)
}

func (a *app) waitDeviceCmd() *cobra.Command {
	var online bool
	var connected string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "device <name>",
		Short: "Wait until a device is online and, optionally, has a connection",
		Long: `Waits until the device is online (seen now: a confirmed neighbor entry, a connection or a WireGuard handshake;
a lease alone does not count) and, with --connected, has an established connection to the destination port,
and returns 0. A device the gateway does not know yet is waited for too: it may be discovered while waiting.
The connection must exist when it is looked at (the wait polls the gateway's connection table).

Returns 1 when the time runs out and 2 when the gateway cannot be asked.`,
		Example: `  chaosctl wait device esp32-42 --online --connected tcp/8883 --timeout 90s`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !online && connected == "" {
				return failf(ExitError, "say what to wait for: --online and/or --connected")
			}
			var want *connSpec
			if connected != "" {
				c, err := parseConnSpec(connected)
				if err != nil {
					return err
				}
				want = &c
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			return a.waitDevice(cmd.Context(), cl, args[0], online, want, timeout)
		},
	}
	cmd.Flags().BoolVar(&online, "online", false, "the device is online")
	cmd.Flags().StringVar(&connected, "connected", "", "the device has an established connection: tcp/8883, udp/10.0.0.5:5683")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "give up after this time (exit 1)")
	return cmd
}

func (a *app) waitDevice(ctx context.Context, cl *client, name string, online bool, conn *connSpec, timeout time.Duration) error {
	sigs, stop := a.signals()
	defer stop()
	started := a.clk().Monotonic()
	deadline := a.clk().NewTimer(timeout)
	defer deadline.Stop()
	tick := a.clk().NewTicker(a.env.Poll)
	defer tick.Stop()
	var onlineSince, connectedSince bool
	var missing string
	failures := 0
	for {
		dev, err := a.lookDevice(ctx, cl, name)
		var flows []model.Flow
		if err == nil && dev != nil && conn != nil {
			flows, err = listAll[model.Flow](ctx, cl, "/flows", url.Values{"device": {dev.Id.String()}})
		}
		if err != nil {
			if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
				return apiFail(err)
			}
			failures++
			fmt.Fprintf(a.env.Stderr, "the gateway does not answer (%d of %d): %v\n", failures, pollFailures, err)
			if failures >= pollFailures {
				return failf(ExitError, "lost the gateway while waiting for device %s: %v", name, err)
			}
		} else {
			failures = 0
			missing = ""
			switch {
			case dev == nil:
				missing = "the gateway does not know the device"
			case online && !dev.Observed.Online:
				missing = "the device is not online"
			}
			if dev != nil && online && dev.Observed.Online && !onlineSince {
				onlineSince = true
				fmt.Fprintf(a.env.Stderr, "[%6.1fs] device %s is online\n", a.since(started), name)
			}
			if missing == "" && conn != nil {
				hit := false
				for _, f := range flows {
					if conn.matches(f) {
						hit = true
						break
					}
				}
				if hit && !connectedSince {
					connectedSince = true
					fmt.Fprintf(a.env.Stderr, "[%6.1fs] device %s has a connection %s\n", a.since(started), name, conn)
				}
				if !hit {
					missing = fmt.Sprintf("the device has no established connection %s", conn)
				}
			}
			if missing == "" {
				fmt.Fprintf(a.env.Stdout, "device %s is ready after %.1fs\n", name, a.since(started))
				return nil
			}
		}
		select {
		case <-tick.C():
		case <-deadline.C():
			return failf(ExitFailed, "timed out after %v: %s", timeout, missing)
		case sig := <-sigs:
			return failf(ExitError, "%v received while waiting for device %s", sig, name)
		case <-ctx.Done():
			return failf(ExitError, "interrupted while waiting for device %s", name)
		}
	}
}

// lookDevice returns the device, or nil when the gateway does not know it.
func (a *app) lookDevice(ctx context.Context, cl *client, name string) (*model.DeviceView, error) {
	var dv model.DeviceView
	if _, err := cl.call(ctx, http.MethodGet, "/devices/"+url.PathEscape(name), nil, nil, &dv); err != nil {
		if ae := asAPIError(err); ae != nil && ae.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &dv, nil
}
