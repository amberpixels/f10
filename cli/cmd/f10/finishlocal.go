package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// neverPushed proves a branch has no request, no copy on origin and no
// upstream before finish merges it here: an upstream origin lacks may be a
// request merged there that the host no longer lists (glab lists open only).
func neverPushed(ctx context.Context, in finishInput, branch string, pullErr error) (bool, error) {
	if pullErr == nil {
		return false, nil
	}

	if !noSuchPull(pullErr) || in.local {
		return false, pullErr
	}

	pushed, err := gitx.RemoteBranchExists(ctx, in.main, branch)

	switch {
	case err != nil:
		return false, err
	case pushed:
		return false, fmt.Errorf("%w: origin has %s with no PR: open one, or delete origin's copy to merge it here",
			pullErr, branch)
	}

	upstream, err := gitx.Config(ctx, in.main, "branch."+branch+".merge")

	switch {
	case err != nil:
		return false, err
	case upstream != "":
		return false, fmt.Errorf("%w: %s tracks a branch origin no longer has; if its request merged on the host, "+
			"finish it by hand", pullErr, branch)
	}

	return true, nil
}

// landingBase is the local default branch, or origin's copy when the local
// one is only behind it, so origin's commits are never folded into the merge.
func landingBase(ctx context.Context, dir, def string) (string, error) {
	local, err := gitx.Ref(ctx, dir, "refs/heads/"+def)
	if err != nil {
		return "", err
	}

	remote, err := gitx.Ref(ctx, dir, "refs/remotes/origin/"+def)
	if err != nil {
		return "", err
	}

	switch {
	case local == "":
		return "", fmt.Errorf("cannot read %s here", def)
	case remote == "" || remote == local:
		return local, nil
	}

	behind, err := gitx.IsAncestor(ctx, dir, remote, local)
	if err != nil || behind {
		return local, err
	}

	ahead, err := gitx.IsAncestor(ctx, dir, local, remote)
	if err != nil || ahead {
		return remote, err
	}

	return "", fmt.Errorf("%s and origin/%s have diverged: bring %s up to date with origin first", def, def, def)
}

// landLocal builds the merge in memory on landingBase so no checkout is
// touched, then moves the default branch. A conflict, or a rebase that is
// not a fast-forward, is left to a catchup; a rerun lands nothing. The last
// note says whether the commit closes the task or the tracker must.
func landLocal(ctx context.Context, in finishInput, branch, def string) (string, []string, error) {
	defRef, head := "refs/heads/"+def, "refs/heads/"+branch
	// old is the compare-and-swap value the ref moves from: an unread one
	// would move it unchecked
	old, err := gitx.Ref(ctx, in.main, defRef)
	if err != nil {
		return "", nil, err
	}

	base, err := landingBase(ctx, in.main, def)
	if err != nil {
		return "", nil, err
	}

	tip, err := gitx.Ref(ctx, in.main, head)
	if err != nil {
		return "", nil, err
	}

	if tip == "" {
		return "", nil, fmt.Errorf("cannot read %s here", branch)
	}

	tree, err := gitx.MergeTree(ctx, in.main, base, head)
	if err != nil {
		return "", nil, fmt.Errorf("%s conflicts with %s: run /f10:catchup in its worktree, then finish again",
			branch, def)
	}

	notes := []string{fmt.Sprintf("%s was never pushed: merged into %s here, nothing pushed", branch, def)}

	if tree == gitx.Out(ctx, in.main, "rev-parse", base+"^{tree}") {
		// a rerun after a landing: the unpushed commit may already close the task
		note := unclosedNote(in)
		if closing := closingLines(in); closing != "" && strings.Contains(
			gitx.Out(ctx, in.main, "log", "--format=%B", "refs/remotes/origin/"+def+"..refs/heads/"+def), closing) {
			note = closesOnPush(in, def)
		}

		return base, append(notes, branch+" was already in "+def, note), nil
	}

	method, note, err := in.host.mergeMethod(ctx, in.method)
	if err != nil {
		return "", nil, err
	}

	notes = appendNote(notes, note)

	var sha string

	closing := closingLines(in)

	switch method {
	case "--rebase":
		ff, err := gitx.IsAncestor(ctx, in.main, base, head)
		if err != nil {
			return "", nil, err
		}

		if !ff {
			return "", nil, fmt.Errorf("%s is behind %s, and a rebase lands only a fast-forward here: "+
				"run /f10:catchup in its worktree, then finish again", branch, def)
		}

		sha, closing = tip, ""
	case "--squash":
		sha, err = gitx.CommitTree(ctx, in.main, tree, []string{base}, squashMessage(ctx, in, base, head)...)
	default: // --merge, or glab's plain merge
		sha, err = gitx.CommitTree(ctx, in.main, tree, []string{base, tip},
			paragraphs("Merge branch '"+branch+"'", cmp.Or(mergeBody(in, closing, method), closing))...)
	}

	if err != nil {
		return "", nil, fmt.Errorf("merging %s into %s: %w", branch, def, err)
	}

	if gitx.CurrentBranch(ctx, in.main) == def {
		_, err = gitx.Exec(ctx, in.main, "merge", "--ff-only", sha)
	} else {
		err = gitx.UpdateRef(ctx, in.main, defRef, sha, old)
	}

	if err != nil {
		return "", nil, fmt.Errorf("moving %s to the merge %s: %w", def, sha, err)
	}

	if closing == "" {
		return sha, append(notes, unclosedNote(in)), nil
	}

	return sha, append(notes, closesOnPush(in, def)), nil
}

