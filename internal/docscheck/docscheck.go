// Package docscheck reads the user documentation (M30) the way a test needs it: the fenced code blocks with the words
// of their info string, the headings and the links.
//
// The documents under docs/ that a user follows (the user guide, the API guide, the cookbook, the troubleshooting and
// recovery instructions, the release notes) hold examples. An example that nobody runs goes wrong silently, so every
// block of code in them says what it is, in the words after the language of its fence:
//
//	```sh run              a script, run with bash against a gateway on the simulated kernel (internal/api, tag examples)
//	```sh run exit=2       the same, and the script is expected to end with this exit code
//	```yaml schema=Scenario a document, checked against this schema of api/openapi.yaml
//	```text output         what the preceding `run` block prints; each line must be a line of the real output ("…" stands for any text)
//	```sh host             a command for the host of the gateway (docker, systemctl): shown, not run in CI
//	```yaml fragment       a part of a file or a document that is not complete on its own
//
// Plain fences without a language (diagrams) are not examples and need no word. The lint is [Lint]; the runner is the
// test `TestTheDocumentationExamplesRunAgainstTheGateway` in internal/api.
package docscheck

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Documents are the documents that carry examples, relative to the repository root. Every block of code in them is
// checked by the tests of this package, and the blocks that run are run by internal/api (`make test-docs`).
var Documents = []string{
	"docs/user-guide.md",
	"docs/api-guide.md",
	"docs/cookbook.md",
	"docs/troubleshooting.md",
	"docs/recovery.md",
	"docs/install.md",
	"docs/release-notes/v1.0.0.md",
}

// Block is one fenced block of code.
type Block struct {
	File  string
	Line  int // of the opening fence
	Lang  string
	Words []string // the words of the info string after the language
	Text  string
}

// Has reports whether the info string has the word (or the word=value pair with this key).
func (b Block) Has(key string) bool {
	_, ok := b.Value(key)
	return ok
}

// Value returns the value of a key=value word, "" for a bare word.
func (b Block) Value(key string) (string, bool) {
	for _, w := range b.Words {
		k, v, _ := strings.Cut(w, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}

func (b Block) String() string { return fmt.Sprintf("%s:%d", b.File, b.Line) }

// Blocks reads the fenced blocks of a Markdown file. A fence is three backticks at the start of a line (indented by up
// to three spaces); it is closed by a line of at least as many backticks.
func Blocks(file string) ([]Block, error) {
	f, err := os.Open(file) //nolint:gosec // a path of the repository's documentation
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var (
		out    []Block
		cur    *Block
		indent int // of the opening fence: a block in a list item is indented, its text is shown without it
		fence  string
		text   strings.Builder
		n      int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		n++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " ")
		if cur == nil {
			if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "```") {
				continue
			}
			ticks := len(trimmed) - len(strings.TrimLeft(trimmed, "`"))
			fence = strings.Repeat("`", ticks)
			indent = len(line) - len(trimmed)
			info := strings.Fields(strings.TrimSpace(trimmed[ticks:]))
			cur = &Block{File: file, Line: n}
			if len(info) > 0 {
				cur.Lang, cur.Words = info[0], info[1:]
			}
			text.Reset()
			continue
		}
		if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, "`") == "" {
			cur.Text = text.String()
			out = append(out, *cur)
			cur = nil
			continue
		}
		lead := len(line) - len(strings.TrimLeft(line, " "))
		text.WriteString(line[min(lead, indent):])
		text.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		return nil, fmt.Errorf("%s:%d: the fenced block is never closed", file, cur.Line)
	}
	return out, nil
}

// checked are the languages whose blocks are examples and so must say what they are.
var checked = map[string]bool{"sh": true, "bash": true, "yaml": true, "json": true, "http": true, "ini": true, "toml": true}

// Lint returns the problems of the blocks of a file: an example without a word that says what it is, or with words that
// do not fit it.
func Lint(blocks []Block) []string {
	var out []string
	for i, b := range blocks {
		if b.Has("output") && (i == 0 || !blocks[i-1].Has("run")) {
			out = append(out, fmt.Sprintf("%s: an output block follows the run block it shows the output of", b))
		}
		if !checked[b.Lang] {
			continue
		}
		kinds := 0
		for _, k := range []string{"run", "schema", "host", "output", "fragment"} {
			if b.Has(k) {
				kinds++
			}
		}
		switch {
		case kinds == 0:
			out = append(out, fmt.Sprintf("%s: a %s block needs one of run, schema=<Schema>, host, output or fragment after the language", b, b.Lang))
		case kinds > 1:
			out = append(out, fmt.Sprintf("%s: a block is one of run, schema, host, output and fragment, not several", b))
		}
		for _, w := range b.Words {
			k, _, _ := strings.Cut(w, "=")
			switch k {
			case "run", "schema", "host", "output", "fragment", "exit":
			default:
				out = append(out, fmt.Sprintf("%s: unknown word %q in the info string", b, w))
			}
		}
		if b.Has("run") && b.Lang != "sh" && b.Lang != "bash" {
			out = append(out, fmt.Sprintf("%s: only sh and bash blocks are run", b))
		}
		if b.Has("exit") && !b.Has("run") {
			out = append(out, fmt.Sprintf("%s: exit= belongs to a run block", b))
		}
		if v, ok := b.Value("schema"); ok && (v == "" || (b.Lang != "yaml" && b.Lang != "json")) {
			out = append(out, fmt.Sprintf("%s: schema=<Schema> belongs to a yaml or json block", b))
		}
	}
	return out
}

