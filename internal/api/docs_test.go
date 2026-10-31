//go:build examples

package api_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/docscheck"
)

// M30: the examples of the documentation run against a gateway on the simulated kernel with the real engine and the real
// clock, the way test-examples runs the example suites. Every fenced `sh run` block of the documents in
// docscheck.Documents is a bash script that a reader would paste into a shell: the environment is the one the documents
// describe (CHAOSGW_URL, CHAOSGW_TOKEN with the scope overlays, CHAOSGW_ADMIN_TOKEN with the scope full, CHAOSGW_ADMIN_PASSWORD, chaosctl, curl
// and jq on the PATH, the device esp32-42 in the network IoT, online). A script must end with the exit code its block
// names (0 unless `exit=N`), and the `output` block behind it must be found in what it printed. The blocks that are not
// run (`host`, `schema=`, `fragment`) are checked at level 0 by internal/docscheck. `make test-docs` runs this test.

const docsBlockTimeout = 3 * time.Minute

func TestTheDocumentationExamplesRunAgainstTheGateway(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docs := docscheck.Documents
	if only := os.Getenv("CHAOSGW_DOCS"); only != "" { // an author runs one document: CHAOSGW_DOCS=docs/api-guide.md
		docs = strings.Split(only, ",")
	}
	ran := 0
	for _, doc := range docs {
		blocks, err := docscheck.Blocks(filepath.Join(root, doc))
		if err != nil {
			t.Fatal(err)
		}
		hasRun := false
		for _, b := range blocks {
			hasRun = hasRun || b.Has("run")
		}
		if !hasRun {
			continue
		}
		t.Run(doc, func(t *testing.T) {
			g, env := exampleGateway(t)
			automation := envValue(env, "CHAOSGW_TOKEN")
			admin := g.mintToken("full")
			work := t.TempDir()
			bin := filepath.Join(work, ".bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(envValue(env, "CHAOSCTL"), filepath.Join(bin, "chaosctl")); err != nil {
				t.Fatal(err)
			}
			env = append(env, "CHAOSGW_ADMIN_TOKEN="+admin, "CHAOSGW_ADMIN_PASSWORD="+adminPassword, "PATH="+bin+":"+os.Getenv("PATH"), "HOME="+work)

			var last string // what the last run block printed, for the output block behind it
			for _, b := range blocks {
				switch {
				case b.Has("run"):
					want := 0
					if v, ok := b.Value("exit"); ok {
						n, err := strconv.Atoi(v)
						if err != nil {
							t.Fatalf("%s: exit=%q is not a number", b, v)
						}
						want = n
					}
					out, code := runDocBlock(t, work, env, b.Text)
					last = out
					ran++
					if code != want {
						t.Errorf("%s: the script ends with the exit code %d, the block says %d\n--- script\n%s--- output\n%s", b, code, want, b.Text, out)
					}
					// the overlays and runs a block leaves do not reach the next one (a revision, profile or scenario it makes stays, so a
					// block that needs configuration makes or reads its own)
					g.token = admin
					if r := g.do("POST", "/reset?owner=all", nil, nil, nil); r.Status != 200 && r.Status != 204 {
						t.Fatalf("resetting the gateway after %s: %d %s", b, r.Status, r.Body)
					}
					g.token = automation
				case b.Has("output"):
					if missing := docscheck.Match(b.Text, last); len(missing) > 0 {
						t.Errorf("%s: the output block shows lines that the script did not print:\n  %s\n--- the script printed\n%s", b, strings.Join(missing, "\n  "), last)
					}
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("no document has a run block: the examples are not tested")
	}
}

// runDocBlock runs a script with bash the way a reader would paste it (with -e and pipefail, which the documents say
// they assume) and returns what it printed on both streams and its exit code.
func runDocBlock(t *testing.T, dir string, env []string, script string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), docsBlockTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-eo", "pipefail", "-c", script)
	cmd.Dir, cmd.Env = dir, env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return out.String(), ee.ExitCode()
	}
	t.Fatalf("running a script: %v\n%s", err, out.String())
	return "", -1
}

func envValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], name+"="); ok {
			return v
		}
	}
	return ""
}
