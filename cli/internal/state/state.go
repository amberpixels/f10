// Package state owns the run state: one `key value` file per session, keyed
// by session rather than by repo because two sessions over one checkout are
// two runs. The status line's renderer, `bin/f10-state.sh render`, reads the
// same format in bash on every refresh; the render parity test pins the two.
package state

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/flow"
)

// DefaultTTL is how long a run counts as live with nothing reporting.
const DefaultTTL = 24 * time.Hour

const (
	dirEnv           = "F10_STATE_DIR"
	ttlEnv           = "F10_STATE_TTL"
	sessionEnv       = "F10_SESSION_ID"
	claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"
	fileExt          = ".state"
	lockExt          = ".lock" // beside the session's .state, which Update holds
)

// Phases in the order the badge draws them.
var Phases = []string{"capture", "plan", "ship"}

// A Run is one session's state file, read back; each phase holds a Statuses.
type Run struct {
	Session string `json:"session"`
	Task    string `json:"task,omitempty"`
	URL     string `json:"url,omitempty"`
	Root    string `json:"root,omitempty"`  // checkout root the run is about
	Skill   string `json:"skill,omitempty"` // the skill that began the run
	Capture string `json:"capture"`
	Plan    string `json:"plan"`
	Ship    string `json:"ship"`
	Leaf    string `json:"leaf,omitempty"` // the pipeline step running, or stopped on
	// Leaf's place in Pipeline, counted from 1, so a step the pipeline
	// names twice (`judge`, `pr`) is told apart; 0 is unknown
	At    int    `json:"at,omitempty"`
	Final string `json:"final,omitempty"` // the ship pipeline's declared last step
	// the ship pipeline's steps as declared, in order, labels cut
	Pipeline []string `json:"pipeline,omitempty"`
	// Labels is each Pipeline entry's display name by index ("" for none);
	// nil when no entry has one. Nothing matches on it, only shows it
	Labels []string `json:"labels,omitempty"`
	Note   string   `json:"note,omitempty"` // why the run stopped
	Next   string   `json:"next,omitempty"` // what unblocks it
	Ask    string   `json:"ask,omitempty"`  // the question a driven run stopped on, options included
	// Wait is the base task a driven ship stopped for, its branch without
	// code yet: a drive parks the task and sends ship again once it has some
	Wait string `json:"wait,omitempty"`
	// Stop is the kind of stop a ship left, StopGuard for its catchup guard,
	// so a drive need not guess it from the stop's shape
	Stop string `json:"stop,omitempty"`
	// Done is the phaseless step (catchup) whose report is out, DoneAt when;
	// a reader compares DoneAt, since any later write moves Updated alone
	Done    string    `json:"done,omitempty"`
	DoneAt  time.Time `json:"done_at,omitzero"`
	Updated time.Time `json:"updated"`
}

// Dir is where state files live: F10_STATE_DIR, else the default.
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

// TTL is how long a run counts as live, from F10_STATE_TTL.
func TTL() time.Duration {
	if s := os.Getenv(ttlEnv); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}

	return DefaultTTL
}

// Session is the override, else the id Claude Code exports into every Bash
// tool call; empty from a plain terminal. A hook names its session in the
// payload instead.
func Session() string {
	return cmp.Or(os.Getenv(sessionEnv), os.Getenv(claudeSessionEnv))
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Path sanitizes the session id as the renderer does, since it names a file.
func Path(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+fileExt)
}

// StartedPath marks when the session loaded the plugin, for doctor to compare
// with the install's update time.
func StartedPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+".started")
}

// ModelPath holds the model the session started on, from its SessionStart
// payload; lore reads it to name the agent behind an entry.
func ModelPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+".model")
}

// SessionModel is the model ModelPath recorded, "" when none was.
func SessionModel(dir, session string) string {
	if dir == "" || session == "" {
		return ""
	}

	raw, err := os.ReadFile(ModelPath(dir, session))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}

// WaitPath is the wait a session has open: what on and since when.
func WaitPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+".wait")
}

// Wait is the marker WaitPath holds: the reason, and for a dialog, the tool
// and the tool call it is about.
type Wait struct {
	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
	UseID  string `json:"tool_use_id,omitempty"`
}

// OpenWait is the wait the session has open, false for none.
func OpenWait(dir, session string) (Wait, bool) {
	var w Wait

	raw, err := os.ReadFile(WaitPath(dir, session))
	if err != nil || json.Unmarshal(raw, &w) != nil || w.Reason == "" {
		return w, false
	}

	return w, true
}

// BackgroundPath marks background work a session's turn launched (a Bash
// command with run_in_background, or a Monitor). It is a file of its own,
// not a run field, because writing the run moves Updated, which a drive
// reads as a turn beginning.
func BackgroundPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+".background")
}

// Background is the marker BackgroundPath holds. Parked is set once the
// launching turn ended with the work pending.
type Background struct {
	At     time.Time `json:"at"`
	Parked bool      `json:"parked,omitempty"`
}

