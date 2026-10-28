package main

import (
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

	return true, writeTaskRows(cmd, rows, launchesHead)
}

// launchKids is the session's launches as rows to read, in launch order: a
// start's task, a drive's tasks unless it is done or cancelled, then the
// session run's own task when no launch names it. Each task shows once.
func launchKids(ctx context.Context, warn io.Writer, t *target, sid string) []childRow {
	launches, err := state.Launches(state.Dir(), sid)
	if err != nil {
		fmt.Fprintf(warn, "f10: reading this session's launches: %v\n", err)
	}

	if len(launches) == 0 {
		return nil
	}

	var tokens []string

	for _, l := range launches {
		switch l.Kind {
		case state.LaunchStart:
			tokens = append(tokens, l.Value)
		case state.LaunchDrive:
			tokens = append(tokens, driveTokens(t.sharedRoot(), l.Value)...)
		}
	}

	if len(tokens) > 0 {
		if own := sessionTaskID(sid); own != "" {
			tokens = append(tokens, own)
		}
	}

	var (
		kids []childRow
		refs []ref.Ref
	)

	for _, tok := range tokens {
		id, suffix := ref.SplitSuffix(tok)

		r, err := t.reference(ctx, id)
		if err != nil {
			continue
		}

		r = r.Head()
		if slices.ContainsFunc(kids, func(k childRow) bool {
			return k.ID == r.ID && strings.EqualFold(k.suffix, suffix)
		}) {
			continue
		}

		kids = append(kids, childRow{ID: r.ID, suffix: suffix})
		refs = append(refs, r)
	}

	known := t.taskRefs(ctx, refs, true, false, warn)
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
