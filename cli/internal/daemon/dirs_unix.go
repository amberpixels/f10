//go:build unix

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ownDir refuses a run dir another user owns: its socket and drained
// list carry this user's drives and tokens.
func ownDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("daemon: %s is owned by another user", dir)
	}

	return nil
}

// logAge is how long a finished drive's log is kept.
const logAge = 7 * 24 * time.Hour

// pruneLogs drops finished drive logs older than logAge, their exit files,
// and their lines in the index.
func pruneLogs(dir string, now time.Time) {
	logs := logsDir(dir)

	entries, err := os.ReadDir(logs)
	if err != nil {
		return
	}

	gone := map[string]bool{}

	for _, e := range entries {
		path := filepath.Join(logs, e.Name())
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		fi, err := e.Info()
		if _, done := ReadExit(path); err != nil || !done || now.Sub(fi.ModTime()) <= logAge {
			continue
		}

		_ = os.Remove(path)
		_ = os.Remove(ExitPath(path))
		gone[path] = true
	}

	index := filepath.Join(logs, "index")

	raw, err := os.ReadFile(index)
	if err != nil || len(gone) == 0 {
		return
	}

	var keep []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 && !gone[f[2]] {
			keep = append(keep, line)
		}
	}

	body := strings.Join(keep, "\n")
	if body != "" {
		body += "\n"
	}

	_ = os.WriteFile(index, []byte(body), 0o600)
}
