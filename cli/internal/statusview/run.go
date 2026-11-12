package statusview

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// writeRuns prints runs, each with its gates, or encodes them under --json.
// head is the task's shipment, printed once above the runs in place of each
// run's task and url rows; nil for runs listed without a task. agent is the
// profile the task's branch records, "" for the kind's default.
func writeRuns(v StatusView, runs []*state.Run, gates [][]gate, head *Shipment, showRoot bool, agent string,
	now time.Time,
) error {
	if v.JSON {
		out := make([]RunJSON, len(runs))
		for i, r := range runs {
			out[i] = RunJSON{Run: r, Agent: agent, Gates: gates[i], Shipment: head}
			if head != nil {
				out[i].Verdicts, out[i].Reviews = head.Verdicts, head.Reviews
			}
		}

		return ui.JSON(v.Out, out)
	}

	limit := ui.Width(v.Out)

	if head != nil {
		return writeRunsUnder(
			v.Out,
			head.Facts(render.LinksFor(v.Out, v.Plain).On),
			runs,
			gates,
			showRoot,
			agent,
			now,
			limit,
		)
	}

	for i, r := range runs {
		if i > 0 {
			fmt.Fprintln(v.Out)
		}

		RunRows(v.Out, r, RunView{Gates: gates[i], ShowRoot: showRoot, Agent: agent, Now: now, Limit: limit})
	}

	return nil
}

// writeRunsUnder prints the facts head and each run's rows as one table, every
// value in one column, so a yaml fence colors the block as one mapping. The
// first run goes on under the head; each later one follows a blank line.
func writeRunsUnder(w io.Writer, head []render.Fact, runs []*state.Run, gates [][]gate, showRoot bool, agent string,
	now time.Time, limit int,
) error {
	blocks := make([][]render.Fact, max(1, len(runs)))
	for i, r := range runs {
		blocks[i] = runFacts(r, RunView{Gates: gates[i], ShowRoot: showRoot, Agent: agent, Now: now, Headed: true})
	}

	// the plan phase row takes the key `plan`, so the plan file's row names itself
	head = slices.Clone(head)
	if len(runs) > 0 {
		for i := range head {
			if head[i].Label == "plan" {
				head[i].Label = "plan file"
			}
		}
	}

	blocks[0] = slices.Concat(head, blocks[0])

	width := 0
	for _, b := range blocks {
		width = max(width, render.LabelWidth(b))
	}

	for i, b := range blocks {
		if i > 0 {
			fmt.Fprintln(w)
		}

		if err := render.WriteFacts(w, b, width, limit); err != nil {
			return err
		}
	}

	return nil
}

// currentPhase is the furthest phase a run has reached, with its status:
// `capture running`, `ship blocked at judge`.
func currentPhase(r *state.Run) string {
	phase := r.Furthest()

	return phase + " " + r.PhaseText(phase)
}

// RunJSON is a run as --json prints it, a task's gates beside the state's
// own keys.
type RunJSON struct {
	*state.Run

	Agent string `json:"agent,omitempty"`
	Gates []gate `json:"gates,omitempty"`

	// the task's files and facts, repeated per run as the agent is; the
	// files predate the shipment and repeat what it holds
	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`
	Shipment *Shipment     `json:"shipment,omitempty"`
}

// RunView is how one run block prints.
type RunView struct {
	Gates    []gate
	ShowRoot bool   // listing runs: for one session the root is the directory you are in
	Agent    string // the profile the task's branch records, "" for the kind's default
	Now      time.Time
	Limit    int
	Headed   bool // a shipment block above holds the task and url rows
}

// RunRows prints one run in the headless colon-less shape; every run block
// without a facts head, `f10 state show` included, prints through it.
func RunRows(w io.Writer, r *state.Run, v RunView) {
	for _, f := range runFacts(r, v) {
		scope.HeaderRow(w, f.Label, f.Value, v.Limit, f.Style)
	}
}

// runFacts is a run's rows as data, each styled by its status, the task's
// gates under ship when it has them.
func runFacts(r *state.Run, v RunView) []render.Fact {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	var rows []render.Fact

	add := func(label, value string, style lipgloss.Style) {
		rows = append(rows, RunFact(label, value, style))
	}

	if !v.Headed {
		add("task", textx.OrDash(r.Task), plain)
		add("url", textx.OrDash(r.URL), plain)
	}

	if v.Agent != "" {
		add("agent", v.Agent, faint)
	}

	if v.ShowRoot {
		add("root", textx.OrDash(r.Root), faint)
	}

	for _, p := range state.Phases {
		add(p, r.PhaseText(p), statusStyle(r.Status(p)))
	}

	rows = append(rows, gateFacts(v.Gates)...)

	for _, kv := range [][2]string{{"note", r.Note}, {"next", r.Next}, {"ask", r.Ask}, {"wait", r.Wait}, {"stop", r.Stop}} {
		if kv[1] != "" {
			add(kv[0], kv[1], plain)
		}
	}

	if final := r.FinalShown(); final != "" {
		add("final", final, faint)
	}

	age := "unknown"
	if !r.Updated.IsZero() {
		age = textx.Age(v.Now, r.Updated) + " ago"
	}

	if !r.Live(v.Now, state.TTL()) {
		age += " (stale)"
	}

	add("updated", age, faint)

	return rows
}

func RunFact(label, value string, style lipgloss.Style) render.Fact {
	return render.Fact{Label: label, Value: value, Run: true, Style: style}
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
