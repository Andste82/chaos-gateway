//go:build testbed

// Package fixture is a tiny test package for the vmrun tests. It is under testdata, so
// `go test ./...` ignores it; the vmrun tests build it by path.
package fixture

import (
	"os"
	"testing"
)

func TestPasses(t *testing.T) {
	t.Run("sub", func(t *testing.T) {})
}

func TestSkips(t *testing.T) { t.Skip("skipped on purpose") }

func TestWorkingDirectoryIsThePackageDirectory(t *testing.T) {
	if _, err := os.Stat("fixture_test.go"); err != nil {
		t.Fatalf("the guest must run the binary in the package directory: %v", err)
	}
}

func TestEmulationFlag(t *testing.T) {
	if want := os.Getenv("VMRUN_FIXTURE_WANT_EMULATED"); want != "" {
		if got := os.Getenv("CHAOSGW_TESTBED_EMULATED"); got != want {
			t.Fatalf("CHAOSGW_TESTBED_EMULATED = %q, want %q", got, want)
		}
	}
}

func TestFailsOnRequest(t *testing.T) {
	if os.Getenv("VMRUN_FIXTURE_FAIL") != "" {
		t.Fatal("failing as requested")
	}
}
