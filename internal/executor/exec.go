package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Outcome is the result of a request.
type Outcome struct {
	// Generation counts the requests that changed kernel state. It bumps once per request, also
	// when the request failed half-way: the state may have changed then.
	Generation uint64 `json:"generation"`
	// Completed is the number of operations of the request that ran to the end.
	Completed int `json:"completed"`
	// Data holds the result of each Read operation, in order.
	Data []json.RawMessage `json:"data,omitempty"`
}

// Executor runs requests one at a time, in arrival order. It is the single writer of the
// kernel's network configuration.
type Executor struct {
	run   Runner
	scope *Scope
	log   *slog.Logger
	state string // file holding the assigned interfaces, empty for none

	jobs    chan *job
	done    chan struct{} // closed by Close
	stopped chan struct{} // closed when the worker has returned
	once    sync.Once
	mu      sync.Mutex // guards gen
	gen     uint64
}

type job struct {
	ctx context.Context
	ops []Operation
	out chan jobResult
}

type jobResult struct {
	outcome Outcome
	err     error
}

// Option configures an Executor.
type Option func(*Executor)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(e *Executor) { e.log = l } }

// WithStateFile makes the set of assigned interfaces survive restarts.
func WithStateFile(path string) Option { return func(e *Executor) { e.state = path } }

// New starts an executor. Close stops it.
func New(run Runner, opts ...Option) (*Executor, error) {
	e := &Executor{run: run, scope: NewScope(), log: slog.New(slog.DiscardHandler), jobs: make(chan *job, 64), done: make(chan struct{}), stopped: make(chan struct{})}
	for _, o := range opts {
		o(e)
	}
	if err := e.loadState(); err != nil {
		return nil, err
	}
	go e.worker()
	return e, nil
}

// Close stops the executor: the request that is running finishes (an operation is never
// interrupted, plan §3.11), queued requests are dropped with ErrClosed. Close returns when the
// worker has stopped.
func (e *Executor) Close() {
	e.once.Do(func() { close(e.done) })
	<-e.stopped
}

// Scope returns the executor's scope.
func (e *Executor) Scope() *Scope { return e.scope }

// Generation returns the current generation.
func (e *Executor) Generation() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gen
}

// ErrClosed is returned when the executor has been closed.
var ErrClosed = errors.New("executor is closed")

// Do runs one operation.
func (e *Executor) Do(ctx context.Context, op Operation) (Outcome, error) {
	return e.DoBatch(ctx, []Operation{op})
}

// DoBatch runs operations in order as one request. All operations are checked against the scope
// before the first one runs; the first failure stops the request. An empty batch returns the
// current generation.
func (e *Executor) DoBatch(ctx context.Context, ops []Operation) (Outcome, error) {
	select {
	case <-e.done:
		return Outcome{}, ErrClosed
	default:
	}
	j := &job{ctx: ctx, ops: ops, out: make(chan jobResult, 1)}
	select {
	case e.jobs <- j:
	case <-e.done:
		return Outcome{}, ErrClosed
	case <-ctx.Done():
		return Outcome{Generation: e.Generation()}, ctx.Err()
	}
	select {
	case r := <-j.out:
		return r.outcome, r.err
	case <-e.stopped:
		// the worker is gone: the result is either already there or never will be
		select {
		case r := <-j.out:
			return r.outcome, r.err
		default:
			return Outcome{}, ErrClosed
		}
	case <-ctx.Done():
		// the request stays queued or keeps running: an operation is never interrupted, and a
		// request that already started has to finish for the kernel state to stay consistent
		return Outcome{Generation: e.Generation()}, ctx.Err()
	}
}

func (e *Executor) worker() {
	defer close(e.stopped)
	for {
		select {
		case <-e.done:
			return
		case j := <-e.jobs:
			j.out <- e.execute(j)
		}
	}
}

func (e *Executor) execute(j *job) (res jobResult) {
	var out Outcome
	mutating := false
	defer func() {
		if mutating {
			e.mu.Lock()
			e.gen++
			e.mu.Unlock()
		}
		out.Generation = e.Generation()
		res.outcome = out
	}()
	// a panic in an operation is a failed request, not the end of the only writer; the stack is
	// logged, and the generation bumps because the kernel state is unknown
	defer func() {
		if r := recover(); r != nil {
			e.log.Error("panic while executing a request", "panic", r, "stack", string(debug.Stack()))
			mutating = true
			res = jobResult{err: fmt.Errorf("internal error: %v", r)}
		}
	}()
	// the scope is checked here, in the serialized worker: an assignment queued earlier has
	// taken effect by now. Assignments inside the batch widen the scope for the operations after
	// them, and all operations are checked before the first one runs.
	check := NewScope(e.scope.Devs()...)
	for i, op := range j.ops {
		if a, ok := op.(*AssignInterfaces); ok {
			check.Set(a.Devs)
		}
		if err := check.Check(op); err != nil {
			out.Generation = e.Generation()
			return jobResult{outcome: out, err: fmt.Errorf("operation %d (%s): %w", i, op.OpType(), err)}
		}
	}
	if err := j.ctx.Err(); err != nil {
		return jobResult{err: err} // cancelled while queued: nothing ran
	}
	for i, op := range j.ops {
		if op.Mutates() {
			mutating = true
		}
		data, err := e.runOp(j.ctx, op)
		if err != nil {
			e.log.Warn("operation failed", "type", op.OpType(), "index", i, "error", err)
			return jobResult{err: fmt.Errorf("operation %d (%s): %w", i, op.OpType(), err)}
		}
		if data != nil {
			out.Data = append(out.Data, data)
		}
		out.Completed++
	}
	return jobResult{}
}

