package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// idempotencyTTL is how long a key is kept (plan §2.15).
const idempotencyTTL = 24 * time.Hour

// stored is a remembered response.
type stored struct {
	Fingerprint string            `json:"fingerprint"`
	Status      int               `json:"status"`
	Header      map[string]string `json:"header,omitempty"`
	Body        []byte            `json:"body,omitempty"`
	At          time.Time         `json:"at"`
}

func (r *stored) write(c *gin.Context) {
	for k, v := range r.Header {
		c.Header(k, v)
	}
	c.Header("Idempotent-Replay", "true")
	c.Status(r.Status)
	_, _ = c.Writer.Write(r.Body)
}

type idempotency struct {
	clk  clock.Clock
	path string

	mu       sync.Mutex
	entries  map[string]*stored
	inflight map[string]chan struct{}
}

func openIdempotency(dir string, clk clock.Clock) (*idempotency, error) {
	i := &idempotency{clk: clk, entries: map[string]*stored{}, inflight: map[string]chan struct{}{}}
	if dir == "" {
		return i, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	i.path = filepath.Join(dir, "idempotency.json")
	raw, err := os.ReadFile(i.path)
	if errors.Is(err, os.ErrNotExist) {
		return i, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &i.entries); err != nil {
		i.entries = map[string]*stored{} // a corrupt cache only loses the replay
	}
	i.expire()
	return i, nil
}

func (i *idempotency) close() error { return nil }

func (i *idempotency) expire() {
	now := i.clk.Now()
	for k, e := range i.entries {
		if now.Sub(e.At) > idempotencyTTL {
			delete(i.entries, k)
		}
	}
}

func fingerprint(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			h.Write([]byte(v))
		case []byte:
			h.Write(v)
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// begin claims a key. A key whose first request is still running makes the second wait for it, so
// two concurrent requests with the same key do one thing. release stores the result of a request
// that claimed the key (only a success is kept: a retry after an error runs again).
func (i *idempotency) begin(key, fp string) (release func(*stored, string), replay *stored, conflict bool) {
	for {
		i.mu.Lock()
		i.expire()
		if e, ok := i.entries[key]; ok {
			i.mu.Unlock()
			if e.Fingerprint != fp {
				return nil, nil, true
			}
			return nil, e, false
		}
		if ch, ok := i.inflight[key]; ok {
			i.mu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		i.inflight[key] = ch
		i.mu.Unlock()
		return func(res *stored, fp string) {
			i.mu.Lock()
			defer i.mu.Unlock()
			delete(i.inflight, key)
			close(ch)
			if res != nil && res.Status >= 200 && res.Status < 300 {
				res.Fingerprint, res.At = fp, i.clk.Now()
				i.entries[key] = res
				i.persist()
			}
		}, nil, false
	}
}

func (i *idempotency) persist() {
	if i.path == "" {
		return
	}
	raw, err := json.Marshal(i.entries)
	if err != nil {
		return
	}
	tmp := i.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, i.path)
	}
}

// recorder keeps what a handler wrote, to store it.
type recorder struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

func (r *recorder) Write(b []byte) (int, error) {
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *recorder) WriteString(s string) (int, error) {
	r.buf.WriteString(s)
	return r.ResponseWriter.WriteString(s)
}

func (r *recorder) result(c *gin.Context) *stored {
	h := map[string]string{}
	for _, k := range []string{"Content-Type", "Location", "Chaos-Generation", "ETag"} {
		if v := r.Header().Get(k); v != "" {
			h[k] = v
		}
	}
	return &stored{Status: r.Status(), Header: h, Body: append([]byte(nil), r.buf.Bytes()...)}
}
