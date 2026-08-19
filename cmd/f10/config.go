package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/internal/facts"
	"github.com/amberpixels/f10/internal/probe"
	"github.com/amberpixels/f10/internal/resolve"
)

// A view is the one model both outputs render - the human table and --json
// marshal the same struct, so they cannot disagree.
type view struct {
	CheckoutRoot  string        `json:"checkoutRoot"`
	StorageRoot   string        `json:"storageRoot"`
	Instructions  string        `json:"instructions"` // source description, resolve.sh wording
	NestedIgnored string        `json:"nestedIgnored,omitempty"`
	Remote        string        `json:"remote,omitempty"`
	PlansDir      string        `json:"plansDir"`
	Plans         []string      `json:"plans,omitempty"`
	Fields        []facts.Field `json:"fields"`
	Unrecognized  []facts.Field `json:"unrecognized,omitempty"`
	Layering      string        `json:"layering"`
	Visibility    string        `json:"visibility"`
	Storage       string        `json:"storage"`
	TrackerKind   string        `json:"trackerKind,omitempty"`
}

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "print the effective configuration, with the origin of every value",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit the same view as JSON"},
		},
		Action: runConfig,
	}
}

func runConfig(ctx context.Context, cmd *cli.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	res := resolve.Resolve(ctx, cwd)
	pb := probe.Run(ctx, res)
	eff := facts.Build(res, pb)

	v := &view{
		CheckoutRoot:  res.CheckoutRoot,
		StorageRoot:   res.StorageRoot,
		Instructions:  res.Source,
		NestedIgnored: res.NestedIgnored,
		Remote:        pb.RemoteURL,
		PlansDir:      pb.PlansDir,
		Plans:         pb.Plans,
		Fields:        eff.Fields,
		Unrecognized:  eff.Unrecognized,
		Layering:      eff.Layering,
		Visibility:    eff.Visibility,
		Storage:       eff.Storage,
		TrackerKind:   eff.TrackerKind,
	}

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("encoding view: %w", err)
		}

		return nil
	}

	renderHuman(cmd.Writer, v)

	return nil
}

// headerIndent is the value column of the header block: the widest label,
// "checkout root", plus the gap.
const headerIndent = 16

// renderHuman prints the aligned two-column header and the field table. The
// origin column is the feature; lipgloss colors degrade to plain text when
// stdout is not a tty or NO_COLOR is set - termenv handles both. Values
// longer than the terminal wrap inside their own column, never under the
// labels.
func renderHuman(w io.Writer, v *view) {
	faint := lipgloss.NewStyle().Faint(true)
	plain := lipgloss.NewStyle()
	limit := termWidth(w)

	headerRow(w, "checkout root", v.CheckoutRoot, limit, plain)
	headerRow(w, "storage root", v.StorageRoot, limit, plain)
	headerRow(w, "instructions", v.Instructions, limit, plain)

	if v.NestedIgnored != "" {
		note := "ignoring nested " + v.NestedIgnored + " - resolution is anchored to the checkout root"
		headerRow(w, "note", note, limit, faint)
	}

	if v.Remote != "" {
		headerRow(w, "remote", v.Remote, limit, plain)
	}

	if len(v.Plans) > 0 {
		// the freshest few, not the whole dir - it grows without bound.
		// No directory here: it is always <storage root>/plans, and
		// --json still carries both the path and the full list
		const recent = 3

		shown := v.Plans
		if len(shown) > recent {
			shown = shown[:recent]
		}

		list := strings.Join(shown, ", ")
		if len(v.Plans) > recent {
			list += ", ..."
		}

		headerRow(w, "plans", fmt.Sprintf("%d: %s", len(v.Plans), list), limit, plain)
	}

	fmt.Fprintln(w)
	renderFields(w, v.Fields, limit)

	if len(v.Unrecognized) > 0 {
		banner := "unrecognized sections (shown, never dropped)"

		fmt.Fprintln(w)

		if limit > 0 {
			banner = "── " + banner + " " + rule(limit-len(banner)-4)
		}

		fmt.Fprintln(w, faint.Render(banner))
		renderFields(w, v.Unrecognized, limit)
	}
}

// headerRow prints one label/value line, the label dimmed so values carry
// the eye, wrapping the value inside its own column.
func headerRow(w io.Writer, label, value string, limit int, style lipgloss.Style) {
	faint := lipgloss.NewStyle().Faint(true)

	segments := wrap(value, limit-headerIndent)
	fmt.Fprintf(w, "%s%s\n", faint.Render(fmt.Sprintf("%-*s", headerIndent, label)), style.Render(segments[0]))

	for _, s := range segments[1:] {
		fmt.Fprintf(w, "%-*s%s\n", headerIndent, "", style.Render(s))
	}
}

