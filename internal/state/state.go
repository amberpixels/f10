// Package state reads the run state bin/f10-state.sh writes: one small
// `key value` file per session under ~/.claude/f10/state, keyed by session
// rather than by repo because two sessions over one checkout are two runs.
//
// The script is the writer and the semantics oracle. This package only
// reads, with the script's own tolerances - unknown keys ignored, a
// non-numeric timestamp read as stale - so `f10 status` and
// `f10-state.sh show` describe one run the same way. The parity test pins
// that.
package state

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultTTL is how long a run counts as live with nothing reporting: the
// script's default, and the point past which the badge stops rendering.
const DefaultTTL = 24 * time.Hour

const (
	dirEnv           = "F10_STATE_DIR"
	ttlEnv           = "F10_STATE_TTL"
	sessionEnv       = "F10_SESSION_ID"
	claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"
	fileExt          = ".state"
)

// Phases in the order the badge draws them.
var Phases = []string{"capture", "plan", "ship"}

// A Run is one session's state file, read back. Every phase holds one of
// the script's statuses: pending, running, done, failed, partial, skipped,
// prior, blocked.
type Run struct {
	Session string    `json:"session"`
	Task    string    `json:"task,omitempty"`
	URL     string    `json:"url,omitempty"`
	Root    string    `json:"root,omitempty"` // checkout root the run is about
	Capture string    `json:"capture"`
	Plan    string    `json:"plan"`
	Ship    string    `json:"ship"`
	Leaf    string    `json:"leaf,omitempty"`  // the pipeline step running, or stopped on
	Final   string    `json:"final,omitempty"` // the ship pipeline's declared last step
	Note    string    `json:"note,omitempty"`  // why the run stopped
	Next    string    `json:"next,omitempty"`  // what unblocks it
	Updated time.Time `json:"updated"`
}

// Dir is where state files live: the script's variable, else its default.
func Dir() string {
	if d := os.Getenv(dirEnv); d != "" {
		return d
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".claude", "f10", "state")
}

// TTL is how long a run counts as live, from the script's variable.
func TTL() time.Duration {
	if s := os.Getenv(ttlEnv); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}

	return DefaultTTL
}

// Session is the session this process runs in, in the order the script
// resolves it: the explicit override, then the id Claude Code exports into
// every Bash tool call. Empty from a plain terminal.
func Session() string {
	return cmp.Or(os.Getenv(sessionEnv), os.Getenv(claudeSessionEnv))
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Path is the state file for a session: the id sanitized the way the
// script sanitizes it, since it names a file.
func Path(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+fileExt)
}

// Load reads one state file. A missing file is os.ErrNotExist, unwrapped
// enough for errors.Is.
func Load(path string) (*Run, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	defer f.Close()

	r := &Run{
		Session: strings.TrimSuffix(filepath.Base(path), fileExt),
		Capture: "pending",
		Plan:    "pending",
		Ship:    "pending",
	}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), " ")

		switch key {
		case "task":
			r.Task = value
		case "url":
			r.URL = value
		case "root":
			r.Root = value
		case "capture":
			r.Capture = cmp.Or(value, "pending")
		case "plan":
			r.Plan = cmp.Or(value, "pending")
		case "ship":
			r.Ship = cmp.Or(value, "pending")
		case "leaf":
			r.Leaf = value
		case "final":
			r.Final = value
		case "note":
			r.Note = value
		case "next":
			r.Next = value
		case "updated":
			// the one field anything does maths on: garbage reads as the
			// zero time, which is stale under any ttl, never an error
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
				r.Updated = time.Unix(n, 0)
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("state: reading %s: %w", path, err)
	}

	return r, nil
}

// Live reports whether the run was updated within ttl of now.
func (r *Run) Live(now time.Time, ttl time.Duration) bool {
	return !r.Updated.IsZero() && now.Sub(r.Updated) <= ttl
}

// List reads every live run in dir, newest first. A missing directory is
// an empty list: no run has ever reported on this machine.
func List(dir string, now time.Time, ttl time.Duration) ([]*Run, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}

	var runs []*Run

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}

		r, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // a torn or vanished file is one badge fewer, not a failure
		}

		if r.Live(now, ttl) {
			runs = append(runs, r)
		}
	}

	slices.SortFunc(runs, func(a, b *Run) int {
		return b.Updated.Compare(a.Updated)
	})

	return runs, nil
}

// Status is the phase's status as recorded.
func (r *Run) Status(phase string) string {
	switch phase {
	case "capture":
		return r.Capture
	case "plan":
		return r.Plan
	default:
		return r.Ship
	}
}

// Stopped reports whether the phase status is one a run halts on.
func Stopped(status string) bool {
	return status == "failed" || status == "partial" || status == "blocked"
}

// PhaseText is the status with its step attached where one is known and
// belongs to that status: `running: implement`, `blocked at judge`. The
// leaf is a pipeline step, so only ship ever carries one. The script's
// show prints the same words.
func (r *Run) PhaseText(phase string) string {
	status := r.Status(phase)

	switch {
	case r.Leaf == "" || phase != "ship":
		return status
	case status == "running":
		return status + ": " + r.Leaf
	case Stopped(status):
		return status + " at " + r.Leaf
	default:
		return status
	}
}
