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

// The interception proxy (M22) runs the same mitmproxy everywhere: in the image of the gateway, in the testbed image the level 1 tests run in and in the
// level 0 job that starts the real one for the tests of the responder. A version that differs in one of them would test something other than what ships.
func TestMitmproxyIsOneVersionInTheImageTheTestbedAndTheLevel0Job(t *testing.T) {
	want := map[string]string{}
	for file, re := range map[string]string{
		"deploy/Dockerfile":        `(?m)^ARG MITMPROXY_VERSION=(\S+)$`,
		".devcontainer/Dockerfile": `(?m)^ARG MITMPROXY_VERSION=(\S+)$`,
		".github/workflows/ci.yml": `pip" install -q mitmproxy==(\S+)`,
	} {
		m := regexp.MustCompile(re).FindStringSubmatch(read(t, strings.Split(file, "/")...))
		if m == nil {
			t.Errorf("%s names no mitmproxy version", file)
			continue
		}
		want[file] = m[1]
	}
	for file, v := range want {
		if v != want["deploy/Dockerfile"] {
			t.Errorf("%s has mitmproxy %s, the image has %s", file, v, want["deploy/Dockerfile"])
		}
	}
	// the level 0 job fails without mitmproxy instead of skipping the tests that need it
	if !strings.Contains(read(t, ".github", "workflows", "ci.yml"), "CHAOSGW_REQUIRE_MITMPROXY=1") {
		t.Error("the level 0 job does not require mitmproxy")
	}
}

// The tests that must run in the testbed jobs are all there: every test the list M22_LEVEL1 names exists in the Go sources, so a rename cannot leave
// a name in the list that no run can pass (the run would fail, which is the point; this says which name).
func TestEveryTestOfTheLevel1ListsExists(t *testing.T) {
	var sources []string
	root := filepath.Join("..", "..")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, "_test.go") && !strings.Contains(p, "node_modules") {
			if b, err := os.ReadFile(p); err == nil {
				sources = append(sources, string(b))
			}
		}
		return nil
	})
	all := strings.Join(sources, "\n")
	for _, l := range strings.Split(read(t, ".github", "level1-tests.env"), "\n") {
		m := regexp.MustCompile(`^([A-Z0-9_]+)=(.+)`).FindStringSubmatch(l)
		if m == nil {
			continue
		}
		for _, name := range strings.Split(m[2], ",") {
			if !strings.Contains(all, "func "+name+"(t *testing.T)") {
				t.Errorf("%s names %s, which is no test", m[1], name)
			}
		}
	}
}
