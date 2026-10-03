package clock

import (
	"testing"

	"go.uber.org/goleak"
)

// The fake clock runs timer callbacks in goroutines: none may outlive a test (plan §3.11).
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
