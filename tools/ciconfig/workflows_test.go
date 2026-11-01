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

// runScript runs the script of a step with bash in a directory that holds the files, and returns its output and exit status. The stubs are
// scripts that are put on the PATH in front of the real programs.
func runScript(t *testing.T, script string, env []string, stubs, files map[string]string) (string, int) {
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
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(body, "#!") {
			mode = 0o755 // a script
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
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

// stepIndex returns the position of the first step of the job for which match is true.
func stepIndex(t *testing.T, j job, what string, match func(step) bool) int {
	t.Helper()
	for i, s := range j.Steps {
		if match(s) {
			return i
		}
	}
	t.Fatalf("the job has no step %s", what)
	return -1
}

// A tag is published only for a commit that passed CI: the step asks for a successful run of ci.yml on the commit, before the first build, and
// the job may read the runs of the repository and nothing more than it has to write.
func TestTheReleaseWorkflowRequiresASuccessfulCIRunOfTheTaggedCommit(t *testing.T) {
	j := parseWorkflow(t, "image.yml").Jobs["image"]
	if want := (map[string]string{"contents": "write", "packages": "write", "actions": "read"}); len(j.Permissions) != len(want) || j.Permissions["actions"] != "read" {
		t.Errorf("the permissions of the job are %v, want %v", j.Permissions, want)
	}
	check := stepIndex(t, j, "that checks CI", func(s step) bool { return s.Name == "Check that CI passed on the commit" })
	tag := stepIndex(t, j, "that checks the tag", func(s step) bool { return strings.Contains(s.Run, "deploy/release-check.sh \"$GITHUB_REF_NAME\"") })
	build := stepIndex(t, j, "that builds", func(s step) bool { return strings.HasPrefix(s.Uses, "docker/build-push-action@") })
	if !(tag < check && check < build) {
		t.Errorf("the check of CI is step %d, the check of the tag %d and the first build %d: it belongs between them", check, tag, build)
	}
	s := j.Steps[check]
	if s.If != "startsWith(github.ref, 'refs/tags/')" {
		t.Errorf("the check of CI runs for %q, a manual run has no tag to check", s.If)
	}
	if !strings.Contains(s.Run, `gh run list --workflow ci.yml --commit "$GITHUB_SHA" --status success`) {
		t.Errorf("the check does not ask for a successful run of ci.yml on the commit: %s", s.Run)
	}
	if !strings.Contains(read(t, ".github", "workflows", "image.yml"), "was cancelled") {
		t.Error("image.yml does not say what to do when the CI run of the commit was cancelled")
	}
	// the check itself, against a gh that finds none or one
	gh := `echo "$*" >> "$RUNNER_TEMP/gh.log"; echo "$COUNT"`
	env := []string{"GITHUB_SHA=abc123", "GITHUB_REPOSITORY=o/r"}
	if out, code := runScript(t, s.Run+"\ncat \"$RUNNER_TEMP/gh.log\"", append(env, "COUNT=1"), map[string]string{"gh": gh}, nil); code != 0 || !strings.Contains(out, "--commit abc123") {
		t.Errorf("a commit with a successful run: exit %d\n%s", code, out)
	}
	for _, count := range []string{"0", ""} {
		if out, code := runScript(t, s.Run, append(env, "COUNT="+count), map[string]string{"gh": gh}, nil); code != 1 || !strings.Contains(out, "no successful run") {
			t.Errorf("a commit without a successful run (%q): exit %d\n%s", count, code, out)
		}
	}
}

// :latest and the Latest release of GitHub are for the highest release version only: a step asks deploy/release-check.sh and the tags and the
// release take its answer.
func TestTheNewestReleaseDecidesWhoGetsLatest(t *testing.T) {
	j := parseWorkflow(t, "image.yml").Jobs["image"]
	newest := stepIndex(t, j, "that asks for the newest release", func(s step) bool { return s.ID == "newest" })
	meta := stepIndex(t, j, "that makes the metadata", func(s step) bool { return s.ID == "meta" })
	if newest >= meta {
		t.Errorf("the newest release is asked in step %d, after the tags are made in step %d", newest, meta)
	}
	s := j.Steps[newest]
	if s.If != "startsWith(github.ref, 'refs/tags/')" || !strings.Contains(s.Run, `deploy/release-check.sh --is-newest "$GITHUB_REF_NAME"`) {
		t.Errorf("the step does not ask release-check.sh about the pushed tag: %q %s", s.If, s.Run)
	}
	for answer, want := range map[string]string{"0": "latest=true", "1": "latest=false"} {
		out, code := runScript(t, s.Run+"\ncat \"$GITHUB_OUTPUT\"", []string{"GITHUB_OUTPUT=out", "GITHUB_REF_NAME=v1.0.1"}, nil,
			map[string]string{"deploy/release-check.sh": "#!/bin/sh\nexit " + answer + "\n"})
		if code != 0 || strings.TrimSpace(out) != want {
			t.Errorf("release-check.sh answers %s: exit %d, output %q, want %q", answer, code, out, want)
		}
	}
}

// A release is made once and corrected on a re-run: `gh release create` is not called when the release exists, so the flags of a
// pre-release and of the Latest release are set with `gh release edit`; the files are uploaded in both cases.
func TestARerunSetsThePreReleaseAndLatestFlagsOfAnExistingRelease(t *testing.T) {
	j := parseWorkflow(t, "image.yml").Jobs["image"]
	s := j.Steps[stepIndex(t, j, "that makes the release", func(s step) bool { return strings.Contains(s.Run, "gh release") })]
	files := map[string]string{}
	for _, f := range []string{"compose.yaml", "host-setup.sh", "install.sh", "update.sh", "uninstall.sh", "env.example"} {
		files["deploy/"+f] = f + "\n"
	}
	gh := `echo "$*" >> "$RUNNER_TEMP/gh.log"
if [ "$1 $2" = "release view" ]; then exit "$VIEW"; fi`
	for _, c := range []struct {
		name, tag, newest, view string
		want, not               string
	}{
		{"new release", "v1.2.0", "true", "1", "release create v1.2.0 --title v1.2.0 --generate-notes --prerelease=false --latest=true", "release edit"},
		{"new hotfix of an older line", "v1.0.1", "false", "1", "release create v1.0.1 --title v1.0.1 --generate-notes --prerelease=false --latest=false", "release edit"},
		{"new candidate", "v1.2.0-rc.1", "false", "1", "release create v1.2.0-rc.1 --title v1.2.0-rc.1 --generate-notes --prerelease=true --latest=false", "release edit"},
		{"re-run of a candidate", "v1.2.0-rc.1", "false", "0", "release edit v1.2.0-rc.1 --prerelease=true --latest=false", "release create"},
		{"re-run of the newest release", "v1.2.0", "true", "0", "release edit v1.2.0 --prerelease=false --latest=true", "release create"},
		{"re-run of a hotfix of an older line", "v1.0.1", "false", "0", "release edit v1.0.1 --prerelease=false --latest=false", "release create"},
	} {
		out, code := runScript(t, s.Run+"\ncat \"$RUNNER_TEMP/gh.log\"", []string{"GITHUB_REF_NAME=" + c.tag, "NEWEST=" + c.newest, "VIEW=" + c.view}, map[string]string{"gh": gh}, files)
		if code != 0 || !strings.Contains(out, c.want+"\n") || strings.Contains(out, c.not) || !strings.Contains(out, "release upload "+c.tag+" compose.yaml") {
			t.Errorf("%s: exit %d, want %q and no %q\n%s", c.name, code, c.want, c.not, out)
		}
	}
}

// makeVars reads the value of a simple assignment of the Makefile as a list of words.
func makeVars(t *testing.T, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + name + ` :?= (.*)$`).FindStringSubmatch(read(t, "Makefile"))
	if m == nil {
		t.Fatalf("the Makefile does not assign %s", name)
	}
	return strings.Fields(m[1])
}

// FuzzCaptureOpenFiles reaches the creation of files only with CAP_NET_RAW: the nightly job runs that target as root and no other, and
// keeps the corpus from night to night (also after a run that failed), keyed by the fuzz tests so that a changed one starts anew.
func TestTheNightlyFuzzJobRunsTheFileTargetAsRootAndKeepsItsCorpus(t *testing.T) {
	targets, root := makeVars(t, "FUZZ_TARGETS"), makeVars(t, "FUZZ_ROOT_TARGETS")
	if len(root) != 1 || root[0] != "./internal/executor:FuzzCaptureOpenFiles" || targets[len(targets)-1] != root[0] {
		t.Errorf("FUZZ_ROOT_TARGETS is %v, want the file target, which is the last of %v", root, targets)
	}

	// the Makefile starts the targets of FUZZ_ROOT_TARGETS behind FUZZ_ROOT_CMD and the others as they are; without the command all alike
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{"go": `echo "RUN $*"`, "asroot": `echo ROOT; exec "$@"`}
	for _, c := range []struct {
		cmd  string
		root []string
	}{{`FUZZ_ROOT_CMD="$PWD/bin/asroot"`, []string{"FuzzCaptureOpenFiles"}}, {"", nil}} {
		out, code := runScript(t, `make -s -C "$REPO" fuzz GO="$PWD/bin/go" FUZZTIME=1s `+c.cmd, []string{"REPO=" + repo}, stubs, nil)
		if code != 0 {
			t.Fatalf("make fuzz: exit %d\n%s", code, out)
		}
		var ran, asRoot []string
		rooted := false
		for _, l := range strings.Split(out, "\n") {
			if l == "ROOT" {
				rooted = true
			} else if m := regexp.MustCompile(`^RUN test .* -fuzz \^(\w+)\$ `).FindStringSubmatch(l); m != nil {
				ran = append(ran, m[1])
				if rooted {
					asRoot = append(asRoot, m[1])
				}
				rooted = false
			}
		}
		if len(ran) != len(targets) || strings.Join(asRoot, " ") != strings.Join(c.root, " ") {
			t.Errorf("make fuzz %s ran %v, of them as root %v, want %d targets and %v as root\n%s", c.cmd, ran, asRoot, len(targets), c.root, out)
		}
	}

	var fuzz job
	for id, j := range parseWorkflow(t, "nightly.yml").Jobs {
		if id == "fuzz" {
			fuzz = j
		}
	}
	run := stepIndex(t, fuzz, "that runs make fuzz", func(s step) bool { return strings.Contains(s.Run, "make fuzz") })
	if !strings.Contains(fuzz.Steps[run].Run, `FUZZ_ROOT_CMD="sudo -E env PATH=$PATH HOME=$HOME"`) || !strings.Contains(fuzz.Steps[run].Run, "FUZZTIME=45m") {
		t.Errorf("the fuzz job does not run the file target as root for 45 minutes per target: %s", fuzz.Steps[run].Run)
	}
	restore := stepIndex(t, fuzz, "that restores the corpus", func(s step) bool { return strings.HasPrefix(s.Uses, "actions/cache/restore@") })
	save := stepIndex(t, fuzz, "that saves the corpus", func(s step) bool { return strings.HasPrefix(s.Uses, "actions/cache/save@") })
	if !(restore < run && run < save) {
		t.Errorf("the corpus is restored in step %d, the fuzz runs in step %d and the corpus is saved in step %d", restore, run, save)
	}
	r, s := fuzz.Steps[restore].With, fuzz.Steps[save].With
	for _, w := range []map[string]any{r, s} {
		if w["path"] != "~/.cache/go-build/fuzz" {
			t.Errorf("the corpus cache holds %v, Go keeps the corpus in GOCACHE/fuzz", w["path"])
		}
	}
	key, ok := s["key"].(string)
	prefix, _ := r["restore-keys"].(string)
	if !ok || key != r["key"] || !strings.Contains(key, "${{ github.run_id }}") || prefix == "" || !strings.HasPrefix(key, prefix) || !strings.Contains(prefix, "hashFiles(") {
		t.Errorf("the keys of the corpus cache are %q (saved) and %q (restored, prefix %q): a key per run and a prefix of the fuzz tests to restore from", key, r["key"], prefix)
	}
	if fuzz.Steps[save].If != "always()" {
		t.Errorf("the corpus is saved if %q: a night with a crasher would lose what it found", fuzz.Steps[save].If)
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
