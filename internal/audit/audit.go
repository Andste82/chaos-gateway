// Package audit is the append-only audit log (plan §2.16): who did what, when and through which
// channel. It is a file of JSON lines, one entry per line, flushed to disk with every entry; the
// API reads it back with cursor pagination. Secrets never enter it: callers pass what happened,
// not the request.
package audit

import (
	"bufio"
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
}

// maxEntries bounds the file: when it grows beyond, the oldest half is dropped at the next start.
const maxEntries = 100000

// Open opens or creates the log file in dir.
func Open(dir string, c clock.Clock) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "audit.jsonl")
	l := &Log{clk: c, next: 1}
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
	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries/2:]
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
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return e, err
	}
	if err := l.f.Sync(); err != nil {
		return e, err
	}
	l.next++
	l.entries = append(l.entries, e)
	return e, nil
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
	var before uint64 = ^uint64(0)
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
