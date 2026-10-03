package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// OnlineWindow is how young a handshake has to be for a peer to count as online (plan §2.2.1).
const OnlineWindow = 3 * time.Minute

// Event types of WireGuard peers.
const (
	EventPeerOnline  = "wireguard_peer_online"
	EventPeerOffline = "wireguard_peer_offline"
)

// PeerStatus is the state of one client or link peer.
type PeerStatus struct {
	// NetworkID and Network name the WireGuard network, PeerID the client (or the link, whose
	// remote side is its only peer).
	NetworkID, Network string
	PeerID, Name       string
	Endpoint           string
	LastHandshake      time.Time
	RxBytes, TxBytes   int64
	// Online is true while the last handshake is younger than OnlineWindow. A peer that is not on
	// the interface (a disabled client) is offline.
	Online bool
}

// PollWireGuard reads the state of every peer of the applied WireGuard networks every interval and
// tells the state owner, which publishes it in the snapshot and emits an event when a peer goes
// online or offline. It runs until ctx or the engine ends.
func (e *Engine) PollWireGuard(ctx context.Context, interval time.Duration) error {
	if !e.started {
		return fmt.Errorf("engine: not started")
	}
	if !e.polling.CompareAndSwap(false, true) {
		return fmt.Errorf("engine: the WireGuard state is polled already")
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
	e.sup.Go(ctx, "engine.poll-wireguard", func(ctx context.Context) error {
		tick := e.cfg.Clock.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C():
			}
			if err := e.pollWireGuardOnce(ctx); err != nil && ctx.Err() == nil {
				e.cfg.Log.Warn("cannot read the WireGuard state", "error", err)
			}
		}
	})
	return nil
}

func (e *Engine) pollWireGuardOnce(ctx context.Context) error {
	snap := e.Snapshot()
	if len(snap.WireGuardInterfaces) == 0 {
		if len(snap.WireGuard) == 0 {
			return nil
		}
		return e.send(ctx, cmdWGStatus{status: map[string]PeerStatus{}})
	}
	var ops []executor.Operation
	for _, w := range snap.WireGuardInterfaces {
		ops = append(ops, &executor.Read{Target: executor.Target{NS: e.cfg.Namespace}, What: executor.ReadWireGuard, Dev: w.Name})
	}
	out, err := e.cfg.Exec.Do(ctx, ops...)
	if err != nil {
		return err
	}
	now := e.cfg.Clock.Now()
	status := map[string]PeerStatus{}
	for i, w := range snap.WireGuardInterfaces {
		var info linux.WGInfo
		if i >= len(out.Data) || json.Unmarshal(out.Data[i], &info) != nil {
			// an interface that cannot be read keeps the last known state of its peers: a
			// failed read is not a peer that went away
			for _, p := range w.Peers {
				if old, ok := snap.WireGuard[p.ID]; ok {
					status[p.ID] = old
				}
			}
			continue
		}
		byKey := map[string]linux.WGPeerInfo{}
		for _, p := range info.Peers {
			byKey[p.PublicKey] = p
		}
		for _, p := range w.Peers {
			st := PeerStatus{NetworkID: w.NetworkID, Network: w.NetworkName, PeerID: p.ID, Name: p.Name}
			if k, ok := byKey[p.PublicKey]; ok {
				st.Endpoint, st.RxBytes, st.TxBytes = k.Endpoint, k.RxBytes, k.TxBytes
				if k.LatestHandshake > 0 {
					st.LastHandshake = time.Unix(k.LatestHandshake, 0)
					st.Online = now.Sub(st.LastHandshake) < OnlineWindow
				}
			}
			status[p.ID] = st
		}
	}
	return e.send(ctx, cmdWGStatus{status: status})
}

// wireguardStatus takes a poll's result: peers that changed state emit an event; a peer that is
// gone from the interface (a disabled client) is offline.
func (o *owner) wireguardStatus(next map[string]PeerStatus) {
	prev := o.snap.WireGuard
	first := !o.wgSeen // the first poll announces nothing: the peers are not "new", they were there
	o.wgSeen = true
	ids := make([]string, 0, len(next)+len(prev))
	for id := range next {
		ids = append(ids, id)
	}
	for id := range prev {
		if _, ok := next[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		was, had := prev[id]
		now, has := next[id]
		wasOnline := had && was.Online
		isOnline := has && now.Online
		if wasOnline == isOnline || first {
			continue
		}
		st := now
		if !has {
			st = was
		}
		typ := EventPeerOffline
		if isOnline {
			typ = EventPeerOnline
		}
		o.event(typ, map[string]any{"network": st.Network, "network_id": st.NetworkID, "peer": st.Name, "peer_id": st.PeerID})
	}
	o.snap.WireGuard = next
	o.publish()
}
