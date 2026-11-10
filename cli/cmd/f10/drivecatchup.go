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
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

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
			": settle it in place, keeping both sides, and commit"+in.catchup.verifyClause())
	}

	if dirty, err := gitx.Status(ctx, dir); err != nil || len(dirty) > 0 {
		reason := fmt.Sprintf("%d uncommitted paths in %s", len(dirty), dir)
		if err != nil {
			reason = err.Error()
		}

		in.failed(ctx, w, r, catchupHalt(r, "the catchup cannot start: "+reason))

		return nil
	}

	ci := in.catchup.input(in.main, dir)
	ci.shipStopped = run != nil && (run.Status("ship") == "failed" || run.Status("ship") == "partial")

	tasks := r.task.ref.Members()
	p, stopped, err := in.catchup.integrate(ctx, ci, in.catchup.message(tasks, changeOf(in.storage.Reviews(tasks[0]))))
	if err != nil {
		in.failed(ctx, w, r, catchupHalt(r, "catching up: "+err.Error()))

		return nil //nolint:nilerr // a failed integration is the task's attempt, not the drive's error
	}

	if c := r.lastCatchup; c != nil {
		if p.base != "" {
			c.Base = p.base
		}

		if p.method != methodNone && p.method != methodSkip {
			c.Integrated = true
		}

		in.keepCatchup(r)
	}

	r.catchupPlan = &p

	if stopped {
		paths, _ := gitx.Unmerged(ctx, dir)

		return in.catchupPrompt(ctx, w, r, ws, in.catchup.conflictSteering(p, r.o.branch, "the drive", paths))
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

		// a rewrite no catchup on record made predates the origin read, so
		// that record cannot lease a force-push
		if c := r.lastCatchup; c != nil && !c.Integrated {
			c.Spent = true
			in.keepCatchup(r)
		}
	}

	how := "rebased onto"
	switch p.method {
	case methodMerge:
		how = "merged"
	case methodNone, methodSkip:
		how = "already holds"
	}

	if in.catchup.agentVerifies != "" {
		fmt.Fprintf(w, "%s catchup: %s %s %s, the agent verifies\n", in.id(id), r.o.branch, how, p.base)

		return in.catchupPrompt(ctx, w, r, ws, in.catchup.cleanSteering(p.base))
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
		"both sides, and conclude it%s", op.Kind, gitx.ShortSHA(op.Onto), r.o.path, at, listOrNone(paths),
		in.catchup.verifyClause())
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
	case in.catchup.agentVerifies != "":
		// the agent verified and cleared the marks, so it is green as it stands
		fmt.Fprintf(w, "%s catchup: %s settled, verified by the agent\n", in.id(id), dir)

		return in.verified(ctx, w, r, &verifyJob{})
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

// catchupLeft is the engine's read of what keeps the branch from being
// caught up with the base this catchup brought in.
func (in *driveInput) catchupLeft(ctx context.Context, r *driveRun) (string, error) {
	p, err := in.catchupBase(ctx, r)
	if err != nil {
		return "", err
	}

	return in.catchup.left(ctx, in.main, r.o.path, r.o.branch, p)
}

