package run

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Checks (plan §2.10, M16) evaluate what the gateway observes of the scenario's target inside a window
// that starts when a named step has been applied. They run in a goroutine of their own next to the timeline
// (checkRun), which polls the connections and the DNS log, so the timeline's steps are never late because of
// a check. A positive check (a connection was established, traffic was seen, a name was asked) passes as
// soon as it is observed and fails when its window closes without; a negative check (no connection, no
// traffic) fails as soon as it is observed and passes when its window closes. A run lasts until its last
// check is decided.

// checkPoll is how often an open window looks at the connections and the DNS log. An observation is placed
// into the window by the poll that sees it, so it is exact to this interval.
const checkPoll = waitPoll

// maxEvidence bounds the observations that a check keeps as its evidence.
const maxEvidence = 10

// liveCheck is a check while the run lasts.
type liveCheck struct {
	idx    int
	kind   string
	what   string // the selector as a person reads it, for the messages
	match  model.TrafficMatch
	except []model.Destination
	dns    string
	from   string
	within time.Duration
	until  string // "" for a window of `within`

	opened bool // the step the window starts at has been reached
	armed  bool // the baseline poll of the window is behind: the polls from now on belong to the window
	ended  bool // the end of the window is known
	// the window on the timeline (offsets from its start) and on the wall clock
	wstart, wend         time.Duration
	wstartWall, wendWall time.Time
	decided              bool
	evidence             []map[string]any
}

func newLiveCheck(cfg *model.Configuration, idx int, c *model.Check) *liveCheck {
	lc := &liveCheck{idx: idx, kind: domain.CheckKind(c), from: c.Window.From, until: deref(c.Window.Until)}
	if c.Window.Within != nil {
		lc.within, _ = time.ParseDuration(*c.Window.Within)
	}
	switch lc.kind {
	case "reconnected":
		lc.match = *c.Reconnected
	case "no_connection":
		lc.match = *c.NoConnection
	case "traffic_seen":
		lc.match = *c.TrafficSeen
	case "traffic_not_seen":
		n := c.TrafficNotSeen
		lc.match = model.TrafficMatch{Destination: n.Destination, PortRanges: n.PortRanges, Ports: n.Ports, Protocol: n.Protocol}
		lc.except = deref(n.Except)
	case "dns_query_seen":
		lc.dns = c.DnsQuerySeen.Name
	}
	if lc.kind == "dns_query_seen" {
		lc.what = "a query for " + lc.dns
	} else {
		lc.what = describeMatch(cfg, lc.match)
	}
	return lc
}

// usesFlows reports whether the check looks at the connections (the others at the DNS log).
func (c *liveCheck) usesFlows() bool { return c.kind != "dns_query_seen" }

// ---- names

// checkName is the name of a check in the report: the one the scenario gives it, else one derived from its
// type, its selector and its window.
func checkName(cfg *model.Configuration, c *model.Check) string {
	if c.Name != nil && *c.Name != "" {
		return *c.Name
	}
	lc := newLiveCheck(cfg, 0, c)
	what := lc.dns
	switch lc.kind {
	case "dns_query_seen":
	case "traffic_not_seen":
		what = lc.what
		if len(lc.except) > 0 {
			var ex []string
			for i := range lc.except {
				ex = append(ex, describeDestination(cfg, &lc.except[i]))
			}
			what += " except " + strings.Join(ex, ", ")
		}
	default:
		what = lc.what
	}
	return fmt.Sprintf("%s %s, %s", lc.kind, what, describeWindow(c.Window))
}

func describeWindow(w model.CheckWindow) string {
	if w.Until != nil {
		return "from " + w.From + " until " + *w.Until
	}
	return "from " + w.From + " within " + deref(w.Within)
}

// describeMatch names a selector: "tcp/8883", "udp/5353-5360 to 203.0.113.0/24", "any".
func describeMatch(cfg *model.Configuration, m model.TrafficMatch) string {
	p := string(deref(m.Protocol))
	if p == "" {
		p = "any"
	}
	var ports []string
	for _, port := range deref(m.Ports) {
		ports = append(ports, strconv.Itoa(port))
	}
	for _, r := range deref(m.PortRanges) {
		ports = append(ports, fmt.Sprintf("%d-%d", r.From, r.To))
	}
	out := p
	if len(ports) > 0 {
		out += "/" + strings.Join(ports, ",")
	}
	if m.Destination != nil {
		out += " to " + describeDestination(cfg, m.Destination)
	}
	return out
}

func describeDestination(cfg *model.Configuration, d *model.Destination) string {
	switch {
	case d.Cidr != nil:
		return *d.Cidr
	case d.Hostname != nil:
		return *d.Hostname
	case d.Uplink != nil:
		return "the uplink"
	case d.Network != nil:
		if cfg != nil && cfg.Networks != nil {
			for id, n := range *cfg.Networks {
				if strings.EqualFold(id, *d.Network) {
					if name := networkName(n); name != "" {
						return "network " + name
					}
				}
			}
		}
		return "network " + *d.Network
	}
	return "any"
}

