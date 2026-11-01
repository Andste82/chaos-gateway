package ciconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hostSetupDirs reads DATA_DIRS of the host setup: name, owner and mode of the directories below /var/lib/chaosgw.
func hostSetupDirs(t *testing.T) map[string][2]string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^DATA_DIRS="([^"]+)"$`).FindStringSubmatch(read(t, "deploy", "host-setup.sh"))
	if m == nil {
		t.Fatal("deploy/host-setup.sh has no DATA_DIRS")
	}
	out := map[string][2]string{}
	for _, spec := range strings.Fields(m[1]) {
		f := strings.Split(spec, ":")
		if len(f) != 3 {
			t.Fatalf("DATA_DIRS entry %q is not name:owner:mode", spec)
		}
		out[f[0]] = [2]string{f[1], f[2]}
	}
	return out
}

// The host setup makes what the image gives the API's user and what the compose file mounts from the host (plan §3.6, M28): the same
// directories, with the owner of the Dockerfile (65532 for the API's, the secrets 0700), plus the executor's and Kea's own.
func TestTheHostSetupMakesEveryDirectoryTheImageGivesTheAPIAndTheComposeFileMounts(t *testing.T) {
	dirs := hostSetupDirs(t)
	for _, d := range apiDirs(t) {
		if !strings.HasPrefix(d, "/var/lib/chaosgw/") {
			continue
		}
		name := filepath.Base(d)
		got, ok := dirs[name]
		if !ok {
			t.Errorf("the image gives %s to the API's user and the host setup does not make it", d)
			continue
		}
		if got[0] != "65532" {
			t.Errorf("%s: the host setup gives it to %s, the image to 65532", d, got[0])
		}
		if want := map[bool]string{true: "0700", false: "0755"}[name == "secrets"]; got[1] != want {
			t.Errorf("%s: the host setup gives it the mode %s, want %s", d, got[1], want)
		}
	}
	for _, name := range []string{"exec", "kea"} {
		if got, ok := dirs[name]; !ok || got[0] != "0" {
			t.Errorf("%s: the host setup must make it for root: %v", name, got)
		}
	}

	// every host directory the compose file bind-mounts below /var/lib/chaosgw is one of them, and the other way round
	var doc struct {
		Services map[string]struct {
			Volumes []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read(t, "deploy", "compose.yaml")), &doc); err != nil {
		t.Fatal(err)
	}
	mounted := map[string]bool{}
	for _, s := range doc.Services {
		for _, n := range s.Volumes {
			var v struct {
				Type   string `yaml:"type"`
				Source string `yaml:"source"`
			}
			if n.Kind != yaml.MappingNode { // a named volume of the sockets, in the short form
				continue
			}
			if err := n.Decode(&v); err != nil {
				t.Fatal(err)
			}
			if v.Type == "bind" && strings.HasPrefix(v.Source, "/var/lib/chaosgw/") {
				mounted[strings.TrimPrefix(v.Source, "/var/lib/chaosgw/")] = true
			}
		}
	}
	var made, mount []string
	for name := range dirs {
		made = append(made, name)
	}
	for name := range mounted {
		mount = append(mount, name)
	}
	sort.Strings(made)
	sort.Strings(mount)
	if strings.Join(made, " ") != strings.Join(mount, " ") {
		t.Errorf("the host setup makes %v, compose.yaml mounts %v", made, mount)
	}
	// teardown --purge empties the directories the host setup makes (cmd/chaosgw/teardown.go): the same list
	purge := read(t, "cmd", "chaosgw", "teardown.go")
	for name := range dirs {
		path := "/var/lib/chaosgw/" + name
		if name == "kea" {
			path = "/var/lib/kea"
		}
		if !strings.Contains(purge, `"`+path+`"`) {
			t.Errorf("`chaosgw teardown --purge` does not empty %s", path)
		}
	}
}

