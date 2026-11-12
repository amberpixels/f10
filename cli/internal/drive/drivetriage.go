package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
)

// endAfterChecks ends a task whose skills are through. A drive that does
// not finish holds a shipped PR until its checks settle, up to
// ChecksBudget, so a head that turns red is triaged before the task ends.
func (in *DriveInput) endAfterChecks(ctx context.Context, r *DriveRun, skills []string) error {
	done := driveResult{Row: "done: " + strings.Join(skills, " ")}

	if r.Finishes || in.Checks == nil || len(skills) == 0 || skills[len(skills)-1] != "ship" {
		return endWith(done)
	}

	hc, url, err := in.Checks(ctx, r.O.Branch)
	if err != nil {
		done.Lines = append(done.Lines, r.Task.Ref.ID+" checks not read: "+err.Error())

		return endWith(done)
	}

	if err := branchmeta.ClearOnGreen(ctx, in.Main, r.O.Branch, hc.State == forge.Green); err != nil {
		done.Lines = append(done.Lines, r.Task.Ref.ID+" triage record not cleared: "+err.Error())
	}

	// a head CI has not registered yet reads as none: it waits a short
	// grace, so a repo with no CI is not held for the whole budget
	waits := hc.State == forge.Pending || (hc.State == "" && url != "" && !r.noChecksPast(in.Now(), hc.SHA))

	switch {
	case url == "" || (hc.State != forge.Red && !waits):
		r.checks = nil

		return endWith(done)
	case hc.State == forge.Red:
		return in.triage(ctx, r, finish.ChecksError{URL: url, Checks: hc}, "")
	}

	if r.checks == nil || r.checks.sha != hc.SHA {
		r.checks = &checksHold{sha: hc.SHA, since: in.Now()}
		fmt.Fprintf(in.Out, "%s ship: waiting on checks (%s)\n", in.id(r.Task.Ref.ID), forge.Names(hc.Running))
	}

	if in.Now().Sub(r.checks.since) >= finish.ChecksBudget {
		r.checks = nil
		done.Row += "; checks still running"

		return endWith(done)
	}

	in.enter(r, atWaiting)
	in.saveTask(r, func(t *drivestore.Task) { t.Checks = hc.SHA })

	return nil
}

// checksAppearGrace is how long a head with no checks is held after ship,
// for CI to register it, before the drive reads it as having none.
const checksAppearGrace = 2 * time.Minute

// noChecksPast reports a head held with no checks for longer than the grace.
func (r *DriveRun) noChecksPast(now time.Time, sha string) bool {
	return r.checks != nil && r.checks.sha == sha && now.Sub(r.checks.since) >= checksAppearGrace
}

// triage sends a red head to the task's agent as /f10:ci. The step decides
// whether to fix, rerun or ask, so the drive only checks that a triage
// moved something: the same red head with the same record halts.
func (in *DriveInput) triage(ctx context.Context, r *DriveRun, ce finish.ChecksError, answer string) error {
	id := r.Task.Ref.ID

	// a hold with no head keeps finish's answer across the triage, and any
	// pending head after it starts a fresh budget
	r.checks = &checksHold{answer: answer}

	tr, err := branchmeta.ReadTriage(ctx, in.Main, r.O.Branch)
	if err != nil {
		return halt(id, "finish", err.Error())
	}

	key := ce.Checks.SHA + " " + tr.Format()
	if r.triaged == key {
		r.triaged, r.checks = "", nil

		return halt(id, "ci", ce.Error()+"\nthe triage left this head red with nothing new tried")
	}

	r.triaged = key

	ws, _, err := in.look(ctx, r)
	if err != nil {
		return err
	}

	at := "ship"
	if r.Finishes {
		at = "finish"
		in.finishStep(ctx, r.O.Branch, branchmeta.FinishQueued, "triaging checks")
	}

	fmt.Fprintf(in.Out, "%s %s: checks red (%s), triaging\n", in.id(id), at, forge.Names(ce.Checks.Failed))

	r.turn = turn{Skill: "ci", SentAt: in.Now()}
	in.saveTask(r, func(t *drivestore.Task) { t.Skill, t.Outcome = "ci", nil })
	in.enter(r, atRunning)

	return in.prompt(ctx, r, ws, "ci", "")
}
