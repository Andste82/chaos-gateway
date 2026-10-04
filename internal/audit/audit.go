// Package audit is the append-only audit log (plan §2.16): who did what, when and through which
// channel. It is a file of JSON lines, one entry per line, flushed to disk with every entry; the
// API reads it back with cursor pagination. Secrets never enter it: callers pass what happened,
// not the request.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Entry is one line of the log.
type Entry struct {
	// ID is the sequence number as a decimal string, assigned by Append; ids grow without gaps.
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Actor  Actor     `json:"actor"`
	Via    string    `json:"via"` // ui | api | cli | system
	Action string    `json:"action"`
	Object *Object   `json:"object,omitempty"`
	// Revision is set for entries about a revision.
	Revision int64  `json:"revision,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Actor is who acted.
type Actor struct {
	Type string `json:"type"` // user | token | run | system
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Object is what the action was about.
type Object struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Log is the audit log. It is safe for concurrent use.
type Log struct {
	mu   sync.Mutex
	f    *os.File
	clk  clock.Clock
	next uint64
	// entries is the whole log in memory: the log is small (one line per administrative action).
	entries []Entry
	path    string
	// retention is how long an entry is kept (§3.6: 1 year); zero uses DefaultRetention.
	retention time.Duration
	// err is the error of the last failed Append, nil after a successful one (M5-04).
	err error
}

// Option configures Open.
type Option func(*Log)

// WithRetention overrides DefaultRetention (tests use a short one).
func WithRetention(d time.Duration) Option {
	return func(l *Log) { l.retention = d }
}

// DefaultRetention is §3.6's one year, used when Open is not given one.
const DefaultRetention = 365 * 24 * time.Hour

// maxEntries bounds the file: when it grows beyond, the oldest half is dropped at the next start.
const maxEntries = 100000

// Open opens or creates the log file in dir.
func Open(dir string, c clock.Clock, opts ...Option) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "audit.jsonl")
	l := &Log{clk: c, next: 1, path: path, retention: DefaultRetention}
	for _, o := range opts {
		o(l)
	}
	if raw, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(raw)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue // a torn last line after a crash
			}
			l.entries = append(l.entries, e)
		}
		_ = raw.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if l.pruneEntries() {
		if err := rewrite(path, l.entries); err != nil {
			return nil, err
		}
	}
	if n := len(l.entries); n > 0 {
		last, _ := strconv.ParseUint(l.entries[n-1].ID, 10, 64)
		l.next = last + 1
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	l.f = f
	return l, nil
}

// pruneEntries drops entries older than retention and, if the log still exceeds maxEntries, the
// oldest half of what remains. It reports whether anything was dropped. Callers hold l.mu.
func (l *Log) pruneEntries() bool {
	before := len(l.entries)
	if l.retention > 0 {
		cut := l.clk.Now().Add(-l.retention)
		i := 0
		for i < len(l.entries) && l.entries[i].Time.Before(cut) {
			i++
		}
		l.entries = l.entries[i:]
	}
	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries/2:]
	}
	return len(l.entries) != before
}

// Run drops entries past retention once a day, until ctx ends (§3.6: retention 1 year).
func (l *Log) Run(ctx context.Context) {
	t := l.clk.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			l.mu.Lock()
			changed := l.pruneEntries()
			entries := append([]Entry(nil), l.entries...)
			l.mu.Unlock()
			if !changed {
				continue
			}
			if err := rewrite(l.path, entries); err != nil {
				l.mu.Lock()
				l.err = err
				l.mu.Unlock()
			}
		}
	}
}

func rewrite(path string, es []Entry) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, e := range es {
		b, _ := json.Marshal(e)
		if _, err := f.Write(append(b, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Close closes the file.
func (l *Log) Close() error { return l.f.Close() }

// Append adds an entry; it sets ID and Time and returns the entry as stored.
func (l *Log) Append(e Entry) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.ID = strconv.FormatUint(l.next, 10)
	e.Time = l.clk.Now().UTC()
	if e.Via == "" {
		e.Via = "system"
	}
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	// a write or fsync failure (plan §3.11: a failing audit writer marks the API unhealthy) is kept
	// apart from a marshal error, which is the caller's bug, not the log's health
	if _, werr := l.f.Write(append(b, '\n')); werr != nil {
		l.err = werr
		return e, werr
	}
	if werr := l.f.Sync(); werr != nil {
		l.err = werr
		return e, werr
	}
	l.err = nil
	l.next++
	l.entries = append(l.entries, e)
	return e, nil
}

// Err returns the error of the last failed append, or nil after a successful one (M5-04).
func (l *Log) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Filter selects entries.
type Filter struct {
	Action string
	Actor  string
	Since  time.Time
	Until  time.Time
}

func (f Filter) match(e Entry) bool {
	return (f.Action == "" || e.Action == f.Action) && (f.Actor == "" || e.Actor.ID == f.Actor || e.Actor.Name == f.Actor) &&
		(f.Since.IsZero() || !e.Time.Before(f.Since)) && (f.Until.IsZero() || e.Time.Before(f.Until))
}

// List returns up to limit entries, newest first. cursor is the id of the last entry of the
// previous page ("" for the first); next is the cursor of the following page, "" at the end.
func (l *Log) List(f Filter, cursor string, limit int) (items []Entry, next string, err error) {
	before := ^uint64(0)
	if cursor != "" {
		before, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor %q", cursor)
		}
	}
	if limit <= 0 {
		limit = 100
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		e := l.entries[i]
		id, _ := strconv.ParseUint(e.ID, 10, 64)
		if id >= before || !f.match(e) {
			continue
		}
		if len(items) == limit {
			return items, items[len(items)-1].ID, nil
		}
		items = append(items, e)
	}
	return items, "", nil
}
