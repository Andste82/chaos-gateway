package ctl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// decodeYAML reads a YAML or JSON document into the generic form the API's JSON takes.
func decodeYAML(raw []byte) (any, error) {
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	if v == nil {
		return nil, fmt.Errorf("the document is empty")
	}
	return jsonable(v), nil
}

// jsonable turns the maps with any key that YAML produces into maps with string keys.
func jsonable(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = jsonable(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = jsonable(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = jsonable(e)
		}
		return t
	}
	return v
}

// defaultRunLease is the lease of a run that chaosctl waits for: the gateway aborts the run and removes its
// overlays when the job that started it dies and nobody renews it.
const defaultRunLease = "30s"

// awaitOptions say what to do while and after a run is waited for.
type awaitOptions struct {
	junit, artifacts string
	timeout          time.Duration
	// lease is the lease the run has: the run is renewed at a third of it
	lease time.Duration
	// owns is set for the run this process started: a signal or the timeout aborts it. Waiting for someone
	// else's run only stops waiting.
	owns bool
}

func (a *app) runCmd() *cobra.Command {
	var file, lease string
	var sets []string
	var wait, noQueue bool
	var o awaitOptions
	cmd := &cobra.Command{
		Use:   "run [scenario]",
		Short: "Start a run of a scenario from the gateway or from a file",
		Long: `Starts a run of a scenario that is stored on the gateway (by name or UUID) or of one from a file (-f), whose
fields --set overrides by dotted path. The scenario never becomes part of the gateway's configuration.

Without --wait it prints the id of the run and returns; follow it with 'chaosctl wait run <id>'. With --wait it
waits for the run to end, shows its progress on standard error and its result on standard output, and returns
the result as the exit code: 0 the run passed, 1 a check failed, 2 the run ended as error or was aborted.

A run that is waited for has a lease (30s unless --lease says otherwise; --lease 0: none) that chaosctl renews
while it waits, so a CI job that is killed leaves no fault behind: the gateway aborts the run when the renewals
stop. An interrupt or a terminate signal, and --timeout, abort the run and remove its overlays.

--junit and --artifacts save the JUnit report; --artifacts also the JSON report, the events and the captures
the run made, into a directory.`,
		Example: `  chaosctl run -f mqtt-outage.yaml --set target.device=$DUT --wait --junit report.xml --artifacts out/
  chaosctl run mqtt-outage --wait`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (file == "") == (len(args) == 0) {
				return failf(ExitError, "name a stored scenario or give a scenario file with -f, not both and not neither")
			}
			if !wait && (o.junit != "" || o.artifacts != "" || o.timeout != 0 || lease != "") {
				return failf(ExitError, "--junit, --artifacts, --timeout and --lease belong to --wait: a run that nobody waits for cannot be renewed or reported")
			}
			req := map[string]any{}
			if file != "" {
				doc, err := a.readDocument(file)
				if err != nil {
					return failf(ExitError, "%v", err)
				}
				if _, ok := doc.(map[string]any); !ok {
					return failf(ExitError, "%s is not a scenario (an object)", file)
				}
				req["inline"] = doc
			} else {
				req["scenario"] = args[0]
			}
			params, err := parseSets(sets)
			if err != nil {
				return err
			}
			if len(params) > 0 {
				req["parameters"] = params
			}
			if wait {
				if lease == "" {
					lease = defaultRunLease
				}
				d, err := time.ParseDuration(lease)
				if err != nil || d < 0 {
					return failf(ExitError, "--lease %q is not a duration like 30s", lease)
				}
				if d > 0 {
					if d < time.Second {
						return failf(ExitError, "--lease must be at least 1s")
					}
					req["lease"] = lease
					o.lease = d
				}
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			q := url.Values{}
			if noQueue {
				q.Set("queue", "false")
			}
			var run model.Run
			if _, err := cl.call(cmd.Context(), http.MethodPost, "/runs", q, req, &run); err != nil {
				return apiFail(err)
			}
			fmt.Fprintf(a.env.Stderr, "run %s %s (%s)\n", run.Id, run.Status, run.Scenario.Name)
			if !wait {
				if a.asJSON {
					a.printJSON(run)
				} else {
					fmt.Fprintln(a.env.Stdout, run.Id)
				}
				return nil
			}
			o.owns = true
			return a.await(cmd.Context(), cl, &run, o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&file, "file", "f", "", "scenario file (YAML or JSON; '-': standard input)")
	f.StringArrayVar(&sets, "set", nil, "override a field by dotted path, key=value (repeatable): target.device=esp32-42, steps.slow.at=5s")
	f.BoolVar(&wait, "wait", false, "wait for the run to end and return its result as the exit code")
	f.BoolVar(&noQueue, "no-queue", false, "fail with target_busy instead of queueing behind a run on the same target")
	f.StringVar(&lease, "lease", "", "with --wait: the run is aborted when it is not renewed within this time (default "+defaultRunLease+", 0: none)")
	f.DurationVar(&o.timeout, "timeout", 0, "with --wait: abort the run and return 2 when it has not ended after this time")
	f.StringVar(&o.junit, "junit", "", "with --wait: write the JUnit report to this file")
	f.StringVar(&o.artifacts, "artifacts", "", "with --wait: write the reports, the events and the captures of the run into this directory")
	return cmd
}

// parseSets reads `key=value` arguments.
func parseSets(sets []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, failf(ExitError, "--set %q is not key=value", s)
		}
		out[k] = v
	}
	return out, nil
}

