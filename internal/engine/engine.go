package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Andste82/chaos-gateway/internal/bird"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/supervisor"
)

// DefaultConfirmTimeout is the commit-confirm window when the configuration names none (plan §2.14).
const DefaultConfirmTimeout = 60 * time.Second

// Config wires an Engine.
type Config struct {
	Store *store.Store
	Exec  apply.Exec
	// Namespace is the network namespace the executor works in; empty for its own.
	Namespace string
	Clock     clock.Clock
	Log       *slog.Logger
	// Supervisor starts the goroutines; the engine makes one when nil.
	Supervisor *supervisor.Supervisor
	// Secrets is the store of WireGuard keys. The engine reads it to derive the public keys the
	// compiler needs; the executor reads the private keys itself.
	Secrets *secrets.Store
	// DHCP is the DHCP server (Kea); nil leaves DHCP out: nothing is configured, no leases are read.
	DHCP DHCP
}

// Snapshot is an immutable view of the engine. Nothing in a published snapshot is modified
// afterwards: readers never lock and never see a half-applied change (plan §3.11).
type Snapshot struct {
	// Generation counts the changes of the desired state: revisions, observed host changes and
	// restores. It bumps with each; an apply verifies the generation it compiled.
	Generation uint64
	// Revision is the active revision, 0 before the first one.
	Revision int64
	// Config is the active configuration; shared, never modified.
	Config *model.Configuration
	// Pending is the revision that waits for confirmation.
	Pending *PendingInfo
	// Host is the observed host the compiler works with.
	Host compiler.Host
	// Applied is the last apply that was verified.
	Applied *AppliedInfo
	// LastError is the error of the last failed apply, empty after a success.
	LastError string
	// Problems are the warnings of the last applied target (a degraded network).
	Problems []compiler.Problem
	// WireGuardInterfaces are the WireGuard networks of the last applied target (public data only).
	// Bridges are the bridges of the last applied target: one per local test network.
	Bridges             []compiler.Bridge
	WireGuardInterfaces []compiler.WGInterface
	// WireGuard is the state of every peer, by the id of the client (or of the link): the last
	// poll's result.
	WireGuard map[string]PeerStatus
	// Bird is set while the last applied target runs dynamic routing: the instance and its
	// configuration (public data: neighbors and prefixes, no secrets).
	Bird *compiler.BirdTarget
	// Routing is the state of every routing protocol by name: the last poll's result.
	Routing map[string]bird.ProtocolStatus
	// Devices are the configured and the discovered devices with their addresses and state (plan §2.3).
	Devices []DeviceState
	// Identity is which addresses belong to which device now.
	Identity domain.Identity
	// Leases are the DHCP leases of the last observation.
	Leases []model.DhcpLease
	// KeaNetworks maps a Kea subnet id to the UUID of its network (last applied target).
	KeaNetworks map[int]string
	// DHCPError is why the DHCP server does not run the applied configuration, empty when it does.
	DHCPError string
}

// PendingInfo describes a revision waiting for confirmation.
type PendingInfo struct {
	Revision int64
	Previous int64
	Deadline time.Time
}

// AppliedInfo describes the last verified apply.
type AppliedInfo struct {
	Generation uint64
	Revision   int64
	Hash       string
	At         time.Time
	Uplink     compiler.Uplink
}

// Engine is the state owner, the apply loop and their snapshot.
type Engine struct {
	cfg  Config
	snap atomic.Pointer[Snapshot]
	cmds chan command
	wake chan struct{}
	// desired is what the apply loop converges to; written by the state owner only.
	desired atomic.Pointer[desired]
	results chan applyResult
	events  *bus
	sup     *supervisor.Supervisor

	ctx             context.Context
	cancel          context.CancelFunc
	core            sync.WaitGroup // the state owner and the apply loop
	done            chan struct{}  // closed when both have returned
	started         bool
	polling         atomic.Bool
	pollingRouting  atomic.Bool
	pollingObserved atomic.Bool
	dhcp            dhcpState
	activeMu        sync.Mutex
	active          map[netip.Addr]bool
	// observeNow asks the observation poller to read at once (a lease event, a neighbor change).
	observeNow chan struct{}
}

// ErrClosed is returned by commands after Close.
var ErrClosed = errors.New("engine: closed")

// ErrApplyFailed is returned when applying a revision failed. The previous revision was restored
// (Restored) unless there was none.
type ErrApplyFailed struct {
	Revision int64
	Cause    error
	Restored bool
}

func (e *ErrApplyFailed) Error() string {
	return fmt.Sprintf("apply of revision %d failed: %v", e.Revision, e.Cause)
}
func (e *ErrApplyFailed) Unwrap() error { return e.Cause }

