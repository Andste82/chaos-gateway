package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const id = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"

func TestKeysAreStoredPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuard(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	k := WireGuardKeys{PrivateKey: "priv", PresharedKey: "psk", Generation: 3}
	if err := s.PutWireGuard(id, k); err != nil {
		t.Fatal(err)
	}
	got, err := s.WireGuard(id)
	if err != nil || got != k {
		t.Fatalf("%+v %v", got, err)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "wireguard"): 0o700, filepath.Join(dir, "wireguard", id+".json"): 0o600} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", p, st, err, want)
		}
	}
	if ids, _ := s.IDs(); len(ids) != 1 || ids[0] != id {
		t.Errorf("ids %v", ids)
	}
	if err := s.DeleteWireGuard(id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWireGuard(id); err != nil {
		t.Fatalf("deleting what is gone: %v", err)
	}
	if _, err := s.WireGuard(id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestOnlyAUUIDBecomesAFileName(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, bad := range []string{"", "../x", "a/b", "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f2", "0B7C6A3E-1F2D-4C5B-9A8E-7D6C5B4A3F21"} {
		if _, err := s.WireGuard(bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
		if err := s.PutWireGuard(bad, WireGuardKeys{}); err == nil {
			t.Errorf("%q stored", bad)
		}
	}
}

func TestOpenTightensAnExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestACorruptFileIsAnError(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := os.WriteFile(filepath.Join(s.dir, "wireguard", id+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuard(id); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenReadOnlyCreatesAndChangesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenReadOnly(dir); err == nil {
		t.Fatal("a directory without secrets is an error: nothing is created")
	}
	if _, err := os.Stat(filepath.Join(dir, "wireguard")); err == nil {
		t.Fatal("OpenReadOnly created a directory")
	}
	rw, _ := Open(dir)
	if err := rw.PutWireGuard(id, WireGuardKeys{PrivateKey: "p"}); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := ro.WireGuard(id); err != nil || k.PrivateKey != "p" {
		t.Fatalf("%+v %v", k, err)
	}
	// a directory that others can read is refused: the secrets are not protected
	if err := os.Chmod(filepath.Join(dir, "wireguard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(dir); err == nil {
		t.Fatal("an open directory must be refused")
	}
	if st, _ := os.Stat(filepath.Join(dir, "wireguard")); st.Mode().Perm() != 0o755 {
		t.Error("OpenReadOnly changed the mode")
	}
}

func TestConcurrentWritersDoNotCollide(t *testing.T) {
	s, _ := Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.PutWireGuard(id, WireGuardKeys{PrivateKey: "k", Generation: i}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.WireGuard(id); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Join(s.dir, "wireguard"))
	if len(ents) != 1 {
		t.Errorf("%d files: no temporary file is left behind", len(ents))
	}
	if ids, _ := s.IDs(); len(ids) != 1 {
		t.Errorf("%v", ids)
	}
}
