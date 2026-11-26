package main

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/ui"
)

// The palette is kyo's and ele's (kyo/internal/render/style.go), so the
// three tools read alike side by side.
var (
	colTeal  = lipgloss.Color("#14B8A6")
	colGreen = lipgloss.Color("#22C55E")
	colRed   = lipgloss.Color("#EF4444")
	colAmber = lipgloss.Color("#F59E0B")
	colPink  = lipgloss.Color("#F472B6")
	colGray  = lipgloss.Color("#6B7280")
)

// styles colors the runner's output. Zero-value styles render their input
// verbatim, so plain output needs no branch at the call sites.
type styles struct {
	rich bool // a terminal without NO_COLOR: escape codes, links and a repainted block

	ok    lipgloss.Style
	fail  lipgloss.Style
	spin  lipgloss.Style
	timer lipgloss.Style
	cost  lipgloss.Style
	dim   lipgloss.Style
	title lipgloss.Style
}

// newStyles builds styles for w. Off a terminal or under NO_COLOR they are
// plain, so a pipe never gets an escape code.
func newStyles(w io.Writer) *styles {
	if os.Getenv("NO_COLOR") != "" || !ui.IsTerminal(w) {
		return &styles{}
	}

	r := lipgloss.NewRenderer(w)

	return &styles{
		rich:  true,
		ok:    r.NewStyle().Foreground(colGreen),
		fail:  r.NewStyle().Foreground(colRed).Bold(true),
		spin:  r.NewStyle().Foreground(colTeal),
		timer: r.NewStyle().Foreground(colPink),
		cost:  r.NewStyle().Foreground(colAmber),
		dim:   r.NewStyle().Foreground(colGray),
		title: r.NewStyle().Bold(true),
	}
}

func (st *styles) mark(pass bool) string {
	if pass {
		return st.ok.Render("✓")
	}

	return st.fail.Render("✗")
}

// verdict colors a comparison's mark: better green, worse red.
func (st *styles) verdict(m string) string {
	switch m {
	case "better":
		return st.ok.Render(m)
	case "worse":
		return st.fail.Render(m)
	}

	return m
}
