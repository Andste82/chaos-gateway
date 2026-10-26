package api

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// This file is the API of the interception proxy (plan §2.8, M22): what the proxy reports (the HTTP, WebSocket and
// raw-stream exchanges it saw, the TLS secrets of its connections), the log a client reads (GET /intercept/flows) and
// the regeneration of the test CA. The handshakes of the proxy's connections with the devices go to
// POST /internal/tls/handshakes like those of the responder (tlsservice.go), and the proxy fetches its rules from
// GET /internal/tls/config (the cases of the kind `intercept`).

// interceptLogSize is how many exchanges the API keeps in memory.
const interceptLogSize = 20000

// httpFaultEventEvery is how often http_fault_triggered is sent for one overlay and device: a device that makes a
// request every second would otherwise fill the event stream.
const httpFaultEventEvery = 10 * time.Second

type interceptState struct {
	// lastPollAt is when the proxy last asked for its configuration: the health check uses it to tell whether the
	// proxy runs. 0 before the first poll (a gateway without a proxy never has one).
	lastPollAt atomic.Int64 // UnixNano

	mu      sync.Mutex
	seq     int64
	entries []loggedFlow // oldest first, bounded
	// faultEvent is when a rule last announced itself for an overlay and device (http_fault_triggered)
	faultEvent map[string]time.Time
}

type loggedFlow struct {
	seq int64
	e   model.InterceptFlow
}

// interceptLastPoll is when the proxy last asked for its configuration, zero before the first poll.
func (s *Server) interceptLastPoll() time.Time {
	if ns := s.intercept.lastPollAt.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// PostInterceptFlows implements POST /internal/intercept/flows.
func (s *Server) PostInterceptFlows(c *gin.Context) {
	var batch []model.InterceptFlowReport
	if p := decodeJSON(c, &batch); p != nil {
		s.write(c, p)
		return
	}
	if len(batch) > 1000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 1000 exchanges per request"))
		return
	}
	snap := s.cfg.Engine.Snapshot()
	flows := make([]model.InterceptFlow, 0, len(batch))
	for i, r := range batch {
		client, err := netip.ParseAddr(r.Client)
		if err != nil || !client.Is4() || r.Time.IsZero() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "exchange %d needs a time and the client's IPv4 address", i))
			return
		}
		if _, err := netip.ParseAddrPort(r.OriginalDst); err != nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "exchange %d: original_dst %q is not an address and port", i, r.OriginalDst))
			return
		}
		if !r.Protocol.Valid() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "exchange %d: unknown protocol %q", i, r.Protocol))
			return
		}
		for _, a := range deref(r.Actions) {
			if !a.Valid() {
				s.write(c, newProblem(model.ErrorCodeValidationFailed, "exchange %d: unknown action %q", i, a))
				return
			}
		}
		f := flowOf(r)
		f.Client = client.Unmap().String()
		if dev, ok := snap.Identity.OwnerOf(client.Unmap()); ok {
			if id, err := uuid.Parse(dev); err == nil {
				f.Device = &id
			}
		}
		flows = append(flows, f)
	}
	s.intercept.mu.Lock()
	for _, f := range flows {
		s.intercept.seq++
		s.intercept.entries = append(s.intercept.entries, loggedFlow{seq: s.intercept.seq, e: f})
	}
	if over := len(s.intercept.entries) - interceptLogSize; over > 0 {
		s.intercept.entries = append([]loggedFlow(nil), s.intercept.entries[over:]...)
	}
	var events []map[string]any
	now := s.clk.Now()
	for _, f := range flows {
		if len(deref(f.Actions)) == 0 {
			continue
		}
		if ev := s.faultEventLocked(snap, f, now); ev != nil {
			events = append(events, ev)
		}
	}
	s.intercept.mu.Unlock()
	for _, ev := range events {
		s.cfg.Engine.Emit(engine.EventHTTPFaultTriggered, ev)
	}
	c.Status(204)
}

