package reviews

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/trace"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A Sweep is a coherence sweep's range, read before its round so the
// findings never land under a task the sweep itself reviews.
type Sweep struct {
	Branch  string           // the branch a sweep of this range runs on
	Decline findings.Decline // empty when the checked-out branch may hold it
	Detail  string           // the decline's reason, naming the tasks
	Here    bool             // the sweep's branch is the one checked out
}

// sweepBranch names the range by the ref as typed, or by its commit when the
// ref (`HEAD~20`, `main^`) makes no valid branch name.
func sweepBranch(ctx context.Context, dir, since, start, head string) string {
	name := "sweep/" + strings.ReplaceAll(since, "/", "-") + "-to-" + head
	if _, err := gitx.Exec(ctx, dir, "check-ref-format", "--branch", name); err != nil {
		return "sweep/" + start + "-to-" + head
	}

	return name
}

// CheckSweep refuses the default branch, where resolve would commit to it,
// and a branch whose task is swept or is the parent of a swept task, where
// the sweep's findings, lore and PR would be filed under that task.
func CheckSweep(ctx context.Context, t *target.Target, since string, warn io.Writer) (Sweep, error) {
	head, err := gitx.Exec(ctx, t.Dir, "rev-parse", "--short=7", "HEAD")
	if err != nil {
		return Sweep{}, fmt.Errorf("reading HEAD: %w", err)
	}

	start, err := gitx.Exec(ctx, t.Dir, "rev-parse", "--verify", "--quiet", "--short=7", since+"^{commit}")
	if err != nil {
		return Sweep{}, fmt.Errorf("%s names no commit here", since)
	}

	branch := gitx.CurrentBranch(ctx, t.Dir)
	sw := Sweep{Branch: sweepBranch(ctx, t.Dir, since, start, head)}
	sw.Here = branch == sw.Branch

	// unknown is not "elsewhere": a sweep on main would commit its fixes there
	def, err := stack.LocalDefault(ctx, t.Dir)
	if err != nil {
		return sw, err
	}

	if branch == def {
		sw.Decline = findings.OnDefault

		return sw, nil
	}

	r, err := branchTask(ctx, t)
	if errors.Is(err, ref.ErrNoReference) {
		return sw, nil
	}

	if err != nil {
		return sw, err
	}

	swept, err := sweptTasks(ctx, t.Dir, t.Effective.IDPrefix, since)
	if err != nil {
		return sw, err
	}

	for _, id := range r.Members() {
		if slices.Contains(swept, id) {
			sw.Decline, sw.Detail = findings.Swept, id+" is one of the swept tasks"

			return sw, nil
		}
	}

	for _, m := range r.MemberRefs() {
		kids, err := sweptChildren(ctx, t, m, swept)
		if err != nil {
			ui.Warnf(warn, "cannot read the branch task's subtasks (%v), so the parent check is skipped", err)

			continue
		}

		if len(kids) > 0 {
			sw.Decline = findings.Swept
			sw.Detail = m.ID + " is the parent of swept " + strings.Join(kids, ", ")

			return sw, nil
		}
	}

	return sw, nil
}

// branchTask is the task the branch names, never the one this session
// recorded: a sweep branch names none, and must stay its own.
func branchTask(ctx context.Context, t *target.Target) (ref.Ref, error) {
	return ref.Lookup{Prefix: t.Effective.IDPrefix, Dir: t.Dir, NoSession: true}.Find(ctx, "")
}

// sweptTasks is every task the range's commits name: their Task: trailers,
// else the id in the subject, the same map the coherence lens builds.
func sweptTasks(ctx context.Context, dir, prefix, since string) ([]string, error) {
	out, err := gitx.Exec(ctx, dir, "log", since+"..HEAD", "--format=%B%x1e")
	if err != nil {
		return nil, fmt.Errorf("reading %s..HEAD: %w", since, err)
	}

	match := ref.Matcher(prefix)

	var ids []string

	for msg := range strings.SplitSeq(out, "\x1e") {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}

		tasks := trace.Parse(msg).Tasks
		if len(tasks) == 0 {
			subject, _, _ := strings.Cut(msg, "\n")
			if id := match(subject); id != "" {
				tasks = []string{id}
			}
		}

		for _, id := range tasks {
			if id = strings.ToUpper(id); !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}

	return ids, nil
}

// sweptChildren is the task's subtasks, closed ones included, that the range
// swept. A tracker with no parent link has none to find.
func sweptChildren(ctx context.Context, t *target.Target, task ref.Ref, swept []string) ([]string, error) {
	raw, err := stack.RawChildrenFor(ctx, t, task)
	if errors.Is(err, hostops.ErrNoParentLink) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var kids []string

	for _, c := range raw {
		for _, id := range swept {
			// a host's sub-issues come back as bare numbers
			if strings.EqualFold(c.ID, id) || c.ID == id[strings.LastIndexByte(id, '-')+1:] {
				kids = append(kids, id)
			}
		}
	}

	return kids, nil
}
