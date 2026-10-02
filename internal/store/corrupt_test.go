package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// stored makes a store with revisions 1 (active) and 2 (candidate).
func storeWithTwo(t *testing.T) (*Store, string) {
	t.Helper()
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "one")
	if _, err := s.Commit(1, t0); err != nil {
		t.Fatal(err)
	}
	create(t, s, changed(t, cfg), 1, "two")
	return s, dir
}

// dirWithTwo is storeWithTwo for tests that open the directory themselves: the store is closed.
func dirWithTwo(t *testing.T) string {
	t.Helper()
	s, dir := storeWithTwo(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func revFile(dir string, id int64) string {
	return filepath.Join(dir, "revisions", revisionName(id)+".json")
}

func rewrite(t *testing.T, path string, edit func(string) string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(raw))), 0o640); err != nil {
		t.Fatal(err)
	}
}

func asCorrupt(err error) *ErrCorrupt {
	var c *ErrCorrupt
	if errors.As(err, &c) {
		return c
	}
	return nil
}

func TestAConfigurationEditedByHandIsReportedAsCorruptNotUsed(t *testing.T) {
	s, dir := storeWithTwo(t)
	// still valid JSON, still a valid configuration, but not what was written
	rewrite(t, revFile(dir, 2), func(s string) string { return strings.Replace(s, "90s", "91s", 1) })
	_, _, err := s.Get(2)
	c := asCorrupt(err)
	if c == nil || !strings.Contains(c.Reason, "checksum") || c.Path != revFile(dir, 2) {
		t.Fatalf("err = %v", err)
	}
	// other revisions still work
	if _, _, err := s.Get(1); err != nil {
		t.Fatalf("Get(1): %v", err)
	}
	if _, _, err := s.Active(); err != nil {
		t.Fatalf("Active: %v", err)
	}
}

func TestACorruptRevisionIsSkippedByListAndFoundByVerify(t *testing.T) {
	s, dir := storeWithTwo(t)
	rewrite(t, revFile(dir, 2), func(s string) string { return strings.Replace(s, "90s", "91s", 1) })
	list, err := s.List(ListOptions{})
	if err != nil || len(list) != 1 || list[0].Id != 1 {
		t.Fatalf("List: %+v %v", list, err)
	}
	bad, err := s.Verify()
	if err != nil || len(bad) != 1 || bad[0].Revision != 2 || asCorrupt(bad[0].Err) == nil {
		t.Fatalf("Verify: %+v %v", bad, err)
	}
	// a healthy store verifies clean
	healthy, _ := storeWithTwo(t)
	if bad, err := healthy.Verify(); err != nil || len(bad) != 0 {
		t.Fatalf("Verify: %+v %v", bad, err)
	}
}

func TestDamagedFilesAreReportedWithTheirPath(t *testing.T) {
	tests := []struct {
		name   string
		damage func(t *testing.T, dir string) string
		reason string
	}{
		{"a truncated revision file", func(t *testing.T, dir string) string {
			rewrite(t, revFile(dir, 2), func(s string) string { return s[:len(s)/2] })
			return revFile(dir, 2)
		}, "unexpected end"},
		{"garbage instead of a revision file", func(t *testing.T, dir string) string {
			rewrite(t, revFile(dir, 2), func(string) string { return "not json at all" })
			return revFile(dir, 2)
		}, "invalid character"},
		{"an empty revision file", func(t *testing.T, dir string) string {
			rewrite(t, revFile(dir, 2), func(string) string { return "" })
			return revFile(dir, 2)
		}, ""},
		{"a revision file that says it is another revision", func(t *testing.T, dir string) string {
			rewrite(t, revFile(dir, 2), func(s string) string { return strings.Replace(s, `"id": 2`, `"id": 7`, 1) })
			return revFile(dir, 2)
		}, "says revision 7"},
		{"a revision without a schema version", func(t *testing.T, dir string) string {
			rewrite(t, revFile(dir, 2), func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schemaa": 1`, 1) })
			return revFile(dir, 2)
		}, "no schema_version"},
		{"a missing status file", func(t *testing.T, dir string) string {
			if err := os.Remove(filepath.Join(dir, "revisions", "000002.status.json")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "revisions", "000002.status.json")
		}, "missing"},
		{"a garbage status file", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "revisions", "000002.status.json")
			rewrite(t, p, func(string) string { return "{{{" })
			return p
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := storeWithTwo(t)
			path := tt.damage(t, dir)
			_, _, err := s.Get(2)
			c := asCorrupt(err)
			if c == nil {
				t.Fatalf("expected ErrCorrupt, got %v", err)
			}
			if c.Path != path || !strings.Contains(c.Reason, tt.reason) {
				t.Fatalf("path %s reason %q, want %s / %q", c.Path, c.Reason, path, tt.reason)
			}
			if !strings.Contains(c.Error(), path) {
				t.Errorf("the message must name the file: %s", c.Error())
			}
			// nothing is returned from a file that cannot be trusted
			if _, cfg, _ := s.Get(2); cfg != nil {
				t.Error("a corrupt revision must not return a configuration")
			}
		})
	}
}

