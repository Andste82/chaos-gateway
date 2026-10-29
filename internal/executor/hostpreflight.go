package executor

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// preflightCache is how long the checks of the host stay valid: GET /system/preflight and GET /setup (which the wizard polls with
// "Check again") ask for them, and a run makes a namespace and lists the tables of nftables.
const preflightCache = 10 * time.Second

// HostPreflightState is the answer of ReadHostPreflight: the checks the executor made of the host (plan §3.4, M28) and when.
// The API adds them to its own report.
type HostPreflightState struct {
	At     time.Time             `json:"at"`
	Checks []preflight.HostCheck `json:"checks"`
}

// WithPreflight names the directories of the host that the container mounts (plan §3.6) and that the preflight checks for their
// owner: the API container's user has to be able to write them.
func WithPreflight(dirs []preflight.DataDir) Option { return func(e *Executor) { e.pfDirs = dirs } }

// WithOwnRoot replaces the file system and the kernel release the preflight reads (the executor's own /proc, /sys and
// /lib/modules); the tests of the checks have no real machine.
func WithOwnRoot(fsys fs.FS, release string) Option {
	return func(e *Executor) { e.pfFS, e.pfRelease = fsys, release }
}

// HostPreflight runs the checks of the host, or returns the last run when it is younger than ten seconds. It loads the
// kernel modules that exist but are not loaded, and makes and removes a throw-away namespace; nothing else changes. The
// checks need the real tools: an executor on a simulated kernel (a Runner other than ExecRunner) has none to report.
func (e *Executor) HostPreflight(ctx context.Context) HostPreflightState {
	e.pfMu.Lock()
	defer e.pfMu.Unlock()
	if !e.pfAt.IsZero() && e.clock.Now().Sub(e.pfAt) < preflightCache {
		return e.pfState
	}
	st := HostPreflightState{At: e.clock.Now().UTC(), Checks: []preflight.HostCheck{}}
	if tools, ok := e.run.(interface{ Path(Tool) (string, bool) }); ok {
		fsys, release := e.pfFS, e.pfRelease
		if fsys == nil {
			env, err := preflight.HostEnv()
			if err != nil {
				st.Checks = append(st.Checks, preflight.HostCheck{ID: "host.kernel", Status: "warn", Message: "the kernel release cannot be read: " + err.Error()})
			}
			fsys, release = env.FS, env.Release
			if fsys == nil {
				fsys = os.DirFS("/")
			}
		}
		st.Checks = append(st.Checks, preflight.HostChecks(ctx, preflight.HostProbe{
			FS:       fsys,
			Release:  release,
			HasTool:  func(name string) bool { _, ok := tools.Path(Tool(name)); return ok },
			Run:      e.hostRun,
			HostFS:   e.hostFS,
			DataDirs: e.pfDirs,
		})...)
	}
	e.pfAt, e.pfState = e.clock.Now(), st
	return st
}

// RemoveCheckNamespaces removes the throw-away namespaces of the host preflight that an executor killed in the middle of a check
// left in /run/netns: the stop cleanup and `chaosgw teardown` call it, as the preflight does before its next check. It returns the
// names it removed; an executor on a simulated kernel has no tools and removes none.
func (e *Executor) RemoveCheckNamespaces(ctx context.Context) []string {
	e.pfMu.Lock()
	defer e.pfMu.Unlock()
	if _, ok := e.run.(interface{ Path(Tool) (string, bool) }); !ok {
		return nil
	}
	return preflight.RemoveCheckNamespaces(ctx, e.hostRun)
}

// hostRun runs a tool of the executor for the checks of the host and returns what it printed.
func (e *Executor) hostRun(ctx context.Context, name string, args ...string) (string, error) {
	r, err := e.run.Run(ctx, Command{Tool: Tool(name), Args: args})
	out := strings.TrimSpace(r.Stdout + "\n" + r.Stderr)
	switch {
	case err != nil:
		return out, err
	case r.Exit != 0:
		return out, fmt.Errorf("%s exited with status %d", name, r.Exit)
	}
	return out, nil
}