func closesOnPush(in finishInput, def string) string {
	return strings.Join(closedIDs(in), ", ") + " closes when " + def + " reaches origin"
}

// alreadyLanded reports every change on head already in base, the case
// landLocal lands nothing for; a conflict is not landed.
func alreadyLanded(ctx context.Context, dir, base, head string) bool {
	tree, err := gitx.MergeTree(ctx, dir, base, head)

	return err == nil && tree == gitx.Out(ctx, dir, "rev-parse", base+"^{tree}")
}

// squashMessage shapes the commit as a PR squash would: the task and title
// as subject, the branch's subjects as body, and the trailers with traces on.
func squashMessage(ctx context.Context, in finishInput, base, head string) []string {
	subjects := strings.Split(gitx.Out(ctx, in.main, "log", "--reverse", "--format=%s", base+".."+head), "\n")
	subject := subjects[0]

	if in.title != nil {
		if title, err := in.title(ctx); err == nil && title != "" {
			subject = in.task.Label() + ": " + title
		}
	}

	var list string
	if len(subjects) > 1 {
		list = "- " + strings.Join(subjects, "\n- ")
	}

	description := strings.Join(paragraphs(list, closingLines(in)), "\n\n")

	return paragraphs(subject, cmp.Or(mergeBody(in, description, "--squash"), description))
}

func paragraphs(parts ...string) []string {
	var out []string

	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}

	return out
}

// unclosedNote: no request closes a locally merged task, so the tracker must.
func unclosedNote(in finishInput) string {
	return "no PR closes " + strings.Join(closedIDs(in), ", ") + ": close it on the tracker"
}

func closedIDs(in finishInput) []string {
	return append([]string{in.task.ID}, in.close...)
}

// closingLines closes the lead and the riders --close names once the commit
// reaches origin's default branch; written whatever traces says, since it is
// the task's own reference. Other trackers, a driver's included, are not
// closed by a commit, as on the PR path.
func closingLines(in finishInput) string {
	if !in.hostIssues || in.driver != nil {
		return ""
	}

	var lines []string

	for _, m := range in.task.MemberRefs() {
		if m.ID == in.task.ID || slices.Contains(in.close, m.ID) {
			lines = append(lines, "Closes #"+m.Number)
		}
	}

	return strings.Join(lines, "\n")
}
