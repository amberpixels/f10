package facts

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A flow is every step a task runs through: a leading `plan` runs only for a
// task with no plan file, and /f10:ship runs the entries after it. An entry
// is a step or `step:arg`, one shell word each.

// DefaultFlow is the flow a project that declares none runs.
const DefaultFlow = "plan implement pr"

// legacyDriveChain is the chain `f10 drive` ran when project.md declared
// none, folded into the default flow when old keys are converted.
const legacyDriveChain = "plan judge ship review resolve finish"

// Flow is one declared flow's entries as written: a list, or one string
// split on whitespace and arrows, never on commas, which an argument may
// hold. The old `review (local)` form stays one entry until normalizeEntry.
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
// f10 init --migrate leaves.
func (f Flow) MarshalYAML() (any, error) { return strings.Join(f, " "), nil }

// splitFlow reads `plan implement → review (local) → pr`: whitespace and
// arrows separate, and a parenthesised word joins the entry before it.
func splitFlow(text string) Flow {
	var out Flow

	for tok := range strings.FieldsSeq(strings.NewReplacer("→", " ", "->", " ").Replace(text)) {
		tok = strings.Trim(tok, "*_`")
		if tok == "" {
			continue
		}

		if n := len(out); n > 0 && strings.Contains(out[n-1], "(") && !strings.Contains(out[n-1], ")") {
			out[n-1] += " " + tok

			continue
		}

		if n := len(out); n > 0 && strings.HasPrefix(tok, "(") {
			out[n-1] += " " + tok

			continue
		}

		out = append(out, tok)
	}

	return out
}

// normalizeEntry is an entry in the colon form, and whether it was written
// in the old parenthesised one: `review (CI)` is `review:ci`.
func normalizeEntry(e string) (string, bool) {
	step, rest, paren := strings.Cut(e, "(")
	if !paren {
		return strings.TrimSpace(e), false
	}

	arg := strings.ToLower(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(rest), ")")), "-"))

	return strings.TrimSpace(step) + ":" + arg, true
}

// normalizeFlow is every entry in the colon form, and whether any was not.
func normalizeFlow(f []string) ([]string, bool) {
	out := make([]string, 0, len(f))
	legacy := false

	for _, e := range f {
		n, old := normalizeEntry(e)
		out = append(out, n)
		legacy = legacy || old
	}

	return out, legacy
}

// SplitEntry is an entry's step and its argument, in either form:
// `review:local` and `review (local)` are both review and local.
func SplitEntry(e string) (string, string) {
	e, _ = normalizeEntry(e)
	step, arg, _ := strings.Cut(e, ":")

	return step, arg
}

// ShipEntries is what /f10:ship runs: the flow after a leading `plan`.
func ShipEntries(flow []string) []string {
	if len(flow) > 0 && flow[0] == "plan" {
		return flow[1:]
	}

	return flow
}

