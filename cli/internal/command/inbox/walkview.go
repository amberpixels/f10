package inbox

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// walkView draws an ask for the walk. Its styles come from a renderer bound
// to the writer, so a pipe or NO_COLOR gets the same layout as plain text.
type walkView struct {
	w     io.Writer
	width int
	links bool

	faint, bold lipgloss.Style
}

func newWalkView(w io.Writer, links bool) walkView {
	r := lipgloss.NewRenderer(w)

	return walkView{
		w:     w,
		width: ui.Width(w),
		links: links,
		faint: r.NewStyle().Faint(true),
		bold:  r.NewStyle().Bold(true),
	}
}

// ask prints a's header, its context and its questions, each option
// lettered and the default marked. name is the task as the list labels it.
func (v walkView) ask(a asks.Ask, name string, now time.Time) {
	header := name
	if a.Skill != "" {
		header += v.faint.Render(" · at " + a.Skill)
	}

	header += v.faint.Render(" · asked " + now.Sub(a.AskedAt).Round(time.Minute).String() + " ago")

	fmt.Fprintln(v.w, " "+asks.AskIcon+" "+header)

	if a.URL != "" {
		fmt.Fprintln(v.w, " "+v.faint.Render(a.URL))
	}

	if a.Context != "" {
		for _, line := range v.wrap(a.Context, " ") {
			fmt.Fprintln(v.w, line)
		}
	}

	qs := a.Questions
	if len(qs) == 0 {
		qs = asks.Parse(a.Text)
	}

	for _, q := range qs {
		fmt.Fprintln(v.w)
		fmt.Fprintln(v.w, " "+v.bold.Render(strconv.Itoa(q.N)+". "+q.Text))

		for i, o := range q.Options {
			line := "    " + string(rune('a'+i)) + ") " + o
			if i == 0 {
				line += v.faint.Render("  (default)")
			}

			fmt.Fprintln(v.w, line)
		}
	}
}

var (
	urlToken = regexp.MustCompile(`^(https?://\S+?)([.,;:)]*)$`)
	shaToken = regexp.MustCompile(`^([0-9a-f]{40})([.,;:)]*)$`)
)

// shaShown is how many characters of a full sha the context shows.
const shaShown = 8

// wrap breaks text into faint lines at the terminal's width, never inside a
// word, so a url stays whole and clickable. A full sha shows shortened.
func (v walkView) wrap(text, indent string) []string {
	var (
		lines []string
		line  strings.Builder
		cols  int
	)

	for word := range strings.FieldsSeq(text) {
		shown, styled := v.token(word)

		if cols > 0 && v.width > 0 && len(indent)+cols+1+len(shown) > v.width {
			lines = append(lines, indent+line.String())
			line.Reset()

			cols = 0
		}

		if cols > 0 {
			line.WriteString(v.faint.Render(" "))

			cols++
		}

		line.WriteString(styled)

		cols += render.VisibleWidth(shown)
	}

	if cols > 0 {
		lines = append(lines, indent+line.String())
	}

	return lines
}

// token is a context word as shown, and as printed with its style and link.
func (v walkView) token(word string) (string, string) {
	if m := shaToken.FindStringSubmatch(word); m != nil {
		shown := m[1][:shaShown] + m[2]

		return shown, v.faint.Render(shown)
	}

	if m := urlToken.FindStringSubmatch(word); m != nil && v.links {
		return word, render.Hyperlink(m[1], v.faint.Render(m[1])) + v.faint.Render(m[2])
	}

	return word, v.faint.Render(word)
}
