package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// The catchup engine, shared by drive and the watcher: git integrates, the
// binary verifies where it can, and the agent is prompted only for what git
// or verify could not settle. One record (drives.Catchup), one progress
// fingerprint and one leased push serve both callers.

// catchupEngine is how a branch is caught up from outside its agent.
type catchupEngine struct {
	// policy holds the catchup keys; dir, main and shipStopped are set per task
	policy catchupInput
	traces bool
	// agentVerifies says why the agent runs verify after a clean integration
	// and clears the marks once green, "" when the binary runs verify
	agentVerifies string
	// integrate, verify and base are the git and the verify the engine
	// runs, replaced by tests that script neither; base reads the base the
	// marks name without a fetch
	integrate func(ctx context.Context, in catchupInput, message func(catchupPlan) []string) (catchupPlan, bool, error)
	verify    func(ctx context.Context, dir, log string) (verifyRun, error)
	base      func(ctx context.Context, in catchupInput) (catchupPlan, error)
}

// newCatchupEngine is the real git and verify; why, when not "", is the
// reason spec cannot run and the agent verifies instead.
func newCatchupEngine(policy catchupInput, spec verifySpec, why string, traces bool, main string) *catchupEngine {
	e := &catchupEngine{
		policy:        policy,
		traces:        traces,
		agentVerifies: why,
		integrate:     integrateBranch,
		base:          resolveCatchup,
	}

	if why == "" {
		e.verify = func(ctx context.Context, dir, log string) (verifyRun, error) {
			return runVerify(ctx, dir, spec.commands(ctx, dir, main), log)
		}
	}

	return e
}

// input is the policy for one worktree.
func (e *catchupEngine) input(main, dir string) catchupInput {
	ci := e.policy
	ci.dir, ci.main = dir, main

	return ci
}

// integrateBranch decides the base and method as f10 pr catchup does and
// integrates; stopped is a conflict left in progress. A failed fetch decides
// on the refs at hand, as the step does.
func integrateBranch(
	ctx context.Context,
	in catchupInput,
	message func(catchupPlan) []string,
) (catchupPlan, bool, error) {
	_ = gitx.Fetch(ctx, in.main)

	p, err := resolveCatchup(ctx, in)
	if err != nil {
		return p, false, err
	}

	if p.found == foundGone {
		for _, key := range []branchmeta.Key{branchmeta.After, branchmeta.AfterBranch} {
			if err := branchmeta.UnsetIfSet(ctx, in.main, p.branch, key); err != nil {
				return p, false, err
			}
		}
	}

	if p.method == methodNone || p.method == methodSkip {
		return p, false, nil
	}

	var msg []string
	if p.method == methodMerge {
		msg = message(p)
	}

	stopped, err := gitx.Integrate(ctx, in.dir, p.method, p.sha, p.upstream, msg...)

	return p, stopped, err
}

// message is a catchup merge's message: git's own subject, then the
// trailers a catchup commit carries for tasks, change naming its PR series.
func (e *catchupEngine) message(tasks []string, change string) func(catchupPlan) []string {
	return func(p catchupPlan) []string {
		msg := []string{"Merge " + p.base + " into " + p.branch}

		if !e.traces || len(tasks) == 0 {
			return msg
		}

		trailers, err := trace.Render(trace.Trailers{Tasks: tasks, Step: "catchup"})
		if err != nil || trailers == "" {
			return msg
		}

		trailers += changeTrailer(tasks[0], change)

		return append(msg, strings.TrimSpace(trailers))
	}
}

// conflictSteering is what a turn settles after git stopped on conflicts.
func (e *catchupEngine) conflictSteering(p catchupPlan, branch, who string, paths []string) string {
	verb, what := "rebased "+branch+" onto", "rebase"
	if p.method == methodMerge {
		verb, what = "merged", "merge"
	}

	return fmt.Sprintf("%s %s %s (%s) and git stopped on conflicts in %s: settle them in place, keeping both sides, "+
		"then conclude the %s%s", who, verb, p.base, gitx.ShortSHA(p.sha), listOrNone(paths), what, e.verifyClause())
}

