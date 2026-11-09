package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// binaryCatchup is how the drive catches a branch up itself: git integrates
// and the binary verifies, and the agent is prompted only for what git or
// verify could not settle. A nil one on driveInput leaves the whole catchup
// to the agent, as a Verify heading in prose needs.
type binaryCatchup struct {
	// policy holds the catchup keys; dir and shipStopped are set per task
	policy catchupInput
	traces bool
	// integrate, verify and base are the git and the verify the drive
	// runs, replaced by tests that script neither; base reads the base the
	// marks name without a fetch
	integrate func(ctx context.Context, in catchupInput, message func(catchupPlan) []string) (catchupPlan, bool, error)
	verify    func(ctx context.Context, dir, log string) (verifyRun, error)
	base      func(ctx context.Context, in catchupInput) (catchupPlan, error)
}

// newBinaryCatchup is the real git and verify; the caller has checked that
// spec is a verify the binary can run.
func newBinaryCatchup(policy catchupInput, spec verifySpec, traces bool, main string) *binaryCatchup {
	return &binaryCatchup{
		policy:    policy,
		traces:    traces,
		integrate: integrateBranch,
		base:      resolveCatchup,
		verify: func(ctx context.Context, dir, log string) (verifyRun, error) {
			return runVerify(ctx, dir, spec.commands(ctx, dir, main), log)
		},
	}
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

// catchupMessage is a catchup merge's message: git's own subject, then the
// trailers a catchup commit carries.
func (in *driveInput) catchupMessage(r *driveRun) func(catchupPlan) []string {
	return func(p catchupPlan) []string {
		msg := []string{"Merge " + p.base + " into " + p.branch}

		if in.catchup == nil || !in.catchup.traces {
			return msg
		}

		tasks := r.task.ref.Members()

		trailers, err := trace.Render(trace.Trailers{Tasks: tasks, Step: "catchup"})
		if err != nil || trailers == "" {
			return msg
		}

		trailers += changeTrailer(tasks[0], changeOf(in.storage.Reviews(tasks[0])))

		return append(msg, strings.TrimSpace(trailers))
	}
}

// catchupHere runs a catchup the drive sends: an operation already in
// progress goes to the agent as it stands, a dirty tree is a failed attempt,
// and anything else is integrated here.
func (in *driveInput) catchupHere(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	ws *agentctl.Workspace,
	run *state.Run,
) error {
	id, dir := r.task.ref.ID, r.o.path

	// an idle agent on a run still running may sit on a dialog over files
	// git is about to rewrite, so it halts here as a prompt would
	fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: r.sentAt, since: r.freshAt}
	if err := refuseStalled(fwd, ws, agentctl.AgentName(id, r.task.suffix, dir), dir); err != nil {
		in.end(ctx, w, r, halted(id, "catchup", err.Error()))

		return nil
	}

	op, err := gitx.InProgress(ctx, dir)
	if err != nil {
		in.end(ctx, w, r, halted(id, "catchup", err.Error()))

		return nil
	}

	if op.Kind != "" {
		return in.catchupPrompt(ctx, w, r, ws, in.inProgressSteering(ctx, r, op))
	}

	// what the last turn left (markers, uncommitted paths) goes back to the
	// agent: integrating again would find the base held and verify past it
	if left := r.catchupLeft; left != "" {
		r.catchupLeft = ""

		return in.catchupPrompt(ctx, w, r, ws, "the last catchup turn left it unsettled: "+left+
			": settle it in place, keeping both sides, and commit")
	}

	if dirty, err := gitx.Status(ctx, dir); err != nil || len(dirty) > 0 {
		reason := fmt.Sprintf("%d uncommitted paths in %s", len(dirty), dir)
		if err != nil {
			reason = err.Error()
		}

		in.failed(ctx, w, r, catchupHalt(r, "the catchup cannot start: "+reason))

		return nil
	}

	ci := in.catchup.policy
	ci.dir, ci.main = dir, in.main
	ci.shipStopped = run != nil && (run.Status("ship") == "failed" || run.Status("ship") == "partial")

	p, stopped, err := in.catchup.integrate(ctx, ci, in.catchupMessage(r))
	if err != nil {
		in.failed(ctx, w, r, catchupHalt(r, "catching up: "+err.Error()))

		return nil //nolint:nilerr // a failed integration is the task's attempt, not the drive's error
	}

	if r.lastCatchup != nil && p.base != "" {
		r.lastCatchup.base = p.base
	}

	r.catchupPlan = &p

	if p.method != methodNone && p.method != methodSkip && r.lastCatchup != nil {
		r.lastCatchup.integrated = true
	}

	if stopped {
		paths, _ := gitx.Unmerged(ctx, dir)

		verb, what := "rebased "+r.o.branch+" onto", "rebase"
		if p.method == methodMerge {
			verb, what = "merged", "merge"
		}

		return in.catchupPrompt(ctx, w, r, ws, fmt.Sprintf(
			"the drive %s %s (%s) and git stopped on conflicts in %s: settle them in place, keeping both sides, "+
				"then conclude the %s",
			verb, p.base, gitx.ShortSHA(p.sha), listOrNone(paths), what))
	}

	if p.method == methodNone || p.method == methodSkip {
		key, err := in.mark(ctx, r)
		if err != nil {
			in.end(ctx, w, r, halted(id, "catchup", err.Error()))

			return nil
		}

		// a marked branch that already holds its base may be a drive drained
		// between its integration and verify, so it verifies before the marks clear
		if key == "" {
			fmt.Fprintf(w, "%s catchup: %s already holds %s\n", in.id(id), r.o.branch, cmp.Or(p.base, "its base"))

			return in.next(ctx, w, r)
		}

		// a rewrite this catchup did not make (an earlier drive's) predates
		// the origin read, so that record cannot lease a force-push
		if r.lastCatchup != nil && !r.lastCatchup.integrated {
			r.lastCatchup.held = false
		}
	}

	how := "rebased onto"
	switch p.method {
	case methodMerge:
		how = "merged"
	case methodNone, methodSkip:
		how = "already holds"
	}

	fmt.Fprintf(w, "%s catchup: %s %s %s, verifying\n", in.id(id), r.o.branch, how, p.base)
	in.startVerify(ctx, r)

	return nil
}

