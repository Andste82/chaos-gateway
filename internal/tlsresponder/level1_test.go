//go:build testbed

package tlsresponder

import (
	"testing"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// This file is why the level 1 runs (tools/testvm) take the package at all: they run only the packages that have tests under the build tag
// `testbed`, and the tests of the interception with the real mitmproxy (M22_LEVEL1 in .github/level1-tests.env) are plain tests of this
// package that need the proxy of the testbed image. A bed without mitmproxy fails here instead of letting them skip.
func TestTheTestbedImageHasMitmproxy(t *testing.T) {
	if _, ok := testbed.MitmPython(); !ok {
		t.Fatal("mitmproxy is not installed (the testbed image has it in /opt/mitm-venv)")
	}
}
