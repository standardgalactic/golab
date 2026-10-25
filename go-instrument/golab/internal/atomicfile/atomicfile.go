// Package atomicfile replaces a file's contents so that a reader, or a
// crash, never leaves the file half written. The new contents are written to
// a temporary file in the same directory, flushed to stable storage, and
// then renamed over the destination.
//
// The guarantee rests on the rename. On POSIX filesystems, renaming within
// one filesystem replaces the destination in a single step, so a reader opens
// either the whole old file or the whole new one. The package does not
// promise anything on filesystems that implement rename differently, and it
// does not make the contents correct; it only makes them arrive whole.
package atomicfile

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write calls fill with a buffered writer for a new file and, if fill and
// every later step succeed, installs the file at path with the given
// permissions. If anything fails, the file at path is left exactly as it was
// and the temporary file is removed.
func Write(path string, perm os.FileMode, fill func(io.Writer) error) (err error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if err != nil {
			if !closed {
				tmp.Close()
			}
			os.Remove(tmpName)
		}
	}()

	w := bufio.NewWriter(tmp)
	if err = fill(w); err != nil {
		return err
	}
	if err = w.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	if err = tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	closed = true
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	// The rename itself is a change to the directory, and it is durable only
	// once the directory has been flushed. A failure here means the new file
	// is in place but might not survive a crash, so it is reported.
	d, derr := os.Open(dir)
	if derr != nil {
		return fmt.Errorf("open directory: %w", derr)
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

// WriteBytes is Write for contents already in memory.
func WriteBytes(path string, perm os.FileMode, data []byte) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}
