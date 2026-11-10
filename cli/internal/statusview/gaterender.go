package statusview

import (
	"strings"
)

func GateLines(gates []Gate) [][2]string {
	out := make([][2]string, len(gates))

	for i, g := range gates {
		out[i] = [2]string{"  " + g.Shown(), g.Text()}
	}

	return out
}

// CompactGates is the parent rows' gate column.
func CompactGates(gates []Gate) string {
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