func ended(s model.RunStatus) bool {
	switch s {
	case model.RunStatusPassed, model.RunStatusFailed, model.RunStatusError, model.RunStatusAborted:
		return true
	}
	return false
}

// runExit is the exit code of a run that has ended.
func runExit(s model.RunStatus) int {
	switch s {
	case model.RunStatusPassed:
		return ExitOK
	case model.RunStatusFailed:
		return ExitFailed
	}
	return ExitError
}

// pollFailures is how many requests in a row may fail while waiting before the wait gives up.
const pollFailures = 10

// await waits for a run to end, keeps its lease, reports the progress and the result, saves the reports and
// returns the exit code as an error.
func (a *app) await(ctx context.Context, cl *client, run *model.Run, o awaitOptions) error {
	sigs, stop := a.signals()
	defer stop()
	started := a.clk().Monotonic()
	logf := func(format string, args ...any) {
		fmt.Fprintf(a.env.Stderr, "[%6.1fs] %s\n", a.since(started), fmt.Sprintf(format, args...))
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewDone := make(chan struct{})
	if o.lease > 0 {
		go func() {
			defer close(renewDone)
			a.renewLoop(wctx, cl, run.Id.String(), o.lease, logf)
		}()
	} else {
		close(renewDone)
	}
	stopRenewing := func() {
		cancel()
		<-renewDone
	}

	var deadline <-chan time.Time
	if o.timeout > 0 {
		t := a.clk().NewTimer(o.timeout)
		defer t.Stop()
		deadline = t.C()
	}
	tick := a.clk().NewTicker(a.env.Poll)
	defer tick.Stop()

	id := run.Id.String()
	cur := run
	progress := newProgress(logf)
	progress.update(cur)
	failures := 0
	var interrupted string
	for !ended(cur.Status) {
		select {
		case <-tick.C():
		case sig := <-sigs:
			interrupted = fmt.Sprintf("%v received", sig)
		case <-deadline:
			interrupted = fmt.Sprintf("--timeout %v ran out", o.timeout)
		case <-ctx.Done():
			interrupted = "interrupted"
		}
		if interrupted != "" {
			stopRenewing()
			if !o.owns {
				return failf(ExitError, "%s; the run %s goes on", interrupted, id)
			}
			logf("%s: aborting the run", interrupted)
			actx, acancel := cleanupContext()
			var ab model.Run
			_, err := cl.call(actx, http.MethodPost, "/runs/"+id+"/abort", nil, nil, &ab)
			acancel()
			if err != nil {
				// the run may have ended between the signal and the abort: read its result once more
				rctx, rcancel := cleanupContext()
				var last model.Run
				_, gerr := cl.call(rctx, http.MethodGet, "/runs/"+id, nil, nil, &last)
				rcancel()
				if gerr != nil || !ended(last.Status) {
					return failf(ExitError, "%s, and the run could not be aborted: %v (the lease takes it down if it has one)", interrupted, err)
				}
				ab = last
			}
			cur = &ab
			progress.update(cur)
			break
		}
		var next model.Run
		if _, err := cl.call(ctx, http.MethodGet, "/runs/"+id, nil, nil, &next); err != nil {
			if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusNotFound || ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
				stopRenewing()
				return apiFail(err)
			}
			failures++
			logf("the gateway does not answer (%d of %d): %v", failures, pollFailures, err)
			if failures >= pollFailures {
				stopRenewing()
				return failf(ExitError, "lost the gateway while waiting for run %s: %v", id, err)
			}
			continue
		}
		failures = 0
		cur = &next
		progress.update(cur)
	}
	stopRenewing()

	fmt.Fprintln(a.env.Stderr, "")
	a.printResult(cur, interrupted)
	code := runExit(cur.Status)
	if err := a.saveReports(ctx, cl, cur, o); err != nil {
		fmt.Fprintf(a.env.Stderr, "chaosctl: %v\n", err)
		if code == ExitOK {
			code = ExitError
		}
	}
	if code == ExitOK {
		return nil
	}
	return exitWith(code)
}

