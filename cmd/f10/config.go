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
		// the freshest few, not the whole dir - it grows without bound,
		// and --json still carries the full list
		const recent = 3

		shown := v.Plans
		if len(shown) > recent {
			shown = shown[:recent]
		}

		list := strings.Join(shown, ", ")
		if len(v.Plans) > recent {
			list += ", ..."
		}

		headerRow(w, "plans", fmt.Sprintf("%d in %s: %s", len(v.Plans), v.PlansDir, list), limit, plain)
	}

	fmt.Fprintln(w)
	renderFields(w, v.Fields, limit)

	if len(v.Unrecognized) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, faint.Render("unrecognized sections (shown, never dropped):"))
		renderFields(w, v.Unrecognized, limit)
	}
}

// headerRow prints one label/value line, wrapping the value inside its own
// column.
func headerRow(w io.Writer, label, value string, limit int, style lipgloss.Style) {
	segments := wrap(value, limit-headerIndent)
	fmt.Fprintf(w, "%-*s%s\n", headerIndent, label, style.Render(segments[0]))

	for _, s := range segments[1:] {
		fmt.Fprintf(w, "%-*s%s\n", headerIndent, "", style.Render(s))
	}
}

func renderFields(w io.Writer, fields []facts.Field, limit int) {
	nameW, originW := 0, 0
	for _, f := range fields {
		nameW = max(nameW, len(f.Name))
		originW = max(originW, len(f.Origin))
	}

	valueIndent := nameW + 2 + originW + 2

	for _, f := range fields {
		origin := fmt.Sprintf("%-*s", originW, f.Origin)
		value := f.Value

		if value == "" {
			value = "-"
		}

		first := true

		for line := range strings.SplitSeq(value, "\n") {
			for _, seg := range wrap(line, limit-valueIndent) {
				if first {
					fmt.Fprintf(w, "%-*s  %s  %s\n", nameW, f.Name, styleOrigin(f.Origin).Render(origin), seg)

					first = false

					continue
				}

				// wrapped and prose continuation lines alike sit
				// under the value column, never under the labels
				fmt.Fprintf(w, "%-*s%s\n", valueIndent, "", seg)
			}
		}
	}
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