// stanzaIndent and stanzaRightPad are a prose block's margins: a hair of
// left inset under the header, and enough right inset that text never
// touches the terminal's edge.
const (
	stanzaIndent   = 1
	stanzaRightPad = 2
)

// renderFields prints scalar values as aligned rows and prose values as
// stanzas - a `name  origin` header with the value as an indented block
// using the full width. One table cannot serve both: a three-column prefix
// leaves a paragraph a ribbon of the terminal, and a paragraph gives a
// scalar nothing back.
func renderFields(w io.Writer, fields []facts.Field, limit int) {
	origins := displayOrigins(fields)

	nameW, originW := 0, 0
	for i, f := range fields {
		nameW = max(nameW, len(f.Name))
		originW = max(originW, len(origins[i]))
	}

	// partition by render form: every inline row first, so scalars scan as
	// one table, then the prose blocks behind a bare rule - the seam
	// between the two groups is part of the layout
	type entry struct {
		field  facts.Field
		origin string
		value  string
	}

	var rows, blocks []entry

	for i, f := range fields {
		value := f.Value
		if value == "" {
			value = "-"
		}

		e := entry{field: f, origin: origins[i], value: value}
		if !strings.Contains(value, "\n") && (limit <= 0 || nameW+2+originW+2+len(value) <= limit) {
			rows = append(rows, e)
		} else {
			blocks = append(blocks, e)
		}
	}

	for _, e := range rows {
		paddedOrigin := fmt.Sprintf("%-*s", originW, e.origin)
		fmt.Fprintf(w, "%-*s  %s  %s\n", nameW, e.field.Name, styleOrigin(e.field.Origin).Render(paddedOrigin), e.value)
	}

	blockNameW := 0
	for _, e := range blocks {
		blockNameW = max(blockNameW, len(e.field.Name))
	}

	for i, e := range blocks {
		if i > 0 || len(rows) > 0 {
			fmt.Fprintln(w)
		}

		stanzaHeader(w, e.field.Name, e.origin, styleOrigin(e.field.Origin), limit, blockNameW)
		renderProse(w, e.value, limit)
	}
}

// stanzaHeader draws a section header as a horizontal rule carrying the
// name and origin - `── Roles · declared ─────` - so each prose block gets
// a crisp edge to scan by. Names pad to nameW so the dot and the origin
// align down the page. Piped output keeps the plain two-word header.
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

// rule is n cells of horizontal line, none when the header already fills
// the width.
func rule(n int) string {
	if n < 1 {
		return ""
	}

	return strings.Repeat("─", n)
}

// renderProse prints a value block: each logical line wrapped to the
// width, bullets keeping a hanging indent so continuations align with
// their text.
func renderProse(w io.Writer, value string, limit int) {
	for line := range strings.SplitSeq(value, "\n") {
		hang := 0
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			hang = 2
		}

		first := true

		for _, seg := range wrap(line, limit-stanzaIndent-hang-stanzaRightPad) {
			indent := stanzaIndent
			if !first {
				indent += hang
			}

			fmt.Fprintf(w, "%-*s%s\n", indent, "", seg)

			first = false
		}
	}
}

// displayOrigins shortens origin labels for display: the layer qualifier
// on "declared" earns its width only when more than one declaring layer is
// actually in play. The full origin stays in --json.
func displayOrigins(fields []facts.Field) []string {
	declared := map[string]bool{}
	for _, f := range fields {
		if strings.HasPrefix(f.Origin, "declared") {
			declared[f.Origin] = true
		}
	}

	origins := make([]string, len(fields))
	for i, f := range fields {
		origins[i] = f.Origin
		if len(declared) == 1 && strings.HasPrefix(f.Origin, "declared") {
			origins[i] = "declared"
		}
	}

	return origins
}

// wrap greedily breaks text at spaces so every line fits width. Width 0 or
// below (terminal unknown, or narrower than the indent) disables wrapping;
// a single token longer than the width is hard-cut rather than overflowed.
func wrap(text string, width int) []string {
	if width <= 0 || len(text) <= width {
		return []string{text}
	}

	var lines []string

	for len(text) > width {
		cut := strings.LastIndex(text[:width+1], " ")
		if cut <= 0 {
			cut = width
		}

		lines = append(lines, strings.TrimRight(text[:cut], " "))
		text = strings.TrimLeft(text[cut:], " ")
	}

	return append(lines, text)
}

// termWidth is the width wrapping targets: the terminal's when stdout is
// one, otherwise 0 - piped output stays unwrapped for greppability.
func termWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}

	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}

	return width
}

// styleOrigin colors the origin cell: detection, declaration and defaults
// must be tellable apart at a glance - that column is the whole point.
func styleOrigin(origin string) lipgloss.Style {
	switch {
	case origin == facts.OriginDetected:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	case strings.HasPrefix(origin, "declared"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	case origin == facts.OriginDefault:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	default: // absent
		return lipgloss.NewStyle().Faint(true)
	}
}