// ReviewHalves is which halves a review entry's argument runs: none or
// `local` the local review, `ci` the remote one, `local+ci` both, in
// either order. Neither for any other argument.
func ReviewHalves(arg string) (bool, bool) {
	switch strings.ToLower(arg) {
	case "", "local":
		return true, false
	case "ci":
		return false, true
	case "local+ci", "ci+local":
		return true, true
	}

	return false, false
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
	flowNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	stepNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// checkFlow reports what is wrong with one flow. Step names are not checked
// against a vocabulary: a project-defined step is any overlay's name.
func checkFlow(name string, flow []string) []string {
	var bad []string

	if !flowNameRE.MatchString(name) {
		bad = append(bad, fmt.Sprintf("flows: %q is not a lowercase name", name))
	}

	if len(flow) == 0 {
		return append(bad, fmt.Sprintf("flows.%s: no entries", name))
	}

	entries, _ := normalizeFlow(flow)

	for i, e := range entries {
		step, arg, colon := strings.Cut(e, ":")

		switch {
		case !stepNameRE.MatchString(step):
			bad = append(bad, fmt.Sprintf("flows.%s: %q is not a step name", name, e))
		case colon && arg == "":
			bad = append(bad, fmt.Sprintf("flows.%s: %q has a colon and no argument", name, e))
		case step == "review" && !anyHalf(arg):
			bad = append(bad, fmt.Sprintf("flows.%s: %q names no review half; write review:local, review:ci "+
				"or review:local+ci", name, e))
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

func anyHalf(arg string) bool {
	local, remote := ReviewHalves(arg)

	return local || remote
}

// checkRemoteHalves warns about a CI half that cannot run: no remote
// reviewer, or no pr or push before it. Warnings, since a legacy drive chain
// converts to a trailing review:ci on a default that may open no PR.
func (e *Effective) checkRemoteHalves() {
	for _, name := range e.FlowNames() {
		published := false

		for _, entry := range e.Flows[name] {
			step, arg := SplitEntry(entry)
			if step == "pr" || step == "push" {
				published = true
			}

			if _, remote := ReviewHalves(arg); step != "review" || !remote {
				continue
			}

			switch {
			case !e.Review.Remote():
				e.problem(false, "flows.%s: %s reads a CI review, and Review declares no remote reviewer", name, entry)
			case !published:
				e.problem(false, "flows.%s: %s reads a CI review on the PR, and no pr or push comes before it",
					name, entry)
			}

			break
		}
	}
}

// legacyFlows converts the old keys: each pipeline becomes a flow, led by
// plan unless planless. The drive chain folds into default alone, since only
// drive ran it; a trailing finish turns drive_finish on.
func legacyFlows(pipes map[string][]string, planless, chain []string, remote bool) (map[string][]string, bool) {
	flows := map[string][]string{}

	if _, ok := pipes["default"]; !ok {
		pipes = maps.Clone(pipes)
		pipes["default"] = []string{"implement", "pr"} // the old default pipeline
	}

	for name, steps := range pipes {
		var f []string
		if !slices.Contains(planless, name) {
			f = append(f, "plan")
		}

		entries, _ := normalizeFlow(steps)
		flows[name] = append(f, entries...)
	}

	finish := len(chain) > 0 && chain[len(chain)-1] == "finish"

	def, ok := flows["default"]
	ship := slices.Index(chain, "ship")

	if !ok || ship < 0 {
		return flows, finish
	}

	if judge := slices.Index(chain, "judge"); judge >= 0 && judge < ship && !Planless(def) &&
		judge > slices.Index(chain, "plan") {
		def = slices.Insert(slices.Clone(def), 1, "judge")
	}

	reviews := slices.ContainsFunc(def, func(e string) bool { step, _ := SplitEntry(e); return step == "review" })
	if slices.Index(chain, "review") > ship && !reviews {
		if remote {
			def = append(def, "review:ci")
		} else {
			def = append(def, "review:local")
		}
	}

	flows["default"] = def

	return flows, finish
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

func flowsShown(flows map[string][]string, names []string) string {
	shown := make([]string, 0, len(names))
	for _, name := range names {
		shown = append(shown, name+": "+strings.Join(flows[name], " "))
	}

	return strings.Join(shown, "; ")
}

// legacyFlowKeys are the frontmatter keys flows replaces.
var legacyFlowKeys = []string{"pipelines", "planless", "pipeline_by_area", "drive_chain"}

// resolveFlows settles flows, flow_by_area and drive_finish: the
// frontmatter's flows, else the old keys and prose sections converted with
// a warning, else the default.
func (e *Effective) resolveFlows(fm *layered, declared map[string]section) {
	var legacy []string

	for _, key := range legacyFlowKeys {
		if _, ok := fm.origin[key]; ok {
			legacy = append(legacy, key)
		}
	}

	prose := []string{}

	for _, name := range []string{"Ship pipeline(s)", "Drive chain"} {
		if strings.TrimSpace(declared[name].body) != "" {
			prose = append(prose, "the "+name+" section in "+declared[name].file)
		}
	}

	// a drive merges what it ships unless the project says its merges are a
	// human's; a legacy chain says it by ending in finish or not
	e.DriveFinish = true
	finishOrigin := OriginDefault

	switch {
	case fm.Flows != nil:
		e.Flows = map[string][]string{}

		var old []string

		for name, f := range fm.Flows {
			entries, paren := normalizeFlow(f)
			e.Flows[name] = entries

			if paren {
				old = append(old, name)
			}
		}

		// start and drive run default unasked, so a project naming only
		// other flows still has one
		if _, ok := e.Flows["default"]; !ok {
			e.Flows["default"] = strings.Fields(DefaultFlow)
		}

		if len(old) > 0 {
			slices.Sort(old)
			e.problem(
				false,
				"flows %s use the old `step (arg)` entry form; f10 init --migrate rewrites it to `step:arg`",
				strings.Join(old, ", "),
			)
		}

		if len(legacy)+len(prose) > 0 {
			e.problem(false, "%s ignored, since flows is declared; f10 init --migrate removes them",
				strings.Join(append(legacy, prose...), ", "))
		}

		e.Facts = append(e.Facts, Fact{
			Key: "flows", Value: flowsShown(e.Flows, e.FlowNames()),
			Origin: "declared (" + fm.origin["flows"] + ", frontmatter)",
		})
		e.FlowByArea = fm.FlowByArea
	case len(legacy)+len(prose) > 0:
		pipes, planless := fm.pipelines()
		if pipes == nil {
			pipes, planless = prosePipelines(declared["Ship pipeline(s)"].body)
		}

		if pipes == nil {
			pipes = map[string][]string{"default": {"implement", "pr"}}
		}

		chain := []string(fm.DriveChain)
		if chain == nil {
			chain = proseChain(declared["Drive chain"].body)
		}

		if len(chain) == 0 {
			chain = strings.Fields(legacyDriveChain)
		}

		var finish bool

		e.Flows, finish = legacyFlows(pipes, planless, lowered(chain), e.Review.Remote())
		e.DriveFinish, finishOrigin = finish, "converted from the drive chain"

		from := strings.Join(append(legacy, prose...), ", ")
		e.problem(false, "%s converted to flows; f10 init --migrate rewrites them", from)
		e.Facts = append(e.Facts, Fact{
			Key: "flows", Value: flowsShown(e.Flows, e.FlowNames()), Origin: "converted from " + from,
		})
		e.FlowByArea = cmpMap(fm.FlowByArea, fm.ByArea)
	default:
		e.Flows = map[string][]string{"default": strings.Fields(DefaultFlow)}
		e.Facts = append(e.Facts, Fact{Key: "flows", Value: "default: " + DefaultFlow, Origin: OriginDefault})
		e.FlowByArea = fm.FlowByArea
	}

	if fm.DriveFinish != "" {
		e.DriveFinish, finishOrigin = fm.DriveFinish == "on", "declared ("+fm.origin["drive_finish"]+", frontmatter)"
	}

	e.Facts = append(e.Facts, Fact{Key: "drive_finish", Value: onOff(e.DriveFinish), Origin: finishOrigin})

	e.checkRemoteHalves()

	if e.FlowByArea != nil {
		e.Facts = append(e.Facts, Fact{
			Key:    "flow_by_area",
			Value:  mapShown(e.FlowByArea),
			Origin: "declared (" + cmpOr(fm.origin["flow_by_area"], fm.origin["pipeline_by_area"]) + ", frontmatter)",
		})

		for _, area := range slices.Sorted(maps.Keys(e.FlowByArea)) {
			if _, ok := e.Flows[e.FlowByArea[area]]; !ok {
				e.problem(true, "project.md frontmatter: flow_by_area: %q maps to %q, which names no flow",
					area, e.FlowByArea[area])
			}
		}
	}
}

// pipelines is the old pipelines key as plain lists, nil when unset.
func (l *layered) pipelines() (map[string][]string, []string) {
	if l.Pipelines == nil {
		return nil, nil
	}

	pipes := map[string][]string{}
	for name, steps := range l.Pipelines {
		pipes[strings.ToLower(name)] = steps
	}

	return pipes, l.Planless
}

func lowered(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = strings.ToLower(s)
	}

	return out
}

func cmpMap(a, b map[string]string) map[string]string {
	if a != nil {
		return a
	}

	return b
}

func onOff(b bool) string {
	if b {
		return "on"
	}

	return "off"
}
