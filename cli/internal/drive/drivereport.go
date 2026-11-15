package drive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Report exits with an ask over a halt, since answering the ask is what
// resumes the drive.
func (in *DriveInput) Report(runs []*DriveRun, notes []string) error {
	var (
		rows   []render.Fact
		lines  []string
		asking []string
		code   int
	)

	halted := map[string]bool{}

	for _, r := range runs {
		id, shown := r.Task.Ref.ID, in.id(r.Task.Ref.ID)

		switch {
		case r.End != nil:
			rows = append(rows, render.Fact{Label: shown, Value: r.End.Row})
			lines = append(lines, r.End.Lines...)

			if r.End.Code == ui.ExitAsk {
				asking = append(asking, id)
			}

			halted[id] = r.End.Code == ui.ExitHalted
			code = max(code, r.End.Code)
		case r.queued():
			rows = append(rows, render.Fact{Label: shown, Value: "queued"})
		case r.active() && r.Phase == atParked:
			rows = append(rows, render.Fact{Label: shown, Value: "waiting on " + in.id(r.Base.Task.Ref.ID)})
		case r.active() && r.Phase == atWaiting && r.checks != nil:
			rows = append(rows, render.Fact{Label: shown, Value: "waiting on checks"})
		case r.active() && r.Phase == atWaiting:
			rows = append(rows, render.Fact{Label: shown, Value: "waiting for " + in.id(r.Base.Task.Ref.ID)})
		case r.active() && r.Skill == "ci":
			rows = append(rows, render.Fact{Label: shown, Value: "triaging checks"})
		case r.active():
			rows = append(rows, render.Fact{Label: shown, Value: "running " + r.Skill})
		case halted[r.Task.Base]:
			rows = append(rows, render.Fact{Label: shown, Value: "halted: base " + in.id(r.Task.Base) + " halted"})
			halted[id] = true
			code = max(code, ui.ExitHalted)
		default:
			rows = append(rows, render.Fact{Label: shown, Value: "not reached"})
		}
	}

	if len(asking) > 0 {
		code = ui.ExitAsk
		lines = append(lines, "answer with: "+in.resumeLine()+answerFlags(asking))
	}

	if in.Answers.Bare != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	for _, r := range runs {
		if _, left := in.Answers.Keyed(r.Task.Ref.ID); left {
			notes = append(notes, "--answer for "+r.Task.Ref.ID+" went unused: it was not stopped on a question")
		}
	}

	// an answer reported unused is dropped, so a resume never sends it to a
	// question nobody saw; only one a stopped drive never reached is kept
	in.Answers.Clear()
	in.savePending(nil)

	notes = append(notes, lines...)
	notes = append(notes, in.reproLines(context.Background(), runs)...)

	if len(rows) > 0 {
		fmt.Fprintln(in.Out)
	}

	if err := render.WriteReport(in.Out, rows, notes); err != nil {
		return err
	}

	if code != 0 {
		return cli.Exit("", code)
	}

	return nil
}

// handoffBlock heads a finished task's handoff with its id and indents it,
// so several tasks' lists stay apart beneath the drive's report.
func handoffBlock(id string, lines []string) []string {
	if len(lines) == 0 {
		return nil
	}

	block := []string{id + " handoff:"}
	for _, l := range lines {
		block = append(block, strings.TrimRight("  "+l, " "))
	}

	return block
}

// reproLines names each task planned without a reproduction, because a
// drive plans unattended and nobody else would see it. A task's rows are
// found by its worktree, which the debug pass recorded them from.
func (in *DriveInput) reproLines(ctx context.Context, runs []*DriveRun) []string {
	var out []string

	for _, r := range runs {
		if r.O.Path == "" {
			continue
		}

		checkout := r.O.Path
		if resolved, err := filepath.EvalSymlinks(checkout); err == nil {
			checkout = resolved
		}

		id := r.Task.Ref.ID

		events, err := ledger.ReadEvents(ctx, ledger.Path(), ledger.Filter{
			Task: id, Kind: ledger.RegressionFound, Checkout: checkout,
		})
		if err != nil || len(events) == 0 {
			continue
		}

		p, err := ledger.Decode[*ledger.RegressionFoundPayload](events[len(events)-1])
		if err != nil {
			continue
		}

		switch p.GetRepro() {
		case "skipped":
			out = append(out, in.id(id)+": reproduction skipped (costly), planned from a code reading")
		case "unconfirmed":
			out = append(out, in.id(id)+": reproduction unconfirmed, planned from what was tried")
		}
	}

	return out
}
