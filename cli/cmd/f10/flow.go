package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The flow verb lists the declared flows, the same list an unknown --flow
// name fails with.

// cfgFlow is the flow a started task runs, under `branch.<name>.` beside
// f10-after, for status and for a rerun of drive.
const cfgFlow = "f10-flow"

func flowCommand() *cli.Command {
	return &cli.Command{
		Name:      "flow",
		Usage:     "list the flows project.md declares and what selects each, with their entries under the run's pace",
		ArgsUsage: "[<name>]",
		Description: "With a name, that flow alone, one entry per line: the entries the pace skips marked, and each " +
			"dial with its origin; this is the list /f10:ship runs. The pace is --pace, else the branch's record, " +
			"else the project's pace:, else the user's; with none, every flow runs as declared, " +
			"an optional CI review (ci?) settled.",
		Flags: []cli.Flag{
			paceFlag(),
			&cli.BoolFlag{Name: "json", Usage: "emit the same list as JSON"},
		},
		Action: runFlow,
	}
}

type flowRow struct {
	Name     string          `json:"name"`
	Entries  []string        `json:"entries"`          // as declared, labels cut
	Labels   []string        `json:"labels,omitempty"` // each entry's display label by index; nil when none has one
	Run      []string        `json:"run,omitempty"`    // what runs under the pace
	Selected []string        `json:"selectedBy"`
	Resolved *facts.Resolved `json:"resolved,omitempty"` // the entries under the pace; nil when nothing changes them, or the pace breaks the flow
	Broken   string          `json:"broken,omitempty"`   // why the pace does not resolve on this flow
}

type flowView struct {
	Pace        string            `json:"pace,omitempty"`
	PaceOrigin  string            `json:"paceOrigin,omitempty"`
	Ceiling     string            `json:"ceiling,omitempty"` // the cap on the pace, or why it does not apply
	Flows       []flowRow         `json:"flows"`
	DriveFinish bool              `json:"driveFinish"`
	FlowByArea  map[string]string `json:"flowByArea,omitempty"`
}

