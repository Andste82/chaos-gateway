package store

import (
	"encoding/json"
	"fmt"
	"os"
)

// Migration upgrades the content of a file from one schema version to the next.
type Migration func(raw []byte) ([]byte, error)

// migrations maps a kind of file ("state", "revision", "status") and the version it upgrades
// from to the migration.
type migrations map[string]map[int]Migration

// defaultMigrations are the migrations of this build: none yet, schema version 1 is the first.
var defaultMigrations = migrations{}

// upgrade brings the content of a file to the current schema version, step by step. A file of a
// newer version is refused (ErrNewerSchema). The file on disk is not touched: migrateAll
// rewrites it, with a backup.
func (m migrations) upgrade(kind, path string, raw []byte) ([]byte, error) {
	for {
		v, err := schemaVersionOf(raw)
		if err != nil {
			return nil, &ErrCorrupt{path, err.Error()}
		}
		switch {
		case v == SchemaVersion:
			return raw, nil
		case v > SchemaVersion:
			return nil, &ErrNewerSchema{Path: path, Found: v, Supported: SchemaVersion}
		}
		step, ok := m[kind][v]
		if !ok {
			return nil, &ErrCorrupt{path, fmt.Sprintf("there is no migration from schema version %d", v)}
		}
		if raw, err = step(raw); err != nil {
			return nil, fmt.Errorf("store: migrate %s from schema version %d: %w", path, v, err)
		}
	}
}

func schemaVersionOf(raw []byte) (int, error) {
	var head struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return 0, err
	}
	if head.SchemaVersion == nil {
		return 0, fmt.Errorf("no schema_version")
	}
	return *head.SchemaVersion, nil
}

// migrateAll rewrites every file of an older schema at the current version. The old content
// stays next to it as <file>.bak-v<version> (plan §3.9: a backup of the previous revision).
func (s *Store) migrateAll() error {
	files := map[string]string{s.statePath(): "state"} // path → kind
	ids, err := s.revisionIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		files[s.revPath(id)] = "revision"
		if _, err := os.Stat(s.statusPath(id)); err == nil {
			files[s.statusPath(id)] = "status"
		}
	}
	for path, kind := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		v, err := schemaVersionOf(raw)
		if err != nil {
			continue // corrupt files are reported when they are read
		}
		if v > SchemaVersion {
			return &ErrNewerSchema{Path: path, Found: v, Supported: SchemaVersion}
		}
		if v == SchemaVersion {
			continue
		}
		migrated, err := s.mig.upgrade(kind, path, raw)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(fmt.Sprintf("%s.bak-v%d", path, v), raw, 0o640); err != nil {
			return err
		}
		if err := writeFileAtomic(path, migrated, 0o640); err != nil {
			return err
		}
	}
	return nil
}
