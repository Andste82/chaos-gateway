package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// Result is the outcome of an apply that reached the kernel.
type Result struct {
	Plan    *Plan
	Outcome executor.Outcome
	// After is the state read back for verify.
	After *State
	// Mismatches are the differences verify found; empty when the kernel matches the target.
	Mismatches []Mismatch
}

// Error is a failed apply. Stage tells where: "read", "plan", "execute" or "verify". An execute
// failure may have changed the kernel half-way: the caller restores the previous state.
type Error struct {
	Stage      string
	Err        error
	Mismatches []Mismatch
}

func (e *Error) Error() string {
	if len(e.Mismatches) > 0 {
		var parts []string
		for i, m := range e.Mismatches {
			if i == 3 {
				parts = append(parts, fmt.Sprintf("and %d more", len(e.Mismatches)-3))
				break
			}
			parts = append(parts, m.String())
		}
		return "apply " + e.Stage + ": " + strings.Join(parts, "; ")
	}
	return "apply " + e.Stage + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// ErrVerify is wrapped by a verify failure.
var ErrVerify = errors.New("the kernel does not match the target")

func want(t *compiler.Target) Want {
	return Want{Sysctls: t.Sysctls, Offloads: t.Offloads, BirdInstance: compiler.BirdInstance}
}

// Preview reads the current state and plans what an apply would do, without changing anything.
func Preview(ctx context.Context, ex Exec, ns string, t *compiler.Target) (*Plan, error) {
	s, err := ReadState(ctx, ex, ns, want(t))
	if err != nil {
		return nil, &Error{Stage: "read", Err: err}
	}
	p, err := BuildPlan(t, s, ns)
	if err != nil {
		return nil, &Error{Stage: "plan", Err: err}
	}
	return p, nil
}

// Apply carries the target into the kernel: it reads the state, plans the difference, applies it
// as one executor request and verifies the result by reading the state back (plan §2.14). A
// failing apply returns an *Error; the result is returned in every case where the plan was built.
func Apply(ctx context.Context, ex Exec, ns string, t *compiler.Target) (*Result, error) {
	s, err := ReadState(ctx, ex, ns, want(t))
	if err != nil {
		return nil, &Error{Stage: "read", Err: err}
	}
	p, err := BuildPlan(t, s, ns)
	if err != nil {
		return nil, &Error{Stage: "plan", Err: err}
	}
	res := &Result{Plan: p}
	out, err := ex.Do(ctx, p.Ops...)
	res.Outcome = out
	if err != nil {
		return res, &Error{Stage: "execute", Err: err}
	}
	after, err := ReadState(ctx, ex, ns, want(t))
	if err != nil {
		return res, &Error{Stage: "verify", Err: err}
	}
	res.After = after
	res.Mismatches = Verify(t, after)
	if len(res.Mismatches) > 0 {
		return res, &Error{Stage: "verify", Err: ErrVerify, Mismatches: res.Mismatches}
	}
	return res, nil
}
