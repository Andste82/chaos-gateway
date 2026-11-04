package ctl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
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

Returns 1 when the time runs out, and 2 when the gateway cannot be asked (ten polls in a row fail, or the time
runs out while the latest poll failed or none was answered).`,
		Example: `  chaosctl wait device esp32-42 --online --connected tcp/8883 --timeout 90s`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sigs, stop := a.signals()
			defer stop()
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
			return a.waitDevice(cmd.Context(), cl, args[0], online, want, timeout, sigs)
		},
	}
	cmd.Flags().BoolVar(&online, "online", false, "the device is online")
	cmd.Flags().StringVar(&connected, "connected", "", "the device has an established connection: tcp/8883, udp/10.0.0.5:5683")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "give up after this time (exit 1)")
	return cmd
}

// slowPoll is how long a poll that the end of the wait cut off must have been going on to count as unanswered.
const slowPoll = time.Second

func (a *app) waitDevice(ctx context.Context, cl *client, name string, online bool, conn *connSpec, timeout time.Duration, sigs <-chan os.Signal) error {
	started := a.clk().Monotonic()
	deadline := a.clk().NewTimer(timeout)
	defer deadline.Stop()
	wctx, endWatch := watch(ctx, sigs, deadline.C(), fmt.Errorf("timed out after %v", timeout))
	defer endWatch(context.Canceled)
	tick := a.clk().NewTicker(a.env.Poll)
	defer tick.Stop()
	var onlineSince, connectedSince bool
	var missing string
	// lastErr is why the latest poll failed (nil: it was answered); asked: some poll was answered
	var lastErr error
	asked := false
	failures := 0
	for {
		// no poll after the wait has ended: it would be cut off at once and say nothing about the gateway
		if wctx.Err() == nil {
			began := a.clk().Monotonic()
			pctx, pcancel := context.WithTimeout(wctx, a.pollTimeout())
			dev, err := a.lookDevice(pctx, cl, name)
			var flows []model.Flow
			if err == nil && dev != nil && conn != nil {
				flows, err = listAll[model.Flow](pctx, cl, "/flows", url.Values{"device": {dev.Id.String()}})
			}
			pcancel()
			if err != nil {
				if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
					return apiFail(err)
				}
				if wctx.Err() != nil {
					// what ended the wait cut the poll off: it says something about the gateway only if the poll had
					// had time to be answered in (a healthy gateway answers in milliseconds; one that happened to be in
					// flight when the time ran out is no evidence)
					if a.clk().Monotonic()-began >= slowPoll {
						lastErr = errors.New("the poll was not answered when the time ran out")
					}
				} else {
					lastErr = err
					failures++
					fmt.Fprintf(a.env.Stderr, "the gateway does not answer (%d of %d): %v\n", failures, pollFailures, err)
					if failures >= pollFailures {
						return failf(ExitError, "lost the gateway while waiting for device %s: %v", name, err)
					}
				}
			} else {
				asked, lastErr = true, nil
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
		}
		select {
		case <-tick.C():
		case <-wctx.Done():
		}
		if wctx.Err() == nil {
			continue
		}
		var se *signalError
		var de *deadlineError
		switch cause := context.Cause(wctx); {
		case errors.As(cause, &se):
			return failf(ExitError, "%v received while waiting for device %s", se.sig, name)
		case errors.As(cause, &de):
			// a time that ran out while the gateway could not be asked is no verdict on the device
			if lastErr != nil || !asked {
				if lastErr == nil {
					lastErr = errors.New("no poll was answered")
				}
				return failf(ExitError, "%v, and the gateway could not be asked: %v", cause, lastErr)
			}
			return failf(ExitFailed, "%v: %s", cause, missing)
		}
		return failf(ExitError, "interrupted while waiting for device %s", name)
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

// ntpHint is what a wait for an NTP query that ran out of time may mean besides a device that does not ask: the queries the
// server sees are those to the gateway's address and, where the network redirects hardcoded servers or a time fault covers the
// device, those to other servers (plan §2.18, risk 37).
const ntpHint = "the device did not ask the gateway's NTP server (a device with a hardcoded server asks it only where the network redirects hardcoded NTP or a time fault covers it; a device with NTS or another source of time never does; many devices poll once an hour or less)"

func (a *app) waitNTPCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "ntp <device>",
		Short: "Wait until a device asks the gateway's NTP server for the time",
		Long: `Waits for the next query of the device to the gateway's NTP server that arrives after the command began, whatever the server