// ---- the connections

// flowTracker remembers the connections of the last poll, to tell what changed between two polls.
type flowTracker struct{ seen map[string]flowMemo }

type flowMemo struct {
	established bool
	packets     int64
}

// flowEvent is what a poll found out about a connection since the poll before it.
type flowEvent struct {
	flow engine.Flow
	// created: the connection was not there at the poll before (or its counters started over);
	// established: it is established now and was not before; traffic: it carried packets since.
	created, established, traffic bool
}

func flowKey(f engine.Flow) string {
	if f.StartedAt.IsZero() {
		return f.ID
	}
	return f.ID + "@" + strconv.FormatInt(f.StartedAt.UnixNano(), 10)
}

// update records the connections of a poll and returns what changed since the poll before.
func (t *flowTracker) update(flows []engine.Flow) []flowEvent {
	next := make(map[string]flowMemo, len(flows))
	var out []flowEvent
	for _, f := range flows {
		key := flowKey(f)
		est := established(f)
		packets := f.Upload.Packets + f.Download.Packets
		next[key] = flowMemo{established: est, packets: packets}
		old, known := t.seen[key]
		ev := flowEvent{flow: f}
		switch {
		case !known || packets < old.packets: // new, or the same tuple again after the entry went
			ev.created, ev.traffic, ev.established = true, true, est
		default:
			ev.established = est && !old.established
			ev.traffic = packets > old.packets
		}
		if ev.created || ev.established || ev.traffic {
			out = append(out, ev)
		}
	}
	t.seen = next
	return out
}

// observes reports whether a connection event is one the check looks for: of the scenario's target, selected by
// the check's selector, and of the kind that decides it (a connection that has been established for the two
// connection checks, any packet for the two traffic checks).
func (c *liveCheck) observes(snap *engine.Snapshot, target model.Scope, devices map[string]bool, ev flowEvent) bool {
	if !flowInScope(devices, target, ev.flow) || !matchFlow(snap, c.match, ev.flow) {
		return false
	}
	switch c.kind {
	case "reconnected", "no_connection":
		return ev.established
	case "traffic_seen":
		return true
	case "traffic_not_seen":
		for _, d := range c.except {
			if matchFlow(snap, model.TrafficMatch{Destination: &d}, ev.flow) {
				return false
			}
		}
		return true
	}
	return false
}

func flowEvidence(f engine.Flow, at time.Time) map[string]any {
	m := map[string]any{
		"kind": "flow", "protocol": f.Protocol, "src": f.Src.String(), "sport": f.SPort, "dst": f.Dst.String(), "dport": f.DPort,
		"state": f.State, "observed_at": at.UTC().Format(time.RFC3339Nano),
	}
	if f.Device != "" {
		m["device"] = f.Device
	}
	return m
}

func describeFlow(f engine.Flow) string {
	return fmt.Sprintf("%s %s:%d to %s:%d (%s)", f.Protocol, f.Src, f.SPort, f.Dst, f.DPort, strings.ToLower(f.State))
}

// ---- the loop

// runChecks evaluates the checks of a run until all are decided or the context ends. start is the monotonic
// time of the start of the timeline, which the window of `start` and the offsets of the steps count from.
func (m *Manager) runChecks(ctx context.Context, r *runState, start time.Duration) {
	checks := make([]*liveCheck, 0, len(deref(r.sc.Checks)))
	for i := range deref(r.sc.Checks) {
		checks = append(checks, newLiveCheck(m.cfg.Gateway.Snapshot().Config, i, &(*r.sc.Checks)[i]))
	}
	var tracker flowTracker
	for {
		now, nowWall := m.clk.Monotonic()-start, m.clk.Now()
		times := m.stepTimes(r)

		// windows that open or close with a step
		for _, c := range checks {
			if c.decided {
				continue
			}
			if !c.opened {
				if t, ok := times[c.from]; ok {
					c.opened, c.wstart, c.wstartWall = true, t.off, t.wall
					m.setCheck(r, c.idx, func(cr *model.CheckResult) { cr.WindowStart = ptr(t.wall) })
					if c.until == "" {
						m.closeWindow(r, c, c.wstart+c.within, t.wall.Add(c.within))
					}
				}
			}
			if c.opened && !c.ended && c.until != "" {
				if t, ok := times[c.until]; ok {
					m.closeWindow(r, c, t.off, t.wall)
				}
			}
		}

		snap := m.cfg.Gateway.Snapshot()
		devs := scopeDevices(snap, r.sc.Target)
		devSet := idSet(devs)
		var events []flowEvent
		var flowErr error
		if anyOpen(checks, true) {
			flows, err := m.cfg.Observer.Flows(ctx)
			if err != nil {
				flowErr = err
				m.log.Debug("cannot read the connections for the checks of a run", "run", r.id, "error", err)
			} else {
				events = tracker.update(flows)
			}
		}

		for _, c := range checks {
			if c.decided || !c.opened {
				continue
			}
			if c.armed {
				switch {
				case c.usesFlows():
					m.feed(r, c, snap, devSet, events, nowWall)
				default:
					m.lookDNS(r, c, devs)
				}
			}
			if c.decided {
				continue
			}
			if c.ended && now >= c.wend {
				m.endWindow(r, c, flowErr, devs)
			}
		}
		for _, c := range checks {
			if c.opened {
				c.armed = true
			}
		}

		if allDecided(checks) {
			return
		}
		d := nextWake(checks, now)
		var timer clock.Timer
		var wake <-chan time.Time
		if d > 0 {
			timer = m.clk.NewTimer(d)
			wake = timer.C()
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-wake:
		case <-r.progress:
			if timer != nil {
				timer.Stop()
			}
		}
	}
}

