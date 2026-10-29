package executor

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// treeOf lists every path below dir, to compare before and after.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var all []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		all = append(all, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// FuzzCaptureOpenFiles (M29, the review of the executor interface): the interfaces the request names are assigned, so the request
// goes past the scope check to the capture directory. The capture root has a real subdirectory and two links that point out of it
// (to a directory of the test and to its own parent); whatever the request names, nothing is created or changed outside the capture
// root, and a link out of it is never followed (`os.OpenRoot`, the mitigation of the threat model). Without the privilege for a raw
// socket (level 0) the capture fails after the directory is opened and before a file is made; with it a capture may start, and it
// is stopped at once.
func FuzzCaptureOpenFiles(f *testing.F) {
	for _, s := range []string{
		`{"dir":"c1","format":"pcapng","interfaces":[{"name":"lo","filter":{"protocol":"udp","ports":[{"from":40404,"to":40404}]}}]}`,
		`{"dir":"link","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"uplink","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"missing","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"c1","format":"pcapng","interfaces":[{"name":"lo"}],"ring_files":2,"ring_file_size":65536,"duration_ms":50}`,
		`{"dir":"..","format":"pcap","interfaces":[{"name":"lo"}]}`,
		`{"dir":"c1/../../x","format":"pcap","interfaces":[{"name":"lo"}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var o CaptureOpen
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&o) != nil || o.Validate() != nil {
			return
		}
		base := t.TempDir()
		root, outside := filepath.Join(base, "captures"), filepath.Join(base, "outside")
		for _, d := range []string{root, outside, filepath.Join(root, "c1")} {
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("..", filepath.Join(root, "uplink")); err != nil {
			t.Fatal(err)
		}
		// the directory step on its own, which fails the same way with and without the privilege for a raw socket: a link out of the
		// capture root is refused there, a real subdirectory is opened
		d, derr := openCaptureDir(root, o.Dir)
		if derr == nil {
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if (o.Dir == "link" || o.Dir == "uplink") && derr == nil {
			t.Fatalf("the directory step followed a link out of the capture root: %s", raw)
		}
		if o.Dir == "c1" && derr != nil {
			t.Fatalf("the directory step refused the subdirectory of the capture root: %v: %s", derr, raw)
		}
		before := treeOf(t, base)
		e := newExec(t, &fakeRunner{}, WithCaptures(root, 10))
		names := make([]string, 0, len(o.Interfaces))
		for _, i := range o.Interfaces {
			names = append(names, i.Name)
		}
		e.Scope().Set(names)
		s, err := e.OpenCapture(t.Context(), o)
		if err == nil {
			s.Close()
		}
		if (o.Dir == "link" || o.Dir == "uplink") && err == nil {
			t.Fatalf("a link out of the capture root was followed: %s", raw)
		}
		// whatever was made, it is below the capture root: the tree outside it is as it was
		for _, p := range treeOf(t, base) {
			if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
				continue
			}
			if !slices.Contains(before, p) {
				t.Fatalf("%s was created outside the capture root by %s", p, raw)
			}
		}
		if got := treeOf(t, outside); len(got) != 1 {
			t.Fatalf("the directory behind the link holds %v after %s", got, raw)
		}
		for _, p := range treeOf(t, root) {
			if rel, _ := filepath.Rel(root, p); strings.Count(rel, string(filepath.Separator)) > 1 {
				t.Fatalf("%s is deeper than one directory below the capture root", p)
			}
		}
	})
}
