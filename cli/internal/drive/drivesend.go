package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// send runs one element in the task's agent; the poll reads how it ends.
func (in *DriveInput) send(ctx context.Context, r *DriveRun, skill string) error {
	id := r.Task.Ref.ID

	ws, run, err := in.Look(ctx, r)
	if err != nil {
		return err
	}

	run = r.current(run)
	r.turn = turn{Skill: skill, SentAt: in.Now(), retry: r.retry}

	// a skill after the catchup may push the branch itself
	if c := r.LastCatchup; c != nil && skill != "catchup" {
		c.Spent = true
	}

	in.saveTask(r, func(t *drives.Task) { t.Skill, t.Outcome = skill, nil })
	in.KeepCatchup(r)

	answeredAt, answered := in.Answers.Heard(id)

	if skill == "catchup" && in.Catchup.AgentVerifies != "" && !r.toldCatchup {
		r.toldCatchup = true
		fmt.Fprintf(in.Out, "%s catchup: %s, so the agent verifies\n", in.id(id), in.Catchup.AgentVerifies)
	}

	switch {
	case ws.Status == agentctl.StatusWorking:
		r.begun, r.attached = true, true

		// the turn began at its prompt: a catchup may have reported before now
		if p, ok := state.LastPrompt(in.StateDir, r.O.Path); ok && !p.At.After(r.SentAt) {
			r.SentAt = p.At.Truncate(time.Second)
		}

		fmt.Fprintf(in.Out, "%s %s: attached to the turn in progress\n", in.id(id), skill)
	case answered:
		// the agent heard the answer where it was given: its turn began
		// then, and the prompt mark the hook wrote just before the claim
		// may fall in the second before
		r.SentAt = answeredAt.Add(-time.Second).Truncate(time.Second)

		fmt.Fprintf(in.Out, "%s %s: answered outside this drive, following the turn\n", in.id(id), skill)
	case skill == "catchup" && !awaitsDecision(run):
		// git integrates and verify runs here; the agent gets only what they
		// could not settle, a resend included
		return in.catchupHere(ctx, r, ws, run)
	case r.retry:
		// an attempt failed: the action goes again, whatever the run says
		return in.prompt(ctx, r, ws, skill, "")
	case run != nil && driverec.StoppedRun(run):
		answer := in.takeAnswer(id)
		if answer == "" {
			// a catchup clears a mark but restores the ship stop it noted, so
			// git, not the note, says whether that stop still holds
			if at := ResumeAt(skill, run); at != "" && stack.BranchUnsettled(ctx, in.Main, r.O.Path, r.O.Branch) == "" {
				why := "stopped for a catchup, the branch is settled"
				switch {
				case OriginWait(run):
					why = "its base is on origin now"
				case run.Wait != "":
					why = "its base has code now"
				}

				fmt.Fprintf(in.Out, "%s %s: %s: %s\n", in.id(id), skill, why, ResumeShown(run, at))

				return in.prompt(ctx, r, ws, skill, at)
			}

			return endWith(in.verdict(r.Task, skill, run))
		}

		// closed before the prompt, so the pane hook does not read the
		// drive's own answer as one typed there
		in.claimSent(r, answer)

		if err := in.deliver(ctx, r, answer); err != nil {
			return err
		}

		in.took(id, skill, answer)
		fmt.Fprintf(in.Out, "%s %s: answer sent\n", in.id(id), skill)
	case run != nil && phaseDone(run, skill):
		// the agent got there before a drive that stopped could see it
		fmt.Fprintf(in.Out, "%s %s: already done\n", in.id(id), skill)

		if err := in.advance(ctx, r, skill); err != nil {
			return err
		}

		return in.Next(ctx, r)
	default:
		return in.prompt(ctx, r, ws, skill, "")
	}

	return nil
}

// prompt sends the skill to an agent at rest, steering beside the task,
// unless the agent idles on a run still running.
func (in *DriveInput) prompt(
	ctx context.Context,
	r *DriveRun,
	ws *agentctl.Workspace,
	skill, steering string,
) error {
	id := r.Task.Ref.ID

	r.retry = false

	// an idle agent on a run still running waits on something in its pane,
	// a permission dialog among others: text typed there could answer it
	if err := start.RefuseStalled(
		in.stallCheck(r),
		ws,
		agentctl.AgentName(id, r.Task.Suffix, r.O.Path),
		r.O.Path,
	); err != nil {
		return Halt(id, skill, err.Error())
	}

	p := Steered(SkillPrompt(skill, r.Task.Ref.Label(), r.O.Dep, r.Flow, r.Finishes), steering)
	if err := in.deliver(ctx, r, p); err != nil {
		return err
	}

	fmt.Fprintf(in.Out, "%s %s: sent\n", in.id(id), skill)

	return nil
}

// deliver sends text to the task's agent, the first text starting it, and
// keeps the text when it was typed, for the poll's Enter check.
func (in *DriveInput) deliver(ctx context.Context, r *DriveRun, text string) error {
	name, typed, err := r.O.Deliver(ctx, r.Task.Ref.ID, r.Task.Suffix, text)
	if err != nil {
		return err
	}

	if typed {
		r.Typed, r.typedTo = text, name
	}

	return nil
}

// RetryEnter checks, once and EnterRetry after a typed send, that the text
// left the agent's input box, pressing Enter again if it did not. A failed
// check is printed, and the start window still decides.
func (in *DriveInput) RetryEnter(ctx context.Context, r *DriveRun) {
	if r.Typed == "" || r.enterRetried || in.Now().Sub(r.SentAt) < launch.EnterRetry {
		return
	}

	r.enterRetried = true

	if note := launch.RetryEnter(ctx, worktree.Agents(r.O.Path), r.typedTo, r.Typed); note != "" {
		fmt.Fprintf(in.Out, "%s %s: %s\n", in.id(r.Task.Ref.ID), r.Skill, note)
	}
}

// Steered puts steering after the skill's subject, before any chained
// clause and the driven marker, so the skill reads it as its own.
func Steered(prompt, steering string) string {
	if steering == "" {
		return prompt
	}

	if i := strings.Index(prompt, " && "); i >= 0 {
		return prompt[:i] + " " + steering + prompt[i:]
	}

	if base, ok := strings.CutSuffix(prompt, " "+launch.DrivenMarker); ok {
		return base + " " + steering + " " + launch.DrivenMarker
	}

	return prompt + " " + steering
}
