package stack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// CatchupEngine is how a branch is caught up from outside its agent.
type CatchupEngine struct {
	// Policy holds the catchup keys; dir, main and ShipStopped are set per task
	Policy CatchupInput
	traces bool
	// AgentVerifies says why the agent runs verify after a clean integration
	// and clears the marks once green, "" when the binary runs verify
	AgentVerifies string
	// Integrate, verify and base are the git and the verify the engine
	// runs, replaced by tests that script neither; base reads the base the
	// marks name without a fetch
	Integrate func(ctx context.Context, in CatchupInput, message func(CatchupPlan) []string) (CatchupPlan, bool, error)
	Verify    func(ctx context.Context, dir, log string) (VerifyRun, error)
	Base      func(ctx context.Context, in CatchupInput) (CatchupPlan, error)
}

// NewCatchupEngine is the real git and verify; why, when not "", is the
// reason spec cannot run and the agent verifies instead.
func NewCatchupEngine(policy CatchupInput, spec VerifySpec, why string, traces bool, main string) *CatchupEngine {
	e := &CatchupEngine{
		Policy:        policy,
		traces:        traces,
		AgentVerifies: why,
		Integrate:     IntegrateBranch,
		Base:          ResolveCatchup,
	}

	if why == "" {
		e.Verify = func(ctx context.Context, dir, log string) (VerifyRun, error) {
			return RunVerify(ctx, dir, spec.commands(ctx, dir, main), log)
		}
	}

	return e
}

// Input is the policy for one worktree.
func (e *CatchupEngine) Input(main, dir string) CatchupInput {
	ci := e.Policy
	ci.Dir, ci.Main = dir, main

	return ci
}

// Message is a catchup merge's message: git's own subject, then the
// trailers a catchup commit carries for tasks, change naming its PR series.
func (e *CatchupEngine) Message(tasks []string, change string) func(CatchupPlan) []string {
	return func(p CatchupPlan) []string {
		msg := []string{"Merge " + p.Base + " into " + p.Branch}

		if !e.traces || len(tasks) == 0 {
			return msg
		}

		trailers, err := trace.Render(trace.Trailers{Tasks: tasks, Step: "catchup"})
		if err != nil || trailers == "" {
			return msg
		}

		trailers += ChangeTrailer(tasks[0], change)

		return append(msg, strings.TrimSpace(trailers))
	}
}

// ConflictSteering is what a turn settles after git stopped on conflicts.
func (e *CatchupEngine) ConflictSteering(p CatchupPlan, branch, who string, paths []string) string {
	verb, what := "rebased "+branch+" onto", "rebase"
	if p.Method == MethodMerge {
		verb, what = "merged", "merge"
	}

	return fmt.Sprintf("%s %s %s (%s) and git stopped on conflicts in %s: settle them in place, keeping both sides, "+
		"then conclude the %s%s", who, verb, p.Base, gitx.ShortSHA(p.SHA), ListOrNone(paths), what, e.VerifyClause())
}

// CleanSteering is the turn the agent gets after a clean integration when
// the binary cannot run verify.
func (e *CatchupEngine) CleanSteering(base string) string {
	return fmt.Sprintf("%s came in cleanly and %s: run verify, fix it by the branch's intent and commit the fix, "+
		"then clear the catchup marks once verify is green", base, e.AgentVerifies)
}

// VerifyClause tells a turn under an agent-run verify to verify and clear
// the marks itself; under the binary's verify it adds nothing.
func (e *CatchupEngine) VerifyClause() string {
	if e.AgentVerifies == "" {
		return ""
	}

	return ", then run verify and clear the catchup marks once it is green"
}

// Left reads what keeps the branch from being caught up with p (TreeState's
// left), and when the agent verifies, whether a mark is still set.
func (e *CatchupEngine) Left(ctx context.Context, main, dir, branch string, p CatchupPlan) (TreeState, error) {
	t, err := ReadTree(ctx, dir)
	if err != nil || t.Busy() != "" {
		return t, err
	}

	if t, err = t.WithBase(ctx, branch, p); err != nil || !t.held || len(t.markers) > 0 {
		return t, err
	}

	if e.AgentVerifies != "" {
		t.marked, err = MarkOn(ctx, main, branch)
	}

	return t, err
}

// IntegrateBranch decides the base and method as f10 pr catchup does and
// integrates; stopped is a conflict left in progress.
func IntegrateBranch(
	ctx context.Context,
	in CatchupInput,
	message func(CatchupPlan) []string,
) (CatchupPlan, bool, error) {
	return IntegrateWith(ResolveCatchup)(ctx, in, message)
}

