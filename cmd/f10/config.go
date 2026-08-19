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

// renderHuman prints the aligned two-column header and the field table. The
// origin column is the feature; lipgloss colors degrade to plain text when
// stdout is not a tty or NO_COLOR is set - termenv handles both.
func renderHuman(w io.Writer, v *view) {
	faint := lipgloss.NewStyle().Faint(true)

	fmt.Fprintln(w, "checkout root   "+v.CheckoutRoot)
	fmt.Fprintln(w, "storage root    "+v.StorageRoot)
	fmt.Fprintln(w, "instructions    "+v.Instructions)

	if v.NestedIgnored != "" {
		note := "ignoring nested " + v.NestedIgnored + " - resolution is anchored to the checkout root"
		fmt.Fprintln(w, "note            "+faint.Render(note))
	}

	if v.Remote != "" {
		fmt.Fprintln(w, "remote          "+v.Remote)
	}

	if len(v.Plans) > 0 {
		fmt.Fprintf(w, "plans           %d in %s: %s\n", len(v.Plans), v.PlansDir, strings.Join(v.Plans, ", "))
	}

	fmt.Fprintln(w)
	renderFields(w, v.Fields)

	if len(v.Unrecognized) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, faint.Render("unrecognized sections (shown, never dropped):"))
		renderFields(w, v.Unrecognized)
	}
}

func renderFields(w io.Writer, fields []facts.Field) {
	nameW, originW := 0, 0
	for _, f := range fields {
		nameW = max(nameW, len(f.Name))
		originW = max(originW, len(f.Origin))
	}

	for _, f := range fields {
		origin := fmt.Sprintf("%-*s", originW, f.Origin)
		value := f.Value

		if value == "" {
			value = "-"
		}

		lines := strings.Split(value, "\n")
		fmt.Fprintf(w, "%-*s  %s  %s\n", nameW, f.Name, styleOrigin(f.Origin).Render(origin), lines[0])

		// continuation lines of a prose value sit under the value column
		for _, line := range lines[1:] {
			fmt.Fprintf(w, "%-*s  %-*s  %s\n", nameW, "", originW, "", line)
		}
	}
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