func TestAConfigurationThatChecksOutButDoesNotDecodeIsNotReturned(t *testing.T) {
	// the checksum matches but the content is not a configuration: the decode catches it
	s, dir := storeWithTwo(t)
	raw, err := os.ReadFile(revFile(dir, 2))
	if err != nil {
		t.Fatal(err)
	}
	var f revisionFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Configuration = json.RawMessage(`"oops"`)
	f.SHA256 = sha256Sum(f.Configuration)
	fixed, _ := json.Marshal(f)
	if err := os.WriteFile(revFile(dir, 2), fixed, 0o640); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := s.Get(2)
	if c := asCorrupt(err); c == nil || !strings.Contains(c.Reason, "does not decode") || cfg != nil {
		t.Fatalf("err = %v, cfg = %v", err, cfg)
	}
}

func TestACorruptStateFileStopsOpen(t *testing.T) {
	dir := dirWithTwo(t)
	rewrite(t, filepath.Join(dir, "config.json"), func(string) string { return "garbage" })
	if _, err := Open(dir); asCorrupt(err) == nil {
		t.Fatalf("Open: %v", err)
	}
	rewrite(t, filepath.Join(dir, "config.json"), func(string) string { return `{"schema_version":1,"active":1,"next_id":0}` })
	if _, err := Open(dir); asCorrupt(err) == nil {
		t.Fatalf("a next_id of 0: %v", err)
	}
}

func TestAnOrphanRevisionFileRepairsTheNextID(t *testing.T) {
	// a crash after the revision was written but before the pointer was: the file exists, the
	// pointer does not know it yet
	s, dir := storeWithTwo(t)
	rewrite(t, filepath.Join(dir, "config.json"), func(s string) string { return strings.Replace(s, `"next_id": 3`, `"next_id": 2`, 1) })
	s2 := reopen(t, s, dir)
	if r := create(t, s2, exampleConfig(t), 1, ""); r.Id != 3 {
		t.Fatalf("the orphan must keep its id: the next id is %d, want 3", r.Id)
	}
}

