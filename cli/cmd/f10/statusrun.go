package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// writeRuns prints runs, each with its gates, or encodes them under --json.
// head is the task's shipment, printed once above the runs in place of each
// run's task and url rows; nil for runs listed without a task. agent is the
// profile the task's branch records, "" for the kind's default.
func writeRuns(cmd *cli.Command, runs []*state.Run, gates [][]gate, head *shipment, showRoot bool, agent string,
	now time.Time,
) error {
	if cmd.Bool("json") {
		out := make([]runJSON, len(runs))
		for i, r := range runs {
			out[i] = runJSON{Run: r, Agent: agent, Gates: gates[i]}
			if head != nil {
				out[i].Verdicts, out[i].Reviews = head.verdicts, head.reviews
			}
		}

		return ui.JSON(cmd.Writer, out)
	}

	limit := ui.Width(cmd.Writer)

	if head != nil {
		return writeRunsUnder(cmd.Writer, head.facts(), runs, gates, showRoot, agent, now, limit)
	}

	for i, r := range runs {
		if i > 0 {
			fmt.Fprintln(cmd.Writer)
		}

		renderRun(cmd.Writer, r, gates[i], showRoot, agent, now, limit)
	}

	return nil
}

// writeRunsUnder prints the facts head and each run's rows as one table, every
// value in one column, so a yaml fence colors the block as one mapping. The
// first run goes on under the head; each later one follows a blank line.
func writeRunsUnder(w io.Writer, head []fact, runs []*state.Run, gates [][]gate, showRoot bool, agent string,
	now time.Time, limit int,
) error {
	blocks := make([][]fact, max(1, len(runs)))
	for i, r := range runs {
		blocks[i] = runRows(r, gates[i], showRoot, agent, now)
	}

	blocks[0] = slices.Concat(head, blocks[0])

	width := 0
	for _, b := range blocks {
		width = max(width, labelWidth(b))
	}

	for i, b := range blocks {
		if i > 0 {
			fmt.Fprintln(w)
		}

		if err := writeFacts(w, b, width, limit); err != nil {
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

// runJSON is a run as --json prints it, a task's gates beside the state's
// own keys.
type runJSON struct {
	*state.Run

	Agent string `json:"agent,omitempty"`
	Gates []gate `json:"gates,omitempty"`

	// the task's files, repeated per run as the agent is
	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`
}

// runGates is each of the task's runs' gates, from the ledger's events.
func runGates(ctx context.Context, t *target, in taskStatusInput, events []ledger.Event, runs []*state.Run) [][]gate {
	src := newGateSource(t, events, in.host)
	out := make([][]gate, len(runs))

	for i, r := range runs {
		out[i] = src.gatesOf(ctx, in.id, in.branch, in.root, layout.Locate(ctx, r.Root).StorageRoot.Path(), r)
	}

	return out
}

// newGateSource binds the gate reads to this target.
func newGateSource(t *target, events []ledger.Event, h *host) gateSource {
	src := gateSource{
		events: events,
		main:   cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		eff:    t.eff,
		review: t.eff.Review,
		store:  &lore.Store{Root: loreRootFor(t)},
		host:   h,
	}

	src.shared = t.lay.StorageRoot.Path()
	src.driveFlows = driveFlows(src.shared)

	return src
}

// renderRun prints one run in the order `f10 state show` prints the same
// facts, the task's gates under ship when it has them. The root row appears
// only when listing: for one session it is the directory you are in.
func renderRun(w io.Writer, r *state.Run, gates []gate, showRoot bool, agent string, now time.Time, limit int) {
	plain := lipgloss.NewStyle()

	headerRow(w, "task", textx.OrDash(r.Task), limit, plain)
	headerRow(w, "url", textx.OrDash(r.URL), limit, plain)
	renderRunRows(w, r, gates, showRoot, agent, now, limit)
}

// renderRunRows is a run's rows after its task and url, in renderRun's
// colon-less shape.
func renderRunRows(w io.Writer, r *state.Run, gates []gate, showRoot bool, agent string, now time.Time, limit int) {
	for _, f := range runRows(r, gates, showRoot, agent, now) {
		headerRow(w, f.label, f.value, limit, f.style)
	}
}

// runRows is a run's rows after its task and url: the step rows a shipment
// block heads, each styled by its status.
func runRows(r *state.Run, gates []gate, showRoot bool, agent string, now time.Time) []fact {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	var rows []fact

	add := func(label, value string, style lipgloss.Style) {
		rows = append(rows, runFact(label, value, style))
	}

	if agent != "" {
		add("agent", agent, faint)
	}

	if showRoot {
		add("root", textx.OrDash(r.Root), faint)
	}

	for _, p := range state.Phases {
		add(p, r.PhaseText(p), statusStyle(r.Status(p)))
	}

	for _, g := range gates {
		add("  "+g.shown(), g.text(), statusStyle(g.style()))
	}

	if r.Note != "" {
		add("note", r.Note, plain)
	}

	if r.Next != "" {
		add("next", r.Next, plain)
	}

	if r.Ask != "" {
		add("ask", r.Ask, plain)
	}

	if r.Wait != "" {
		add("wait", r.Wait, plain)
	}

	if final := r.FinalShown(); final != "" {
		add("final", final, faint)
	}

	age := "unknown"
	if !r.Updated.IsZero() {
		age = textx.Age(now, r.Updated) + " ago"
	}

	if !r.Live(now, state.TTL()) {
		age += " (stale)"
	}

	add("updated", age, faint)

	return rows
}

func runFact(label, value string, style lipgloss.Style) fact {
	return fact{label: label, value: value, run: true, style: style}
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
