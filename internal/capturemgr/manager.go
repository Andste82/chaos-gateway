// Package capturemgr is the management side of packet captures (plan §2.11, M17): it turns the scope of a request
// into the interfaces and filters the executor reads (the executor holds the privilege for the AF_PACKET sockets and
// writes the files), follows each capture through the stream the executor keeps open, keeps its record, enforces the
// quota and the free-space limit of the volume, and serves the files for download, also while the capture runs.
//
// A capture lives as long as the stream: a process that ends takes its captures with it, and the next one marks them
// `failed` (Recover). The files are in the directory `<dir>/<capture id>/`, which the manager makes (so it owns the
// files) next to the record `capture.json`; the executor, which sees the same volume, makes the packet files in it.
package capturemgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// DefaultQuota is the total size of all captures when the configuration names none (plan §3.6).
const DefaultQuota = 5 << 30

// DefaultMinFreePercent is the free space of the volume below which captures stop and new ones are refused (plan §3.6).
const DefaultMinFreePercent = 10.0

// MaxRunning bounds the captures that run at once (the executor's limit).
const MaxRunning = executor.MaxCaptures

const (
	recordName  = "capture.json"
	fileVersion = 1
)

var (
	// ErrNotFound is returned for a capture that does not exist (any more).
	ErrNotFound = errors.New("capturemgr: not found")
	// ErrRunning is returned for the deletion of a capture that still runs.
	ErrRunning = errors.New("capturemgr: the capture still runs")
	// ErrNoKeylog is returned for the key log of a capture that has none.
	ErrNoKeylog = errors.New("capturemgr: the capture has no TLS key log")
	// ErrNoCaptures is returned when the gateway has no capture volume or no executor that captures.
	ErrNoCaptures = errors.New("capturemgr: this deployment has no capture volume")
	// ErrDiskLow is returned when the volume has less free space than the limit.
	ErrDiskLow = errors.New("capturemgr: the volume of the captures has less than 10 % free space")
	// ErrTooMany is returned when the most captures that run at once run.
	ErrTooMany = errors.New("capturemgr: too many captures run at once")
	// ErrQuota is returned when running captures use the quota, so that no capture could record.
	ErrQuota = errors.New("capturemgr: the running captures use the quota")
	// ErrUnavailable is returned when the executor cannot be reached.
	ErrUnavailable = errors.New("capturemgr: the executor does not answer")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("capturemgr: the manager is closed")
)

// UnsupportedError is returned for a request that needs a milestone this build does not have.
type UnsupportedError struct{ What, Milestone string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s is not available in this build; it arrives with milestone %s (see GET /capabilities)", e.What, e.Milestone)
}

// Opener starts captures: the executor. *executor.Client, *executor.Redialing and apply.Local are ones.
type Opener interface {
	OpenCapture(ctx context.Context, o executor.CaptureOpen) (executor.CaptureStream, error)
}

// Gateway is what the manager needs of the engine: the snapshot to resolve a scope with and the event bus.
type Gateway interface {
	Snapshot() *engine.Snapshot
	Emit(typ string, data map[string]any)
}

// Config configures a Manager.
type Config struct {
	Clock clock.Clock
	// Dir is the capture volume as this process sees it. Required.
	Dir     string
	Opener  Opener
	Gateway Gateway
	// Namespace is the network namespace of the interfaces (the testbed's gateway namespace); empty in production, where
	// the executor captures in its own.
	Namespace string
	Log       *slog.Logger
	// Quota returns the total size all captures may take (the configuration's `settings.retention.captures_quota`);
	// DefaultQuota when nil or when it returns zero.
	Quota func() int64
	// MinFreePercent and DiskFree are the free-space limit of the volume and how to read it (statfs of Dir by default).
	MinFreePercent float64
	DiskFree       func() (free, total uint64, err error)
	// NewID makes the id of a capture; a random UUID by default.
	NewID func() uuid.UUID
}

// StartRequest is a capture to start.
type StartRequest struct {
	model.CaptureRequest
	// Run attaches the capture to a run.
	Run *uuid.UUID
}

// Manager owns the captures.
type Manager struct {
	cfg Config
	clk clock.Clock
	log *slog.Logger

	wg sync.WaitGroup // the goroutines that follow a capture

	mu     sync.Mutex
	caps   map[uuid.UUID]*capState
	closed bool
}

