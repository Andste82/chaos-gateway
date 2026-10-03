package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// birdParsesFile runs `bird -p` on a generated configuration; without bird the check is skipped.
func birdParsesFile(t *testing.T, text string) error {
	t.Helper()
	bin, err := exec.LookPath("bird")
	if err != nil {
		t.Skip("bird is not installed")
	}
	f := filepath.Join(t.TempDir(), "x.conf")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-p", "-c", f).CombinedOutput(); err != nil {
		return &birdErr{strings.TrimSpace(string(out))}
	}
	return nil
}

type birdErr struct{ s string }

func (e *birdErr) Error() string { return e.s }