// The files the Dockerfile copies into the image for the host (the host setup copies them out) exist, are let through by .dockerignore,
// and the scripts are executable: a COPY of a file the context does not have fails the build only in CI.
func TestTheFilesOfTheDeploymentAreInTheImage(t *testing.T) {
	docker := read(t, "deploy", "Dockerfile")
	ignore := read(t, ".dockerignore")
	files := 0
	for _, l := range strings.Split(docker, "\n") {
		if !strings.HasPrefix(l, "COPY deploy/") {
			continue
		}
		fields := strings.Fields(l)
		for _, f := range fields[1 : len(fields)-1] {
			files++
			if _, err := os.Stat(filepath.Join("..", "..", f)); err != nil {
				t.Errorf("the Dockerfile copies %s: %v", f, err)
			}
			if !strings.Contains(ignore, "\n!"+f+"\n") {
				t.Errorf(".dockerignore does not let %s into the build context", f)
			}
			if strings.HasSuffix(f, ".sh") {
				if fi, err := os.Stat(filepath.Join("..", "..", f)); err == nil && fi.Mode()&0o111 == 0 {
					t.Errorf("%s is not executable", f)
				}
			}
		}
	}
	if files < 6 {
		t.Errorf("found %d files copied from deploy/ in the Dockerfile", files)
	}
	// what host-setup.sh --image copies out of the image is in /usr/local/share/chaosgw
	for _, f := range []string{"compose.yaml", "update.sh", "env.example", "host-setup.sh"} {
		if !strings.Contains(docker, "deploy/"+f) {
			t.Errorf("the Dockerfile does not copy deploy/%s", f)
		}
	}
	setup := read(t, "deploy", "host-setup.sh")
	if !strings.Contains(setup, "-cf - compose.yaml update.sh host-setup.sh env.example install.sh uninstall.sh") || !strings.Contains(docker, "/usr/local/share/chaosgw/") {
		t.Error("the host setup copies other files out of the image than the ones the Dockerfile puts there")
	}
	// the tools the executor and the containers run (plan §3.4) and tcpdump (M28)
	for _, pkg := range []string{"tcpdump", "kmod", "iproute2", "nftables", "iptables", "ethtool", "conntrack", "wireguard-tools"} {
		if !regexp.MustCompile(`(?m)^\s+.*\b` + pkg + `\b.*\\?$`).MatchString(docker) {
			t.Errorf("the Dockerfile does not install %s", pkg)
		}
	}
	// the stale description of P5-AUDIT-22 is gone
	if strings.Contains(docker, "follows with M28") {
		t.Error("the Dockerfile still says part of the image follows with M28")
	}
}

// smokeTestsBeforePush says whether the steps that run the image smoke test of both platforms all come before the step that pushes.
// The comments of the workflow name the script too, so the position is that of the `run:` lines, not of the first mention.
func smokeTestsBeforePush(workflow string) bool {
	push := strings.Index(workflow, "push: ${{")
	amd, arm := strings.LastIndex(workflow, "run: deploy/smoke-image.sh linux/amd64"), strings.LastIndex(workflow, "run: deploy/smoke-image.sh linux/arm64")
	return push >= 0 && amd >= 0 && arm >= 0 && amd < push && arm < push
}

