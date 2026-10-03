package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// Event types of the routing protocols.
const (
	EventRoutingChanged       = "routing_session_changed"
	EventRoutingRoutesChanged = "routing_routes_changed"
)

// PollRouting reads the protocols of the BIRD instance every interval and tells the state owner,
// which publishes them in the snapshot and emits an event when an adjacency comes up or goes down.
// It runs until ctx or the engine ends.
func (e *Engine) PollRouting(ctx context.Context, interval time.Duration) error {
	if !e.started {
		return fmt.Errorf("engine: not started")
	}
	if !e.pollingRouting.CompareAndSwap(false, true) {
		return fmt.Errorf("engine: the routing state is polled already")
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	e.sup.Go(ctx, "engine.poll-routing", func(ctx context.Context) error {
		tick := e.cfg.Clock.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C():
			}
			if err := e.pollRoutingOnce(ctx); err != nil && ctx.Err() == nil {
				e.cfg.Log.Warn("cannot read the routing state", "error", err)
			}
		}
	})
	return nil
}

func (e *Engine) pollRoutingOnce(ctx context.Context) error {
	snap := e.Snapshot()
	if snap.Bird == nil {
		if len(snap.Routing) == 0 {
			return nil
		}
		return e.send(ctx, cmdRoutingStatus{status: map[string]bird.ProtocolStatus{}})
	}
	out, err := e.cfg.Exec.Do(ctx, &executor.Read{What: executor.ReadBird, Instance: snap.Bird.Instance})
	if err != nil {
		return err
	}
	var st executor.BirdState
	if len(out.Data) == 0 || json.Unmarshal(out.Data[0], &st) != nil {
		return fmt.Errorf("the BIRD state cannot be decoded")
	}
	status := map[string]bird.ProtocolStatus{}
	for _, p := range st.Protocols {
		switch p.Proto {
		case "BGP", "OSPF", "Babel":
			status[p.Name] = p
		}
	}
	return e.send(ctx, cmdRoutingStatus{status: status})
}

// routingStatus takes a poll's result and emits an event for every adjacency and route count that
// changed. It publishes the new snapshot only when something actually changed.
func (o *owner) routingStatus(next map[string]bird.ProtocolStatus) {
	prev := o.snap.Routing
	first := !o.routingSeen
	o.routingSeen = true
	if !first && reflect.DeepEqual(prev, next) {
		return
	}
	names := make([]string, 0, len(next)+len(prev))
	for n := range next {
		names = append(names, n)
	}
	for n := range prev {
		if _, ok := next[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		p, hadPrev := prev[n]
		cur, hasNext := next[n]
		wasUp, isUp := p.Established(), cur.Established()
		if wasUp != isUp && (!first || isUp) {
			state := "up"
			st := cur
			if !isUp {
				state = "down"
				if !hasNext {
					st = p
				}
			}
			o.event(EventRoutingChanged, map[string]any{"state": state, "protocol": n, "type": st.Proto, "neighbor": st.Neighbor, "info": st.Info, "last_error": st.LastError})
		}
		if !first && hadPrev && hasNext && (p.Imported != cur.Imported || p.Exported != cur.Exported) {
			o.event(EventRoutingRoutesChanged, map[string]any{
				"protocol": n, "type": cur.Proto,
				"imported": cur.Imported, "exported": cur.Exported, "filtered": cur.Filtered,
				"previous_imported": p.Imported, "previous_exported": p.Exported,
			})
		}
	}
	o.snap.Routing = next
	o.publish()
}
