package reviews

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/taskid"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// A prSubject is the change a step reads: a branch, where it is checked
// out, the task it belongs to, and its request. Every step that names a PR,
// a task or "this branch" resolves it here, so none parses a branch itself.
type prSubject struct {
	Main   string
	Branch string
	remote bool   // only origin has the branch
	Path   string // the checkout holding it, "" when none does
	Here   bool   // checked out in the checkout the command runs in
	Task   *ref.Ref
	Number int    // the request: one named, else the branch's open one; 0 for none
	Target string // the request's own base branch, which wins over the recorded one

	// LookupErr is a failed host lookup for a branch checked out here, whose
	// diff is local and goes on against the git-only base
	LookupErr error
}

// Base is the branch the subject is diffed against: its open request's
// target, else the one prBase reads from git.
func (s prSubject) Base(ctx context.Context, warn io.Writer) (string, error) {
	if s.Target != "" {
		return s.Target, nil
	}

	return stack.PRBase(ctx, warn, s.Main, s.Branch)
}

// HeadRef is the ref git reads the subject's branch at.
func (s prSubject) HeadRef() string {
	if s.Here {
		return "HEAD"
	}

	if s.remote {
		return "refs/remotes/origin/" + s.Branch
	}

	return "refs/heads/" + s.Branch
}

// PullNumberOf is the request a token names, or 0: a url, #n, !n, or a bare
// number, always a request here; a task is named with its prefix or url.
func PullNumberOf(token string) int {
	number, _ := worktree.PullToken(token)
	if number == "" && taskid.IsNumber(token) {
		number = token
	}

	n, _ := strconv.Atoi(number)

	return n
}

// ResolvePRSubject turns a PR, a task id or nothing (the current branch) into
// a subject, with the branch's open request looked up on the host, whose
// target then wins over the base git records.
func ResolvePRSubject(ctx context.Context, t *target.Target, token string) (prSubject, error) {
	s := prSubject{Main: cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)}
	token = strings.TrimSpace(token)
	withPull := true

	switch n := PullNumberOf(token); {
	case n > 0:
		h, err := hostops.HostOf(t)
		if err != nil {
			return s, err
		}

		p, err := h.PR(ctx, strconv.Itoa(n))
		if err != nil {
			return s, err
		}

		if s.Branch = p.Head; s.Branch == "" {
			return s, fmt.Errorf("%s returned no head branch for #%d", h.Name, n)
		}

		s.Number, s.Target, withPull = n, p.Base, false
		s.remote = stack.RefSHA(ctx, s.Main, "refs/heads/"+s.Branch) == ""
		s.Task = BranchTask(ctx, t, s.Main, s.Branch)
	case token == "":
		if s.Branch = gitx.CurrentBranch(ctx, t.Dir); s.Branch == "" {
			return s, errors.New("HEAD is detached: name a PR or a task")
		}

		if r, err := t.Reference(ctx, ""); err == nil && r.Origin != ref.OriginSession {
			s.Task = &r
		}
	default:
		r, err := t.Reference(ctx, token)
		if err != nil {
			return s, err
		}

		r = r.Head()
		s.Task = &r

		// origin too, unlike TaskCheckout: a branch pushed from another
		// machine is only on origin, and still names the change
		want, _, err := stack.BranchName(ctx, t.Driver(), r, "", nil)
		if err != nil {
			return s, err
		}

		x, err := stack.LoadBranchIndex(ctx, s.Main)
		if err != nil {
			return s, err
		}

		found, err := x.Find(ctx, want, r.ID, "", stack.PolicyLocalOrOrigin)
		if err != nil {
			return s, err
		}

		if found.Name == "" {
			// a finished task's branch is deleted; its last request still
			// holds the change, which explain and judge read after the merge
			p, err := LastPullOf(ctx, t, want)
			if err != nil {
				return s, err
			}

			if p.Number == 0 {
				return s, fmt.Errorf("%s has no branch here or on origin and no PR: start it first", r.ID)
			}

			s.Branch, s.remote, s.Number, s.Target = want, true, p.Number, p.Base

			return s, nil
		}

		s.Branch, s.remote = found.Name, found.Remote
	}

	if !s.remote {
		wts, err := gitx.Worktrees(ctx, s.Main)
		if err != nil {
			return s, err
		}

		s.Path = stack.WorktreeOf(wts, s.Branch)
		s.Here = s.Path != "" && gitx.SamePath(s.Path, t.Layout.CheckoutRoot)
	}

	if withPull {
		p, err := OpenPullOf(ctx, t, s.Branch)

		switch {
		case err != nil && s.Here:
			s.LookupErr = err
		case err != nil:
			return s, err
		}

		s.Number, s.Target = p.Number, p.Base
	}

	return s, nil
}

