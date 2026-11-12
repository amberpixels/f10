package statusview

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// BareStatus is what `status` with no task id prints, and whether it
// printed a question a driven agent waits on.
func BareStatus(ctx context.Context, v StatusView, dir string, scoped bool, sid string, all bool) (bool, error) {
	now := time.Now()

	// the listing below reads the reader's runs when the guesses built one
	var rd *statusReader

	// the session run's task before the checkout's, the reverse of ref.Lookup,
	// since the run is what status reports; in a checkout naming no task the
	// launches come first, as the session run's task would hide them
	if !all && !scoped {
		if t, err := target.At(ctx, dir, false, "status"); err == nil {
			if rd, err = NewStatusReader(t, now); err != nil {
				return false, err
			}

			here := checkoutTaskID(ctx, t)
			if here == "" && sid != "" {
				if p, err := rd.launches(ctx, v, sid); p.Printed {
					return p.asks, err
				}
			}

			if id := cmp.Or(sessionTaskID(sid), here); id != "" {
				if p, err := rd.Task(ctx, v, id, true); p.Printed {
					return p.asks, err
				}
			}
		}
	}

	var (
		runs     []*state.Run
		showRoot bool
	)

	switch {
	case sid != "" && !all:
		r, err := state.Load(state.Path(state.Dir(), sid))
		if errors.Is(err, os.ErrNotExist) {
			_, err = fmt.Fprintln(v.Out, "no f10 run in this session")

			return false, err
		}

		if err != nil {
			return false, err
		}

		runs = []*state.Run{r}
	default:
		live, err := liveRuns(rd, now)
		if err != nil {
			return false, err
		}

		showRoot = true
		runs = live

		if !all {
			root := layout.Locate(ctx, dir).CheckoutRoot
			runs = InRoot(live, root)

			if len(runs) == 0 {
				_, err = fmt.Fprintf(v.Out, "no live f10 run in %s (--all lists every session)\n", root)

				return false, err
			}
		}

		if len(runs) == 0 {
			_, err = fmt.Fprintln(v.Out, "no live f10 run on this machine")

			return false, err
		}
	}

	asks := slices.ContainsFunc(runs, func(r *state.Run) bool { return r.Ask != "" })

	return asks, writeRuns(v, runs, make([][]gate, len(runs)), nil, showRoot, "", now)
}

// liveRuns is the reader's runs, else a fresh read of the state dir.
func liveRuns(rd *statusReader, now time.Time) ([]*state.Run, error) {
	if rd != nil {
		return rd.live, nil
	}

	return state.List(state.Dir(), now, state.TTL())
}

// sessionTaskID is the task the session's run names, "" for none.
func sessionTaskID(sid string) string {
	if sid == "" {
		return ""
	}

	if r, err := state.Load(state.Path(state.Dir(), sid)); err == nil {
		return r.Task
	}

	return ""
}

// checkoutTaskID is the task the branch names, else the one a linked
// worktree's directory names on a detached HEAD, "" for none.
func checkoutTaskID(ctx context.Context, t *target.Target) string {
	if r, err := t.BranchReference(ctx, ""); err == nil {
		return r.Label()
	}

	linked := t.Layout.MainRoot != "" && gitx.Resolved(t.Layout.MainRoot) != gitx.Resolved(t.Layout.CheckoutRoot)
	if !linked || gitx.Out(ctx, t.Dir, "rev-parse", "--abbrev-ref", "HEAD") != "HEAD" {
		return ""
	}

	return ref.Match(t.Effective.IDPrefix, filepath.Base(t.Layout.CheckoutRoot))
}

// taskStatusInput is what `status <id>` knows about a task before reading
// any run.
type taskStatusInput struct {
	ID      string
	Branch  string
	Root    string        // the checkout holding branch
	Main    string        // the main checkout, where the branch's refs and config live
	Plan    string        // where the task's plan would be
	Handoff string        // where the task's handoff would be
	storage string        // the storage root holding the task's verdicts and findings
	Host    *hostops.Host // nil without a host CLI: no PR row
	agent   string        // the profile the branch records, as typed; "" for the kind's default
}

// errNoRunToRead is a task with no branch, or a branch checked out
// nowhere: no run to read, though a finished one still reports.
var errNoRunToRead = errors.New("no run to read")

