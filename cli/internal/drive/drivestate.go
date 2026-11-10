package drive

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	statuscmd "github.com/amberpixels/f10/cli/internal/command/status"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
)

// AttemptLimit is how many times in a row one action may fail with the
// task's state unchanged before the drive halts it.
var AttemptLimit = 3

// A TaskState is where a task stands by git, its PR and its files, read only
// where the drive reconciles a turn, never on every poll, since the PR read
// is a host call.
type TaskState struct {
	Head, Origin string
	Code         bool           // the branch has commits of its own past the default branch
	mark         branchmeta.Key // the catchup mark on the branch, "" for none
	busy         string         // a merge or rebase in progress, or a dirty tree; "" for none
	plan         bool
	Pr           string // the open PR's number, "" for none or no host to ask
	Reviews      int    // review rounds written for the change
	ChangeJudged bool
	Run          *state.Run
}

// Fingerprint is the state an attempt compares: the agent's status and every
// timestamp stay out, since they change on each turn that makes no progress.
func (s TaskState) Fingerprint() string {
	parts := []string{
		s.Head, s.Origin, strconv.FormatBool(s.Code), string(s.mark), s.busy, strconv.FormatBool(s.plan), s.Pr,
		strconv.Itoa(s.Reviews), strconv.FormatBool(s.ChangeJudged),
	}

	if r := s.Run; r != nil {
		parts = append(parts, r.Plan, r.Ship, r.Leaf, strconv.Itoa(r.At), r.Wait)
	}

	return strings.Join(parts, "|")
}

// Has reports the evidence that a skill is done. finish is the drive's own
// step, never read from the branch, and any other skill only
// from its run's phase.
func (s TaskState) Has(skill string) bool {
	if s.stopped(skill) {
		return false
	}

	switch skill {
	case "plan":
		return s.plan || s.Run != nil && phaseDone(s.Run, "plan")
	case "ship":
		return s.Run != nil && phaseDone(s.Run, "ship") || s.Shipped()
	}

	return false
}

// Shipped reads the shipment ship leaves from the branch: its last delivering
// entry's output, and an artifact from every review and change judge on the
// way, so a ship stopped between its first pr and its last is not shipped.
// With no pipeline recorded, an open PR carrying the branch stands for it.
func (s TaskState) Shipped() bool {
	var pipeline flow.Flow
	if s.Run != nil {
		pipeline = s.Run.Entries()
	}

	deliver := cmp.Or(pipeline.Delivered(), "pr")
	implemented := false

	for _, e := range pipeline {
		switch e.Step {
		case "implement":
			implemented = true
		case "review":
			if s.Reviews == 0 {
				return false
			}
		case "judge":
			if implemented && !s.ChangeJudged {
				return false
			}
		}
	}

	// a tree mid-build or mid-merge is not what the PR carries
	if s.busy != "" {
		return false
	}

	pushed := s.Code && s.Origin != "" && s.Origin == s.Head

	switch deliver {
	case "pr":
		return s.Pr != "" && pushed
	case "push":
		return pushed
	case "commit":
		return s.Code
	}

	// implement leaves nothing on the branch, and a deploy nothing it can read
	return false
}

// stopped says the current run reports phase stopped without an ask: an
// explicit stop is a failed attempt, whatever the branch shows.
func (s TaskState) stopped(phase string) bool {
	return s.Run != nil && s.Run.Ask == "" && state.Stopped(s.Run.Status(phase))
}

// readState reads the task's state. A read git cannot answer leaves its
// field empty: the state then shows less evidence, never more.
func (in *DriveInput) readState(ctx context.Context, r *DriveRun, run *state.Run) TaskState {
	branch := r.O.Branch
	s := TaskState{Run: run}

	s.Head, _ = gitx.Ref(ctx, in.Main, "refs/heads/"+branch)
	s.Origin, _ = gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+branch)
	s.mark, _ = stack.MarkOn(ctx, in.Main, branch)
	s.busy = stack.TreeBusy(ctx, r.O.Path)

	if def, err := stack.LocalDefault(ctx, in.Main); err == nil {
		s.Code = stack.BaseHasCode(ctx, in.Main, branch, def)
	}

	lead := r.Task.Ref.Head().ID
	s.plan = r.O.Plan != "" || fsx.Exists(in.Storage.Plan(lead))

	o := statuscmd.ReadOutcomes(nil, in.Storage.Path(), lead)
	s.Reviews, s.ChangeJudged = len(o.Local)+len(o.Remote), o.ChangeJudge != nil

	if in.Pull != nil && s.Origin != "" {
		if p, err := in.Pull(ctx, branch); err == nil && p.Status == "open" {
			s.Pr = strconv.Itoa(p.Number)
		}
	}

	return s
}

