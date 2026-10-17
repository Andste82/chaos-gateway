package engine

import (
	"context"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// DefaultUplinkWait is how long an apply waits for the uplink's IPv4 address when the host has none
// at the moment (issue #64): `netplan apply` or a Wi-Fi reassociation takes it away for a few seconds.
const DefaultUplinkWait = 10 * time.Second

// announceHostChange wakes everyone who waits for the next change of the observed host.
func (e *Engine) announceHostChange() {
	e.hostMu.Lock()
	close(e.hostChanged)
	e.hostChanged = make(chan struct{})
	e.hostMu.Unlock()
}

// nextHostChange is closed at the next change of the observed host.
func (e *Engine) nextHostChange() <-chan struct{} {
	e.hostMu.Lock()
	defer e.hostMu.Unlock()
	return e.hostChanged
}

// PreviewAwaitingUplink is Preview for an apply: while the candidate does not compile only because the
// uplink has no IPv4 address yet, it waits for the observer (FollowHost) to report a change of the host
// and compiles again, up to max. After max the preview with the problem is returned, and the apply
// fails as before. A transient loss of the address (`netplan apply`, a Wi-Fi reassociation) then no
// longer fails an apply that comes a moment too early (issue #64).
func (e *Engine) PreviewAwaitingUplink(ctx context.Context, rev int64, max time.Duration) (*Preview, error) {
	var timer clock.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		// taken before the compile: a change between the compile and the wait is not missed
		changed := e.nextHostChange()
		p, err := e.Preview(ctx, rev)
		if err != nil || max <= 0 || !onlyTheUplinkAddressMissing(p.Problems) {
			return p, err
		}
		if timer == nil {
			timer = e.cfg.Clock.NewTimer(max)
			e.cfg.Log.Info("the uplink has no IPv4 address: the apply waits for it", "revision", rev, "max", max)
		}
		select {
		case <-changed:
		case <-timer.C():
			return p, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.done:
			return nil, ErrClosed
		}
	}
}

// onlyTheUplinkAddressMissing reports whether the uplink's missing address is the target's only error:
// waiting for it makes no sense when the candidate fails anyway.
func onlyTheUplinkAddressMissing(ps []compiler.Problem) bool {
	found := false
	for _, p := range ps {
		if p.Severity != compiler.SevError {
			continue
		}
		if p.Code != compiler.CodeUplinkNoAddress {
			return false
		}
		found = true
	}
	return found
}