func runFlow(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	paces, err := paceSet(t.eff)
	if err != nil {
		return err
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	pace, err := activePace(ctx, paces, cmd.String("pace"), main, gitx.CurrentBranch(ctx, t.lay.CheckoutRoot))
	if err != nil {
		return err
	}

	v := pacedFlowsView(t.eff, paces, pace)

	if name := strings.ToLower(strings.TrimSpace(cmd.Args().First())); name != "" {
		if _, err := resolveShipFlow(t.eff, name); err != nil {
			return err
		}

		i := slices.IndexFunc(v.Flows, func(r flowRow) bool { return strings.EqualFold(r.Name, name) })
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
	set := facts.NewPaceSet()
	set.Remote = eff.Review.Remote()

	return pacedFlowsView(eff, set, runPace{})
}

// pacedFlowsView is every flow with its entries under pace; a flow the
// pace breaks keeps its declared entries and says why.
func pacedFlowsView(eff *facts.Effective, set *facts.PaceSet, pace runPace) flowView {
	v := flowView{DriveFinish: eff.DriveFinish, FlowByArea: eff.FlowByArea, Pace: pace.name, PaceOrigin: pace.origin}
	v.Ceiling = ceilingShown(set, pace.name)

	for _, name := range eff.FlowNames() {
		labels := eff.FlowLabels[name]
		row := flowRow{Name: name, Entries: eff.Flows[name], Labels: labels, Selected: selectors(name)}

		if r, err := set.ResolveFlow(name, eff.Flows[name], pace.name); err != nil {
			row.Broken = err.Error()
		} else if pace.name != "" || r.Changed() {
			r = r.WithLabels(labels)
			row.Resolved, row.Run = &r, r.Run()
		}

		v.Flows = append(v.Flows, row)
	}

	return v
}

// ceilingShown is the ceiling's dials with their layers, or why it leaves
// the pace alone; "" with no ceiling.
func ceilingShown(set *facts.PaceSet, pace string) string {
	if len(set.Ceiling) == 0 {
		return ""
	}

	if set.Exempt(pace) {
		return "not applied: pace " + pace + " is in except (" + set.ExceptOrigin + ")"
	}

	var parts []string
	for _, key := range slices.Sorted(maps.Keys(set.Ceiling)) {
		parts = append(parts, key+" "+set.Ceiling[key].Shown()+" ("+set.CeilingOrigins[key]+")")
	}

	return strings.Join(parts, ", ")
}

// writeFlowEntries is one flow, an entry per line with its dials, then the
// pace's finish; the list /f10:ship runs.
func writeFlowEntries(w io.Writer, v flowView) error {
	f := v.Flows[0]

	var b strings.Builder

	if v.Pace != "" {
		fmt.Fprintf(&b, "pace: %s (%s)\n", v.Pace, v.PaceOrigin)
	}

	if v.Ceiling != "" {
		fmt.Fprintf(&b, "ceiling: %s\n", v.Ceiling)
	}

	if f.Resolved == nil {
		for _, e := range facts.LabeledEntries(f.Entries, f.Labels) {
			fmt.Fprintln(&b, e)
		}

		_, err := io.WriteString(w, b.String())

		return err
	}

	width := len(facts.PaceFinishKey)
	for _, e := range f.Resolved.Entries {
		width = max(width, len(state.Labeled(e.Entry, e.Label)))
	}

	for _, e := range f.Resolved.Entries {
		entry := state.Labeled(e.Entry, e.Label)

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

func writeFlows(w io.Writer, v flowView) error {
	width := len("drive_finish")
	if v.Ceiling != "" {
		width = max(width, len("ceiling"))
	}
	for _, f := range v.Flows {
		width = max(width, len(f.Name))
	}

	var b strings.Builder

	if v.Pace != "" {
		fmt.Fprintf(&b, "%-*s  %s (%s)\n", width, "pace", v.Pace, v.PaceOrigin)
	}

	if v.Ceiling != "" {
		fmt.Fprintf(&b, "%-*s  %s\n", width, "ceiling", v.Ceiling)
	}

	for _, f := range v.Flows {
		entries := facts.LabeledEntries(f.Entries, f.Labels)
		if f.Resolved != nil {
			entries = f.Resolved.Labeled()
		}

		fmt.Fprintf(&b, "%-*s  %s\n%-*s  (%s)\n", width, f.Name, strings.Join(entries, " "),
			width, "", strings.Join(f.Selected, ", "))

		if f.Broken != "" {
			fmt.Fprintf(&b, "%-*s  does not run at this pace: %s\n", width, "", f.Broken)
		}

		if f.Resolved != nil && f.Resolved.Finish != nil {
			from := "the pace"
			if f.Resolved.Finish.Capped != "" {
				from = "the ceiling; " + f.Resolved.Finish.Capped
			}

			fmt.Fprintf(&b, "%-*s  finish %s, from %s\n", width, "", f.Resolved.Finish.Value, from)
		}
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

// A shipFlow is the flow a started task ships through. byArea is no flow
// named on a project declaring flow_by_area: ship picks the flow from the
// task's area, so it is told none and nothing is recorded on the branch.
type shipFlow struct {
	name     string
	planless bool
	byArea   bool
	pace     string // the pace it runs at, "" for none
	finish   string // the pace's finish for a run ending at a PR: on, off, or "" to leave drive_finish
}

// finishes says whether drive ends the task with finish: the pace's word
// when it has one, else drive_finish.
func (f shipFlow) finishes(driveFinish bool) bool {
	if f.finish != "" {
		return f.finish == "on"
	}

	return driveFinish
}

func (f shipFlow) word() string {
	if f.name == "" {
		return ""
	}

	return f.name + ": "
}

func (f shipFlow) recorded() string {
	switch {
	case f.byArea:
		return ""
	case f.name == "":
		return "default"
	default:
		return f.name
	}
}

func (f shipFlow) label() string {
	if f.byArea {
		return "flow the task's area picks"
	}

	return f.recorded()
}

func (f shipFlow) plans() bool { return !f.planless && !f.byArea }

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

func resolveShipFlow(eff *facts.Effective, name string) (shipFlow, error) {
	return resolvePacedFlow(eff, nil, "", name)
}

// resolvePacedFlow is the named flow under pace: an entry the pace skips
// does not run, so `plan: off` makes the flow planless. A nil set is no pace.
func resolvePacedFlow(eff *facts.Effective, set *facts.PaceSet, pace, name string) (shipFlow, error) {
	if set == nil {
		set, pace = facts.NewPaceSet(), ""
		set.Remote = eff.Review.Remote()
	}

	if name == "" && len(eff.FlowByArea) > 0 {
		// ship picks the flow, so whether it ends at a PR, and so whether
		// the pace's finish applies, is unknown: drive_finish decides, unless
		// the ceiling forbids finishing, which holds whatever the flow
		fl := shipFlow{byArea: true, pace: pace}
		if _, caps := set.Capped(pace); caps[facts.PaceFinishKey].Value == "off" {
			fl.finish = "off"
		}

		return fl, nil
	}

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

	r, err := set.ResolveFlow(lookup, flow, pace)
	if err != nil {
		return shipFlow{}, err
	}

	fl := shipFlow{planless: facts.Planless(r.Run()), pace: pace}
	if r.Finish != nil {
		fl.finish = r.Finish.Value
	}

	if lookup != "default" {
		fl.name = lookup
	}

	return fl, nil
}

func flowFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "flow",
		Usage: "run the declared flow `NAME` instead of default; f10 flow lists them",
	}
}

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

func completingFlow(args []string) bool {
	const marker = "--generate-shell-completion"

	if i := slices.Index(args, marker); i > 0 {
		args = args[:i]
	}

	return len(args) > 0 && args[len(args)-1] == "--flow"
}
