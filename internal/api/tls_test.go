package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishCertificateCopiesOnlyTheCertificate(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCertificate(dir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "service", "api.pem")
	if err := PublishCertificate(dir, dst); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the published certificate does not match cert.pem")
	}
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestPublishCertificateOverwritesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCertificate(dir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "api.pem")
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishCertificate(dir, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "stale" {
		t.Errorf("the stale certificate was not replaced")
	}
}
