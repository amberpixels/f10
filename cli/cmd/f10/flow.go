package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// The flow verb lists the declared flows and what selects each, the same
// list an unknown --flow name fails with.

// cfgFlow is the flow a started task runs, under `branch.<name>.` beside
// f10-after, for status and for a rerun of drive.
const cfgFlow = "f10-flow"

func flowCommand() *cli.Command {
	return &cli.Command{
		Name:  "flow",
		Usage: "list the flows project.md declares and what selects each",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit the same list as JSON"},
		},
		Action: runFlow,
	}
}

// A flowRow is one declared flow and the commands that run it.
type flowRow struct {
	Name     string   `json:"name"`
	Entries  []string `json:"entries"`
	Selected []string `json:"selectedBy"`
}

type flowView struct {
	Flows       []flowRow         `json:"flows"`
	DriveFinish bool              `json:"driveFinish"`
	FlowByArea  map[string]string `json:"flowByArea,omitempty"`
}

func runFlow(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	v := flowsView(t.eff)

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("encoding flows: %w", err)
		}

		return nil
	}

	return writeFlows(cmd.Writer, v)
}

func flowsView(eff *facts.Effective) flowView {
	v := flowView{DriveFinish: eff.DriveFinish, FlowByArea: eff.FlowByArea}

	for _, name := range eff.FlowNames() {
		v.Flows = append(v.Flows, flowRow{Name: name, Entries: eff.Flows[name], Selected: selectors(name)})
	}

	return v
}

// selectors is what runs a flow: default unasked, local by --local too,
// any other by name.
func selectors(name string) []string {
	switch name {
	case "default":
		return []string{"f10 start", "f10 drive"}
	case "local":
		return []string{"--local", "--flow local"}
	default:
		return []string{"--flow " + name}
	}
}

// writeFlows prints a row per flow, its entries, then what selects it,
// and drive's finish setting under them.
func writeFlows(w io.Writer, v flowView) error {
	width := len("drive_finish")
	for _, f := range v.Flows {
		width = max(width, len(f.Name))
	}

	var b strings.Builder

	for _, f := range v.Flows {
		fmt.Fprintf(&b, "%-*s  %s\n%-*s  (%s)\n", width, f.Name, strings.Join(f.Entries, " "),
			width, "", strings.Join(f.Selected, ", "))
	}

	finish := "off: f10 drive stops after ship"
	if v.DriveFinish {
		finish = "on: f10 drive ends each task with finish"
	}

	fmt.Fprintf(&b, "%-*s  %s\n", width, "drive_finish", finish)

	for _, area := range sortedKeys(v.FlowByArea) {
		fmt.Fprintf(&b, "%-*s  %s runs %s\n", width, "flow_by_area", area, v.FlowByArea[area])
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// A shipFlow is the flow a started task ships through: its name, "" for
// default, and whether it writes a plan file.
type shipFlow struct {
	name     string
	planless bool
}

// word is how /f10:ship is told the flow: `local: ` before the id, nothing
// for default.
func (f shipFlow) word() string {
	if f.name == "" {
		return ""
	}

	return f.name + ": "
}

// recorded is the name written on the branch.
func (f shipFlow) recorded() string {
	if f.name == "" {
		return "default"
	}

	return f.name
}

// flowFlags reads --flow and --local into one name, "" for default:
// --local is --flow local.
func flowFlags(cmd *cli.Command) (string, error) {
	name := strings.ToLower(strings.TrimSpace(cmd.String("flow")))

	if cmd.Bool("local") {
		if name != "" && name != "local" {
			return "", fmt.Errorf("--local and --flow %s name two flows", name)
		}

		name = "local"
	}

	return name, nil
}

// resolveShipFlow is the named flow as declared; a name project.md does
// not declare fails with the list f10 flow prints.
func resolveShipFlow(eff *facts.Effective, name string) (shipFlow, error) {
	lookup := strings.ToLower(name)
	if lookup == "" {
		lookup = "default"
	}

	flow, ok := eff.Flow(lookup)
	if !ok {
		var b strings.Builder
		_ = writeFlows(&b, flowsView(eff))

		return shipFlow{}, fmt.Errorf("--flow %s: project.md declares no such flow; it declares:\n%s", name,
			strings.TrimRight(b.String(), "\n"))
	}

	if lookup == "default" {
		lookup = ""
	}

	return shipFlow{name: lookup, planless: facts.Planless(flow)}, nil
}

// flowFlag is the --flow flag start and drive share.
func flowFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "flow",
		Usage: "run the declared flow `NAME` instead of default; f10 flow lists them",
	}
}

// completeFlow answers shell completion: the declared flow names after
// --flow, the default flag and command suggestions otherwise. Outside a
// project it suggests nothing for --flow.
func completeFlow(ctx context.Context, cmd *cli.Command) {
	if !completingFlow(os.Args) {
		cli.DefaultCompleteWithFlags(ctx, cmd)

		return
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return
	}

	for _, name := range t.eff.FlowNames() {
		fmt.Fprintln(cmd.Root().Writer, name)
	}
}

// completingFlow reports a completion request whose word before the
// cursor is --flow.
func completingFlow(args []string) bool {
	const marker = "--generate-shell-completion"

	if i := slices.Index(args, marker); i > 0 {
		args = args[:i]
	}

	return len(args) > 0 && args[len(args)-1] == "--flow"
}