// IntegrateWith is IntegrateBranch deciding the base with resolve, which a
// test scripts. A failed fetch decides on the refs at hand, as the step does:
// an integration needs some base, where originTip needs origin's real tip.
func IntegrateWith(
	resolve func(context.Context, CatchupInput) (CatchupPlan, error),
) func(context.Context, CatchupInput, func(CatchupPlan) []string) (CatchupPlan, bool, error) {
	return func(ctx context.Context, in CatchupInput, message func(CatchupPlan) []string) (CatchupPlan, bool, error) {
		_ = in.Fetched.Fetch(ctx, in.Main)

		p, err := resolve(ctx, in)
		if err != nil {
			return p, false, err
		}

		if p.Found == FoundGone {
			for _, key := range []branchmeta.Key{branchmeta.After, branchmeta.AfterBranch} {
				if err := branchmeta.UnsetIfSet(ctx, in.Main, p.Branch, key); err != nil {
					return p, false, err
				}
			}
		}

		if p.Method == MethodNone || p.Method == MethodSkip {
			return p, false, nil
		}

		var msg []string
		if p.Method == MethodMerge {
			msg = message(p)
		}

		stopped, err := gitx.Integrate(ctx, in.Dir, p.Method, p.SHA, p.Upstream, msg...)

		return p, stopped, err
	}
}

func ListOrNone(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}

	return strings.Join(paths, ", ")
}

// PRTarget is what a task's PR targets and the mark that sends a catchup for
// it: origin's copy of the f10-after-branch base, else its local copy, else
// the default branch. sha is "" for a base found nowhere.
type PRTarget struct {
	SHA, Name, Value string
	Key              branchmeta.Key
}

func PRTargetOf(ctx context.Context, main, branch string) (PRTarget, error) {
	after, sha, err := RecordedBase(ctx, main, branch)
	if err != nil {
		return PRTarget{}, err
	}

	if after != "" {
		return PRTarget{SHA: sha, Name: after, Key: branchmeta.Moved, Value: sha}, nil
	}

	// the default branch resolves as the catchup will read it, so the mark
	// and the catchup never name different commits
	sha, name, err := defaultTarget(ctx, main)
	if err != nil {
		return PRTarget{}, err
	}

	def, err := LocalDefault(ctx, main)

	return PRTarget{SHA: sha, Name: name, Key: branchmeta.Landed, Value: def}, err
}

// RecordedBase is the f10-after-branch base start recorded on branch, and
// its sha, origin's copy first; sha is "" for a base found nowhere.
func RecordedBase(ctx context.Context, main, branch string) (string, string, error) {
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
	def, err := LocalDefault(ctx, main)
	if err != nil {
		return "", "", err
	}

	return DefaultTargetOf(ctx, main, def)
}

func DefaultTargetOf(ctx context.Context, main, def string) (string, string, error) {
	return DefaultTip(ctx, main, def, divergeOrigin)
}

