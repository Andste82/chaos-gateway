package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/capture"
)

// What a capture request is refused for, before any socket is opened: these tests need no privilege.

// capExec is an executor with a capture directory and the loopback interface assigned.
func capExec(t *testing.T, opts ...Option) (*Executor, string) {
	t.Helper()
	dir := t.TempDir()
	e := newExec(t, &fakeRunner{}, append([]Option{WithCaptures(dir, 10)}, opts...)...)
	e.Scope().Set([]string{"lo"})
	return e, dir
}

func mkCapDir(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func loOpen(dir string) CaptureOpen {
	return CaptureOpen{Dir: dir, Format: capture.PCAPNG, Interfaces: []CaptureInterface{{Name: "lo", Filter: capture.Filter{Protocol: capture.UDP, Ports: []capture.PortRange{{From: 40404, To: 40404}}}}}}
}

func TestACaptureIsRefusedOutsideTheScopeAndForBadRequests(t *testing.T) {
	e, root := capExec(t)
	mkCapDir(t, root, "c1")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mod  func(*CaptureOpen)
		want string
	}{
		"an interface that is not assigned": {func(o *CaptureOpen) { o.Interfaces[0].Name = "eth0" }, "not assigned"},
		"a path in the directory name":      {func(o *CaptureOpen) { o.Dir = "../c1" }, "invalid capture directory"},
		"a slash in the directory name":     {func(o *CaptureOpen) { o.Dir = "a/b" }, "invalid capture directory"},
		"dot dot":                           {func(o *CaptureOpen) { o.Dir = ".." }, "invalid capture directory"},
		"a directory that does not exist":   {func(o *CaptureOpen) { o.Dir = "nodir" }, "directory of the capture"},
		"a link out of the root":            {func(o *CaptureOpen) { o.Dir = "evil" }, "directory of the capture"},
		"no interface":                      {func(o *CaptureOpen) { o.Interfaces = nil }, "1 to"},
		"an interface twice":                {func(o *CaptureOpen) { o.Interfaces = append(o.Interfaces, o.Interfaces[0]) }, "twice"},
		"an interface name with a slash":    {func(o *CaptureOpen) { o.Interfaces[0].Name = "a/b" }, "invalid interface name"},
		"a namespace with a slash":          {func(o *CaptureOpen) { o.Namespace = "../x" }, "invalid namespace"},
		"an unknown format":                 {func(o *CaptureOpen) { o.Format = "text" }, "format"},
		"a ring of one file":                {func(o *CaptureOpen) { o.RingFiles, o.RingFileSize = 1, 100 }, "2 to 100"},
		"a ring without a file size":        {func(o *CaptureOpen) { o.RingFiles = 3 }, "file size"},
		"a ring with a maximum size":        {func(o *CaptureOpen) { o.RingFiles, o.RingFileSize, o.MaxSize = 3, 1000, 5 }, "maximum size"},
		"a file size without a ring":        {func(o *CaptureOpen) { o.RingFileSize = 100 }, "needs a ring"},
		"a negative duration":               {func(o *CaptureOpen) { o.DurationMS = -1 }, "negative"},
		"a short snap length":               {func(o *CaptureOpen) { o.Snaplen = 10 }, "snap length"},
		"a bad filter":                      {func(o *CaptureOpen) { o.Interfaces[0].Filter.Protocol = "sctp" }, "protocol"},
	}
	for name, c := range cases {
		o := loOpen("c1")
		c.mod(&o)
		s, err := e.OpenCapture(context.Background(), o)
		if err == nil {
			s.Close()
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if !errors.Is(func() error {
		_, err := e.OpenCapture(context.Background(), CaptureOpen{Dir: "c1", Format: capture.PCAP, Interfaces: []CaptureInterface{{Name: "eth0"}}})
		return err
	}(), ErrOutOfScope) {
		t.Error("an interface that is not assigned is not out of scope")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("a capture wrote below a link that leads out of the root: %d files", len(entries))
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "c1")); len(entries) != 0 {
		t.Errorf("a refused capture left %d files", len(entries))
	}
	// an executor without a directory refuses captures
	bare := newExec(t, &fakeRunner{})
	bare.Scope().Set([]string{"lo"})
	if _, err := bare.OpenCapture(context.Background(), loOpen("c1")); !errors.Is(err, ErrNoCaptureDir) {
		t.Errorf("an executor without a capture directory: %v", err)
	}
}