// inProgressSteering names, from git, the merge or rebase a turn must finish.
func (in *driveInput) inProgressSteering(ctx context.Context, r *driveRun, op gitx.Operation) string {
	paths, _ := gitx.Unmerged(ctx, r.o.path)

	at := ""
	if op.Kind == "rebase" && op.Total > 0 {
		at = fmt.Sprintf(" at commit %d of %d", op.Done, op.Total)
	}

	return fmt.Sprintf("a %s onto %s is still in progress in %s%s, conflicted: %s: settle it in place, keeping "+
		"both sides, and conclude it", op.Kind, gitx.ShortSHA(op.Onto), r.o.path, at, listOrNone(paths))
}

// catchupPrompt sends /f10:catchup with steering as a new turn of the
// catchup in flight.
func (in *driveInput) catchupPrompt(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	ws *agentctl.Workspace,
	steering string,
) error {
	r.sentAt, r.begun, r.idleSince, r.attached = in.now(), false, time.Time{}, false
	r.typed, r.enterRetried = "", false

	return in.prompt(ctx, w, r, ws, "catchup", steering)
}

func listOrNone(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}

	return strings.Join(paths, ", ")
}

// catchupHalt is the end a catchup that fails its last attempt gives.
func catchupHalt(r *driveRun, reason string) driveResult {
	halt := halted(r.task.ref.ID, "catchup", reason)
	halt.lines = append(halt.lines, "next: settle it with /f10:catchup in "+r.o.path+", then rerun the drive")

	return halt
}

func (in *driveInput) startVerify(ctx context.Context, r *driveRun) {
	dir, log := r.o.path, filepath.Join(cmp.Or(in.root, os.TempDir()), "catchup", r.task.ref.ID+".log")

	r.verifying = startVerifyJob(ctx, func(ctx context.Context) (verifyRun, error) {
		return in.catchup.verify(ctx, dir, log)
	})
}