// cleanSteering is the turn the agent gets after a clean integration when
// the binary cannot run verify.
func (e *catchupEngine) cleanSteering(base string) string {
	return fmt.Sprintf("%s came in cleanly and %s: run verify, fix it by the branch's intent and commit the fix, "+
		"then clear the catchup marks once verify is green", base, e.agentVerifies)
}

// verifyClause tells a turn under an agent-run verify to verify and clear
// the marks itself; under the binary's verify it adds nothing.
func (e *catchupEngine) verifyClause() string {
	if e.agentVerifies == "" {
		return ""
	}

	return ", then run verify and clear the catchup marks once it is green"
}

func listOrNone(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}

	return strings.Join(paths, ", ")
}

// left reads what keeps the branch from being caught up with p (treeState's
// left), and when the agent verifies, whether a mark is still set.
func (e *catchupEngine) left(ctx context.Context, main, dir, branch string, p catchupPlan) (treeState, error) {
	t, err := readTree(ctx, dir)
	if err != nil || t.busy() != "" {
		return t, err
	}

	if err := t.readBase(ctx, branch, p); err != nil || !t.held || len(t.markers) > 0 {
		return t, err
	}

	if e.agentVerifies != "" {
		t.marked, err = markOn(ctx, main, branch)
	}

	return t, err
}

// clearMarks unsets the catchup marks once the base is in and verify is
// green, and records the base the branch now stands on, as the step does.
func clearMarks(ctx context.Context, main, branch string, p catchupPlan) error {
	for _, key := range []branchmeta.Key{branchmeta.Landed, branchmeta.Moved} {
		if err := branchmeta.UnsetIfSet(ctx, main, branch, key); err != nil {
			return fmt.Errorf("clearing the %s mark on %s: %w", key, branch, err)
		}
	}

	if p.found == foundAfter {
		return branchmeta.Set(ctx, main, branch, branchmeta.Tip, p.sha)
	}

	return branchmeta.UnsetIfSet(ctx, main, branch, branchmeta.Tip)
}

// prTarget is what a task's PR targets and the mark that sends a catchup for
// it: origin's copy of the f10-after-branch base, else its local copy, else
// the default branch. sha is "" for a base found nowhere.
type prTarget struct {
	sha, name, value string
	key              branchmeta.Key
}

func prTargetOf(ctx context.Context, main, branch string) (prTarget, error) {
	after, sha, err := recordedBase(ctx, main, branch)
	if err != nil {
		return prTarget{}, err
	}

	if after != "" {
		return prTarget{sha: sha, name: after, key: branchmeta.Moved, value: sha}, nil
	}

	// the default branch resolves as the catchup will read it, so the mark
	// and the catchup never name different commits
	sha, name, err := defaultTarget(ctx, main)
	if err != nil {
		return prTarget{}, err
	}

	def, err := localDefault(ctx, main)

	return prTarget{sha: sha, name: name, key: branchmeta.Landed, value: def}, err
}

// recordedBase is the f10-after-branch base start recorded on branch, and
// its sha, origin's copy first; sha is "" for a base found nowhere.
func recordedBase(ctx context.Context, main, branch string) (string, string, error) {
	after, err := branchmeta.Get(ctx, main, branch, branchmeta.AfterBranch)
	if err != nil || after == "" {
		return "", "", err
	}

	for _, ref := range []string{"refs/remotes/origin/" + after, "refs/heads/" + after} {
		sha, err := gitx.Ref(ctx, main, ref)
		if err != nil || sha != "" {
			return after, sha, err
		}
	}

	return after, "", nil
}

// defaultTarget is the default branch's sha as the catchup step reads it for
// f10-landed: the local branch when it holds origin's, else origin's.
func defaultTarget(ctx context.Context, main string) (string, string, error) {
	def, err := localDefault(ctx, main)
	if err != nil {
		return "", "", err
	}

	return defaultTargetOf(ctx, main, def)
}

