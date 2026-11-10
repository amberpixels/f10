package config

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/mdline"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/scope"
)

const (
	PageMin = 60
	PageMax = 100
)

type headerLine struct {
	label, value string
	style        lipgloss.Style
}

// RenderHuman prints the header and the field table; the origin column is
// the feature. limit is the terminal width, 0 when piped.
func RenderHuman(w io.Writer, v *View, limit int) {
	faint := lipgloss.NewStyle().Faint(true)
	plain := lipgloss.NewStyle()

	header := []headerLine{
		{"checkout root", v.CheckoutRoot, plain},
		{"storage root", v.StorageRoot, plain},
	}

	header = append(header, headerLine{"instructions", v.Instructions, plain})

	if v.NestedIgnored != "" {
		note := "ignoring nested " + v.NestedIgnored + " - the layout is anchored to the checkout root"
		header = append(header, headerLine{"note", note, faint})
	}

	if v.Remote != "" {
		header = append(header, headerLine{"remote", v.Remote, plain})
	}

	if v.IDPrefix != "" {
		header = append(header, headerLine{"task ids", v.IDPrefix + "-<n>", plain})

		// a derived prefix holds only until something declares one, and the
		// plan files it has already named will not follow a change
		if v.IDPrefixDerived {
			header = append(header, headerLine{"", v.IDPrefixOrigin.String() + " - `f10 init` declares it", faint})
		}
	}

	// -C takes a path with nothing configured; a bare name needs these
	projectLookup := "paths only - set " + projects.RootsEnv + " to find -C by name"
	if len(v.Roots) > 0 {
		projectLookup = strings.Join(v.Roots, ", ")
	}

	header = append(header, headerLine{"projects", projectLookup, faint})

	if len(v.Plans) > 0 {
		// the freshest few, not the whole dir, which grows without bound;
		// --json still carries the full list
		const recent = 3

		shown := v.Plans
		if len(shown) > recent {
			shown = shown[:recent]
		}

		list := strings.Join(shown, ", ")
		if len(v.Plans) > recent {
			list += ", ..."
		}

		header = append(header, headerLine{"plans", fmt.Sprintf("%d: %s", len(v.Plans), list), plain})
	}

	factTable := layoutFields(factFields(v.Facts), limit)
	sections := layoutFields(v.Fields, limit)
	unrecognized := layoutFields(v.Unrecognized, limit)

	widest := max(factTable.width(), sections.width(), unrecognized.width())
	for _, h := range header {
		widest = max(widest, scope.HeaderIndent+lipgloss.Width(h.value))
	}

	page := PageWidth(widest, limit)

	for _, h := range header {
		scope.HeaderRow(w, h.label, h.value, limit, h.style)
	}

	fmt.Fprintln(w)
	banner(w, "facts the binary acts on", page)
	renderFields(w, factTable, page)

	fmt.Fprintln(w)
	banner(w, "project.md sections", page)
	renderFields(w, sections, page)

	if len(v.Unrecognized) > 0 {
		fmt.Fprintln(w)
		banner(w, "unrecognized sections (shown, never dropped)", page)
		renderFields(w, unrecognized, page)
	}

	renderProblems(w, v.Problems)
}

func PageWidth(widest, limit int) int {
	if limit <= 0 {
		return 0
	}

	return min(limit, max(PageMin, min(widest, PageMax)))
}

func banner(w io.Writer, title string, limit int) {
	if limit > 0 {
		title = "── " + title + " " + rule(limit-len(title)-4)
	}

	fmt.Fprintln(w, lipgloss.NewStyle().Faint(true).Render(title))
}

func factFields(list []facts.Fact) []facts.Field {
	fields := make([]facts.Field, 0, len(list))
	for _, f := range list {
		fields = append(fields, facts.Field{Name: f.Key, Value: f.Value, Origin: f.Origin})
	}

	return fields
}

// renderProblems lists what is wrong with the declaration, errors first:
// an error stops every command but config and init.
func renderProblems(w io.Writer, problems []facts.Problem) {
	if len(problems) == 0 {
		return
	}

	fmt.Fprintln(w)

	for _, p := range problems {
		if p.Error {
			fmt.Fprintln(w, lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("error: "+p.Message))
		}
	}

	for _, p := range problems {
		if !p.Error {
			fmt.Fprintln(w, lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("warning: "+p.Message))
		}
	}
}

// stanzaIndent and stanzaRightPad are a prose block's margins, so text
// never touches the page's edge.
const (
	stanzaIndent   = 1
	stanzaRightPad = 2
)

type entry struct {
	field  facts.Field
	origin string
	value  string
	pairs  [][2]string
}

// A fieldLayout splits a field list by render form once, so the page width
// is measured on exactly what prints.
type fieldLayout struct {
	rows, blocks   []entry
	nameW, originW int
	blockNameW     int
}

// width is the widest inline row the layout prints. Stanzas wrap to the
// page, key/value sub-rows included, so they do not count.
func (l fieldLayout) width() int {
	widest := 0
	for _, e := range l.rows {
		widest = max(widest, l.nameW+2+l.originW+2+lipgloss.Width(e.value))
	}

	return widest
}

