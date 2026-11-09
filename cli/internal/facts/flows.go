package facts

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/state"
)

// A flow is every step a task runs through: a leading `plan` runs only for a
// task with no plan file, and /f10:ship runs the entries after it. An entry
// is a step or `step:arg`, one shell word each.

// DefaultFlow is the flow a project that declares none runs.
const DefaultFlow = "plan implement pr"

// Flow is one declared flow's entries as written: a list, or one string
// split on whitespace and arrows, never on commas, which an argument may
// hold.
type Flow []string

// UnmarshalYAML accepts a sequence of entries or one string.
func (f *Flow) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}

		*f = Flow(trimSteps(list))
	case yaml.ScalarNode:
		*f = splitFlow(node.Value)
	default:
		return fmt.Errorf("line %d: a list of entries or one `plan implement pr` string", node.Line)
	}

	return nil
}

// MarshalYAML writes the flow as one space-separated string, the shape
// f10 init writes.
func (f Flow) MarshalYAML() (any, error) { return strings.Join(f, " "), nil }

// splitFlow reads `plan implement → review:local → pr`: whitespace and
// arrows separate.
func splitFlow(text string) Flow {
	var out Flow

	for tok := range strings.FieldsSeq(strings.NewReplacer("→", " ", "->", " ").Replace(text)) {
		if tok = strings.Trim(tok, "*_`"); tok != "" {
			out = append(out, tok)
		}
	}

	return out
}

// unlabeled is an entry with its label cut.
func unlabeled(e string) string {
	e, _ = state.SplitLabel(e)

	return strings.TrimSpace(e)
}

// splitLabels is every entry with its label cut, and the labels by index,
// nil when none has one.
func splitLabels(f []string) ([]string, []string) {
	out := make([]string, 0, len(f))
	labels := make([]string, len(f))
	labeled := false

	for i, e := range f {
		_, labels[i] = state.SplitLabel(e)
		out = append(out, unlabeled(e))
		labeled = labeled || labels[i] != ""
	}

	if !labeled {
		labels = nil
	}

	return out, labels
}

// SplitEntry is an entry's step and its argument: `review:local` is review
// and local.
func SplitEntry(e string) (string, string) {
	step, arg, _ := strings.Cut(unlabeled(e), ":")

	return step, arg
}

// ShipEntries is what /f10:ship runs: the flow after a leading `plan`.
func ShipEntries(flow []string) []string {
	if len(flow) > 0 && flow[0] == "plan" {
		return flow[1:]
	}

	return flow
}

// ShipLabels is labels cut as ShipEntries cuts their flow.
func ShipLabels(flow, labels []string) []string {
	if len(flow) > 0 && flow[0] == "plan" && len(labels) > 0 {
		return labels[1:]
	}

	return labels
}

// By is who a review entry runs: the local review, the CI review, or both.
// CIOptional is a `ci?`, which runs only where it can (Settle).
type By struct {
	Local, CI, CIOptional bool
}

// ReviewBy reads a review entry's argument: none, `local` or `host` the
// local review (`host` on the host's own code review), `ci` the CI review,
// `local+ci` both in either order, `ci?` marking the CI review optional.
// False for any other argument.
func ReviewBy(arg string) (By, bool) {
	switch strings.ToLower(arg) {
	case "", "local", "host":
		return By{Local: true}, true
	case "ci":
		return By{CI: true}, true
	case "ci?":
		return By{CI: true, CIOptional: true}, true
	case "local+ci", "ci+local":
		return By{Local: true, CI: true}, true
	case "local+ci?", "ci?+local":
		return By{Local: true, CI: true, CIOptional: true}, true
	}

	return By{}, false
}

// Settle decides an optional CI review: it runs when a remote reviewer is
// declared and a pr or push comes before it, and is dropped otherwise,
// with the reason. A required one comes back as it is.
func (b By) Settle(remote, published bool) (By, string) {
	if !b.CIOptional {
		return b, ""
	}

	b.CIOptional = false

	switch {
	case !remote:
		b.CI = false

		return b, "no remote reviewer declared"
	case !published:
		b.CI = false

		return b, "no pr or push before it"
	}

	return b, ""
}

