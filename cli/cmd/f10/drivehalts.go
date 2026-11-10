package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// end prints the result now, so a halt or an ask is on screen when it
// happens, and puts a halt in the ledger for status to read after the exit.
// leaveRuns runs on every way out of the loop, drained and failed included:
// an interrupted drive leaves no finish record claiming it still runs, and
// no catchup verify running in its own process group.
func (in *driveInput) leaveRuns(ctx context.Context, runs []*driveRun) {
	for _, r := range runs {
		if r.active() {
			in.parkFinish(ctx, r)
		}

		if r.verifying != nil {
			r.verifying.halt()
		}
	}
}

func (in *driveInput) end(ctx context.Context, w io.Writer, r *driveRun, res driveResult) {
	r.end, r.queued = &res, false

	// a cancelled or halted run takes its catchup's verify with it
	if r.verifying != nil {
		r.verifying.stop()
	}

	// a finished task's branch took the record with it
	if res.row != rowFinished {
		in.parkFinish(ctx, r)
	}

	in.saveTask(r, func(t *drives.Task) {
		t.Skill, t.Queued, t.Outcome = "", false, &drives.Outcome{Row: res.row, Code: res.code}
	})
	// an asking task stays leased until the drive exits, since the drive
	// runs on and resumes for its answer; another drive must not claim it
	if res.code != ui.ExitAsk {
		in.unlease(r, in.runs)
	}

	if res.code == ui.ExitAsk && res.ask != "" {
		in.raise(ctx, r, strings.TrimPrefix(res.row, "asked at "), res.ask)
	}

	if res.code == ui.ExitHalted && !res.fromRun && !res.drives {
		in.recordHalt(ctx, r, res)
	}

	// a finished task's lines are its handoff, which finish's report just
	// printed; they wait for the drive's report
	if len(res.lines) == 0 || res.row == rowFinished {
		fmt.Fprintf(w, "%s: %s\n", in.id(r.task.ref.ID), res.row)

		return
	}

	for _, l := range res.lines {
		fmt.Fprintln(w, l)
	}
}

func halted(id, skill, reason string) driveResult {
	return driveResult{
		row:    "halted at " + skill,
		code:   ui.ExitHalted,
		lines:  []string{fmt.Sprintf("%s halted at %s: %s", id, skill, reason)},
		skill:  skill,
		reason: reason,
	}
}

// recordHalt puts a halt drive found itself in the ledger, placed in the
// task's worktree when it has one.
func (in *driveInput) recordHalt(ctx context.Context, r *driveRun, res driveResult) {
	if in.record == nil {
		return
	}

	in.record(ctx, cmp.Or(r.o.path, in.main), ledger.Event{
		At:      in.now(),
		Task:    r.task.ref.ID,
		Kind:    ledger.TaskHalted,
		Source:  ledger.Observed,
		Payload: ledger.NewPayload(haltPayload(res)),
	})
}

// haltPayload is a halt's ledger payload: the attempts it counted, if any.
func haltPayload(res driveResult) ledger.Payload {
	p := ledger.Payload{"skill": res.skill, "reason": res.reason}

	if len(res.attempts) > 0 {
		reasons := make([]string, 0, len(res.attempts))
		for _, a := range res.attempts {
			reasons = append(reasons, a.Reason)
		}

		p["attempts"] = reasons
	}

	return p
}

func recordAt(ctx context.Context, dir string, e ledger.Event) {
	place := ledger.Where(ctx, dir)
	e.Project, e.ProjectLocal, e.Checkout, e.Branch = place.Project, place.ProjectLocal, place.Checkout, place.Branch

	ledger.Record(e)
}

// baseHead is the base branch's head, "none" for no branch, or why git
// could not read it.
func (in *driveInput) baseHead(ctx context.Context, b *driveRun) (string, string) {
	head, err := gitx.Ref(ctx, in.main, "refs/heads/"+b.o.branch)
	if err != nil {
		return "", "reading " + b.task.ref.ID + "'s branch: " + err.Error()
	}

	return cmp.Or(head, "none"), ""
}

// A taskEnd is a task's end returned up its path instead of applied where it
// was found; onTask, at each per-task entry of the tick, applies it.
type taskEnd struct{ res driveResult }

func (e *taskEnd) Error() string { return e.res.row + ": " + e.res.reason }

func endWith(res driveResult) error { return &taskEnd{res: res} }

// halt ends the task halted at skill for reason.
func halt(id, skill, reason string) error { return endWith(halted(id, skill, reason)) }

// endOf is the end err carries, if it is one.
func endOf(err error) (driveResult, bool) {
	var e *taskEnd
	if errors.As(err, &e) {
		return e.res, true
	}

	return driveResult{}, false
}

// onTask applies an end r's path returned; any other error goes on up.
func (in *driveInput) onTask(ctx context.Context, w io.Writer, r *driveRun, err error) error {
	if res, ok := endOf(err); ok {
		in.end(ctx, w, r, res)

		return nil
	}

	return err
}
