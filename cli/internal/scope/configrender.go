package scope

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/render"
)

// HeaderIndent is the value column of the header block: the widest label,
// "checkout root", plus the gap.
const HeaderIndent = 16

func HeaderRow(w io.Writer, label, value string, limit int, style lipgloss.Style) {
	LinkedHeaderRow(w, label, value, "", limit, style)
}

// LinkedHeaderRow links each wrapped line to target, since wrapping a value
// that already holds a link's escapes would cut them apart.
func LinkedHeaderRow(w io.Writer, label, value, target string, limit int, style lipgloss.Style) {
	faint := lipgloss.NewStyle().Faint(true)

	segments := render.Wrap(value, limit-HeaderIndent)
	fmt.Fprintf(w, "%s%s\n", faint.Render(fmt.Sprintf("%-*s", HeaderIndent, label)),
		render.Hyperlink(target, style.Render(segments[0])))

	for _, s := range segments[1:] {
		fmt.Fprintf(w, "%-*s%s\n", HeaderIndent, "", render.Hyperlink(target, style.Render(s)))
	}
}
