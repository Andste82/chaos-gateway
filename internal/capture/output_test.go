package capture

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func openRoot(t testing.TB) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func bigPacket(i int, size int) Packet {
	p := pk{eth: true, src: macDev, dst: macGW, sa: "10.10.0.11", da: "203.0.113.10", proto: protoUDP, sp: uint16(1000 + i), dp: 53, payload: size - 42}.build()
	return Packet{Iface: 0, Time: t0, Data: p, OrigLen: len(p)}
}

func TestAnOutputWithASizeLimitStopsBeforeItWouldPassIt(t *testing.T) {
	for _, format := range []Format{PCAP, PCAPNG} {
		root, dir := openRoot(t)
		limit := int64(10_000)
		o, err := NewOutput(OutputConfig{Root: root, Format: format, Ifaces: []InterfaceInfo{ethIface}, MaxSize: limit, UID: -1, GID: -1})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for {
			err := o.Write(bigPacket(n, 500))
			if errors.Is(err, ErrSizeLimit) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if err := o.Close(); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(filepath.Join(dir, "capture"+format.Ext()))
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > limit || st.Size() < limit-600 {
			t.Errorf("%s: the file has %d bytes for a limit of %d", format, st.Size(), limit)
		}
		if o.Size() != st.Size() || o.Packets() != int64(n) || n < 10 {
			t.Errorf("%s: size %d (file %d), %d packets (%d written)", format, o.Size(), st.Size(), o.Packets(), n)
		}
		if err := o.Write(bigPacket(0, 100)); err == nil {
			t.Errorf("%s: a closed output takes a packet", format)
		}
	}
}

func TestARingKeepsTheNewestFilesAndEveryFileStandsAlone(t *testing.T) {
	for _, format := range []Format{PCAP, PCAPNG} {
		root, dir := openRoot(t)
		o, err := NewOutput(OutputConfig{Root: root, Format: format, Ifaces: []InterfaceInfo{ethIface}, Files: 3, FileSize: 5000, UID: -1, GID: -1})
		if err != nil {
			t.Fatal(err)
		}
		const total = 100
		for i := 0; i < total; i++ {
			if err := o.Write(bigPacket(i, 400)); err != nil {
				t.Fatal(err)
			}
		}
		files := o.Files()
		if len(files) != 3 {
			t.Fatalf("%s: %d files in the ring: %v", format, len(files), files)
		}
		if err := o.Close(); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 3 {
			t.Fatalf("%s: %d files on disk", format, len(entries))
		}
		var joined bytes.Buffer
		var sum int64
		for i, name := range files {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			sum += int64(len(b))
			if int64(len(b)) > 5000 {
				t.Errorf("%s: %s has %d bytes for a file size of 5000", format, name, len(b))
			}
			if _, got := readAll(t, b); len(got) == 0 {
				t.Errorf("%s: %s holds no packets", format, name)
			}
			if _, err := CopyComplete(&joined, bytes.NewReader(b), format, i > 0); err != nil {
				t.Fatal(err)
			}
		}
		if o.Size() != sum {
			t.Errorf("%s: the output says %d bytes, the files have %d", format, o.Size(), sum)
		}
		if o.Packets() != total {
			t.Errorf("%s: %d packets counted, %d written", format, o.Packets(), total)
		}
		// the newest packets are in the ring, the oldest are gone
		_, got := readAll(t, joined.Bytes())
		last := got[len(got)-1]
		if want := bigPacket(total-1, 400).Data; !bytes.Equal(last.Data, want) {
			t.Errorf("%s: the ring does not end with the newest packet", format)
		}
		if bytes.Equal(got[0].Data, bigPacket(0, 400).Data) {
			t.Errorf("%s: the ring still has the oldest packet", format)
		}
		if len(got) >= total {
			t.Errorf("%s: the ring kept %d of %d packets", format, len(got), total)
		}
		if files[0] == "capture-00001"+format.Ext() {
			t.Errorf("%s: the first file of the ring was not deleted: %v", format, files)
		}
	}
}

func TestAPacketLargerThanTheFileGetsAFileOfItsOwn(t *testing.T) {
	root, _ := openRoot(t)
	o, err := NewOutput(OutputConfig{Root: root, Format: PCAPNG, Ifaces: []InterfaceInfo{ethIface}, Files: 2, FileSize: 800, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	for i := 0; i < 4; i++ {
		if err := o.Write(bigPacket(i, 1400)); err != nil {
			t.Fatal(err)
		}
	}
	if o.Packets() != 4 || len(o.Files()) != 2 {
		t.Errorf("%d packets, files %v", o.Packets(), o.Files())
	}
}

func TestOutputConfigurationIsChecked(t *testing.T) {
	root, _ := openRoot(t)
	for name, cfg := range map[string]OutputConfig{
		"no directory":   {Format: PCAP, Ifaces: []InterfaceInfo{ethIface}},
		"a ring of one":  {Root: root, Format: PCAP, Ifaces: []InterfaceInfo{ethIface}, Files: 1, FileSize: 100},
		"a ring of size": {Root: root, Format: PCAP, Ifaces: []InterfaceInfo{ethIface}, Files: 2},
		"no interface":   {Root: root, Format: PCAP},
	} {
		if _, err := NewOutput(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAnOutputNeverOverwritesOrFollowsWhatIsThere(t *testing.T) {
	root, dir := openRoot(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "capture.pcapng")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOutput(OutputConfig{Root: root, Format: PCAPNG, Ifaces: []InterfaceInfo{ethIface}, UID: -1, GID: -1}); err == nil {
		t.Fatal("an output is made over a symbolic link")
	}
	if b, _ := os.ReadFile(outside); !bytes.Equal(b, []byte("keep")) {
		t.Errorf("the file behind the link was written: %q", b)
	}
	// a file of an earlier capture is not overwritten either
	if err := os.WriteFile(filepath.Join(dir, "capture.pcap"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOutput(OutputConfig{Root: root, Format: PCAP, Ifaces: []InterfaceInfo{ethIface}, UID: -1, GID: -1}); !errors.Is(err, os.ErrExist) {
		t.Errorf("an existing file: %v, want ErrExist", err)
	}
}

func TestTheFileCanBeReadWhileItIsWritten(t *testing.T) {
	root, dir := openRoot(t)
	o, err := NewOutput(OutputConfig{Root: root, Format: PCAPNG, Ifaces: []InterfaceInfo{ethIface}, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	for i := 0; i < 5; i++ {
		_ = o.Write(bigPacket(i, 300))
	}
	if err := o.Flush(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "capture.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out bytes.Buffer
	if _, err := CopyComplete(&out, f, PCAPNG, false); err != nil {
		t.Fatal(err)
	}
	if _, got := readAll(t, out.Bytes()); len(got) != 5 {
		t.Errorf("%d packets of a running capture, want 5", len(got))
	}
	_, _ = f.Seek(0, io.SeekStart)
}

func TestAFailedRotationIsStickyAndLeavesNoNilWriter(t *testing.T) {
	root, dir := openRoot(t)
	o, err := NewOutput(OutputConfig{Root: root, Format: PCAPNG, Ifaces: []InterfaceInfo{ethIface}, Files: 2, FileSize: 2000, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	// the name of the second file is taken: the first rotation cannot open it
	if err := os.WriteFile(filepath.Join(dir, "capture-00002.pcapng"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var first error
	for i := 0; i < 20 && first == nil; i++ {
		first = o.Write(bigPacket(i, 400))
	}
	if !errors.Is(first, os.ErrExist) {
		t.Fatalf("the failed rotation: %v, want ErrExist", first)
	}
	written := o.Packets()
	// before the fix the second write evaluated the size of a writer that was nil
	for i := 0; i < 3; i++ {
		if err := o.Write(bigPacket(i, 400)); !errors.Is(err, os.ErrExist) {
			t.Errorf("write %d after the failed rotation: %v, want the error of the rotation", i, err)
		}
	}
	if o.Packets() != written {
		t.Errorf("%d packets counted after the failed rotation, were %d", o.Packets(), written)
	}
	if err := o.Flush(); !errors.Is(err, os.ErrExist) {
		t.Errorf("flush after the failed rotation: %v, want the error of the rotation", err)
	}
	if err := o.Close(); !errors.Is(err, os.ErrExist) {
		t.Errorf("close after the failed rotation: %v, want the error of the rotation", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "capture-00002.pcapng")); string(b) != "old" {
		t.Errorf("the file of the next number was written: %q", b)
	}
	// what was written before the rotation failed is a complete file
	b, err := os.ReadFile(filepath.Join(dir, "capture-00001.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if _, got := readAll(t, b); int64(len(got)) != written {
		t.Errorf("the first file holds %d packets, %d were written", len(got), written)
	}
}

func TestAPanicInAGoroutineOfASessionEndsOnlyThatSession(t *testing.T) {
	s := &Session{done: make(chan struct{}), cancel: func() {}}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer s.recoverPanic("read")
		var o *Output
		_ = o.Write(Packet{})
	}()
	<-finished
	r := s.Result()
	if r.Reason != ReasonError || r.Err == nil || !s.stopping.Load() {
		t.Errorf("result %+v, stopping %v: a panic must end the session with an error", r, s.stopping.Load())
	}
}