answered, and returns 0 with the answer it gave. A time fault shows on a device only when it polls next, so a test sets the
fault and waits for this query instead of guessing how long the device takes. A query that the rate limit dropped, and a
connection to the NTS key-establishment port, are not queries. The time of a query is the gateway's own, and so is the moment the
command began (the time of the first answer, to the nanosecond), so the clock of the machine that runs the command does not
matter. The server reports its queries in batches of half a second, so a query is seen that late.

Returns 1 when the time runs out without a query, and 2 when the gateway cannot be asked (ten polls in a row fail, or the time
runs out while the latest poll failed or none was answered). A device the gateway does not know yet is waited for too.`,
		Example: `  chaosctl time set offset --offset +400d --device esp32-42 --ttl 10m
  chaosctl wait ntp esp32-42 --timeout 5m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sigs, stop := a.signals()
			defer stop()
			cl, err := a.client()
			if err != nil {
				return err
			}
			return a.waitNTP(cmd.Context(), cl, args[0], timeout, sigs)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "give up after this time (exit 1)")
	return cmd
}

// lookNTP reads the NTP log of the device from `since` on and returns the first query after it (nil when there is none) and the
// start of the wait on the gateway's clock; known says whether the gateway knows the device. Without a `since` (the first poll)
// the start is the time the gateway names in its answer (`now`, to the nanosecond): a query counts when its time is not before
// it, so one that arrived between the start of the command and its first poll is not taken for an old one, and one answered
// before the command began, with the fault not yet in force, does not end the wait. A gateway that sends no `now` gets the Date
// of the answer, which has a resolution of a second, so a query of the second before the command may count then. When the
// answer has neither, the newest time in the log is the start and nothing counts yet (the log is in the order of arrival, not
// of time: the server reports in batches).
func (a *app) lookNTP(ctx context.Context, cl *client, name string, since time.Time) (query *model.NtpQueryLogEntry, start time.Time, known bool, err error) {
	q := url.Values{"device": {name}, "limit": {"500"}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339Nano))
	}
	var pg model.NtpQueryPage
	hdr, err := cl.call(ctx, http.MethodGet, "/ntp/queries", q, nil, &pg)
	if err != nil {
		if ae := asAPIError(err); ae != nil && ae.Status == http.StatusNotFound {
			return nil, dateOf(hdr), false, nil
		}
		return nil, time.Time{}, false, err
	}
	start = since
	if start.IsZero() {
		if start = pg.Now; start.IsZero() {
			start = dateOf(hdr)
		}
		if start.IsZero() {
			for i := range pg.Items {
				if pg.Items[i].Time.After(start) {
					start = pg.Items[i].Time
				}
			}
			return nil, start.Add(time.Nanosecond), true, nil // the entry that sets the start is not after it
		}
	}
	for i := range pg.Items { // newest first
		e := &pg.Items[i]
		isQuery := (e.Kind == nil || *e.Kind == model.Query) && (e.Answer == nil || *e.Answer != model.NtpQueryLogEntryAnswerDropped)
		if isQuery && !e.Time.Before(start) && (query == nil || e.Time.Before(query.Time)) {
			query = e // the oldest by time: the log is in the order of arrival
		}
	}
	return query, start, true, nil
}

// dateOf is the time in the Date header of an answer, zero when it has none.
func dateOf(h http.Header) time.Time {
	t, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		return time.Time{}
	}
	return t
}

