package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Queue statistics and their epochs (plan §2.12: "per netem queue: packets dropped and delayed ... a
// queue that is re-created starts a new counter epoch").
//
// The statistics of a queue are the counters of the netem leaf below the class of one (fault id,
// direction) on one interface: what the kernel counts for the leaf, read when asked (the API reads
// them when it shows an overlay or a fault). They live as long as the leaf: a change in place
// (`replace` of the same kind) keeps them, a deletion and a new leaf starts them at zero. The epoch
// of a queue is the generation of the apply that made its leaf, so a consumer that sees the same
// epoch twice may subtract the readings, and one that sees another discards the difference.

// QueueKey identifies a queue: the interface and the class of the (fault id, direction).
func QueueKey(dev, class string) string { return dev + " " + class }

// ReadQueues reads the counters of the netem leaves of the applied target's tc tree: one entry per
// QueueKey. Nothing is read (and nothing is returned) when the applied target has no tree. The read
// is of the live kernel, so it also shows what a leaf holds that the last apply has not changed.
func (e *Engine) ReadQueues(ctx context.Context) (map[string]linux.NormStats, error) {
	devs := e.Snapshot().TCDevs
	out := map[string]linux.NormStats{}
	if len(devs) == 0 {
		return out, nil
	}
	ops := make([]executor.Operation, len(devs))
	for i, d := range devs {
		ops[i] = &executor.Read{Target: executor.Target{NS: e.cfg.Namespace}, What: executor.ReadTC, Dev: d}
	}
	res, err := e.cfg.Exec.Do(ctx, ops...)
	if err != nil {
		return nil, fmt.Errorf("read the queues: %w", err)
	}
	for i, d := range devs {
		if i >= len(res.Data) {
			break
		}
		var t *linux.NormTree
		if err := json.Unmarshal(res.Data[i], &t); err != nil {
			return nil, fmt.Errorf("read the queues of %s: %w", d, err)
		}
		if t == nil {
			continue
		}
		for _, q := range t.Subtree(compiler.TCRootHandle).Qdiscs {
			if q.Netem != nil && q.Stats != nil {
				out[QueueKey(d, q.Parent)] = *q.Stats
			}
		}
	}
	return out, nil
}

// trackQueues keeps the epochs of the queues of the applied target. A leaf the apply made new (or
// one the engine has not seen before: the gateway restarted, or the kernel was not what the last
// apply left) starts an epoch in this generation; a leaf that stays keeps its epoch. After an apply
// that failed nothing is known about what it did to the leaves, so every queue gets a new epoch.
func (o *owner) trackQueues(t *compiler.Target, plan *apply.Plan, gen uint64, failed bool) {
	made := map[string]bool{}
	if plan != nil {
		for _, k := range plan.QueuesCreated {
			made[k] = true
		}
	}
	old := o.ov.queueBorn
	next := map[string]int64{}
	if failed {
		for k := range old {
			next[k] = int64(gen)
		}
		o.ov.queueBorn = next
		o.snap.QueueEpochs = next
		return
	}
	var devs []string
	for _, tr := range t.TCTrees() {
		devs = append(devs, tr.Devs...)
		for _, c := range tr.Classes {
			for _, d := range tr.Devs {
				k := QueueKey(d, c.ClassID())
				if g, ok := old[k]; ok && !made[k] {
					next[k] = g
				} else {
					next[k] = int64(gen)
				}
			}
		}
	}
	sort.Strings(devs)
	o.ov.queueBorn = next
	o.snap.QueueEpochs = next
	o.snap.TCDevs = devs
}

// trackCounters follows the epoch of the nft counters of the whole table (the state's
// counter_epoch): it changes when the table was made anew by an apply, and with the first apply of
// the engine, because a restarted gateway cannot tell whether the counters it finds are the ones it
// left. Every fault's own epoch restarts with it.
func (o *owner) trackCounters(plan *apply.Plan, gen uint64) {
	if o.counterEpoch != 0 && (plan == nil || !plan.NftNew) {
		return
	}
	o.counterEpoch = int64(gen)
	o.snap.CounterEpoch = o.counterEpoch
	// a new map: the old one is the Snapshot.FaultEpochs that readers of earlier snapshots hold, and a
	// published map is never written to
	born := make(map[string]int64, len(o.ov.born))
	for k := range o.ov.born {
		born[k] = int64(gen)
	}
	o.ov.born = born
	o.snap.FaultEpochs = born
	// the same for the counters of the access rules
	rborn := make(map[string]int64, len(o.ov.ruleBorn))
	for k := range o.ov.ruleBorn {
		rborn[k] = int64(gen)
	}
	o.ov.ruleBorn = rborn
	if o.snap.RuleEpochs != nil {
		o.snap.RuleEpochs = rborn
	}
}
