package drive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
)

// catchupHere runs a catchup the drive sends: an operation already in
// progress goes to the agent as it stands, a dirty tree is a failed attempt,
// and anything else is integrated here.
func (in *DriveInput) catchupHere(
	ctx context.Context,
	r *DriveRun,
	ws *agentctl.Workspace,
	run *state.Run,
) error {
	id, dir := r.Task.Ref.ID, r.O.Path

	// an idle agent on a run still running may sit on a dialog over files
	// git is about to rewrite, so it halts here as a prompt would
	if err := start.RefuseStalled(in.stallCheck(r), ws, agentctl.AgentName(id, r.Task.Suffix, dir), dir); err != nil {
		return Halt(id, "catchup", err.Error())
	}

	tree, err := stack.ReadTree(ctx, dir)
	if errors.Is(err, stack.ErrOpUnread) {
		return Halt(id, "catchup", err.Error())
	}

	if tree.Op.Kind != "" {
		return in.catchupPrompt(ctx, r, ws, in.inProgressSteering(ctx, r, tree.Op))
	}

	// what the last turn left (markers, uncommitted paths) goes back to the
	// agent: integrating again would find the base held and verify past it
	if left := r.catchupLeft; left != "" {
		r.catchupLeft = ""

		return in.catchupPrompt(ctx, r, ws, "the last catchup turn left it unsettled: "+left+
			": settle it in place, keeping both sides, and commit"+in.Catchup.VerifyClause())
	}

	if err != nil || tree.Dirty > 0 {
		reason := tree.Busy()
		if err != nil {
			reason = err.Error()
		}

		return in.failed(ctx, r, catchupHalt(r, "the catchup cannot start: "+reason))
	}

	ci := in.Catchup.Input(in.Main, dir)
	ci.Fetched = &in.View().fetch
	ci.ShipStopped = run != nil && (run.Status("ship") == "failed" || run.Status("ship") == "partial")

	tasks := r.Task.Ref.Members()
	p, stopped, err := in.Catchup.Integrate(
		ctx,
		ci,
		in.Catchup.Message(tasks, archive.ChangeOf(in.Storage.Reviews(tasks[0]))),
	)
	if err != nil {
		return in.failed(
			ctx,
			r,
			catchupHalt(r, "catching up: "+err.Error()),
		)
	}

	if c := r.LastCatchup; c != nil {
		if p.Base != "" {
			c.Base = p.Base
		}

		if p.Method != stack.MethodNone && p.Method != stack.MethodSkip {
			c.Integrated = true
		}

		in.KeepCatchup(r)
	}

	r.catchupPlan = &p

	if stopped {
		paths, _ := gitx.Unmerged(ctx, dir)

		return in.catchupPrompt(ctx, r, ws, in.Catchup.ConflictSteering(p, r.O.Branch, "the drive", paths))
	}

	if p.Method == stack.MethodNone || p.Method == stack.MethodSkip {
		key, err := in.mark(ctx, r)
		if err != nil {
			return Halt(id, "catchup", err.Error())
		}

		// a marked branch that already holds its base may be a drive drained
		// between its integration and verify, so it verifies before the marks clear
		if key == "" {
			fmt.Fprintf(in.Out, "%s catchup: %s already holds %s\n", in.id(id), r.O.Branch, cmp.Or(p.Base, "its base"))

			return in.Next(ctx, r)
		}

		// a rewrite no catchup on record made predates the origin read, so
		// that record cannot lease a force-push
		if c := r.LastCatchup; c != nil && !c.Integrated {
			c.Spent = true
			in.KeepCatchup(r)
		}
	}

	how := "rebased onto"
	switch p.Method {
	case stack.MethodMerge:
		how = "merged"
	case stack.MethodNone, stack.MethodSkip:
		how = "already holds"
	}

	if in.Catchup.AgentVerifies != "" {
		fmt.Fprintf(in.Out, "%s catchup: %s %s %s, the agent verifies\n", in.id(id), r.O.Branch, how, p.Base)

		return in.catchupPrompt(ctx, r, ws, in.Catchup.CleanSteering(p.Base))
	}

	fmt.Fprintf(in.Out, "%s catchup: %s %s %s, verifying\n", in.id(id), r.O.Branch, how, p.Base)
	in.startVerify(ctx, r)

	return nil
}