// capState is one capture. The record and the pending stop are guarded by Manager.mu.
type capState struct {
	id     uuid.UUID
	rec    model.Capture
	dir    string
	stream executor.CaptureStream
	done   chan struct{} // closed when the capture has ended and its record is written
	// pending is why the manager itself stopped the capture, else the reason the executor reports counts
	pending *pendingStop
	// clamped says that the quota cut the maximum size the request named
	clamped bool
	files   []string
	fileMu  sync.Mutex
}

type pendingStop struct {
	reason  model.CaptureStopReason
	message string
}

type recordFile struct {
	Version int           `json:"version"`
	Capture model.Capture `json:"capture"`
	Files   []string      `json:"files,omitempty"`
}

// New creates a manager. Call Recover once to take over what an earlier process left.
func New(cfg Config) (*Manager, error) {
	if cfg.Clock == nil || cfg.Dir == "" || cfg.Gateway == nil {
		return nil, errors.New("capturemgr: the clock, the directory and the gateway are required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.MinFreePercent <= 0 {
		cfg.MinFreePercent = DefaultMinFreePercent
	}
	if cfg.NewID == nil {
		cfg.NewID = uuid.New
	}
	if cfg.DiskFree == nil {
		dir := cfg.Dir
		cfg.DiskFree = func() (uint64, uint64, error) {
			var st unix.Statfs_t
			if err := unix.Statfs(dir, &st); err != nil {
				return 0, 0, err
			}
			return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
		}
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, clk: cfg.Clock, log: cfg.Log, caps: map[uuid.UUID]*capState{}}, nil
}

// Available reports whether captures can be started: there is an executor that captures.
func (m *Manager) Available() bool { return m.cfg.Opener != nil }

func (m *Manager) quota() int64 {
	if m.cfg.Quota != nil {
		if q := m.cfg.Quota(); q > 0 {
			return q
		}
	}
	return DefaultQuota
}

// diskLow reports whether the volume has less free space than the limit.
func (m *Manager) diskLow() (bool, float64) {
	free, total, err := m.cfg.DiskFree()
	if err != nil || total == 0 {
		return false, 100
	}
	pct := float64(free) * 100 / float64(total)
	return pct < m.cfg.MinFreePercent, pct
}

// Recover takes over the captures an earlier process left: one that was running ended with that process
// (the executor closes the files when the stream breaks), so it is `failed` with the reason `error`.
func (m *Manager) Recover() error {
	entries, err := os.ReadDir(m.cfg.Dir)
	if err != nil {
		return err
	}
	now := m.clk.Now()
	for _, e := range entries {
		id, err := uuid.Parse(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.cfg.Dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, recordName))
		if err != nil {
			m.log.Warn("cannot read the record of a capture: it is left alone", "capture", id, "error", err)
			continue
		}
		var f recordFile
		if err := json.Unmarshal(raw, &f); err != nil || f.Capture.Id != id {
			m.log.Warn("the record of a capture is not valid: it is left alone", "capture", id)
			continue
		}
		c := &capState{id: id, rec: f.Capture, dir: dir, done: closedChan(), files: f.Files}
		if c.rec.Status == model.CaptureStatusRunning {
			c.rec.Status = model.CaptureStatusFailed
			c.rec.StopReason = ptr(model.CaptureStopReasonError)
			c.rec.StoppedAt = ptr(now)
			c.rec.Message = ptr("the service restarted while the capture ran")
			size, _ := dirSize(dir)
			c.rec.SizeBytes = ptr(size)
			m.save(c)
		}
		m.mu.Lock()
		m.caps[id] = c
		m.mu.Unlock()
	}
	return nil
}

// Close stops the captures that run and waits for them: they end `failed`, as a service that stops ends them.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	var running []*capState
	for _, c := range m.caps {
		if c.rec.Status == model.CaptureStatusRunning {
			c.pending = &pendingStop{model.CaptureStopReasonError, "the service stopped while the capture ran"}
			running = append(running, c)
		}
	}
	m.mu.Unlock()
	for _, c := range running {
		c.stream.Stop("user")
	}
	for _, c := range running {
		select {
		case <-c.done:
		case <-ctx.Done():
		}
	}
	m.wg.Wait()
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func ptr[T any](v T) *T { return &v }

