package state

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const launchesExt = ".launches"

// Launch kinds: a task `f10 start` opened, or a drive by its number.
const (
	LaunchStart = "start"
	LaunchDrive = "drive"
)

// A Launch is one thing a session set going: a start's task, or a drive.
type Launch struct {
	Kind  string
	Value string // the task as typed for start, the drive's number for drive
}

// LaunchesPath lists what a session launched, so a bare status in the main
// checkout can show it.
func LaunchesPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+launchesExt)
}

// AppendLaunch adds one launch to session's list. One short line in one
// append write, so two parallel starts in a session never tear a line.
func AppendLaunch(dir, session string, l Launch) error {
	if dir == "" || session == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	f, err := os.OpenFile(LaunchesPath(dir, session), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	_, err = f.WriteString(l.Kind + " " + oneLine(l.Value) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	return nil
}

// Launches reads session's list in launch order, a malformed line or a
// repeat (a rerun start, a resumed drive) skipped. A missing file is none.
func Launches(dir, session string) ([]Launch, error) {
	if dir == "" || session == "" {
		return nil, nil
	}

	f, err := os.Open(LaunchesPath(dir, session))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	defer f.Close()

	var out []Launch

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		kind, value, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		l := Launch{Kind: kind, Value: strings.TrimSpace(value)}

		if (kind != LaunchStart && kind != LaunchDrive) || l.Value == "" ||
			slices.ContainsFunc(
				out,
				func(o Launch) bool { return o.Kind == l.Kind && strings.EqualFold(o.Value, l.Value) },
			) {
			continue
		}

		out = append(out, l)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("state: reading launches: %w", err)
	}

	return out, nil
}
