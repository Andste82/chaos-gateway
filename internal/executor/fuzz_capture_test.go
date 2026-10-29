package executor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzCaptureOpen (M29, the review of the executor interface): a capture request is the one operation that opens raw sockets and
// writes files, so it has its own path through the executor (OpenCapture, not Decode and Plan). Whatever the request is, a refused
// one opens nothing, an accepted one names one directory below the capture root and interfaces the kernel can have, and none of them
// is served on an interface that is not assigned to Chaos Gateway: an executor with no assigned interface refuses every request,
// before it opens a socket or a file.
func FuzzCaptureOpen(f *testing.F) {
	for _, s := range []string{
		`{"dir":"c1","format":"pcapng","interfaces":[{"name":"br-iot","filter":{"protocol":"udp","ports":[{"from":53,"to":53}]}}]}`,
		`{"namespace":"cgp-3a1f7c52","dir":"c2","format":"pcap","snaplen":96,"interfaces":[{"name":"eth0","filter":{}}],"duration_ms":5000}`,
		`{"dir":"c3","format":"pcapng","interfaces":[{"name":"wg-hub","filter":{}},{"name":"br-lab","filter":{}}],"ring_files":4,"ring_file_size":1048576}`,
		`{"dir":"../etc","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"a/b","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"c4","format":"pcap","interfaces":[{"name":"lo; id"}]}`,
		`{"dir":"c5","format":"pcap","interfaces":[{"name":"lo"}],"ring_files":2,"ring_file_size":4611686018427387904}`,
		`{"dir":".","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"c6","format":"pcap","interfaces":[]}`,
		`{"dir":"c7","format":"pcap","interfaces":[{"name":"lo"},{"name":"lo"}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var o CaptureOpen
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&o) != nil {
			return
		}
		invalid := o.Validate()
		if invalid == nil {
			if strings.ContainsAny(o.Dir, "/\\\x00") || o.Dir == "." || o.Dir == ".." || strings.HasPrefix(o.Dir, ".") {
				t.Fatalf("a request for the directory %q is accepted", o.Dir)
			}
			for _, i := range o.Interfaces {
				if len(i.Name) > 15 || strings.ContainsAny(i.Name, "/ \t\n\x00;|&$`<>\"'\\") {
					t.Fatalf("a request for the interface %q is accepted", i.Name)
				}
			}
			if o.RingFiles > 0 && o.RingFileSize > maxCaptureBytes/int64(o.RingFiles) {
				t.Fatalf("a ring of %d files of %d bytes is accepted", o.RingFiles, o.RingFileSize)
			}
		}
		// no interface is assigned to this executor: nothing is opened, the refusal comes before the first socket
		e := newExec(t, &fakeRunner{}, WithCaptures(t.TempDir(), 10))
		s, err := e.OpenCapture(t.Context(), o)
		if err == nil {
			s.Close()
			t.Fatalf("a capture is started on an executor with no assigned interface: %s", raw)
		}
	})
}
