package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// StepLateThreshold is how long the change of a step may wait for the executor before the run gets
// the warning `step_late` (plan §2.10, §3.11).
const StepLateThreshold = 100 * time.Millisecond

// DefaultRetention is how long the record of an ended run is kept (plan §3.6).
const DefaultRetention = 30 * 24 * time.Hour

// DefaultMaxRuns bounds the runs that are queued or running at the same time. A run is a goroutine and
// a directory; the limit only keeps a runaway client from filling the memory.
const DefaultMaxRuns = 200

// waitPoll is how often a `wait` step looks at what the gateway sees.
const waitPoll = 250 * time.Millisecond

// The step types and check types this build runs (GET /capabilities). The others arrive with the
// milestones of Unsupported.
var (
	SupportedStepTypes  = []string{"profile", "fault", "rule", "wireguard", "capture", "wait", "remove", "restore"}
	SupportedCheckTypes = []string{"reconnected", "no_connection", "traffic_seen", "traffic_not_seen", "dns_query_seen"}
)

var (
	// ErrNotFound is returned for a run that does not exist (any more: the record of an ended run is kept
	// for the retention time).
	ErrNotFound = errors.New("run: not found")
	// ErrNoScenario is returned (wrapped, naming the scenario) for a run of a scenario the active revision does not have.
	ErrNoScenario = errors.New("run: no such scenario")
	// ErrForbidden is returned when a caller that may only touch its own runs names one of another owner.
	ErrForbidden = errors.New("run: the run belongs to another owner")
	// ErrNoLease is returned when renewing a run that has no lease.
	ErrNoLease = errors.New("run: has no lease to renew")
	// ErrEnded is returned (wrapped, with the reason, for a run that is being aborted) when renewing a run that has ended.
	ErrEnded = errors.New("run: has ended")
	// ErrNotEnded is returned for the report of a run that has not ended.
	ErrNotEnded = errors.New("run: has not ended yet")
	// ErrTooManyRuns is returned when DefaultMaxRuns (Config.MaxRuns) runs are queued or running.
	ErrTooManyRuns = errors.New("run: too many queued or running runs")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("run: the manager is closed")
)

// TargetBusyError is returned by Create with Queue false when the target of the run overlaps one of a
// run that is queued or running (`409 target_busy`).
type TargetBusyError struct {
	// Run is the run that holds the target and Scenario its scenario's name.
	Run      uuid.UUID
	Scenario string
}

func (e *TargetBusyError) Error() string {
	return fmt.Sprintf("the target is busy: run %s (%s) is queued or running on it", e.Run, e.Scenario)
}

// CleanupError is returned by Abort for a run that has ended but whose overlays could not be removed: the run is
// recorded (Run), and the manager keeps trying to remove the overlays.
type CleanupError struct {
	Run model.Run
	Err error
}

func (e *CleanupError) Error() string {
	return fmt.Sprintf("run %s ended as %s, but its overlays could not be removed (the gateway keeps trying): %v", e.Run.Id, e.Run.Status, e.Err)
}
func (e *CleanupError) Unwrap() error { return e.Err }

// UnsupportedError is returned by Create for a scenario that needs a milestone this build does not have
// (`422 unsupported_feature`).
type UnsupportedError struct {
	// What names the step type, check or setting ("a DNS fault step", "checks").
	What string
	// Milestone is the milestone that brings it.
	Milestone string
}

func (e *UnsupportedError) Error() string {
	if e.Milestone == "" {
		return fmt.Sprintf("a scenario with %s cannot run in this deployment", e.What)
	}
	return fmt.Sprintf("a scenario with %s cannot run in this build; it arrives with milestone %s (see GET /capabilities)", e.What, e.Milestone)
}

// Gateway is what the runs need of the engine: the overlays an owner writes, the snapshot to check a
// scenario against, and the event bus. *engine.Engine is one.
type Gateway interface {
	Snapshot() *engine.Snapshot
	PutOverlay(ctx context.Context, w engine.OverlayWrite) (engine.OverlayResult, error)
	DeleteOverlay(ctx context.Context, id uuid.UUID, by *model.Owner, actor model.Actor) (engine.OverlayResult, error)
	ResetOverlays(ctx context.Context, owner *model.Owner, actor model.Actor) (engine.OverlayResult, error)
	Emit(typ string, data map[string]any)
	Subscribe() (<-chan engine.Event, func())
}