// catchupSettled reads from git whether the agent's turn settled the
// catchup, and verifies if it did; anything left is a failed attempt, whose
// resend prompts again naming what is left, or with retry false (a prompt
// the agent never took, maybe under a dialog typing could answer) a halt.
func (in *driveInput) catchupSettled(ctx context.Context, w io.Writer, r *driveRun, retry bool) error {
	id, dir := r.task.ref.ID, r.o.path

	reason, err := in.catchupLeft(ctx, r)

	switch {
	case err != nil:
		in.failed(ctx, w, r, catchupHalt(r, err.Error()))
	case reason != "" && !retry:
		in.end(ctx, w, r, catchupHalt(r, "the catchup did not end clean: "+reason))
	case reason != "":
		// a branch that lost its base is integrated again by the drive, by the
		// declared method; anything else goes back to the agent
		if !strings.Contains(reason, notHeld) {
			r.catchupLeft = reason
		}

		in.failed(ctx, w, r, catchupHalt(r, "the catchup did not end clean: "+reason))
	default:
		fmt.Fprintf(w, "%s catchup: %s settled, verifying\n", in.id(id), dir)
		in.startVerify(ctx, r)
	}

	return nil
}

// catchupStalled reads a catchup turn that went idle without its report.
// What git explains is left goes back to the agent, under the count of
// attempts with nothing changed. An open dialog halts at once, because typing
// the prompt again could answer it.
func (in *driveInput) catchupStalled(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	run *state.Run,
	halt driveResult,
) error {
	if run != nil {
		if wt, ok := state.OpenWait(in.stateDir, run.Session); ok && wt.Reason != observe.WaitTurn {
			in.end(ctx, w, r, halt)

			return nil
		}
	}

	return in.catchupSettled(ctx, w, r, true)
}

// catchupLeft says what keeps the branch from being caught up, "" for
// nothing: an operation in progress, uncommitted paths, the base not in the
// branch, or a conflict marker left in a path the branch changes.
func (in *driveInput) catchupLeft(ctx context.Context, r *driveRun) (string, error) {
	dir := r.o.path

	op, err := gitx.InProgress(ctx, dir)
	if err != nil {
		return "", err
	}

	if op.Kind != "" {
		at := ""
		if op.Kind == "rebase" && op.Total > 0 {
			at = fmt.Sprintf(" at commit %d of %d", op.Done, op.Total)
		}

		return fmt.Sprintf("a %s is still in progress in %s%s", op.Kind, dir, at), nil
	}

	dirty, err := gitx.Status(ctx, dir)
	if err != nil {
		return "", err
	}

	if len(dirty) > 0 {
		return fmt.Sprintf("%d uncommitted paths in %s", len(dirty), dir), nil
	}

	p, err := in.catchupBase(ctx, r)
	if err != nil {
		return "", err
	}

	held, err := gitx.IsAncestor(ctx, dir, p.sha, "HEAD")
	if err != nil {
		return "", err
	}

	if !held {
		return fmt.Sprintf("%s%s%s (%s)", r.o.branch, notHeld, p.base, gitx.ShortSHA(p.sha)), nil
	}

	markers, err := gitx.Markers(ctx, dir, p.sha)
	if err != nil {
		return "", err
	}

	if len(markers) > 0 {
		return "conflict markers left in " + strings.Join(markers, ", "), nil
	}

	return "", nil
}

// notHeld marks catchupLeft's reason for a branch that lost its base.
const notHeld = " does not hold "

// catchupBase is the base the catchup brought in: the one this drive
// integrated, else, for a drive that did not (resumed), the one the marks
// name, read as f10 pr catchup reads it without a fetch. Re-reading after a
// turn would move it to the base branch's newer tip once a mark is cleared.
func (in *driveInput) catchupBase(ctx context.Context, r *driveRun) (catchupPlan, error) {
	if r.catchupPlan != nil {
		return *r.catchupPlan, nil
	}

	ci := in.catchup.policy
	ci.dir, ci.main = r.o.path, in.main

	p, err := in.catchup.base(ctx, ci)
	if err == nil && p.sha == "" {
		err = fmt.Errorf("the base of %s resolves to no commit", r.o.branch)
	}

	return p, err
}