// taskStatusFor resolves the task to its checkout.
func taskStatusFor(ctx context.Context, t *target.Target, r ref.Ref, suffix string) (taskStatusInput, error) {
	// a bundle's run, plan and branch are its lead's
	r = r.Head()

	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)

	name, path, err := stack.TaskCheckout(ctx, main, t.Driver(), r, suffix)

	switch {
	case err != nil:
		return taskStatusInput{}, err
	case name == "":
		return taskStatusInput{}, fmt.Errorf("%s has no branch here: %w", r.ID, errNoRunToRead)
	case path == "":
		return taskStatusInput{}, fmt.Errorf("branch %s is checked out nowhere: %w", name, errNoRunToRead)
	}

	in := taskStatusInput{
		ID:      r.ID,
		Branch:  name,
		Root:    path,
		Main:    main,
		Plan:    t.Layout.StorageRoot.Plan(r.ID),
		Handoff: archive.TaskHandoff(t.Layout.StorageRoot, r.ID),
		storage: t.Layout.StorageRoot.Path(),
	}

	if h, err := hostops.HostOf(t); err == nil {
		h = h.In(path)
		in.Host = &h
	}

	// a branch with no record, or one unreadable, shows no agent row
	if p, err := runplan.RecordedProfile(ctx, main, name); err == nil && !p.IsDefault() {
		in.agent = p.Short()
	}

	return in, nil
}

// taskRuns splits the checkout's runs into the task's own and the rest,
// since every skill call reseeds its run and the agent may be on another
// task by now; an empty task is the task's only in a phase skill's run that
// fetch has not named yet, since a catchup's run names no task either.
func taskRuns(runs []*state.Run, id string) ([]*state.Run, []*state.Run) {
	var own, others []*state.Run

	for _, r := range runs {
		fresh := r.Task == "" && state.OpensPhase(r.Skill) && (r.Capture == state.Upcoming || r.Capture == "skipped")

		if strings.EqualFold(r.Task, id) || fresh {
			own = append(own, r)
		} else {
			others = append(others, r)
		}
	}

	return own, others
}

// fallback is what `status <id>` reports for a task no live run is about:
// the runs in its checkout, which are on something else, then what is durable.
// The flat keys predate the shipment and repeat what it holds.
type fallback struct {
	Task    string       `json:"task"`
	Root    string       `json:"root"`
	Others  []*state.Run `json:"others"`
	Plan    string       `json:"plan,omitempty"`
	PR      string       `json:"pr,omitempty"`
	PRState string       `json:"pr_state,omitempty"`

	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`

	Shipment *Shipment `json:"shipment,omitempty"`
}

func (fb fallback) Render(w io.Writer, limit int, link bool) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	var rows []render.Fact
	if fb.Shipment != nil {
		rows = fb.Shipment.Facts(link)
	}

	rows = append(rows, RunFact("run", "none for "+fb.Task+" in "+fb.Root, faint))

	for _, r := range fb.Others {
		rows = append(rows, RunFact("agent", textx.OrDash(r.Task)+" · "+currentPhase(r), plain))
	}

	if fb.PRState != "" {
		rows = append(rows, RunFact("pr state", fb.PRState, plain))
	}

	_ = render.WriteFacts(w, rows, render.LabelWidth(rows), limit)
}

// taskFallback lays the task's shipment out as the fallback.
//
//nolint:lostfield // a reduction: the fallback shows the shipment, not every input
func taskFallback(in taskStatusInput, others []*state.Run, ship Shipment) fallback {
	fb := fallback{
		Task: in.ID, Root: in.Root, Others: others, Plan: ship.Plan, PR: ship.PR, PRState: ship.PRState,
		Verdicts: ship.Verdicts, Reviews: ship.Reviews, Shipment: &ship,
	}
	if fb.Others == nil {
		fb.Others = []*state.Run{} // an empty list, not null, for a JSON reader
	}

	return fb
}

// writeTaskRows prints task rows as a tree headed by parent, or encodes them
// under --json.
func writeTaskRows(v StatusView, rows []TaskRow, parent string) error {
	if v.JSON {
		return ui.JSON(v.Out, rows)
	}

	rv := RowView{Parent: parent, Plain: v.Plain}

	switch {
	case rv.Plain:
		rv.Width = ui.PlainWidth()
	case ui.IsTerminal(v.Out):
		rv.Term, rv.Width = true, ui.Width(v.Out)
	}

	return RenderRows(v.Out, rows, rv)
}

// InRoot keeps the runs recorded against root. The hook stores git's
// toplevel and the layout package stores the physical path, so both sides are
// compared in physical form.
func InRoot(runs []*state.Run, root string) []*state.Run {
	var kept []*state.Run

	for _, r := range runs {
		if r.Root != "" && gitx.Resolved(r.Root) == gitx.Resolved(root) {
			kept = append(kept, r)
		}
	}

	return kept
}