// anyOpen reports whether a check that has not been decided has an open window, of the connection kind
// (flows) or of any kind.
func anyOpen(checks []*liveCheck, flows bool) bool {
	for _, c := range checks {
		if c.opened && !c.decided && (!flows || c.usesFlows()) {
			return true
		}
	}
	return false
}

func allDecided(checks []*liveCheck) bool {
	for _, c := range checks {
		if !c.decided {
			return false
		}
	}
	return true
}

// nextWake is when the loop has to look again: at the next poll while a window is open, at the end of the
// earliest window if that comes first. Zero when only the progress of the timeline can change anything (every
// window waits for its step).
func nextWake(checks []*liveCheck, now time.Duration) time.Duration {
	var wake time.Duration
	for _, c := range checks {
		if c.decided || !c.opened {
			continue
		}
		d := checkPoll
		if c.ended {
			d = min(d, max(c.wend-now, time.Millisecond))
		}
		if wake == 0 || d < wake {
			wake = d
		}
	}
	return wake
}

// closeWindow fixes the end of a window.
func (m *Manager) closeWindow(r *runState, c *liveCheck, off time.Duration, wall time.Time) {
	c.ended, c.wend, c.wendWall = true, off, wall
	m.setCheck(r, c.idx, func(cr *model.CheckResult) { cr.WindowEnd = ptr(wall) })
}

// feed hands the connection events of a poll to a check whose window is open and decides it if they do:
// a positive check passes on the first connection it looks for, a negative one fails on it.
func (m *Manager) feed(r *runState, c *liveCheck, snap *engine.Snapshot, devices map[string]bool, events []flowEvent, at time.Time) {
	var hits []flowEvent
	for _, ev := range events {
		if c.observes(snap, r.sc.Target, devices, ev) {
			hits = append(hits, ev)
		}
	}
	if len(hits) == 0 {
		return
	}
	for i := range hits {
		if len(c.evidence) < maxEvidence {
			c.evidence = append(c.evidence, flowEvidence(hits[i].flow, at))
		}
	}
	first := describeFlow(hits[0].flow)
	after := at.Sub(c.wstartWall).Round(time.Millisecond)
	n := ""
	if len(hits) > 1 {
		n = fmt.Sprintf(" (and %d more)", len(hits)-1)
	}
	switch c.kind {
	case "reconnected":
		m.decide(r, c, model.CheckResultStatusPassed, fmt.Sprintf("%s was established %s into the window", first, after))
	case "traffic_seen":
		m.decide(r, c, model.CheckResultStatusPassed, fmt.Sprintf("traffic was seen %s into the window: %s%s", after, first, n))
	case "no_connection":
		m.decide(r, c, model.CheckResultStatusFailed, fmt.Sprintf("%s was established %s into the window%s", first, after, n))
	case "traffic_not_seen":
		m.decide(r, c, model.CheckResultStatusFailed, fmt.Sprintf("traffic was seen %s into the window: %s%s", after, first, n))
	}
}

// lookDNS looks into the DNS log for a `dns_query_seen` check whose window is open: a query for the name by a device
// of the target, asked inside the window.
func (m *Manager) lookDNS(r *runState, c *liveCheck, devs []engine.DeviceState) {
	if len(devs) == 0 {
		return
	}
	for _, q := range m.cfg.Observer.DNSQueries(ids(devs), c.dns, c.wstartWall) {
		if c.ended && q.Time.After(c.wendWall) {
			continue
		}
		c.evidence = append(c.evidence, map[string]any{"kind": "dns_query", "name": q.Name, "type": q.Type, "device": q.Device, "time": q.Time.UTC().Format(time.RFC3339Nano)})
		m.decide(r, c, model.CheckResultStatusPassed, fmt.Sprintf("the query for %s (%s) was asked %s into the window", q.Name, q.Type, q.Time.Sub(c.wstartWall).Round(time.Millisecond)))
		return
	}
}