func defaultTargetOf(ctx context.Context, main, def string) (string, string, error) {
	return defaultTip(ctx, main, def, divergeOrigin)
}

func markOn(ctx context.Context, main, branch string) (branchmeta.Key, error) {
	for _, key := range []branchmeta.Key{branchmeta.Landed, branchmeta.Moved} {
		v, err := branchmeta.Get(ctx, main, branch, key)
		if err != nil {
			return "", fmt.Errorf("reading the %s mark on %s: %w", key, branch, err)
		}

		if v != "" {
			return key, nil
		}
	}

	return "", nil
}

// branchUnsettled says why a catchup did not bring branch up to date, or "".
// Read from git, since catchup clears the mark only once verify is green.
func branchUnsettled(ctx context.Context, main, dir, branch string) string {
	key, err := markOn(ctx, main, branch)

	switch {
	case err != nil:
		return err.Error()
	case key != "":
		return "the branch is still marked " + string(key)
	}

	return treeBusy(ctx, dir)
}

func treeBusy(ctx context.Context, dir string) string {
	t, err := readTree(ctx, dir)
	if err != nil {
		return err.Error()
	}

	return t.busy()
}

// errOpUnread is a read of a worktree that could not tell whether a merge or
// rebase is in progress there.
var errOpUnread = errors.New("could not tell whether a merge or rebase is in progress")

// A treeState is what git shows of a worktree a catchup acts on: an
// operation in progress and the uncommitted paths, and once readBase ran,
// whether the branch holds the base and the conflict markers left past it.
type treeState struct {
	dir     string
	op      gitx.Operation
	dirty   int
	branch  string
	base    catchupPlan
	held    bool
	markers []string
	marked  branchmeta.Key // a catchup mark still set, read only when the agent verifies
}

// readTree reads the operation in progress and the uncommitted paths in dir.
func readTree(ctx context.Context, dir string) (treeState, error) {
	t := treeState{dir: dir}

	op, err := gitx.InProgress(ctx, dir)
	if err != nil {
		return t, fmt.Errorf("%w in %s: %w", errOpUnread, dir, err)
	}

	t.op = op

	dirty, err := gitx.Status(ctx, dir)
	if err != nil {
		return t, err
	}

	t.dirty = len(dirty)

	return t, nil
}

// readBase reads whether branch holds p, and if it does, the conflict
// markers left in the paths it changes past p.
func (t *treeState) readBase(ctx context.Context, branch string, p catchupPlan) error {
	t.branch, t.base = branch, p

	held, err := gitx.IsAncestor(ctx, t.dir, p.sha, "HEAD")
	if err != nil || !held {
		return err
	}

	t.held = true

	t.markers, err = gitx.Markers(ctx, t.dir, p.sha)

	return err
}

// busy says what keeps a catchup from starting, "" for nothing.
func (t treeState) busy() string {
	switch {
	case t.op.Kind != "":
		return fmt.Sprintf("a %s is still in progress in %s", t.op.Kind, t.dir)
	case t.dirty > 0:
		return fmt.Sprintf("%d uncommitted paths in %s", t.dirty, t.dir)
	}

	return ""
}

// baseLost reports a branch that no longer holds the base readBase read.
func (t treeState) baseLost() bool {
	return t.base.sha != "" && t.op.Kind == "" && t.dirty == 0 && !t.held
}

// left says what keeps the branch from being caught up, "" for nothing: an
// operation in progress (a rebase with its place), uncommitted paths, the
// base not in the branch, a conflict marker, or a mark still set.
func (t treeState) left() string {
	switch {
	case t.op.Kind == "rebase" && t.op.Total > 0:
		return fmt.Sprintf("%s at commit %d of %d", t.busy(), t.op.Done, t.op.Total)
	case t.busy() != "":
		return t.busy()
	case t.baseLost():
		return fmt.Sprintf("%s does not hold %s (%s)", t.branch, t.base.base, gitx.ShortSHA(t.base.sha))
	case len(t.markers) > 0:
		return "conflict markers left in " + strings.Join(t.markers, ", ")
	case t.marked != "":
		return "the branch is still marked " + string(t.marked) + ", so verify did not go green"
	}

	return ""
}