// inProgressSteering names, from git, the merge or rebase a turn must finish.
func (in *DriveInput) inProgressSteering(ctx context.Context, r *DriveRun, op gitx.Operation) string {
	paths, _ := gitx.Unmerged(ctx, r.O.Path)

	at := ""
	if op.Kind == "rebase" && op.Total > 0 {
		at = fmt.Sprintf(" at commit %d of %d", op.Done, op.Total)
	}

	return fmt.Sprintf("a %s onto %s is still in progress in %s%s, conflicted: %s: settle it in place, keeping "+
		"both sides, and conclude it%s", op.Kind, gitx.ShortSHA(op.Onto), r.O.Path, at, stack.ListOrNone(paths),
		in.Catchup.VerifyClause())
}

// catchupPrompt sends /f10:catchup with steering as a new turn of the
// catchup in flight.
func (in *DriveInput) catchupPrompt(
	ctx context.Context,
	r *DriveRun,
	ws *agentctl.Workspace,
	steering string,
) error {
	r.turn = turn{Skill: r.Skill, SentAt: in.Now(), retry: r.retry}

	return in.prompt(ctx, r, ws, "catchup", steering)
}

// catchupHalt is the end a catchup that fails its last attempt gives.
func catchupHalt(r *DriveRun, reason string) DriveResult {
	halt := Halted(r.Task.Ref.ID, "catchup", reason)
	halt.Lines = append(halt.Lines, "next: settle it with /f10:catchup in "+r.O.Path+", then rerun the drive")

	return halt
}

func (in *DriveInput) startVerify(ctx context.Context, r *DriveRun) {
	dir, log := r.O.Path, filepath.Join(cmp.Or(in.Root, os.TempDir()), "catchup", r.Task.Ref.ID+".log")

	in.Enter(r, atVerifying)
	r.Job = StartVerifyJob(ctx, func(ctx context.Context) (stack.VerifyRun, error) {
		return in.Catchup.Verify(ctx, dir, log)
	})
}

// catchupSettled reads from git whether the agent's turn settled the
// catchup, and verifies if it did; anything left is a failed attempt, whose
// resend prompts again naming what is left, or with retry false (a prompt
// the agent never took, maybe under a dialog typing could answer) a halt.
func (in *DriveInput) catchupSettled(ctx context.Context, r *DriveRun, retry bool) error {
	id, dir := r.Task.Ref.ID, r.O.Path

	tree, err := in.CatchupLeft(ctx, r)
	reason := tree.Left()

	switch {
	case err != nil:
		return in.failed(ctx, r, catchupHalt(r, err.Error()))
	case reason != "" && !retry:
		return endWith(catchupHalt(r, "the catchup did not end clean: "+reason))
	case reason != "":
		// a branch that lost its base is integrated again by the drive, by the
		// declared method; anything else goes back to the agent
		if !tree.BaseLost() {
			r.catchupLeft = reason
		}

		return in.failed(ctx, r, catchupHalt(r, "the catchup did not end clean: "+reason))
	case in.Catchup.AgentVerifies != "":
		// the agent verified and cleared the marks, so it is green as it stands
		fmt.Fprintf(in.Out, "%s catchup: %s settled, verified by the agent\n", in.id(id), dir)

		return in.verified(ctx, r, &verifyJob{})
	default:
		fmt.Fprintf(in.Out, "%s catchup: %s settled, verifying\n", in.id(id), dir)
		in.startVerify(ctx, r)
	}

	return nil
}

// catchupStalled reads a catchup turn that went idle without its report.
// What git explains is left goes back to the agent, under the count of
// attempts with nothing changed. An open dialog halts at once, because typing
// the prompt again could answer it.
func (in *DriveInput) catchupStalled(
	ctx context.Context,
	r *DriveRun,
	run *state.Run,
	halt DriveResult,
) error {
	if run != nil {
		if wt, ok := state.OpenWait(in.StateDir, run.Session); ok && wt.Reason != observe.WaitTurn {
			return endWith(halt)
		}
	}

	return in.catchupSettled(ctx, r, true)
}

