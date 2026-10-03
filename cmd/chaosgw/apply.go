package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// runApply is `chaosgw apply --file`: apply a configuration without the API (the bootstrap of
// tests and of the appliance harness). It validates the file, compiles it against the host and
// applies and verifies it through the executor. With --state-dir the configuration also becomes a
// revision of the store and the active one, as if it had been applied through the API.
func runApply(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chaosgw apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "configuration file (YAML or JSON)")
	socket := fs.String("socket", "/run/chaosgw/exec.sock", "path of the executor's Unix socket")
	namespace := fs.String("namespace", "", "network namespace to configure (default: the executor's own; for tests)")
	secretsDir := fs.String("secrets-dir", "", "directory with the secrets; WireGuard networks need it (their keys are generated there)")
	stateDir := fs.String("state-dir", "", "store the configuration as the active revision in this directory")
	dryRun := fs.Bool("dry-run", false, "show what would change and change nothing")
	execUID := fs.Int("executor-uid", 0, "uid the executor runs as (it must be root or this user)")
	timeout := fs.Duration("timeout", 2*time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *execUID < 0 {
		fmt.Fprintln(stderr, "chaosgw apply: --executor-uid must not be negative")
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: chaosgw apply --file <configuration> [--socket <path>] [--state-dir <dir>] [--dry-run]")
		return 2
	}
	if *dryRun && *stateDir != "" {
		fmt.Fprintln(stderr, "chaosgw apply: --dry-run and --state-dir exclude each other")
		return 2
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatAuto)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %s is not a valid configuration:\n", *file)
		var ve domain.ValidationErrors
		if errors.As(err, &ve) {
			for _, e := range ve {
				fmt.Fprintf(stderr, "  %s: %s (%s)\n", e.Path, e.Message, e.Code)
			}
		} else {
			fmt.Fprintf(stderr, "  %v\n", err)
		}
		return 1
	}
	// the compiler works on a configuration that names objects by UUID, as a stored revision does
	cfg, nerrs := domain.Normalize(cfg)
	if len(nerrs) > 0 {
		fmt.Fprintf(stderr, "chaosgw apply: %s: %s\n", *file, nerrs[0].Message)
		return 1
	}
	var sec *secrets.Store
	if *secretsDir != "" {
		dir := *secretsDir
		if *dryRun {
			// a dry run must not create keys: it works on a copy of the secrets
			tmp, err := copySecrets(dir)
			if err != nil {
				fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
				return 1
			}
			defer func() { _ = os.RemoveAll(tmp) }()
			dir = tmp
		}
		var err error
		if sec, err = secrets.Open(dir); err != nil {
			fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
			return 1
		}
	}
	// generate the keys of WireGuard networks and clients; the configuration then carries the public keys
	if cfg, err = wireguard.Provision(cfg, sec); err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		if sec == nil {
			fmt.Fprintln(stderr, "  (give --secrets-dir for configurations with WireGuard networks)")
		}
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	c, err := executor.Dial(ctx, *socket, executor.DialOptions{Auth: executor.AllowUIDs(uint32(*execUID))})
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: cannot reach the executor at %s: %v\n", *socket, err)
		return 1
	}
	defer func() { _ = c.Close() }()

	if *stateDir != "" {
		return applyThroughStore(ctx, c, *namespace, *stateDir, cfg, sec, stdout, stderr)
	}
	return applyDirect(ctx, c, *namespace, cfg, sec, *dryRun, stdout, stderr)
}

func applyDirect(ctx context.Context, ex apply.Exec, ns string, cfg *model.Configuration, sec *secrets.Store, dryRun bool, stdout, stderr io.Writer) int {
	host, err := apply.ReadHost(ctx, ex, ns)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	in := compiler.Input{Config: cfg, Host: host, Generation: compiler.Generation{Seq: 1}}
	if sec != nil {
		in.Keys, _ = wireguard.InterfaceKeys(cfg, sec)
	}
	tg := compiler.Compile(in)
	printProblems(stderr, tg)
	if tg.HasErrors() {
		fmt.Fprintln(stderr, "chaosgw apply: nothing was changed")
		return 1
	}
	if dryRun {
		state, err := apply.ReadState(ctx, ex, ns, apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
			return 1
		}
		plan, err := apply.BuildPlan(tg, state, ns)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "would apply:")
		for _, s := range plan.Summary {
			fmt.Fprintln(stdout, "  "+s)
		}
		d := apply.Diff(tg, state)
		printDiff(stdout, "nftables", d.Nftables)
		printDiff(stdout, "network", d.Routes)
		return 0
	}
	res, err := apply.Apply(ctx, ex, ns, tg)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "applied and verified: %d networks, uplink %s via %s (%s)\n", len(tg.Bridges), tg.Uplink.Name, tg.Uplink.Gateway, tg.Hash)
	for _, s := range res.Plan.Summary {
		fmt.Fprintln(stdout, "  "+s)
	}
	return 0
}

func applyThroughStore(ctx context.Context, ex apply.Exec, ns, dir string, cfg *model.Configuration, sec *secrets.Store, stdout, stderr io.Writer) int {
	st, err := store.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()
	e, err := engine.New(engine.Config{Store: st, Exec: ex, Namespace: ns, Secrets: sec})
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	if err := e.Start(ctx); err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	defer e.Close()
	rev, err := st.Create(cfg, store.CreateOptions{IfMatch: st.ActiveID(), Now: time.Now(), By: model.Actor{Id: "system", Type: "system"}, Message: "chaosgw apply --file"})
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		return 1
	}
	a, err := e.Apply(ctx, rev.Id, engine.ApplyOptions{SkipConfirm: true})
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw apply: %v\n", err)
		_ = st.Discard(rev.Id)
		return 1
	}
	s := e.Snapshot()
	printProblemList(stderr, s.Problems)
	fmt.Fprintf(stdout, "revision %d is active (generation %d), applied and verified\n", a.Revision, a.Generation)
	return 0
}

func printProblems(w io.Writer, t *compiler.Target) { printProblemList(w, t.Problems) }

func printProblemList(w io.Writer, ps []compiler.Problem) {
	for _, p := range ps {
		fmt.Fprintf(w, "%s: %s\n", p.Severity, p.Message)
	}
}

func printDiff(w io.Writer, what, d string) {
	if d == "" {
		return
	}
	fmt.Fprintf(w, "%s:\n", what)
	for _, l := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		fmt.Fprintln(w, "  "+l)
	}
}

// copySecrets copies a secrets directory to a temporary one (mode 0700).
func copySecrets(dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "chaosgw-dryrun-")
	if err != nil {
		return "", err
	}
	src, err := secrets.Open(dir)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	dst, err := secrets.Open(tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	ids, err := src.IDs()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	for _, id := range ids {
		k, err := src.WireGuard(id)
		if err == nil {
			err = dst.PutWireGuard(id, k)
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
	}
	return tmp, nil
}
