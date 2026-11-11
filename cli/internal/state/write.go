package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/flock"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/taskid"
)

// Upcoming is a phase or step in the run's flow that the run has not reached:
// nothing running or waiting on it.
const Upcoming = "upcoming"

// Statuses a phase can hold, in the order the usage text lists them.
var Statuses = []string{Upcoming, "running", "done", "failed", "partial", "skipped", "prior", "blocked"}

// A UsageError is a typo in step prose (an unknown phase or status, a missing
// argument), so the command exits 2 rather than leave the badge frozen.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// LegacyStatus reads the name a not-reached phase had before it was Upcoming,
// still held by older run files, ledger claims and older callers.
func LegacyStatus(status string) string {
	if status == "pending" {
		return Upcoming
	}

	return status
}

func usagef(format string, args ...any) error {
	return &UsageError{msg: fmt.Sprintf(format, args...)}
}

// Fresh is the run a session has before anything reported: every phase
// upcoming.
func Fresh(session string) *Run {
	return &Run{Session: session, Capture: Upcoming, Plan: Upcoming, Ship: Upcoming}
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
var fallbackRoot = func(ctx context.Context) string {
	if top, err := gitx.Toplevel(ctx, ""); err == nil {
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

// ErrBusy is a session lock another writer held for the whole wait.
var ErrBusy = errors.New("state: the session is being written")

// Update loads the session's run, applies change and saves it, all under
// the session's lock, so the hooks and f10 state, separate processes, never
// save a stale copy over each other. change reports whether to save; loaded
// says a file was read. A lock not taken within wait is ErrBusy.
func Update(
	ctx context.Context,
	dir, session string,
	wait time.Duration,
	now time.Time,
	change func(r *Run, loaded bool) (bool, error),
) error {
	if dir == "" || session == "" {
		return errors.New("state: no directory or session to update")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	release, err := flock.Wait(Path(dir, session)+lockExt, wait)
	if errors.Is(err, flock.ErrHeld) {
		return ErrBusy
	}

	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	defer release()

	r, loaded := LoadOrFresh(dir, session)

	save, err := change(r, loaded)
	if err != nil || !save {
		return err
	}

	return r.Save(ctx, dir, now)
}

// Save writes the run as the status line's bash renderer reads it, through a
// temp file so the polling renderer never sees a torn file; values are
// flattened to one line, since a newline would start a new key.
func (r *Run) Save(ctx context.Context, dir string, now time.Time) error {
	if dir == "" || r.Session == "" {
		return errors.New("state: no directory or session to save to")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if r.Root == "" {
		r.Root = fallbackRoot(ctx)
	}

	r.Updated = time.Unix(now.Unix(), 0)

	var b strings.Builder

	b.WriteString("v 1\n")

	// the done signal is a key most runs never write: absent reads as none
	var signal [][2]string
	if r.Done != "" {
		signal = [][2]string{{"done", r.Done}, {"done_at", unixText(r.DoneAt)}}
	}

	// so is a wait, which only a driven ship on an empty base writes, and a
	// stop kind, which only ship's catchup guard writes
	if r.Stop != "" {
		signal = append([][2]string{{"stop", r.Stop}}, signal...)
	}

	if r.Wait != "" {
		signal = append([][2]string{{"wait", r.Wait}}, signal...)
	}

	// and labels, which only a flow that names its entries has
	if r.Labels != nil {
		signal = append([][2]string{{"labels", joinLabels(r.Labels)}}, signal...)
	}

	for _, kv := range slices.Concat([][2]string{
		{"task", r.Task},
		{"url", r.URL},
		{"root", r.Root},
		{"skill", r.Skill},
		{"capture", r.Capture},
		{"plan", r.Plan},
		{"ship", r.Ship},
		{"leaf", r.Leaf},
		{"at", atText(r.At)},
		{"final", r.Final},
		{"pipeline", strings.Join(r.Pipeline, PipelineSep)},
		{"note", r.Note},
		{"next", r.Next},
		{"ask", r.Ask},
	}, signal, [][2]string{
		{"updated", strconv.FormatInt(r.Updated.Unix(), 10)},
	}) {
		b.WriteString(kv[0] + " " + oneLine(kv[1]) + "\n")
	}

	if err := fsx.WriteFile(Path(dir, r.Session), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	return nil
}

func unixText(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return strconv.FormatInt(t.Unix(), 10)
}

func atText(at int) string {
	if at <= 0 {
		return ""
	}

	return strconv.Itoa(at)
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

// Clear drops a session's state file under the session's lock, so a writer
// mid-update cannot save the cleared run back. Absent is fine; a lock held
// past the wait still clears.
func Clear(dir, session string) {
	if release, err := flock.Wait(Path(dir, session)+lockExt, clearWait); err == nil {
		defer release()
	}

	_ = os.Remove(Path(dir, session))
}

const clearWait = 2 * time.Second

// RootOf is the checkout root a cwd is in; outside git, the cwd itself.
func RootOf(ctx context.Context, cwd string) string {
	if top, err := gitx.Toplevel(ctx, cwd); err == nil {
		return top
	}

	return cwd
}

// pruneAge is far past the TTL, so prune only reclaims files that already
// render nothing. Sessions end without notice, so every file outlives its run.
const pruneAge = 7 * 24 * time.Hour

// Prune drops the per-session files of long-dead sessions; .conventions is
// the marker f10-inject.sh writes.
func Prune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() ||
			!slices.ContainsFunc(
				[]string{
					fileExt, lockExt, ".conventions", startedExt, modelExt, waitExt, backgroundExt,
					promptExt, launchesExt, asksExt,
				},
				func(ext string) bool { return strings.HasSuffix(name, ext) },
			) {
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

// backfill retires the upcoming phases before the one entered, or a run from a
// task id or a plan file would never fill its leading circles. `skipped` means
// nobody said; evidence of earlier work (a task id, a saved plan) is `prior`.
func (r *Run) backfill(phase string) {
	if phase == "plan" || phase == "ship" {
		if r.Capture == Upcoming {
			r.Capture = "skipped"
		}
	}

	if phase == "ship" && r.Plan == Upcoming {
		r.Plan = "skipped"
	}
}

// Set records a phase's status. A stop keeps the leaf, the argument else the
// last running one, since "blocked at judge" is the answer to "ship blocked";
// running again retires the note, question and done signal, which a stop
// keeps for the driven catchup that wrote it before restoring.
func (r *Run) Set(phase, status, leaf string) error {
	if !slices.Contains(Phases, phase) {
		return usagef("unknown phase '%s' (want: %s)", phase, strings.Join(Phases, " "))
	}

	status = LegacyStatus(status)
	if !slices.Contains(Statuses, status) {
		return usagef("unknown status '%s'", status)
	}

	// a label names the entry for a person; the run records the step
	leaf, label := flow.CutLabel(leaf)

	r.backfill(phase)
	r.setStatus(phase, status)

	switch {
	case status == "running":
		r.Leaf, r.Note, r.Next, r.Ask, r.Wait, r.Stop = leaf, "", "", "", "", ""
		r.Done, r.DoneAt = "", time.Time{}
	case Stopped(status):
		if leaf != "" {
			r.Leaf = leaf
		}
	default:
		r.Leaf = ""
	}

	// only ship's leaves are pipeline entries
	at := 0
	if phase == "ship" {
		at = r.place(r.Leaf, label)
	}

	r.At = at

	return nil
}

// place is leaf's place in the pipeline from 1: the entry its label names,
// else where the run is, else the next entry by that name, else the first.
// An unnamed leaf (catchup's guard stopping) keeps the place, so a judge
// resumed after it is still the second.
func (r *Run) place(leaf, label string) int {
	if leaf == "" {
		return 0
	}

	if k := slices.Index(r.Labels, label); label != "" && k >= 0 && k < len(r.Pipeline) && r.Pipeline[k] == leaf {
		return k + 1
	}

	at := min(max(r.At, 0), len(r.Pipeline))
	if at > 0 && r.Pipeline[at-1] == leaf {
		return at
	}

	if k := slices.Index(r.Pipeline[at:], leaf); k >= 0 {
		return at + k + 1
	}

	if k := slices.Index(r.Pipeline, leaf); k >= 0 {
		return k + 1
	}

	return at
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

	if r.Capture == Upcoming || r.Capture == "skipped" {
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

// SetWait records the base task a driven ship stopped for, its branch
// without code yet. Not an ask: no one is asked, and a drive waits on it.
func (r *Run) SetWait(task string) error {
	if task == "" {
		return usagef("wait needs the base task")
	}

	r.Wait = oneLine(task)

	return nil
}

// StopGuard is the stop ship's catchup guard leaves: a conflict it marked
// the branch for, which a drive catches up before sending ship again.
const StopGuard = "guard"

// SetStop records the kind of stop a ship left; only StopGuard exists.
func (r *Run) SetStop(kind string) error {
	if kind != StopGuard {
		return usagef("unknown stop kind '%s'", kind)
	}

	r.Stop = kind

	return nil
}

// SetDone records that a phaseless step (catchup) reported at now: the drive
// reads it as the step's end, and an idle agent without it stopped without
// reporting. It touches no phase, so the badge a ship run left stands.
func (r *Run) SetDone(step string, now time.Time) error {
	if step == "" {
		return usagef("done needs the step that reported")
	}

	r.Done, r.DoneAt = step, time.Unix(now.Unix(), 0)

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
	var kept, labels []string

	labeled := false

	for _, s := range steps {
		for _, e := range SplitPipeline(s) {
			entry, label := flow.CutLabel(e)
			kept, labels = append(kept, entry), append(labels, label)
			labeled = labeled || label != ""
		}
	}

	if len(kept) == 0 {
		return usagef("pipeline needs at least one step")
	}

	if !labeled {
		labels = nil
	}

	r.Pipeline, r.Labels = kept, labels
	r.Final = kept[len(kept)-1]
	r.At = 0

	return nil
}

// PlanWritten is the plan step's output landing: plan done, unreported, and
// the file's name is the task id.
func (r *Run) PlanWritten(task string) {
	r.Task = task
	r.backfill("plan")
	r.Plan = "done"
	r.Leaf, r.At = "", 0
}

// Backstop is the Stop hook's promotion of a running phase, for the most
// dropped call: the final `set ship done`. Ship spans many turns, so it
// promotes only on the declared final step, or with no final or leaf known,
// overstating being the cheaper error. True when anything changed.
func (r *Run) Backstop() bool {
	changed := false

	if r.Capture == "running" {
		r.Capture, changed = "done", true
	}

	if r.Plan == "running" {
		r.Plan, changed = "done", true
	}

	if r.Ship == "running" && (r.Final == "" || r.Leaf == "" || r.Leaf == r.Final) {
		r.Ship, r.Leaf, r.At, changed = "done", "", 0, true
	}

	return changed
}

var (
	pipelineWord  = regexp.MustCompile(`^\s*[A-Za-z][A-Za-z0-9_-]*:\s+(.*)$`)
	dryRunPattern = "--dry-run"
)

func tokens(args string) []string {
	return strings.Fields(strings.ReplaceAll(args, dryRunPattern, ""))
}

func isURL(t string) bool { return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") }

// isDescription reads a first token that is an id, plan path or url as a
// reference whatever follows, since the model appends context to skill args;
// otherwise one token is a reference and a sentence a description.
func isDescription(args string) bool {
	toks := tokens(args)
	if len(toks) == 0 {
		return false
	}

	first, _, _ := strings.Cut(toks[0], "+")
	if strings.HasSuffix(first, ".md") || isURL(first) || taskid.IsWord(first) || taskid.IsNumber(first) {
		return false
	}

	return len(toks) > 1
}

// refTaskOf is the task the first token names, a bundle by its lead.
func refTaskOf(args string) string {
	toks := tokens(args)
	if len(toks) == 0 {
		return ""
	}

	first, _, _ := strings.Cut(toks[0], "+")

	switch {
	case strings.HasSuffix(first, ".md"):
		return strings.TrimSuffix(filepath.Base(first), ".md")
	case isURL(first):
		return ""
	case taskid.IsWord(first) || taskid.IsNumber(first):
		return first
	}

	return ""
}

// createsTask is a skill whose argument describes a task still to be
// created, never a reference: debug's error text can hold an id-shaped token.
func createsTask(skill string) bool {
	return skill == "capture" || skill == "debug"
}

// Seed begins a run for a skill invocation. A chain over the task already held
// (plan, then ship) carries settled phases forward, or ship would show this
// session's plan as `skipped`; --help runs nothing and reports false, so the
// badge keeps the last real run.
func (r *Run) Seed(ctx context.Context, skill, args, cwd string) bool {
	if slices.Contains(strings.Fields(args), "--help") {
		return false
	}

	// a leading pipeline name (`ship local: ABC-1042`) is not the reference
	if m := pipelineWord.FindStringSubmatch(args); m != nil {
		args = m[1]
	}

	chain := false

	if ref := refTaskOf(args); !createsTask(skill) && ref != "" && r.Task != "" {
		upRef, upTask := strings.ToUpper(ref), strings.ToUpper(r.Task)
		chain = upRef == upTask || (taskid.IsNumber(ref) && strings.HasSuffix(upTask, "-"+ref))
	}

	r.Skill = skill
	r.Leaf, r.Final, r.Note, r.Next, r.Ask, r.Wait, r.Stop = "", "", "", "", "", "", ""
	r.Done, r.DoneAt = "", time.Time{}
	r.Pipeline, r.Labels, r.At = nil, nil, 0
	r.Ship = Upcoming

	// so a terminal with no session id can still find its runs
	if cwd != "" {
		r.Root = RootOf(ctx, cwd)
	}

	if chain {
		if !settled(r.Capture) {
			r.Capture = Upcoming
		}

		// the plan skill is here to (re)plan; a kept `done` would lie over it
		if skill == "plan" || !settled(r.Plan) {
			r.Plan = Upcoming
		}
	} else {
		r.Task, r.URL, r.Capture, r.Plan = "", "", Upcoming, Upcoming

		// a full id ties the run to its board row before fetch reports; a
		// bare number waits for fetch, which knows the prefix
		if ref := refTaskOf(args); !createsTask(skill) && taskid.IsWord(ref) {
			r.Task = strings.ToUpper(ref)
		}
	}

	// the first phase opens here, where the argument is: a description
	// captures first
	switch skill {
	case "capture", "debug":
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

		if r.Capture == Upcoming {
			r.Capture = "prior"
		}

		if r.Plan == Upcoming {
			r.Plan = "prior"
		}
	}

	return true
}

// OpensPhase reports whether the skill runs a phase (debug captures), so its
// run may be a task's before fetch names one.
func OpensPhase(skill string) bool {
	return skill == "debug" || slices.Contains(Phases, skill)
}

func settled(status string) bool {
	return status == "done" || status == "prior" || status == "partial"
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
