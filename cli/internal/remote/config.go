package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/storage"
)

// DefaultPort is the listener's port when the setting names none.
const DefaultPort = 7410

// A Config is the listener setting: off unless enabled.
type Config struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port,omitempty"`
	// MCP serves the API as MCP tools at MCPPath on the listener.
	MCP bool `json:"mcp,omitempty"`
}

// ListenPort is the port the listener binds.
func (c Config) ListenPort() int {
	if c.Port > 0 {
		return c.Port
	}

	return DefaultPort
}

// Dir holds the setting and the tokens. It lives in the user's f10 dir, not
// the run dir, because the run dir may be a tmpfs a reboot empties.
func Dir() (string, error) {
	dir, err := storage.UserFilesDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "remote"), nil
}

func configPath(dir string) string { return filepath.Join(dir, "config.json") }

// ReadConfig is the setting in dir; a missing file reads as off.
func ReadConfig(dir string) (Config, error) {
	raw, err := os.ReadFile(configPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}

	if err != nil {
		return Config{}, err
	}

	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s does not read as the remote setting: %w", configPath(dir), err)
	}

	return c, nil
}

// WriteConfig replaces the setting in dir.
func WriteConfig(dir string, c Config) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return writeFile(configPath(dir), append(raw, '\n'))
}

// writeFile replaces path through a rename, so a reader never sees half a
// file, and keeps it to this user.
func writeFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}
