package ctl

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// The private key of an exported client is secret whatever was at the path before: an existing file keeps its mode
// when it is written in place, and a symbolic link is followed to somewhere else.
func TestWgExportWritesTheKeyWithMode0600OverAnExistingFileAndALink(t *testing.T) {
	f := &fakeGateway{t: t}
	dir := t.TempDir()

	existing := filepath.Join(dir, "laptop.conf")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o644); err != nil { // the umask may have taken bits
		t.Fatal(err)
	}
	if code, _, errb := f.run(t, nil, "wg", "export", "lab-wg", "laptop-1", "--out", existing); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if got := modeOf(t, existing); got != 0o600 {
		t.Errorf("an existing 0644 file ends with mode %v", got)
	}
	if b, _ := os.ReadFile(existing); !strings.Contains(string(b), "PrivateKey") {
		t.Errorf("the file holds %q", b)
	}

	// a symbolic link is replaced, the file it points to is not written
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.conf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := f.run(t, nil, "wg", "export", "lab-wg", "laptop-1", "--out", link); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Errorf("the target of the link was written: %q", b)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("the link is replaced by a file with mode 0600: %v %v", fi, err)
	}

	// a new file, and no left-over
	fresh := filepath.Join(dir, "new.conf")
	if code, _, errb := f.run(t, nil, "wg", "export", "lab-wg", "laptop-1", "-o", fresh); code != 0 || modeOf(t, fresh) != 0o600 {
		t.Fatalf("%d %s", code, errb)
	}
	if _, err := os.Stat(fresh + ".part"); err == nil {
		t.Error("a .part file is left")
	}
}

func TestAFileIsWrittenInPlaceOnlyWhenItIsNotARegularFile(t *testing.T) {
	// a failing write leaves the old file and no .part
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeFileAtomic(p, privateFileMode, func(w io.Writer) error {
		_, _ = w.Write([]byte("new but"))
		return errors.New("cut off")
	})
	if err == nil {
		t.Fatal("the failure is reported")
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Errorf("the file holds %q", b)
	}
	if _, err := os.Stat(p + ".part"); err == nil {
		t.Error("a .part file is left")
	}
	// a left-over .part of any mode is not written into
	if err := os.WriteFile(p+".part", []byte("stale"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, privateFileMode, func(w io.Writer) error { _, err := w.Write([]byte("fresh")); return err }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "fresh" || modeOf(t, p) != 0o600 {
		t.Errorf("%q %v", b, modeOf(t, p))
	}
	// /dev/null is not replaced by a file
	if err := writeFileAtomic(os.DevNull, privateFileMode, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(os.DevNull); err != nil || fi.Mode().IsRegular() {
		t.Errorf("%v %v", fi, err)
	}
}

// A download goes to disk as it arrives, not after it has arrived completely in memory; the name is decided from the
// headers; a failed one leaves neither the file nor a .part; what may hold secrets is private.
func TestAnArtifactIsStreamedToItsFileAndIsPrivate(t *testing.T) {
	release := make(chan struct{})
	first := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/captures/c1/download":
			w.Header().Set("Content-Disposition", `attachment; filename="c1.pcapng"`)
			w.Header().Set("Content-Length", "11")
			_, _ = w.Write([]byte("hello "))
			w.(http.Flusher).Flush()
			close(first)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte("world"))
		case "/api/v1/captures/cut/download":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("only a little"))
			w.(http.Flusher).Flush()
		case "/api/v1/runs/r/report.xml":
			_, _ = w.Write([]byte("<testsuites/>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl, err := newClient(connection{url: srv.URL, token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "made", "by", "chaosctl")

	done := make(chan error, 1)
	go func() {
		done <- fetchTo(t.Context(), cl, "/captures/c1/download", url.Values{}, out, "capture-c1", true, true)
	}()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the first chunk never came")
	}
	// the first chunk is on disk while the download is still going on
	part := filepath.Join(out, "capture-c1.pcapng.part")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(part); err == nil && string(b) == "hello " {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the download is buffered in memory: no .part file with the first chunk")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(out, "capture-c1.pcapng")
	if b, _ := os.ReadFile(final); string(b) != "hello world" {
		t.Errorf("%q", b)
	}
	if got := modeOf(t, final); got != 0o600 {
		t.Errorf("the capture has mode %v", got)
	}
	if got := modeOf(t, out); got != 0o700 {
		t.Errorf("the directory has mode %v", got)
	}
	if _, err := os.Stat(part); err == nil {
		t.Error("the .part file is left")
	}

	// a download that breaks off leaves nothing
	err = fetchTo(t.Context(), cl, "/captures/cut/download", nil, out, "capture-cut", true, true)
	if err == nil {
		t.Fatal("a cut-off download is an error")
	}
	for _, n := range []string{"capture-cut.pcap", "capture-cut.pcap.part"} {
		if _, err := os.Stat(filepath.Join(out, n)); err == nil {
			t.Errorf("%s is left", n)
		}
	}
	// an error answer creates no file either
	if err := fetchTo(t.Context(), cl, "/captures/none/download", nil, out, "capture-none", true, true); err == nil {
		t.Error("a 404 is an error")
	}
	if es, _ := os.ReadDir(out); len(es) != 1 {
		t.Errorf("the directory holds %d entries: %v", len(es), fmt.Sprint(es))
	}

	// a report is not secret: the mode of an ordinary file, in an ordinary directory
	rep := filepath.Join(t.TempDir(), "r")
	if err := fetchTo(t.Context(), cl, "/runs/r/report.xml", nil, rep, "report.xml", false, false); err != nil {
		t.Fatal(err)
	}
	umask := syscall.Umask(0)
	syscall.Umask(umask)
	if got, want := modeOf(t, filepath.Join(rep, "report.xml")), os.FileMode(0o644)&^os.FileMode(umask); got != want {
		t.Errorf("the report has mode %v, want %v", got, want)
	}
}