// ---- starting

// Start resolves the scope, makes room in the quota, starts the capture in the executor and returns the running
// capture.
func (m *Manager) Start(ctx context.Context, req StartRequest) (model.Capture, error) {
	if m.cfg.Opener == nil {
		return model.Capture{}, ErrNoCaptures
	}
	snap := m.cfg.Gateway.Snapshot()
	if snap == nil || snap.Config == nil {
		return model.Capture{}, engine.ErrNoConfiguration
	}
	plan, err := Resolve(snap, req.Scope)
	if err != nil {
		return model.Capture{}, err
	}
	format := capture.PCAPNG
	if req.Format != nil && *req.Format == model.CaptureRequestFormatPcap {
		format = capture.PCAP
	}
	if format == capture.PCAP && plan.Mixed() {
		return model.Capture{}, domain.ValidationErrors{{Path: "/format", Code: "mixed_link_types", Message: "the scope spans interfaces of different link types (Ethernet and raw IP): a pcap file holds one, use pcapng"}}
	}
	if len(plan.Interfaces) > capture.MaxInterfaces {
		return model.Capture{}, domain.ValidationErrors{{Path: "/scope", Code: "too_many_interfaces", Message: fmt.Sprintf("the scope spans %d interfaces, a capture reads at most %d", len(plan.Interfaces), capture.MaxInterfaces)}}
	}
	if low, _ := m.diskLow(); low {
		return model.Capture{}, ErrDiskLow
	}

	open := executor.CaptureOpen{Namespace: m.cfg.Namespace, Format: format, Interfaces: plan.Interfaces}
	if req.Snaplen != nil {
		open.Snaplen = *req.Snaplen
	}
	if req.Duration != nil {
		d, _ := time.ParseDuration(*req.Duration)
		open.DurationMS = d.Milliseconds()
	}
	quota := m.quota()
	var need int64
	clamped := false
	if rb := req.RingBuffer; rb != nil {
		size, _ := domain.ParseByteSize(rb.FileSize)
		open.RingFiles, open.RingFileSize = rb.Files, size
		// compared before the multiplication: files x size does not fit an int64 for a size of up to 2^62 bytes
		if rb.Files > 0 && size > quota/int64(rb.Files) {
			return model.Capture{}, domain.ValidationErrors{{Path: "/ring_buffer", Code: domain.CodeCaptureLimit, Message: fmt.Sprintf("the ring buffer (%d files of %s) is larger than the quota (%s)", rb.Files, rb.FileSize, domain.FormatByteSize(quota))}}
		}
		need = int64(rb.Files) * size
	} else {
		want := int64(domain.DefaultCaptureMaxSize)
		if req.MaxSize != nil {
			want, _ = domain.ParseByteSize(*req.MaxSize)
			if want > quota {
				return model.Capture{}, domain.ValidationErrors{{Path: "/max_size", Code: domain.CodeCaptureLimit, Message: fmt.Sprintf("the maximum size is larger than the quota (%s)", domain.FormatByteSize(quota))}}
			}
		}
		open.MaxSize = want
		need = domain.MinCaptureFile
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return model.Capture{}, ErrClosed
	}
	running := 0
	for _, c := range m.caps {
		if c.rec.Status == model.CaptureStatusRunning {
			running++
		}
	}
	if running >= MaxRunning {
		m.mu.Unlock()
		return model.Capture{}, ErrTooMany
	}
	m.mu.Unlock()
	// the oldest stopped captures go until this one fits
	used := m.makeRoom(quota, need)
	avail := quota - used
	if req.RingBuffer != nil {
		if need > avail {
			return model.Capture{}, ErrQuota
		}
	} else {
		if avail < domain.MinCaptureFile {
			return model.Capture{}, ErrQuota
		}
		if open.MaxSize > avail {
			open.MaxSize, clamped = avail, true
		}
	}

	id := m.cfg.NewID()
	open.Dir = id.String()
	dir := filepath.Join(m.cfg.Dir, open.Dir)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return model.Capture{}, err
	}
	stream, err := m.cfg.Opener.OpenCapture(ctx, open)
	if err != nil {
		_ = os.RemoveAll(dir)
		return model.Capture{}, openError(err)
	}
	first, ok := m.firstEvent(ctx, stream)
	if !ok {
		stream.Close()
		_ = os.RemoveAll(dir)
		return model.Capture{}, ErrUnavailable
	}
	now := m.clk.Now()
	rec := model.Capture{
		Id: id, Name: req.Name, Scope: req.Scope, Format: ptr(model.CaptureFormat(format)), Snaplen: req.Snaplen,
		Duration: req.Duration, MaxSize: req.MaxSize, RingBuffer: req.RingBuffer, Status: model.CaptureStatusRunning, StartedAt: now,
		Run: req.Run, Keylog: ptr(false), Sensitive: ptr(false), SizeBytes: ptr(int64(0)), Packets: ptr(int64(0)), Dropped: ptr(int64(0)),
	}
	ifs := make([]ifaceRec, 0, len(first.Interfaces))
	for i, ii := range first.Interfaces {
		fd := ""
		if i < len(plan.Interfaces) {
			fd = plan.Interfaces[i].Filter.String()
		}
		ifs = append(ifs, ifaceRec{Name: ii.Name, Link: ii.Link, Filter: fd})
	}
	rec.Interfaces = interfacesOf(ifs)
	c := &capState{id: id, rec: rec, dir: dir, stream: stream, done: make(chan struct{}), clamped: clamped, files: first.Files}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		stream.Close()
		_ = os.RemoveAll(dir)
		return model.Capture{}, ErrClosed
	}
	m.caps[id] = c
	m.wg.Add(1)
	v := c.rec
	m.mu.Unlock()
	m.save(c)
	m.emitStarted(c)
	go m.follow(c)
	return v, nil
}