// layoutFields puts scalar values that fit in rows and the rest in stanzas:
// one table cannot serve both, since a three-column prefix leaves a
// paragraph a ribbon of the terminal and a paragraph gives a scalar nothing.
func layoutFields(fields []facts.Field, limit int) fieldLayout {
	origins := displayOrigins(fields)

	var l fieldLayout
	for i, f := range fields {
		l.nameW = max(l.nameW, len(f.Name))
		l.originW = max(l.originW, len(origins[i]))
	}

	for i, f := range fields {
		value := f.Value
		if value == "" {
			value = "-"
		}

		e := entry{field: f, origin: origins[i], value: value}
		if !strings.Contains(value, "\n") && (limit <= 0 || l.nameW+2+l.originW+2+len(value) <= limit) {
			l.rows = append(l.rows, e)

			continue
		}

		e.pairs = keyValues(value)
		l.blocks = append(l.blocks, e)
		l.blockNameW = max(l.blockNameW, len(f.Name))
	}

	return l
}

func renderFields(w io.Writer, l fieldLayout, page int) {
	for _, e := range l.rows {
		paddedOrigin := fmt.Sprintf("%-*s", l.originW, e.origin)
		fmt.Fprintf(
			w,
			"%-*s  %s  %s\n",
			l.nameW,
			e.field.Name,
			styleOrigin(e.field.Origin).Render(paddedOrigin),
			e.value,
		)
	}

	for i, e := range l.blocks {
		if i > 0 || len(l.rows) > 0 {
			fmt.Fprintln(w)
		}

		stanzaHeader(w, e.field.Name, e.origin, styleOrigin(e.field.Origin), page, l.blockNameW)

		if e.pairs != nil {
			renderPairs(w, e.pairs, page)
		} else {
			renderProse(w, e.value, page)
		}
	}
}

// keyValues reads a body made only of `- **Label** - value` items as pairs;
// nil when any line is something else, so ordinary prose keeps its rendering.
func keyValues(value string) [][2]string {
	var pairs [][2]string

	for line := range strings.SplitSeq(value, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		item, ok := mdline.Item(line)
		if !ok || !strings.HasPrefix(item, "**") {
			return nil
		}

		label, v, ok := mdline.Cut(item, " - ", ":")
		if !ok {
			return nil
		}

		pairs = append(pairs, [2]string{label, mdline.Unmark(v)})
	}

	return pairs
}

func pairLabelWidth(pairs [][2]string) int {
	labelW := 0
	for _, p := range pairs {
		labelW = max(labelW, lipgloss.Width(p[0]))
	}

	return labelW
}

func renderPairs(w io.Writer, pairs [][2]string, page int) {
	labelW := pairLabelWidth(pairs)
	col := stanzaIndent + labelW + 2

	for _, p := range pairs {
		segments := render.Wrap(p[1], page-col-stanzaRightPad)
		fmt.Fprintf(w, "%-*s%-*s  %s\n", stanzaIndent, "", labelW, p[0], segments[0])

		for _, s := range segments[1:] {
			fmt.Fprintf(w, "%-*s%s\n", col, "", s)
		}
	}
}

// stanzaHeader draws a section header as a rule carrying the name and
// origin, so each prose block gets a crisp edge; piped output keeps it plain.
func stanzaHeader(w io.Writer, name, origin string, originStyle lipgloss.Style, limit, nameW int) {
	bold := lipgloss.NewStyle().Bold(true)
	faint := lipgloss.NewStyle().Faint(true)
	padded := fmt.Sprintf("%-*s", nameW, name)

	if limit <= 0 {
		fmt.Fprintf(w, "%s  %s\n", padded, origin)
		return
	}

	used := 3 + nameW + 3 + len(origin) + 1 // "── ", " · " and the trailing space, in display cells
	fmt.Fprintln(w,
		faint.Render("── ")+bold.Render(padded)+faint.Render(" · ")+
			originStyle.Render(origin)+" "+faint.Render(rule(limit-used)))
}

func rule(n int) string {
	if n < 1 {
		return ""
	}

	return strings.Repeat("─", n)
}

// renderProse wraps each line; bullets hang so continuations align with
// their text.
func renderProse(w io.Writer, value string, limit int) {
	for line := range strings.SplitSeq(value, "\n") {
		hang := 0
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			hang = 2
		}

		first := true

		for _, seg := range render.Wrap(line, limit-stanzaIndent-hang-stanzaRightPad) {
			indent := stanzaIndent
			if !first {
				indent += hang
			}

			fmt.Fprintf(w, "%-*s%s\n", indent, "", seg)

			first = false
		}
	}
}

// displayOrigins drops the layer qualifier on "declared" unless more than
// one declaring layer is in play; the full origin stays in --json.
func displayOrigins(fields []facts.Field) []string {
	declared := map[facts.Origin]bool{}
	for _, f := range fields {
		if f.Origin.Declared() {
			declared[f.Origin] = true
		}
	}

	origins := make([]string, len(fields))
	for i, f := range fields {
		origins[i] = f.Origin.String()
		if len(declared) == 1 && f.Origin.Declared() {
			origins[i] = "declared"
		}
	}

	return origins
}

// styleOrigin keeps detection, declaration and defaults tellable apart at a
// glance in the origin column.
func styleOrigin(origin facts.Origin) lipgloss.Style {
	switch origin.Kind {
	case facts.OriginDetected:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	case facts.OriginDeclared:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	case facts.OriginDefault, facts.OriginJudgment:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	default: // absent
		return lipgloss.NewStyle().Faint(true)
	}
}
