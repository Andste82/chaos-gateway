//go:build examples

package api_test

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M18: the example test suites of clients/examples (pytest with the generated Python client, Jest with the generated
// TypeScript client, and both with chaosctl) run against a gateway on the simulated kernel with the real engine and
// the real clock, the way a CI job of a user would run them against a lab gateway. `make test-examples` prepares the
// generated clients and the tools and runs this test; it is not part of `go test ./...` because it needs Python,
// Node and their packages.

// exampleGateway is a gateway with the device esp32-42 online and a token with the scope overlays, the one an
// example suite uses.
func exampleGateway(t *testing.T) (g *gw, env []string) {
	t.Helper()
	g = newGW(t)
	g.finishSetup()
	id := g.mustPatch(map[string]any{
		"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}},
		"devices":  map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}}},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	token := g.mintToken("overlays")

	bin := filepath.Join(t.TempDir(), "chaosctl")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/chaosctl")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build chaosctl: %v\n%s", err, out)
	}
	return g, append(os.Environ(), "CHAOSGW_URL="+g.ts.URL, "CHAOSGW_TOKEN="+token, "CHAOSGW_DEVICE=esp32-42", "CHAOSCTL="+bin,
		"CHAOSGW_INSECURE=", "CHAOSGW_CA_CERT=")
}

func needEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set: `make test-examples` prepares the tools and runs this test", name)
	}
	return v
}

func TestTheExampleSuitesPassAgainstTheGateway(t *testing.T) {
	python := needEnv(t, "EXAMPLES_PYTHON")
	jestDir := needEnv(t, "EXAMPLES_JEST_DIR")
	pytestDir := needEnv(t, "EXAMPLES_PYTEST_DIR")
	g, env := exampleGateway(t)

	t.Run("pytest", func(t *testing.T) {
		junit := filepath.Join(t.TempDir(), "pytest.xml")
		cmd := exec.Command(python, "-m", "pytest", "-p", "no:cacheprovider", "--junitxml", junit)
		cmd.Dir, cmd.Env = pytestDir, env
		out, err := cmd.CombinedOutput()
		t.Logf("%s", out)
		if err != nil {
			t.Fatalf("pytest: %v", err)
		}
		requireNoSkips(t, junit, 8)
	})

	t.Run("jest", func(t *testing.T) {
		junit := filepath.Join(t.TempDir(), "jest.xml")
		cmd := exec.Command(filepath.Join(jestDir, "node_modules", ".bin", "jest"), "--ci", "--reporters=default", "--reporters=jest-junit")
		cmd.Dir = jestDir
		cmd.Env = append(env, "JEST_JUNIT_OUTPUT_FILE="+junit)
		out, err := cmd.CombinedOutput()
		t.Logf("%s", out)
		if err != nil {
			t.Fatalf("jest: %v", err)
		}
		requireNoSkips(t, junit, 8)
	})

	// the suites leave nothing on the gateway
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left on the gateway", n)
	}
	eventuallyTrue(t, "the runs to have ended", func() bool {
		for _, r := range g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any) {
			switch r.(map[string]any)["status"] {
			case "queued", "running":
				return false
			}
		}
		return true
	})
}

// requireNoSkips reads the JUnit file of a suite: at least min tests, none failed, none skipped (a skipped test is
// one that did not check what it is there for).
func requireNoSkips(t *testing.T, path string, min int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the suite wrote no JUnit file: %v", err)
	}
	var doc struct {
		Suites []struct {
			Tests    int `xml:"tests,attr"`
			Failures int `xml:"failures,attr"`
			Errors   int `xml:"errors,attr"`
			Skipped  int `xml:"skipped,attr"`
		} `xml:"testsuite"`
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Skipped  int `xml:"skipped,attr"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	tests, bad, skipped := doc.Tests, doc.Failures+doc.Errors, doc.Skipped
	for _, s := range doc.Suites {
		tests, bad, skipped = tests+s.Tests, bad+s.Failures+s.Errors, skipped+s.Skipped
	}
	if tests < min || bad != 0 || skipped != 0 {
		t.Errorf("%d tests, %d failed, %d skipped; want at least %d, none failed, none skipped\n%s", tests, bad, skipped, min, strings.TrimSpace(string(raw)))
	}
}