// Every smoke test of the deployment is run by a job, with the image that job built: the image on both platforms, the whole compose file
// on a Docker host. The release workflow runs the image smoke test before it pushes.
func TestTheSmokeTestsOfTheDeploymentAreRunByTheJobs(t *testing.T) {
	ci := read(t, ".github", "workflows", "ci.yml")
	for _, want := range []string{
		"deploy/smoke-image.sh linux/amd64 chaos-gateway:ci",
		"deploy/smoke-image.sh linux/arm64 chaos-gateway:ci-arm64",
		"tags: chaos-gateway:ci-arm64",
		"shellcheck deploy/*.sh",
		"CHAOSGW_VERSION=ci deploy/smoke-compose.sh",
		"sudo sh deploy/host-setup.sh --data-only",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml lacks %q", want)
		}
	}
	image := read(t, ".github", "workflows", "image.yml")
	for _, want := range []string{"deploy/smoke-image.sh linux/amd64", "deploy/smoke-image.sh linux/arm64", "load: true"} {
		if !strings.Contains(image, want) {
			t.Errorf("image.yml lacks %q: the image is pushed without having been started", want)
		}
	}
	if !smokeTestsBeforePush(image) {
		t.Error("image.yml pushes before it runs the image smoke test of both platforms (the steps `run: deploy/smoke-image.sh ...`)")
	}
	// the check looks at the steps, not at the comments that name the script
	pushed := "# deploy/smoke-image.sh runs first\nsteps:\n  - run: deploy/smoke-image.sh linux/amd64 a\n  - run: deploy/smoke-image.sh linux/arm64 b\n  - with:\n      push: ${{ true }}\n"
	if !smokeTestsBeforePush(pushed) {
		t.Error("the smoke steps before the push are not recognised")
	}
	for name, wf := range map[string]string{
		"steps after the push": "# deploy/smoke-image.sh\n  - with:\n      push: ${{ true }}\n  - run: deploy/smoke-image.sh linux/amd64 a\n  - run: deploy/smoke-image.sh linux/arm64 b\n",
		"comment only":         "# deploy/smoke-image.sh\n  - with:\n      push: ${{ true }}\n",
		"one platform":         "  - run: deploy/smoke-image.sh linux/amd64 a\n  - with:\n      push: ${{ true }}\n",
		"no push":              "  - run: deploy/smoke-image.sh linux/amd64 a\n  - run: deploy/smoke-image.sh linux/arm64 b\n",
	} {
		if smokeTestsBeforePush(wf) {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, script := range []string{"smoke-image.sh", "smoke-compose.sh", "update.sh", "host-setup.sh"} {
		if fi, err := os.Stat(filepath.Join("..", "..", "deploy", script)); err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("deploy/%s is not executable: %v", script, err)
		}
	}
	// the nightly appliance run installs the files the way an administrator does
	nightly := read(t, ".github", "workflows", "nightly.yml")
	if !strings.Contains(nightly, "TestContainerDeploymentOnCleanUbuntuHosts") {
		t.Error("nightly.yml does not run the level 2 test of the container deployment")
	}
}

// The installation guide describes the deployment that is shipped: the host setup with the image, the compose file, the update script, the
// data directory, the optional profile and the teardown service.
func TestTheInstallationGuideNamesTheFilesAndCommandsOfTheDeployment(t *testing.T) {
	doc := read(t, "docs", "install.md")
	for _, want := range []string{"host-setup.sh", "--image", "docker compose up -d", "update.sh", "compose.yaml", "/var/lib/chaosgw", "--profile teardown", "COMPOSE_PROFILES=interception", "Setup token"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/install.md does not mention %q", want)
		}
	}
}

// A tag with a pre-release part (the release candidate that tests the workflow) neither moves :latest nor makes a normal GitHub release, and
// the files of the deployment are attached to the release of a tag.
func TestAPreReleaseTagDoesNotMoveLatestAndMakesAPreRelease(t *testing.T) {
	image := read(t, ".github", "workflows", "image.yml")
	latest := regexp.MustCompile(`type=raw,value=latest,enable=(.*)`).FindStringSubmatch(image)
	if latest == nil {
		t.Fatal("image.yml does not tag :latest")
	}
	for _, want := range []string{"startsWith(github.ref, 'refs/tags/v')", "!contains(github.ref_name, '-')"} {
		if !strings.Contains(latest[1], want) {
			t.Errorf(":latest is enabled by %q, which lacks %q: a release candidate would move it", latest[1], want)
		}
	}
	create := regexp.MustCompile(`gh release create .*`).FindString(image)
	if create == "" || !strings.Contains(create, "$flags") || !strings.Contains(image, `*-*) flags="--prerelease"`) {
		t.Errorf("image.yml creates the release without --prerelease for a tag with a hyphen: %q", create)
	}
	for _, want := range []string{"gh release upload", "compose.yaml host-setup.sh install.sh update.sh uninstall.sh env.example SHA256SUMS", "if: startsWith(github.ref, 'refs/tags/v')"} {
		if !strings.Contains(image, want) {
			t.Errorf("image.yml lacks %q: the files of the deployment are not attached to the release", want)
		}
	}
}

