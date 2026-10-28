package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/state"
)

// branchSize is what a task changed: lines added plus deleted against the
// base the branch forked from, lockfiles, generated code and snapshots
// left out.
type branchSize struct {
	lines, md, files int
	base             string
}

// measureBranch reads the branch's size before it lands: a local merge or
// rebase moves the merge base to the branch's tip, after which the diff
// reads zero. A stacked branch measures against its base branch, else the
// default branch's copy the merge goes into. A PR the host already merged
// with a merge commit reads zero the same way, so it is measured from that
// commit's first parent. False when no base resolves.
func measureBranch(ctx context.Context, dir, branch, def string, local bool) (branchSize, bool) {
	bases := []string{"origin/" + def}
	if local {
		bases = []string{def}
	}

	if after := gitx.Out(ctx, dir, "config", "--get", "branch."+branch+"."+ref.AfterBranchKey); after != "" {
		bases = append([]string{after}, bases...)
	}

	head := "refs/heads/" + branch

	for _, base := range bases {
		out, err := gitx.Exec(ctx, dir, "diff", "--numstat", "--no-renames", base+"..."+head)
		if err != nil {
			continue
		}

		s := numstatSize(out, base)
		if s.files == 0 && !local {
			if merge, _, _ := strings.Cut(gitx.Out(ctx, dir, "rev-list", "--ancestry-path", "--merges",
				"--reverse", head+".."+base), "\n"); merge != "" {
				if out, err := gitx.Exec(
					ctx,
					dir,
					"diff",
					"--numstat",
					"--no-renames",
					merge+"^1..."+head,
				); err == nil {
					s = numstatSize(out, merge+"^1")
				}
			}
		}

		return s, true
	}

	return branchSize{}, false
}

// numstatSize sums `git diff --numstat`; a binary file's `-` counts zero.
func numstatSize(out, base string) branchSize {
	s := branchSize{base: base}

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 || standup.UpkeepPath(fields[2]) {
			continue
		}

		added, _ := strconv.Atoi(fields[0])
		deleted, _ := strconv.Atoi(fields[1])

		s.files++
		s.lines += added + deleted

		if strings.HasSuffix(fields[2], ".md") {
			s.md += added + deleted
		}
	}

	return s
}

// recordSize puts the size in the ledger for `f10 stats`; a test swaps it.
var recordSize = func(ctx context.Context, dir, branch, task string, s branchSize) {
	place := ledger.Where(ctx, dir)

	ledger.Record(ledger.Event{
		At:           time.Now(),
		Project:      place.Project,
		ProjectLocal: place.ProjectLocal,
		Checkout:     place.Checkout,
		Branch:       branch,
		Session:      state.Session(),
		Task:         task,
		Kind:         ledger.TaskSized,
		Source:       ledger.Observed,
		Payload: ledger.NewPayload(ledger.Payload{
			"lines": s.lines, "md_lines": s.md, "files": s.files, "base": s.base,
		}),
	})
}