func (a *app) waitNTP(ctx context.Context, cl *client, name string, timeout time.Duration, sigs <-chan os.Signal) error {
	started := a.clk().Monotonic()
	deadline := a.clk().NewTimer(timeout)
	defer deadline.Stop()
	wctx, endWatch := watch(ctx, sigs, deadline.C(), fmt.Errorf("timed out after %v", timeout))
	defer endWatch(context.Canceled)
	tick := a.clk().NewTicker(a.env.Poll)
	defer tick.Stop()
	// base is when the wait began on the gateway's clock (see lookNTP): a query counts when it is not earlier
	var base time.Time
	var missing string
	var lastErr error
	asked := false
	failures := 0
	for {
		// no poll after the wait has ended: it would be cut off at once and say nothing about the gateway
		if wctx.Err() == nil {
			began := a.clk().Monotonic()
			pctx, pcancel := context.WithTimeout(wctx, a.pollTimeout())
			hit, start, known, err := a.lookNTP(pctx, cl, name, base)
			pcancel()
			if err != nil {
				if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
					return apiFail(err)
				}
				if wctx.Err() != nil {
					// the end of the wait cut the poll off: it says something about the gateway only if it had time to be answered in
					if a.clk().Monotonic()-began >= slowPoll {
						lastErr = errors.New("the poll was not answered when the time ran out")
					}
				} else {
					lastErr = err
					failures++
					fmt.Fprintf(a.env.Stderr, "the gateway does not answer (%d of %d): %v\n", failures, pollFailures, err)
					if failures >= pollFailures {
						return failf(ExitError, "lost the gateway while waiting for the NTP query of device %s: %v", name, err)
					}
				}
			} else {
				asked, lastErr = true, nil
				failures = 0
				if base.IsZero() {
					base = start // the first answer fixes the start, also of a device the gateway does not know yet
				}
				switch {
				case !known:
					missing = "the gateway does not know the device"
				case hit != nil:
					fmt.Fprintf(a.env.Stdout, "device %s asked the NTP server after %.1fs: %s\n", name, a.since(started), describeNTPQuery(hit))
					return nil
				default:
					missing = ntpHint
				}
			}
		}
		select {
		case <-tick.C():
		case <-wctx.Done():
		}
		if wctx.Err() == nil {
			continue
		}
		var se *signalError
		var de *deadlineError
		switch cause := context.Cause(wctx); {
		case errors.As(cause, &se):
			return failf(ExitError, "%v received while waiting for the NTP query of device %s", se.sig, name)
		case errors.As(cause, &de):
			// a time that ran out while the gateway could not be asked is no verdict on the device
			if lastErr != nil || !asked {
				if lastErr == nil {
					lastErr = errors.New("no poll was answered")
				}
				return failf(ExitError, "%v, and the gateway could not be asked: %v", cause, lastErr)
			}
			return failf(ExitFailed, "%v: %s", cause, missing)
		}
		return failf(ExitError, "interrupted while waiting for the NTP query of device %s", name)
	}
}

// describeNTPQuery says how the server answered a query: the answer, the time served and how far it is from the correct
// time, the fault that shaped it and the server asked.
func describeNTPQuery(e *model.NtpQueryLogEntry) string {
	answer := "unknown"
	if e.Answer != nil {
		answer = strings.ReplaceAll(string(*e.Answer), "_", "-")
	}
	parts := []string{"answer " + answer}
	if e.ServedTime != nil {
		parts = append(parts, "served "+e.ServedTime.UTC().Format(time.RFC3339))
	}
	if e.OffsetMs != nil {
		parts = append(parts, fmt.Sprintf("offset %dms", *e.OffsetMs))
	}
	if e.Fault != nil {
		parts = append(parts, "fault "+e.Fault.String())
	}
	if e.Server != nil && *e.Server != "" {
		parts = append(parts, "server "+*e.Server)
	}
	return strings.Join(parts, ", ")
}
