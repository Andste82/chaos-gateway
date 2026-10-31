package docscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBlocksReadTheInfoStringTheTextAndTheLine(t *testing.T) {
	p := write(t, t.TempDir(), "a.md", "# A\n\n```sh run exit=2\necho hi\n```\n\ntext\n\n- item\n\n  ```yaml schema=Scenario\n  name: x\n  steps: []\n  ```\n\n````text\n```\nnested\n```\n````\n")
	bs, err := Blocks(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 3 {
		t.Fatalf("%d blocks: %+v", len(bs), bs)
	}
	if b := bs[0]; b.Lang != "sh" || !b.Has("run") || b.Line != 3 || b.Text != "echo hi\n" {
		t.Errorf("%+v", b)
	} else if v, _ := b.Value("exit"); v != "2" {
		t.Errorf("exit=%q", v)
	}
	// a block in a list item is shown without the indentation of its fence
	if b := bs[1]; b.Lang != "yaml" || b.Text != "name: x\nsteps: []\n" {
		t.Errorf("%+v", b)
	} else if v, _ := b.Value("schema"); v != "Scenario" {
		t.Errorf("schema=%q", v)
	}
	// a longer fence holds a shorter one
	if b := bs[2]; b.Lang != "text" || b.Text != "```\nnested\n```\n" {
		t.Errorf("%q", b.Text)
	}
}

func TestAnUnclosedBlockIsAnError(t *testing.T) {
	p := write(t, t.TempDir(), "a.md", "```sh run\necho hi\n")
	if _, err := Blocks(p); err == nil || !strings.Contains(err.Error(), "never closed") {
		t.Fatalf("%v", err)
	}
}

func TestLintDemandsThatAnExampleSaysWhatItIs(t *testing.T) {
	bs := func(info, text string) []Block {
		p := write(t, t.TempDir(), "a.md", "```"+info+"\n"+text+"\n```\n")
		b, err := Blocks(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, c := range []struct {
		name, info string
		problems   int
	}{
		{"a script that says nothing", "sh", 1},
		{"a script that runs", "sh run", 0},
		{"a script with an exit code", "sh run exit=1", 0},
		{"an exit code without run", "sh host exit=1", 1},
		{"two kinds", "sh run host", 1},
		{"a word nobody knows", "sh run quickly", 1},
		{"yaml that is checked", "yaml schema=Scenario", 0},
		{"a schema without a name", "yaml schema", 1},
		{"a schema on a script", "sh schema=Scenario", 1},
		{"a command for the host", "sh host", 0},
		{"a fragment", "yaml fragment", 0},
		{"plain text needs nothing", "text", 0},
		{"a diagram needs nothing", "", 0},
		{"a python block is not run", "python run", 0},
		{"yaml cannot run", "yaml run", 1},
	} {
		if got := Lint(bs(c.info, "x")); len(got) != c.problems {
			t.Errorf("%s: %d problems, want %d: %v", c.name, len(got), c.problems, got)
		}
	}
	// an output block follows the run block it belongs to
	p := write(t, t.TempDir(), "a.md", "```text output\nx\n```\n")
	b, _ := Blocks(p)
	if got := Lint(b); len(got) != 1 {
		t.Errorf("an output block without a run block before it: %v", got)
	}
	p = write(t, t.TempDir(), "a.md", "```sh run\nx\n```\n\n```text output\nx\n```\n")
	b, _ = Blocks(p)
	if got := Lint(b); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestMatchFindsEachLineOfTheOutputBlockInTheOutput(t *testing.T) {
	got := "ID    KIND   OWNER   EXPIRES\n01a1  fault  token:ci  2026-10-29T19:25:17Z\ndone  \n"
	for _, c := range []struct {
		want    string
		missing int
	}{
		{"done", 0},
		{"ID … KIND … OWNER … EXPIRES", 0},
		{"01a1 … fault … token:ci …", 0},
		{"ID … KIND … OWNER … EXPIRES\n\ndone\n", 0},
		{"donee", 1},                // not a line of the output
		{"one", 1},                  // a line matches as a whole, not as a part
		{"fault", 1},                // likewise
		{"… fault … token:ci …", 0}, // ellipsis at both ends
		{"token:ci … nowhere", 1},   // the parts must be in the line
		{"done\nnot there\nnor this", 2},
	} {
		if m := Match(c.want, got); len(m) != c.missing {
			t.Errorf("%q: missing %v, want %d", c.want, m, c.missing)
		}
	}
}

func TestSlugIsTheAnchorGitHubMakes(t *testing.T) {
	for in, want := range map[string]string{
		"Safe mode":                                        "safe-mode",
		"The `kea` container (M28)":                        "the-kea-container-m28",
		"3. Start the containers":                          "3-start-the-containers",
		"A CI job that runs a test under a fault":          "a-ci-job-that-runs-a-test-under-a-fault",
		"Forgotten password, lost token, lost setup token": "forgotten-password-lost-token-lost-setup-token",
		"Troubleshooting: DHCP does not answer":            "troubleshooting-dhcp-does-not-answer",
		"Why is my device refused?":                        "why-is-my-device-refused",
	} {
		if got := Slug(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The text of a release is shown away from the repository, so the release notes link with addresses; they are checked as
// the files they name.
func TestLinksIntoTheRepositoryOnTheSiteAreCheckedAsFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "docs/other.md", "# Other\n\n## Two words\n")
	p := write(t, dir, "docs/notes/n.md", strings.Join([]string{
		"# N", "",
		"[ok](" + RepoBlob + "v1.0.0/docs/other.md#two-words) [gone](" + RepoBlob + "v1.0.0/docs/missing.md) [anchor](" + RepoBlob + "v1.0.0/docs/other.md#nothing)",
		"[elsewhere](https://example.com/Andste82/chaos-gateway/blob/v1/docs/missing.md)",
	}, "\n")+"\n")
	got, err := Links(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "docs/missing.md\" leads nowhere") || !strings.Contains(got[1], "#nothing\" points at a heading") {
		t.Fatalf("problems: %v", got)
	}
	targets, err := LinkTargets(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 4 {
		t.Errorf("targets: %v", targets)
	}
}

func TestLinksFindWhatLeadsNowhere(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "other.md", "# Other\n\n## Two words\n\n## Two words\n")
	write(t, dir, "sub/third.md", "# Third\n")
	p := write(t, dir, "a.md", strings.Join([]string{
		"# A", "",
		"[ok](other.md) [ok](other.md#two-words) [ok](other.md#two-words-1) [ok](#a) [ok](sub/third.md#third) [ext](https://example.com/x.md#y) [mail](mailto:x@y.z)",
		"[gone](missing.md)",
		"[no anchor](other.md#nothing)",
		"[no local anchor](#nothing)",
		"`[code](missing.md)`",
		"```", "[in a fence](missing.md)", "```",
	}, "\n")+"\n")
	got, err := Links(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("%d problems: %v", len(got), got)
	}
	for i, want := range []string{"a.md:4: the link \"missing.md\" leads nowhere", "a.md:5: the link \"other.md#nothing\" points at a heading", "a.md:6: the link \"#nothing\" points at a heading"} {
		if !strings.HasSuffix(strings.Split(got[i], "\n")[0], "") || !strings.Contains(got[i], want) {
			t.Errorf("problem %d: %s, want %s", i, got[i], want)
		}
	}
}
