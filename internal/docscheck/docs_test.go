package docscheck_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/docscheck"
	"github.com/Andste82/chaos-gateway/internal/domain"
)

// M30: the user documentation is tested at level 0 here (the blocks that run are run by internal/api, `make test-docs`):
// every example says what it is, every document and scenario in them is valid against the spec, the links lead somewhere,
// every preflight check the code can report is explained, the banner of safe mode links to the recovery instructions and the
// release notes name what is not validated on ARM64.

const root = "../.."

func path(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(path(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEveryExampleOfTheDocumentsSaysWhatItIs(t *testing.T) {
	runs := 0
	for _, doc := range docscheck.Documents {
		blocks, err := docscheck.Blocks(path(doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) == 0 && !strings.Contains(doc, "release-notes") {
			t.Errorf("%s has no example at all", doc)
		}
		for _, p := range docscheck.Lint(blocks) {
			t.Error(p)
		}
		for _, b := range blocks {
			if b.Has("run") {
				runs++
			}
		}
	}
	// the runner has something to run: a document that lost its run blocks would pass the lint and test nothing
	if runs < 30 {
		t.Errorf("the documents hold %d run blocks, expected at least 30", runs)
	}
}

func TestEveryDocumentAndScenarioIsValidAgainstTheSpec(t *testing.T) {
	v, err := domain.Schemas()
	if err != nil {
		t.Fatal(err)
	}
	// the configuration the documents write about: the device esp32-42 in the network IoT (the one of the example gateway)
	cfg, err := domain.DecodeConfiguration([]byte(read(t, "api/examples/configuration.yaml")), domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	checked, scenarios := 0, 0
	for _, doc := range docscheck.Documents {
		blocks, err := docscheck.Blocks(path(doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range blocks {
			name, ok := b.Value("schema")
			if !ok {
				continue
			}
			checked++
			if !v.Has(name) {
				t.Errorf("%s: the spec has no schema %q", b, name)
				continue
			}
			format := domain.FormatYAML
			if b.Lang == "json" {
				format = domain.FormatJSON
			}
			parsed, err := domain.ParseDocument([]byte(b.Text), format)
			if err != nil {
				t.Errorf("%s: %v", b, err)
				continue
			}
			for _, e := range v.Validate(name, parsed) {
				t.Errorf("%s: not a valid %s: %s %s: %s", b, name, e.Path, e.Code, e.Message)
			}
			// a scenario also meets the rules the schema cannot express (a window that starts at a step that does not exist,
			// a duplicate step id, an unknown device), as the API applies them when a run is made
			if name == "Scenario" {
				scenarios++
				sc, err := domain.DecodeScenario([]byte(b.Text), format)
				if err != nil {
					t.Errorf("%s: %v", b, err)
					continue
				}
				for _, e := range domain.ValidateScenario(cfg, sc) {
					t.Errorf("%s: not a valid scenario for the example gateway: %s %s: %s", b, e.Path, e.Code, e.Message)
				}
			}
		}
	}
	if scenarios < 6 {
		t.Errorf("only %d scenarios are checked against a configuration, expected at least 6", scenarios)
	}
	if checked < 8 {
		t.Errorf("only %d documents are checked against the spec, expected at least 8", checked)
	}
}

func TestTheLinksOfTheDocumentsLeadSomewhere(t *testing.T) {
	files := append([]string{"README.md", "docs/install.md"}, docscheck.Documents...)
	for _, f := range files {
		problems, err := docscheck.Links(path(f))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// The ids of the preflight checks that the code can report: the literals of the places that make them, and the three
// families whose id has a part that depends on the host.
func TestEveryPreflightCheckIsExplainedInTheTroubleshooting(t *testing.T) {
	doc := read(t, "docs/troubleshooting.md")
	literal := regexp.MustCompile(`(?:ID: *|id :?= |add\()"((?:kernel|module|host|interfaces?|preflight)(?:\.[a-z_]+)*)"`)
	var ids []string
	for _, src := range []string{"internal/preflight/host.go", "internal/preflight/dockerpools.go", "internal/executor/hostpreflight.go", "cmd/chaosgw/api.go", "internal/api/system.go"} {
		for _, m := range literal.FindAllStringSubmatch(read(t, src), -1) {
			ids = append(ids, m[1])
		}
	}
	// the ids that must be among them: a pattern that stopped matching a place would else shrink the check unseen
	for _, id := range []string{"kernel.version", "host.tools", "host.docker_pools", "host.kernel", "host.netns", "interfaces.os_config", "host", "preflight"} {
		if !slices.Contains(ids, id) {
			t.Errorf("the check %q is not found in the sources (%v): the patterns no longer match the code", id, ids)
		}
	}
	for _, id := range ids {
		if !strings.Contains(doc, "`"+id+"`") {
			t.Errorf("the check %q is reported by the code and not explained in docs/troubleshooting.md", id)
		}
	}
	for src, family := range map[string]string{
		"internal/preflight/host.go": "host.data.<dir>",
		"cmd/chaosgw/api.go":         "module.<name>",
		"internal/api/system.go":     "interface.<name>.os_config",
	} {
		if !strings.Contains(doc, "`"+family+"`") {
			t.Errorf("the family %q of %s is not explained in docs/troubleshooting.md", family, src)
		}
	}
}

// The components of GET /system/health are the enum of the spec; a component the troubleshooting does not name is one the
// reader cannot find.
func TestEveryHealthComponentIsNamedInTheTroubleshooting(t *testing.T) {
	m := regexp.MustCompile(`enum: \[(api, executor[^\]]*)\]`).FindStringSubmatch(read(t, "api/openapi.yaml"))
	if m == nil {
		t.Fatal("api/openapi.yaml has no enum of the health components")
	}
	doc := read(t, "docs/troubleshooting.md")
	components := strings.Split(m[1], ", ")
	if len(components) < 8 {
		t.Fatalf("the spec lists %v", components)
	}
	for _, c := range components {
		if !strings.Contains(doc, "`"+c+"`") {
			t.Errorf("the health component %q of the spec is not named in docs/troubleshooting.md", c)
		}
	}
}

func TestTheBannerOfSafeModeLinksToTheRecoveryInstructions(t *testing.T) {
	shell := read(t, "web/src/components/layout/AppShell.vue")
	m := regexp.MustCompile(`href="(https://github\.com/Andste82/chaos-gateway/blob/main/([^"#]+)(?:#([^"]+))?)"[^>]*\s*>\s*How to recover`).FindStringSubmatch(shell)
	if m == nil {
		t.Fatal("the safe-mode banner has no link 'How to recover' into the repository's documents")
	}
	file, anchor := m[2], m[3]
	if file != "docs/recovery.md" {
		t.Errorf("the banner links to %s, want docs/recovery.md (P3-M12-05)", file)
	}
	anchors, err := docscheck.Anchors(path(file))
	if err != nil {
		t.Fatal(err)
	}
	if !anchors[anchor] {
		t.Errorf("the banner links to #%s, which is no heading of %s", anchor, file)
	}
	// the banner promises what the document delivers
	if doc := read(t, "docs/recovery.md"); !strings.Contains(doc, "safe-mode banner links here") {
		t.Error("docs/recovery.md does not say that the safe-mode banner links to it")
	}
}

// P7-M27-01 and -02 are still open for the maintainer. The documents describe what M27 does today, which is option (a) of both:
// a management tunnel is down in safe mode (the way in is the management interface) and a fallback to a revision from before a
// key rotation ends in safe mode (the way out is a new revision). A decision for option (b) of either item must change the
// documents and this test. Deleting a sentence must fail a test.
func TestTheDocumentsStateWhatSafeModeAndAKeyRotationDo(t *testing.T) {
	tunnel := regexp.MustCompile(`(?s)role\s+` + "`management`" + `.{0,80}\sdown\s.{0,120}the\s+way\s+in\s+is\s+the\s+management\s+interface`)
	rotation := regexp.MustCompile(`(?s)before\s+(?:the|a)\s+(?:WireGuard\s+key\s+)?rotation.{0,160}safe\s+mode.{0,160}new\s+revision`)
	for doc, wants := range map[string][]*regexp.Regexp{
		"docs/install.md":              {tunnel},
		"docs/recovery.md":             {tunnel, rotation},
		"docs/release-notes/v1.0.0.md": {tunnel, rotation},
	} {
		text := read(t, doc)
		for _, re := range wants {
			if !re.MatchString(text) {
				t.Errorf("%s does not state what the pattern %q says (P7-M27-01, P7-M27-02)", doc, re)
			}
		}
	}
}

// The scope overlays is not tied to one device (P6-M24-01): the guides say so and call the token a lab-wide credential, and the
// user guide does not say that faults cover forwarded connections (only access rules do; faults are M35b).
func TestTheGuidesStateWhatTheScopeOverlaysReachesAndWhatForwardsCover(t *testing.T) {
	for _, doc := range []string{"docs/api-guide.md", "docs/user-guide.md"} {
		if text := read(t, doc); !regexp.MustCompile(`(?s)not\s+tied\s+to\s+one\s+device.{0,400}lab-wide\s+credential`).MatchString(text) {
			t.Errorf("%s does not say that the scope overlays is not tied to one device and is a lab-wide credential", doc)
		}
	}
	if text := read(t, "docs/user-guide.md"); regexp.MustCompile(`rule\s+or\s+fault\s+whose\s+source`).MatchString(text) {
		t.Error("docs/user-guide.md says that a fault whose source is the device covers forwarded connections, but faults do not apply to them yet")
	}
}

// The overview of the CI jobs in development.md names the job that runs the examples of the documents.
func TestTheCIOverviewNamesTheJobThatRunsTheExamples(t *testing.T) {
	dev := read(t, "docs/development.md")
	_, overview, ok := strings.Cut(dev, "\n## CI\n")
	if !ok {
		t.Fatal("docs/development.md has no section CI")
	}
	overview, _, _ = strings.Cut(overview, "\n## ")
	if !strings.Contains(overview, "the job `docs`") || !strings.Contains(overview, "`make test-docs`") {
		t.Error("the CI section of docs/development.md does not name the job `docs` that runs `make test-docs`")
	}
}

func TestTheReleaseNotesNameWhatIsUnvalidatedOnARM64(t *testing.T) {
	notes := read(t, "docs/release-notes/v1.0.0.md")
	for _, want := range []string{
		"## Not validated on ARM64 until the hardware lab has run",
		"Raspberry Pi",
		"H1",
		"interception proxy is not validated on ARM64",
		"performance targets",
		"through the TLS proxy",
		"## What was not reviewed from outside",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the release notes do not contain %q", want)
		}
	}
}

// image.yml publishes the file as the text of the release, which is shown on a page of the site where a link relative to
// the file leads nowhere: the links are addresses of the files at the tag that is released.
func TestTheLinksOfTheReleaseNotesAreAddressesAtTheirTag(t *testing.T) {
	targets, err := docscheck.LinkTargets(path("docs/release-notes/v1.0.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) < 6 {
		t.Fatalf("the release notes have %d links, expected at least 6", len(targets))
	}
	for _, target := range targets {
		if strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, docscheck.RepoBlob) {
			continue
		}
		if !strings.HasPrefix(target, docscheck.RepoBlob+"v1.0.0/docs/") {
			t.Errorf("the link %q in the release notes is not an address of a document at the tag v1.0.0", target)
		}
	}
}

// The host commands of the documents are not run in CI; at least the commands, flags and services they name must exist.
func TestTheHostCommandsOfTheDocumentsNameThingsThatExist(t *testing.T) {
	update := read(t, "deploy/update.sh")
	teardown := read(t, "deploy/compose.teardown.yaml")
	main := read(t, "cmd/chaosgw/main.go")
	admin := read(t, "cmd/chaosgw/admin.go")
	updateOptions := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(--[a-z-]+)\)`).FindAllStringSubmatch(update, -1) {
		updateOptions[m[1]] = true
	}
	if len(updateOptions) < 6 {
		t.Fatalf("found the options %v in deploy/update.sh: the pattern no longer matches the script", updateOptions)
	}
	sub := regexp.MustCompile(`chaosgw (admin reset-password|teardown|schema-check)`)
	option := regexp.MustCompile(`\s(--[a-z-]+)`)
	seen, updates := 0, 0
	for _, doc := range docscheck.Documents {
		blocks, err := docscheck.Blocks(path(doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range blocks {
			if !b.Has("host") {
				continue
			}
			seen++
			for _, m := range sub.FindAllStringSubmatch(b.Text, -1) {
				name, rest, _ := strings.Cut(m[1], " ")
				if !strings.Contains(main, `{"`+name+`",`) {
					t.Errorf("%s: cmd/chaosgw has no command %q", b, name)
				}
				if rest != "" && !strings.Contains(admin, `args[0] != "`+rest+`"`) {
					t.Errorf("%s: `chaosgw %s` has no subcommand %q", b, name, rest)
				}
			}
			// every option of every command line that runs update.sh, not only the first
			for _, line := range strings.Split(b.Text, "\n") {
				if !strings.Contains(line, "update.sh") {
					continue
				}
				updates++
				code, _, _ := strings.Cut(line, "#")
				for _, m := range option.FindAllStringSubmatch(code, -1) {
					if !updateOptions[m[1]] {
						t.Errorf("%s: deploy/update.sh has no option %s", b, m[1])
					}
				}
			}
			if strings.Contains(b.Text, "--profile teardown run --rm teardown") {
				if !strings.Contains(teardown, `profiles: ["teardown"]`) || !regexp.MustCompile(`(?m)^  teardown:$`).MatchString(teardown) {
					t.Errorf("%s: deploy/compose.teardown.yaml has no service teardown under the profile teardown", b)
				}
			}
			if strings.Contains(b.Text, "reset-password") {
				for _, f := range []string{"--secrets-dir", "--data-dir"} {
					if !strings.Contains(b.Text, f) {
						t.Errorf("%s: reset-password needs %s", b, f)
					}
				}
			}
		}
	}
	if seen == 0 || updates == 0 {
		t.Errorf("the documents have %d host blocks and %d lines that run update.sh", seen, updates)
	}
}

// The documents name the options of install.sh and uninstall.sh on command lines and in prose; an option the script does not have is a
// command that fails for the person who typed it (the scripts are tested in tools/deployscripts).
func TestTheOptionsTheDocumentsGiveInstallAndUninstallExist(t *testing.T) {
	options := func(script string) map[string]bool {
		out := map[string]bool{"--help": true}
		for _, m := range regexp.MustCompile(`(?m)^\s*(--[a-z-]+)(?: \| -h)?\)`).FindAllStringSubmatch(read(t, script), -1) {
			out[m[1]] = true
		}
		return out
	}
	install, uninstall := options("deploy/install.sh"), options("deploy/uninstall.sh")
	if len(install) < 7 || len(uninstall) < 5 {
		t.Fatalf("found the options %v of install.sh and %v of uninstall.sh: the pattern no longer matches the scripts", install, uninstall)
	}
	scripts := []struct {
		name    string
		pattern *regexp.Regexp
		options map[string]bool
	}{
		{"install.sh", regexp.MustCompile(`(^|[^a-z])install\.sh`), install},
		{"uninstall.sh", regexp.MustCompile(`uninstall\.sh`), uninstall},
	}
	option := regexp.MustCompile("(?:^|[\\s`(])(--[a-z][a-z-]*)")
	seen := map[string]int{}
	for _, doc := range []string{"README.md", "docs/install.md", "docs/release.md", "docs/recovery.md", "docs/troubleshooting.md", "docs/user-guide.md", "docs/release-notes/v1.0.0.md"} {
		for n, line := range strings.Split(read(t, doc), "\n") {
			for _, s := range scripts {
				idx := s.pattern.FindStringIndex(line)
				if idx == nil {
					continue
				}
				// the options after the name of the script; a line that names uninstall.sh after install.sh gives the later options to the later script
				after := line[idx[1]:]
				if s.name == "install.sh" {
					if u := strings.Index(after, "uninstall.sh"); u >= 0 {
						after = after[:u]
					}
				}
				seen[s.name]++
				code, _, _ := strings.Cut(after, "#")
				for _, m := range option.FindAllStringSubmatch(code, -1) {
					if !s.options[m[1]] {
						t.Errorf("%s:%d: deploy/%s has no option %s", doc, n+1, s.name, m[1])
					}
				}
			}
		}
	}
	if seen["install.sh"] < 5 || seen["uninstall.sh"] < 4 {
		t.Errorf("the documents name install.sh on %d lines and uninstall.sh on %d: they lost their instructions", seen["install.sh"], seen["uninstall.sh"])
	}
}

// The release notes are the text of the GitHub release: a container exception they name must be one the compose file has (the audit of
// Phase 7 found the notes saying Kea runs without no-new-privileges after the option was set).
func TestTheReleaseNotesNameNoContainerExceptionTheComposeFileDoesNotHave(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			SecurityOpt []string `yaml:"security_opt"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read(t, "deploy/compose.yaml")), &compose); err != nil {
		t.Fatal(err)
	}
	if len(compose.Services) < 8 {
		t.Fatalf("deploy/compose.yaml has %d services", len(compose.Services))
	}
	notes := read(t, "docs/release-notes/v1.0.0.md")
	for name, svc := range compose.Services {
		if slices.Contains(svc.SecurityOpt, "no-new-privileges:true") && regexp.MustCompile("`"+regexp.QuoteMeta(name)+"` container[^.]*without `no-new-privileges`").MatchString(notes) {
			t.Errorf("the release notes say the %s container runs without no-new-privileges, deploy/compose.yaml sets it", name)
		}
	}
}

// The acceptance maps of the development guide name the tests that hold each clause; a test that was renamed leaves a clause with a
// name that protects nothing.
func TestEveryTestTheDevelopmentGuideNamesInATableExists(t *testing.T) {
	defined := map[string]bool{}
	def := regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]*)\(`)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "worktrees") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p) //nolint:gosec // a test file of the repository
		if err != nil {
			return err
		}
		for _, m := range def.FindAllStringSubmatch(string(raw), -1) {
			defined[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(defined) < 1000 {
		t.Fatalf("found %d tests: the walk no longer finds the test files", len(defined))
	}
	name := regexp.MustCompile("`((?:Test|Fuzz)[A-Za-z0-9_]+)(?:/[^`]*)?`")
	rows := 0
	for n, line := range strings.Split(read(t, "docs/development.md"), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		for _, m := range name.FindAllStringSubmatch(line, -1) {
			rows++
			if !defined[m[1]] {
				t.Errorf("docs/development.md:%d names the test %s, which does not exist", n+1, m[1])
			}
		}
	}
	if rows < 100 {
		t.Errorf("the tables name %d tests: the pattern no longer matches", rows)
	}
}
