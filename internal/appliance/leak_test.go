package appliance

import (
	"testing"

	"go.uber.org/goleak"
)

// vm.go starts goroutines (the SSH session and the log collector): none may outlive a test.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