// renewLoop renews the lease of a run at a third of its length until the context ends. A run that has ended or
// is gone ends the loop.
func (a *app) renewLoop(ctx context.Context, cl *client, id string, lease time.Duration, logf func(string, ...any)) {
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
		_, err := cl.call(ctx, http.MethodPost, "/runs/"+id+"/renew", nil, nil, nil)
		switch {
		case err == nil:
			failing = false
		case ctx.Err() != nil:
			return
		default:
			if ae := asAPIError(err); ae != nil && (ae.Status == http.StatusNotFound || ae.Status == http.StatusUnprocessableEntity) {
				return // the run has ended
			}
			if !failing {
				logf("the lease of the run could not be renewed: %v", err)
			}
			failing = true
		}
	}
}

// progress reports what changed in a run between two looks.
type progress struct {
	logf   func(string, ...any)
	status model.RunStatus
	steps  map[string]string
	checks map[int]string
	warns  int
}

func newProgress(logf func(string, ...any)) *progress {
	return &progress{logf: logf, steps: map[string]string{}, checks: map[int]string{}}
}

func (p *progress) update(r *model.Run) {
	if r.Steps != nil {
		for _, s := range *r.Steps {
			if st := string(s.Status); st != p.steps[s.Id] {
				p.steps[s.Id] = st
				if st == "pending" {
					continue
				}
				line := fmt.Sprintf("step %s %s", s.Id, st)
				if s.OffsetMs != nil {
					line += fmt.Sprintf(" at +%.1fs", float64(*s.OffsetMs)/1000)
				}
				if s.Error != nil && *s.Error != "" {
					line += ": " + *s.Error
				}
				p.logf("%s", line)
			}
		}
	}
	if r.Checks != nil {
		for _, c := range *r.Checks {
			if st := string(c.Status); st != p.checks[c.Index] {
				p.checks[c.Index] = st
				if st == "pending" {
					continue
				}
				p.logf("check %s %s", c.Name, st)
			}
		}
	}
	if r.Warnings != nil && len(*r.Warnings) > p.warns {
		for _, w := range (*r.Warnings)[p.warns:] {
			p.logf("warning %s: %s", w.Code, w.Message)
		}
		p.warns = len(*r.Warnings)
	}
	// the status after what happened in it: the end of a run is the last line
	if r.Status != p.status {
		p.status = r.Status
		p.logf("run %s", r.Status)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// printResult says how a run ended: the status line, the checks, the warnings and the reason.
func (a *app) printResult(r *model.Run, interrupted string) {
	if a.asJSON {
		a.printJSON(r)
		return
	}
	w := a.env.Stdout
	dur := ""
	if r.StartedAt != nil && r.EndedAt != nil {
		dur = fmt.Sprintf(", %.1fs", r.EndedAt.Sub(*r.StartedAt).Seconds())
	}
	fmt.Fprintf(w, "run %s %s (scenario %s%s)\n", r.Id, strings.ToUpper(string(r.Status)), r.Scenario.Name, dur)
	if r.Reason != nil && *r.Reason != "" {
		fmt.Fprintf(w, "  reason: %s\n", *r.Reason)
	}
	if interrupted != "" {
		fmt.Fprintf(w, "  %s\n", interrupted)
	}
	if r.Steps != nil {
		for _, s := range *r.Steps {
			if s.Status == "error" || s.Status == "skipped" {
				fmt.Fprintf(w, "  step %s: %s%s\n", s.Id, s.Status, withColon(deref(s.Error)))
			}
		}
	}
	if r.Checks != nil {
		for _, c := range *r.Checks {
			fmt.Fprintf(w, "  check %s: %s%s\n", c.Name, c.Status, withColon(deref(c.Message)))
		}
	}
	if r.Warnings != nil {
		for _, wn := range *r.Warnings {
			fmt.Fprintf(w, "  warning %s: %s\n", wn.Code, wn.Message)
		}
	}
}

func withColon(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

// saveReports writes the JUnit file and the artifacts the options ask for.
func (a *app) saveReports(ctx context.Context, cl *client, r *model.Run, o awaitOptions) error {
	id := r.Id.String()
	var problems []string
	if o.junit != "" {
		if err := fetchTo(ctx, cl, "/runs/"+id+"/report.xml", nil, filepath.Dir(o.junit), filepath.Base(o.junit), false, false); err != nil {
			problems = append(problems, fmt.Sprintf("--junit %s: %v", o.junit, err))
		}
	}
	if o.artifacts != "" {
		if err := os.MkdirAll(o.artifacts, privateDirMode); err != nil {
			problems = append(problems, fmt.Sprintf("--artifacts: %v", err))
		} else {
			problems = append(problems, a.saveArtifacts(ctx, cl, r, o.artifacts)...)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// saveArtifacts downloads what the run lists as its artifacts into dir.
func (a *app) saveArtifacts(ctx context.Context, cl *client, r *model.Run, dir string) []string {
	id := r.Id.String()
	list := deref(r.Artifacts)
	if len(list) == 0 {
		base := "/api/v1/runs/" + id
		list = []model.Artifact{{Kind: model.ArtifactKindReportJson, Url: base + "/report.json"}, {Kind: model.ArtifactKindReportXml, Url: base + "/report.xml"},
			{Kind: model.ArtifactKindEvents, Url: base + "/events"}}
	}
	var problems []string
	for _, art := range list {
		u, err := url.Parse(art.Url)
		if err != nil || !strings.HasPrefix(u.Path, "/api/v1/") {
			problems = append(problems, fmt.Sprintf("artifact %s: the address %q is not a path of the API", art.Kind, art.Url))
			continue
		}
		name := ""
		switch art.Kind {
		case model.ArtifactKindReportJson:
			name = "report.json"
		case model.ArtifactKindReportXml:
			name = "report.xml"
		case model.ArtifactKindEvents:
			name = "events.jsonl"
		case model.ArtifactKindCapture:
			name = "capture-" + captureID(art)
		case model.ArtifactKindKeylog:
			name = "keylog-" + captureID(art) + ".txt"
		}
		err = fetchTo(ctx, cl, strings.TrimPrefix(u.Path, "/api/v1"), u.Query(), dir, name, art.Kind == model.ArtifactKindCapture, true)
		if ae := asAPIError(err); ae != nil && ae.Status == http.StatusForbidden && art.Kind != model.ArtifactKindReportJson && art.Kind != model.ArtifactKindReportXml {
			fmt.Fprintf(a.env.Stderr, "artifact %s is not saved: the token may not download it (%v)\n", art.Kind, err)
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("artifact %s: %v", art.Kind, err))
		}
	}
	return problems
}

func captureID(a model.Artifact) string {
	if a.Capture != nil {
		return a.Capture.String()
	}
	return "unknown"
}

// fetchTo streams a path of the API into dir/name through a temporary file, so a failed download leaves nothing half
// written and a capture of any size does not sit in memory. With addExt the extension of the file name the gateway
// suggests (.pcap or .pcapng of a capture) is added to the name. A private file (and the directory made for it) is
// readable by its owner alone.
func fetchTo(ctx context.Context, cl *client, apiPath string, query url.Values, dir, name string, addExt, private bool) error {
	res, suggested, err := cl.open(ctx, apiPath, query)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if addExt {
		ext := filepath.Ext(filepath.Base(suggested))
		if ext == "" {
			ext = ".pcap"
		}
		name += ext
	}
	mode, dirMode := os.FileMode(publicFileMode), os.FileMode(publicDirMode)
	if private {
		mode, dirMode = privateFileMode, privateDirMode
	}
	if dir != "" {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return err
		}
	}
	return writeFileAtomic(filepath.Join(dir, name), mode, func(w io.Writer) error {
		if _, err := io.Copy(w, res.Body); err != nil {
			return fmt.Errorf("GET %s: %w", apiPath, err)
		}
		return nil
	})
}

func (a *app) abortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abort <run>",
		Short: "Abort a queued or running run and remove its overlays",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var run model.Run
			if _, err := cl.call(cmd.Context(), http.MethodPost, "/runs/"+url.PathEscape(args[0])+"/abort", nil, nil, &run); err != nil {
				return apiFail(err)
			}
			if a.asJSON {
				a.printJSON(run)
				return nil
			}
			fmt.Fprintf(a.env.Stdout, "run %s %s\n", run.Id, run.Status)
			return nil
		},
	}
}

func (a *app) waitCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "wait", Short: "Wait for a device or a run"}
	var o awaitOptions
	runWait := &cobra.Command{
		Use:   "run <run>",
		Short: "Wait for a run to end and return its result as the exit code",
		Long: `Waits for a run that was started without --wait (or by someone else) and returns what 'run --wait' returns:
0 passed, 1 a check failed, 2 error or aborted. It does not renew a lease and does not abort the run on an
interrupt or --timeout; it only stops waiting (exit 2).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var run model.Run
			if _, err := cl.call(cmd.Context(), http.MethodGet, "/runs/"+url.PathEscape(args[0]), nil, nil, &run); err != nil {
				return apiFail(err)
			}
			o.owns = false
			return a.await(cmd.Context(), cl, &run, o)
		},
	}
	runWait.Flags().DurationVar(&o.timeout, "timeout", 0, "stop waiting after this time (exit 2); 0: wait for the end")
	runWait.Flags().StringVar(&o.junit, "junit", "", "write the JUnit report to this file")
	runWait.Flags().StringVar(&o.artifacts, "artifacts", "", "write the reports, the events and the captures of the run into this directory")
	cmd.AddCommand(a.waitDeviceCmd(), runWait)
	return cmd
}
