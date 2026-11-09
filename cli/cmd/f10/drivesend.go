package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/state"
)

// send runs one element in the task's agent; the poll reads how it ends.
func (in *driveInput) send(ctx context.Context, w io.Writer, r *driveRun, skill string) error {
	id := r.task.ref.ID

	ws, run, err := in.look(ctx, r)
	if err != nil {
		return err
	}

	run = r.current(run)
	r.skill, r.sentAt, r.begun, r.idleSince, r.attached = skill, in.now(), false, time.Time{}, false
	r.typed, r.enterRetried = "", false

	// a skill after the catchup may push the branch itself
	if c := r.lastCatchup; c != nil && skill != "catchup" {
		c.held = false
	}

	in.saveTask(r, func(t *drives.Task) { t.Skill, t.Outcome = skill, nil })

	answeredAt, answered := in.answeredIn[id]
	delete(in.answeredIn, id)

	if skill == "catchup" && in.catchup == nil && in.catchupWhy != "" && !r.toldCatchup {
		r.toldCatchup = true
		fmt.Fprintf(w, "%s catchup: %s, so the agent catches up\n", in.id(id), in.catchupWhy)
	}

	switch {
	case ws.Status == agentctl.StatusWorking:
		r.begun, r.attached = true, true

		// the turn began at its prompt: a catchup may have reported before now
		if p, ok := state.LastPrompt(in.stateDir, r.o.path); ok && !p.At.After(r.sentAt) {
			r.sentAt = p.At.Truncate(time.Second)
		}

		fmt.Fprintf(w, "%s %s: attached to the turn in progress\n", in.id(id), skill)
	case answered:
		// the agent heard the answer where it was given: its turn began
		// then, and the prompt mark the hook wrote just before the claim
		// may fall in the second before
		r.sentAt = answeredAt.Add(-time.Second).Truncate(time.Second)

		fmt.Fprintf(w, "%s %s: answered outside this drive, following the turn\n", in.id(id), skill)
	case skill == "catchup" && in.catchup != nil:
		// git integrates and verify runs here; the agent gets only what they
		// could not settle, a resend included
		return in.catchupHere(ctx, w, r, ws, run)
	case r.retry:
		// an attempt failed: the action goes again, whatever the run says
		return in.prompt(ctx, w, r, ws, skill, "")
	case run != nil && skill == "catchup" && heldStop(run):
		// the ship stop is the one this catchup settles
		return in.prompt(ctx, w, r, ws, skill, "")
	case run != nil && stoppedRun(run):
		answer := in.takeAnswer(id)
		if answer == "" {
			// a catchup clears a mark but restores the ship stop it noted, so
			// git, not the note, says whether that stop still holds
			if at := resumeAt(skill, run); at != "" && in.unsettled(ctx, r) == "" {
				why := "stopped for a catchup, the branch is settled"
				if run.Wait != "" {
					why = "its base has code now"
				}

				fmt.Fprintf(w, "%s %s: %s: %s\n", in.id(id), skill, why, resumeShown(run, at))

				return in.prompt(ctx, w, r, ws, skill, at)
			}

			in.end(ctx, w, r, in.verdict(r.task, skill, run))

			return nil
		}

		// closed before the prompt, so the pane hook does not read the
		// drive's own answer as one typed there
		in.claimSent(r, answer)

		if err := in.deliver(ctx, r, answer); err != nil {
			return err
		}

		in.took(id, skill, answer)
		fmt.Fprintf(w, "%s %s: answer sent\n", in.id(id), skill)
	case run != nil && phaseDone(run, skill):
		// the agent got there before a drive that stopped could see it
		fmt.Fprintf(w, "%s %s: already done\n", in.id(id), skill)

		if err := branchmeta.Set(ctx, in.main, r.o.branch, branchmeta.Drive, skill); err != nil {
			return fmt.Errorf("recording %s done: %w", skill, err)
		}

		r.pos++

		return in.next(ctx, w, r)
	default:
		return in.prompt(ctx, w, r, ws, skill, "")
	}

	return nil
}

