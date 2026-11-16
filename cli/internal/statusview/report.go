package statusview

import (
	"fmt"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The shapes a status takes, one per Report.
const (
	ReportLine     = "line"     // a sentence alone: no run to show
	ReportRuns     = "runs"     // runs, each with its task's gates
	ReportRows     = "rows"     // a parent's subtasks, or what a session launched
	ReportFallback = "fallback" // a task no live run is about
	ReportFinished = "finished" // a task finished and gone
)

// A Report is what one status call shows, read in full before anything
// prints, so the daemon reads it and the client prints it as one call in
// process would. Kind is empty when a guessed task showed nothing.
type Report struct {
	Kind string `json:"kind,omitempty"`
	Line string `json:"line,omitempty"`

	Runs []RunJSON `json:"runs,omitempty"`
	// Headed is true when the runs share their task's shipment, printed
	// once above them; ShowRoot when they are listed across checkouts
	Headed   bool `json:"headed,omitempty"`
	ShowRoot bool `json:"show_root,omitempty"`

	Rows   []TaskRow `json:"rows,omitempty"`
	Parent string    `json:"parent,omitempty"` // the rows' head

	Fallback *fallback `json:"fallback,omitempty"`
	Finished *finished `json:"finished,omitempty"`

	// Asks is true when what shows holds a question a driven agent waits on
	Asks bool `json:"asks,omitempty"`
}

// finished is a finished task: its row, which --json prints, and the
// shipment the archive holds, which the text prints.
type finished struct {
	Row      TaskRow  `json:"row"`
	Shipment Shipment `json:"shipment"`
}

func lineReport(format string, args ...any) Report {
	return Report{Kind: ReportLine, Line: fmt.Sprintf(format, args...)}
}

// runsReport lays runs out with their gates; head is the task's shipment,
// nil for runs listed without a task.
//
//nolint:lostfield // the report holds each run whole, not a conversion of it
func runsReport(runs []*state.Run, gates [][]gate, head *Shipment, showRoot bool, agent string) Report {
	out := make([]RunJSON, len(runs))
	for i, r := range runs {
		out[i] = RunJSON{Run: r, Agent: agent, Gates: gates[i], Shipment: head}
		if head != nil {
			out[i].Verdicts, out[i].Reviews = head.Verdicts, head.Reviews
		}
	}

	return Report{
		Kind:     ReportRuns,
		Runs:     out,
		Headed:   head != nil,
		ShowRoot: showRoot,
		Asks:     slices.ContainsFunc(runs, func(r *state.Run) bool { return r.Ask != "" }),
	}
}

func rowsReport(rows []TaskRow, head string) Report {
	return Report{
		Kind:   ReportRows,
		Rows:   rows,
		Parent: head,
		Asks:   slices.ContainsFunc(rows, func(r TaskRow) bool { return r.Stage == stageAsking }),
	}
}

// Render prints rep under v's flags, as the status call that read it would.
func Render(v StatusView, rep Report) error {
	switch rep.Kind {
	case ReportLine:
		_, err := fmt.Fprintln(v.Out, rep.Line)

		return err
	case ReportRuns:
		return renderRuns(v, rep)
	case ReportRows:
		return writeTaskRows(v, rep.Rows, rep.Parent)
	case ReportFallback:
		if rep.Fallback == nil {
			return nil
		}

		if v.JSON {
			return ui.JSON(v.Out, rep.Fallback)
		}

		rep.Fallback.Render(v.Out, ui.Width(v.Out), render.LinksFor(v.Out, v.Plain).On)
	case ReportFinished:
		if rep.Finished == nil {
			return nil
		}

		if v.JSON {
			return writeTaskRows(v, []TaskRow{rep.Finished.Row}, rep.Finished.Row.ID)
		}

		rows := finishedFacts(rep.Finished.Shipment, rep.Finished.Row.Gates, render.LinksFor(v.Out, v.Plain).On)

		return render.WriteFacts(v.Out, rows, render.LabelWidth(rows), ui.Width(v.Out))
	}

	return nil
}

func renderRuns(v StatusView, rep Report) error {
	if v.JSON {
		return ui.JSON(v.Out, rep.Runs)
	}

	runs := make([]*state.Run, len(rep.Runs))
	gates := make([][]gate, len(rep.Runs))
	agent := ""

	var head *Shipment

	for i, r := range rep.Runs {
		runs[i], gates[i], agent = r.Run, r.Gates, r.Agent
		if rep.Headed {
			head = r.Shipment
		}
	}

	return writeRuns(v, runs, gates, head, rep.ShowRoot, agent, time.Now())
}
