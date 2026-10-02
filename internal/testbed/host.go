package testbed

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func ctxBackground() context.Context { return context.Background() }

// runHost runs a command in the namespace the test runs in, with a clean environment.
func runHost(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = CleanEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
