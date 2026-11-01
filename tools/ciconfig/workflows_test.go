package ciconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