// reconcile moves the task past every skill whose evidence is in,
// recording each as drive does a reported one, and says how many it passed.
func (in *DriveInput) reconcile(ctx context.Context, r *DriveRun, s TaskState) (int, error) {
	passed := 0

	for skills := in.Skills(r); r.Pos < len(skills) && s.Has(skills[r.Pos]); {
		skill := skills[r.Pos]
		if err := in.advance(ctx, r, skill); err != nil {
			return passed, err
		}

		fmt.Fprintf(in.Out, "%s %s: done (from the branch)\n", in.id(r.Task.Ref.ID), skill)

		passed++
	}

	return passed, nil
}

// advance records skill done on the branch and moves the task to its next
// skill; the task moved, so the attempt count starts over.
func (in *DriveInput) advance(ctx context.Context, r *DriveRun, skill string) error {
	if err := branchmeta.Set(ctx, in.Main, r.O.Branch, branchmeta.Drive, skill); err != nil {
		return fmt.Errorf("recording %s done: %w", skill, err)
	}

	r.Pos++
	in.clearAttempts(r)

	return nil
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
func (in *DriveInput) attempt(
	ctx context.Context,
	r *DriveRun,
	action string,
	halt DriveResult,
) error {
	run, _ := in.run(r.O.Path) // a run the state dir cannot give leaves the run out

	return in.AttemptAt(r, action, in.readState(ctx, r, r.current(run)).Fingerprint(), halt)
}

// AttemptAt is attempt with the state to compare given: a catchup's is the
// conflict it left, since a catchup that merges moves the head without
// progress, and the reason it gives changes wording between catchups.
func (in *DriveInput) AttemptAt(r *DriveRun, action, fp string, halt DriveResult) error {
	reason := halt.Reason
	restarted := false

	if n := len(r.attempts); n > 0 && (r.attempts[n-1].Action != action || r.attempts[n-1].State != fp) {
		restarted = r.attempts[n-1].Action == action
		r.attempts = nil
	}

	r.attempts = append(r.attempts, attempt{Action: action, State: fp, Reason: reason, At: in.Now()})

	if len(r.attempts) < AttemptLimit {
		// the count is of failures in a row with nothing changed, never of
		// turns, so a restart says why it reads 1 again
		if restarted {
			fmt.Fprintf(in.Out, "%s %s: failed again, but the task moved since the last failure, "+
				"so the count starts over at 1 of %d, trying again: %s\n",
				in.id(r.Task.Ref.ID), action, AttemptLimit, reason)
		} else {
			fmt.Fprintf(in.Out, "%s %s: failed, %d of %d in a row with nothing changed, trying again: %s\n",
				in.id(r.Task.Ref.ID), action, len(r.attempts), AttemptLimit, reason)
		}

		return nil
	}

	res := halt
	if len(r.attempts) > 1 {
		// the count is the drive's own word on the halt, beside any the run gave
		res.fromRun = false

		res.Lines = append(
			res.Lines,
			fmt.Sprintf("  %s failed %d times in a row with nothing changed:", action, len(r.attempts)),
		)

		for i, a := range r.attempts {
			res.Lines = append(
				res.Lines,
				fmt.Sprintf("  attempt %d at %s: %s", i+1, a.At.Format(time.TimeOnly), a.Reason),
			)
		}
	}

	res.Attempts = slices.Clone(r.attempts)

	return endWith(res)
}

// failed is a turn that ended without doing its action: the next poll sends
// the action again, or the task halts on the limit with halt.
func (in *DriveInput) failed(ctx context.Context, r *DriveRun, halt DriveResult) error {
	if err := in.attempt(ctx, r, r.Skill, halt); err != nil {
		return err
	}

	in.Enter(r, atRetrying)

	return nil
}

// clearAttempts starts the count over: the task moved.
func (*DriveInput) clearAttempts(r *DriveRun) {
	r.attempts = nil
}

// settle reads the state at a turn's end its run did not explain: a skill
// the branch shows done moves the task on, anything else is a failed
// attempt of the action in flight, or with retry false, halt at once.
func (in *DriveInput) settle(
	ctx context.Context,
	r *DriveRun,
	run *state.Run,
	halt DriveResult,
	retry bool,
) error {
	// git, not the turn, says whether the drive's own catchup settled
	if r.Skill == "catchup" {
		return in.catchupSettled(ctx, r, retry)
	}

	if r.Skill != "catchup" {
		passed, err := in.reconcile(ctx, r, in.readState(ctx, r, run))
		if err != nil {
			return err
		}

		if passed > 0 {
			return in.Next(ctx, r)
		}
	}

	if !retry {
		return endWith(halt)
	}

	return in.failed(ctx, r, halt)
}
