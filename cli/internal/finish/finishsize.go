package finish

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/state"
)

// BranchSize is what a task changed, lockfiles, generated code and
// snapshots left out.
type BranchSize struct {
	Lines, Md, Files int
	Base             string
}

// measureBranch reads the size before the branch lands, since landing moves
// the merge base to the tip and the diff reads zero; a PR the host already
// merged with a merge commit measures from that commit's first parent.
func measureBranch(ctx context.Context, dir, branch, def string, unpushed bool) (BranchSize, bool) {
	bases := []string{"origin/" + def}
	if unpushed {
		bases = []string{def}
	}

	if after := branchmeta.Out(ctx, dir, branch, branchmeta.AfterBranch); after != "" {
		bases = append([]string{after}, bases...)
	}

	head := "refs/heads/" + branch

	for _, base := range bases {
		out, err := gitx.Exec(ctx, dir, "diff", "--numstat", "--no-renames", base+"..."+head)
		if err != nil {
			continue
		}

		s := numstatSize(out, base)
		if s.Files == 0 && !unpushed {
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

	return BranchSize{}, false
}

// numstatSize sums `git diff --numstat`; a binary file's `-` counts zero.
func numstatSize(out, base string) BranchSize {
	s := BranchSize{Base: base}

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 || standup.UpkeepPath(fields[2]) {
			continue
		}

		added, _ := strconv.Atoi(fields[0])
		deleted, _ := strconv.Atoi(fields[1])

		s.Files++
		s.Lines += added + deleted

		if strings.HasSuffix(fields[2], ".md") {
			s.Md += added + deleted
		}
	}

	return s
}

// RecordSize puts the size in the ledger for `f10 stats`; a test swaps it.
var RecordSize = func(ctx context.Context, dir, branch, task string, s BranchSize) {
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
			"lines": s.Lines, "md_lines": s.Md, "files": s.Files, "base": s.Base,
		}),
	})
}

// sizeRecorder measures the branch while it still differs from its base and
// returns what records it, run only once the merge has landed.
func sizeRecorder(ctx context.Context, in FinishInput, branch, def string, unpushed bool) func() {
	size, sized := measureBranch(ctx, in.Main, branch, def, unpushed)
	if !sized || size.Files == 0 {
		return func() {}
	}

	return func() { RecordSize(ctx, in.Main, branch, in.Task.ID, size) }
}
