package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
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
		if err := writeReport(cmd.Writer, head.facts(), nil); err != nil {
			return err
		}
	}

	for i, r := range runs {
		if i > 0 || head != nil {
			fmt.Fprintln(cmd.Writer)
		}

		if head != nil {
			renderRunRows(cmd.Writer, r, gates[i], showRoot, agent, now, limit)
		} else {
			renderRun(cmd.Writer, r, gates[i], showRoot, agent, now, limit)
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
		flows:  t.eff.Flows,
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

// renderRunRows is a run's rows after its task and url: the step rows a
// shipment block heads.
func renderRunRows(w io.Writer, r *state.Run, gates []gate, showRoot bool, agent string, now time.Time, limit int) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	if agent != "" {
		headerRow(w, "agent", agent, limit, faint)
	}

	if showRoot {
		headerRow(w, "root", textx.OrDash(r.Root), limit, faint)
	}

	for _, p := range state.Phases {
		headerRow(w, p, r.PhaseText(p), limit, statusStyle(r.Status(p)))
	}

	for _, g := range gates {
		headerRow(w, "  "+g.shown(), g.text(), limit, statusStyle(g.style()))
	}

	if r.Note != "" {
		headerRow(w, "note", r.Note, limit, plain)
	}

	if r.Next != "" {
		headerRow(w, "next", r.Next, limit, plain)
	}

	if r.Ask != "" {
		headerRow(w, "ask", r.Ask, limit, plain)
	}

	if r.Wait != "" {
		headerRow(w, "wait", r.Wait, limit, plain)
	}

	if final := r.FinalShown(); final != "" {
		headerRow(w, "final", final, limit, faint)
	}

	age := "unknown"
	if !r.Updated.IsZero() {
		age = textx.Age(now, r.Updated) + " ago"
	}

	if !r.Live(now, state.TTL()) {
		age += " (stale)"
	}

	headerRow(w, "updated", age, limit, faint)
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
