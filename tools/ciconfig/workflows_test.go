package ciconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// step is one step of a job, as far as the tests of the workflows look at it.
type step struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

type job struct {
	Name        string            `yaml:"name"`
	Timeout     int               `yaml:"timeout-minutes"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []step            `yaml:"steps"`
}

type workflow struct {
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress any    `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]job `yaml:"jobs"`
}

var workflowFiles = []string{"ci.yml", "nightly.yml", "weekly.yml", "image.yml"}

func parseWorkflow(t *testing.T, file string) workflow {
	t.Helper()
	var w workflow
	if err := yaml.Unmarshal([]byte(read(t, ".github", "workflows", file)), &w); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return w
}

// stepRunning returns the step of the job whose script contains the text.
func stepRunning(t *testing.T, j job, text string) step {
	t.Helper()
	for _, s := range j.Steps {
		if strings.Contains(s.Run, text) {
			return s
		}
	}
	t.Fatalf("the job has no step that runs %q", text)
	return step{}
}

// runScript runs the script of a step with bash and returns its output and exit status. The stubs are scripts that are put on the PATH
// in front of the real programs.
func runScript(t *testing.T, script string, env []string, stubs map[string]string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// GitHub runs a step with `bash -e {0}`
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + dir, "RUNNER_TEMP=" + dir}, env...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// Every job has a timeout of its own (a hung job would run for the 6 hours of GitHub's default), and the limits inside a job (the VM of
// testvm, the test binary, `go test -timeout`) are below it, so that the tool that knows what hung ends the run and not the job's timeout.
func TestEveryJobHasATimeoutAboveTheLimitsInsideIt(t *testing.T) {
	limit := regexp.MustCompile(`(-vm-timeout|-test-timeout|-timeout) (\d+h(?:\d+m)?|\d+m)\b`)
	for _, file := range workflowFiles {
		for id, j := range parseWorkflow(t, file).Jobs {
			if j.Timeout <= 0 {
				t.Errorf("%s: the job %s has no timeout-minutes", file, id)
				continue
			}
			if j.Timeout > 360 {
				t.Errorf("%s: the job %s has %d minutes, GitHub's own limit is 360", file, id, j.Timeout)
			}
			for _, s := range j.Steps {
				for _, m := range limit.FindAllStringSubmatch(s.Run, -1) {
					d, err := time.ParseDuration(m[2])
					if err != nil {
						t.Fatal(err)
					}
					if d >= time.Duration(j.Timeout)*time.Minute {
						t.Errorf("%s: the job %s has %d minutes, not above its own %s %s", file, id, j.Timeout, m[1], m[2])
					}
				}
			}
		}
	}
}

// The release workflow is the one that writes to the repository and the registry: its actions are pinned to a commit, the version they stand
// for is in the comment after the hash (the other workflows keep the version tags, they have no write permission besides contents: read).
func TestTheActionsOfTheReleaseWorkflowArePinnedToACommit(t *testing.T) {
	pinned := regexp.MustCompile(`^[\w.-]+/[\w.-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+$`)
	var uses int
	for _, l := range strings.Split(read(t, ".github", "workflows", "image.yml"), "\n") {
		_, after, ok := strings.Cut(strings.TrimSpace(l), "uses: ")
		if !ok || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		uses++
		if !pinned.MatchString(after) {
			t.Errorf("image.yml: %q is not `owner/action@<40 hex characters> # vX.Y.Z`", after)
		}
	}
	if uses < 6 {
		t.Errorf("found only %d `uses:` lines in image.yml", uses)
	}
}

// A run of CI on main is the verdict on that commit (image.yml asks for it before it publishes a tag): a later push must not cancel it, nor
// replace it while it waits. Only the runs of a pull request are cancelled by a newer push to the same pull request.
func TestTheCIRunsOfMainAreNeverCancelled(t *testing.T) {
	w := parseWorkflow(t, "ci.yml")
	if got := w.Concurrency.CancelInProgress; got != "${{ github.event_name == 'pull_request' }}" {
		t.Errorf("cancel-in-progress is %v: it cancels the runs of pushes to main", got)
	}
	// the group of a push is the commit, the group of a pull request its ref
	if g := w.Concurrency.Group; !strings.Contains(g, "github.event_name == 'pull_request' && github.ref || github.sha") {
		t.Errorf("the concurrency group %q lets a push to main wait in the group of the branch, where a later push replaces it", g)
	}
}