// PendingBackground is the session's background mark, false for none.
func PendingBackground(dir, session string) (Background, bool) {
	var b Background

	if dir == "" || session == "" {
		return b, false
	}

	raw, err := os.ReadFile(BackgroundPath(dir, session))
	if err != nil || json.Unmarshal(raw, &b) != nil || b.At.IsZero() {
		return b, false
	}

	return b, true
}

// MarkBackground writes the session's background mark over any before it.
func MarkBackground(dir, session string, b Background) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if err := os.WriteFile(BackgroundPath(dir, session), raw, 0o600); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	return nil
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
		Capture: Upcoming,
		Plan:    Upcoming,
		Ship:    Upcoming,
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
		case "skill":
			r.Skill = value
		case "capture":
			r.Capture = LegacyStatus(cmp.Or(value, Upcoming))
		case "plan":
			r.Plan = LegacyStatus(cmp.Or(value, Upcoming))
		case "ship":
			r.Ship = LegacyStatus(cmp.Or(value, Upcoming))
		case "leaf":
			r.Leaf = value
		case "at":
			r.At, _ = strconv.Atoi(value)
		case "final":
			r.Final = value
		case "pipeline":
			r.Pipeline = SplitPipeline(value)
		case "labels":
			r.Labels = splitLabels(value)
		case "note":
			r.Note = value
		case "next":
			r.Next = value
		case "ask":
			r.Ask = value
		case "wait":
			r.Wait = value
		case "stop":
			r.Stop = value
		case "done":
			r.Done = value
		case "done_at":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
				r.DoneAt = time.Unix(n, 0)
			}
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

// PipelineSep joins a recorded pipeline's steps, as `pipelines:` strings
// spell them: a step name may hold spaces (`review (local)`).
const PipelineSep = " → "

// SplitPipeline reads a recorded pipeline back into its steps.
func SplitPipeline(s string) []string {
	var steps []string

	for step := range strings.SplitSeq(s, strings.TrimSpace(PipelineSep)) {
		if step = strings.TrimSpace(step); step != "" {
			steps = append(steps, step)
		}
	}

	return steps
}

// noLabel holds an unlabelled entry's place in the state file's labels line.
const noLabel = "-"

func joinLabels(labels []string) string {
	shown := make([]string, len(labels))
	for i, l := range labels {
		shown[i] = cmp.Or(l, noLabel)
	}

	return strings.Join(shown, PipelineSep)
}

func splitLabels(s string) []string {
	labels := SplitPipeline(s)
	for i, l := range labels {
		if l == noLabel {
			labels[i] = ""
		}
	}

	return labels
}

// Entries is the pipeline with each entry's label: the state file keeps
// the two on separate lines.
func (r *Run) Entries() flow.Flow { return flow.Zip(r.Pipeline, r.Labels) }

// LabelAt is the label of the pipeline's at-th entry, counted from 1.
func (r *Run) LabelAt(at int) string {
	if at < 1 || at > len(r.Labels) {
		return ""
	}

	return r.Labels[at-1]
}

// LeafShown is the leaf as a person reads it: its entry's label, else the
// step itself. A leaf the pipeline does not hold (catchup) keeps the place
// of the entry it guards, so it never borrows that entry's label.
func (r *Run) LeafShown() string {
	if r.At > 0 && r.At <= len(r.Pipeline) && r.Pipeline[r.At-1] == r.Leaf {
		return cmp.Or(r.LabelAt(r.At), r.Leaf)
	}

	return r.Leaf
}

// FinalShown is the final step as a person reads it: the last entry's label,
// else the step itself.
func (r *Run) FinalShown() string {
	if n := len(r.Pipeline); n > 0 && r.Pipeline[n-1] == r.Final {
		return cmp.Or(r.LabelAt(n), r.Final)
	}

	return r.Final
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

// Furthest is the last phase the run has left: its status is neither
// upcoming nor unset. A run that left none is at its first phase.
func (r *Run) Furthest() string {
	phase := Phases[0]

	for _, p := range Phases {
		if s := r.Status(p); s != Upcoming && s != "" {
			phase = p
		}
	}

	return phase
}

// StoppedPhase is the last phase whose status is one a run halts on, and
// false when none is.
func (r *Run) StoppedPhase() (string, bool) {
	for _, p := range slices.Backward(Phases) {
		if Stopped(r.Status(p)) {
			return p, true
		}
	}

	return "", false
}

// PhaseText is the status with its step where one belongs to it: `running:
// implement`, `blocked at judge`. The leaf is a pipeline step, so only ship
// carries one, except plan's judge, which fetch runs before planning.
func (r *Run) PhaseText(phase string) string {
	status := r.Status(phase)

	switch {
	case r.Leaf == "" || (phase != "ship" && (phase != "plan" || r.Leaf != "judge")):
		return status
	case status == "running":
		return status + ": " + r.LeafShown()
	case Stopped(status):
		return status + " at " + r.LeafShown()
	default:
		return status
	}
}
