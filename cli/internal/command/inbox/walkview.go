package inbox

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// shownQuestion is a question as the walk puts it: the stored one, or the
// one recovered from its prose. Stored is what the answer is sent against.
type shownQuestion struct {
	asks.Question

	Stored    asks.Question
	Recovered bool
	Context   string // background recovered from the question's prose
	Note      string // why the default is the default, from the prose
}

// shownQuestions are a's questions as the walk shows and asks them, the
// same list for both.
func shownQuestions(a asks.Ask) []shownQuestion {
	qs := a.Questions
	if len(qs) == 0 {
		qs = asks.Parse(a.Text)
	}

	out := make([]shownQuestion, len(qs))
	for i, q := range qs {
		out[i] = shownQuestion{Question: q, Stored: q}
		if r, ok := asks.Recover(q); ok {
			out[i] = shownQuestion{Question: r.Question, Stored: q, Recovered: true, Context: r.Context, Note: r.Note}
		}
	}

	return out
}

// walkView draws an ask for the walk. Its styles come from a renderer bound
// to the writer, so a pipe or NO_COLOR gets the same layout as plain text.
type walkView struct {
	w     io.Writer
	width int
	links bool

	plain, faint, bold lipgloss.Style
}

func newWalkView(w io.Writer, links bool) walkView {
	r := lipgloss.NewRenderer(w)

	return walkView{
		w:     w,
		width: ui.Width(w),
		links: links,
		plain: r.NewStyle(),
		faint: r.NewStyle().Faint(true),
		bold:  r.NewStyle().Bold(true),
	}
}

const (
	contextIndent = "   "
	optionIndent  = "    "
)

// ask prints a's header, its context and its questions, each option
// lettered and the default marked. name is the task as the list labels it.
func (v walkView) ask(a asks.Ask, qs []shownQuestion, name string, now time.Time) {
	header := name
	if a.Skill != "" {
		header += v.faint.Render(" · at " + a.Skill)
	}

	header += v.faint.Render(" · asked " + now.Sub(a.AskedAt).Round(time.Minute).String() + " ago")

	v.println(" " + asks.AskIcon + " " + header)

	if a.URL != "" {
		v.println(" " + v.faint.Render(a.URL))
	}

	context := []string{a.Context}
	for _, q := range qs {
		context = append(context, q.Context)
	}

	if text := strings.TrimSpace(strings.Join(context, " ")); text != "" {
		v.context(text)
	}

	for _, q := range qs {
		fmt.Fprintln(v.w)

		lead := " " + strconv.Itoa(q.N) + ". "
		v.lines(v.words(q.Text, v.bold), lead, strings.Repeat(" ", len(lead)), v.bold)

		for i, o := range q.Options {
			words := v.words(o, v.plain)
			if i == 0 {
				mark := "(default)"
				if q.Note != "" {
					mark = "(default, " + q.Note + ")"
				}

				marked := v.words(mark, v.faint)
				marked[0].gap = 2
				words = append(words, marked...)
			}

			lead := optionIndent + string(rune('a'+i)) + ") "
			v.lines(words, lead, strings.Repeat(" ", len(lead)), v.plain)
		}
	}
}

// labelled is a context sentence opening with a one-word label, "Log: ...".
var labelled = regexp.MustCompile(`^([A-Za-z][\w-]{0,11}):\s+(\S.*)$`)

// context prints one sentence per line; labelled sentences become rows
// with their labels in a column.
func (v walkView) context(text string) {
	sentences := asks.Sentences(text)

	col := 0

	for _, s := range sentences {
		if m := labelled.FindStringSubmatch(s); m != nil {
			col = max(col, len(m[1]))
		}
	}

	for _, s := range sentences {
		m := labelled.FindStringSubmatch(s)
		if m == nil {
			v.lines(v.words(s, v.faint), contextIndent, contextIndent, v.faint)

			continue
		}

		lead := contextIndent + "· " + fmt.Sprintf("%-*s", col, strings.ToLower(m[1])) + "  "
		v.lines(v.words(m[2], v.faint), lead, strings.Repeat(" ", render.VisibleWidth(lead)), v.faint)
	}
}

// a word is one token as shown (for width) and as printed (styled, linked);
// gap is the spaces before it on a line it does not open.
type word struct {
	shown, styled string
	gap           int
}

func (v walkView) words(text string, style lipgloss.Style) []word {
	var out []word

	for w := range strings.FieldsSeq(text) {
		shown, styled := v.token(w, style)
		out = append(out, word{shown: shown, styled: styled, gap: 1})
	}

	return out
}

// lines prints words at the terminal's width, never breaking inside a word,
// so a url stays whole and clickable: lead opens the first line, indent the
// rest. A word wider than a line gets a line of its own.
func (v walkView) lines(words []word, lead, indent string, style lipgloss.Style) {
	var line strings.Builder

	prefix, cols := style.Render(lead), render.VisibleWidth(lead)
	open := true

	for _, w := range words {
		width := render.VisibleWidth(w.shown)

		if !open && v.width > 0 && cols+w.gap+width > v.width {
			v.println(prefix + line.String())
			line.Reset()

			prefix, cols, open = indent, render.VisibleWidth(indent), true
		}

		if !open {
			line.WriteString(style.Render(strings.Repeat(" ", w.gap)))

			cols += w.gap
		}

		line.WriteString(w.styled)

		cols += width
		open = false
	}

	if !open {
		v.println(prefix + line.String())
	}
}

func (v walkView) println(s string) { fmt.Fprintln(v.w, s) }

var (
	urlToken = regexp.MustCompile(`^(https?://\S+?)([.,;:)]*)$`)
	shaToken = regexp.MustCompile(`^([0-9a-f]{7,40})([.,;:)]*)$`)
	hasDigit = regexp.MustCompile(`\d`)
	hasHexAZ = regexp.MustCompile(`[a-f]`)
)

const (
	// shaShown is how many characters of a sha the walk shows.
	shaShown = 8
	// urlLong is the width past which a linked url shows as a short label.
	urlLong = 32
)

// token is a word as shown, and as printed with its style and link. A sha
// shows shortened; a long url shows as its last path segments, but only
// linked, since a pipe needs the whole address.
func (v walkView) token(w string, style lipgloss.Style) (string, string) {
	if m := shaToken.FindStringSubmatch(w); len(m) == 3 && hasDigit.MatchString(m[1]) && hasHexAZ.MatchString(m[1]) {
		shown := m[1][:min(len(m[1]), shaShown)] + m[2]

		return shown, style.Render(shown)
	}

	if m := urlToken.FindStringSubmatch(w); m != nil && v.links {
		label := m[1]
		if len(label) > urlLong {
			label = "↗ " + shortURL(m[1])
		}

		return label + m[2], render.Hyperlink(m[1], style.Render(label)) + style.Render(m[2])
	}

	return w, style.Render(w)
}

// shortURL is a url's last two path segments, else its host and path.
func shortURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || segs[0] == "" {
		return u.Host + strings.TrimRight(u.Path, "/")
	}

	return strings.Join(segs[len(segs)-2:], "/")
}
