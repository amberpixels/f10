package main

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/state"
)

func gateLines(gates []gate) [][2]string {
	out := make([][2]string, len(gates))

	for i, g := range gates {
		out[i] = [2]string{"  " + g.shown(), g.text()}
	}

	return out
}

// style is the badge status whose palette the gate prints in.
func (g gate) style() string {
	states := []string{g.State}
	if g.Local != nil || g.Remote != nil {
		states = nil

		for _, h := range []*reviewHalf{g.Local, g.Remote} {
			if h != nil {
				states = append(states, h.State)
			}
		}
	}

	switch {
	case slices.Contains(states, gateRunning):
		return "running"
	case slices.ContainsFunc(states, state.Stopped):
		return "failed"
	case slices.Contains(states, gateUnknown):
		return "blocked"
	case slices.Contains(states, gateOpen):
		return "partial"
	case !slices.ContainsFunc(states, func(s string) bool { return s != gateDone && s != gateSettled }):
		return "done"
	}

	return state.Upcoming
}

// shown is the gate's name as a person reads it: its label, else its step.
func (g gate) shown() string { return cmp.Or(g.Label, g.Step) }

func (g gate) text() string {
	switch {
	case g.Local != nil || g.Remote != nil:
		var parts []string

		if g.Local != nil {
			parts = append(parts, "local: "+g.Local.text())
		}

		if g.Remote != nil {
			parts = append(parts, "remote: "+g.Remote.text())
		}

		return strings.Join(parts, " · ")
	case g.Judged != "":
		return g.Judged + ": " + g.stateText()
	}

	return g.stateText()
}

func (g gate) stateText() string {
	switch {
	case (g.State == gateRunning || g.State == gateQueued) && g.Via != "" && g.Action != "":
		return g.State + " (" + g.Via + "): " + g.Action
	case (g.State == gateRunning || g.State == gateQueued) && g.Via != "":
		return g.State + " (" + g.Via + ")"
	case g.State == gateWaiting && g.Action != "":
		return g.State + " " + g.Action
	case g.State == gateDone && g.Outcome != nil:
		text := g.Outcome.Verdict
		if done := g.Outcome.done(); done != "" {
			text += " (" + done + ")"
		}

		if g.Outcome.Reason != "" {
			text += " - " + g.Outcome.Reason
		}

		return text
	}

	return g.State
}

// done is what the ship run did with the verdict, from the decision line;
// continue and a file with no line say nothing, and a new word shows as is.
func (j *judgeOutcome) done() string {
	switch j.Decision {
	case "", "continue":
		return ""
	case "built":
		if j.Changes > 0 {
			return strconv.Itoa(j.Changes) + " built"
		}

		return "built"
	case "refactor as agreed":
		return "refactor built"
	case "folded":
		return "folded into plan"
	case "block", "stop":
		return "blocked"
	case "asked":
		return "waiting on an answer"
	case "overridden":
		return "overridden by the user"
	}

	return j.Decision
}

func (h *reviewHalf) text() string {
	switch h.State {
	case gateRunning:
		what := fmt.Sprintf("round %d running", len(h.Rounds)+1)

		switch h.Action {
		case "resolve":
			what = "resolve running"
		case "await":
			// review:local+ci: this review's round is in, the other's is not
			what = fmt.Sprintf("round %d in, waiting for the other review", len(h.Rounds))
		}

		if h.Via != "" {
			what += " (" + h.Via + ")"
		}

		return what
	case gateUnknown:
		return "unknown (" + cmp.Or(h.Detail, "no answer") + ")"
	case gateOpen:
		if len(h.Rounds) == 0 {
			return "open (not handled)"
		}

		r := h.Rounds[len(h.Rounds)-1]

		return fmt.Sprintf("%d open of %d found", r.Ask, r.Found)
	case gateSettled:
		if len(h.Rounds) == 0 {
			return gateSettled
		}

		return reviewText(h.Rounds)
	}

	return h.State
}

// compactGates is the parent rows' gate column.
func compactGates(gates []gate) string {
	var parts []string

	for _, g := range gates {
		switch {
		case g.State == gateOff:
			// a step the flow leaves out has no column to fill
		case g.Step == "review":
			parts = append(parts, statusGlyph(glyphReview)+" L "+g.Local.compact(), "R "+g.Remote.compact())
		case g.Step == "judge":
			j := statusGlyph(glyphJudge) + " J "
			switch g.Judged {
			case "task":
				j = statusGlyph(glyphJudge) + " TJ "
			case "plan":
				j = statusGlyph(glyphJudge) + " PJ "
			}

			parts = append(parts, j+g.compactState())
		}
	}

	return strings.Join(parts, " · ")
}

func (g gate) compactState() string {
	switch g.State {
	case gateRunning:
		return "…"
	case gateDone:
		if g.Outcome != nil {
			return g.Outcome.Verdict
		}
	}

	return g.State
}

func (h *reviewHalf) compact() string {
	if h == nil {
		return "-"
	}

	switch h.State {
	case gateRunning:
		return "…"
	case gateUnknown:
		return "?"
	case gateSettled:
		if len(h.Rounds) == 0 {
			return "ok"
		}

		r := h.Rounds[len(h.Rounds)-1]
		if r.Found == 0 {
			return "clean"
		}

		return fmt.Sprintf("%d/%d", r.Fixed, r.Found)
	case gateOpen:
		if len(h.Rounds) == 0 {
			return gateOpen
		}

		return fmt.Sprintf("%d open", h.Rounds[len(h.Rounds)-1].Ask)
	}

	return h.State
}
