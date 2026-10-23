package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The status noun reads what the badge reads, and says the rest: which
// step a run stopped on, why, and what unblocks it. Inside a session the
// session id is in the environment and the answer is that one run; from a
// plain terminal there is none, so it lists this repo's live runs, and
// --all every live run on the machine. A task id names another run: the
// task's own, reported from the checkout that has the task's branch, which
// is how a session reads the question a driven agent stopped on. With no
// run of its own, the task gets what is durable: its plan file and its PR.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "where this session's f10 run is, a task's run in its own worktree, or every live run in this repo",
		ArgsUsage: "[task-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "every live run on this machine, not only this repo's"},
			&cli.BoolFlag{Name: "json", Usage: "emit the same runs as JSON"},
		},
		Action: runStatus,
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	dir := state.Dir()
	now := time.Now()
	all := cmd.Bool("all")
	token := strings.TrimSpace(cmd.Args().First())

	var (
		runs     []*state.Run
		showRoot bool
	)

	switch sid := state.Session(); {
	case token != "":
		if all {
			return errors.New("status takes a task id or --all, not both: the id already picks a checkout")
		}

		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		id, suffix := ref.SplitSuffix(token)

		r, err := t.reference(ctx, id)
		if err != nil {
			return err
		}

		if rows, ok, err := parentRows(ctx, t, r); err != nil || ok {
			if err != nil {
				return err
			}

			return writeTaskRows(cmd, rows)
		}

		in, err := taskStatusFor(ctx, t, r, suffix)
		if errors.Is(err, errNoRunToRead) {
			row, ferr := finishedRow(ctx, t, r.Head())
			if ferr != nil || row == nil {
				return cmp.Or(ferr, err)
			}

			return writeTaskRows(cmd, []taskRow{*row})
		}

		if err != nil {
			return err
		}

		live, err := state.List(dir, now, state.TTL())
		if err != nil {
			return err
		}

		var others []*state.Run

		showRoot = true
		runs, others = taskRuns(inRoot(live, in.root), in.id)

		if len(runs) == 0 {
			fb := taskFallback(ctx, in, others)

			if cmd.Bool("json") {
				return encodeJSON(cmd.Writer, fb)
			}

			fb.render(cmd.Writer, termWidth(cmd.Writer))

			return nil
		}
	case sid != "" && !all:
		r, err := state.Load(state.Path(dir, sid))
		if errors.Is(err, os.ErrNotExist) {
			_, err = fmt.Fprintln(cmd.Writer, "no f10 run in this session")

			return err
		}

		if err != nil {
			return err
		}

		runs = []*state.Run{r}
	default:
		live, err := state.List(dir, now, state.TTL())
		if err != nil {
			return err
		}

		showRoot = true
		runs = live

		if !all {
			root, err := checkoutRoot(ctx, cmd)
			if err != nil {
				return err
			}

			runs = inRoot(live, root)

			if len(runs) == 0 {
				_, err = fmt.Fprintf(cmd.Writer, "no live f10 run in %s (--all lists every session)\n", root)

				return err
			}
		}

		if len(runs) == 0 {
			_, err = fmt.Fprintln(cmd.Writer, "no live f10 run on this machine")

			return err
		}
	}

	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, runs)
	}

	limit := termWidth(cmd.Writer)

	for i, r := range runs {
		if i > 0 {
			fmt.Fprintln(cmd.Writer)
		}

		renderRun(cmd.Writer, r, showRoot, now, limit)
	}

	return nil
}

// taskStatusInput is what `status <id>` knows about a task before reading
// any run: the checkout holding its branch, where the agent working on it
// reports, and where its durable facts are looked up when no run is its own.
type taskStatusInput struct {
	id     string
	branch string
	root   string // the checkout holding branch
	plan   string // where the task's plan would be
	host   *host  // nil without a host CLI: no PR row
}

// errNoRunToRead is a task with no branch, or a branch checked out
// nowhere: no run to read, though a finished one still reports.
var errNoRunToRead = errors.New("no run to read")

// taskStatusFor resolves the task to its checkout.
func taskStatusFor(ctx context.Context, t *target, r ref.Ref, suffix string) (taskStatusInput, error) {
	// a bundle's run, plan and branch are its lead's
	r = r.Head()

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	name, path, err := taskCheckout(ctx, main, t.driver(), r, suffix)

	switch {
	case err != nil:
		return taskStatusInput{}, err
	case name == "":
		return taskStatusInput{}, fmt.Errorf("%s has no branch here: %w", r.ID, errNoRunToRead)
	case path == "":
		return taskStatusInput{}, fmt.Errorf("branch %s is checked out nowhere: %w", name, errNoRunToRead)
	}

	in := taskStatusInput{
		id:     r.ID,
		branch: name,
		root:   path,
		plan:   filepath.Join(taskPlansDir(path, t.sharedPlansDir()), r.ID+".md"),
	}

	if h, err := t.host(); err == nil {
		h = h.at(path)
		in.host = &h
	}

	return in, nil
}