// CatchupLeft is the engine's read of what keeps the branch from being
// caught up with the base this catchup brought in.
func (in *DriveInput) CatchupLeft(ctx context.Context, r *DriveRun) (stack.TreeState, error) {
	p, err := in.CatchupBase(ctx, r)
	if err != nil {
		return stack.TreeState{}, err
	}

	return in.Catchup.Left(ctx, in.Main, r.O.Path, r.O.Branch, p)
}

// CatchupBase is the base the catchup brought in: the one this drive
// integrated, else, for a drive that did not (resumed), the one the marks
// name, read as f10 pr catchup reads it without a fetch. Re-reading after a
// turn would move it to the base branch's newer tip once a mark is cleared.
func (in *DriveInput) CatchupBase(ctx context.Context, r *DriveRun) (stack.CatchupPlan, error) {
	if r.catchupPlan != nil {
		return *r.catchupPlan, nil
	}

	p, err := in.Catchup.Base(ctx, in.Catchup.Input(in.Main, r.O.Path))
	if err == nil && p.SHA == "" {
		err = fmt.Errorf("the base of %s resolves to no commit", r.O.Branch)
	}

	return p, err
}

// verified acts on the verify the drive ran: green clears the marks, pushes
// and moves on; red prompts the agent to fix it by the branch's intent.
func (in *DriveInput) verified(ctx context.Context, r *DriveRun, job *verifyJob) error {
	id := r.Task.Ref.ID

	if job.Err != nil {
		return in.failed(
			ctx,
			r,
			catchupHalt(r, "running verify: "+job.Err.Error()),
		)
	}

	p, err := in.CatchupBase(ctx, r)
	if err != nil {
		return in.failed(ctx, r, catchupHalt(r, err.Error()))
	}

	if job.res.Failed != "" {
		fmt.Fprintf(in.Out, "%s catchup: verify is red: %s\n", in.id(id), job.res.Failed)

		// a red the agent cannot fix counts like any failed catchup: the same
		// command red on the same target three times halts
		halt := catchupHalt(
			r,
			"verify is red after the catchup: "+job.res.Failed+" failed, its output is in "+job.res.Log,
		)

		if err := in.AttemptAt(r, "catchup", drives.Fingerprint(p.SHA, "", job.res.Failed), halt); err != nil {
			return err
		}

		ws, _, err := in.Look(ctx, r)
		if err != nil {
			return err
		}

		return in.catchupPrompt(ctx, r, ws, fmt.Sprintf(
			"%s came in cleanly and verify is red: `%s` failed, its output is in %s: fix it by the branch's "+
				"intent, verify, and commit the fix", p.Base, job.res.Failed, job.res.Log))
	}

	// the agent may have committed since verify started, so git is read again
	tree, err := in.CatchupLeft(ctx, r)
	if reason := tree.Left(); err == nil && reason != "" {
		r.catchupLeft = reason
		err = fmt.Errorf("the catchup did not end clean: %s", reason)
	}

	if err != nil {
		return in.failed(ctx, r, catchupHalt(r, err.Error()))
	}

	r.catchupPlan = nil

	if err := stack.ClearMarks(ctx, in.Main, r.O.Branch, p); err != nil {
		return Halt(id, "catchup", err.Error())
	}

	// the count clears once the entry the catchup guarded goes through, so a
	// conflict that comes back each time still reaches the limit
	fmt.Fprintf(in.Out, "%s catchup: %s caught up with %s, verify green\n", in.id(id), r.O.Branch, p.Base)

	if err := in.pushAfterCatchup(ctx, r, "catchup", ""); err != nil {
		return err
	}

	return in.Next(ctx, r)
}

