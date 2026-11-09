// Package fsx holds the file helpers every package shares.
package fsx

import (
	"fmt"
	"os"
	"path/filepath"
)

// Exists reports whether path names anything a stat can see.
func Exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// WriteFile replaces path through a rename, so a reader never sees half a
// file. The temp file ends in .tmp, which every directory lister here skips.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	err = write(tmp, data, perm)
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

func write(tmp *os.File, data []byte, perm os.FileMode) error {
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()

		return err
	}

	return tmp.Close()
}