type ifaceRec struct {
	Name   string
	Link   capture.LinkType
	Filter string
}

func interfacesOf(ifs []ifaceRec) *[]struct {
	Filter   *string                         `json:"filter,omitempty"`
	LinkType model.CaptureInterfacesLinkType `json:"link_type"`
	Name     string                          `json:"name"`
} {
	out := make([]struct {
		Filter   *string                         `json:"filter,omitempty"`
		LinkType model.CaptureInterfacesLinkType `json:"link_type"`
		Name     string                          `json:"name"`
	}, 0, len(ifs))
	for _, i := range ifs {
		lt := model.Ethernet
		if i.Link == capture.LinkRaw {
			lt = model.Raw
		}
		item := struct {
			Filter   *string                         `json:"filter,omitempty"`
			LinkType model.CaptureInterfacesLinkType `json:"link_type"`
			Name     string                          `json:"name"`
		}{LinkType: lt, Name: i.Name}
		if i.Filter != "" {
			item.Filter = ptr(i.Filter)
		}
		out = append(out, item)
	}
	return &out
}

// firstEvent reads the event `started` the executor sends first.
func (m *Manager) firstEvent(ctx context.Context, s executor.CaptureStream) (executor.CaptureEvent, bool) {
	select {
	case ev, ok := <-s.Events():
		if ok && ev.Kind == executor.CaptureStarted {
			return ev, true
		}
	case <-ctx.Done():
	case <-m.clk.After(30 * time.Second):
	}
	return executor.CaptureEvent{}, false
}

