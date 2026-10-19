package ctl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// defaultWithLease is the lease of the overlays of 'chaosctl with': if chaosctl is killed without a chance to clean
// up (SIGKILL, a lost machine), the gateway removes them when the renewals stop.
const defaultWithLease = "30s"

func (a *app) withCmd() *cobra.Command {
	var tf targetFlags
	var lf lifetimeFlags
	var ff faultFlags
	var profile string
	var files []string
	cmd := &cobra.Command{
		Use:   "with [flags] -- <command> [args...]",
		Short: "Run a command under a profile or fault and always clean up",
		Long: `Applies a profile and/or a fault (and any overlay request files), runs the command, and removes what it applied
when the command ends, whatever way it ends: it succeeds, it fails, it is not found, or chaosctl is interrupted.
The command's standard input, output and error are chaosctl's; a terminate or hang-up signal is passed to it (an interrupt is the
terminal's Ctrl-C, which reaches the command itself), and a second signal kills it.

The overlays have a lease (30s unless --lease says otherwise; --lease 0: none; an --overlay file keeps the lease it
names unless --lease is given) that chaosctl renews while the command runs, so the gateway takes them away on its own if chaosctl is killed. They get the TTL from --ttl too,
if given. The environment of the command has CHAOSCTL_OVERLAYS, the ids of the overlays, comma separated.

The exit code is the command's, or 128 plus the signal that ended it. chaosctl's own failure (a bad argument, a
gateway that refused the overlay, an overlay that could not be removed after a successful command) is 125, a
command that cannot be run 126, and a command that is not found 127.`,
		Example: `  chaosctl with --device esp32-42 --profile bad-lte --ttl 10m -- pytest tests/test_firmware_update.py
  chaosctl with --network IoT --blackout --protocol tcp --port 8883 -- ./reconnect-test.sh`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash != 0 || len(args) == 0 {
				return failf(ExitInternal, "give the command after '--': chaosctl with [flags] -- <command> [args...]")
			}
			argv := args
			var reqs []map[string]any
			hasTarget := tf.device != "" || tf.group != "" || tf.network != "" || tf.global
			var scope map[string]any
			if hasTarget {
				s, err := tf.scope()
				if err != nil {
					return asInternal(err)
				}
				scope = s
			}
			if profile != "" {
				if scope == nil {
					return failf(ExitInternal, "name the target of the profile: --device, --group, --network or --global")
				}
				reqs = append(reqs, map[string]any{"target": scope, "profile": profile})
			}
			if ff.given() {
				if scope == nil {
					return failf(ExitInternal, "name the target of the fault: --device, --group, --network or --global")
				}
				body, err := ff.faultBody(a.readDocument)
				if err != nil {
					return asInternal(err)
				}
				reqs = append(reqs, map[string]any{"target": scope, "fault": body})
			}
			for _, f := range files {
				doc, err := a.readDocument(f)
				if err != nil {
					return failf(ExitInternal, "--overlay %s: %v", f, err)
				}
				m, ok := doc.(map[string]any)
				if !ok {
					return failf(ExitInternal, "--overlay %s is not an overlay request (an object)", f)
				}
				if scope != nil {
					m["target"] = scope
				}
				reqs = append(reqs, m)
			}
			if len(reqs) == 0 {
				return failf(ExitInternal, "apply something: --profile, a fault (--latency, --loss, --blackout, …) or --overlay <file>")
			}
			// The default lease is for the requests that name none: a lease in an --overlay file is its author's
			// choice, an explicit --lease replaces it (0 takes it away) as it does for 'overlay create'.
			leases := make([]time.Duration, len(reqs))
			for i, r := range reqs {
				if _, has := r["lease"]; !has && lf.lease == "" {
					r["lease"] = defaultWithLease
				}
				if err := lf.apply(r); err != nil {
					return asInternal(err)
				}
				if l, ok := r["lease"].(string); ok {
					d, err := time.ParseDuration(l)
					if err != nil || d < time.Second {
						return failf(ExitInternal, "the lease %q of an overlay must be a duration of at least 1s (--lease 0: none)", l)
					}
					leases[i] = d
				}
			}
			cl, err := a.client()
			if err != nil {
				return asInternal(err)
			}
			return a.runWith(cmd.Context(), cl, reqs, leases, argv)
		},
	}
	// every failure of chaosctl itself, usage included, is 125 here: 2 may be the command's own code
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{code: ExitInternal, err: err} })
	f := cmd.Flags()
	tf.add(f)
	ff.add(f)
	f.StringVar(&profile, "profile", "", "activate this profile")
	f.StringArrayVar(&files, "overlay", nil, "also create the overlay of this request file (repeatable; --device and the like replace its target)")
	lf.add(f, "", "the gateway removes the overlays when they are not renewed within this time; chaosctl renews them (default "+defaultWithLease+" for the requests without a lease, 0: no lease)")
	f.SetInterspersed(false)
	return cmd
}

