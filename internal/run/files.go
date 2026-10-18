package run

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// fileVersion is the schema version of run.json; a file of another version is not read.
const fileVersion = 1

const (
	runFileName    = "run.json"
	eventsFileName = "events.jsonl"
)

// runFile is the record of a run on disk: the run as the API shows it and the final counters of
// the overlays it created.
type runFile struct {
	Version  int             `json:"schema_version"`
	Run      model.Run       `json:"run"`
	Counters []counterRecord `json:"counters,omitempty"`
}

// counterRecord is the final counter of an overlay a step created (RunReport.counters).
type counterRecord struct {
	Step     string        `json:"step"`
	Overlay  uuid.UUID     `json:"overlay"`
	Counters model.Counter `json:"counters"`
}

func runDir(dir string, id uuid.UUID) string { return filepath.Join(dir, id.String()) }

// writeFileAtomic writes a file so that a reader sees the old content or the new, never half: a
// temporary file in the same directory, synced, then renamed over it.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readRunFile(dir string, id uuid.UUID) (*runFile, error) {
	raw, err := os.ReadFile(filepath.Join(runDir(dir, id), runFileName))
	if err != nil {
		return nil, err
	}
	var f runFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	if f.Version != fileVersion {
		return nil, fmt.Errorf("the record has schema version %d, this build reads %d", f.Version, fileVersion)
	}
	if f.Run.Id != id {
		return nil, fmt.Errorf("the record of run %s holds run %s", id, f.Run.Id)
	}
	return &f, nil
}

// listRunDirs returns the runs that have a directory, oldest name first.
func listRunDirs(dir string) ([]uuid.UUID, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if id, err := uuid.Parse(e.Name()); err == nil {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// appendLine adds one line to a run's event file.
func appendLine(dir string, id uuid.UUID, line []byte) error {
	f, err := os.OpenFile(filepath.Join(runDir(dir, id), eventsFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(append([]byte(nil), line...), '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readEvents returns the lines of a run's event file, none when it has none.
func readEvents(dir string, id uuid.UUID) ([][]byte, error) {
	f, err := os.Open(filepath.Join(runDir(dir, id), eventsFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, []byte(line))
		}
	}
	return out, sc.Err()
}

// eventsSize is the size of a run's event file in bytes, zero without one.
func eventsSize(dir string, id uuid.UUID) int64 {
	if st, err := os.Stat(filepath.Join(runDir(dir, id), eventsFileName)); err == nil {
		return st.Size()
	}
	return 0
}

// removeRun deletes a run's directory.
func removeRun(dir string, id uuid.UUID) error { return os.RemoveAll(runDir(dir, id)) }

// ended reports whether a status is final.
func ended(s model.RunStatus) bool { return s != model.RunStatusQueued && s != model.RunStatusRunning }

// endedBefore reports whether a record ended before the time.
func endedBefore(r model.Run, t time.Time) bool {
	return ended(r.Status) && r.EndedAt != nil && r.EndedAt.Before(t)
}