// openError turns what the executor refused a capture with into the errors of this package.
func openError(err error) error {
	var vp domain.ValidationErrors
	switch {
	case errors.Is(err, executor.ErrNoCaptureDir):
		return ErrNoCaptures
	case errors.Is(err, executor.ErrDiskLow), errors.Is(err, capture.ErrDiskLow):
		return ErrDiskLow
	case errors.Is(err, executor.ErrTooManyCaptures):
		return ErrTooMany
	case errors.Is(err, executor.ErrOutOfScope):
		return domain.ValidationErrors{{Path: "/scope", Code: "not_assigned", Message: "an interface of the scope is not assigned to Chaos Gateway: " + err.Error()}}
	case errors.As(err, &vp):
		return err
	}
	var re *executor.RemoteError
	if errors.As(err, &re) && re.Code == executor.CodeInvalid {
		return domain.ValidationErrors{{Path: "/", Code: "invalid_capture", Message: re.Message}}
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// ---- following a capture

// follow reads the stream of a capture until it ends.
func (m *Manager) follow(c *capState) {
	defer m.wg.Done()
	ended := false
	for ev := range c.stream.Events() {
		switch ev.Kind {
		case executor.CaptureStats:
			m.update(c, ev)
			m.enforceQuota(c)
		case executor.CaptureStopped:
			m.finish(c, &ev)
			ended = true
		}
	}
	if !ended {
		m.finish(c, &executor.CaptureEvent{Kind: executor.CaptureStopped, Reason: "error", Error: "the executor ended the stream without an end"})
	}
	c.stream.Close()
}

// update takes the counters of a stats event into the record.
func (m *Manager) update(c *capState, ev executor.CaptureEvent) {
	m.mu.Lock()
	c.rec.Packets, c.rec.SizeBytes, c.rec.Dropped = ptr(ev.Packets), ptr(ev.Size), ptr(ev.Dropped)
	c.files = ev.Files
	m.mu.Unlock()
}

// finish records the end of a capture and announces it.
func (m *Manager) finish(c *capState, ev *executor.CaptureEvent) {
	now := m.clk.Now()
	m.mu.Lock()
	if c.rec.Status != model.CaptureStatusRunning {
		m.mu.Unlock()
		return
	}
	c.rec.Packets, c.rec.SizeBytes, c.rec.Dropped = ptr(ev.Packets), ptr(ev.Size), ptr(ev.Dropped)
	if len(ev.Files) > 0 {
		c.files = ev.Files
	}
	reason, status, msg := model.CaptureStopReason(ev.Reason), model.CaptureStatusStopped, ""
	switch {
	case c.pending != nil && (ev.Reason == "user" || ev.Reason == "error"):
		reason, msg = c.pending.reason, c.pending.message
	case ev.Reason == "size" && c.clamped:
		msg = "the capture reached the space left in the quota"
	case ev.Reason == "disk_low":
		msg = "the volume of the captures has less than 10 % free space"
	case ev.Reason == "error" && ev.Error != "":
		msg = ev.Error
	}
	if reason == model.CaptureStopReasonError {
		status = model.CaptureStatusFailed
		if msg == "" {
			msg = "the capture failed"
		}
	}
	c.rec.Status, c.rec.StopReason, c.rec.StoppedAt = status, &reason, &now
	if msg != "" {
		c.rec.Message = ptr(msg)
	}
	v := c.rec
	m.mu.Unlock()
	m.save(c)
	data := map[string]any{"capture": c.id.String(), "reason": string(reason), "packets": deref(v.Packets), "size_bytes": deref(v.SizeBytes),
		"subject": engine.Subject{Kind: "capture", ID: c.id.String(), Name: deref(v.Name)}}
	if v.Run != nil {
		data["run"] = v.Run.String()
	}
	if msg != "" {
		data["message"] = msg
	}
	m.cfg.Gateway.Emit(engine.EventCaptureStopped, data)
	if reason == model.CaptureStopReasonDiskLow {
		_, pct := m.diskLow()
		m.cfg.Gateway.Emit(engine.EventDiskLow, map[string]any{"capture": c.id.String(), "free_percent": int(pct), "subject": engine.Subject{Kind: "capture", ID: c.id.String(), Name: deref(v.Name)}})
	}
	close(c.done)
}

func (m *Manager) emitStarted(c *capState) {
	data := map[string]any{"capture": c.id.String(), "subject": engine.Subject{Kind: "capture", ID: c.id.String(), Name: deref(c.rec.Name)}}
	if c.rec.Run != nil {
		data["run"] = c.rec.Run.String()
	}
	m.cfg.Gateway.Emit(engine.EventCaptureStarted, data)
}

// ---- quota

// usage is the size of all captures: the files of a running capture count as the executor last reported them.
func (m *Manager) usageLocked() int64 {
	var n int64
	for _, c := range m.caps {
		n += deref(c.rec.SizeBytes)
	}
	return n
}

// makeRoom deletes the oldest stopped captures until the captures take at most quota-need bytes, and returns
// what they take then (running captures are never deleted).
func (m *Manager) makeRoom(quota, need int64) int64 {
	for {
		m.mu.Lock()
		used := m.usageLocked()
		if used+need <= quota {
			m.mu.Unlock()
			return used
		}
		var oldest *capState
		for _, c := range m.caps {
			if c.rec.Status == model.CaptureStatusRunning {
				continue
			}
			if oldest == nil || stoppedAt(c).Before(stoppedAt(oldest)) {
				oldest = c
			}
		}
		if oldest == nil {
			m.mu.Unlock()
			return used
		}
		delete(m.caps, oldest.id)
		m.mu.Unlock()
		m.log.Info("a capture is deleted: the captures take more than their quota", "capture", oldest.id, "quota", quota)
		if err := os.RemoveAll(oldest.dir); err != nil {
			m.log.Warn("cannot delete a capture", "capture", oldest.id, "error", err)
		}
	}
}

func stoppedAt(c *capState) time.Time {
	if c.rec.StoppedAt != nil {
		return *c.rec.StoppedAt
	}
	return c.rec.StartedAt
}

// enforceQuota keeps the total of all captures within the quota while one runs: the oldest stopped captures go
// first; a running capture that still does not fit is stopped with the reason `size`.
func (m *Manager) enforceQuota(c *capState) {
	quota := m.quota()
	used := m.makeRoom(quota, 0)
	if used <= quota {
		return
	}
	m.mu.Lock()
	if c.pending != nil || c.rec.Status != model.CaptureStatusRunning {
		m.mu.Unlock()
		return
	}
	c.pending = &pendingStop{model.CaptureStopReasonSize, fmt.Sprintf("the captures take %s, more than their quota of %s", domain.FormatByteSize(used), domain.FormatByteSize(quota))}
	m.mu.Unlock()
	c.stream.Stop("user")
}

// ---- records

// save writes the record of a capture.
func (m *Manager) save(c *capState) {
	m.mu.Lock()
	f := recordFile{Version: fileVersion, Capture: c.rec, Files: append([]string(nil), c.files...)}
	m.mu.Unlock()
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		m.log.Error("cannot encode a capture", "capture", c.id, "error", err)
		return
	}
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	tmp := filepath.Join(c.dir, recordName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		m.log.Error("cannot write a capture", "capture", c.id, "error", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, recordName)); err != nil {
		m.log.Error("cannot write a capture", "capture", c.id, "error", err)
	}
}

// dirSize adds up the sizes of the packet files of a capture.
func dirSize(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, e := range entries {
		if packetFile.MatchString(e.Name()) {
			if info, err := e.Info(); err == nil {
				n += info.Size()
			}
		}
	}
	return n, nil
}

// packetFile matches the files of a capture: capture.pcapng, or capture-00001.pcapng, … of a ring buffer. The number
// has five digits up to 99999 and grows after that, so it is matched with five or more.
var packetFile = regexp.MustCompile(`^capture(-[0-9]{5,})?\.(pcap|pcapng)$`)

// ---- reading

// Get returns a capture.
func (m *Manager) Get(id uuid.UUID) (model.Capture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.caps[id]
	if !ok {
		return model.Capture{}, ErrNotFound
	}
	return c.rec, nil
}

// List returns the captures newest first; with run only those of that run.
func (m *Manager) List(run *uuid.UUID) []model.Capture {
	m.mu.Lock()
	out := make([]model.Capture, 0, len(m.caps))
	for _, c := range m.caps {
		if run != nil && (c.rec.Run == nil || *c.rec.Run != *run) {
			continue
		}
		out = append(out, c.rec)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].Id.String() < out[j].Id.String()
	})
	return out
}

