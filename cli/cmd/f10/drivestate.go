package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/state"
)

// attemptLimit is how many times in a row one action may fail with the
// task's state unchanged before the drive halts it.
var attemptLimit = 3

// A taskState is where a task stands by git, its PR and its files, read only
// where the drive reconciles a turn, never on every poll, since the PR read
// is a host call.
type taskState struct {
	head, origin string
	code         bool           // the branch has commits of its own past the default branch
	mark         branchmeta.Key // the catchup mark on the branch, "" for none
	busy         string         // a merge or rebase in progress, or a dirty tree; "" for none
	plan         bool
	pr           string // the open PR's number, "" for none or no host to ask
	reviews      int    // review rounds written for the change
	changeJudged bool
	run          *state.Run
}

// readState reads the task's state. A read git cannot answer leaves its
// field empty: the state then shows less evidence, never more.
func (in *driveInput) readState(ctx context.Context, r *driveRun, run *state.Run) taskState {
	branch := r.o.branch
	s := taskState{run: run}

	s.head, _ = gitx.Ref(ctx, in.main, "refs/heads/"+branch)
	s.origin, _ = gitx.Ref(ctx, in.main, "refs/remotes/origin/"+branch)
	s.mark, _ = markOn(ctx, in.main, branch)
	s.busy = treeBusy(ctx, r.o.path)

	if def, err := localDefault(ctx, in.main); err == nil {
		s.code = baseHasCode(ctx, in.main, branch, def)
	}

	lead := r.task.ref.Head().ID
	s.plan = r.o.plan != "" || fsx.Exists(in.storage.Plan(lead))

	o := readOutcomes(nil, in.storage.Path(), lead)
	s.reviews, s.changeJudged = len(o.local)+len(o.remote), o.changeJudge != nil

	if in.pull != nil && s.origin != "" {
		if p, err := in.pull(ctx, branch); err == nil && p.Status == "open" {
			s.pr = strconv.Itoa(p.Number)
		}
	}

	return s
}

// fingerprint is the state an attempt compares: the agent's status and every
// timestamp stay out, since they change on each turn that makes no progress.
func (s taskState) fingerprint() string {
	parts := []string{
		s.head, s.origin, strconv.FormatBool(s.code), string(s.mark), s.busy, strconv.FormatBool(s.plan), s.pr,
		strconv.Itoa(s.reviews), strconv.FormatBool(s.changeJudged),
	}

	if r := s.run; r != nil {
		parts = append(parts, r.Plan, r.Ship, r.Leaf, strconv.Itoa(r.At), r.Wait)
	}

	return strings.Join(parts, "|")
}

// stopped says the current run reports phase stopped without an ask: an
// explicit stop is a failed attempt, whatever the branch shows.
func (s taskState) stopped(phase string) bool {
	return s.run != nil && s.run.Ask == "" && state.Stopped(s.run.Status(phase))
}

// has reports the evidence that a skill is done. finish is the drive's own
// step, never read from the branch, and any other skill only
// from its run's phase.
func (s taskState) has(skill string) bool {
	if s.stopped(skill) {
		return false
	}

	switch skill {
	case "plan":
		return s.plan || s.run != nil && phaseDone(s.run, "plan")
	case "ship":
		return s.run != nil && phaseDone(s.run, "ship") || s.shipped()
	}

	return false
}

// shipped reads the shipment ship leaves from the branch: its last delivering
// entry's output, and an artifact from every review and change judge on the
// way, so a ship stopped between its first pr and its last is not shipped.
// With no pipeline recorded, an open PR carrying the branch stands for it.
func (s taskState) shipped() bool {
	var pipeline flow.Flow
	if s.run != nil {
		pipeline = s.run.Entries()
	}

	deliver := cmp.Or(pipeline.Delivered(), "pr")
	implemented := false

	for _, e := range pipeline {
		switch e.Step {
		case "implement":
			implemented = true
		case "review":
			if s.reviews == 0 {
				return false
			}
		case "judge":
			if implemented && !s.changeJudged {
				return false
			}
		}
	}

	// a tree mid-build or mid-merge is not what the PR carries
	if s.busy != "" {
		return false
	}

	pushed := s.code && s.origin != "" && s.origin == s.head

	switch deliver {
	case "pr":
		return s.pr != "" && pushed
	case "push":
		return pushed
	case "commit":
		return s.code
	}

	// implement leaves nothing on the branch, and a deploy nothing it can read
	return false
}