// prompt sends the skill to an agent at rest, steering beside the task,
// unless the agent idles on a run still running.
func (in *driveInput) prompt(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	ws *agentctl.Workspace,
	skill, steering string,
) error {
	id := r.task.ref.ID

	r.retry = false

	// an idle agent on a run still running waits on something in its pane,
	// a permission dialog among others: text typed there could answer it
	fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: r.sentAt, since: r.freshAt}
	if err := refuseStalled(fwd, ws, agentctl.AgentName(id, r.task.suffix, r.o.path), r.o.path); err != nil {
		in.end(ctx, w, r, halted(id, skill, err.Error()))

		return nil
	}

	p := steered(skillPrompt(skill, r.task.ref.Label(), r.o.dep, r.flow, slices.Contains(r.chain, "finish")), steering)
	if err := in.deliver(ctx, r, p); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s: sent\n", in.id(id), skill)

	return nil
}

// deliver sends text to the task's agent, the first text starting it, and
// keeps the text when it was typed, for the poll's Enter check.
func (in *driveInput) deliver(ctx context.Context, r *driveRun, text string) error {
	name, typed, err := r.o.deliver(ctx, r.task.ref.ID, r.task.suffix, text)
	if err != nil {
		return err
	}

	if typed {
		r.typed, r.typedTo = text, name
	}

	return nil
}

// retryEnter checks, once and enterRetry after a typed send, that the text
// left the agent's input box, pressing Enter again if it did not. A failed
// check is printed, and the start window still decides.
func (in *driveInput) retryEnter(ctx context.Context, w io.Writer, r *driveRun) {
	if r.typed == "" || r.enterRetried || in.now().Sub(r.sentAt) < enterRetry {
		return
	}

	r.enterRetried = true

	if note := retryEnter(ctx, agents(r.o.path), r.typedTo, r.typed); note != "" {
		fmt.Fprintf(w, "%s %s: %s\n", in.id(r.task.ref.ID), r.skill, note)
	}
}

// resumeAt is the steering that resumes a ship the catchup guard stopped,
// or one parked on its base, naming its place when the flow names the entry
// twice; a failed catchup keeps the place of the entry it guarded, which is
// where ship resumes.
func resumeAt(skill string, run *state.Run) string {
	if skill != "ship" || run.Ask != "" || !state.Stopped(run.Ship) {
		return ""
	}

	entry := run.Leaf

	switch {
	case run.Wait != "":
		// a ship parked on its base resumes at the entry that found it empty
	case entry == "catchup" && run.At > 0 && run.At <= len(run.Pipeline):
		entry = run.Pipeline[run.At-1]
	case !strings.Contains(run.Next, "/f10:catchup"):
		return ""
	}

	step, _, _ := strings.Cut(entry, ":")
	if !slices.Contains(guardedSteps, step) || !slices.Contains(run.Pipeline, entry) {
		return ""
	}

	s := "resume at " + entry
	twice := slices.Contains(run.Pipeline[slices.Index(run.Pipeline, entry)+1:], entry)
	if run.At > 0 && twice {
		s += fmt.Sprintf(" (entry %d)", run.At)
	}

	return s
}

// resumeShown is resumeAt's steering as a person reads it: the resumed
// entry by its label, when the flow gives it one.
func resumeShown(run *state.Run, at string) string {
	if label := run.LabelAt(run.At); label != "" {
		return "resume at " + label
	}

	return at
}

// guardedSteps are the entries ship's catchup guard runs before.
var guardedSteps = []string{"implement", "pr", "judge"}

// steered puts steering after the skill's subject, before any chained
// clause and the driven marker, so the skill reads it as its own.
func steered(prompt, steering string) string {
	if steering == "" {
		return prompt
	}

	if i := strings.Index(prompt, " && "); i >= 0 {
		return prompt[:i] + " " + steering + prompt[i:]
	}

	if base, ok := strings.CutSuffix(prompt, " "+drivenMarker); ok {
		return base + " " + steering + " " + drivenMarker
	}

	return prompt + " " + steering
}