// Stop ends a running capture for the reason (`user`, or `run_end` for the end of its run) and returns when its
// files are closed. A capture that has ended is returned as it is.
func (m *Manager) Stop(ctx context.Context, id uuid.UUID, reason model.CaptureStopReason) (model.Capture, error) {
	m.mu.Lock()
	c, ok := m.caps[id]
	if !ok {
		m.mu.Unlock()
		return model.Capture{}, ErrNotFound
	}
	if c.rec.Status != model.CaptureStatusRunning {
		v := c.rec
		m.mu.Unlock()
		return v, nil
	}
	m.mu.Unlock()
	if reason != model.CaptureStopReasonRunEnd {
		reason = model.CaptureStopReasonUser
	}
	c.stream.Stop(string(reason))
	select {
	case <-c.done:
	case <-ctx.Done():
		return model.Capture{}, ctx.Err()
	}
	return m.Get(id)
}

// Wait returns when the capture has ended; ErrNotFound for one that does not exist.
func (m *Manager) Wait(ctx context.Context, id uuid.UUID) (model.Capture, error) {
	m.mu.Lock()
	c, ok := m.caps[id]
	m.mu.Unlock()
	if !ok {
		return model.Capture{}, ErrNotFound
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		return model.Capture{}, ctx.Err()
	}
	return m.Get(id)
}

