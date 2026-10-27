package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// neverPushed reads the host's answer for the branch's request: no request,
// no copy on origin and no upstream is a branch never pushed, which finish
// merges here. A host that failed to answer stays an error, and so does
// --local, which merges nothing. A pushed branch with no request belongs on
// the host, and an upstream origin lacks may be a request merged there that
// the host no longer lists by branch (glab lists open ones only).
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
	case gitx.Out(ctx, in.main, "config", "--get", "branch."+branch+".merge") != "":
		return false, fmt.Errorf("%w: %s tracks a branch origin no longer has; if its request merged on the host, "+
			"finish it by hand", pullErr, branch)
	}

	return true, nil
}

// landingBase is the commit a branch never pushed lands on: the local
// default branch, or origin's copy when the local one is only behind it,
// so origin's commits are never folded into the merge. Diverged is refused.
func landingBase(ctx context.Context, dir, def string) (string, error) {
	local := gitx.Out(ctx, dir, "rev-parse", "-q", "--verify", "refs/heads/"+def)
	remote := gitx.Out(ctx, dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+def)

	switch {
	case local == "":
		return "", fmt.Errorf("cannot read %s here", def)
	case remote == "" || remote == local || gitx.IsAncestor(ctx, dir, remote, local):
		return local, nil
	case gitx.IsAncestor(ctx, dir, local, remote):
		return remote, nil
	}

	return "", fmt.Errorf("%s and origin/%s have diverged: bring %s up to date with origin first", def, def, def)
}

// landLocal merges a branch never pushed into the local default branch by
// the project's merge method, built in memory on landingBase so no checkout
// is touched, then moves the default branch: a fast-forward in the main
// checkout when it is on it, else the ref alone. A conflict, or a rebase
// that is not a fast-forward, is left to a catchup. A branch already in
// the default branch, as a rerun finds it, lands nothing.
func landLocal(ctx context.Context, in finishInput, branch, def string) (string, []string, error) {
	defRef, head := "refs/heads/"+def, "refs/heads/"+branch
	old := gitx.Out(ctx, in.main, "rev-parse", "-q", "--verify", defRef)

	base, err := landingBase(ctx, in.main, def)
	if err != nil {
		return "", nil, err
	}

	tip := gitx.Out(ctx, in.main, "rev-parse", "-q", "--verify", head)
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
		return base, append(notes, branch+" was already in "+def), nil
	}

	method, note, err := in.host.mergeMethod(ctx, in.method)
	if err != nil {
		return "", nil, err
	}

	notes = appendNote(notes, note)

	var sha string

	switch method {
	case "--rebase":
		if !gitx.IsAncestor(ctx, in.main, base, head) {
			return "", nil, fmt.Errorf("%s is behind %s, and a rebase lands only a fast-forward here: "+
				"run /f10:catchup in its worktree, then finish again", branch, def)
		}

		sha = tip
	case "--squash":
		sha, err = gitx.CommitTree(ctx, in.main, tree, []string{base}, squashMessage(ctx, in, base, head)...)
	default: // --merge, or glab's plain merge
		sha, err = gitx.CommitTree(ctx, in.main, tree, []string{base, tip},
			paragraphs("Merge branch '"+branch+"'", mergeBody(in, "", method))...)
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

	return sha, notes, nil
}

// squashMessage is what a squash merged here says: the task and its title,
// as a PR title carries them, else the branch's first commit subject; then
// the branch's subjects when it has several, and the trailers with traces
// (mergeBody is empty without them).
func squashMessage(ctx context.Context, in finishInput, base, head string) []string {
	subjects := strings.Split(gitx.Out(ctx, in.main, "log", "--reverse", "--format=%s", base+".."+head), "\n")
	subject := subjects[0]

	if in.title != nil {
		if title, err := in.title(ctx); err == nil && title != "" {
			subject = in.task.Label() + ": " + title
		}
	}

	var description string
	if len(subjects) > 1 {
		description = "- " + strings.Join(subjects, "\n- ")
	}

	return paragraphs(subject, cmp.Or(mergeBody(in, description, "--squash"), description))
}

// paragraphs drops the empty parts of a commit message.
func paragraphs(parts ...string) []string {
	var out []string

	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}

	return out
}

// unclosedNote names the tasks a local merge leaves open: no request
// closes them, so the tracker does.
func unclosedNote(in finishInput) string {
	ids := append([]string{in.task.ID}, in.close...)

	return "no PR closes " + strings.Join(ids, ", ") + ": close it on the tracker"
}
