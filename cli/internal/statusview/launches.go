package statusview

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// LaunchesHead heads the tree of what this session launched.
const LaunchesHead = "launched here"

// DropRows leaves out the rows drop picks, in tree order: what hung under a
// dropped row moves up to its level.
func DropRows(rows []TaskRow, drop func(TaskRow) bool) []TaskRow {
	var (
		out []TaskRow
		cut []int // the depths of the dropped rows above this one
	)

	for _, r := range rows {
		for len(cut) > 0 && cut[len(cut)-1] >= r.Depth {
			cut = cut[:len(cut)-1]
		}

		if drop(r) {
			cut = append(cut, r.Depth)

			continue
		}

		r.Depth -= len(cut)
		out = append(out, r)
	}

	return out
}

// launchKids is the session's launches as rows, nested under their bases
// as a parent's children are. A finished task is left out, and so is the
// session run's own task, which may be the parent the launches split.
func launchKids(ctx context.Context, warn io.Writer, t *target.Target, sid string) []stack.ChildRow {
	launches, err := state.Launches(state.Dir(), sid)
	if err != nil {
		ui.Warnf(warn, "reading this session's launches: %v", err)
	}

	if len(launches) == 0 {
		return nil
	}

	// a start's task has a branch from the start: none left is done with
	type token struct {
		text    string
		after   string // the base the drive recorded
		started bool
		queued  bool
		phase   string
	}

	var tokens []token

	for _, l := range launches {
		switch l.Kind {
		case state.LaunchStart:
			tokens = append(tokens, token{text: l.Value, started: true})
		case state.LaunchDrive:
			for _, task := range driveTasks(t.Layout.StorageRoot.Path(), l.Value) {
				tokens = append(tokens, token{text: task.ID, after: task.After, queued: task.Queued, phase: task.Phase})
			}
		}
	}

	var (
		kids []stack.ChildRow
		refs []ref.Ref
	)

	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	root := stack.TrackerFor(t).Storage
	bases := map[string]string{} // siblings share a base: resolve it once

	for _, tok := range tokens {
		id, suffix := ref.SplitSuffix(tok.text)

		r, err := t.Reference(ctx, id)
		if err != nil {
			continue
		}

		r = r.Head()
		k := stack.ChildRow{ID: r.ID, Suffix: suffix, Queued: tok.queued, Phase: tok.phase}

		if slices.ContainsFunc(
			kids,
			func(o stack.ChildRow) bool { return o.ID == k.ID && strings.EqualFold(o.Suffix, k.Suffix) },
		) {
			continue
		}

		// a finished task reads nothing further: its row would only drop
		branched, err := stack.HasTaskBranch(ctx, main, r.ID)
		if err == nil && (tok.started && !branched || stack.FinishedTask(hostops.TaskInfo{}, root, r.ID, branched)) {
			continue
		}

		k.After = launchBase(ctx, t, main, r.ID, suffix, tok.after, branched, bases)
		kids = append(kids, k)
		refs = append(refs, r)
	}

	// titles from the ledger, the tracker only on a miss: the typed status
	// answers inside a hook with a short timeout, and a row with no title
	// still reads, so a failed lookup says nothing
	known := TaskRefsFor(t, ctx, refs, false, false, io.Discard)
	for i := range kids {
		kids[i].Title, kids[i].URL = tasktitle.Clean(known[kids[i].ID].Title, kids[i].ID), known[kids[i].ID].URL
	}

	return stack.ChildTree(kids)
}

// launchBase is the base a launched task waits on, as an id: its branch's
// recorded one, else the one its drive recorded, "" once that base finished.
// seen holds each base as written against its answer.
func launchBase(
	ctx context.Context,
	t *target.Target,
	main, id, suffix, recorded string,
	branched bool,
	seen map[string]string,
) string {
	base := recorded
	if branched {
		if x, err := stack.LoadBranchIndex(ctx, main); err == nil {
			if b, err := x.Find(ctx, "", id, suffix, stack.PolicyLocal); err == nil && b.Name != "" {
				base = cmp.Or(branchmeta.Out(ctx, main, b.Name, branchmeta.After), base)
			}
		}
	}

	if base == "" {
		return ""
	}

	if out, ok := seen[base]; ok {
		return out
	}

	out := ""
	if r, err := t.Reference(ctx, base); err == nil {
		out = r.Head().ID

		has, err := stack.HasTaskBranch(ctx, main, out)
		if err != nil || stack.FinishedTask(hostops.TaskInfo{}, stack.TrackerFor(t).Storage, out, has) {
			out = ""
		}
	}

	seen[base] = out

	return out
}

// driveTasks is drive n's tasks as typed, none once the drive is done or
// cancelled, or its record is gone. Queued, parked and waiting hold only
// while the driver runs: a stopped drive holds no task back.
func driveTasks(root, n string) []drives.Task {
	num, err := strconv.Atoi(n)
	if err != nil {
		return nil
	}

	d, ok := drives.ActiveOne(root, num)
	if !ok {
		return nil
	}

	if !d.Running {
		for i := range d.Tasks {
			d.Tasks[i].Queued, d.Tasks[i].Phase = false, ""
		}
	}

	return d.Tasks
}
