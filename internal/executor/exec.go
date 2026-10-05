package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/Andste82/chaos-gateway/internal/bird"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Outcome is the result of a request.
type Outcome struct {
	// Generation counts the requests that changed kernel state. It bumps once per request, also
	// when the request failed half-way: the state may have changed then. It counts mutating
	// requests since this executor started; it is not persistent (decided 2026-10-04, M3-04) — the
	// API's own generation, which is persisted, lives in the engine (see M5-01).
	Generation uint64 `json:"generation"`
	// Completed is the number of operations of the request that ran to the end.
	Completed int `json:"completed"`
	// Data holds the result of each Read operation, in order.
	Data []json.RawMessage `json:"data,omitempty"`
}

// Executor runs requests one at a time, in arrival order. It is the single writer of the
// kernel's network configuration.
type Executor struct {
	// inode identifies a network namespace file; see WithNetnsInode
	inode   func(path string) (uint64, bool)
	run     Runner
	scope   *Scope
	log     *slog.Logger
	state   string // file holding the assigned interfaces, empty for none
	keys    KeyProvider
	birdDir string // where the BIRD configuration files and control sockets live

	// queue holds the requests that wait. Identity updates (incremental set elements, plan §2.3,
	// §3.11) go before queued plans; within a class the order is the arrival order. One worker takes
	// them one at a time: an update never runs while a plan does.
	queue struct {
		mu           sync.Mutex
		high, normal []*job
	}
	wake    chan struct{} // an enqueued job
	slots   chan struct{} // bounds the queue: a request waits for a slot
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

// KeyProvider looks up the WireGuard keys of an object by its UUID: the private key of an
// interface or of a peer the gateway generated, and the preshared key. Keys reach the executor
// this way and never in an operation (plan §2.16).
type KeyProvider func(id string) (private, preshared string, err error)

// WithKeys gives the executor access to the secrets store.
func WithKeys(p KeyProvider) Option { return func(e *Executor) { e.keys = p } }

// WithBirdDir tells the executor where BIRD's configuration files and control sockets are
// (<dir>/<instance>.conf and .ctl): a directory shared with the BIRD container.
func WithBirdDir(dir string) Option { return func(e *Executor) { e.birdDir = dir } }

// WithStateFile makes the set of assigned interfaces survive restarts.
func WithStateFile(path string) Option { return func(e *Executor) { e.state = path } }

// WithNetnsInode replaces how the executor identifies a network namespace file (the tests of the
// service namespace have no real namespaces).
func WithNetnsInode(f func(path string) (uint64, bool)) Option {
	return func(e *Executor) { e.inode = f }
}

// New starts an executor. Close stops it.
func New(run Runner, opts ...Option) (*Executor, error) {
	e := &Executor{run: run, inode: realInode, scope: NewScope(), log: slog.New(slog.DiscardHandler), wake: make(chan struct{}, 1), slots: make(chan struct{}, queueSlots), done: make(chan struct{}), stopped: make(chan struct{})}
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
	case e.slots <- struct{}{}:
	case <-e.done:
		return Outcome{}, ErrClosed
	case <-ctx.Done():
		return Outcome{Generation: e.Generation()}, ctx.Err()
	}
	e.enqueue(j)
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

// Watch streams events of the named kind in the given namespace (M6a-04): it never enters the
// request queue, so a long-lived watch never blocks, and is never blocked by, an ordinary request.
// Each line is parsed and sent on the returned channel as a linux.ConntrackEvent; a line the
// parser rejects is skipped. The channel closes once the underlying command ends, for any reason
// (ctx cancelled, stop called, the executor closing, or the command itself exiting, which for a
// watch is always unexpected); stop ends it early and waits for the command to be reaped.
func (e *Executor) Watch(ctx context.Context, what, ns string) (<-chan linux.ConntrackEvent, func(), error) {
	str, ok := e.run.(Streamer)
	if !ok {
		return nil, nil, errors.New("this runner cannot stream a watch")
	}
	cmd, err := watchCommand(what, ns)
	if err != nil {
		return nil, nil, err
	}
	// wctx also ends the watch when the executor itself closes, not only when ctx does or stop is
	// called: a watch outlives no caller's request, but it must not outlive the executor either.
	wctx, cancelW := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.done:
		case <-wctx.Done():
		}
		cancelW()
	}()
	lines, rawStop, err := str.Stream(wctx, cmd)
	if err != nil {
		cancelW()
		return nil, nil, err
	}
	out := make(chan linux.ConntrackEvent)
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		for line := range lines {
			ev, ok := linux.ParseConntrackEventLine(line)
			if !ok {
				continue
			}
			select {
			case out <- ev:
			case <-stopCh:
				// stop is about to cancel the underlying command too: abandoning the rest of
				// `lines` here does not leak it, since closing stopCh always precedes that.
				return
			case <-wctx.Done():
				// the executor itself is stopping, or ctx ended, with nobody having called stop:
				// the caller's read loop may be gone too, so do not wait for it either.
				return
			}
		}
	}()
	stop := func() {
		close(stopCh)
		rawStop()
		cancelW()
		<-done
	}
	return out, stop, nil
}