// BranchTask is the task a PR's head branch belongs to: its link, else the
// id in its name; nil when neither names one the tracker resolves.
func BranchTask(ctx context.Context, t *target.Target, main, branch string) *ref.Ref {
	id, _ := branchmeta.Get(ctx, main, branch, branchmeta.Task)
	if id == "" && t.Effective != nil {
		id = ref.Matcher(t.Effective.IDPrefix)(branch)
	}

	if id == "" {
		return nil
	}

	r, err := t.Reference(ctx, id)
	if err != nil {
		return nil
	}

	r = r.Head()

	return &r
}

// OpenPullOf is the newest open request whose head is branch, or 0: a merged
// or closed one is history, not the change's request. A checkout with no
// host CLI has no requests, and still diffs.
func OpenPullOf(ctx context.Context, t *target.Target, branch string) (forge.PR, error) {
	h, err := hostops.HostOf(t)
	if err != nil {
		return forge.PR{}, nil //nolint:nilerr // no host is no request
	}

	prs, err := h.PullsByHead(ctx, branch)
	if err != nil {
		return forge.PR{}, err
	}

	for _, p := range prs {
		if p.Status == forge.Open {
			return p, nil
		}
	}

	return forge.PR{}, nil
}

// LastPullOf is the newest request of any state whose head is branch, or 0;
// a checkout with no host CLI has none.
func LastPullOf(ctx context.Context, t *target.Target, branch string) (forge.PR, error) {
	h, err := hostops.HostOf(t)
	if err != nil {
		return forge.PR{}, nil //nolint:nilerr // no host is no request
	}

	prs, err := h.PullsByHead(ctx, branch)
	if err != nil || len(prs) == 0 {
		return forge.PR{}, err
	}

	return prs[0], nil
}

// MergedTree is head merged in memory into the newer of base's local and
// origin refs, and that ref. Diffed against the ref it holds only what main
// lacks, which a merge base misses once the branch's earlier PR was squashed.
func MergedTree(ctx context.Context, dir, head, base string) (string, string, error) {
	var into string

	for _, r := range []string{"refs/heads/" + base, "refs/remotes/origin/" + base} {
		switch {
		case stack.RefSHA(ctx, dir, r) == "":
		case into == "":
			into = r
		default:
			if behind, err := gitx.IsAncestor(ctx, dir, into, r); err != nil {
				return "", "", err
			} else if behind {
				into = r
			}
		}
	}

	if into == "" {
		return "", "", fmt.Errorf("%s is not a branch here or on origin", base)
	}

	tree, err := gitx.MergeTree(ctx, dir, into, head)
	if err != nil {
		return "", "", err
	}

	return into, tree, nil
}

// CapturedHead is HEAD with the tracked work in the tree on top, as a commit
// no ref points at: `git stash create` touches neither the stash list nor
// the index, and prints nothing for a clean tree.
func CapturedHead(ctx context.Context, dir string) (string, error) {
	sha, err := gitx.Exec(ctx, dir, "stash", "create")
	if err != nil || sha != "" {
		return sha, err
	}

	return gitx.Exec(ctx, dir, "rev-parse", "HEAD")
}