// taskRuns splits runs from the task's checkout into the task's own and
// the rest. Every skill call reseeds its session's run, so the agent in a
// task's worktree may be on another task by now. An empty task counts as
// the task's only when the run was seeded from a reference, before fetch
// names it: capture `pending`, or `skipped` once fetch opens the plan phase.
// Capturing, or planning from a description, is about some other task.
func taskRuns(runs []*state.Run, id string) ([]*state.Run, []*state.Run) {
	var own, others []*state.Run

	for _, r := range runs {
		fresh := r.Task == "" && (r.Capture == "pending" || r.Capture == "skipped")

		if strings.EqualFold(r.Task, id) || fresh {
			own = append(own, r)
		} else {
			others = append(others, r)
		}
	}

	return own, others
}

// fallback is what `status <id>` reports for a task no live run is about:
// the runs in its checkout, which are on something else, then its plan file
// and its PR, each only where it exists.
type fallback struct {
	Task    string       `json:"task"`
	Root    string       `json:"root"`
	Others  []*state.Run `json:"others"`
	Plan    string       `json:"plan,omitempty"`
	PR      string       `json:"pr,omitempty"`
	PRState string       `json:"pr_state,omitempty"`
}

// taskFallback gathers the fallback. Status only reads, so a host that
// cannot answer drops the PR rather than failing the report.
func taskFallback(ctx context.Context, in taskStatusInput, others []*state.Run) fallback {
	fb := fallback{Task: in.id, Root: in.root, Others: others}
	if fb.Others == nil {
		fb.Others = []*state.Run{} // an empty list, not null, for a JSON reader
	}

	if exists(in.plan) {
		fb.Plan = in.plan
	}

	if in.host != nil {
		if p, err := in.host.pull(ctx, in.branch); err == nil && p.url() != "" {
			fb.PR, fb.PRState = p.url(), p.status()
		}
	}

	return fb
}

func (fb fallback) render(w io.Writer, limit int) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	headerRow(w, "task", fb.Task, limit, plain)
	headerRow(w, "run", "none for "+fb.Task+" in "+fb.Root, limit, faint)

	for _, r := range fb.Others {
		headerRow(w, "agent", valueOr(r.Task)+" · "+currentPhase(r), limit, plain)
	}

	if fb.Plan != "" {
		headerRow(w, "plan", fb.Plan, limit, plain)
	}

	if fb.PR != "" {
		headerRow(w, "pr", fb.PR+" ("+fb.PRState+")", limit, plain)
	}
}

// writeTaskRows prints task rows, or encodes them under --json.
func writeTaskRows(cmd *cli.Command, rows []taskRow) error {
	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, rows)
	}

	renderRows(cmd.Writer, rows, termWidth(cmd.Writer))

	return nil
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}

	return nil
}

// currentPhase is the furthest phase a run has reached, with its status:
// `capture running`, `ship blocked at judge`.
func currentPhase(r *state.Run) string {
	phase := state.Phases[0]

	for _, p := range state.Phases {
		if r.Status(p) != "pending" {
			phase = p
		}
	}

	return phase + " " + r.PhaseText(phase)
}

// checkoutRoot is the repo a plain terminal asks about: -C's target or the
// cwd, in physical form, the way the seed hook recorded root.
func checkoutRoot(ctx context.Context, cmd *cli.Command) (string, error) {
	dir, _, err := rootDir(cmd)
	if err != nil {
		return "", err
	}

	return layout.Locate(ctx, dir).CheckoutRoot, nil
}

// inRoot keeps the runs recorded against root. The hook stores git's
// toplevel and the layout package stores the physical path, so both sides are
// compared in physical form.
func inRoot(runs []*state.Run, root string) []*state.Run {
	var kept []*state.Run

	for _, r := range runs {
		if r.Root != "" && canonPath(r.Root) == canonPath(root) {
			kept = append(kept, r)
		}
	}

	return kept
}

func canonPath(p string) string {
	if c, err := filepath.EvalSymlinks(p); err == nil {
		return c
	}

	return p
}

// renderRun prints one run as the label/value block config uses, in the
// order `f10 state show` prints the same facts. The root row appears only
// when listing: for one session it is the directory you are in.
func renderRun(w io.Writer, r *state.Run, showRoot bool, now time.Time, limit int) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	headerRow(w, "task", valueOr(r.Task), limit, plain)
	headerRow(w, "url", valueOr(r.URL), limit, plain)

	if showRoot {
		headerRow(w, "root", valueOr(r.Root), limit, faint)
	}

	for _, p := range state.Phases {
		headerRow(w, p, r.PhaseText(p), limit, statusStyle(r.Status(p)))
	}

	if r.Note != "" {
		headerRow(w, "note", r.Note, limit, plain)
	}

	if r.Next != "" {
		headerRow(w, "next", r.Next, limit, plain)
	}

	if r.Ask != "" {
		headerRow(w, "ask", r.Ask, limit, plain)
	}

	if r.Final != "" {
		headerRow(w, "final", r.Final, limit, faint)
	}

	age := ago(now, r.Updated)
	if !r.Live(now, state.TTL()) {
		age += " (stale)"
	}

	headerRow(w, "updated", age, limit, faint)
}

func valueOr(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// statusStyle is the badge's palette: shape first, color second, so the
// same status reads the same in the status line and here.
func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch status {
	case "done":
		return s.Foreground(lipgloss.Color("2"))
	case "running":
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	case "failed":
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case "partial":
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case "blocked":
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case "prior":
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	default:
		return s.Faint(true)
	}
}

// ago is a duration a human reads at a glance: seconds under a minute,
// then the largest whole unit that fits.
func ago(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}

	d := now.Sub(then).Round(time.Second)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