// faultEventLocked is the event http_fault_triggered for an exchange a rule acted on, nil when the same overlay announced
// itself for the device less than httpFaultEventEvery ago. Called with intercept.mu held.
func (s *Server) faultEventLocked(snap *engine.Snapshot, f model.InterceptFlow, now time.Time) map[string]any {
	key := deref(f.Device).String() + "|" + deref(f.Overlay).String() + "|" + f.Client
	if t, ok := s.intercept.faultEvent[key]; ok && now.Sub(t) < httpFaultEventEvery {
		return nil
	}
	if s.intercept.faultEvent == nil {
		s.intercept.faultEvent = map[string]time.Time{}
	}
	if len(s.intercept.faultEvent) > 4096 {
		for k, t := range s.intercept.faultEvent {
			if now.Sub(t) >= httpFaultEventEvery {
				delete(s.intercept.faultEvent, k)
			}
		}
	}
	s.intercept.faultEvent[key] = now
	actions := make([]string, 0, len(deref(f.Actions)))
	for _, a := range deref(f.Actions) {
		actions = append(actions, string(a))
	}
	ev := map[string]any{"client": f.Client, "original_dst": f.OriginalDst, "host": deref(f.Host), "path": deref(f.Path), "actions": actions}
	if f.Overlay != nil {
		ev["overlay"] = f.Overlay.String()
	}
	if f.Status != nil {
		ev["status"] = *f.Status
	}
	if f.Device != nil {
		id := strings.ToLower(f.Device.String())
		ev["device"] = id
		name := id
		if snap.Config != nil {
			if d, ok := deref(snap.Config.Devices)[id]; ok {
				name = d.Name
			}
		}
		for _, d := range snap.Devices {
			if strings.EqualFold(d.ID, id) {
				name = d.Name
			}
		}
		ev["device_name"] = name
		ev["subject"] = engine.Subject{Kind: "device", ID: id, Name: name}
	}
	return ev
}

func flowOf(r model.InterceptFlowReport) model.InterceptFlow {
	f := model.InterceptFlow{
		Client: r.Client, DurationMs: r.DurationMs, Error: r.Error, Host: r.Host, Messages: r.Messages, Method: r.Method,
		OriginalDst: r.OriginalDst, Overlay: r.Overlay, Path: r.Path, Protocol: model.InterceptFlowProtocol(r.Protocol),
		RequestBytes: r.RequestBytes, ResponseBytes: r.ResponseBytes, Status: r.Status, Time: r.Time,
	}
	if r.Actions != nil {
		acts := make([]model.InterceptFlowActions, len(*r.Actions))
		for i, a := range *r.Actions {
			acts[i] = model.InterceptFlowActions(a)
		}
		f.Actions = &acts
	}
	return f
}