// Delete removes a stopped capture and its files.
func (m *Manager) Delete(id uuid.UUID) error {
	m.mu.Lock()
	c, ok := m.caps[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if c.rec.Status == model.CaptureStatusRunning {
		m.mu.Unlock()
		return ErrRunning
	}
	delete(m.caps, id)
	m.mu.Unlock()
	return os.RemoveAll(c.dir)
}

// DeleteRun removes the stopped captures of a run: its record has been deleted after the retention time.
func (m *Manager) DeleteRun(run uuid.UUID) {
	for _, c := range m.List(&run) {
		if c.Status != model.CaptureStatusRunning {
			if err := m.Delete(c.Id); err != nil && !errors.Is(err, ErrNotFound) {
				m.log.Warn("cannot delete a capture of an expired run", "capture", c.Id, "run", run, "error", err)
			}
		}
	}
}

// ---- download

// Download is a capture file to send.
type Download struct {
	// Name is the file name to offer and ContentType its media type.
	Name, ContentType string
	// WriteTo writes the capture: the files of a ring buffer joined, oldest first, of a running capture the
	// complete records only.
	WriteTo func(w io.Writer) (int64, error)
}

// Open returns the capture file for download (`part` is "capture", or "keylog" for the TLS key log).
func (m *Manager) Open(id uuid.UUID, part string) (*Download, error) {
	m.mu.Lock()
	c, ok := m.caps[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	rec, dir := c.rec, c.dir
	m.mu.Unlock()
	if part == "keylog" {
		// the proxy that writes a key log arrives with M22: no capture has one yet
		return nil, ErrNoKeylog
	}
	format := capture.PCAPNG
	if rec.Format != nil && string(*rec.Format) == string(capture.PCAP) {
		format = capture.PCAP
	}
	name := deref(rec.Name)
	if name == "" {
		name = "capture-" + id.String()[:8]
	}
	return &Download{
		Name:        safeFileName(name) + format.Ext(),
		ContentType: format.ContentType(),
		WriteTo: func(w io.Writer) (int64, error) {
			return joinFiles(w, dir, format)
		},
	}, nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFileName(s string) string {
	s = strings.Trim(unsafeName.ReplaceAllString(s, "_"), "._")
	if s == "" {
		return "capture"
	}
	return s
}

// joinFiles writes the packet files of a capture directory, oldest first, as one file. A file that the ring deletes
// while the download runs is skipped.
func joinFiles(w io.Writer, dir string, format capture.Format) (int64, error) {
	var total int64
	names, err := packetFiles(dir)
	if err != nil {
		return total, err
	}
	first := true
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue // the ring deleted it
		}
		if err != nil {
			return total, err
		}
		n, err := capture.CopyComplete(w, f, format, !first && format == capture.PCAP)
		_ = f.Close()
		total += n
		if err != nil {
			return total, err
		}
		first = false
	}
	return total, nil
}

// packetFiles lists the packet files of a capture directory in the order they were written.
func packetFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if packetFile.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	// the numbers are compared as numbers: file 100000 comes after file 99999
	sort.Slice(names, func(i, j int) bool {
		return fileNumber(names[i]) < fileNumber(names[j])
	})
	return names, nil
}

// fileNumber is the number in the name of a file of a ring buffer, zero for the one file of a capture.
func fileNumber(name string) int {
	i, j := strings.IndexByte(name, '-'), strings.IndexByte(name, '.')
	if i < 0 || j < i {
		return 0
	}
	n, _ := strconv.Atoi(name[i+1 : j])
	return n
}
