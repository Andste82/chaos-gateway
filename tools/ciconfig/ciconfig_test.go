// Package ciconfig holds tests of the CI configuration itself: what the workflows check must follow what the
// image and the level 1 test lists say (P4-AUDIT-49).
package ciconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, rel ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// apiDirs are the directories the image gives to the API's user (65532) for its named volumes.
func apiDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	for _, l := range strings.Split(read(t, "deploy", "Dockerfile"), "\n") {
		if !strings.Contains(l, "chown 65532:65532 ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			// the service namespace holder's directory is a tmpfs, not a named volume
			if (strings.HasPrefix(f, "/var/lib/chaosgw/") || strings.HasPrefix(f, "/run/chaosgw-kea-events")) && f != "/run/chaosgw-svcns" {
				dirs = append(dirs, f)
			}
		}
	}
	if len(dirs) < 6 {
		t.Fatalf("found only %v in the Dockerfile's chown line", dirs)
	}
	return dirs
}

// The fresh-volume check of CI (issue #57) must try every directory the image gives to the API's user. The capture
// volume was missing: a root-owned one would have failed every capture on a fresh host unnoticed.
func TestTheFreshVolumeCheckNamesEveryVolumeTheAPIWrites(t *testing.T) {
	ci := read(t, ".github", "workflows", "ci.yml")
	m := regexp.MustCompile(`(?m)^\s*for dir in (.*); do$`).FindStringSubmatch(ci)
	if m == nil {
		t.Fatal("ci.yml has no `for dir in ...; do` loop of the fresh-volume check")
	}
	loop := strings.Fields(m[1])
	have := map[string]bool{}
	for _, d := range loop {
		have[d] = true
	}
	for _, d := range apiDirs(t) {
		if !have[d] {
			t.Errorf("%s is writable for the API user in the image, but not in the fresh-volume loop of ci.yml (%v)", d, loop)
		}
	}
}

// The upgrade note of docs/install.md gives the same volumes to the API's user.
func TestTheUpgradeNoteChownsEveryVolumeTheAPIWrites(t *testing.T) {
	doc := read(t, "docs", "install.md")
	m := regexp.MustCompile(`(?m)^for v in (.*); do$`).FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("docs/install.md has no `for v in ...; do` loop")
	}
	have := map[string]bool{}
	for _, v := range strings.Fields(m[1]) {
		have[v] = true
	}
	for _, d := range apiDirs(t) {
		if strings.HasPrefix(d, "/var/lib/chaosgw/") && !have[filepath.Base(d)] {
			t.Errorf("the volume %s is missing in the upgrade note's loop (%s)", filepath.Base(d), m[1])
		}
	}
}

// Every job that runs the testbed tests names the level 1 lists with -expect-pass, in the CI run and in the nightly
// kernel matrix: a test that skips on one kernel must not stay green.
func TestEveryTestbedRunNamesTheLevel1Lists(t *testing.T) {
	var lists []string
	for _, l := range strings.Split(read(t, ".github", "level1-tests.env"), "\n") {
		if m := regexp.MustCompile(`^([A-Z0-9_]+)=.+`).FindStringSubmatch(l); m != nil {
			lists = append(lists, m[1])
		}
	}
	if len(lists) < 7 {
		t.Fatalf("found only %v in .github/level1-tests.env", lists)
	}
	for _, w := range []struct {
		file string
		runs int
	}{{"ci.yml", 2}, {"nightly.yml", 1}} {
		text := read(t, ".github", "workflows", w.file)
		runs := 0
		for _, l := range strings.Split(text, "\n") {
			if !strings.Contains(l, "make test-vm") && !strings.Contains(l, "make test-privileged") {
				continue
			}
			if strings.Contains(l, "InTheBrowser") {
				continue // the browser job names its own three tests
			}
			runs++
			_, after, ok := strings.Cut(l, "-expect-pass ")
			if !ok {
				t.Errorf("%s: a testbed run without -expect-pass: %s", w.file, strings.TrimSpace(l))
				continue
			}
			for _, name := range lists {
				if !strings.Contains(after, "$"+name) {
					t.Errorf("%s: a testbed run does not pass $%s to -expect-pass", w.file, name)
				}
			}
		}
		if runs != w.runs {
			t.Errorf("%s: %d testbed runs found, want %d", w.file, runs, w.runs)
		}
		if !strings.Contains(text, `>> "$GITHUB_ENV"`) || !strings.Contains(text, "level1-tests.env") {
			t.Errorf("%s does not load .github/level1-tests.env into the environment of its jobs", w.file)
		}
	}
}
