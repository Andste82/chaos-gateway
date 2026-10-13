package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M12: the API serves the web app from --web-dir when given, else the embedded build (none in a
// binary built without the tag webui, as the unit tests are).
func TestWebUIComesFromTheDirectoryOrTheBinary(t *testing.T) {
	if ui, err := webUI(""); err != nil || ui != nil {
		t.Errorf("without the tag webui: %v %v", ui, err)
	}
	dir := t.TempDir()
	if _, err := webUI(dir); err == nil || !strings.Contains(err.Error(), "make build-web") {
		t.Errorf("a directory without a build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui, err := webUI(dir)
	if err != nil || ui == nil {
		t.Fatalf("%v %v", ui, err)
	}
	if b, err := fs.ReadFile(ui, "index.html"); err != nil || string(b) != "<!doctype html>" {
		t.Errorf("%q %v", b, err)
	}
}
