package state

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// Statuses a phase can hold, in the order the usage text lists them.
var Statuses = []string{"pending", "running", "done", "failed", "partial", "skipped", "prior", "blocked"}

// A UsageError is a malformed call: an unknown phase or status, a missing
// argument. It is a typo in step prose, so the command exits 2 rather than
// leave the badge frozen without a word.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &UsageError{msg: fmt.Sprintf(format, args...)}
}

// Fresh is the run a session has before anything reported: every phase
// pending.
func Fresh(session string) *Run {
	return &Run{Session: session, Capture: "pending", Plan: "pending", Ship: "pending"}
}

// LoadOrFresh reads the session's file, or starts a fresh run when there is
// none. The bool says whether a file was read.
func LoadOrFresh(dir, session string) (*Run, bool) {
	r, err := Load(Path(dir, session))
	if err != nil {
		return Fresh(session), false
	}

	return r, true
}

// fallbackRoot is the checkout a run is about when nothing recorded one:
// steps and hooks both run with the checkout as cwd. A variable so tests
// stay off the real cwd.
var fallbackRoot = func() string {
	if top := gitx.Out(context.Background(), "", "rev-parse", "--show-toplevel"); top != "" {
		return top
	}

	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	if physical, err := filepath.EvalSymlinks(wd); err == nil {
		return physical
	}

	return wd
}

