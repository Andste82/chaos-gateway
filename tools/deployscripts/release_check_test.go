package deployscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const releaseCheckScript = "../../deploy/release-check.sh"

// checkout makes a git repository with a commit on main and returns its directory.
func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a")
	git("commit", "-q", "-m", "first")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func releaseCheck(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	script, err := filepath.Abs(releaseCheckScript)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	}
	t.Fatal(err)
	return "", -1
}

func TestAVersionTagOnMainMayBePublished(t *testing.T) {
	dir := checkout(t)
	if err := os.MkdirAll(filepath.Join(dir, "docs", "release-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "release-notes", "v1.0.0.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v1.0.0", "v0.9.12", "v10.20.30", "v1.0.0-rc.1", "v1.0.0-beta", "v2.0.0-rc.1.2"} {
		out, code := releaseCheck(t, dir, tag)
		if code != 0 || !strings.Contains(out, "is a version tag on main") {
			t.Errorf("%s: exit %d\n%s", tag, code, out)
		}
	}
	// a release with notes gets no warning
	if out, _ := releaseCheck(t, dir, "v1.0.0"); strings.Contains(out, "warning") {
		t.Errorf("a release that has its notes got a warning:\n%s", out)
	}
}

func TestATagThatIsNoVersionIsRefused(t *testing.T) {
	dir := checkout(t)
	for _, tag := range []string{"1.0.0", "v1", "v1.0", "v1.0.0.1", "v01.0.0", "v1.0.0+build", "vfoo", "v1.0.0-", "v1.0.0-rc..1", "latest", ""} {
		out, code := releaseCheck(t, dir, tag)
		if code != 1 || !strings.Contains(out, "is not a version tag") {
			t.Errorf("%q: exit %d, want 1\n%s", tag, code, out)
		}
	}
	if _, code := releaseCheck(t, dir); code != 2 {
		t.Errorf("no argument: exit %d, want 2", code)
	}
}

func TestATagOnACommitThatIsNotOnMainIsRefused(t *testing.T) {
	dir := checkout(t)
	gitIn(t, dir, "switch", "-q", "-c", "side")
	if err := os.WriteFile(filepath.Join(dir, "b"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "b")
	gitIn(t, dir, "commit", "-q", "-m", "only on a side branch")
	out, code := releaseCheck(t, dir, "v1.0.0")
	if code != 1 || !strings.Contains(out, "is not on main") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// once main has it, the tag may be published
	gitIn(t, dir, "switch", "-q", "main")
	gitIn(t, dir, "merge", "-q", "--ff-only", "side")
	if out, code := releaseCheck(t, dir, "v1.0.0"); code != 0 {
		t.Fatalf("after the merge: exit %d\n%s", code, out)
	}
}

func TestACheckoutWithoutMainIsRefusedWithTheWayOut(t *testing.T) {
	dir := checkout(t)
	gitIn(t, dir, "branch", "-m", "trunk")
	out, code := releaseCheck(t, dir, "v1.0.0")
	if code != 1 || !strings.Contains(out, "no branch main") || !strings.Contains(out, "fetch-depth 0") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestOriginMainCountsAsMainInAShallowCloneOfTheWorkflow(t *testing.T) {
	// the workflow checks out a tag: HEAD is detached and there is no local main, but origin/main is fetched
	origin := checkout(t)
	clone := t.TempDir()
	cmd := exec.Command("git", "clone", "-q", origin, clone)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	gitIn(t, clone, "checkout", "-q", "--detach", "HEAD")
	gitIn(t, clone, "branch", "-q", "-D", "main")
	if out, code := releaseCheck(t, clone, "v1.0.0-rc.1"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestAReleaseWithoutNotesGetsAWarningButMayBePublished(t *testing.T) {
	dir := checkout(t)
	out, code := releaseCheck(t, dir, "v1.0.0")
	if code != 0 || !strings.Contains(out, "warning: docs/release-notes/v1.0.0.md does not exist") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// a pre-release needs no notes
	if out, _ := releaseCheck(t, dir, "v1.0.0-rc.1"); strings.Contains(out, "warning") {
		t.Errorf("a pre-release got a warning:\n%s", out)
	}
}

// tagNew commits and tags, so that every tag is on main at a commit of its own.
func tagNew(t *testing.T, dir string, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "for "+tag)
		gitIn(t, dir, "tag", tag)
	}
}

// Only the highest release version gets :latest: a hotfix on an older line (v1.0.1 after v1.1.0) must not move it back.
func TestOnlyTheHighestReleaseVersionIsTheNewest(t *testing.T) {
	dir := checkout(t)
	tagNew(t, dir, "v1.0.0")
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.0.0"); code != 0 || !strings.Contains(out, "v1.0.0 is the newest release") {
		t.Fatalf("the only release: exit %d\n%s", code, out)
	}
	tagNew(t, dir, "v1.1.0", "v1.0.1")
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.0.1"); code != 1 || !strings.Contains(out, "v1.0.1 is not the newest release, v1.1.0 is") {
		t.Errorf("a hotfix on an older line: exit %d\n%s", code, out)
	}
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.1.0"); code != 0 {
		t.Errorf("the highest: exit %d\n%s", code, out)
	}
	// a tag that is not made yet is compared as well: the next release is the newest
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.1.1"); code != 0 {
		t.Errorf("the next release: exit %d\n%s", code, out)
	}
	// the versions are compared by their numbers, not as text
	tagNew(t, dir, "v1.9.0", "v1.10.0", "v0.99.0")
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.9.0"); code != 1 || !strings.Contains(out, "v1.10.0 is") {
		t.Errorf("v1.9.0 after v1.10.0: exit %d\n%s", code, out)
	}
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.10.0"); code != 0 {
		t.Errorf("v1.10.0: exit %d\n%s", code, out)
	}
}

func TestAPreReleaseIsNeverTheNewestAndDoesNotHoldBackARelease(t *testing.T) {
	dir := checkout(t)
	tagNew(t, dir, "v1.0.0", "v2.0.0-rc.1")
	if out, code := releaseCheck(t, dir, "--is-newest", "v2.0.0-rc.1"); code != 1 || !strings.Contains(out, "pre-release") {
		t.Errorf("a release candidate: exit %d\n%s", code, out)
	}
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.0.1"); code != 0 {
		t.Errorf("a release next to a candidate of a higher version: exit %d\n%s", code, out)
	}
}

func TestATagThatIsNotOnMainDoesNotHoldBackTheNewestRelease(t *testing.T) {
	dir := checkout(t)
	tagNew(t, dir, "v1.0.0")
	gitIn(t, dir, "switch", "-q", "-c", "side")
	tagNew(t, dir, "v9.0.0")
	gitIn(t, dir, "switch", "-q", "main")
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.0.1"); code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestTheNewestCheckRefusesWhatTheCheckRefuses(t *testing.T) {
	dir := checkout(t)
	if out, code := releaseCheck(t, dir, "--is-newest", "1.0.0"); code != 1 || !strings.Contains(out, "is not a version tag") {
		t.Errorf("no version: exit %d\n%s", code, out)
	}
	if _, code := releaseCheck(t, dir, "--is-newest"); code != 2 {
		t.Errorf("no tag: exit %d, want 2", code)
	}
	gitIn(t, dir, "branch", "-m", "trunk")
	if out, code := releaseCheck(t, dir, "--is-newest", "v1.0.0"); code != 1 || !strings.Contains(out, "no branch main") {
		t.Errorf("no main: exit %d\n%s", code, out)
	}
}