// New creates an Engine; Start runs it.
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil || cfg.Exec == nil {
		return nil, errors.New("engine: a store and an executor are required")
	}
	if cfg.Clock == nil {
		cfg.Clock = &clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	e := &Engine{cfg: cfg, cmds: make(chan command, 64), wake: make(chan struct{}, 1), results: make(chan applyResult, 8),
		events: newBus(), observeNow: make(chan struct{}, 1), sup: cfg.Supervisor, done: make(chan struct{})}
	if e.sup == nil {
		// a panic in the state owner or the apply loop stops the engine: the commands that wait
		// get ErrClosed, and the process (which watches Done) restarts and recompiles from the
		// committed revision (plan §3.11)
		e.sup = supervisor.New(cfg.Log, func(name string, v any) {
			cfg.Log.Error("the engine stops", "goroutine", name, "reason", fmt.Sprint(v))
			if e.cancel != nil {
				e.cancel()
			}
		})
	}
	e.snap.Store(&Snapshot{})
	return e, nil
}

// Snapshot returns the current snapshot. It is safe to call from any goroutine and never blocks.
func (e *Engine) Snapshot() *Snapshot { return e.snap.Load() }

// Emit publishes an event of the layer above the engine (the API: revision_created, ...); it goes
// to the same subscribers and the same replay buffer as the engine's own.
func (e *Engine) Emit(typ string, data map[string]any) {
	e.events.publish(e.cfg.Clock.Now(), typ, data)
}

// SubscribeFrom is Subscribe for a client that reconnects: the events after the sequence number
// last that are still buffered come first (Last-Event-ID, plan §2.15).
func (e *Engine) SubscribeFrom(last uint64) ([]Event, <-chan Event, func()) {
	return e.events.subscribeFrom(last)
}

// Subscribe returns a channel of events and a function that ends the subscription.
func (e *Engine) Subscribe() (<-chan Event, func()) { return e.events.subscribe() }

// Health returns the state of the engine's goroutines.
func (e *Engine) Health() []supervisor.Health { return e.sup.Health() }

// Start launches the state owner and the apply loop. A revision that was waiting for
// confirmation when the process stopped is rolled back: a restart inside the confirmation window
// boots the previous revision (plan §2.1.1). The active revision is applied.
func (e *Engine) Start(ctx context.Context) error {
	if e.started {
		return errors.New("engine: already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	init, err := e.initialState(ctx)
	if err != nil {
		cancel()
		return err
	}
	e.ctx, e.cancel, e.started = ctx, cancel, true
	e.core.Add(2)
	e.sup.Critical(ctx, "engine.owner", func(ctx context.Context) error { defer e.core.Done(); return e.runOwner(ctx, init) })
	e.sup.Critical(ctx, "engine.apply", func(ctx context.Context) error { defer e.core.Done(); return e.runApplyLoop(ctx) })
	if e.cfg.DHCP != nil {
		e.sup.Go(ctx, "engine.dhcp", e.runDHCPRetry)
	}
	go func() { e.core.Wait(); close(e.done) }()
	return nil
}

// Done is closed when the state owner and the apply loop have stopped: after Close, or because one
// of them panicked. A process that sees it restarts.
func (e *Engine) Done() <-chan struct{} { return e.done }

// Close stops the engine and waits for its core goroutines.
func (e *Engine) Close() {
	if !e.started {
		return
	}
	e.cancel()
	<-e.done
	e.sup.Wait()
}

func (e *Engine) initialState(ctx context.Context) (*ownerInit, error) {
	init := &ownerInit{}
	if p, ok := e.cfg.Store.PendingConfirm(); ok {
		if _, err := e.cfg.Store.Rollback(p.Revision, e.cfg.Clock.Now()); err != nil {
			return nil, fmt.Errorf("roll back the unconfirmed revision %d: %w", p.Revision, err)
		}
		e.events.publish(e.cfg.Clock.Now(), EventRolledBack, map[string]any{"revision": p.Revision, "reason": "restart"})
	}
	rev, cfg, err := e.cfg.Store.Active()
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		init.revision, init.config = rev.Id, cfg
	}
	host, err := apply.ReadHost(ctx, e.cfg.Exec, e.cfg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("read the host: %w", err)
	}
	init.host = host
	return init, nil
}

// send delivers a command to the state owner.
func (e *Engine) send(ctx context.Context, c command) error {
	select {
	case e.cmds <- c:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return ErrClosed
	}
}

func wait[T any](ctx context.Context, e *Engine, ch <-chan T) (T, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		var z T
		return z, ctx.Err()
	case <-e.done:
		var z T
		return z, ErrClosed
	}
}
