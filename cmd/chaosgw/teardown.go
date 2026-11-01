package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// dataDirs are the directories of the volumes (plan §3.6) `teardown --purge` empties, with what each holds.
var dataDirs = []struct{ path, what string }{
	{"/var/lib/chaosgw/state", "the revisions"},
	{"/var/lib/chaosgw/secrets", "the secrets (WireGuard keys, the admin credentials, the HTTPS key)"},
	{"/var/lib/chaosgw/api", "the audit log, the runs and the idempotency keys"},
	{"/var/lib/chaosgw/service", "the token of the service containers and the API's published certificate"},
	{"/var/lib/chaosgw/tlsca", "the two CAs of the TLS responder"},
	{"/var/lib/chaosgw/captures", "the packet captures"},
	{"/var/lib/chaosgw/exec", "the interfaces assigned to the executor"},
	{"/var/lib/kea", "the leases and the configuration Kea was started with"},
}

// runTeardown is `chaosgw teardown`: it removes everything the gateway created in the kernel, as the executor does when it
// stops (plan §3.8, D35, variant A), for a gateway that was killed or crashed, and with --purge also empties the volumes once
// the kernel is clean (a teardown that fails leaves them, so that it can be run again).
// It runs in the executor's container: `docker compose run --rm exec chaosgw teardown`. It refuses while an executor is
// serving, because two writers of the kernel's configuration would break the serialization (plan §3.1).
func runTeardown(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chaosgw teardown", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "/run/chaosgw/exec.sock", "path of the executor's Unix socket: a running executor is refused")
	state := fs.String("state", "/var/lib/chaosgw/exec/state.json", "file that keeps the interfaces assigned to the executor")
	birdDir := fs.String("bird-dir", "", "directory shared with the BIRD container; with it the protocols of the BIRD instance are withdrawn")
	serviceNS := fs.String("service-ns", compiler.ServiceNSDefault, "name of the service namespace under /run/netns (chaosgw api --service-ns)")
	purge := fs.Bool("purge", false, "also remove the content of the volumes: revisions, secrets, audit log, runs, captures (irreversible)")
	timeout := fs.Duration("timeout", time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		fmt.Fprintln(stderr, "usage: chaosgw teardown [--purge] [--socket <path>] [--state <file>] [--bird-dir <dir>] [--service-ns <name>]")
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "chaosgw teardown: must run as root: it removes interfaces, rules and tables")
		return 1
	}
	if c, err := net.DialTimeout("unix", *socket, time.Second); err == nil {
		_ = c.Close()
		fmt.Fprintf(stderr, "chaosgw teardown: an executor is serving on %s: stop the gateway first (docker compose stop), which removes everything as well\n", *socket)
		return 1
	}
	if *purge && volumesInUse(dataDirs, stderr) {
		return 1
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	opts := []executor.Option{executor.WithLogger(log), executor.WithStateFile(*state)}
	if *birdDir != "" {
		opts = append(opts, executor.WithBirdDir(*birdDir))
	}
	ex, err := executor.New(newExecRunner(), opts...)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw teardown: %v\n", err)
		return 1
	}
	defer ex.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	for _, name := range ex.RemoveCheckNamespaces(ctx) {
		fmt.Fprintf(stdout, "removed the namespace %s of an interrupted host check\n", name)
	}
	plan, err := apply.Teardown(ctx, apply.Local{E: ex}, "", *serviceNS)
	if plan != nil {
		for _, line := range plan.Summary {
			fmt.Fprintln(stdout, line)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw teardown: %v\n", err)
		if *purge {
			// the volumes hold what a second run needs (the interfaces assigned to the executor name the ports to give back), and
			// emptying them cannot be undone: they stay until the kernel is clean
			fmt.Fprintln(stderr, "chaosgw teardown: the volumes are not removed because the kernel is not clean: run it again with --purge once the cause is gone")
		}
		return 1
	}
	fmt.Fprintln(stdout, "Everything Chaos Gateway created in the kernel is removed.")
	if !*purge {
		return 0
	}
	return purgeVolumes(dataDirs, stdout, stderr)
}

// volumesInUse reports, and says on stderr, whether a process has the store in one of the directories open: the API container
// holds the lock of the revisions for as long as it runs, and it runs while the executor restarts, which is when the executor
// is not answering on the socket. Emptying the revisions, the secrets and the audit log under a running API would leave it with
// a store that is not there.
func volumesInUse(dirs []struct{ path, what string }, stderr io.Writer) bool {
	inUse := false
	for _, d := range dirs {
		used, err := store.InUse(d.path)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "chaosgw teardown: cannot tell whether %s is in use: %v\n", d.path, err)
			inUse = true
		case used:
			fmt.Fprintf(stderr, "chaosgw teardown: %s (%s) is in use: the api container has it open: stop the gateway first (docker compose stop), then purge\n", d.path, d.what)
			inUse = true
		}
	}
	return inUse
}

// purgeVolumes empties the data directories that are mounted here and says which are not. They are directories of the host
// (plan §3.6, bind mounts): `docker compose down -v` removes named volumes only and leaves them, so a directory that is not
// mounted writable here is named, to be removed on the host. It empties nothing while a store is open in one of them.
func purgeVolumes(dirs []struct{ path, what string }, stdout, stderr io.Writer) int {
	if volumesInUse(dirs, stderr) {
		return 1
	}
	code := 0
	var absent []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			absent = append(absent, d.path)
			continue
		case err != nil:
			fmt.Fprintf(stderr, "chaosgw teardown: %v\n", err)
			code = 1
			continue
		}
		if unix.Access(d.path, unix.W_OK) != nil {
			fmt.Fprintf(stderr, "chaosgw teardown: %s is mounted read-only here: %s stay (remove them with rm -rf on the host: docker compose down -v leaves the directories of the host alone)\n", d.path, d.what)
			code = 1
			continue
		}
		failed := false
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(d.path, e.Name())); err != nil {
				fmt.Fprintf(stderr, "chaosgw teardown: %v\n", err)
				failed, code = true, 1
			}
		}
		if !failed {
			fmt.Fprintf(stdout, "Removed %s (%s).\n", d.path, d.what)
		}
	}
	if len(absent) > 0 {
		fmt.Fprintf(stdout, "Not mounted in this container, so not removed: %s (remove them with rm -rf on the host: docker compose down -v leaves the directories of the host alone).\n", strings.Join(absent, ", "))
	}
	return code
}