// catchupRecord notes what the catchup about to go out for a mark starts
// from. A read git cannot answer fails: a record made up would halt a
// healthy task, or loop on a doomed one.
func catchupRecord(
	ctx context.Context, main, dir, branch string, key branchmeta.Key, now time.Time,
) (*drives.Catchup, error) {
	target, base, err := catchupTarget(ctx, main, branch, key)
	if err != nil {
		return nil, err
	}

	head, err := gitx.Ref(ctx, dir, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("reading the head of %s: %w", branch, err)
	}

	targetRef, err := catchupTargetRef(ctx, main, branch, key)
	if err != nil {
		return nil, err
	}

	origin, err := originTip(ctx, dir, branch)
	if err != nil {
		return nil, err
	}

	return &drives.Catchup{
		Target: target, Head: head, Origin: origin, Base: base, At: now, TargetRef: targetRef,
	}, nil
}

// catchupConflict is the fingerprint of the conflict branch meets with a
// mark's target now, and the files it conflicts on.
func catchupConflict(ctx context.Context, main, dir, branch string, key branchmeta.Key) (string, string, error) {
	target, _, err := catchupTarget(ctx, main, branch, key)
	if err != nil {
		return "", "", err
	}

	conflicts, err := gitx.Conflicts(ctx, dir, target, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading what %s conflicts on: %w", branch, err)
	}

	return drives.Fingerprint(target, conflictWay(conflicts), ""), conflictPaths(conflicts), nil
}

// conflictWay identifies conflicts by path and the blobs each side brought,
// so the same path with another blob on either side is a new conflict.
func conflictWay(conflicts []gitx.Conflict) string {
	keys := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		keys = append(keys, c.Key())
	}

	return strings.Join(keys, ", ")
}

func conflictPaths(conflicts []gitx.Conflict) string {
	paths := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		paths = append(paths, c.Path)
	}

	return strings.Join(paths, ", ")
}

// originTip is origin's copy of the branch, fetched first when origin has
// it, since a mark set earlier may follow no fetch at all.
func originTip(ctx context.Context, dir, branch string) (string, error) {
	tracking := "refs/remotes/origin/" + branch

	pushed, err := gitx.Ref(ctx, dir, tracking)
	if err != nil || pushed == "" {
		return pushed, err
	}

	if _, err := gitx.Exec(ctx, dir, "fetch", "origin"); err != nil {
		return "", fmt.Errorf("fetching origin before catching %s up: %w", branch, err)
	}

	if pushed, err = gitx.Ref(ctx, dir, tracking); err != nil {
		return "", fmt.Errorf("reading origin/%s: %w", branch, err)
	}

	return pushed, nil
}

// catchupTargetRef is the remote-tracking ref of what a mark's catchup
// brings in; a base recorded nowhere gives "".
func catchupTargetRef(ctx context.Context, main, branch string, key branchmeta.Key) (string, error) {
	if key == branchmeta.Moved {
		after, err := branchmeta.Get(ctx, main, branch, branchmeta.AfterBranch)
		if err != nil || after == "" {
			return "", err
		}

		return "refs/remotes/origin/" + after, nil
	}

	def, err := localDefault(ctx, main)
	if err != nil {
		return "", err
	}

	return "refs/remotes/origin/" + def, nil
}

// catchupTarget resolves a mark's target as the catchup step does; the
// record and the progress check both read it here, so they never name
// different commits.
func catchupTarget(ctx context.Context, main, branch string, key branchmeta.Key) (string, string, error) {
	if key == branchmeta.Moved {
		target, err := branchmeta.Get(ctx, main, branch, key)
		if err != nil {
			return "", "", fmt.Errorf("reading the %s mark on %s: %w", key, branch, err)
		}

		return target, "its base", nil
	}

	return defaultTarget(ctx, main)
}

