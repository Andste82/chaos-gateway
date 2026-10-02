package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDir takes an exclusive lock on the directory. Two Chaos Gateway processes that overlap
// during a restart would race on the pointer and the ids; the second one fails instead.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("store: lock %s: %w", dir, err)
	}
	return f, nil
}

func unlockDir(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// Close releases the lock on the directory. The Store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockDir(s.lock)
	s.lock = nil
	return nil
}
