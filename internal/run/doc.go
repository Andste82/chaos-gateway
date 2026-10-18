// Package run is the engine of scenario runs (plan §2.10, M15): it takes a scenario, checks it
// against the active revision, queues it behind the runs whose targets overlap, executes its timeline
// on the injected monotonic clock and writes the report.
//
// A run is a goroutine of its own that sends commands to the state owner through the engine
// (internal/engine): every step that creates or replaces an overlay is a write of an overlay owned by
// the run (the overlay store keeps the key, so a step with the key of an earlier one replaces it),
// `remove` deletes one step's overlay and `restore` resets the run's owner. A run never writes a
// revision. When it ends, for whatever reason, the last thing it does is to reset its owner, so no
// overlay of a run outlives it; a run that ends because its lease ran out is no different.
//
// The state of a run is kept in memory and in a directory of its own (`<dir>/<id>/run.json`, the
// events as JSON lines next to it), so the report of a run outlives the process; a run that was queued
// or running when the process ended is `aborted` with the reason `service restart` when the next process
// starts (Recover). Time is the monotonic clock of the engine: leases and the schedule never jump with the
// wall clock, which is only displayed.
//
// The manager holds one mutex for the table of runs and the records in it; nothing is called out of
// the package while it is held.
package run