// runWith creates the overlays, runs the command and removes the overlays again.
func (a *app) runWith(ctx context.Context, cl *client, reqs []map[string]any, leases []time.Duration, argv []string) error {
	// listen from the start: a signal while the overlays are created must not kill chaosctl before it cleans up
	sigs, stopSignals := a.signals()
	defer stopSignals()
	var ids []string
	removeAll := func() error {
		cctx, cancel := cleanupContext()
		defer cancel()
		var failed []string
		for _, id := range ids {
			if _, err := cl.call(cctx, http.MethodDelete, "/overlays/"+url.PathEscape(id), nil, nil, nil); err != nil {
				if ae := asAPIError(err); ae != nil && ae.Status == http.StatusNotFound {
					continue // its TTL ran out
				}
				failed = append(failed, fmt.Sprintf("overlay %s: %v", id, err))
				continue
			}
			fmt.Fprintf(a.env.Stderr, "overlay %s removed\n", id)
		}
		ids = nil
		if len(failed) > 0 {
			return errors.New("could not remove " + strings.Join(failed, "; "))
		}
		return nil
	}
	// whatever happens below, the overlays that were created are removed
	cleaned := false
	defer func() {
		if !cleaned {
			if err := removeAll(); err != nil {
				fmt.Fprintf(a.env.Stderr, "chaosctl: %v\n", err)
			}
		}
	}()
	for _, r := range reqs {
		ov, err := a.createOverlay(ctx, cl, r)
		if err != nil {
			return failf(ExitInternal, "%v", err)
		}
		ids = append(ids, ov.Id.String())
		fmt.Fprintf(a.env.Stderr, "applied %s: overlay %s\n", kindOf(ov), ov.Id)
	}

	rctx, stopRenewing := context.WithCancel(ctx)
	var renewing sync.WaitGroup
	for i, id := range ids {
		if leases[i] > 0 {
			renewing.Add(1)
			go func() {
				defer renewing.Done()
				a.renewOverlay(rctx, cl, id, leases[i])
			}()
		}
	}
	select {
	case sig := <-sigs:
		stopRenewing()
		renewing.Wait()
		return failf(ExitInternal, "%v received before the command started", sig)
	default:
	}
	code, runErr := a.runCommand(argv, ids, sigs)
	stopRenewing()
	renewing.Wait()

	cleaned = true
	cleanErr := removeAll()
	if runErr != nil {
		fmt.Fprintf(a.env.Stderr, "chaosctl: %v\n", runErr)
	}
	if cleanErr != nil {
		fmt.Fprintf(a.env.Stderr, "chaosctl: %v\n", cleanErr)
		if code == 0 {
			return exitWith(ExitInternal)
		}
	}
	if code == 0 {
		return nil
	}
	return exitWith(code)
}

func kindOf(o *model.Overlay) string { return string(o.Kind) }

// renewOverlay renews the lease of an overlay at a third of its length until the context ends. An overlay that is
// gone ends the loop (its TTL ran out, or someone removed it).
func (a *app) renewOverlay(ctx context.Context, cl *client, id string, lease time.Duration) {
	every := lease / 3
	if every < 100*time.Millisecond {
		every = 100 * time.Millisecond
	}
	t := a.clk().NewTicker(every)
	defer t.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		pctx, cancel := context.WithTimeout(ctx, a.pollTimeout())
		_, err := cl.call(pctx, http.MethodPost, "/overlays/"+url.PathEscape(id)+"/renew", nil, nil, nil)
		cancel()
		switch {
		case err == nil:
			failing = false
		case ctx.Err() != nil:
			return
		default:
			if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusNotFound || ae.Status == http.StatusUnprocessableEntity) {
				fmt.Fprintf(a.env.Stderr, "overlay %s is gone: %v\n", id, err)
				return
			}
			if !failing {
				fmt.Fprintf(a.env.Stderr, "the lease of overlay %s could not be renewed: %v\n", id, err)
			}
			failing = true
		}
	}
}

// runCommand runs the command with chaosctl's streams and returns its exit code (127 and 126 when it cannot be
// run, 128 plus the signal when it was killed). The error says why a command could not be run.
func (a *app) runCommand(argv, overlayIDs []string, sigs <-chan os.Signal) (int, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = a.env.Stdin, a.env.Stdout, a.env.Stderr
	environ := a.env.Environ
	if environ == nil {
		environ = os.Environ()
	}
	c.Env = append(append([]string(nil), environ...), "CHAOSCTL_OVERLAYS="+strings.Join(overlayIDs, ","))
	if err := c.Start(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
			return ExitNotFound, fmt.Errorf("%s: command not found", argv[0])
		default:
			return ExitNotRunnable, fmt.Errorf("%s: %w", argv[0], err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	signalled := 0
	for {
		select {
		case err := <-done:
			if err == nil {
				return 0, nil
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					return 128 + int(ws.Signal()), nil
				}
				return ee.ExitCode(), nil
			}
			return ExitInternal, err
		case sig := <-sigs:
			// A terminate signal goes on to the command as it came. An interrupt does not: it is the terminal's Ctrl-C,
			// which reaches the command in the same process group by itself, and passing it on would interrupt it twice.
			// Either way chaosctl keeps waiting so that it can clean up; the second signal ends the command.
			signalled++
			switch {
			case signalled > 1:
				_ = c.Process.Signal(syscall.SIGKILL)
			case sig == os.Interrupt:
			default:
				if ss, ok := sig.(syscall.Signal); ok {
					_ = c.Process.Signal(ss)
				} else {
					_ = c.Process.Signal(syscall.SIGTERM)
				}
			}
		}
	}
}

// asInternal makes any error the failure of chaosctl itself.
func asInternal(err error) error {
	var ee *exitError
	if errors.As(err, &ee) {
		return &exitError{code: ExitInternal, err: ee.err}
	}
	return &exitError{code: ExitInternal, err: err}
}