// endWindow decides a check whose window has closed without the observation that would have decided it
// earlier: a positive check has failed, a negative one has passed. A connection check that could not read the
// connections at its last poll cannot say either way: it is an error.
func (m *Manager) endWindow(r *runState, c *liveCheck, flowErr error, devs []engine.DeviceState) {
	window := fmt.Sprintf("the window of %s", (c.wend - c.wstart).Round(time.Millisecond))
	switch {
	case c.usesFlows() && flowErr != nil:
		m.decide(r, c, model.CheckResultStatusError, "the connections could not be read when the window closed: "+flowErr.Error())
	case c.kind == "dns_query_seen":
		m.lookDNS(r, c, devs)
		if !c.decided {
			m.decide(r, c, model.CheckResultStatusFailed, fmt.Sprintf("no device of the target asked for %s in %s", c.dns, window))
		}
	case c.kind == "reconnected":
		m.decide(r, c, model.CheckResultStatusFailed, fmt.Sprintf("no connection matching %s was established in %s", c.what, window))
	case c.kind == "traffic_seen":
		m.decide(r, c, model.CheckResultStatusFailed, fmt.Sprintf("no traffic matching %s was seen in %s", c.what, window))
	case c.kind == "no_connection":
		m.decide(r, c, model.CheckResultStatusPassed, fmt.Sprintf("no connection matching %s was established in %s", c.what, window))
	case c.kind == "traffic_not_seen":
		m.decide(r, c, model.CheckResultStatusPassed, fmt.Sprintf("no traffic matching %s was seen in %s", c.what, window))
	}
}

// decide records the result of a check, once.
func (m *Manager) decide(r *runState, c *liveCheck, status model.CheckResultStatus, message string) {
	if c.decided {
		return
	}
	c.decided = true
	evidence := c.evidence
	var name string
	m.setCheck(r, c.idx, func(cr *model.CheckResult) {
		cr.Status, cr.Message = status, ptr(message)
		if len(evidence) > 0 {
			cr.Evidence = &evidence
		}
		name = cr.Name
	})
	m.save(r)
	m.emit(r, engine.EventRunCheck, map[string]any{"check": c.idx, "check_name": name, "status": string(status), "message": message})
}

// setCheck changes the result of a check in the record.
func (m *Manager) setCheck(r *runState, idx int, f func(*model.CheckResult)) {
	m.mu.Lock()
	cs := *r.rec.Checks
	f(&cs[idx])
	m.mu.Unlock()
}

// ---- when the steps were reached

// stepTime is when the timeline reached a step: the offset from its start (monotonic) and the wall time.
type stepTime struct {
	off  time.Duration
	wall time.Time
}

// reached records that the timeline has reached a step (it is applied, or had nothing to do), and wakes the
// checks, whose windows start and end with steps.
func (m *Manager) reached(r *runState, id string, off time.Duration, wall time.Time) {
	m.mu.Lock()
	if _, ok := r.reachedAt[id]; !ok {
		r.reachedAt[id] = stepTime{off, wall}
	}
	m.mu.Unlock()
	select {
	case r.progress <- struct{}{}:
	default:
	}
}

// stepTimes returns the steps reached so far and the start of the timeline, as "start".
func (m *Manager) stepTimes(r *runState) map[string]stepTime {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]stepTime, len(r.reachedAt))
	for k, v := range r.reachedAt {
		out[k] = v
	}
	return out
}

// verdict is how a run whose steps and checks are done ends: `error` when a check could not be decided,
// `failed` when one failed, else `passed`.
func (m *Manager) verdict(r *runState) (model.RunStatus, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failed, errored []model.CheckResult
	for _, c := range deref(r.rec.Checks) {
		switch c.Status {
		case model.CheckResultStatusFailed:
			failed = append(failed, c)
		case model.CheckResultStatusError:
			errored = append(errored, c)
		}
	}
	switch {
	case len(errored) > 0:
		return model.RunStatusError, verdictReason("could not be decided", errored)
	case len(failed) > 0:
		return model.RunStatusFailed, verdictReason("failed", failed)
	}
	return model.RunStatusPassed, ""
}

func verdictReason(what string, cs []model.CheckResult) string {
	out := fmt.Sprintf("check %q %s: %s", cs[0].Name, what, deref(cs[0].Message))
	if len(cs) > 1 {
		out += fmt.Sprintf(" (and %d more)", len(cs)-1)
	}
	return out
}
