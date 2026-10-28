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

	rows := slices.DeleteFunc(src.rowsFrom(ctx, kids), func(r taskRow) bool { return r.Stage == stageDone })
	if len(rows) == 0 {
		return false, nil
	}

	if a, ok := ctx.Value(statusAsksKey{}).(*statusAsks); ok {
		a.printed = slices.ContainsFunc(rows, func(r taskRow) bool { return r.Stage == stageAsking })
	}

	return true, writeTaskRows(cmd, rows, launchesHead)
}

// launchKids is the session's launches as rows to read, in launch order: a
// start's task, a drive's tasks unless it is done or cancelled, then the
// session run's own task when no launch names it. Each task shows once, and
// a finished one not at all.
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
		started bool
	}

	var tokens []token

	for _, l := range launches {
		switch l.Kind {
		case state.LaunchStart:
			tokens = append(tokens, token{text: l.Value, started: true})
		case state.LaunchDrive:
			for _, id := range driveTokens(t.sharedRoot(), l.Value) {
				tokens = append(tokens, token{text: id})
			}
		}
	}

	if len(tokens) > 0 {
		if own := sessionTaskID(sid); own != "" {
			tokens = append(tokens, token{text: own})
		}
	}

	var (
		kids []childRow
		refs []ref.Ref
		seen []childRow
	)

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	for _, tok := range tokens {
		id, suffix := ref.SplitSuffix(tok.text)

		r, err := t.reference(ctx, id)
		if err != nil {
			continue
		}

		r = r.Head()
		k := childRow{ID: r.ID, suffix: suffix}

		if slices.ContainsFunc(
			seen,
			func(o childRow) bool { return o.ID == k.ID && strings.EqualFold(o.suffix, k.suffix) },
		) {
			continue
		}

		seen = append(seen, k)

		// a finished task reads nothing further: its row would only drop
		branched, err := hasTaskBranch(ctx, main, r.ID)
		if err == nil && (tok.started && !branched || finishedTask(taskInfo{}, t.tracker().archive, r.ID, branched)) {
			continue
		}

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

	return kids
}

// driveTokens is drive n's tasks as typed, none once the drive is done or
// cancelled, or its record is gone.
func driveTokens(root, n string) []string {
	num, err := strconv.Atoi(n)
	if err != nil {
		return nil
	}

	rec, err := drives.Read(root, num)
	if err != nil || rec.Cancelled != nil || rec.Done() {
		return nil
	}

	out := make([]string, 0, len(rec.Tasks))
	for _, task := range rec.Tasks {
		out = append(out, task.ID)
	}

	return out
}