// waiting returns how many requests wait in the queue.
func (e *Executor) waiting() int {
	e.queue.mu.Lock()
	defer e.queue.mu.Unlock()
	return len(e.queue.high) + len(e.queue.normal)
}

// queueSlots is how many requests may wait.
const queueSlots = 64

// isIdentityUpdate reports whether a request consists of incremental set-element operations only.
func isIdentityUpdate(ops []Operation) bool {
	if len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		switch op.(type) {
		case *NftAddElements, *NftDelElements:
		default:
			return false
		}
	}
	return true
}

func (e *Executor) enqueue(j *job) {
	e.queue.mu.Lock()
	if isIdentityUpdate(j.ops) {
		e.queue.high = append(e.queue.high, j)
	} else {
		e.queue.normal = append(e.queue.normal, j)
	}
	e.queue.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Executor) pop() *job {
	e.queue.mu.Lock()
	defer e.queue.mu.Unlock()
	var j *job
	switch {
	case len(e.queue.high) > 0:
		j, e.queue.high = e.queue.high[0], e.queue.high[1:]
	case len(e.queue.normal) > 0:
		j, e.queue.normal = e.queue.normal[0], e.queue.normal[1:]
	}
	if j != nil {
		<-e.slots
	}
	return j
}

func (e *Executor) worker() {
	defer close(e.stopped)
	for {
		select {
		case <-e.done:
			return
		default:
		}
		if j := e.pop(); j != nil {
			j.out <- e.execute(j)
			continue
		}
		select {
		case <-e.done:
			return
		case <-e.wake:
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
	check.SetOSOwned(e.scope.OSOwned())
	for i, op := range j.ops {
		if a, ok := op.(*AssignInterfaces); ok {
			check.Set(a.Devs)
			check.SetOSOwned(a.OSOwned)
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
		return nil, e.assign(o.Devs, o.OSOwned)
	case *Read:
		return e.read(ctx, o)
	}
	if l, ok := op.(*Links); ok {
		if err := e.checkBridges(ctx, l); err != nil {
			return nil, err
		}
	}
	if b, ok := op.(*Bird); ok {
		return nil, e.runBird(ctx, b)
	}
	if s, ok := op.(*ServiceNS); ok && s.Action == "ensure" && s.HolderPID > 0 {
		// a stale PID file can name any process: its namespace must exist and must not be the
		// executor's own (the pair would end up in one namespace, with a route in the host's)
		want, ok := e.inode("/proc/" + strconv.Itoa(s.HolderPID) + "/ns/net")
		if !ok {
			return nil, fmt.Errorf("the holder process %d of the service namespace does not exist", s.HolderPID)
		}
		if own, ok := e.inode("/proc/self/ns/net"); ok && own == want {
			return nil, fmt.Errorf("process %d is in the executor's own network namespace: not a holder", s.HolderPID)
		}
		// a reused PID would otherwise attach to whatever process happens to have it now (M6b-10): the
		// holder reports the inode of the namespace it was actually in when it wrote its PID file, and
		// that must be the one this PID has now
		if s.HolderNetnsInode != 0 && want != s.HolderNetnsInode {
			return nil, fmt.Errorf("process %d is not the service namespace's holder any more (its PID was reused)", s.HolderPID)
		}
		st := e.serviceNSState(s.Name, s.HolderPID)
		if st.Exists && !st.HolderMatches {
			s.recreate = true
		}
	}
	if s, ok := op.(*ServiceNS); ok && s.Action == "delete" {
		if err := e.checkKind(ctx, s.Target, s.HostIf, "veth"); err != nil {
			return nil, err
		}
	}
	if w, ok := op.(*WireGuard); ok && w.Action == "delete" {
		if err := e.checkKind(ctx, w.Target, w.Name, "wireguard"); err != nil {
			return nil, err
		}
	}
	steps, err := Plan(op)
	if err != nil {
		return nil, err
	}
	for _, s := range steps {
		if s.Guard != nil {
			r, err := e.run.Run(ctx, *s.Guard)
			if err != nil {
				return nil, err
			}
			if r.Exit != 0 {
				continue
			}
		}
		if s.Probe != nil {
			r, err := e.run.Run(ctx, *s.Probe)
			if err != nil {
				return nil, err
			}
			if (r.Exit == 0) != s.RunIfProbeOK {
				continue
			}
		}
		cmd := s.Cmd
		if s.NeedsConfig {
			conf, err := e.wgConfig(op.(*WireGuard))
			if err != nil {
				return nil, err
			}
			cmd.Stdin = conf
		}
		r, err := e.run.Run(ctx, cmd)
		if err != nil {
			return nil, err
		}
		if r.Exit != 0 && (!s.Idempotent || !onlyBenign(r.Stderr)) {
			stderr := r.Stderr
			if s.NeedsConfig {
				// the message of a rejected configuration may quote a key
				stderr = "wg rejected the configuration"
			}
			return nil, &CommandError{Cmd: s.Cmd, Exit: r.Exit, Stderr: stderr}
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
		case strings.HasSuffix(line, "File exists"), strings.HasSuffix(line, "No such process"), strings.HasSuffix(line, "No such file or directory"), strings.HasSuffix(line, "Cannot assign requested address"), strings.HasSuffix(line, "Address not found."):
		default:
			return false
		}
	}
	return true
}

// checkKind makes sure that a device exists as the kind it is deleted as: `ip link delete ... type
// X` does not refuse a device of another kind. A device that does not exist is fine.
func (e *Executor) checkKind(ctx context.Context, tg Target, name, kind string) error {
	r, err := e.run.Run(ctx, ReadCommand(&Read{Target: tg, What: ReadLinks, Dev: name}))
	if err != nil {
		return err
	}
	if r.Exit != 0 {
		return nil
	}
	links, err := linux.ParseLinks([]byte(r.Stdout))
	if err != nil {
		return err
	}
	for _, x := range links {
		if x.Name == name && x.Kind() != kind {
			return fmt.Errorf("%s is not a %s: refusing to delete it", name, kind)
		}
	}
	return nil
}

// wgConfig renders the configuration `wg syncconf` reads. It holds the private key of the
// interface and the preshared keys of the peers: it goes to the tool's standard input and nowhere
// else, and it is not logged.
func (e *Executor) wgConfig(w *WireGuard) (string, error) {
	if e.keys == nil {
		return "", errors.New("the executor has no access to the WireGuard keys")
	}
	priv, _, err := e.keys(w.KeyRef)
	if err != nil {
		return "", fmt.Errorf("the key of %s: %w", w.Name, err)
	}
	if priv == "" {
		return "", fmt.Errorf("the key of %s has no private key", w.Name)
	}
	// `wg` echoes a key it rejects: a malformed one is refused here, before it is rendered
	if !wgKey.MatchString(priv) {
		return "", fmt.Errorf("the key of %s is not a WireGuard key", w.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = %d\n", priv, w.ListenPort)
	for _, p := range w.Peers {
		fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\n", p.PublicKey)
		if p.PresharedKeyRef != "" {
			_, psk, err := e.keys(p.PresharedKeyRef)
			if err != nil {
				return "", fmt.Errorf("the preshared key of a peer of %s: %w", w.Name, err)
			}
			if psk != "" {
				if !wgKey.MatchString(psk) {
					return "", fmt.Errorf("the preshared key of a peer of %s is not a WireGuard key", w.Name)
				}
				fmt.Fprintf(&b, "PresharedKey = %s\n", psk)
			}
		}
		fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(p.AllowedIPs, ", "))
		if p.Keepalive > 0 {
			fmt.Fprintf(&b, "PersistentKeepalive = %d\n", p.Keepalive)
		}
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "Endpoint = %s\n", p.Endpoint)
		}
	}
	return b.String(), nil
}

// checkBridges makes sure that delete_bridge only ever deletes a bridge: `ip link delete ... type
// bridge` does not refuse a device of another kind (a veth was deleted by it in the testbed), so
// the kind is read first. A device that does not exist is fine, deleting it is a no-op.
func (e *Executor) checkBridges(ctx context.Context, l *Links) error {
	for _, en := range l.Entries {
		if en.Action != "delete_bridge" {
			continue
		}
		cmd := ReadCommand(&Read{Target: l.Target, What: ReadLinks, Dev: en.Name})
		r, err := e.run.Run(ctx, cmd)
		if err != nil {
			return err
		}
		if r.Exit != 0 {
			continue // no such device
		}
		links, err := linux.ParseLinks([]byte(r.Stdout))
		if err != nil {
			return err
		}
		for _, x := range links {
			if x.Name == en.Name && x.Kind() != "bridge" {
				return fmt.Errorf("%s is not a bridge: refusing to delete it", en.Name)
			}
		}
	}
	return nil
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
	if o.What == ReadAssigned {
		return json.Marshal(e.scope.Devs())
	}
	if o.What == ReadServiceNS {
		return json.Marshal(e.serviceNSState(o.Service, o.PID))
	}
	if o.What == ReadBird {
		st, err := e.readBird(ctx, o.Instance)
		if err != nil {
			return nil, err
		}
		return json.Marshal(st)
	}
	cmd := ReadCommand(o)
	r, err := e.run.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if r.Exit != 0 {
		// a missing table is an empty state, not a failure: the ruleset is created on the first apply
		if o.What == ReadDockerUser && strings.Contains(r.Stderr, "No chain/target/match by that name") {
			return json.Marshal(&linux.DockerUserState{})
		}
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
	case ReadSysctl:
		var n int
		n, err = strconv.Atoi(strings.TrimSpace(r.Stdout))
		v = n
	case ReadDockerUser:
		v = linux.ParseDockerUser(r.Stdout)
	case ReadNeighbors:
		v, err = linux.ParseNeighbors([]byte(r.Stdout))
	case ReadConntrack:
		v = linux.ParseConntrack(r.Stdout)
	case ReadWireGuard:
		// the dump starts with the private key: the parser drops it, and the output goes no further
		v, err = linux.ParseWGDump(r.Stdout)
	}
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	return b, err
}

func (e *Executor) assign(devs, osOwned []string) error {
	e.scope.Set(devs)
	e.scope.SetOSOwned(osOwned)
	return e.saveState()
}

type stateFile struct {
	Interfaces []string `json:"interfaces"`
	OSOwned    []string `json:"os_owned,omitempty"`
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
	e.scope.SetOSOwned(s.OSOwned)
	return nil
}

func (e *Executor) saveState() error {
	if e.state == "" {
		return nil
	}
	b, err := json.Marshal(stateFile{Interfaces: e.scope.Devs(), OSOwned: e.scope.OSOwned()})
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

// EnsureBirdConfig writes the idle configuration of the instance when there is no file yet: BIRD
// needs one to start, and from then on every apply replaces it. It does nothing without a BIRD
// directory.
func (e *Executor) EnsureBirdConfig(instance string) error {
	if e.birdDir == "" {
		return nil
	}
	conf, _, err := e.birdPaths(instance)
	if err != nil {
		return err
	}
	if _, err := os.Stat(conf); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(conf, []byte(bird.Idle(OwnTableFirst)), 0o644)
}

// BirdState is what a bird read returns.
type BirdState struct {
	// Running reports whether the instance answers on its control socket.
	Running bool `json:"running"`
	// ConfigHash is the SHA-256 of the configuration file, empty when there is none.
	ConfigHash string `json:"config_hash,omitempty"`
	// Config is the text of the configuration file, empty when there is none.
	Config string `json:"config,omitempty"`
	// Protocols are the protocols of the running instance.
	Protocols []bird.ProtocolStatus `json:"protocols,omitempty"`
}

// ErrNoBirdDir is returned by a bird operation or read when the executor has no BIRD directory
// (--bird-dir): a configuration that uses dynamic routing cannot be applied, but a check that
// finds nothing to check is not itself an error of the configuration.
var ErrNoBirdDir = errors.New("the executor has no BIRD directory (--bird-dir)")

func (e *Executor) birdPaths(instance string) (conf, sock string, err error) {
	if e.birdDir == "" {
		return "", "", ErrNoBirdDir
	}
	return filepath.Join(e.birdDir, instance+".conf"), filepath.Join(e.birdDir, instance+".ctl"), nil
}

// birdOutput runs a BIRD tool and returns its output; the tools print their errors on either stream.
func (e *Executor) birdOutput(ctx context.Context, tool Tool, args ...string) (string, int, error) {
	r, err := e.run.Run(ctx, Command{Tool: tool, Args: args})
	if err != nil {
		return "", 0, err
	}
	return r.Stdout + r.Stderr, r.Exit, nil
}

// birdRunning probes the control socket the same way readBird does: a non-zero exit from a
// harmless read is BIRD not running (the daemon down, or its socket gone), whatever birdc's exact
// wording for that case happens to be.
func (e *Executor) birdRunning(ctx context.Context, sock string) (bool, error) {
	_, exit, err := e.birdOutput(ctx, ToolBirdc, "-s", sock, "show", "protocols", "all")
	if err != nil {
		return false, err
	}
	return exit == 0, nil
}

// runBird implements the bird operation.
func (e *Executor) runBird(ctx context.Context, o *Bird) error {
	conf, sock, err := e.birdPaths(o.Instance)
	if err != nil {
		return err
	}
	// the text is parsed from a file of its own first: a rejected configuration never replaces the
	// one that runs
	tmp, err := os.CreateTemp(e.birdDir, ".check-*.conf")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(o.Config); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	out, exit, err := e.birdOutput(ctx, ToolBird, "-p", "-c", tmpName)
	if err != nil {
		return err
	}
	if exit != 0 {
		return &BirdError{Message: strings.TrimSpace(strings.ReplaceAll(out, tmpName, "configuration"))}
	}
	if o.Action == "check" {
		return nil
	}
	previous, readErr := os.ReadFile(conf)
	if err := os.Rename(tmpName, conf); err != nil {
		return err
	}
	if running, err := e.birdRunning(ctx, sock); err != nil {
		return err
	} else if !running {
		// best effort (M4c-02): the daemon reads this file at its own start, so the new
		// configuration stays in place instead of being restored; nothing else about this apply
		// failed, so it must not restore the file or fail the apply either.
		return &BirdDownError{}
	}
	out, exit, err = e.birdOutput(ctx, ToolBirdc, "-s", sock, "configure")
	if err == nil && (exit != 0 || (!strings.Contains(out, "Reconfigured") && !strings.Contains(out, "Reconfiguration in progress"))) {
		err = &BirdError{Message: strings.TrimSpace(out)}
	}
	if err != nil {
		// the daemon keeps what it runs: the file has to say the same, or a retry would find the
		// target in place and plan nothing
		if readErr == nil {
			_ = os.WriteFile(conf, previous, 0o644)
		} else {
			_ = os.Remove(conf)
		}
		return err
	}
	return nil
}

// BirdDownError reports that BIRD is not running: the new configuration was written (it reads it
// at its own start) but could not be loaded with `birdc configure`.
type BirdDownError struct{}

func (e *BirdDownError) Error() string { return "bird: not running" }

// BirdError is a configuration BIRD rejects, or a BIRD that cannot be reached; Message is BIRD's.
type BirdError struct{ Message string }

func (e *BirdError) Error() string { return "bird: " + e.Message }

func (e *Executor) readBird(ctx context.Context, instance string) (*BirdState, error) {
	conf, sock, err := e.birdPaths(instance)
	if err != nil {
		return nil, err
	}
	st := &BirdState{}
	if b, err := os.ReadFile(conf); err == nil {
		sum := sha256.Sum256(b)
		st.ConfigHash = hex.EncodeToString(sum[:])
		st.Config = string(b)
	}
	out, exit, err := e.birdOutput(ctx, ToolBirdc, "-s", sock, "show", "protocols", "all")
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		return st, nil // not running
	}
	protos, err := bird.ParseProtocols(out)
	if err != nil {
		return nil, err
	}
	st.Running, st.Protocols = true, protos
	return st, nil
}

// serviceNSState compares the namespace file of the service namespace with the network namespace of
// its holder: the same namespace has the same inode.
func (e *Executor) serviceNSState(name string, pid int) ServiceNSState {
	have, exists := e.inode("/run/netns/" + name)
	st := ServiceNSState{Exists: exists, HolderMatches: true, HolderExists: true}
	if pid > 0 {
		want, ok := e.inode("/proc/" + strconv.Itoa(pid) + "/ns/net")
		st.HolderExists = ok
		if exists {
			st.HolderMatches = ok && want == have
		}
	}
	return st
}

func realInode(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}