func MarkOn(ctx context.Context, main, branch string) (branchmeta.Key, error) {
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

// BranchUnsettled says why a catchup did not bring branch up to date, or "".
// Read from git, since catchup clears the mark only once verify is green.
func BranchUnsettled(ctx context.Context, main, dir, branch string) string {
	key, err := MarkOn(ctx, main, branch)

	switch {
	case err != nil:
		return err.Error()
	case key != "":
		return "the branch is still marked " + string(key)
	}

	return TreeBusy(ctx, dir)
}

func TreeBusy(ctx context.Context, dir string) string {
	t, err := ReadTree(ctx, dir)
	if err != nil {
		return err.Error()
	}

	return t.Busy()
}

// ErrOpUnread is a read of a worktree that could not tell whether a merge or
// rebase is in progress there.
var ErrOpUnread = errors.New("could not tell whether a merge or rebase is in progress")

// A TreeState is what git shows of a worktree a catchup acts on: an
// operation in progress and the uncommitted paths, and once WithBase ran,
// whether the branch holds the base and the conflict markers left past it.
type TreeState struct {
	dir     string
	Op      gitx.Operation
	Dirty   int
	branch  string
	base    CatchupPlan
	held    bool
	markers []string
	marked  branchmeta.Key // a catchup mark still set, read only when the agent verifies
}

// WithBase is t with whether branch holds p read, and if it does, the
// conflict markers left in the paths it changes past p.
func (t TreeState) WithBase(ctx context.Context, branch string, p CatchupPlan) (TreeState, error) {
	t.branch, t.base = branch, p

	held, err := gitx.IsAncestor(ctx, t.dir, p.SHA, "HEAD")
	if err != nil || !held {
		return t, err
	}

	t.held = true
	t.markers, err = gitx.Markers(ctx, t.dir, p.SHA)

	return t, err
}

// Busy says what keeps a catchup from starting, "" for nothing.
func (t TreeState) Busy() string {
	switch {
	case t.Op.Kind != "":
		return fmt.Sprintf("a %s is still in progress in %s", t.Op.Kind, t.dir)
	case t.Dirty > 0:
		return fmt.Sprintf("%d uncommitted paths in %s", t.Dirty, t.dir)
	}

	return ""
}

// BaseLost reports a branch that no longer holds the base WithBase read.
func (t TreeState) BaseLost() bool {
	return t.base.SHA != "" && t.Op.Kind == "" && t.Dirty == 0 && !t.held
}

// Left says what keeps the branch from being caught up, "" for nothing: an
// operation in progress (a rebase with its place), uncommitted paths, the
// base not in the branch, a conflict marker, or a mark still set.
func (t TreeState) Left() string {
	switch {
	case t.Op.Kind == "rebase" && t.Op.Total > 0:
		return fmt.Sprintf("%s at commit %d of %d", t.Busy(), t.Op.Done, t.Op.Total)
	case t.Busy() != "":
		return t.Busy()
	case t.BaseLost():
		return fmt.Sprintf("%s does not hold %s (%s)", t.branch, t.base.Base, gitx.ShortSHA(t.base.SHA))
	case len(t.markers) > 0:
		return "conflict markers left in " + strings.Join(t.markers, ", ")
	case t.marked != "":
		return "the branch is still marked " + string(t.marked) + ", so verify did not go green"
	}

	return ""
}

// ReadTree reads the operation in progress and the uncommitted paths in dir.
func ReadTree(ctx context.Context, dir string) (TreeState, error) {
	t := TreeState{dir: dir}

	op, err := gitx.InProgress(ctx, dir)
	if err != nil {
		return t, fmt.Errorf("%w in %s: %w", ErrOpUnread, dir, err)
	}

	t.Op = op

	dirty, err := gitx.Status(ctx, dir)
	if err != nil {
		return t, err
	}

	t.Dirty = len(dirty)

	return t, nil
}

// CatchupRecord notes what the catchup about to go out for a mark starts
// from. A read git cannot answer fails: a record made up would halt a
// healthy task, or loop on a doomed one.
func CatchupRecord(
	ctx context.Context, main, dir, branch string, key branchmeta.Key, now time.Time, fetched *FetchOnce,
) (*drivestore.Catchup, error) {
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

	origin, err := originTip(ctx, dir, branch, fetched)
	if err != nil {
		return nil, err
	}

	return &drivestore.Catchup{
		Target: target, Head: head, Origin: origin, Base: base, At: now, TargetRef: targetRef,
	}, nil
}

// CatchupConflict is the fingerprint of the conflict branch meets with a
// mark's target now, and the files it conflicts on.
func CatchupConflict(ctx context.Context, main, dir, branch string, key branchmeta.Key) (string, string, error) {
	target, _, err := catchupTarget(ctx, main, branch, key)
	if err != nil {
		return "", "", err
	}

	fp, paths, err := ConflictFingerprint(ctx, dir, target)
	if err != nil {
		return "", "", fmt.Errorf("reading what %s conflicts on: %w", branch, err)
	}

	return fp, paths, nil
}

// ConflictFingerprint is the fingerprint of the conflict the worktree at dir
// meets merging target, and the files it conflicts on; the drive and the
// watcher compare these, so both build them here.
func ConflictFingerprint(ctx context.Context, dir, target string) (string, string, error) {
	conflicts, err := gitx.Conflicts(ctx, dir, target, "HEAD")
	if err != nil {
		return "", "", err
	}

	return drivestore.Fingerprint(target, conflictWay(conflicts), ""), conflictPaths(conflicts), nil
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
func originTip(ctx context.Context, dir, branch string, fetched *FetchOnce) (string, error) {
	tracking := "refs/remotes/origin/" + branch

	pushed, err := gitx.Ref(ctx, dir, tracking)
	if err != nil || pushed == "" {
		return pushed, err
	}

	if err := fetched.Fetch(ctx, dir); err != nil {
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

	def, err := LocalDefault(ctx, main)
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
