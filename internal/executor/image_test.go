package executor_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Issue #57 F7: an image built on the host names its commit (`chaosgw version`) and carries the tag the
// compose files use. `make image-local` builds one for the host's architecture, loads it into the local
// image store and tags it chaos-gateway:latest and :$(VERSION), with the version build arguments.
func TestMakeImageLocalBuildsAVersionedLocalImage(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make")
	}
	cmd := exec.Command("make", "-s", "-n", "image-local", "VERSION=v9.9.9", "COMMIT=abc1234", "DATE=2026-01-02T03:04:05Z")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n image-local: %v\n%s", err, out)
	}
	got := strings.Join(strings.Fields(strings.ReplaceAll(string(out), "\\\n", " ")), " ")
	for _, want := range []string{"docker buildx build", "--load", "-f deploy/Dockerfile",
		"--build-arg VERSION=v9.9.9", "--build-arg COMMIT=abc1234", "--build-arg DATE=2026-01-02T03:04:05Z",
		"-t chaos-gateway:latest", "-t chaos-gateway:v9.9.9"} {
		if !strings.Contains(got, want) {
			t.Errorf("make image-local does not run %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--platform") {
		t.Errorf("make image-local builds for other platforms than the host's:\n%s", got)
	}
}
