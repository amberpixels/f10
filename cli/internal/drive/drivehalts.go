package drive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// LeaveRuns runs on every way out of the loop, drained and failed included:
// an interrupted drive leaves no finish record claiming it still runs, and
// no catchup verify running in its own process group.
func (in *DriveInput) LeaveRuns(ctx context.Context, runs []*DriveRun) {
	for _, r := range runs {
		if r.active() {
			in.parkFinish(ctx, r)
		}

		if r.Job != nil {
			r.Job.halt()
		}
	}
}

// End prints the result now, so a halt or an ask is on screen when it
// happens, and puts a halt in the ledger for status to read after the exit.
func (in *DriveInput) End(ctx context.Context, r *DriveRun, res driveResult) {
	r.End = &res

	// a cancelled or halted run takes its catchup's verify with it
	if r.Job != nil {
		r.Job.stop()
	}

	// a finished task's branch took the record with it
	if res.Row != drivectl.RowFinished {
		in.parkFinish(ctx, r)
	}

	in.saveTask(r, func(t *drivestore.Task) {
		t.Skill, t.Queued, t.Phase, t.Outcome = "", false, drivestore.PhaseEnded, &drivestore.Outcome{
			Row:  res.Row,
			Code: res.Code,
		}
	})
	// an asking task stays leased until the drive exits, since the drive
	// runs on and resumes for its answer; another drive must not claim it
	if res.Code != ui.ExitAsk {
		in.unlease(r, in.Runs)
	}

	if res.Code == ui.ExitAsk && res.ask != "" {
		in.raise(ctx, r, strings.TrimPrefix(res.Row, "asked at "), res.ask)
	}

	if res.Code == ui.ExitHalted && !res.fromRun && !res.Drives {
		in.recordHalt(ctx, r, res)
	}

	// a finished task's lines are its handoff, which finish's report just
	// printed; they wait for the drive's report
	if len(res.Lines) == 0 || res.Row == drivectl.RowFinished {
		fmt.Fprintf(in.Out, "%s: %s\n", in.id(r.Task.Ref.ID), res.Row)

		return
	}

	for _, l := range res.Lines {
		fmt.Fprintln(in.Out, l)
	}
}

func halted(id, skill, reason string) driveResult {
	return driveResult{
		Row:    "halted at " + skill,
		Code:   ui.ExitHalted,
		Lines:  []string{fmt.Sprintf("%s halted at %s: %s", id, skill, reason)},
		Skill:  skill,
		Reason: reason,
	}
}

// recordHalt puts a halt drive found itself in the ledger, placed in the
// task's worktree when it has one.
func (in *DriveInput) recordHalt(ctx context.Context, r *DriveRun, res driveResult) {
	if in.Record == nil {
		return
	}

	in.Record(ctx, cmp.Or(r.O.Path, in.Main), ledger.Event{
		At:      in.Now(),
		Task:    r.Task.Ref.ID,
		Kind:    ledger.TaskHalted,
		Source:  ledger.Observed,
		Payload: ledger.NewPayload(haltPayload(res)),
	})
}

// haltPayload is a halt's ledger payload: the attempts it counted, if any.
func haltPayload(res driveResult) ledger.Payload {
	p := ledger.Payload{"skill": res.Skill, "reason": res.Reason}

	if len(res.Attempts) > 0 {
		reasons := make([]string, 0, len(res.Attempts))
		for _, a := range res.Attempts {
			reasons = append(reasons, a.Reason)
		}

		p["attempts"] = reasons
	}

	return p
}

func RecordAt(ctx context.Context, dir string, e ledger.Event) {
	place := ledger.Where(ctx, dir)
	e.Project, e.ProjectLocal, e.Checkout, e.Branch = place.Project, place.ProjectLocal, place.Checkout, place.Branch

	ledger.Record(e)
}

// baseHead is the base branch's head, "none" for no branch, or why git
// could not read it.
func (in *DriveInput) baseHead(ctx context.Context, b *DriveRun) (string, string) {
	head, err := gitx.Ref(ctx, in.Main, "refs/heads/"+b.O.Branch)
	if err != nil {
		return "", "reading " + b.Task.Ref.ID + "'s branch: " + err.Error()
	}

	return cmp.Or(head, "none"), ""
}

// A taskEndError carries a task's end up its path instead of applying it
// where it was found; onTask applies it at each per-task entry of the tick.
type taskEndError struct{ res driveResult }

func (e *taskEndError) Error() string { return e.res.Row + ": " + e.res.Reason }

func endWith(res driveResult) error { return &taskEndError{res: res} }

// halt ends the task halted at skill for reason.
func halt(id, skill, reason string) error { return endWith(halted(id, skill, reason)) }

// endOf is the end err carries, if it is one.
func endOf(err error) (driveResult, bool) {
	if e, ok := errors.AsType[*taskEndError](err); ok {
		return e.res, true
	}

	return driveResult{}, false
}

// OnTask applies an end r's path returned; any other error goes on up.
func (in *DriveInput) OnTask(ctx context.Context, r *DriveRun, err error) error {
	if res, ok := endOf(err); ok {
		in.End(ctx, r, res)

		return nil
	}

	return err
}
