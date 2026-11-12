package statusview

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/render"
)

// gateFacts is the gates as the indented step rows under a block's ship row.
func gateFacts(gates []gate) []render.Fact {
	out := make([]render.Fact, len(gates))

	for i, g := range gates {
		out[i] = RunFact("  "+g.Shown(), g.Text(), statusStyle(g.style()))
	}

	return out
}

// compactGates is the parent rows' gate column.
func compactGates(gates []gate) string {
	var parts []string

	for _, g := range gates {
		switch {
		case g.State == GateOff:
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

			parts = append(parts, j+g.CompactState())
		}
	}

	return strings.Join(parts, " · ")
}
