package capturemgr

import (
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The TLS key log of a capture (plan §2.8, §2.11, M22): the interception proxy gives the gateway the secrets of its
// connections with the devices (and with the servers), attributed to the client, and the capture that runs while a
// device talks keeps them in `keylog.txt` next to its packet files, in the NSS format Wireshark reads. A capture that has
// received secrets is marked `keylog` and `sensitive` (anyone who has the file and the capture can read the traffic), and
// the download of either needs the scope `full`. Secrets of a connection that no capture saw are not kept: there is nothing
// they could decrypt.

const keylogName = "keylog.txt"

// keylogState is the memo of the world of a snapshot, so a batch of key lines does not rebuild it for every capture.
type keylogState struct {
	mu    sync.Mutex
	snap  *engine.Snapshot
	world *domain.World
}

func (k *keylogState) worldOf(snap *engine.Snapshot) *domain.World {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.snap != snap {
		w, err := domain.NewWorld(snap.Config, nil)
		if err != nil {
			w = nil
		}
		k.snap, k.world = snap, w
	}
	return k.world
}

// AppendKeylog gives the lines of a key log (NSS format, no line breaks) of a connection of the client to every running
// capture whose scope covers the client. A capture that has ended, or is ending, does not take them.
func (m *Manager) AppendKeylog(client netip.Addr, lines []string) {
	if len(lines) == 0 {
		return
	}
	snap := m.cfg.Gateway.Snapshot()
	if snap == nil || snap.Config == nil {
		return
	}
	w := m.keyWorld.worldOf(snap)
	m.mu.Lock()
	var targets []*capState
	for _, c := range m.caps {
		if c.rec.Status == model.CaptureStatusRunning && !c.ending {
			targets = append(targets, c)
		}
	}
	m.mu.Unlock()
	for _, c := range targets {
		if Covers(snap, w, c.rec.Scope, client) {
			m.appendKeylog(c, lines)
		}
	}
}

func (m *Manager) appendKeylog(c *capState, lines []string) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	f, err := os.OpenFile(filepath.Join(c.dir, keylogName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		m.log.Warn("cannot open the key log of a capture", "capture", c.id, "error", err)
		return
	}
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	_, werr := f.Write(buf)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		m.log.Warn("cannot write the key log of a capture", "capture", c.id, "error", werr)
		return
	}
	m.mu.Lock()
	first := !deref(c.rec.Keylog)
	if first && !c.ending {
		c.rec.Keylog, c.rec.Sensitive = ptr(true), ptr(true)
	}
	m.mu.Unlock()
	if first {
		m.save(c)
	}
}

// openKeylog returns the key log of a capture for download.
func (m *Manager) openKeylog(c *capState, rec model.Capture, name string) (*Download, error) {
	if !deref(rec.Keylog) {
		return nil, ErrNoKeylog
	}
	path := filepath.Join(c.dir, keylogName)
	return &Download{
		Name:        safeFileName(name) + "-keylog.txt",
		ContentType: "text/plain",
		WriteTo: func(w io.Writer) (int64, error) {
			c.keyMu.Lock()
			defer c.keyMu.Unlock()
			f, err := os.Open(path)
			if err != nil {
				return 0, err
			}
			defer func() { _ = f.Close() }()
			return io.Copy(w, f)
		},
	}, nil
}
