package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStoreFile writes a file of the store's layout (a pointer and a revision) with the given schema version.
func writeStoreFile(t *testing.T, dir, rel string, schema int) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"schema_version": %d}`, schema)), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSchemaCheckAcceptsTheSchemaOfThisBuildAndAnEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if code, out, errOut := runCmd("schema-check", "--state-dir", dir); code != 0 || !strings.Contains(out, "schema version 1") {
		t.Errorf("an empty directory: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	writeStoreFile(t, dir, "config.json", 1)
	writeStoreFile(t, dir, "revisions/000001.json", 1)
	if code, out, errOut := runCmd("schema-check", "--state-dir", dir); code != 0 || !strings.Contains(out, "reads the configuration in "+dir) {
		t.Errorf("a store of this schema: code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// A newer schema is refused with the version that is needed and the way out, and nothing in the directory changes.
func TestSchemaCheckRefusesANewerSchemaAndSaysWhichVersionIsNeeded(t *testing.T) {
	dir := t.TempDir()
	writeStoreFile(t, dir, "config.json", 1)
	path := writeStoreFile(t, dir, "revisions/000003.json", 9)
	code, out, errOut := runCmd("schema-check", "--state-dir", dir)
	if code != 1 || out != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, want := range []string{path, "schema version 9", "reads schema versions up to 1", "needs version 9", "run the newer version"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"schema_version": 9}` {
		t.Errorf("the check changed the file: %s", raw)
	}
}

func TestSchemaCheckRejectsArgumentsItDoesNotKnow(t *testing.T) {
	if code, _, errOut := runCmd("schema-check", "extra"); code != 2 || !strings.Contains(errOut, "usage: chaosgw schema-check") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if code, _, _ := runCmd("schema-check", "--nope"); code != 2 {
		t.Errorf("an unknown flag: code %d", code)
	}
}