// A release is published only from a checked tag: the check of the tag (deploy/release-check.sh, on the whole history) comes before the first
// build and the first push, and after the push the manifest is read back for both platforms.
func TestTheReleaseWorkflowChecksTheTagBeforeItBuildsAndTheManifestAfterItPushes(t *testing.T) {
	image := read(t, ".github", "workflows", "image.yml")
	if !strings.Contains(image, "fetch-depth: 0") {
		t.Error("image.yml checks out no history: release-check.sh cannot tell whether the tag is on main")
	}
	pos := func(s string) int { return strings.Index(image, s) }
	check, build, push, manifest, upload := pos(`deploy/release-check.sh "$GITHUB_REF_NAME"`), pos("docker/build-push-action@v7"), pos("push: ${{ github.event_name"), pos("imagetools inspect"), pos("gh release upload")
	for name, p := range map[string]int{"the check of the tag": check, "the first build": build, "the push": push, "the check of the manifest": manifest, "the upload": upload} {
		if p < 0 {
			t.Fatalf("image.yml has no %s", name)
		}
	}
	if check >= build || build >= push || push >= manifest || manifest >= upload {
		t.Errorf("the order in image.yml is wrong: check %d, build %d, push %d, manifest %d, upload %d", check, build, push, manifest, upload)
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if !strings.Contains(image[manifest:], platform) {
			t.Errorf("the check of the manifest does not look for %s", platform)
		}
	}
	if !strings.Contains(image, "if: startsWith(github.ref, 'refs/tags/')\n        run: deploy/release-check.sh") {
		t.Error("the check of the tag does not run for a pushed tag only")
	}
}

// The functional smoke test of the arm64 image is the weekly emulated run of level 2: the job exists, builds the arm64 image, sets the
// architecture, runs only the smoke test (the other level 2 tests boot amd64) and its go test timeout is below the timeout of the job.
func TestTheWeeklyJobRunsTheSmokeTestOnAnEmulatedArm64Machine(t *testing.T) {
	var doc struct {
		Jobs map[string]struct {
			Timeout int `yaml:"timeout-minutes"`
			Steps   []struct {
				Env map[string]string `yaml:"env"`
				Run string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(read(t, ".github", "workflows", "weekly.yml")), &doc); err != nil {
		t.Fatal(err)
	}
	job, ok := doc.Jobs["appliance-arm64"]
	if !ok {
		t.Fatal("weekly.yml has no job appliance-arm64: nothing runs the functional smoke test of the arm64 image")
	}
	var build, test bool
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "docker buildx build --platform linux/arm64") {
			build = true
		}
		if strings.Contains(s.Run, "go test -tags appliance") {
			test = true
			if s.Env["CHAOSGW_APPLIANCE_ARCH"] != "arm64" {
				t.Errorf("the appliance test of the weekly job runs with CHAOSGW_APPLIANCE_ARCH=%q", s.Env["CHAOSGW_APPLIANCE_ARCH"])
			}
			if !strings.Contains(s.Run, "-run 'TestSmokeOnCleanUbuntuHosts'") {
				t.Errorf("the weekly arm64 job runs other tests than the smoke test: %s", s.Run)
			}
			m := regexp.MustCompile(`-timeout (\d+)m`).FindStringSubmatch(s.Run)
			if m == nil {
				t.Fatalf("the go test of the weekly arm64 job has no -timeout: %s", s.Run)
			}
			if n, _ := strconv.Atoi(m[1]); n >= job.Timeout {
				t.Errorf("go test -timeout %dm is not below the timeout of the job (%d minutes)", n, job.Timeout)
			}
		}
	}
	if !build || !test {
		t.Errorf("the weekly arm64 job builds the arm64 image: %v, runs the appliance test: %v", build, test)
	}
}
