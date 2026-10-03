package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// keepalive is how often an idle stream gets a comment line (plan §2.15).
var keepalive = 15 * time.Second

// writeTimeout bounds a write to a client: a client that stops reading is disconnected instead of
// holding its goroutine (and, through the full buffer, being dropped by the bus).
var writeTimeout = 10 * time.Second

// publicEvent is the event as the API shows it; internal event types are left out.
type publicEvent struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Time       time.Time      `json:"time"`
	Data       map[string]any `json:"data,omitempty"`
	Message    string         `json:"message,omitempty"`
	Generation *int64         `json:"generation,omitempty"`
}

var knownTypes = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range []model.EventType{
		model.EventTypeApplied, model.EventTypeApplyFailed, model.EventTypeConfirmPending, model.EventTypeRevisionCreated,
		model.EventTypeRevisionApplied, model.EventTypeRevisionConfirmed, model.EventTypeRevisionRolledBack,
		model.EventTypeNetworkDegraded, model.EventTypeNetworkRestored, model.EventTypeUplinkChanged,
		model.EventTypeWireguardPeerOnline, model.EventTypeWireguardPeerOffline, model.EventTypeRoutingSessionChanged,
	} {
		m[string(t)] = true
	}
	return m
}()

func toPublic(ev engine.Event) (publicEvent, bool) {
	if !knownTypes[ev.Type] {
		return publicEvent{}, false
	}
	pe := publicEvent{ID: strconv.FormatUint(ev.Seq, 10), Type: ev.Type, Time: ev.Time.UTC(), Data: ev.Data, Message: describe(ev)}
	if g, ok := ev.Data["generation"]; ok {
		switch v := g.(type) {
		case uint64:
			n := int64(v)
			pe.Generation = &n
		case int64:
			pe.Generation = &v
		}
	}
	return pe, true
}

// describe is the activity-log line of an event.
func describe(ev engine.Event) string {
	str := func(k string) string {
		if v, ok := ev.Data[k]; ok {
			return strings.TrimSpace(toString(v))
		}
		return ""
	}
	switch ev.Type {
	case "applied":
		return "configuration applied"
	case "apply_failed":
		return "apply failed: " + str("error")
	case "revision_created":
		return "revision " + str("revision") + " created"
	case "revision_applied":
		return "revision " + str("revision") + " applied"
	case "revision_confirmed":
		return "revision " + str("revision") + " confirmed"
	case "revision_rolled_back":
		return "revision " + str("revision") + " rolled back"
	case "confirm_pending":
		return "revision " + str("revision") + " waits for confirmation"
	case "wireguard_peer_online":
		return "WireGuard peer " + str("peer") + " online"
	case "wireguard_peer_offline":
		return "WireGuard peer " + str("peer") + " offline"
	case "routing_session_changed":
		return "routing session " + str("protocol") + " " + str("state")
	case "network_degraded":
		return "network degraded: " + str("reason")
	case "network_restored":
		return "network restored"
	}
	return ""
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

// StreamEvents implements GET /events: Server-Sent Events with ids, replay from Last-Event-ID and a
// keepalive comment.
func (s *Server) StreamEvents(c *gin.Context, params model.StreamEventsParams) {
	want := map[string]bool{}
	if params.Types != nil && *params.Types != "" {
		for _, t := range strings.Split(*params.Types, ",") {
			t = strings.TrimSpace(t)
			if !model.EventType(t).Valid() {
				s.write(c, newProblem(model.ErrorCodeBadRequest, "unknown event type %q", t))
				return
			}
			want[t] = true
		}
	}
	var last uint64
	hasLast := false
	if params.LastEventID != nil && *params.LastEventID != "" {
		n, err := strconv.ParseUint(*params.LastEventID, 10, 64)
		if err != nil {
			s.write(c, newProblem(model.ErrorCodeBadRequest, "Last-Event-ID must be an event id"))
			return
		}
		last, hasLast = n, true
	}
	rc := http.NewResponseController(c.Writer)
	var replay []engine.Event
	var live <-chan engine.Event
	var cancel func()
	if hasLast {
		replay, live, cancel = s.cfg.Engine.SubscribeFrom(last)
	} else {
		live, cancel = s.cfg.Engine.Subscribe()
	}
	defer cancel()

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "close") // the stream ends when the server drops a slow client: the connection goes with it
	c.Status(http.StatusOK)
	send := func(raw string) bool {
		if err := rc.SetWriteDeadline(s.clk.Now().Add(writeTimeout)); err != nil {
			s.log.Error("write deadline", "error", err)
		}
		if _, err := c.Writer.WriteString(raw); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(": stream open\n\n") {
		return
	}
	emit := func(ev engine.Event) bool {
		pe, ok := toPublic(ev)
		if !ok || (len(want) > 0 && !want[pe.Type]) {
			return true
		}
		b, _ := json.Marshal(pe)
		return send("id: " + pe.ID + "\nevent: " + pe.Type + "\ndata: " + string(b) + "\n\n")
	}
	for _, ev := range replay {
		if !emit(ev) {
			return
		}
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-live:
			if !ok {
				return // dropped as a slow subscriber: the client reconnects and replays
			}
			if !emit(ev) {
				return
			}
		case <-tick.C:
			if !send(": keepalive\n\n") {
				return
			}
		}
	}
}

// ListAudit implements GET /audit.
func (s *Server) ListAudit(c *gin.Context, params model.ListAuditParams) {
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	var f auditFilter
	if params.Since != nil {
		f.Since = *params.Since
	}
	items, next, err := s.cfg.Audit.List(f, cursor, limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, e := range items {
		b := gin.H{"id": e.ID, "time": e.Time, "actor": e.Actor, "via": e.Via, "action": e.Action}
		if e.Object != nil {
			b["object"] = e.Object
		}
		if e.Revision != 0 {
			b["revision"] = e.Revision
		}
		if e.Detail != "" {
			b["detail"] = e.Detail
		}
		out = append(out, b)
	}
	body := gin.H{"items": out}
	if next != "" {
		body["next_cursor"] = next
	}
	c.JSON(200, body)
}