// targetState is what keeps a catchup from going out for a PR's target:
// a stacked base not on origin yet (its unpushed commits would ride into
// the PR), a PR retargeted by hand (git's base would carry another
// branch's commits into it), or a local tip that is not origin's.
type targetState struct {
	unpushedBase, retargeted, diverged bool
}

// readTarget reads t against the PR's base and the branch's local and
// origin tips; a prBase of "" skips the retarget check.
func readTarget(ctx context.Context, main string, t prTarget, prBase, local, origin string) (targetState, error) {
	var s targetState

	if t.key == branchmeta.Moved {
		on, err := gitx.IsAncestor(ctx, main, t.sha, "refs/remotes/origin/"+t.name)
		if err != nil {
			return s, fmt.Errorf("reading origin/%s: %w", t.name, err)
		}

		s.unpushedBase = !on
	}

	s.retargeted = prBase != "" && prBase != strings.TrimPrefix(t.name, "origin/")
	s.diverged = local != origin

	return s, nil
}

// errBaseNotPushed is a push held back because the base the catchup brought
// in is not on origin yet: the push would carry its commits into the PR.
var errBaseNotPushed = errors.New("its base is not on origin yet")

// originMovedError refuses a force-push that could drop commits pushed to
// the branch while its catchup ran: origin moved, or, unseen, nothing
// recorded what origin held when the catchup went out.
type originMovedError struct {
	branch, base, path string
	unseen             bool
}

func (e originMovedError) Error() string {
	if e.unseen {
		return fmt.Sprintf("%s caught up with %s, not pushed: the drive did not read origin/%s when the catchup "+
			"went out, so a force-push could drop commits pushed since", e.branch, e.base, e.branch)
	}

	return fmt.Sprintf("%s caught up with %s, and origin/%s moved meanwhile: not pushed", e.branch, e.base, e.branch)
}

// pushCatchup is the one push after a clean catchup of a branch origin has:
// plain when it fast-forwards origin, leased against rec's origin tip after
// a rewrite, and never carrying a base origin lacks. A rewrite with no live
// lease is refused as unseen; a push or a refusal spends the lease.
func pushCatchup(ctx context.Context, dir, branch string, rec *drives.Catchup) (string, error) {
	tracking := "refs/remotes/origin/" + branch

	pushed, err := gitx.Ref(ctx, dir, tracking)
	if err != nil || pushed == "" {
		return "", err
	}

	// fetched first, so a push made while the catchup ran shows here
	if _, err := gitx.Exec(ctx, dir, "fetch", "origin"); err != nil {
		return "", err
	}

	if pushed, err = gitx.Ref(ctx, dir, tracking); err != nil {
		return "", err
	}

	head, err := gitx.Ref(ctx, dir, "HEAD")
	if err != nil || head == pushed {
		return "", err
	}

	base := "its base"
	if rec != nil && rec.Base != "" {
		base = rec.Base
	}

	if rec != nil && rec.TargetRef != "" && rec.Target != "" {
		seen, err := gitx.IsAncestor(ctx, dir, rec.Target, rec.TargetRef)
		if err != nil {
			return "", err
		}

		if !seen {
			return "", errBaseNotPushed
		}
	}

	refused := originMovedError{branch: branch, base: base, path: dir}

	fastForward, err := gitx.IsAncestor(ctx, dir, pushed, "HEAD")
	if err != nil {
		return "", err
	}

	// a fast-forward drops nothing, so only a rewrite needs the lease, which
	// a refusal spends: a rerun must not replay a lease origin already broke
	switch {
	case fastForward:
	case rec == nil || rec.Spent:
		refused.unseen = true

		return "", refused
	case pushed != rec.Origin:
		rec.Spent = true

		return "", refused
	}

	lease, how := "", "pushed"
	if !fastForward {
		lease, how = pushed, "pushed with --force-with-lease (the catchup rewrote it)"
	}

	if err := gitx.Push(ctx, dir, branch, lease); err != nil {
		return "", fmt.Errorf("pushing %s, caught up with %s: %w", branch, base, err)
	}

	// origin now holds this push, so the record leases nothing further
	if rec != nil {
		rec.Spent = true
	}

	return how, nil
}
