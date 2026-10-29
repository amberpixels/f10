package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A prSubject is the change a step reads: a branch, where it is checked
// out, the task it belongs to, and its request. Every step that names a PR,
// a task or "this branch" resolves it here, so none parses a branch itself.
type prSubject struct {
	main   string
	branch string
	remote bool   // only origin has the branch
	path   string // the checkout holding it, "" when none does
	here   bool   // checked out in the checkout the command runs in
	task   *ref.Ref
	number int    // the request: one named, else the branch's open one; 0 for none
	target string // the request's own base branch, which wins over the recorded one

	// lookupErr is a failed host lookup for a branch checked out here, whose
	// diff is local and goes on against the git-only base
	lookupErr error
}

// pullNumberOf is the request a token names, or 0: a url, #n, !n, or a bare
// number, always a request here; a task is named with its prefix or url.
func pullNumberOf(token string) int {
	number, _ := pullToken(token)
	if number == "" && digits(token) {
		number = token
	}

	n, _ := strconv.Atoi(number)

	return n
}

// resolvePRSubject turns a PR, a task id or nothing (the current branch) into
// a subject. withPull also looks the branch's open request up on the host;
// base never asks for it, so it reads git alone.
func resolvePRSubject(ctx context.Context, t *target, token string, withPull bool) (prSubject, error) {
	s := prSubject{main: cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)}
	token = strings.TrimSpace(token)

	switch n := pullNumberOf(token); {
	case n > 0:
		h, err := t.host()
		if err != nil {
			return s, err
		}

		p, err := h.pullNumber(ctx, strconv.Itoa(n))
		if err != nil {
			return s, err
		}

		if s.branch = p.head(); s.branch == "" {
			return s, fmt.Errorf("%s returned no head branch for #%d", h.Name, n)
		}

		s.number, s.target, withPull = n, p.base(), false
		s.remote = refSHA(ctx, s.main, "refs/heads/"+s.branch) == ""
		s.task = branchTask(ctx, t, s.main, s.branch)
	case token == "":
		if s.branch = gitx.CurrentBranch(ctx, t.dir); s.branch == "" {
			return s, errors.New("HEAD is detached: name a PR or a task")
		}

		if r, err := t.reference(ctx, ""); err == nil && r.Origin != ref.OriginSession {
			s.task = &r
		}
	default:
		r, err := t.reference(ctx, token)
		if err != nil {
			return s, err
		}

		r = r.Head()
		s.task = &r

		// existingBranch, not taskCheckout: a branch pushed from another
		// machine is only on origin, and still names the change
		want, _, err := branchName(ctx, t.driver(), r, "", nil)
		if err != nil {
			return s, err
		}

		found, err := existingBranch(ctx, s.main, want, r.ID, "")
		if err != nil {
			return s, err
		}

		if found.name == "" {
			return s, fmt.Errorf("%s has no branch here or on origin: start it first", r.ID)
		}

		s.branch, s.remote = found.name, found.remote
	}

	if !s.remote {
		wts, err := gitx.Worktrees(ctx, s.main)
		if err != nil {
			return s, err
		}

		s.path = worktreeOf(wts, s.branch)
		s.here = s.path != "" && gitx.SamePath(s.path, t.lay.CheckoutRoot)
	}

	if withPull {
		p, err := openPullOf(ctx, t, s.branch)

		switch {
		case err != nil && s.here:
			s.lookupErr = err
		case err != nil:
			return s, err
		}

		s.number, s.target = p.Number, p.Base
	}

	return s, nil
}

// branchTask is the task a PR's head branch belongs to: its link, else the
// id in its name; nil when neither names one the tracker resolves.
func branchTask(ctx context.Context, t *target, main, branch string) *ref.Ref {
	id, _ := gitx.Config(ctx, main, "branch."+branch+"."+ref.TaskKey)
	if id == "" && t.eff != nil {
		id = ref.Matcher(t.eff.IDPrefix)(branch)
	}

	if id == "" {
		return nil
	}

	r, err := t.reference(ctx, id)
	if err != nil {
		return nil
	}

	r = r.Head()

	return &r
}

// base is the branch the subject is diffed against: its open request's
// target, else the one prBase reads from git.
func (s prSubject) base(ctx context.Context, warn io.Writer) (string, error) {
	if s.target != "" {
		return s.target, nil
	}

	return prBase(ctx, warn, s.main, s.branch)
}

// openPullOf is the newest open request whose head is branch, or 0: a merged
// or closed one is history, not the change's request. A checkout with no
// host CLI has no requests, and still diffs.
func openPullOf(ctx context.Context, t *target, branch string) (forge.PR, error) {
	h, err := t.host()
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

// prBase is the branch the subject's request targets or would target. A
// recorded base found nowhere was finished and deleted, so its code is on
// the default branch; that is said on warn. prTargetOf keeps "found nowhere"
// as its own answer, which drive and the watcher wait on.
func prBase(ctx context.Context, warn io.Writer, main, branch string) (string, error) {
	after, sha, err := recordedBase(ctx, main, branch)
	if err != nil {
		return "", err
	}

	if after != "" && sha != "" {
		return after, nil
	}

	def, err := localDefault(ctx, main)
	if err != nil {
		return "", err
	}

	if after != "" {
		fmt.Fprintf(warn, "f10: the recorded base %s is gone locally and on origin, so the base is %s\n", after, def)
	}

	return def, nil
}

// forkPoint is where the subject's branch left its base: the newer of the
// merge bases with the local and origin copies, so neither unpushed commits
// of the base nor origin moving ahead read as this change.
func forkPoint(ctx context.Context, dir, head, base string) (string, error) {
	var points []string

	for _, r := range []string{"refs/heads/" + base, "refs/remotes/origin/" + base} {
		if refSHA(ctx, dir, r) == "" {
			continue
		}

		mb, err := gitx.MergeBase(ctx, dir, head, r)
		if err != nil {
			return "", err
		}

		if mb != "" {
			points = append(points, mb)
		}
	}

	switch len(points) {
	case 0:
		return "", fmt.Errorf("%s shares no history with %s, locally or on origin", head, base)
	case 1:
		return points[0], nil
	}

	older, err := gitx.IsAncestor(ctx, dir, points[0], points[1])
	if err != nil {
		return "", err
	}

	if older {
		return points[1], nil
	}

	return points[0], nil
}

// headRef is the ref git reads the subject's branch at.
func (s prSubject) headRef() string {
	if s.here {
		return "HEAD"
	}

	if s.remote {
		return "refs/remotes/origin/" + s.branch
	}

	return "refs/heads/" + s.branch
}
