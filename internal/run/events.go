package run

import (
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/engine"
)

// recordWait is how long the end of a run may take to reach its event file.
const recordWait = 5 * time.Second

// startRecorder follows the event bus and writes the events of every run, the ones the manager emits for it
// and the ones the engine emits for its overlays (they carry the run's id in `run`), to the run's event
// file. A recorder that falls behind is dropped by the bus, as any subscriber is (plan §3.11): it subscribes
// again and the events in between are lost; the log says so.
func (m *Manager) startRecorder() {
	ch, unsub := m.cfg.Gateway.Subscribe()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() { unsub() }()
		for {
			select {
			case <-m.ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					m.log.Warn("the event recorder of the runs fell behind: events of runs may be missing from their event files")
					ch, unsub = m.cfg.Gateway.Subscribe()
					continue
				}
				m.record(ev)
			}
		}
	}()
}

// record appends an event to the file of the run it belongs to.
func (m *Manager) record(ev engine.Event) {
	id, ok := ev.Data["run"].(string)
	if !ok {
		return
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return
	}
	m.mu.Lock()
	r := m.runs[u]
	m.mu.Unlock()
	if r == nil {
		return
	}
	if line, ok := m.cfg.EncodeEvent(ev); ok {
		if err := appendLine(m.cfg.Dir, u, line); err != nil {
			m.log.Error("cannot write an event of a run", "run", u, "error", err)
		}
	}
	if ev.Type == engine.EventRunFinished {
		r.recordedOnce.Do(func() { close(r.recorded) })
	}
}

// Events returns the lines of the event file of a run: its own events and those of its overlays, as
// JSON.
func (m *Manager) Events(id uuid.UUID) ([][]byte, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	over := ok && ended(r.rec.Status)
	m.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	if over {
		m.awaitRecorded(r)
	}
	return readEvents(m.cfg.Dir, id)
}

// eventFileSize is the size of the event file of a run.
func (m *Manager) eventFileSize(id uuid.UUID) int64 { return eventsSize(m.cfg.Dir, id) }

// awaitRecorded waits until the end of a run that has ended is in its event file: the recorder writes it a moment
// after the record says the run has ended.
func (m *Manager) awaitRecorded(r *runState) {
	select {
	case <-r.recorded:
	case <-m.clk.After(recordWait):
	}
}
