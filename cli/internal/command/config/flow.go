package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The flow verb lists the declared flows, the same list an unknown --flow
// name fails with.

func FlowCommand() *cli.Command {
	return &cli.Command{
		Name:      "flow",
		Usage:     "list the flows project.md declares and what selects each, with their entries under the run's pace",
		ArgsUsage: "[<name>]",
		Description: "With a name, that flow alone, one entry per line: the entries the pace skips marked, and each " +
			"dial with its origin; this is the list /f10:ship runs. The pace is --pace, else the branch's record, " +
			"else the project's pace:, else the user's; with none, every flow runs as declared, " +
			"an optional CI review (ci?) settled.",
		Flags: []cli.Flag{
			runplan.PaceFlag(),
			ui.JSONFlag("emit the same list as JSON"),
		},
		Action: runFlow,
	}
}

func runFlow(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{
		Pace:   cmd.String("pace"),
		Main:   cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Branch: gitx.CurrentBranch(ctx, t.Layout.CheckoutRoot),
	})
	if err != nil {
		return err
	}

	v := runplan.PacedFlowsView(t.Effective, t.Effective.PaceSet, run.Pace)

	if name := strings.ToLower(strings.TrimSpace(cmd.Args().First())); name != "" {
		if _, err := runplan.PacedFlow(t.Effective, "", name); err != nil {
			return err
		}

		i := slices.IndexFunc(v.Flows, func(r runplan.FlowRow) bool { return strings.EqualFold(r.Name, name) })
		if i < 0 {
			return fmt.Errorf("no flow %s", name)
		}

		if v.Flows[i].Broken != "" {
			return errors.New(v.Flows[i].Broken)
		}

		v.Flows = v.Flows[i : i+1]

		if !cmd.Bool("json") {
			return writeFlowEntries(cmd.Writer, v)
		}
	}

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, v)
	}

	return runplan.WriteFlows(cmd.Writer, v)
}

// writeFlowEntries is one flow, an entry per line with its dials, then the
// pace's finish; the list /f10:ship runs.
func writeFlowEntries(w io.Writer, v runplan.FlowView) error {
	f := v.Flows[0]

	var b strings.Builder

	if v.Pace != "" {
		fmt.Fprintf(&b, "pace: %s (%s)\n", v.Pace, v.PaceOrigin)
	}

	if v.Ceiling != "" {
		fmt.Fprintf(&b, "ceiling: %s\n", v.Ceiling)
	}

	if f.Resolved == nil {
		for _, e := range flow.Zip(f.Entries, f.Labels).Written() {
			fmt.Fprintln(&b, e)
		}

		_, err := io.WriteString(w, b.String())

		return err
	}

	width := len(facts.PaceFinishKey)
	for _, e := range f.Resolved.Entries {
		width = max(width, len(e.Parsed().Written()))
	}

	for _, e := range f.Resolved.Entries {
		entry := e.Parsed().Written()

		switch {
		case e.Skipped != "":
			fmt.Fprintf(&b, "%-*s  skipped: %s\n", width, entry, e.Skipped)
		case len(e.Dials) > 0:
			fmt.Fprintf(&b, "%-*s  %s\n", width, entry, dialsShown(e.Dials))
		default:
			fmt.Fprintln(&b, entry)
		}
	}

	if fin := f.Resolved.Finish; fin != nil {
		fmt.Fprintf(&b, "%-*s  %s %s\n", width, facts.PaceFinishKey, fin.Value, dialSource(*fin))
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// dialsShown is `rounds 1, effort low (pace fast (built-in))`, each origin
// once after the dials it set.
func dialsShown(dials []facts.Dial) string {
	var parts []string

	for i, d := range dials {
		part := d.Name + " " + d.Value
		if d.Capped != "" || i == len(dials)-1 || dials[i+1].Origin != d.Origin || dials[i+1].Capped != "" {
			part += " " + dialSource(d)
		}

		parts = append(parts, part)
	}

	return strings.Join(parts, ", ")
}

// dialSource is a dial's origin in parentheses, with what the pace had
// when the ceiling capped it.
func dialSource(d facts.Dial) string {
	if d.Capped != "" {
		return "(" + d.Origin + "; " + d.Capped + ")"
	}

	return "(" + d.Origin + ")"
}

func FlowFlags(cmd *cli.Command) (string, error) {
	name := strings.ToLower(strings.TrimSpace(cmd.String("flow")))

	if cmd.Bool("local") {
		if name != "" && name != runplan.LocalFlow {
			return "", fmt.Errorf("--local and --flow %s name two flows", name)
		}

		name = runplan.LocalFlow
	}

	return name, nil
}

func FlowFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "flow",
		Usage: "run the declared flow `NAME` instead of default; f10 flow lists them",
	}
}

func CompleteFlow(ctx context.Context, cmd *cli.Command) {
	if !CompletingFlow(os.Args) {
		cli.DefaultCompleteWithFlags(ctx, cmd)

		return
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return
	}

	for _, name := range t.Effective.FlowNames() {
		fmt.Fprintln(cmd.Root().Writer, name)
	}
}

func CompletingFlow(args []string) bool {
	const marker = "--generate-shell-completion"

	if i := slices.Index(args, marker); i > 0 {
		args = args[:i]
	}

	return len(args) > 0 && args[len(args)-1] == "--flow"
}
