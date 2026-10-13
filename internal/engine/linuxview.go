package engine

import (
	"context"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
)

// LinuxView is the kernel state of the applied target as text, read when asked (plan §2.17
// "Technical view": the compiled configuration with its live counters).
type LinuxView struct {
	Revision   int64
	Generation uint64
	At         time.Time
	apply.LinuxText
}

// LinuxView reads the kernel state Chaos Gateway owns through the executor (read operations only)
// and renders it: the nftables table, the tc trees, the bridges, policy rules and routes, and the
// WireGuard interfaces of the last applied target.
func (e *Engine) LinuxView(ctx context.Context) (*LinuxView, error) {
	snap := e.Snapshot()
	st, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.Want{TCDevs: snap.TCDevs})
	if err != nil {
		return nil, err
	}
	bridges := make([]string, 0, len(snap.Bridges))
	for _, b := range snap.Bridges {
		bridges = append(bridges, b.Name)
	}
	wg := make([]string, 0, len(snap.WireGuardInterfaces))
	for _, w := range snap.WireGuardInterfaces {
		wg = append(wg, w.Name)
	}
	return &LinuxView{Revision: snap.Revision, Generation: snap.Generation, At: e.cfg.Clock.Now(), LinuxText: apply.Render(st, bridges, wg)}, nil
}
