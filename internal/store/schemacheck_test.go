package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The update script asks a new image whether it can read the store before it stops the running gateway (M28): CheckSchema
// finds the files Open refuses, takes no lock (the API has the store open) and changes nothing.
func TestCheckSchemaFindsAFileOfANewerSchemaWhileTheStoreIsOpen(t *testing.T) {
	for _, tt := range []struct {
		name string
		path func(dir string) string
	}{
		{"the pointer", func(dir string) string { return filepath.Join(dir, "config.json") }},
		{"a revision", func(dir string) string { return revFile(dir, 2) }},
		{"a status file", func(dir string) string { return filepath.Join(dir, "revisions", "000001.status.json") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, dir := storeWithTwo(t)
			if err := CheckSchema(dir); err != nil {
				t.Fatalf("a store of this build's schema: %v", err)
			}
			rewrite(t, tt.path(dir), func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schema_version": 7`, 1) })
			before, _ := os.ReadFile(tt.path(dir))
			err := CheckSchema(dir)
			var newer *ErrNewerSchema
			if !errors.As(err, &newer) || newer.Found != 7 || newer.Supported != SchemaVersion || newer.Path != tt.path(dir) {
				t.Fatalf("err = %v", err)
			}
			if after, _ := os.ReadFile(tt.path(dir)); string(after) != string(before) {
				t.Error("the check changed the file")
			}
		})
	}
}

func TestCheckSchemaAcceptsAMissingStoreAndLeavesBrokenFilesToTheStart(t *testing.T) {
	if err := CheckSchema(filepath.Join(t.TempDir(), "none")); err != nil {
		t.Errorf("a store that does not exist yet: %v", err)
	}
	dir := dirWithTwo(t)
	rewrite(t, revFile(dir, 1), func(string) string { return "{not json" })
	if err := CheckSchema(dir); err != nil {
		t.Errorf("a file that does not parse is reported by the start, not by the schema check: %v", err)
	}
}