func TestTemporaryFilesOfAnInterruptedWriteAreRemovedAtOpen(t *testing.T) {
	dir := dirWithTwo(t)
	leftovers := []string{
		filepath.Join(dir, "config.json"+tmpMarker+"deadbeef"),
		filepath.Join(dir, "revisions", "000003.json"+tmpMarker+"cafe"),
		filepath.Join(dir, "revisions", "000003.status.json"+tmpMarker+"cafe"),
	}
	for _, p := range leftovers {
		if err := os.WriteFile(p, []byte(`{"half`), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range leftovers {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s must be removed", p)
		}
	}
	// and they never counted as revisions
	if list, _ := s.List(ListOptions{}); len(list) != 2 {
		t.Fatalf("%d revisions", len(list))
	}
}

// ---- atomic writes -----------------------------------------------------------------------

func TestWriteFileAtomicReplacesTheWholeContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := writeFileAtomic(path, []byte(strings.Repeat("old", 100000)), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "new" {
		t.Fatalf("content = %.20q", raw)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*"+tmpMarker+"*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestAFailedWriteLeavesTheOldFileAndNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := writeFileAtomic(path, []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	// the target is a directory: the final rename fails after the temporary file was written
	target := filepath.Join(dir, "d")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "inner"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, []byte("data"), 0o640); err == nil {
		t.Fatal("expected an error")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*"+tmpMarker+"*")); len(left) != 0 {
		t.Errorf("a failed write left %v", left)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "keep" {
		t.Error("the other file must be untouched")
	}
	// a directory that does not exist
	if err := writeFileAtomic(filepath.Join(dir, "missing", "f"), []byte("x"), 0o640); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}

func TestAFailedCreateLeavesNoRevisionBehind(t *testing.T) {
	s, dir := openStore(t)
	// make the revision file impossible to write: a directory with its name
	if err := os.Mkdir(revFile(dir, 1), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(exampleConfig(t), CreateOptions{Now: t0}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "revisions", "000001.status.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the status file of the failed revision must be removed")
	}
}

// ---- schema versions ---------------------------------------------------------------------

func TestAFileOfANewerSchemaStopsOpenWithTheVersionThatIsNeeded(t *testing.T) {
	for _, tt := range []struct {
		name string
		path func(dir string) string
	}{
		{"the pointer", func(dir string) string { return filepath.Join(dir, "config.json") }},
		{"a revision", func(dir string) string { return revFile(dir, 1) }},
		{"a status file", func(dir string) string { return filepath.Join(dir, "revisions", "000001.status.json") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := dirWithTwo(t)
			rewrite(t, tt.path(dir), func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schema_version": 7`, 1) })
			_, err := Open(dir)
			var newer *ErrNewerSchema
			if !errors.As(err, &newer) || newer.Found != 7 || newer.Supported != SchemaVersion || newer.Path != tt.path(dir) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), "install a newer Chaos Gateway") {
				t.Errorf("the message must say what to do: %v", err)
			}
		})
	}
}

// writeOldFormat writes a revision in an imaginary schema version 0, where the id was called "n".
func TestOlderFilesAreMigratedWithABackup(t *testing.T) {
	dir := dirWithTwo(t)
	// turn revision 1 into a version-0 file: "id" was "n", and no schema_version bump in the content
	rewrite(t, revFile(dir, 1), func(s string) string {
		s = strings.Replace(s, `"schema_version": 1`, `"schema_version": 0`, 1)
		return strings.Replace(s, `"id": 1`, `"n": 1`, 1)
	})
	mig := migrations{"revision": {0: func(raw []byte) ([]byte, error) {
		out := strings.Replace(string(raw), `"schema_version": 0`, `"schema_version": 1`, 1)
		return []byte(strings.Replace(out, `"n": 1`, `"id": 1`, 1)), nil
	}}}
	original, _ := os.ReadFile(revFile(dir, 1))

	s2, err := open(dir, mig)
	if err != nil {
		t.Fatal(err)
	}
	rev, cfg, err := s2.Get(1)
	if err != nil || rev.Id != 1 || cfg == nil || rev.SchemaVersion != 1 {
		t.Fatalf("Get after the migration: %+v %v", rev, err)
	}
	migrated, _ := os.ReadFile(revFile(dir, 1))
	if !strings.Contains(string(migrated), `"schema_version": 1`) || !strings.Contains(string(migrated), `"id": 1`) {
		t.Errorf("the file was not rewritten:\n%.200s", migrated)
	}
	backup, err := os.ReadFile(revFile(dir, 1) + ".bak-v0")
	if err != nil || string(backup) != string(original) {
		t.Fatalf("the backup must hold the old content: %v", err)
	}
	// a second open has nothing to do
	_ = s2.Close()
	if _, err := open(dir, mig); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsRunStepByStep(t *testing.T) {
	dir := dirWithTwo(t)
	rewrite(t, filepath.Join(dir, "config.json"), func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schema_version": -1`, 1) })
	var calls []int
	mig := migrations{"state": {
		-1: func(raw []byte) ([]byte, error) {
			calls = append(calls, -1)
			return []byte(strings.Replace(string(raw), `"schema_version": -1`, `"schema_version": 0`, 1)), nil
		},
		0: func(raw []byte) ([]byte, error) {
			calls = append(calls, 0)
			return []byte(strings.Replace(string(raw), `"schema_version": 0`, `"schema_version": 1`, 1)), nil
		},
	}}
	if _, err := open(dir, mig); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 2 || calls[0] != -1 || calls[1] != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestAMissingOrFailingMigrationStopsOpen(t *testing.T) {
	dir := dirWithTwo(t)
	rewrite(t, revFile(dir, 1), func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schema_version": 0`, 1) })
	if _, err := Open(dir); asCorrupt(err) == nil || !strings.Contains(err.Error(), "no migration from schema version 0") {
		t.Fatalf("no migration registered: %v", err)
	}
	failing := migrations{"revision": {0: func([]byte) ([]byte, error) { return nil, errors.New("boom") }}}
	if _, err := open(dir, failing); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failing migration: %v", err)
	}
	// nothing was rewritten and no backup was made by the failed run
	if _, err := os.Stat(revFile(dir, 1) + ".bak-v0"); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed migration must not leave a backup")
	}
}

var _ = model.Revision{}
var _ = time.Second