// DNSQuery is a query of a device that the gateway's DNS proxy logged.
type DNSQuery struct {
	// Device is the device's UUID, Name the name asked and Type the record type ("A").
	Device, Name, Type string
	Time               time.Time
}

// Observer is what the runs read of what the gateway sees besides the snapshot: the connections for
// `wait` steps and checks, the DNS log for the baseline, for `dns_query` waits and for `dns_query_seen`
// checks, and the counters of overlays for the report and the zero-hit warnings.
type Observer interface {
	// Flows returns the tracked connections of the test networks.
	Flows(ctx context.Context) ([]engine.Flow, error)
	// DNSQuerySeen reports whether one of the devices (UUIDs) asked the gateway's DNS proxy for the
	// name at or after the time. A name may be `*.suffix`.
	DNSQuerySeen(devices []string, name string, since time.Time) bool
	// DNSQueries returns the queries for the name (exact, or `*.suffix`) that one of the devices (UUIDs)
	// asked the gateway's DNS proxy at or after the time, oldest first.
	DNSQueries(devices []string, name string, since time.Time) []DNSQuery
	// DNSClientSeen reports whether the device has asked the gateway's DNS proxy for anything.
	DNSClientSeen(device string) bool
	// Counters returns the counters of the overlays (their kernel counters). An overlay that is in the
	// kernel's view but has nothing in it (a rule that is not in the packet path, a fault that another one
	// overrides) counts zero packets; one whose counters could not be read is left out.
	Counters(ctx context.Context, overlays []uuid.UUID) map[uuid.UUID]*model.Counter
}

// Captures is what the runs need of the packet captures (M17): a capture attached to a run starts with a scope
// of the run, ends with the reason `run_end` or `user`, and is kept as long as the record of the run.
// A capture that cannot be made never ends a run: the run says so with the warning `capture_missing`.
type Captures interface {
	// Start starts a capture of the scope, named name and attached to the run.
	Start(ctx context.Context, scope model.CaptureScope, name string, run uuid.UUID) (model.Capture, error)
	// Stop ends a capture for the reason and returns it once its files are closed; one that has ended is returned as
	// it is.
	Stop(ctx context.Context, id uuid.UUID, reason model.CaptureStopReason) (model.Capture, error)
	// Get returns a capture; OfRun the captures attached to a run, newest first.
	Get(id uuid.UUID) (model.Capture, error)
	OfRun(run uuid.UUID) []model.Capture
	// DeleteRun deletes the captures of a run whose record is deleted.
	DeleteRun(run uuid.UUID)
}

// Config configures a Manager.
type Config struct {
	// Clock is the time source: monotonic for the schedule and the leases. Required.
	Clock clock.Clock
	// Dir holds the runs, one directory each. Required.
	Dir string
	// Gateway and Observer are the engine and what it sees. Required.
	Gateway  Gateway
	Observer Observer
	// Captures records the traffic of a run (`capture: true`, the `capture` steps); nil for a build that cannot.
	Captures Captures
	Log      *slog.Logger
	// Retention is how long the record of an ended run is kept; DefaultRetention when zero.
	Retention time.Duration
	// MaxRuns limits the runs that are queued or running; DefaultMaxRuns when zero.
	MaxRuns int
	// EncodeEvent turns an engine event into the line of a run's event file (the API's Event); false
	// leaves the event out. The default writes the type, time and data.
	EncodeEvent func(engine.Event) ([]byte, bool)
	// NewID creates the id of a run; a random UUID by default.
	NewID func() uuid.UUID
}

// CreateRequest is a run to make.
type CreateRequest struct {
	// Scenario names a stored scenario of the active revision (name or UUID); Inline is a scenario
	// sent with the request. Exactly one is set.
	Scenario string
	Inline   *model.Scenario
	// Parameters change the scenario by dotted path before it is checked (RunOptions.parameters).
	Parameters map[string]string
	// Lease is the interval within which the client must renew the run; zero for none.
	Lease time.Duration
	// Queue lets the run wait behind runs of overlapping targets; false refuses it then.
	Queue bool
	// Owner is who starts the run (a token, or the admin user).
	Owner model.Actor
}

// Filter selects runs for List.
type Filter struct {
	Status   model.RunStatus
	Scenario string
}

// Abort is what an abort came to.
type Abort struct {
	Run model.Run
	// Generation is the generation that removed the run's overlays, zero when it had none.
	Generation uint64
}
