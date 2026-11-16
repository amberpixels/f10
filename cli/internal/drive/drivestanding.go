package drive

import (
	"fmt"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
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

// guarded reports the guard's stop by the kind the run recorded, or by the
// drive's own record of it while the run still stops where the guard did; a
// stop that asks or blocks since, or one elsewhere, is never the guard's.
func guarded(run *state.Run, g *drivestore.Guard) bool {
	return guardStop(run) || g != nil && run.Ask == "" && (run.Ship == "failed" || run.Ship == "partial") &&
		run.Leaf == g.Entry && run.At == g.At
}

// awaitsDecision is a stop a catchup must not run over: a question or a
// blocked decision, not one a catchup leaves in place (heldStop).
func awaitsDecision(run *state.Run, g *drivestore.Guard) bool {
	return run != nil && drivectl.StoppedRun(run) && !heldStop(run, g) && (run.Ask != "" || blockedRun(run))
}

// heldStop is a ship stop a catchup leaves in place: the guard's, or a
// ship parked on its base, which resumes once the catchup ends clean.
func heldStop(run *state.Run, g *drivestore.Guard) bool { return guarded(run, g) || run.Wait != "" }

// phaseRunning reports run's plan or ship phase still running: the two
// phases a turn owns, so the turn has not reported its end.
func phaseRunning(run *state.Run, phase string) bool {
	return (phase == "plan" || phase == "ship") && run != nil && run.Status(phase) == "running"
}

// unreported reports a turn whose end the run has not told. A catchup is
// done only by a signal written after the prompt: both times are whole
// seconds, and no catchup reports within the second it was sent.
func unreported(run *state.Run, skill string, since time.Time) bool {
	if !phaseless(skill) {
		return phaseRunning(run, skill)
	}

	return run == nil || run.Done != skill || !run.DoneAt.After(since)
}

// phaseless reports a turn with no phase of its own, catchup or a ci
// triage, which ends on its done signal.
func phaseless(skill string) bool {
	return skill == "catchup" || skill == "ci"
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
// where ship resumes. g, the drive's record of the guard's stop, outranks
// the run's place, which a catchup's turn may have rewritten.
func resumeAt(skill string, run *state.Run, g *drivestore.Guard) string {
	if skill != "ship" || run.Ask != "" || !state.Stopped(run.Ship) {
		return ""
	}

	entry, at := run.Leaf, run.At

	switch {
	case run.Wait != "":
		// a ship parked on its base resumes at the entry that found it empty
	case g != nil && guarded(run, g):
		entry, at = g.Entry, g.At
	case entry == "catchup" && run.At > 0 && run.At <= len(run.Pipeline):
		entry = run.Pipeline[run.At-1]
	case !guardStop(run):
		return "" // only the guard's typed kind says so, never the wording of next
	}

	step := flow.Parse(entry).Step
	if !slices.Contains(flow.Guarded, step) || !slices.Contains(run.Pipeline, entry) {
		return ""
	}

	s := "resume at " + entry
	twice := slices.Contains(run.Pipeline[slices.Index(run.Pipeline, entry)+1:], entry)
	if at > 0 && twice {
		s += fmt.Sprintf(" (entry %d)", at)
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
