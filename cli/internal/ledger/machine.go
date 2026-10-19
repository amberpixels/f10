package ledger

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"
)

// MachineID is this machine's id, generated once into <dir>/machine-id.
// Created exclusively, so two first runs read back the same id. Empty when
// the directory cannot be written.
func MachineID(dir string) string {
	path := filepath.Join(dir, "machine-id")

	if raw, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id
		}
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ""
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		raw, _ := os.ReadFile(path)

		return strings.TrimSpace(string(raw))
	}

	if err != nil {
		return ""
	}

	id := ulid.Make().String()
	_, werr := f.WriteString(id + "\n")

	if cerr := f.Close(); werr != nil || cerr != nil {
		return ""
	}

	return id
}