// verified acts on the verify the drive ran: green clears the marks, pushes
// and moves on; red prompts the agent to fix it by the branch's intent.
func (in *driveInput) verified(ctx context.Context, w io.Writer, r *driveRun, job *verifyJob) error {
	id := r.task.ref.ID

	if job.err != nil {
		in.failed(ctx, w, r, catchupHalt(r, "running verify: "+job.err.Error()))

		return nil //nolint:nilerr // a verify that could not run is the task's attempt
	}

	p, err := in.catchupBase(ctx, r)
	if err != nil {
		in.failed(ctx, w, r, catchupHalt(r, err.Error()))

		return nil
	}

	if job.res.failed != "" {
		fmt.Fprintf(w, "%s catchup: verify is red: %s\n", in.id(id), job.res.failed)

		// a red the agent cannot fix counts like any failed catchup: the same
		// command red on the same head three times halts
		head, _ := gitx.Ref(ctx, r.o.path, "HEAD")
		halt := catchupHalt(
			r,
			"verify is red after the catchup: "+job.res.failed+" failed, its output is in "+job.res.log,
		)

		if res := in.attemptAt(w, r, "catchup", head+"|"+job.res.failed, halt); res != nil {
			in.end(ctx, w, r, *res)

			return nil
		}

		ws, _, err := in.look(ctx, r)
		if err != nil {
			return err
		}

		return in.catchupPrompt(ctx, w, r, ws, fmt.Sprintf(
			"%s came in cleanly and verify is red: `%s` failed, its output is in %s: fix it by the branch's "+
				"intent, verify, and commit the fix", p.base, job.res.failed, job.res.log))
	}

	// the agent may have committed since verify started, so git is read again
	reason, err := in.catchupLeft(ctx, r)
	if err == nil && reason != "" {
		r.catchupLeft = reason
		err = fmt.Errorf("the catchup did not end clean: %s", reason)
	}

	if err != nil {
		in.failed(ctx, w, r, catchupHalt(r, err.Error()))

		return nil
	}

	r.catchupPlan = nil

	if err := clearMarks(ctx, in.main, r.o.branch, p); err != nil {
		in.end(ctx, w, r, halted(id, "catchup", err.Error()))

		return nil
	}

	// the count clears once the entry the catchup guarded goes through, so a
	// conflict that comes back each time still reaches the limit
	fmt.Fprintf(w, "%s catchup: %s caught up with %s, verify green\n", in.id(id), r.o.branch, p.base)

	if res := in.pushCatchup(ctx, w, r); res != nil {
		in.end(ctx, w, r, *res)

		return nil
	}

	return in.next(ctx, w, r)
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

// catchupSent is what the branch looked like when a catchup went out, for
// the progress check a later conflict runs; a field left "" compares to
// nothing, and origin, the target's remote-tracking ref, is "" when the
// target is recorded nowhere.
type catchupSent struct {
	target, head, way, base, origin string
	// pushed is origin's copy of the branch itself then, what the push after
	// the catchup leases against; held is false once a push or a later skill
	// made it stale
	pushed string
	held   bool
	// integrated is set once this catchup rewrote the branch itself, after
	// pushed was read, so the lease stays sound on a resend
	integrated bool
}

// halts says why a conflict after this catchup halts the task, or "" when
// another catchup can still make progress, as when the target moved.
func (c *catchupSent) halts(target, head string, conflicts []gitx.Conflict, base string) string {
	switch {
	case c == nil:
		return ""
	case head != "" && head == c.head:
		return "the catchup left the branch unchanged"
	case target != "" && target == c.target:
		return base + " has not moved since the catchup"
	case len(conflicts) > 0 && conflictWay(conflicts) == c.way:
		return "the same files conflict with " + base + " the same way again: " + conflictPaths(conflicts)
	}

	return ""
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

// recordCatchup notes what the catchup about to go out starts from, for the
// progress check a later conflict runs. A read git cannot answer halts: a
// record made up would halt a healthy task, or loop on a doomed one.
func (in *driveInput) recordCatchup(ctx context.Context, r *driveRun, key branchmeta.Key) error {
	target, base, err := in.catchupTarget(ctx, r, key)
	if err != nil {
		return err
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return fmt.Errorf("reading the head of %s: %w", r.o.branch, err)
	}

	conflicts, err := gitx.Conflicts(ctx, r.o.path, target, "HEAD")
	if err != nil {
		return fmt.Errorf("reading what %s conflicts on: %w", r.o.branch, err)
	}

	origin, err := in.targetOrigin(ctx, r, key)
	if err != nil {
		return err
	}

	pushed, err := in.originTip(ctx, r)
	if err != nil {
		return err
	}

	r.lastCatchup = &catchupSent{
		target: target, head: head, way: conflictWay(conflicts), base: base, origin: origin,
		pushed: pushed, held: true,
	}

	return nil
}

// originTip is origin's copy of the branch, fetched first when origin has
// it, since a mark set earlier may follow no fetch at all.
func (in *driveInput) originTip(ctx context.Context, r *driveRun) (string, error) {
	tracking := "refs/remotes/origin/" + r.o.branch

	pushed, err := gitx.Ref(ctx, r.o.path, tracking)
	if err != nil || pushed == "" {
		return pushed, err
	}

	if _, err := gitx.Exec(ctx, r.o.path, "fetch", "origin"); err != nil {
		return "", fmt.Errorf("fetching origin before catching %s up: %w", r.o.branch, err)
	}

	if pushed, err = gitx.Ref(ctx, r.o.path, tracking); err != nil {
		return "", fmt.Errorf("reading origin/%s: %w", r.o.branch, err)
	}

	return pushed, nil
}

// targetOrigin is the remote-tracking ref of what a mark's catchup brings
// in; a base recorded nowhere gives "".
func (in *driveInput) targetOrigin(ctx context.Context, r *driveRun, key branchmeta.Key) (string, error) {
	if key == branchmeta.Moved {
		after, err := branchmeta.Get(ctx, in.main, r.o.branch, branchmeta.AfterBranch)
		if err != nil || after == "" {
			return "", err
		}

		return "refs/remotes/origin/" + after, nil
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return "", err
	}

	return "refs/remotes/origin/" + def, nil
}

// catchupTarget resolves a mark's target as the catchup step does, and the
// progress check reads the target through this too, so the record and the
// comparison never name different commits.
func (in *driveInput) catchupTarget(ctx context.Context, r *driveRun, key branchmeta.Key) (string, string, error) {
	if key == branchmeta.Moved {
		target, err := branchmeta.Get(ctx, in.main, r.o.branch, key)
		if err != nil {
			return "", "", fmt.Errorf("reading the %s mark on %s: %w", key, r.o.branch, err)
		}

		return target, "its base", nil
	}

	return defaultTarget(ctx, in.main)
}

// conflictAgain says why a conflict after the catchup this drive sent makes
// no progress, or "" to catch up again, and the conflict as attempts compare
// it; the files conflicting now are named either way.
func (in *driveInput) conflictAgain(ctx context.Context, r *driveRun, key branchmeta.Key) (string, string, error) {
	if r.lastCatchup == nil {
		return "", "", nil
	}

	target, base, err := in.catchupTarget(ctx, r, key)
	if err != nil {
		return "", "", err
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading the head of %s: %w", r.o.branch, err)
	}

	conflicts, err := gitx.Conflicts(ctx, r.o.path, target, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading what %s conflicts on: %w", r.o.branch, err)
	}

	reason := r.lastCatchup.halts(target, head, conflicts, base)
	if reason != "" && len(conflicts) > 0 && !strings.HasSuffix(reason, conflictPaths(conflicts)) {
		reason += "; conflicting: " + conflictPaths(conflicts)
	}

	if reason != "" {
		reason = r.o.branch + " still conflicts with " + base + " after a catchup: " + reason
	}

	return reason, conflictWay(conflicts), nil
}

// catchupAttempt counts a catchup that did not bring the branch up to date,
// and on the limit halts with the way to settle it by hand.
func (in *driveInput) catchupAttempt(w io.Writer, r *driveRun, skill, reason, conflict string) *driveResult {
	res := in.attemptAt(w, r, "catchup", conflict, halted(r.task.ref.ID, skill, reason))
	if res != nil {
		res.lines = append(res.lines, "next: settle it with /f10:catchup in "+r.o.path+", then rerun the drive")
	}

	return res
}

// pushCaughtUp pushes plain when the catchup merged, so origin's copy is an
// ancestor, and with a lease against pushed when it rebased. Pushed must be
// origin's sha when the catchup went out: a commit pushed while it ran is not
// on a rebased branch, and a lease read after would let the push drop it.
// With no such record (a resumed drive, a rerun) only a plain push goes out.
func (in *driveInput) pushCaughtUp(ctx context.Context, w io.Writer, r *driveRun, skill, pushed, base string) error {
	refused := originMovedError{branch: r.o.branch, base: base, path: r.o.path}

	if c := r.lastCatchup; c != nil && c.held {
		c.held = false

		if pushed != c.pushed {
			return refused
		}
	} else {
		fastForward, err := gitx.IsAncestor(ctx, r.o.path, pushed, "HEAD")
		if err != nil {
			return err
		}

		if !fastForward {
			refused.unseen = true

			return refused
		}
	}

	how, err := pushCaughtUp(ctx, r.o.path, r.o.branch, pushed, base)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s: %s caught up with %s, %s\n", in.id(r.task.ref.ID), skill, r.o.branch, base, how)

	return nil
}

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

// pushHalted halts on a failed push, saying how to bring in what origin
// gained when that is the cause.
func pushHalted(id, skill string, err error) driveResult {
	res := halted(id, skill, err.Error())

	moved := originMovedError{}

	switch {
	case !errors.As(err, &moved):
	case moved.unseen:
		res.lines = append(res.lines, "next: check origin/"+moved.branch+" holds nothing "+moved.path+
			" lacks, push it with --force-with-lease yourself, then rerun the drive")
	default:
		res.lines = append(res.lines, "next: bring origin/"+moved.branch+" into "+moved.path+
			", then /f10:catchup there, then rerun the drive")
	}

	return res
}

// pushCatchup pushes a branch origin has once a catchup ended clean, so its
// PR carries the integration before the next skill, but never commits origin
// lacks: another task's unpushed work must not ride into this PR.
func (in *driveInput) pushCatchup(ctx context.Context, w io.Writer, r *driveRun) *driveResult {
	id, branch := r.task.ref.ID, r.o.branch

	halt := func(err error) *driveResult {
		res := halted(id, "catchup", err.Error())
		res.lines = append(res.lines, "next: rerun the drive")

		return &res
	}

	pushed, err := gitx.Ref(ctx, r.o.path, "refs/remotes/origin/"+branch)
	if err != nil {
		return halt(err)
	}

	if pushed == "" {
		return nil
	}

	// fetched first, so a push made while the catchup ran shows here
	if _, err := gitx.Exec(ctx, r.o.path, "fetch", "origin"); err != nil {
		return halt(err)
	}

	if pushed, err = gitx.Ref(ctx, r.o.path, "refs/remotes/origin/"+branch); err != nil {
		return halt(err)
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return halt(err)
	}

	if head == pushed {
		return nil
	}

	base := "its base"
	if r.lastCatchup != nil {
		base = r.lastCatchup.base
	}

	if c := r.lastCatchup; c != nil && c.origin != "" && c.target != "" {
		seen, err := gitx.IsAncestor(ctx, r.o.path, c.target, c.origin)
		if err != nil {
			return halt(err)
		}

		if !seen {
			fmt.Fprintf(w, "%s catchup: %s caught up with %s, not pushed: %s is not on origin yet\n",
				in.id(id), branch, base, base)

			return nil
		}
	}

	if err := in.pushCaughtUp(ctx, w, r, "catchup", pushed, base); err != nil {
		if errors.As(err, new(originMovedError)) {
			res := pushHalted(id, "catchup", err)

			return &res
		}

		return halt(err)
	}

	return nil
}
