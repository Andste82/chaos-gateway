package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A data directory with the wrong owner (a restore as root) made the API crash-loop with a bare error of the file system: it checks the
// directories it owns when it starts and names the directory, its owner and the way to put it right.
func TestTheAPIRefusesToStartWhenADirectoryItOwnsCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory whose mode forbids it")
	}
	root := t.TempDir()
	dirs := map[string]string{}
	for _, name := range []string{"state", "api", "service", "tlsca"} {
		dirs[name] = filepath.Join(root, name)
		if err := os.Mkdir(dirs[name], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := apiOptions{stateDir: dirs["state"], dataDir: dirs["api"], tlsCADir: dirs["tlsca"], serviceToken: filepath.Join(dirs["service"], "token")}
	if err := checkOwnedDirs(o); err != nil {
		t.Fatalf("directories the API can write: %v", err)
	}
	for _, name := range dirs {
		if left, _ := filepath.Glob(filepath.Join(name, ".write-check-*")); len(left) != 0 {
			t.Errorf("the check left %v behind", left)
		}
	}

	for name, dir := range dirs {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		err := checkOwnedDirs(o)
		if err == nil {
			t.Errorf("%s: a directory the API cannot write was accepted", name)
		} else if msg := err.Error(); !strings.Contains(msg, dir+" (") || !strings.Contains(msg, "mode 0555") || !strings.Contains(msg, "user "+strconv.Itoa(os.Geteuid())) ||
			!strings.Contains(msg, "sudo sh /opt/chaosgw/host-setup.sh --data-only") {
			t.Errorf("%s: the message does not name the directory, its owner and the way out: %v", name, err)
		}
		// serveAPI stops at it, before it opens anything else
		if err := serveAPI(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), io.Discard, o); err == nil || !strings.Contains(err.Error(), dir) {
			t.Errorf("%s: serveAPI: %v", name, err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// a directory that is not there is left to the code that makes it, and a path that is no directory is named
	missing := apiOptions{stateDir: filepath.Join(root, "nowhere"), dataDir: dirs["api"]}
	if err := checkOwnedDirs(missing); err != nil {
		t.Errorf("a missing directory: %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkOwnedDirs(apiOptions{stateDir: file}); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("a file: %v", err)
	}
}