// Arg is the argument that runs b: local, ci or local+ci, a `?` kept;
// "" when b runs nothing.
func (b By) Arg() string {
	ci := "ci"
	if b.CIOptional {
		ci = "ci?"
	}

	switch {
	case b.Local && b.CI:
		return "local+" + ci
	case b.CI:
		return ci
	case b.Local:
		return "local"
	}

	return ""
}

// Planless reports a flow that writes no plan file.
func Planless(flow []string) bool { return len(flow) == 0 || flow[0] != "plan" }

// JudgeKind is what the judge at entry i judges: the plan before any
// `implement`, the change after one.
func JudgeKind(flow []string, i int) string {
	for _, e := range flow[:min(i, len(flow))] {
		if step, _ := SplitEntry(e); step == "implement" {
			return "change"
		}
	}

	return "plan"
}

var (
	flowNameRE  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	stepNameRE  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	labelNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

// GenericSteps is every generic step a flow entry may name, plus finish, which
// drive runs. A label never takes one of these names, so status and stats
// never show a step that did not run.
var GenericSteps = []string{
	"brainstorm", "capture", "catchup", "cleanup", "commit", "debug", "demo", "deploy", "explain", "fetch",
	"finish", "handoff", "implement", "judge", "plan", "pr", "push", "resolve", "review", "standup",
}

// checkFlow reports what is wrong with one flow. Step names are not checked
// against a vocabulary: a project-defined step is any overlay's name. others
// is every step the declared flows name, which no label may take.
func checkFlow(name string, flow []string, others ...string) []string {
	var bad []string

	if !flowNameRE.MatchString(name) {
		bad = append(bad, fmt.Sprintf("flows: %q is not a lowercase name", name))
	}

	if len(flow) == 0 {
		return append(bad, fmt.Sprintf("flows.%s: no entries", name))
	}

	entries, labels := splitLabels(flow)
	bad = append(bad, checkLabels(name, flow, labels, others)...)

	for i, e := range entries {
		step, arg, colon := strings.Cut(e, ":")

		switch {
		case !stepNameRE.MatchString(step):
			bad = append(bad, fmt.Sprintf("flows.%s: %q is not a step name", name, e))
		case colon && arg == "":
			bad = append(bad, fmt.Sprintf("flows.%s: %q has a colon and no argument", name, e))
		case step == "review" && !validBy(arg):
			bad = append(bad, fmt.Sprintf("flows.%s: %q names no reviewer; write review:local, review:host, "+
				"review:ci, review:local+ci, review:ci? or review:local+ci?", name, e))
		case step == "catchup":
			bad = append(bad, fmt.Sprintf("flows.%s: catchup is never an entry: ship catches up before implement, "+
				"pr and a change judge; the catchup key picks merge, rebase or off", name))
		case step == "plan" && i > 0:
			bad = append(bad, fmt.Sprintf("flows.%s: plan comes first, since ship runs only the entries after it; "+
				"stale_after judges a task before planning it", name))
		case step == "judge" && Planless(entries) && JudgeKind(entries, i) == "plan":
			bad = append(bad, fmt.Sprintf("flows.%s: a judge before implement judges the plan, "+
				"and the flow writes none; lead with plan", name))
		}
	}

	return bad
}

// checkLabels reports a label that is not lower kebab-case, repeats in its
// flow, or names a step.
func checkLabels(name string, flow, labels, others []string) []string {
	var bad []string

	seen := map[string]bool{}

	for i := range flow {
		label, written := state.LabelAt(labels, i), strings.TrimSpace(flow[i])

		switch {
		case label == "" && strings.ContainsAny(written, "[]"):
			bad = append(bad, fmt.Sprintf("flows.%s: %q: a label is a name in brackets after the entry, "+
				"as pr[pr-polish]", name, written))
		case label == "":
			continue
		case !labelNameRE.MatchString(label):
			bad = append(bad, fmt.Sprintf("flows.%s: %q: label %q is not lower kebab-case", name, written, label))
		case slices.Contains(GenericSteps, label) || slices.Contains(others, label):
			bad = append(bad, fmt.Sprintf("flows.%s: %q: label %q is a step's name, "+
				"so status would show a step that did not run", name, written, label))
		case seen[label]:
			bad = append(bad, fmt.Sprintf("flows.%s: label %q names two entries", name, label))
		}

		seen[label] = true
	}

	return bad
}

// FlowSteps is every step the flows name, their labels cut.
func FlowSteps[F ~[]string](flows map[string]F) []string {
	var steps []string

	for _, f := range flows {
		for _, e := range f {
			if step, _ := SplitEntry(e); !slices.Contains(steps, step) {
				steps = append(steps, step)
			}
		}
	}

	return steps
}

func validBy(arg string) bool {
	_, ok := ReviewBy(arg)

	return ok
}

// checkRemoteReview warns about a required CI review that cannot run: no
// remote reviewer, or no pr or push before it. An optional one (`ci?`)
// never warns: dropping it is what `?` asks for.
func (e *Effective) checkRemoteReview() {
	for _, name := range e.FlowNames() {
		published := false

		for _, entry := range e.Flows[name] {
			step, arg := SplitEntry(entry)
			if step == "pr" || step == "push" {
				published = true
			}

			if by, _ := ReviewBy(arg); step != "review" || !by.CI || by.CIOptional {
				continue
			}

			switch {
			case !e.Review.Remote():
				e.problem(false, "flows.%s: %s reads a CI review, and Review declares no remote reviewer; "+
					"write ci? to run it only where one is", name, entry)
			case !published:
				e.problem(false, "flows.%s: %s reads a CI review on the PR, and no pr or push comes before it",
					name, entry)
			}

			break
		}
	}
}

// FlowNames is every declared flow, default first, then by name.
func (e *Effective) FlowNames() []string {
	names := slices.Sorted(maps.Keys(e.Flows))
	if i := slices.Index(names, "default"); i > 0 {
		names = append([]string{"default"}, slices.Delete(names, i, i+1)...)
	}

	return names
}

// Flow is the named flow, in any case, and whether it is declared.
func (e *Effective) Flow(name string) ([]string, bool) {
	for declared, flow := range e.Flows {
		if strings.EqualFold(declared, name) {
			return flow, true
		}
	}

	return nil, false
}

// LabeledEntries is entries as written, each with its label.
func LabeledEntries(entries, labels []string) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = state.Labeled(e, state.LabelAt(labels, i))
	}

	return out
}

