package api

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the public side of the NTP server's state (plan §2.18, M34): the query log and the state of one device, which
// the Time panel of the device detail, chaosctl and the scripts of a test read. What they read is kept in ntpservice.go.

// deviceIDOf resolves a device reference (id or name) against the active configuration and the observed devices, as the
// DNS query log does, and writes the problem when it is none. It returns the id in lower case.
func (s *Server) deviceIDOf(c *gin.Context, ref string) (string, bool) {
	_, cfg, ok := s.configAt(c, nil)
	if !ok {
		return "", false
	}
	d, found := findDevice(s.deviceViews(cfg, true), ref)
	if !found {
		s.write(c, notFound("device", ref))
		return "", false
	}
	return strings.ToLower(d.ID), true
}

// ListNtpQueries implements GET /ntp/queries: newest first.
func (s *Server) ListNtpQueries(c *gin.Context, params model.ListNtpQueriesParams) {
	wantDevice := ""
	if params.Device != nil {
		id, ok := s.deviceIDOf(c, *params.Device)
		if !ok {
			return
		}
		wantDevice = id
	}
	n, after, err := pageArgs(params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	// the page is cut under the lock from the newest entry on, and the walk ends with the page (one entry more tells whether
	// there is a next one): a wait polls this every moment and the server reports into the same lock
	key := func(l loggedNTP) string { return fmt.Sprintf("%020d", int64(1<<62)-l.seq) }
	sel := make([]loggedNTP, 0, n+1)
	s.ntp.logMu.Lock()
	s.ntpAdopt(s.cfg.Engine.Snapshot())
	for i := len(s.ntp.entries) - 1; i >= 0 && len(sel) <= n; i-- {
		l := s.ntp.entries[i]
		if (wantDevice != "" && l.device != wantDevice) || (params.Since != nil && l.e.Time.Before(*params.Since)) {
			continue
		}
		if params.Cursor != nil && *params.Cursor != "" && key(l) <= after {
			continue
		}
		sel = append(sel, l)
	}
	s.ntp.logMu.Unlock()
	items, next := sel, (*string)(nil)
	if len(sel) > n {
		items = sel[:n]
		k := encodeCursor(key(items[n-1]))
		next = &k
	}
	out := make([]model.NtpQueryLogEntry, 0, len(items))
	for _, l := range items {
		out = append(out, l.e)
	}
	// the time of the answer, to the nanosecond, on the clock the entries are stamped with: a wait starts at it (the Date header
	// has a resolution of a second, so a query of the second before would count)
	body := listBody(out, next)
	body["now"] = s.clk.Now().UTC().Format(time.RFC3339Nano)
	c.JSON(200, body)
}

// GetDeviceNtp implements GET /devices/{deviceId}/ntp: a device that never asked has zeros, an unknown one is a 404.
// `dropped` counts the log entries over the rate limit, and the server writes at most one of them per second for a device
// (sampled to one per second per device): it is not the number of queries the limit dropped.
func (s *Server) GetDeviceNtp(c *gin.Context, deviceId model.DeviceId) {
	id, ok := s.deviceIDOf(c, deviceId)
	if !ok {
		return
	}
	out := model.DeviceNtp{Answers: []struct {
		Count int64      `json:"count"`
		Fault model.Uuid `json:"fault"`
	}{}, Recent: []model.NtpQueryLogEntry{}}
	if u, err := uuid.Parse(id); err == nil {
		out.Device = u
	}
	d, seen := s.ntpDeviceState(id)
	if !seen {
		c.JSON(200, out)
		return
	}
	out.Queries, out.Silenced, out.Dropped, out.NtsKe = d.Queries, d.Silenced, d.Dropped, d.NTSKE
	faults := make([]string, 0, len(d.Answers))
	for f := range d.Answers {
		faults = append(faults, f)
	}
	slices.Sort(faults)
	for _, f := range faults {
		if u, err := uuid.Parse(f); err == nil {
			out.Answers = append(out.Answers, struct {
				Count int64      `json:"count"`
				Fault model.Uuid `json:"fault"`
			}{Count: d.Answers[f], Fault: u})
		}
	}
	if !d.LastQuery.IsZero() {
		last := model.DeviceNtpLastQuery{Time: d.LastQuery, Server: d.LastServer, ServedTime: d.LastServed, OffsetMs: d.LastOffsetMs}
		if d.LastAnswer != "" {
			a := model.DeviceNtpLastQueryAnswer(d.LastAnswer)
			last.Answer = &a
		}
		if u, err := uuid.Parse(d.LastFault); err == nil {
			last.Fault = &u
		}
		out.LastQuery = &last
	}
	for i := len(d.recent) - 1; i >= 0; i-- { // newest first
		out.Recent = append(out.Recent, d.recent[i])
	}
	c.JSON(200, out)
}