// Match reports whether every line of want (a line of an output block) is found among the lines of got. A line with
// "…" matches a line of got that has its parts in this order, at its beginning and end where the line does not start or
// end with "…". It returns the lines that are not found.
func Match(want, got string) []string {
	gotLines := strings.Split(got, "\n")
	for i := range gotLines {
		gotLines[i] = strings.TrimRight(gotLines[i], " \t\r")
	}
	var missing []string
	for _, w := range strings.Split(want, "\n") {
		w = strings.TrimRight(w, " \t\r")
		if strings.TrimSpace(w) == "" {
			continue
		}
		parts := strings.Split(w, "…")
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		re := regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
		found := false
		for _, g := range gotLines {
			if re.MatchString(g) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, w)
		}
	}
	return missing
}

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	linkRE    = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	slugDrop  = regexp.MustCompile("[^a-z0-9 _-]")
)

// Slug is the anchor GitHub makes of a heading: lower case, punctuation removed, spaces as hyphens.
func Slug(heading string) string {
	h := strings.ToLower(strings.TrimSpace(heading))
	h = strings.NewReplacer("`", "", "*", "").Replace(h)
	h = slugDrop.ReplaceAllString(h, "")
	return strings.ReplaceAll(h, " ", "-")
}

// Anchors returns the anchors of the headings of a Markdown file (a second heading with the same text gets -1, -2, ...).
func Anchors(file string) (map[string]bool, error) {
	raw, err := os.ReadFile(file) //nolint:gosec // a path of the repository's documentation
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		s := Slug(m[2])
		if n := seen[s]; n > 0 {
			out[fmt.Sprintf("%s-%d", s, n)] = true
		} else {
			out[s] = true
		}
		seen[s]++
	}
	return out, nil
}

// Links returns the problems of the relative links of a Markdown file: a target that is not there, an anchor that is
// no heading of the file it points into. Links to other sites are not followed.
func Links(file string) ([]string, error) {
	raw, err := os.ReadFile(file) //nolint:gosec // a path of the repository's documentation
	if err != nil {
		return nil, err
	}
	var out []string
	inFence := false
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// inline code is not a link
		line = regexp.MustCompile("`[^`]*`").ReplaceAllString(line, "")
		for _, m := range linkRE.FindAllStringSubmatch(line, -1) {
			target := m[1]
			// a link to a file of this repository on the site is checked as the file (the release notes use them: the
			// text of a release is shown on a page where a relative link leads nowhere)
			repoFile, isRepo := repoLink(target)
			if !isRepo && (strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:")) {
				continue
			}
			p, anchor, _ := strings.Cut(target, "#")
			dest := file
			if isRepo {
				p, anchor, _ = strings.Cut(repoFile, "#")
				root, err := repositoryRoot(file)
				if err != nil {
					return nil, err
				}
				dest = filepath.Join(root, filepath.FromSlash(p))
				if _, err := os.Stat(dest); err != nil {
					out = append(out, fmt.Sprintf("%s:%d: the link %q leads nowhere", file, i+1, target))
					continue
				}
			} else if p != "" {
				dest = filepath.Join(filepath.Dir(file), filepath.FromSlash(p))
				if _, err := os.Stat(dest); err != nil {
					out = append(out, fmt.Sprintf("%s:%d: the link %q leads nowhere", file, i+1, target))
					continue
				}
			}
			if anchor == "" || path.Ext(dest) != ".md" {
				continue
			}
			anchors, err := Anchors(dest)
			if err != nil {
				return nil, err
			}
			if !anchors[anchor] {
				out = append(out, fmt.Sprintf("%s:%d: the link %q points at a heading that does not exist", file, i+1, target))
			}
		}
	}
	return out, nil
}

// RepoBlob is the start of the address of a file of this repository on the site; the next part is the tag or branch.
const RepoBlob = "https://" + "github.com/Andste82/chaos-gateway/blob/"

// repoLink returns the path (with the anchor) in the repository of an address under [RepoBlob].
func repoLink(target string) (string, bool) {
	rest, ok := strings.CutPrefix(target, RepoBlob)
	if !ok {
		return "", false
	}
	_, file, ok := strings.Cut(rest, "/")
	return file, ok
}

// LinkTargets returns the targets of the links of a Markdown file, outside of code.
func LinkTargets(file string) ([]string, error) {
	raw, err := os.ReadFile(file) //nolint:gosec // a path of the repository's documentation
	if err != nil {
		return nil, err
	}
	var out []string
	inFence := false
	inline := regexp.MustCompile("`[^`]*`")
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range linkRE.FindAllStringSubmatch(inline.ReplaceAllString(line, ""), -1) {
			out = append(out, m[1])
		}
	}
	return out, nil
}

// repositoryRoot is the directory with the go.mod above a file.
func repositoryRoot(file string) (string, error) {
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s: no go.mod above the file", file)
		}
		dir = parent
	}
}
