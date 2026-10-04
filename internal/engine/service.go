package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// WatchService looks at the service namespace every interval and applies again when it is not what
// the last apply made: its holder restarted (the old namespace stays alive under its name, no netlink
// event tells), the holder came up after the first apply, or the namespace is gone. It does nothing
// without a service namespace in the configuration of the engine.
func (e *Engine) WatchService(ctx context.Context, interval time.Duration) {
	if e.cfg.ServiceNS == "" {
		return
	}
	// the watcher belongs to the engine: it ends with it, and with the caller's context
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	e.sup.Go(ctx, "engine.service-watch", func(ctx context.Context) error {
		tick := e.cfg.Clock.NewTicker(interval)
		defer tick.Stop()
		var lastPID int
		var lastBad time.Time
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C():
			}
			pid := 0
			if e.cfg.ServiceHolderPID != nil {
				pid = e.cfg.ServiceHolderPID()
			}
			if e.Snapshot().Service == nil {
				lastPID = pid
				continue // nothing applied yet: the first apply reads it
			}
			out, err := e.cfg.Exec.Do(ctx, &executor.Read{What: executor.ReadServiceNS, Service: e.cfg.ServiceNS, PID: pid})
			if err != nil || len(out.Data) == 0 {
				continue
			}
			var st executor.ServiceNSState
			if json.Unmarshal(out.Data[0], &st) != nil {
				continue
			}
			_ = e.send(ctx, cmdServiceStatus{exists: st.Exists, holderMatches: st.HolderMatches, holderExists: st.HolderExists})
			bad := !st.Exists || !st.HolderMatches
			now := e.cfg.Clock.Now()
			// a change of the PID, or a namespace that is wrong, applies again at once; a namespace that
			// stays wrong (an apply that cannot fix it) is tried again every 30 s, not every tick
			if pid != lastPID || (bad && now.Sub(lastBad) > 30*time.Second) {
				lastPID = pid
				if bad || pid != 0 {
					if bad {
						lastBad = now
					}
					_ = e.send(ctx, cmdResync{})
				}
			}
		}
	})
}
