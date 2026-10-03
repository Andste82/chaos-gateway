package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

const id = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"

func TestKeysAreStoredPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuard(id); err != ErrNotFound {
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
	if _, err := s.WireGuard(id); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestOnlyAUUIDBecomesAFileName(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, bad := range []string{"", "../x", "a/b", "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f2", "0B7C6A3E-1F2D-4C5B-9A8E-7D6C5B4A3F21"} {
		if _, err := s.WireGuard(bad); err == nil || err == ErrNotFound {
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
	if _, err := s.WireGuard(id); err == nil || err == ErrNotFound {
		t.Fatalf("got %v", err)
	}
}