// reconcile moves the task past every skill whose evidence is in,
// recording each as drive does a reported one, and says how many it passed.
func (in *driveInput) reconcile(ctx context.Context, w io.Writer, r *driveRun, s taskState) (int, error) {
	passed := 0

	for skills := in.skills(r); r.pos < len(skills) && s.has(skills[r.pos]); {
		skill := skills[r.pos]
		if err := branchmeta.Set(ctx, in.main, r.o.branch, branchmeta.Drive, skill); err != nil {
			return passed, fmt.Errorf("recording %s done: %w", skill, err)
		}

		fmt.Fprintf(w, "%s %s: done (from the branch)\n", in.id(r.task.ref.ID), skill)

		r.pos++
		passed++
	}

	if passed > 0 {
		in.clearAttempts(r)
	}

	return passed, nil
}

// An attempt is one failure of an action, with the state it left.
type attempt struct {
	Action, State, Reason string
	At                    time.Time
}

// attempt records a failure of action, halt being the end it gives, and
// halts the task once attemptLimit failures in a row left the state
// unchanged; nil means try again. A failure after which the state moved
// starts the count over, since the task moved.
func (in *driveInput) attempt(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	action string,
	halt driveResult,
) *driveResult {
	run, _ := in.run(r.o.path) // a run the state dir cannot give leaves the run out

	return in.attemptAt(w, r, action, in.readState(ctx, r, r.current(run)).fingerprint(), halt)
}

// attemptAt is attempt with the state to compare given: a catchup's is the
// conflict it left, since a catchup that merges moves the head without
// progress, and the reason it gives changes wording between catchups.
func (in *driveInput) attemptAt(w io.Writer, r *driveRun, action, fp string, halt driveResult) *driveResult {
	reason := halt.reason
	restarted := false

	if n := len(r.attempts); n > 0 && (r.attempts[n-1].Action != action || r.attempts[n-1].State != fp) {
		restarted = r.attempts[n-1].Action == action
		r.attempts = nil
	}

	r.attempts = append(r.attempts, attempt{Action: action, State: fp, Reason: reason, At: in.now()})

	if len(r.attempts) < attemptLimit {
		// the count is of failures in a row with nothing changed, never of
		// turns, so a restart says why it reads 1 again
		if restarted {
			fmt.Fprintf(w, "%s %s: failed again, but the task moved since the last failure, "+
				"so the count starts over at 1 of %d, trying again: %s\n",
				in.id(r.task.ref.ID), action, attemptLimit, reason)
		} else {
			fmt.Fprintf(w, "%s %s: failed, %d of %d in a row with nothing changed, trying again: %s\n",
				in.id(r.task.ref.ID), action, len(r.attempts), attemptLimit, reason)
		}

		return nil
	}

	res := halt
	if len(r.attempts) > 1 {
		// the count is the drive's own word on the halt, beside any the run gave
		res.fromRun = false

		res.lines = append(
			res.lines,
			fmt.Sprintf("  %s failed %d times in a row with nothing changed:", action, len(r.attempts)),
		)

		for i, a := range r.attempts {
			res.lines = append(
				res.lines,
				fmt.Sprintf("  attempt %d at %s: %s", i+1, a.At.Format(time.TimeOnly), a.Reason),
			)
		}
	}

	res.attempts = slices.Clone(r.attempts)

	return &res
}

// failed is a turn that ended without doing its action: the next poll sends
// the action again, or the task halts on the limit with halt.
func (in *driveInput) failed(ctx context.Context, w io.Writer, r *driveRun, halt driveResult) {
	if res := in.attempt(ctx, w, r, r.skill, halt); res != nil {
		in.end(ctx, w, r, *res)

		return
	}

	r.resend = true
}

// clearAttempts starts the count over: the task moved.
func (*driveInput) clearAttempts(r *driveRun) {
	r.attempts = nil
}

// settle reads the state at a turn's end its run did not explain: a skill
// the branch shows done moves the task on, anything else is a failed
// attempt of the action in flight, or with retry false, halt at once.
func (in *driveInput) settle(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	run *state.Run,
	halt driveResult,
	retry bool,
) error {
	// git, not the turn, says whether the drive's own catchup settled
	if r.skill == "catchup" {
		return in.catchupSettled(ctx, w, r, retry)
	}

	if r.skill != "catchup" {
		passed, err := in.reconcile(ctx, w, r, in.readState(ctx, r, run))
		if err != nil {
			return err
		}

		if passed > 0 {
			return in.next(ctx, w, r)
		}
	}

	if !retry {
		in.end(ctx, w, r, halt)

		return nil
	}

	in.failed(ctx, w, r, halt)

	return nil
}
