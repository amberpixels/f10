package main

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// writeRuns prints runs, each with its gates, or encodes them under --json.
// head is the task's shipment, printed once above the runs in place of each
// run's task and url rows; nil for runs listed without a task. agent is the
// profile the task's branch records, "" for the kind's default.
func writeRuns(v statusView, runs []*state.Run, gates [][]gate, head *shipment, showRoot bool, agent string,
	now time.Time,
) error {
	if v.json {
		out := make([]runJSON, len(runs))
		for i, r := range runs {
			out[i] = runJSON{Run: r, Agent: agent, Gates: gates[i], Shipment: head}
			if head != nil {
				out[i].Verdicts, out[i].Reviews = head.Verdicts, head.Reviews
			}
		}

		return ui.JSON(v.w, out)
	}

	if head != nil {
		if err := writeReport(v.w, head.facts(linksFor(v.w, v.plain).on), nil); err != nil {
			return err
		}
	}

	for i, r := range runs {
		if i > 0 || head != nil {
			fmt.Fprintln(v.w)
		}

		runRows(v.w, r, runView{
			gates: gates[i], showRoot: showRoot, agent: agent, now: now, limit: ui.Width(v.w), headed: head != nil,
		})
	}

	return nil
}

// currentPhase is the furthest phase a run has reached, with its status:
// `capture running`, `ship blocked at judge`.
func currentPhase(r *state.Run) string {
	phase := r.Furthest()

	return phase + " " + r.PhaseText(phase)
}

// runJSON is a run as --json prints it, a task's gates beside the state's
// own keys.
type runJSON struct {
	*state.Run

	Agent string `json:"agent,omitempty"`
	Gates []gate `json:"gates,omitempty"`

	// the task's files and facts, repeated per run as the agent is; the
	// files predate the shipment and repeat what it holds
	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`
	Shipment *shipment     `json:"shipment,omitempty"`
}

// runView is how one run block prints.
type runView struct {
	gates    []gate
	showRoot bool   // listing runs: for one session the root is the directory you are in
	agent    string // the profile the task's branch records, "" for the kind's default
	now      time.Time
	limit    int
	headed   bool // a shipment block above holds the task and url rows
}

// runRows prints one run, the task's gates under ship when it has them;
// every run block, `f10 state show` included, prints through it.
func runRows(w io.Writer, r *state.Run, v runView) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	if !v.headed {
		headerRow(w, "task", textx.OrDash(r.Task), v.limit, plain)
		headerRow(w, "url", textx.OrDash(r.URL), v.limit, plain)
	}

	if v.agent != "" {
		headerRow(w, "agent", v.agent, v.limit, faint)
	}

	if v.showRoot {
		headerRow(w, "root", textx.OrDash(r.Root), v.limit, faint)
	}

	for _, p := range state.Phases {
		headerRow(w, p, r.PhaseText(p), v.limit, statusStyle(r.Status(p)))
	}

	for _, g := range v.gates {
		headerRow(w, "  "+g.shown(), g.text(), v.limit, statusStyle(g.style()))
	}

	for _, kv := range [][2]string{{"note", r.Note}, {"next", r.Next}, {"ask", r.Ask}, {"wait", r.Wait}} {
		if kv[1] != "" {
			headerRow(w, kv[0], kv[1], v.limit, plain)
		}
	}

	if final := r.FinalShown(); final != "" {
		headerRow(w, "final", final, v.limit, faint)
	}

	age := "unknown"
	if !r.Updated.IsZero() {
		age = textx.Age(v.now, r.Updated) + " ago"
	}

	if !r.Live(v.now, state.TTL()) {
		age += " (stale)"
	}

	headerRow(w, "updated", age, v.limit, faint)
}

// statusStyle is the badge's palette: shape first, color second, so the
// same status reads the same in the status line and here.
func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch status {
	case "done":
		return s.Foreground(lipgloss.Color("2"))
	case "running":
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	case "failed":
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case "partial":
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case "blocked":
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case "prior":
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	default:
		return s.Faint(true)
	}
}