// catchupBase is the base the catchup brought in: the one this drive
// integrated, else, for a drive that did not (resumed), the one the marks
// name, read as f10 pr catchup reads it without a fetch. Re-reading after a
// turn would move it to the base branch's newer tip once a mark is cleared.
func (in *driveInput) catchupBase(ctx context.Context, r *driveRun) (catchupPlan, error) {
	if r.catchupPlan != nil {
		return *r.catchupPlan, nil
	}

	p, err := in.catchup.base(ctx, in.catchup.input(in.main, r.o.path))
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
		// command red on the same target three times halts
		halt := catchupHalt(
			r,
			"verify is red after the catchup: "+job.res.failed+" failed, its output is in "+job.res.log,
		)

		if res := in.attemptAt(w, r, "catchup", drives.Fingerprint(p.sha, "", job.res.failed), halt); res != nil {
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

	if res := in.pushAfterCatchup(ctx, w, r, "catchup", ""); res != nil {
		in.end(ctx, w, r, *res)

		return nil
	}

	return in.next(ctx, w, r)
}

// recordCatchup notes what the catchup about to go out starts from, kept on
// the drive record so a resumed drive leases its push the same way.
func (in *driveInput) recordCatchup(ctx context.Context, r *driveRun, key branchmeta.Key) error {
	rec, err := catchupRecord(ctx, in.main, r.o.path, r.o.branch, key, in.now())
	if err != nil {
		return err
	}

	r.lastCatchup = rec
	in.keepCatchup(r)

	return nil
}

// keepCatchup writes the task's catchup record to the drive record.
func (in *driveInput) keepCatchup(r *driveRun) {
	if in.rec == nil {
		return
	}

	var c *drives.Catchup
	if r.lastCatchup != nil {
		saved := *r.lastCatchup
		c = &saved
	}

	in.saveTask(r, func(t *drives.Task) { t.Catchup = c })
}

// conflictAttempt counts a conflict met after the catchup this drive sent,
// by its fingerprint: the same conflict with the same target three times
// in a row halts. Nil means catch up again.
func (in *driveInput) conflictAttempt(
	ctx context.Context, w io.Writer, r *driveRun, skill string, key branchmeta.Key,
) *driveResult {
	c := r.lastCatchup
	if c == nil {
		return nil
	}

	fp, paths, err := catchupConflict(ctx, in.main, r.o.path, r.o.branch, key)
	if err != nil {
		res := halted(r.task.ref.ID, skill, err.Error())

		return &res
	}

	reason := r.o.branch + " still conflicts with " + cmp.Or(c.Base, "its base") + " after a catchup"
	if paths != "" {
		reason += ": " + paths
	}

	res := in.attemptAt(w, r, "catchup", fp, halted(r.task.ref.ID, skill, reason))
	if res != nil {
		res.lines = append(res.lines, "next: settle it with /f10:catchup in "+r.o.path+", then rerun the drive")
	}

	return res
}

// pushAfterCatchup pushes the caught-up branch through the engine's leased
// push and says so, naming base, else the record's; a refused or failed
// push halts.
func (in *driveInput) pushAfterCatchup(
	ctx context.Context, w io.Writer, r *driveRun, skill, base string,
) *driveResult {
	id, branch := r.task.ref.ID, r.o.branch

	how, err := pushCatchup(ctx, r.o.path, branch, r.lastCatchup)
	in.keepCatchup(r)

	if c := r.lastCatchup; base == "" && c != nil {
		base = c.Base
	}

	base = cmp.Or(base, "its base")

	switch {
	case errors.Is(err, errBaseNotPushed):
		fmt.Fprintf(w, "%s %s: %s caught up with %s, not pushed: %s is not on origin yet\n",
			in.id(id), skill, branch, base, base)

		return nil
	case err != nil:
		res := pushHalted(id, skill, err)

		return &res
	case how != "":
		fmt.Fprintf(w, "%s %s: %s caught up with %s, %s\n", in.id(id), skill, branch, base, how)
	}

	return nil
}

// pushHalted halts on a failed push, saying how to bring in what origin
// gained when that is the cause.
func pushHalted(id, skill string, err error) driveResult {
	res := halted(id, skill, err.Error())

	moved := originMovedError{}

	switch {
	case !errors.As(err, &moved):
		res.lines = append(res.lines, "next: rerun the drive")
	case moved.unseen:
		res.lines = append(res.lines, "next: check origin/"+moved.branch+" holds nothing "+moved.path+
			" lacks, push it with --force-with-lease yourself, then rerun the drive")
	default:
		res.lines = append(res.lines, "next: bring origin/"+moved.branch+" into "+moved.path+
			", then /f10:catchup there, then rerun the drive")
	}

	return res
}
