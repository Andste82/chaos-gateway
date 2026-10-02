package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tmpMarker marks the temporary files of an atomic write; Open removes the ones a crash left.
const tmpMarker = ".tmp-"

// writeFileAtomic replaces the file at path with data: the content is written to a temporary
// file in the same directory, flushed, renamed over the target and the directory is flushed.
// Readers see the old or the new file, and after a crash one of the two survives.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := path + tmpMarker + hex.EncodeToString(b[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir flushes a directory so that a rename in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

// removeTempFiles deletes the leftovers of interrupted atomic writes in dir.
func removeTempFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), tmpMarker) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// revisionName is the file name of a revision: six digits, so that files sort by id.
func revisionName(id int64) string { return fmt.Sprintf("%06d", id) }
