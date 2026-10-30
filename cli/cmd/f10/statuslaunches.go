package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// launchesHead heads the tree of what this session launched.
const launchesHead = "launched here"

// statusAsks, under statusAsksKey in the context, learns whether a status
// printed a question to relay: the hook then leaves the turn to the model.
type (
	statusAsks    struct{ printed bool }
	statusAsksKey struct{}
)

// launchStatus prints the tree of what session started or drove that is
// still live, reporting whether it printed: with nothing live, the bare
// answer stays what it was.
func launchStatus(ctx context.Context, cmd *cli.Command, t *target, sid string) (bool, error) {
	kids := launchKids(ctx, cmd.ErrWriter, t, sid)
	if len(kids) == 0 {
		return false, nil
	}

	src, err := statusSource(ctx, t)
	if err != nil {
		return false, err
	}

	rows := dropRows(src.rowsFrom(ctx, kids), func(r taskRow) bool { return r.Stage == stageDone })
	if len(rows) == 0 {
		return false, nil
	}

	if a, ok := ctx.Value(statusAsksKey{}).(*statusAsks); ok {
		a.printed = slices.ContainsFunc(rows, func(r taskRow) bool { return r.Stage == stageAsking })
	}

	return true, writeTaskRows(cmd, rows, launchesHead)
}

// dropRows leaves out the rows drop picks, in tree order: what hung under a
// dropped row moves up to its level.
func dropRows(rows []taskRow, drop func(taskRow) bool) []taskRow {
	var (
		out []taskRow
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
func launchKids(ctx context.Context, warn io.Writer, t *target, sid string) []childRow {
	launches, err := state.Launches(state.Dir(), sid)
	if err != nil {
		fmt.Fprintf(warn, "f10: reading this session's launches: %v\n", err)
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
	}

	var tokens []token

	for _, l := range launches {
		switch l.Kind {
		case state.LaunchStart:
			tokens = append(tokens, token{text: l.Value, started: true})
		case state.LaunchDrive:
			for _, task := range driveTasks(t.sharedRoot(), l.Value) {
				tokens = append(tokens, token{text: task.ID, after: task.After, queued: task.Queued})
			}
		}
	}

	var (
		kids []childRow
		refs []ref.Ref
	)

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	archive := t.tracker().archive
	bases := map[string]string{} // siblings share a base: resolve it once

	for _, tok := range tokens {
		id, suffix := ref.SplitSuffix(tok.text)

		r, err := t.reference(ctx, id)
		if err != nil {
			continue
		}

		r = r.Head()
		k := childRow{ID: r.ID, suffix: suffix, queued: tok.queued}

		if slices.ContainsFunc(
			kids,
			func(o childRow) bool { return o.ID == k.ID && strings.EqualFold(o.suffix, k.suffix) },
		) {
			continue
		}

		// a finished task reads nothing further: its row would only drop
		branched, err := hasTaskBranch(ctx, main, r.ID)
		if err == nil && (tok.started && !branched || finishedTask(taskInfo{}, archive, r.ID, branched)) {
			continue
		}

		k.After = launchBase(ctx, t, main, r.ID, suffix, tok.after, branched, bases)
		kids = append(kids, k)
		refs = append(refs, r)
	}

	// titles from the ledger, the tracker only on a miss: the typed status
	// answers inside a hook with a short timeout, and a row with no title
	// still reads, so a failed lookup says nothing
	known := t.taskRefs(ctx, refs, false, false, io.Discard)
	for i := range kids {
		kids[i].Title, kids[i].URL = cleanTitle(kids[i].ID, known[kids[i].ID].Title), known[kids[i].ID].URL
	}

	return childTree(kids)
}

// launchBase is the base a launched task waits on, as an id: its branch's
// recorded one, else the one its drive recorded, "" once that base finished.
// seen holds each base as written against its answer.
func launchBase(
	ctx context.Context,
	t *target,
	main, id, suffix, recorded string,
	branched bool,
	seen map[string]string,
) string {
	base := recorded
	if branched {
		if b, err := existingBranchIn(ctx, main, "", id, suffix, "refs/heads/"); err == nil && b.name != "" {
			base = cmp.Or(branchAfter(ctx, main, b.name), base)
		}
	}

	if base == "" {
		return ""
	}

	if out, ok := seen[base]; ok {
		return out
	}

	out := ""
	if r, err := t.reference(ctx, base); err == nil {
		out = r.Head().ID

		has, err := hasTaskBranch(ctx, main, out)
		if err != nil || finishedTask(taskInfo{}, t.tracker().archive, out, has) {
			out = ""
		}
	}

	seen[base] = out

	return out
}

// driveTasks is drive n's tasks as typed, none once the drive is done or
// cancelled, or its record is gone.
func driveTasks(root, n string) []drives.Task {
	num, err := strconv.Atoi(n)
	if err != nil {
		return nil
	}

	rec, err := drives.Read(root, num)
	if err != nil || rec.Cancelled != nil || rec.Done() {
		return nil
	}

	return rec.Tasks
}
