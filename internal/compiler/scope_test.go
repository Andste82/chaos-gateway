package compiler

import (
	"encoding/json"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// executorCheck runs the executor's own scope check over a ruleset: the compiler's output must
// never be something the executor would refuse.
func executorCheck(tx []byte) error { return executor.CheckNftRuleset(json.RawMessage(tx)) }
