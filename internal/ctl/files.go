package ctl

import (
	"errors"
	"io"
	"os"
)

// Modes of what chaosctl writes: a file that may hold a secret (the private key of a WireGuard client, the raw
// traffic of a capture, a key log) is readable by its owner alone, in a directory that only its owner may enter.
const (
	privateFileMode = 0o600
	privateDirMode  = 0o700
	publicFileMode  = 0o644
	publicDirMode   = 0o755
)

// writeFileAtomic writes path through a new file of its own next to it (path+".part"), made with the mode whatever
// the target had before, and renames it over the target when write has succeeded; on a failure the target is as it
// was and nothing is left behind. A symbolic link at path is replaced, not followed, and the mode of an existing file
// does not matter. A target that is not a regular file (a terminal, a pipe: `--out /dev/stdout`) is written in place.
func writeFileAtomic(path string, mode os.FileMode, write func(io.Writer) error) (err error) {
	if fi, serr := os.Stat(path); serr == nil && !fi.Mode().IsRegular() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		werr := write(f)
		return errors.Join(werr, f.Close())
	}
	tmp := path + ".part"
	// a left-over of an earlier run (of any mode, or a link) must not be written into
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
