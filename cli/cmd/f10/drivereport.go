package main

import (
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// report exits with an ask over a halt, since answering the ask is what
// resumes the drive.
func (in *driveInput) report(runs []*driveRun, notes []string) error {
	var (
		rows   []render.Fact
		lines  []string
		asking []string
		code   int
	)

	halted := map[string]bool{}

	for _, r := range runs {
		id, shown := r.task.ref.ID, in.id(r.task.ref.ID)

		switch {
		case r.end != nil:
			rows = append(rows, render.Fact{Label: shown, Value: r.end.row})
			lines = append(lines, r.end.lines...)

			if r.end.code == ui.ExitAsk {
				asking = append(asking, id)
			}

			halted[id] = r.end.code == ui.ExitHalted
			code = max(code, r.end.code)
		case r.queued():
			rows = append(rows, render.Fact{Label: shown, Value: "queued"})
		case r.active() && r.phase == atParked:
			rows = append(rows, render.Fact{Label: shown, Value: "waiting on " + in.id(r.base.task.ref.ID)})
		case r.active() && r.phase == atWaiting:
			rows = append(rows, render.Fact{Label: shown, Value: "waiting for " + in.id(r.base.task.ref.ID)})
		case r.active():
			rows = append(rows, render.Fact{Label: shown, Value: "running " + r.skill})
		case halted[r.task.base]:
			rows = append(rows, render.Fact{Label: shown, Value: "halted: base " + in.id(r.task.base) + " halted"})
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

	if in.answers.bare != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	for _, r := range runs {
		if _, left := in.answers.keyed(r.task.ref.ID); left {
			notes = append(notes, "--answer for "+r.task.ref.ID+" went unused: it was not stopped on a question")
		}
	}

	// an answer reported unused is dropped, so a resume never sends it to a
	// question nobody saw; only one a stopped drive never reached is kept
	in.answers.clear()
	in.savePending(nil)

	notes = append(notes, lines...)

	if len(rows) > 0 {
		fmt.Fprintln(in.out)
	}

	if err := render.WriteReport(in.out, rows, notes); err != nil {
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