func flowsShown(flows, labels map[string][]string, names []string) string {
	shown := make([]string, 0, len(names))
	for _, name := range names {
		shown = append(shown, name+": "+strings.Join(LabeledEntries(flows[name], labels[name]), " "))
	}

	return strings.Join(shown, "; ")
}

// resolveFlows settles flows and flow_by_area: the frontmatter's flows,
// else the default.
func (e *Effective) resolveFlows(fm *layered) {
	if fm.Flows == nil {
		e.Flows = map[string][]string{"default": strings.Fields(DefaultFlow)}
		e.Facts = append(e.Facts, Fact{Key: "flows", Value: "default: " + DefaultFlow, Origin: OriginDefault})
	} else {
		e.Flows = map[string][]string{}

		for name, f := range fm.Flows {
			entries, labels := splitLabels(f)
			e.Flows[name] = entries

			if labels != nil {
				if e.FlowLabels == nil {
					e.FlowLabels = map[string][]string{}
				}

				e.FlowLabels[name] = labels
			}
		}

		// start and drive run default unasked, so a project naming only
		// other flows still has one
		if _, ok := e.Flows["default"]; !ok {
			e.Flows["default"] = strings.Fields(DefaultFlow)
		}

		e.Facts = append(e.Facts, Fact{
			Key: "flows", Value: flowsShown(e.Flows, e.FlowLabels, e.FlowNames()),
			Origin: "declared (" + fm.origin["flows"] + ", frontmatter)",
		})
	}

	e.FlowByArea = fm.FlowByArea

	e.checkRemoteReview()

	if e.FlowByArea != nil {
		e.Facts = append(e.Facts, Fact{
			Key:    "flow_by_area",
			Value:  mapShown(e.FlowByArea),
			Origin: "declared (" + fm.origin["flow_by_area"] + ", frontmatter)",
		})

		for _, area := range slices.Sorted(maps.Keys(e.FlowByArea)) {
			if _, ok := e.Flows[e.FlowByArea[area]]; !ok {
				e.problem(true, "project.md frontmatter: flow_by_area: %q maps to %q, which names no flow",
					area, e.FlowByArea[area])
			}
		}
	}
}