// Save writes the run in the format the status line's bash renderer reads,
// key for key, through a temp file so the polling renderer never sees a torn
// file. Every value is flattened to one line: a newline would start a new
// key.
func (r *Run) Save(dir string, now time.Time) error {
	if dir == "" || r.Session == "" {
		return errors.New("state: no directory or session to save to")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if r.Root == "" {
		r.Root = fallbackRoot()
	}

	r.Updated = time.Unix(now.Unix(), 0)

	var b strings.Builder

	b.WriteString("v 1\n")

	for _, kv := range [][2]string{
		{"task", r.Task},
		{"url", r.URL},
		{"root", r.Root},
		{"skill", r.Skill},
		{"capture", r.Capture},
		{"plan", r.Plan},
		{"ship", r.Ship},
		{"leaf", r.Leaf},
		{"final", r.Final},
		{"pipeline", strings.Join(r.Pipeline, PipelineSep)},
		{"note", r.Note},
		{"next", r.Next},
		{"ask", r.Ask},
		{"updated", strconv.FormatInt(r.Updated.Unix(), 10)},
	} {
		b.WriteString(kv[0] + " " + oneLine(kv[1]) + "\n")
	}

	path := Path(dir, r.Session)
	tmp := path + "." + strconv.Itoa(os.Getpid())

	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("state: %w", err)
	}

	return nil
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

// Clear drops a session's state file. Absent is fine.
func Clear(dir, session string) {
	_ = os.Remove(Path(dir, session))
}

// pruneAge is far past the TTL, so prune only reclaims files that already
// render nothing. Sessions end without notice, so every file outlives its run.
const pruneAge = 7 * 24 * time.Hour

// Prune drops the per-session files of long-dead sessions: run state, the
// conventions marker f10-inject.sh writes, and the session start mark.
func Prune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !slices.ContainsFunc([]string{fileExt, ".conventions", ".started"}, func(ext string) bool {
			return strings.HasSuffix(name, ext)
		}) {
			continue
		}

		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > pruneAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func (r *Run) setStatus(phase, status string) {
	switch phase {
	case "capture":
		r.Capture = status
	case "plan":
		r.Plan = status
	default:
		r.Ship = status
	}
}

// backfill retires the pending phases before the one being entered: a run
// from a task id never captures, one from a plan file never plans, and their
// leading circles would never fill. `skipped` means "nobody said"; positive
// evidence of earlier work (a task id, a saved plan) upgrades to `prior`.
func (r *Run) backfill(phase string) {
	if phase == "plan" || phase == "ship" {
		if r.Capture == "pending" {
			r.Capture = "skipped"
		}
	}

	if phase == "ship" && r.Plan == "pending" {
		r.Plan = "skipped"
	}
}

// Set records a phase's status. A stop state keeps the leaf (the argument,
// else the last running one): "blocked at judge" is the answer to "ship
// blocked". A phase running again retires the old note and question.
func (r *Run) Set(phase, status, leaf string) error {
	if !slices.Contains(Phases, phase) {
		return usagef("unknown phase '%s' (want: %s)", phase, strings.Join(Phases, " "))
	}

	if !slices.Contains(Statuses, status) {
		return usagef("unknown status '%s'", status)
	}

	r.backfill(phase)
	r.setStatus(phase, status)

	switch {
	case status == "running":
		r.Leaf, r.Note, r.Next, r.Ask = leaf, "", "", ""
	case Stopped(status):
		if leaf != "" {
			r.Leaf = leaf
		}
	default:
		r.Leaf = ""
	}

	return nil
}

// SetTask records the task the run is about. A tracker task id means the
// task predates this run, so an unreported capture becomes `prior`; `done`
// and `running` stay, since capture reports those itself.
func (r *Run) SetTask(id, url string) error {
	if id == "" {
		return usagef("task needs an id")
	}

	r.Task = id
	if url != "" {
		r.URL = url
	}

	if r.Capture == "pending" || r.Capture == "skipped" {
		r.Capture = "prior"
	}

	return nil
}

// SetNote records why a run stopped and what unblocks it.
func (r *Run) SetNote(reason, next string) error {
	if reason == "" {
		return usagef("note needs a reason")
	}

	r.Note, r.Next = oneLine(reason), oneLine(next)

	return nil
}

// SetAsk records the questionnaire a driven run stopped on, options
// included: no one is in its pane to answer.
func (r *Run) SetAsk(text string) error {
	if text == "" {
		return usagef("ask needs the question")
	}

	r.Ask = oneLine(text)

	return nil
}

// SetFinal declares the ship pipeline's last step, for the Stop backstop: a
// turn ending on it is a dropped `set ship done`; mid-pipeline is a pause.
func (r *Run) SetFinal(step string) error {
	if step == "" {
		return usagef("final needs a step name")
	}

	r.Final = step

	return nil
}

// SetPipeline declares the ship pipeline's steps, in order; its last is the
// final step.
func (r *Run) SetPipeline(steps []string) error {
	var kept []string

	for _, s := range steps {
		kept = append(kept, SplitPipeline(s)...)
	}

	if len(kept) == 0 {
		return usagef("pipeline needs at least one step")
	}

	r.Pipeline = kept
	r.Final = kept[len(kept)-1]

	return nil
}

// PlanWritten is the plan step's output landing: plan done, unreported, and
// the file's name is the task id.
func (r *Run) PlanWritten(task string) {
	r.Task = task
	r.backfill("plan")
	r.Plan = "done"
	r.Leaf = ""
}

// Backstop is the Stop hook's promotion, for the most-dropped call: the final
// `set ship done` after the PR and report. Stop states are left alone.
// Single-turn capture and plan promote; ship spans many turns, so it promotes
// only on the declared final step, or when no final or leaf is known
// (overstating is the cheaper error). It reports whether anything changed.
func (r *Run) Backstop() bool {
	changed := false

	if r.Capture == "running" {
		r.Capture, changed = "done", true
	}

	if r.Plan == "running" {
		r.Plan, changed = "done", true
	}

	if r.Ship == "running" && (r.Final == "" || r.Leaf == "" || r.Leaf == r.Final) {
		r.Ship, r.Leaf, changed = "done", "", true
	}

	return changed
}

var (
	idToken       = regexp.MustCompile(`^[A-Za-z]+-\d+$`)
	numberToken   = regexp.MustCompile(`^\d+$`)
	pipelineWord  = regexp.MustCompile(`^\s*[A-Za-z][A-Za-z0-9_-]*:\s+(.*)$`)
	dryRunPattern = "--dry-run"
)

func tokens(args string) []string {
	return strings.Fields(strings.ReplaceAll(args, dryRunPattern, ""))
}

func isURL(t string) bool { return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") }

// isDescription says whether the argument reads as a description, so the run
// will capture. A first token that is a task id, plan path or url is a
// reference whatever follows, since the model appends context to skill args.
// Otherwise one token is a reference and a sentence a description.
func isDescription(args string) bool {
	toks := tokens(args)
	if len(toks) == 0 {
		return false
	}

	first := toks[0]
	if strings.HasSuffix(first, ".md") || isURL(first) || idToken.MatchString(first) || numberToken.MatchString(first) {
		return false
	}

	return len(toks) > 1
}

// refTaskOf is the task the argument's first token names: a task id as is, a
// plan path by basename. Empty for a url, a description or nothing.
func refTaskOf(args string) string {
	toks := tokens(args)
	if len(toks) == 0 {
		return ""
	}

	first := toks[0]

	switch {
	case strings.HasSuffix(first, ".md"):
		return strings.TrimSuffix(filepath.Base(first), ".md")
	case isURL(first):
		return ""
	case idToken.MatchString(first) || numberToken.MatchString(first):
		return first
	}

	return ""
}

// Seed begins a run for a skill invocation and resets the phases, except a
// chain over one task (plan, then ship) whose argument names the task already
// held: that carries settled phases forward, or ship would show this
// session's plan as `skipped`. It reports false for --help, which runs
// nothing, so the badge keeps the last real run.
func (r *Run) Seed(skill, args, cwd string) bool {
	if slices.Contains(strings.Fields(args), "--help") {
		return false
	}

	// a leading pipeline name (`ship local: ABC-1042`) picks the pipeline,
	// not the task: the reference or description is what follows it
	if m := pipelineWord.FindStringSubmatch(args); m != nil {
		args = m[1]
	}

	chain := false

	if ref := refTaskOf(args); skill != "capture" && ref != "" && r.Task != "" {
		upRef, upTask := strings.ToUpper(ref), strings.ToUpper(r.Task)
		chain = upRef == upTask || (numberToken.MatchString(ref) && strings.HasSuffix(upTask, "-"+ref))
	}

	r.Skill = skill
	r.Leaf, r.Final, r.Note, r.Next, r.Ask = "", "", "", "", ""
	r.Pipeline = nil
	r.Ship = "pending"

	// the checkout root, so a terminal with no session id can still find its
	// runs; outside git the cwd is the root
	if cwd != "" {
		r.Root = cwd
		if top := gitx.Out(context.Background(), cwd, "rev-parse", "--show-toplevel"); top != "" {
			r.Root = top
		}
	}

	if chain {
		if !settled(r.Capture) {
			r.Capture = "pending"
		}

		// the plan skill is here to (re)plan; a kept `done` would lie over it
		if skill == "plan" || !settled(r.Plan) {
			r.Plan = "pending"
		}
	} else {
		r.Task, r.URL, r.Capture, r.Plan = "", "", "pending", "pending"
	}

	// open the first phase here, where the argument is: a description routes
	// plan and ship through capture; a reference sends plan to fetch
	switch skill {
	case "capture":
		r.Capture = "running"
	case "plan":
		if isDescription(args) {
			r.Capture = "running"
		} else {
			r.Plan = "running"
		}
	case "ship":
		if isDescription(args) {
			r.Capture = "running"
		}
	}

	// a plan path skips fetch, so its basename names the task; the file is
	// evidence of earlier work
	if toks := tokens(
		args,
	); (skill == "plan" || skill == "ship") && len(toks) > 0 &&
		strings.HasSuffix(toks[0], ".md") {
		r.Task = strings.TrimSuffix(filepath.Base(toks[0]), ".md")

		if r.Capture == "pending" {
			r.Capture = "prior"
		}

		if r.Plan == "pending" {
			r.Plan = "prior"
		}
	}

	return true
}

func settled(status string) bool {
	return status == "done" || status == "prior" || status == "partial"
}

// Show prints the run one `label value` line per fact, in `f10 status`
// order. A phase carries its glyph and, for ship, its step.
func (r *Run) Show(w io.Writer, now time.Time) {
	row := func(label, value string) { fmt.Fprintf(w, "%-8s %s\n", label, value) }

	row("task", cmpDash(r.Task))
	row("url", cmpDash(r.URL))

	if r.Root != "" {
		row("root", r.Root)
	}

	for _, p := range Phases {
		row(p, Glyph(r.Status(p))+" "+r.PhaseText(p))
	}

	for _, kv := range [][2]string{{"note", r.Note}, {"next", r.Next}, {"ask", r.Ask}, {"final", r.Final}} {
		if kv[1] != "" {
			row(kv[0], kv[1])
		}
	}

	row("updated", strconv.FormatInt(int64(now.Sub(r.Updated).Seconds()), 10)+"s ago")
}

func cmpDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// Glyph is the status line's default shape for a status; the renderer's
// F10_STATE_GLYPHS override applies to the status line only.
func Glyph(status string) string {
	switch status {
	case "running":
		return "◎"
	case "done":
		return "●"
	case "skipped":
		return "◌"
	case "failed":
		return "✗"
	case "prior":
		return "◉"
	case "partial":
		return "󰅚"
	case "blocked":
		return "󰜺"
	default:
		return "○"
	}
}
