package runplan

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
)

type FlowRow struct {
	Name     string          `json:"name"`
	Entries  []string        `json:"entries"`          // as declared, labels cut
	Labels   []string        `json:"labels,omitempty"` // each entry's display label by index; nil when none has one
	Run      []string        `json:"run,omitempty"`    // what runs under the pace
	Selected []string        `json:"selectedBy"`
	Resolved *facts.Resolved `json:"resolved,omitempty"` // the entries under the pace; nil when nothing changes them, or the pace breaks the flow
	Broken   string          `json:"broken,omitempty"`   // why the pace does not resolve on this flow
}

type FlowView struct {
	Pace       string            `json:"pace,omitempty"`
	PaceOrigin string            `json:"paceOrigin,omitempty"`
	Ceiling    string            `json:"ceiling,omitempty"` // the cap on the pace, or why it does not apply
	Flows      []FlowRow         `json:"flows"`
	FlowByArea map[string]string `json:"flowByArea,omitempty"`
}

func flowsView(eff *facts.Effective) FlowView {
	return PacedFlowsView(eff, uncapped(eff.PaceSet), runPace{})
}

// uncapped is the set without its ceiling: a flow under no pace runs as
// declared, which a cap would change.
func uncapped(set *facts.PaceSet) *facts.PaceSet {
	s := *set
	s.Ceiling, s.CeilingOrigins, s.Except = facts.Pace{}, nil, nil

	return &s
}

// PacedFlowsView is every flow with its entries under pace; a flow the
// pace breaks keeps its declared entries and says why.
func PacedFlowsView(eff *facts.Effective, set *facts.PaceSet, pace runPace) FlowView {
	v := FlowView{FlowByArea: eff.FlowByArea, Pace: pace.Name, PaceOrigin: pace.Origin}
	v.Ceiling = ceilingShown(set, pace.Name)

	for _, name := range eff.FlowNames() {
		f := eff.Flows[name]
		row := FlowRow{Name: name, Entries: f.Strings(), Labels: f.Labels(), Selected: selectors(name)}

		if r, err := set.ResolveFlow(name, f, pace.Name); err != nil {
			row.Broken = err.Error()
		} else if pace.Name != "" || r.Changed() {
			row.Resolved, row.Run = &r, r.Run()
		}

		v.Flows = append(v.Flows, row)
	}

	return v
}

// ceilingShown is the ceiling's dials with their layers, or why it leaves
// the pace alone; "" with no ceiling.
func ceilingShown(set *facts.PaceSet, pace string) string {
	if set.Ceiling.Empty() {
		return ""
	}

	if set.Exempt(pace) {
		return "not applied: pace " + pace + " is in except (" + set.ExceptOrigin.String() + ")"
	}

	var parts []string
	for _, key := range set.Ceiling.Keys() {
		parts = append(parts, key+" "+set.Ceiling.Shown(key)+" ("+set.CeilingOrigins[key].String()+")")
	}

	return strings.Join(parts, ", ")
}

// LocalFlow is the flow --local selects on start and drive; finish's
// --local (leave the PR open) and a never-pushed branch's merge here are
// unrelated.
const LocalFlow = "local"

func selectors(name string) []string {
	switch name {
	case "default":
		return []string{"f10 start"}
	case LocalFlow:
		return []string{"--local", "--flow " + LocalFlow}
	default:
		return []string{"--flow " + name}
	}
}

func WriteFlows(w io.Writer, v FlowView) error {
	width := len("flow_by_area")
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
		entries := flow.Zip(f.Entries, f.Labels).Written()
		if f.Resolved != nil {
			entries = f.Resolved.Labeled()
		}

		fmt.Fprintf(&b, "%-*s  %s\n%-*s  (%s)\n", width, f.Name, strings.Join(entries, " "),
			width, "", strings.Join(f.Selected, ", "))

		if f.Broken != "" {
			fmt.Fprintf(&b, "%-*s  does not run at this pace: %s\n", width, "", f.Broken)
		}
	}

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

// A ShipFlow is the flow a started task ships through. ByArea is no flow
// named on a project declaring flow_by_area: ship picks the flow from the
// task's area, so it is told none and nothing is recorded on the branch.
type ShipFlow struct {
	Name     string
	Planless bool
	ByArea   bool
	Pace     string // the pace it runs at, "" for none
	Finishes bool   // drive ends the task with finish: the flow ends in it and the pace runs it
	Mixed    bool   // ByArea only: some flows the map can pick finish and some do not
	NoFinish bool   // the declared flow ends at a PR with no finish entry, so drive stops at ship
}

func (f ShipFlow) Word() string {
	if f.Name == "" {
		return ""
	}

	return f.Name + ": "
}

func (f ShipFlow) Recorded() string {
	switch {
	case f.ByArea:
		return ""
	case f.Name == "":
		return "default"
	default:
		return f.Name
	}
}

func (f ShipFlow) Label() string {
	if f.ByArea {
		return "flow the task's area picks"
	}

	return f.Recorded()
}

func (f ShipFlow) Plans() bool { return !f.Planless && !f.ByArea }

// PacedFlow is the named flow under pace: an entry the pace skips does not
// run, so `plan: off` makes the flow planless. "" is no pace, the flow as
// declared.
func PacedFlow(eff *facts.Effective, pace, name string) (ShipFlow, error) {
	set := eff.PaceSet
	if pace == "" {
		set = uncapped(set)
	}

	if name == "" && len(eff.FlowByArea) > 0 {
		return byAreaFlow(eff, set, pace), nil
	}

	lookup := strings.ToLower(name)
	if lookup == "" {
		lookup = "default"
	}

	declared, ok := eff.Flow(lookup)
	if !ok {
		var b strings.Builder
		_ = WriteFlows(&b, flowsView(eff))

		return ShipFlow{}, fmt.Errorf("--flow %s: project.md declares no such flow; it declares:\n%s", name,
			strings.TrimRight(b.String(), "\n"))
	}

	r, err := set.ResolveFlow(lookup, declared, pace)
	if err != nil {
		return ShipFlow{}, err
	}

	run := flow.Of(r.Run())
	fl := ShipFlow{Planless: !run.Plans(), Pace: pace, Finishes: run.Finishes()}
	fl.NoFinish = !declared.Finishes() && declared.Delivered() == "pr"

	if lookup != "default" {
		fl.Name = lookup
	}

	return fl, nil
}

// byAreaFlow is the flow ship picks from the task's area, which drive cannot
// know. It finishes only when every flow the map can pick runs finish under
// the pace, default included, so a mixed map never merges a task unasked.
// A flow the pace breaks counts as not finishing, because it fails only the
// ship that picks it, never the whole drive.
func byAreaFlow(eff *facts.Effective, set *facts.PaceSet, pace string) ShipFlow {
	fl := ShipFlow{ByArea: true, Pace: pace}
	some, all := false, true

	names := append(slices.Collect(maps.Values(eff.FlowByArea)), "default")
	for _, name := range names {
		declared, ok := eff.Flow(name)
		if !ok {
			continue
		}

		r, err := set.ResolveFlow(name, declared, pace)
		finishes := err == nil && flow.Of(r.Run()).Finishes()
		some, all = some || finishes, all && finishes
	}

	fl.Finishes, fl.Mixed = all, some && !all

	return fl
}
