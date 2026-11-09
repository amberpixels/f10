package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ui"
)

// report exits with an ask over a halt, since answering the ask is what
// resumes the drive.
func (in *driveInput) report(w io.Writer, runs []*driveRun, notes []string) error {
	var (
		rows   []fact
		lines  []string
		asking []string
		code   int
	)

	halted := map[string]bool{}

	for _, r := range runs {
		id, shown := r.task.ref.ID, in.id(r.task.ref.ID)

		switch {
		case r.end != nil:
			rows = append(rows, fact{label: shown, value: r.end.row})
			lines = append(lines, r.end.lines...)

			if r.end.code == ui.ExitAsk {
				asking = append(asking, id)
			}

			halted[id] = r.end.code == ui.ExitHalted
			code = max(code, r.end.code)
		case r.queued:
			rows = append(rows, fact{label: shown, value: "queued"})
		case r.active() && r.parked:
			rows = append(rows, fact{label: shown, value: "waiting on " + in.id(r.base.task.ref.ID)})
		case r.active() && r.waiting:
			rows = append(rows, fact{label: shown, value: "waiting for " + in.id(r.base.task.ref.ID)})
		case r.active():
			rows = append(rows, fact{label: shown, value: "running " + r.skill})
		case halted[r.task.base]:
			rows = append(rows, fact{label: shown, value: "halted: base " + in.id(r.task.base) + " halted"})
			halted[id] = true
			code = max(code, ui.ExitHalted)
		default:
			rows = append(rows, fact{label: shown, value: "not reached"})
		}
	}

	if len(asking) > 0 {
		code = ui.ExitAsk
		lines = append(lines, "answer with: "+in.resumeLine()+answerFlags(asking))
	}

	if in.answer != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	for _, r := range runs {
		if _, left := in.answers[r.task.ref.ID]; left {
			notes = append(notes, "--answer for "+r.task.ref.ID+" went unused: it was not stopped on a question")
		}
	}

	// an answer reported unused is dropped, so a resume never sends it to a
	// question nobody saw; only one a stopped drive never reached is kept
	in.answer, in.answers = "", map[string]string{}
	in.savePending(nil)

	notes = append(notes, lines...)

	if len(rows) > 0 {
		fmt.Fprintln(w)
	}

	if err := writeReport(w, rows, notes); err != nil {
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
