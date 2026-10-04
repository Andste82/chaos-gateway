package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// genReserveBlock is how many generations are reserved per write of the generation file: the owner
// writes once per block instead of once per generation, trading a few skipped numbers after a crash
// for far fewer fsyncs.
const genReserveBlock = 1000

// readGenerationMark returns the high-water mark stored in path, or 0 if the file does not exist.
// Persistence is optional: an empty path always returns 0.
func readGenerationMark(path string) (uint64, error) {
	if path == "" {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read the generation mark: %w", err)
	}
	mark, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse the generation mark %q: %w", path, err)
	}
	return mark, nil
}

// writeGenerationMark persists mark to path atomically: a temporary file in the same directory,
// fsynced, then renamed over the target.
func writeGenerationMark(path string, mark uint64) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write the generation mark: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write the generation mark: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strconv.FormatUint(mark, 10)); err != nil {
		tmp.Close()
		return fmt.Errorf("write the generation mark: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write the generation mark: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the generation mark: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write the generation mark: %w", err)
	}
	return nil
}

// ensureGenReserved persists a new reservation block once the generation counter reaches the last
// one written, so that a restart never reuses a generation the previous process might have handed
// out. A missing GenerationFile (genPath == "") leaves the counter per-process, as before M5-01.
func (o *owner) ensureGenReserved() {
	if o.genPath == "" || o.gen <= o.genReserved {
		return
	}
	mark := o.gen + genReserveBlock - 1
	if err := writeGenerationMark(o.genPath, mark); err != nil {
		o.e.cfg.Log.Warn("cannot persist the generation reservation", "error", err)
		return
	}
	o.genReserved = mark
}
