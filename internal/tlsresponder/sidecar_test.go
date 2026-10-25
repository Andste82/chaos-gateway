package tlsresponder

import (
	"os"
	"os/exec"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The unit tests of the sidecar's Python code (the rules of an interception case and what the addon makes of mitmproxy's failure text) run
// with the Go tests, with the interpreter that has mitmproxy (the tests of the addon's helpers need it to import the addon) or else the
// system's python3 (the tests of the rules need nothing).
func TestThePythonTestsOfTheSidecarPass(t *testing.T) {
	py, ok := testbed.MitmPython()
	if !ok {
		var err error
		if py, err = exec.LookPath("python3"); err != nil {
			if os.Getenv(testbed.RequireMitmEnv) == "1" {
				t.Fatal("python3 is not installed, and this run requires the sidecar's tests")
			}
			t.Skip("python3 is not installed")
		}
	}
	cmd := exec.Command(py, "-I", "-m", "unittest", "discover", "-s", "tests", "-t", ".")
	cmd.Dir = testbed.SidecarDir()
	cmd.Env = append(testbed.CleanEnv(os.Environ()), "PYTHONPATH="+testbed.SidecarDir(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