// recordCatchup notes what the catchup about to go out starts from, kept on
// the drive record so a resumed drive leases its push the same way. A branch
// origin is ahead of is refused: the lease is read after those commits
// landed, so a rebase would push over them. Ahead of origin is the drive's
// own unpushed work, and fine.
func (in *DriveInput) recordCatchup(ctx context.Context, r *DriveRun, key branchmeta.Key) error {
	rec, err := stack.CatchupRecord(ctx, in.Main, r.O.Path, r.O.Branch, key, in.Now(), &in.View().fetch)
	if err != nil {
		return err
	}

	if rec.Origin != "" {
		held, err := gitx.IsAncestor(ctx, r.O.Path, rec.Origin, "refs/heads/"+r.O.Branch)
		if err != nil {
			return fmt.Errorf("reading whether %s holds origin/%s: %w", r.O.Branch, r.O.Branch, err)
		}

		if !held {
			return fmt.Errorf("origin/%s has commits %s lacks, so a catchup would push over them: "+
				"bring origin/%s into %s, then rerun the drive", r.O.Branch, r.O.Branch, r.O.Branch, r.O.Path)
		}
	}

	r.LastCatchup = rec
	in.KeepCatchup(r)

	return nil
}

// KeepCatchup writes the task's catchup record to the drive record.
func (in *DriveInput) KeepCatchup(r *DriveRun) {
	if in.Rec == nil {
		return
	}

	var c *drives.Catchup
	if r.LastCatchup != nil {
		saved := *r.LastCatchup
		c = &saved
	}

	in.saveTask(r, func(t *drives.Task) { t.Catchup = c })
}

// conflictAttempt counts a conflict met after the catchup this drive sent,
// by its fingerprint: the same conflict with the same target three times
// in a row halts. Nil means catch up again.
func (in *DriveInput) conflictAttempt(
	ctx context.Context, r *DriveRun, skill string, key branchmeta.Key,
) error {
	c := r.LastCatchup
	if c == nil {
		return nil
	}

	fp, paths, err := stack.CatchupConflict(ctx, in.Main, r.O.Path, r.O.Branch, key)
	if err != nil {
		return Halt(r.Task.Ref.ID, skill, err.Error())
	}

	reason := r.O.Branch + " still conflicts with " + cmp.Or(c.Base, "its base") + " after a catchup"
	if paths != "" {
		reason += ": " + paths
	}

	err = in.AttemptAt(r, "catchup", fp, Halted(r.Task.Ref.ID, skill, reason))
	if res, ok := EndOf(err); ok {
		res.Lines = append(res.Lines, "next: settle it with /f10:catchup in "+r.O.Path+", then rerun the drive")

		return endWith(res)
	}

	return err
}

// pushAfterCatchup pushes the caught-up branch through the engine's leased
// push and says so, naming base, else the record's; a refused or failed
// push halts.
func (in *DriveInput) pushAfterCatchup(
	ctx context.Context, r *DriveRun, skill, base string,
) error {
	id, branch := r.Task.Ref.ID, r.O.Branch

	how, err := stack.PushCatchup(ctx, r.O.Path, branch, r.LastCatchup)
	in.KeepCatchup(r)

	if c := r.LastCatchup; base == "" && c != nil {
		base = c.Base
	}

	base = cmp.Or(base, "its base")

	switch {
	case errors.Is(err, stack.ErrBaseNotPushed):
		// the drive's side of stack's UnpushedBase rule: integrated, push held
		fmt.Fprintf(in.Out, "%s %s: %s caught up with %s, not pushed: %s is not on origin yet\n",
			in.id(id), skill, branch, base, base)

		return nil
	case err != nil:
		return endWith(PushHalted(id, skill, err))
	case how != "":
		fmt.Fprintf(in.Out, "%s %s: %s caught up with %s, %s\n", in.id(id), skill, branch, base, how)
	}

	return nil
}

// PushHalted halts on a failed push, saying how to bring in what origin
// gained when that is the cause.
func PushHalted(id, skill string, err error) DriveResult {
	res := Halted(id, skill, err.Error())

	moved := stack.OriginMovedError{}

	switch {
	case !errors.As(err, &moved):
		res.Lines = append(res.Lines, "next: rerun the drive")
	case moved.Unseen:
		res.Lines = append(res.Lines, "next: check origin/"+moved.Branch+" holds nothing "+moved.Path+
			" lacks, push it with --force-with-lease yourself, then rerun the drive")
	default:
		res.Lines = append(res.Lines, "next: bring origin/"+moved.Branch+" into "+moved.Path+
			", then /f10:catchup there, then rerun the drive")
	}

	return res
}
