package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// "Also cut existing connections" (plan §2.4, spike S3 case C6). The kernel keeps accepting an
// established connection after a drop rule arrived, and deleting its conntrack entry alone does
// not cut it behind NAT, so the engine does both, after the apply that brought the rule:
//
//  1. it reads the tracked connections and finds the ones a cutting rule decides now and did not
//     decide before (AccessPlan.Winner of the old and the new plan, on the original tuple);
//  2. when one of them is an established TCP connection it opens a window of half a second in which
//     `cut_forward`/`cut_input` (in front of the established accept) answer the packets of those rules
//     with a TCP reset, then closes it;
//  3. it deletes the entries it found, which is all that UDP, ICMP and idle TCP get: their next packet
//     is a new connection and meets the rule.
//
// The cut is a consequence of the apply, not part of it: a failure is logged and reported with the
// result, the rules hold either way. A rule that did not change is not cut again (the idempotence the
// per-connection comparison gives), and the first apply of a process cuts nothing: the engine does not
// know what the kernel ran before.

// DefaultCutWindow is how long the window of a cut stays open (spike S3, C6 used 0.5 s).
const DefaultCutWindow = 500 * time.Millisecond

// cutOutcome is what a cut did, for the result of the apply.
type cutOutcome struct {
	// Rules are the keys of the rules that cut, sorted.
	Rules []string
	// Connections is the number of tracked connections that were deleted.
	Connections int
	// Reset reports that a window of resets was opened.
	Reset bool
	// Err is why the cut did not complete; the rules are in force anyway.
	Err error
	// WindowOpen reports that the window of resets could not be closed even after the retries: the
	// cut chains still hold rules until the next full apply flushes them (the apply status says so).
	WindowOpen bool
}

func (c *cutOutcome) empty() bool { return c == nil || (len(c.Rules) == 0 && c.Err == nil) }

const (
	// CloseAttempts is how often closing the window is tried before the failure is reported.
	CloseAttempts = 3
	// CloseRetryDelay separates two attempts to close the window.
	CloseRetryDelay = 100 * time.Millisecond
)

// errWindowStaysOpen marks the failure to close the window after all attempts.
var errWindowStaysOpen = errors.New("the window of resets stays open")

// cutWindow is the configured length of the window.
func (e *Engine) cutWindow() time.Duration {
	switch w := e.cfg.CutWindow; {
	case w == 0:
		return DefaultCutWindow
	case w < 0:
		return 0
	default:
		return w
	}
}

// sameRules reports whether two plans have the same effective rules, in the same order, with the
// same sources: then there is nothing to cut that an earlier apply did not cut.
func sameRules(a, b *compiler.AccessPlan) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return reflect.DeepEqual(a.Rules, b.Rules) && reflect.DeepEqual(a.GlobalSources, b.GlobalSources) && reflect.DeepEqual(a.NonUplink, b.NonUplink)
}

// cutFlow is a tracked connection a cutting rule owns.
type cutFlow struct {
	flow executor.ConntrackFlow
	rule string
	// established TCP connections are the ones a window of resets reaches
	tcp bool
}

// cutExisting finds and cuts the connections that the rules of next decide and the rules of old did
// not. old is the target the kernel ran before; nil means unknown, and nothing is cut.
func (e *Engine) cutExisting(ctx context.Context, old, next *compiler.Target) *cutOutcome {
	if old == nil || next == nil || !next.Access.HasCuts() || sameRules(old.Access, next.Access) {
		return nil
	}
	ns := executor.Target{NS: e.cfg.Namespace}
	out, err := e.cfg.Exec.Do(ctx, &executor.Read{Target: ns, What: executor.ReadConntrack})
	if err != nil {
		return &cutOutcome{Err: fmt.Errorf("read the tracked connections: %w", err)}
	}
	var raw []linux.Conntrack
	if len(out.Data) > 0 {
		if err := json.Unmarshal(out.Data[0], &raw); err != nil {
			return &cutOutcome{Err: fmt.Errorf("read the tracked connections: %w", err)}
		}
	}
	flows := cutFlows(old, next, raw)
	if len(flows) == 0 {
		return nil
	}
	res := &cutOutcome{}
	keys := map[string]bool{}
	for _, f := range flows {
		keys[f.rule] = true
	}
	// only established TCP connections are reached by a reset
	resetKeys := map[string]bool{}
	for _, f := range flows {
		if f.tcp {
			resetKeys[f.rule] = true
		}
	}
	for k := range keys {
		res.Rules = append(res.Rules, k)
	}
	sort.Strings(res.Rules)

	if len(resetKeys) > 0 {
		var ks []string
		for k := range resetKeys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		if err := e.cutWindowRun(ctx, next, ns, ks); err != nil {
			res.Err = err
			res.WindowOpen = errors.Is(err, errWindowStaysOpen)
		} else {
			res.Reset = true
		}
	}
	for start := 0; start < len(flows); start += executor.MaxConntrackFlows {
		end := min(start+executor.MaxConntrackFlows, len(flows))
		batch := make([]executor.ConntrackFlow, 0, end-start)
		for _, f := range flows[start:end] {
			batch = append(batch, f.flow)
		}
		if _, err := e.cfg.Exec.Do(ctx, &executor.ConntrackDelete{Target: ns, Flows: batch}); err != nil {
			if res.Err == nil {
				res.Err = fmt.Errorf("delete the tracked connections: %w", err)
			}
			continue
		}
		res.Connections += len(batch)
	}
	return res
}

