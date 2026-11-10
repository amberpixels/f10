package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/state"
)

// blockedRun reports a phase of run stopped blocked: a decision, never breakage.
func blockedRun(run *state.Run) bool {
	return slices.ContainsFunc(state.Phases, func(p string) bool { return run.Status(p) == "blocked" })
}

// guardStop reports the stop ship's catchup guard left, by the kind it
// recorded: failed or partial, marked as the guard's.
func guardStop(run *state.Run) bool {
	return run.Stop == state.StopGuard && (run.Ship == "failed" || run.Ship == "partial")
}

// awaitsDecision is a stop a catchup must not run over: a question or a
// blocked decision, not one a catchup leaves in place (heldStop).
func awaitsDecision(run *state.Run) bool {
	return run != nil && driverec.StoppedRun(run) && !heldStop(run) && (run.Ask != "" || blockedRun(run))
}

// heldStop is a ship stop a catchup leaves in place: the guard's, or a
// ship parked on its base, which resumes once the catchup ends clean.
func heldStop(run *state.Run) bool { return guardStop(run) || run.Wait != "" }

// phaseRunning reports run's plan or ship phase still running: the two
// phases a turn owns, so the turn has not reported its end.
func phaseRunning(run *state.Run, phase string) bool {
	return (phase == "plan" || phase == "ship") && run != nil && run.Status(phase) == "running"
}

// unreported reports a turn whose end the run has not told. A catchup is
// done only by a signal written after the prompt: both times are whole
// seconds, and no catchup reports within the second it was sent.
func unreported(run *state.Run, skill string, since time.Time) bool {
	if skill != "catchup" {
		return phaseRunning(run, skill)
	}

	return run == nil || run.Done != "catchup" || !run.DoneAt.After(since)
}

// phaseDone reports a run past skill: plan and ship own a phase, which
// reads done, or prior for a plan an earlier run wrote.
func phaseDone(run *state.Run, skill string) bool {
	if skill != "plan" && skill != "ship" {
		return false
	}

	s := run.Status(skill)

	return s == "done" || s == "prior"
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

	step := flow.Parse(entry).Step
	if !slices.Contains(flow.Guarded, step) || !slices.Contains(run.Pipeline, entry) {
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
