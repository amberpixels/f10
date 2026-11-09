package main

import (
	"cmp"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
)

func verifyFieldIn(fields []facts.Field) facts.Field {
	const name = "Verify"

	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}

	return facts.Field{Name: name, Origin: facts.OriginOf(facts.OriginAbsent)}
}

// verifyLine is the verify the steps run, resolved once: its origin, the
// file it came from, then the commands or the judgment left to the model.
func verifyLine(f facts.Field) string {
	body := strings.TrimSpace(f.Value)

	switch {
	case f.Origin.Declared() && !strings.Contains(body, "\n"):
		return "verify: " + f.Origin.String() + "  " + body + "\n"
	case f.Origin.Declared():
		var b strings.Builder

		b.WriteString("verify: " + f.Origin.String() + "\n")

		for line := range strings.SplitSeq(body, "\n") {
			b.WriteString("  " + line + "\n")
		}

		return b.String()
	case f.Origin.Kind == facts.OriginDetected || f.Origin.Kind == facts.OriginJudgment:
		return "verify: " + f.Origin.String() + " (" + f.Source + ")  " + body + "\n"
	default:
		return "verify: absent  nothing declared or detected: find the commands the repo uses, and suggest declaring them under Verify in project.md\n"
	}
}

// verifyRerun ends every scoped head line: the load runs before implement
// writes anything, so the set it printed is stale by the time verify runs.
const verifyRerun = "; run `f10 config verify` again right before verify runs, since the diff grows"

// verifyDiff is every path the checked-out branch changes against its base:
// commits, staged and unstaged work, and untracked files, so a new file
// counts before it is committed. Local refs only, since the load runs it.
func verifyDiff(ctx context.Context, lay *layout.Layout) (string, []string, error) {
	return branchDiff(ctx, lay.CheckoutRoot, cmp.Or(lay.MainRoot, lay.CheckoutRoot))
}

// branchDiff is verifyDiff for the checkout at dir, main the main checkout.
func branchDiff(ctx context.Context, dir, main string) (string, []string, error) {
	branch := gitx.CurrentBranch(ctx, dir)
	if branch == "" {
		return "", nil, errors.New("HEAD is detached")
	}

	base, err := prBase(ctx, io.Discard, main, branch)
	if err != nil {
		return "", nil, err
	}

	from, err := forkPoint(ctx, dir, "HEAD", base)
	if err != nil {
		return base, nil, err
	}

	// a rename lists both paths, and -z keeps non-ASCII paths unquoted
	tracked, err := gitx.Exec(ctx, dir, "diff", "--name-only", "--no-renames", "-z", from)
	if err != nil {
		return base, nil, err
	}

	untracked, err := gitx.Exec(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return base, nil, err
	}

	var files []string

	for _, out := range []string{tracked, untracked} {
		for f := range strings.SplitSeq(out, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}

	return base, files, nil
}

// scopedVerifyLine is the frontmatter's verify list cut to what the diff
// calls for, with a declared prose heading beneath as the rules. An
// unreadable diff runs every command: a wrong skip costs more than a slow one.
func scopedVerifyLine(eff *facts.Effective, prose facts.Field, base string, changed []string, diffErr error) string {
	var b strings.Builder

	run, left := facts.ScopeVerify(eff.Verify, changed)

	switch {
	case diffErr != nil:
		b.WriteString(
			"verify: " + eff.VerifyOrigin.String() + ", every command: the diff could not be read (" + diffErr.Error() + ")\n",
		)

		for _, c := range eff.Verify {
			b.WriteString("  " + c.Run + "\n")
		}
	case len(run) == 0:
		var patterns []string
		for _, c := range left {
			patterns = append(patterns, c.Paths...)
		}

		b.WriteString("verify: nothing to check  the diff against " + base + " touches none of " +
			strings.Join(patterns, ", ") + verifyRerun + "\n")
	default:
		b.WriteString(
			"verify: " + eff.VerifyOrigin.String() + ", scoped to the diff against " + base + verifyRerun + "\n",
		)

		for _, p := range run {
			why := "every diff"
			if p.Pattern != "" {
				why = p.Pattern + " matched " + p.File
			}

			b.WriteString("  " + p.Run + "  (" + why + ")\n")
		}

		for _, c := range left {
			b.WriteString("  not run: " + c.Run + "  (nothing changed under " + strings.Join(c.Paths, ", ") + ")\n")
		}
	}

	// labelled, so a command the prose still names is not read as one to run
	if prose.Origin.Declared() {
		b.WriteString("  rules from the Verify heading, not commands:\n")

		for line := range strings.SplitSeq(strings.TrimSpace(prose.Value), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}

	return b.String()
}
