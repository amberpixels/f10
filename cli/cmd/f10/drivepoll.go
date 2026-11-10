package main

import (
	"context"
	"fmt"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// stalled is an agent idle for grace with its turn's end unreported, once
// the Stop hook had time; drive and the watcher read a stall the same way.
// Background work launched at bg keeps it working: Herdr shows an agent idle
// while it waits on one.
func stalled(
	status agentctl.Status,
	run *state.Run,
	skill string,
	since time.Time,
	idle, grace time.Duration,
	bg, now time.Time,
) bool {
	atRest := status == agentctl.StatusIdle || status == agentctl.StatusDone

	return atRest && unreported(run, skill, since) && idle >= grace && !backgroundPending(bg, now)
}

// backgroundPending is background work launched at bg still counting as
// running: up to verifyLimit, the longest a catchup's verify may take, so a
// job whose notification never comes still ends in a stall.
func backgroundPending(bg, now time.Time) bool {
	return !bg.IsZero() && now.Sub(bg) < stack.VerifyLimit
}

// backgroundAt is when the run's session last launched background work its
// hooks still count as pending, the zero time for none.
func backgroundAt(dir string, run *state.Run) time.Time {
	if run == nil {
		return time.Time{}
	}

	b, _ := state.PendingBackground(dir, run.Session)

	return b.At
}

func idleWhile(run *state.Run, skill string) string {
	if skill == "catchup" {
		return "and its catchup has not reported"
	}

	return "while its run says " + skill + " " + run.PhaseText(skill)
}

// poll reads one task's turn. Herdr's `done` lasts from the previous turn,
// so only working or a run report proves a turn began; idle proves no end
// either, since a turn parked on a background task reads idle too.
func (in *driveInput) poll(
	ctx context.Context,
	r *driveRun,
) error {
	id, skill := r.task.ref.ID, r.skill

	switch r.phase {
	case atWaiting, atParked, atQueued:
		return in.next(ctx, r)
	case atVerifying:
		job := r.job
		if !job.finished() {
			return nil
		}

		r.job = nil
		in.enter(r, atRunning)

		return in.verified(ctx, r, job)
	case atRetrying:
		in.enter(r, atRunning)
		r.retry = true

		return in.send(ctx, r, r.skill)
	case atPending, atHeld, atRunning:
	}

	ws := workspaceByID(in.view().wss, r.o.ws.ID)
	if ws == nil {
		return in.workspaceGone(r)
	}

	run, _ := in.run(r.o.path)
	run = r.current(run)

	now := in.now()
	since := r.sentAt.Truncate(time.Second) // the state file keeps whole seconds
	recent := run != nil && !run.Updated.Before(since)

	switch ws.Status {
	case agentctl.StatusWorking:
		r.begun, r.idleSince = true, time.Time{}

		return nil
	case agentctl.StatusIdle, agentctl.StatusDone:
	default:
		// Herdr shows no agent yet: the start window bounds this wait too
		if !r.begun && now.Sub(r.sentAt) > startWindow {
			return halt(id, skill, fmt.Sprintf("workspace %s shows no agent %s after the prompt: "+
				"check its pane, then rerun this drive", workspaceName(ws), startWindow))
		}

		return nil
	}

	// a prompt mark proves a turn the looks missed: one that began and ended
	// between two of them
	if !r.begun && !recent {
		if p, ok := state.LastPrompt(in.stateDir, r.o.path); ok && !p.At.Before(since) {
			if now.Sub(p.At) < promptSettle {
				return nil
			}

			r.begun = true
		}
	}

	if !r.begun && !recent {
		in.retryEnter(ctx, r)

		if now.Sub(r.sentAt) > startWindow {
			// a prompt never taken may sit under a startup dialog, so it is not typed again
			return in.settle(ctx, r, run, halted(id, skill, fmt.Sprintf(
				"the agent in workspace %s did not take the prompt within %s", workspaceName(ws), startWindow)), false)
		}

		return nil
	}

	r.begun = true

	// catchup has no phase, so only its done signal names the turn as one
	if r.attached && skill != "catchup" && run != nil && run.Done == "catchup" && run.DoneAt.After(since) {
		fmt.Fprintf(in.out, "%s %s: the turn was a catchup\n", in.id(id), skill)

		r.skill, skill = "catchup", "catchup"
	}

	if recent && stoppedRun(run) {
		switch {
		case skill == "ship" && run.Wait != "":
			return in.park(ctx, r, run)
		case skill == "catchup" && heldStop(run):
			// a catchup that stopped and then ended clean restores the stop it
			// found, so git says how it ended
		case skill == "ship" && guardStop(run) && resumeAt(skill, run) != "" && in.marked(ctx, r):
			// the stop the guard leaves names the catchup ship resumes from; a
			// mark on a ship that failed elsewhere is the watcher's, not the guard's
			if err := in.guardAgain(ctx, r); err != nil {
				return err
			}

			fmt.Fprintf(in.out, "%s ship: the catchup guard conflicted, catching up\n", in.id(id))

			return in.next(ctx, r)
		case run.Ask != "" || blockedRun(run):
			// blocked is a decision, a judge's stop or a stale task's verdict:
			// sending the skill again would roll it again
			return endWith(in.verdict(r.task, skill, run))
		default:
			// the run may be a turn the drive did not send, as a ship typed by
			// hand while it followed plan: the branch says how far it got
			return in.settle(ctx, r, run, in.verdict(r.task, skill, run), true)
		}
	}

	if unreported(run, skill, since) {
		// the grace counts from when background work stops showing, not from
		// the turn's end
		bg := backgroundAt(in.stateDir, run)
		if r.idleSince.IsZero() || backgroundPending(bg, now) {
			r.idleSince = now
		}

		// a turn that ends without its report is read from the branch; one the
		// branch does not get past halts, since the agent may wait on a
		// permission dialog that typing the skill again could answer
		if stalled(ws.Status, run, skill, since, now.Sub(r.idleSince), stallGrace, bg, now) {
			r.idleSince = time.Time{}

			halt := halted(id, skill, fmt.Sprintf("agent %s in workspace %s is idle %s: "+
				"it stopped without reporting, answer it in that pane", agentctl.AgentName(id, r.task.suffix, r.o.path),
				workspaceName(ws), idleWhile(run, skill)))

			if skill == "catchup" {
				return in.catchupStalled(ctx, r, run, halt)
			}

			return in.settle(ctx, r, run, halt, false)
		}

		return nil
	}

	fmt.Fprintf(in.out, "%s %s: done\n", in.id(id), skill)

	if skill == "catchup" {
		return in.catchupSettled(ctx, r, true)
	}

	if err := in.advance(ctx, r, skill); err != nil {
		return err
	}

	return in.next(ctx, r)
}

// guardAgain halts a ship whose guard conflicted the same way three times
// after the drive's catchups, or nil to catch up again; a mark git cannot
// read is left to next, which halts.
func (in *driveInput) guardAgain(ctx context.Context, r *driveRun) error {
	key, err := in.mark(ctx, r)
	if err != nil || key == "" {
		return nil //nolint:nilerr // next reads the mark again and halts on it
	}

	return in.conflictAttempt(ctx, r, "ship", key)
}

// marked counts a mark git cannot read as marked: the catchup it sends
// cannot settle, and names the read when it halts.
func (in *driveInput) marked(ctx context.Context, r *driveRun) bool {
	key, err := in.mark(ctx, r)

	return key != "" || err != nil
}

func (in *driveInput) mark(ctx context.Context, r *driveRun) (branchmeta.Key, error) {
	return stack.MarkOn(ctx, in.main, r.o.branch)
}

// verdict reads a stopped run: an ask goes to the user, anything else is the
// halt it gives, at once on a rerun, or once its attempts run out in a poll.
func (*driveInput) verdict(task driveTask, skill string, run *state.Run) driveResult {
	id := task.ref.ID

	if run.Ask != "" {
		return driveResult{
			row:   "asked at " + skill,
			code:  ui.ExitAsk,
			lines: []string{fmt.Sprintf("%s asks at %s: %s", id, skill, run.Ask)},
			ask:   run.Ask,
		}
	}

	reason := run.Note
	if p, ok := run.StoppedPhase(); reason == "" && ok {
		reason = "the run says " + p + " " + run.PhaseText(p)
	}

	res := halted(id, skill, reason)
	res.fromRun = true

	if run.Next != "" {
		res.lines = append(res.lines, "next: "+run.Next)
	}

	return res
}