// cutWindowRun opens the window for the rules of keys, waits, and closes it. The window is closed
// even when the wait is interrupted or the opening failed half-way: an open window resets
// established connections, which must never outlive the cut.
func (e *Engine) cutWindowRun(ctx context.Context, t *compiler.Target, ns executor.Target, keys []string) error {
	open, err := t.Access.CutTransaction(keys)
	if err != nil {
		return err
	}
	shut, err := t.Access.CutTransaction(nil)
	if err != nil {
		return err
	}
	closeWindow := func() error { return e.closeCutWindow(ctx, shut, ns) }
	if _, err := e.cfg.Exec.Do(ctx, &executor.NftApply{Target: ns, Ruleset: open}); err != nil {
		if cerr := closeWindow(); cerr != nil {
			return fmt.Errorf("open the cut window: %w; %w", err, cerr)
		}
		return fmt.Errorf("open the cut window: %w", err)
	}
	if w := e.cutWindow(); w > 0 {
		timer := e.cfg.Clock.NewTimer(w)
		select {
		case <-timer.C():
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err := closeWindow(); err != nil {
		// the window stays open until the next apply flushes the chains: say so loudly
		e.cfg.Log.Error("the cut window could not be closed: established connections of the cutting rules are reset until the next apply", "error", err)
		return fmt.Errorf("close the cut window: %w", err)
	}
	return nil
}

// closeCutWindow empties the cut chains. It is retried: a window that stays open resets every
// established connection of the cutting rules' selectors until the next full apply, and no overlay or
// configuration owns that effect. A failure that lasts is reported as errWindowStaysOpen.
func (e *Engine) closeCutWindow(ctx context.Context, shut []byte, ns executor.Target) error {
	var err error
	for attempt := 1; attempt <= CloseAttempts; attempt++ {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		_, err = e.cfg.Exec.Do(cctx, &executor.NftApply{Target: ns, Ruleset: shut})
		cancel()
		if err == nil {
			return nil
		}
		if attempt < CloseAttempts {
			e.cfg.Log.Warn("the cut window could not be closed, trying again", "attempt", attempt, "error", err)
			<-e.cfg.Clock.NewTimer(CloseRetryDelay).C()
		}
	}
	return fmt.Errorf("%w: %w", errWindowStaysOpen, err)
}

// closeStuckWindow is the retry of a window that stayed open, run after an apply that did not flush
// the chains (an incremental identity update).
func (e *Engine) closeStuckWindow(ctx context.Context, t *compiler.Target) error {
	shut, err := t.Access.CutTransaction(nil)
	if err != nil {
		return err
	}
	return e.closeCutWindow(ctx, shut, executor.Target{NS: e.cfg.Namespace})
}

// cutFlows lists the tracked connections that a cutting rule of next decides and the rules of old did
// not decide in the same way, in a stable order. Connections of the control plane (the anti-lockout
// rule's sources and ports), connections the gateway opened and traffic switched inside one bridge
// are never among them, whatever a rule says.
func cutFlows(old, next *compiler.Target, raw []linux.Conntrack) []cutFlow {
	var out []cutFlow
	for _, c := range raw {
		src, err1 := netip.ParseAddr(c.Original.Src)
		dst, err2 := netip.ParseAddr(c.Original.Dst)
		if err1 != nil || err2 != nil || !src.Is4() || !dst.Is4() {
			continue
		}
		var flow executor.ConntrackFlow
		t := compiler.Tuple{Src: src, Dst: dst, Proto: c.Proto}
		switch c.Proto {
		case "tcp", "udp":
			t.Port = c.Original.DPort
			flow = executor.ConntrackFlow{Proto: c.Proto, Src: c.Original.Src, Dst: c.Original.Dst, SPort: c.Original.SPort, DPort: c.Original.DPort}
			if t.Port == 0 || flow.SPort == 0 {
				continue
			}
		case "icmp":
			if c.Original.ICMPType == nil || c.Original.ICMPCode == nil || c.Original.ICMPID == nil {
				continue
			}
			flow = executor.ConntrackFlow{Proto: "icmp", Src: c.Original.Src, Dst: c.Original.Dst,
				ICMPType: c.Original.ICMPType, ICMPCode: c.Original.ICMPCode, ICMPID: c.Original.ICMPID}
		default:
			continue // a protocol the operation does not name: not cut
		}
		if c.Proto == "tcp" && (c.State == "TIME_WAIT" || c.State == "CLOSE" || c.State == "LAST_ACK") {
			continue // the connection is over
		}
		if next.Access.NotJudged(t) {
			continue // a connection the gateway opened, or one switched inside a bridge: no rule judges it
		}
		w := next.Access.Winner(t)
		if w == nil || !w.Cuts() {
			continue
		}
		if o := old.Access.Winner(t); o != nil && o.Key == w.Key && o.Action == w.Action && o.Cuts() {
			continue // this rule has cut it already
		}
		if controlPlane(next, t) {
			continue
		}
		out = append(out, cutFlow{flow: flow, rule: w.Key, tcp: c.Proto == "tcp" && c.State == "ESTABLISHED"})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].flow, out[j].flow
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		if a.SPort != b.SPort {
			return a.SPort < b.SPort
		}
		return a.DPort < b.DPort
	})
	return out
}

// controlPlane reports whether a tuple is traffic the anti-lockout rule protects: a management source
// towards SSH or the UI port.
func controlPlane(t *compiler.Target, tp compiler.Tuple) bool {
	if tp.Proto != "tcp" || (tp.Port != 22 && tp.Port != t.Management.UIPort) {
		return false
	}
	for _, p := range t.Management.Sources {
		if p.Contains(tp.Src) {
			return true
		}
	}
	return false
}