// CommandError is a command that exited non-zero.
type CommandError struct {
	Cmd    Command
	Exit   int
	Stderr string
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if len(msg) > 2000 {
		msg = msg[:2000] + "..."
	}
	return fmt.Sprintf("%s exited with status %d: %s", e.Cmd.Tool, e.Exit, msg)
}

func (e *Executor) runOp(ctx context.Context, op Operation) (json.RawMessage, error) {
	switch o := op.(type) {
	case *AssignInterfaces:
		return nil, e.assign(o.Devs)
	case *Read:
		return e.read(ctx, o)
	}
	steps, err := Plan(op)
	if err != nil {
		return nil, err
	}
	for _, s := range steps {
		if s.Probe != nil {
			r, err := e.run.Run(ctx, *s.Probe)
			if err != nil {
				return nil, err
			}
			if (r.Exit == 0) != s.RunIfProbeOK {
				continue
			}
		}
		r, err := e.run.Run(ctx, s.Cmd)
		if err != nil {
			return nil, err
		}
		if r.Exit != 0 && (!s.Idempotent || !onlyBenign(r.Stderr)) {
			return nil, &CommandError{Cmd: s.Cmd, Exit: r.Exit, Stderr: r.Stderr}
		}
	}
	if o, ok := op.(*Offloads); ok {
		return nil, e.verifyOffloads(ctx, o)
	}
	return nil, nil
}

// onlyBenign reports whether every error line of an `ip -force -batch` run is an "already
// exists" or "does not exist" answer: applying an unchanged rule set, or deleting what is gone,
// must not fail.
func onlyBenign(stderr string) bool {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "", strings.HasPrefix(line, "Command failed"):
		case strings.HasSuffix(line, "File exists"), strings.HasSuffix(line, "No such process"), strings.HasSuffix(line, "No such file or directory"):
		default:
			return false
		}
	}
	return true
}

// verifyOffloads checks the result instead of trusting ethtool's exit status: a feature that is
// fixed in the off state on a device (LRO on veth) is fine, one that stays on is not.
func (e *Executor) verifyOffloads(ctx context.Context, o *Offloads) error {
	for _, d := range o.Devs {
		r, err := e.run.Run(ctx, ReadCommand(&Read{Target: o.Target, What: ReadOffloads, Dev: d}))
		if err != nil {
			return err
		}
		if r.Exit != 0 {
			return &CommandError{Cmd: ReadCommand(&Read{Target: o.Target, What: ReadOffloads, Dev: d}), Exit: r.Exit, Stderr: r.Stderr}
		}
		if on := linux.ParseEthtoolFeatures(r.Stdout).OffloadsStillOn(); len(on) > 0 {
			return fmt.Errorf("offloads of %s are still on: %s", d, strings.Join(on, ", "))
		}
	}
	return nil
}

func (e *Executor) read(ctx context.Context, o *Read) (json.RawMessage, error) {
	cmd := ReadCommand(o)
	r, err := e.run.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if r.Exit != 0 {
		// a missing table is an empty state, not a failure: the ruleset is created on the first apply
		if o.What == ReadNft && r.Exit == 1 && strings.Contains(r.Stderr, "No such file or directory") && !strings.Contains(r.Stderr, "network namespace") {
			return json.Marshal(&linux.Ruleset{})
		}
		return nil, &CommandError{Cmd: cmd, Exit: r.Exit, Stderr: r.Stderr}
	}
	var v any
	switch o.What {
	case ReadLinks:
		v, err = linux.ParseLinks([]byte(r.Stdout))
	case ReadAddrs:
		v, err = linux.ParseAddrs([]byte(r.Stdout))
	case ReadRoutes:
		v, err = linux.ParseRoutes([]byte(r.Stdout))
	case ReadRules:
		v, err = linux.ParseRules([]byte(r.Stdout))
	case ReadNft:
		v, err = linux.ParseNft([]byte(r.Stdout))
	case ReadQdiscs:
		v, err = linux.ParseQdiscs([]byte(r.Stdout))
	case ReadClasses:
		v, err = linux.ParseClasses([]byte(r.Stdout))
	case ReadFilters:
		v, err = linux.ParseFilters([]byte(r.Stdout))
	case ReadOffloads:
		v = linux.ParseEthtoolFeatures(r.Stdout)
	}
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	return b, err
}

func (e *Executor) assign(devs []string) error {
	e.scope.Set(devs)
	return e.saveState()
}

type stateFile struct {
	Interfaces []string `json:"interfaces"`
}

func (e *Executor) loadState() error {
	if e.state == "" {
		return nil
	}
	b, err := os.ReadFile(e.state)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var s stateFile
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("read %s: %w", e.state, err)
	}
	if err := checkDevs(s.Interfaces, true); err != nil {
		return fmt.Errorf("read %s: %w", e.state, err)
	}
	e.scope.Set(s.Interfaces)
	return nil
}

func (e *Executor) saveState() error {
	if e.state == "" {
		return nil
	}
	b, err := json.Marshal(stateFile{Interfaces: e.scope.Devs()})
	if err != nil {
		return err
	}
	tmp := e.state + ".tmp"
	if err := os.MkdirAll(filepath.Dir(e.state), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.state)
}
