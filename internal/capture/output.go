package capture

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// ErrSizeLimit is returned by Output.Write when the packet would take a capture that is not a ring buffer past its
// size limit: the packet is not written.
var ErrSizeLimit = errors.New("capture: the size limit is reached")

// OutputConfig configures an Output.
type OutputConfig struct {
	// Root is the directory the files are made in; the Output never leaves it.
	Root   *os.Root
	Format Format
	// Base is the start of the file names: Base + ".pcapng", or Base-00001.pcapng, … for a ring buffer.
	Base   string
	Ifaces []InterfaceInfo
	// App names the writer in the section header of a pcapng file.
	App string
	// Files and FileSize make a ring buffer: at most Files files of about FileSize bytes, the oldest deleted when a
	// new one starts. Zero for a single file.
	Files    int
	FileSize int64
	// MaxSize bounds the size of a capture that is not a ring buffer, headers included; zero for no bound.
	MaxSize int64
	// UID and GID own the files; negative leaves them as the process makes them. A process that may not change
	// the owner leaves the files alone.
	UID, GID int
	// Mode of the files, 0o640 when zero.
	Mode os.FileMode
}

// Output is the file or the ring of files of one capture. It is safe for concurrent use.
type Output struct {
	cfg OutputConfig

	mu      sync.Mutex
	f       *os.File
	w       *Writer
	name    string
	seq     int
	files   []string         // existing files, oldest first; the last one is being written
	sizes   map[string]int64 // sizes of the closed files
	packets int64            // of all files, also the ones the ring deleted
	closed  bool
}

// NewOutput creates the first file.
func NewOutput(cfg OutputConfig) (*Output, error) {
	if cfg.Root == nil {
		return nil, errors.New("capture: an output needs a directory")
	}
	if cfg.Base == "" {
		cfg.Base = "capture"
	}
	if cfg.Mode == 0 {
		cfg.Mode = 0o640
	}
	if cfg.Files == 1 || cfg.Files < 0 || (cfg.Files > 0 && cfg.FileSize <= 0) {
		return nil, errors.New("capture: a ring buffer has at least two files of a size")
	}
	o := &Output{cfg: cfg, sizes: map[string]int64{}}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.openNext(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Output) ring() bool { return o.cfg.Files > 0 }

// openNext starts the next file of the ring, or the one file.
func (o *Output) openNext() error {
	o.seq++
	name := o.cfg.Base + o.cfg.Format.Ext()
	if o.ring() {
		name = fmt.Sprintf("%s-%05d%s", o.cfg.Base, o.seq, o.cfg.Format.Ext())
	}
	f, err := o.cfg.Root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, o.cfg.Mode)
	if err != nil {
		return err
	}
	if o.cfg.UID >= 0 || o.cfg.GID >= 0 {
		if err := f.Chown(o.cfg.UID, o.cfg.GID); err != nil && !errors.Is(err, os.ErrPermission) {
			_ = f.Close()
			return err
		}
	}
	w, err := NewWriter(f, o.cfg.Format, o.cfg.Ifaces, o.cfg.App)
	if err != nil {
		_ = f.Close()
		return err
	}
	o.f, o.w, o.name = f, w, name
	o.files = append(o.files, name)
	return nil
}

// closeCurrent flushes and closes the file being written and remembers its size.
func (o *Output) closeCurrent() error {
	if o.f == nil {
		return nil
	}
	err := o.w.Flush()
	o.sizes[o.name] = o.w.Size()
	if cerr := o.f.Close(); err == nil {
		err = cerr
	}
	o.f, o.w = nil, nil
	return err
}

// Write appends a packet, starting the next file of a ring buffer first when the packet does not fit the
// current one. It returns ErrSizeLimit, and writes nothing, for a packet that would take a capture that is not a
// ring buffer past its limit.
func (o *Output) Write(p Packet) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return os.ErrClosed
	}
	size := int64(PacketSize(o.cfg.Format, len(p.Data), true))
	if o.ring() {
		if o.w.Size()+size > o.cfg.FileSize && o.w.Packets() > 0 {
			if err := o.rotate(); err != nil {
				return err
			}
		}
	} else if o.cfg.MaxSize > 0 && o.w.Size()+size > o.cfg.MaxSize {
		return ErrSizeLimit
	}
	if err := o.w.WritePacket(p); err != nil {
		return err
	}
	o.packets++
	return nil
}

func (o *Output) rotate() error {
	if err := o.closeCurrent(); err != nil {
		return err
	}
	if err := o.openNext(); err != nil {
		return err
	}
	for len(o.files) > o.cfg.Files {
		old := o.files[0]
		o.files = o.files[1:]
		delete(o.sizes, old)
		if err := o.cfg.Root.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Flush hands the buffered packets to the file.
func (o *Output) Flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.w == nil {
		return nil
	}
	return o.w.Flush()
}

// Close flushes and closes the file; the Output takes no more packets.
func (o *Output) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	return o.closeCurrent()
}

// Size is the number of bytes of the files that exist, also the ones still in the buffer.
func (o *Output) Size() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	var n int64
	for _, s := range o.sizes {
		n += s
	}
	if o.w != nil {
		n += o.w.Size()
	}
	return n
}

// Packets is the number of packets written, also those of files the ring has deleted.
func (o *Output) Packets() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.packets
}

// Files returns the names of the files that exist, oldest first.
func (o *Output) Files() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.files...)
}
