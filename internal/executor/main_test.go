package executor

import (
	"testing"

	"go.uber.org/goleak"
)

// The executor starts goroutines (the worker, one per connection): no test may leave one behind
// (plan §3.11).
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