// ListInterceptFlows implements GET /intercept/flows: newest first.
func (s *Server) ListInterceptFlows(c *gin.Context, params model.ListInterceptFlowsParams) {
	wantDevice := ""
	if params.Device != nil {
		_, cfg, ok := s.configAt(c, nil)
		if !ok {
			return
		}
		d, found := findDevice(s.deviceViews(cfg, true), *params.Device)
		if !found {
			s.write(c, notFound("device", *params.Device))
			return
		}
		wantDevice = strings.ToLower(d.ID)
	}
	s.intercept.mu.Lock()
	all := make([]loggedFlow, 0, len(s.intercept.entries))
	for i := len(s.intercept.entries) - 1; i >= 0; i-- {
		f := s.intercept.entries[i]
		if wantDevice != "" && (f.e.Device == nil || strings.ToLower(f.e.Device.String()) != wantDevice) {
			continue
		}
		if params.Host != nil && (f.e.Host == nil || !nameMatches(*params.Host, *f.e.Host)) {
			continue
		}
		if params.Since != nil && f.e.Time.Before(*params.Since) {
			continue
		}
		if params.Faulted != nil && *params.Faulted != (len(deref(f.e.Actions)) > 0) {
			continue
		}
		all = append(all, f)
	}
	s.intercept.mu.Unlock()
	items, next, err := page(all, func(f loggedFlow) string { return fmt.Sprintf("%020d", int64(1<<62)-f.seq) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	out := make([]model.InterceptFlow, 0, len(items))
	for _, f := range items {
		out = append(out, f.e)
	}
	c.JSON(200, listBody(out, next))
}

// PostInterceptKeylog implements POST /internal/intercept/keylog: the TLS secrets of the proxy's connections go into the
// key log of every running capture that covers the client. Nothing is kept when no capture does, and nothing of a batch whose
// overlay names no interception case in force or one with `keylog: false` (the proxy keeps those lines in; this is the second
// guard of the promise, P5-AUDIT-04).
func (s *Server) PostInterceptKeylog(c *gin.Context) {
	var batches []model.InterceptKeylogBatch
	if p := decodeJSON(c, &batches); p != nil {
		s.write(c, p)
		return
	}
	if len(batches) > 1000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 1000 batches per request"))
		return
	}
	type entry struct {
		client netip.Addr
		lines  []string
	}
	var work []entry
	snap := s.cfg.Engine.Snapshot()
	dropped := 0
	for i, b := range batches {
		client, err := netip.ParseAddr(b.Client)
		if err != nil || !client.Is4() || b.Lines == nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "batch %d needs the client's IPv4 address and its lines", i))
			return
		}
		for _, l := range b.Lines {
			if strings.ContainsAny(l, "\r\n") {
				s.write(c, newProblem(model.ErrorCodeValidationFailed, "batch %d: a line of the key log has a line break", i))
				return
			}
		}
		if b.Overlay != nil && !keylogAllowed(snap, b.Overlay.String()) {
			dropped++
			continue
		}
		work = append(work, entry{client.Unmap(), b.Lines})
	}
	if dropped > 0 {
		s.log.Warn("key log batches of an overlay without an interception case that gives its secrets were dropped", "batches", dropped)
	}
	if s.captures != nil {
		for _, w := range work {
			s.captures.AppendKeylog(w.client, w.lines)
		}
	}
	c.Status(204)
}

// RegenerateTestCa implements POST /tls/ca/regenerate: a new test CA. The responder and the proxy load it within seconds.
func (s *Server) RegenerateTestCa(c *gin.Context) {
	if s.cfg.TLSCADir == "" {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway has no directory for its test CA"))
		return
	}
	pem, err := tlsresponder.RegenerateTestCA(s.cfg.TLSCADir, s.clk.Now())
	if err != nil {
		s.log.Error("cannot make a new test CA", "error", err)
		s.write(c, newProblem(model.ErrorCodeInternal, "the test CA cannot be replaced: %v", err))
		return
	}
	s.record(c, "tls.ca_regenerate", &audit.Object{Kind: "tls_ca", ID: "test-ca", Name: "test CA"}, 0, "")
	c.Header("Content-Disposition", `attachment; filename="chaos-gateway-test-ca.pem"`)
	c.Data(200, "application/x-pem-file", pem)
}

// keylogAllowed reports whether an interception case of the overlay is in force in the applied configuration and gives the
// secrets of its connections (`keylog` is not false).
func keylogAllowed(snap *engine.Snapshot, overlay string) bool {
	if snap == nil {
		return false
	}
	for _, c := range snap.TLSCases {
		if c.Case.Case != model.TlsCaseCaseIntercept || !strings.EqualFold(c.Overlay, overlay) {
			continue
		}
		if c.Case.Intercept == nil || c.Case.Intercept.Keylog == nil || *c.Case.Intercept.Keylog {
			return true
		}
	}
	return false
}

// hasInterceptCase reports whether a case of the kind `intercept` is in force in the applied configuration.
func hasInterceptCase(snap *engine.Snapshot) bool {
	for _, c := range snap.TLSCases {
		if c.Case.Case == model.TlsCaseCaseIntercept {
			return true
		}
	}
	return false
}
