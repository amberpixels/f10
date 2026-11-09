// Package flow is the grammar of a flow entry, `step:arg[label]`, and the
// predicates every reader of a flow shares. It imports nothing of f10's, so
// facts, state and timing can all depend on it.
package flow

import (
	"encoding/json"
	"slices"
	"strings"
)

// An Entry is one flow entry: `review:local[deep-review]` is step review,
// argument local, label deep-review. The label is a display name only.
type Entry struct {
	Step  string
	Arg   string
	Label string
}

// Parse reads one entry as written.
func Parse(s string) Entry {
	body, label := CutLabel(s)
	step, arg, _ := strings.Cut(body, ":")

	return Entry{Step: step, Arg: arg, Label: label}
}

// CutLabel cuts an entry's trailing `[label]`: `pr[pr-polish]` is pr and
// pr-polish. An entry with none has label "".
func CutLabel(s string) (string, string) {
	s = strings.TrimSpace(s)

	i := strings.Index(s, "[")
	if i < 0 || !strings.HasSuffix(s, "]") {
		return s, ""
	}

	return strings.TrimSpace(s[:i]), s[i+1 : len(s)-1]
}

// String is the entry in the colon form, its label cut: what a state file's
// pipeline and a skill's prompt hold.
func (e Entry) String() string {
	if e.Arg == "" {
		return e.Step
	}

	return e.Step + ":" + e.Arg
}

// Written is the entry as a flow writes it, its label in brackets.
func (e Entry) Written() string {
	if e.Label == "" {
		return e.String()
	}

	return e.String() + "[" + e.Label + "]"
}

// A Flow is entries in the order they run.
type Flow []Entry

// Of parses each written entry.
func Of(written []string) Flow {
	if written == nil {
		return nil
	}

	f := make(Flow, len(written))
	for i, s := range written {
		f[i] = Parse(s)
	}

	return f
}

// Zip is a flow from the colon-form entries and their labels by index, the
// two lines a state file keeps; a missing label is "".
func Zip(entries, labels []string) Flow {
	if entries == nil {
		return nil
	}

	f := make(Flow, len(entries))
	for i, s := range entries {
		f[i] = Parse(s)
		if i < len(labels) {
			f[i].Label = labels[i]
		}
	}

	return f
}

// Strings is every entry in the colon form.
func (f Flow) Strings() []string {
	out := make([]string, len(f))
	for i, e := range f {
		out[i] = e.String()
	}

	return out
}

// Written is every entry as written, labels in brackets.
func (f Flow) Written() []string {
	out := make([]string, len(f))
	for i, e := range f {
		out[i] = e.Written()
	}

	return out
}

// Labels is every entry's label by index, nil when none has one.
func (f Flow) Labels() []string {
	if !slices.ContainsFunc(f, func(e Entry) bool { return e.Label != "" }) {
		return nil
	}

	out := make([]string, len(f))
	for i, e := range f {
		out[i] = e.Label
	}

	return out
}

// Plans reports a flow led by `plan`: it writes a plan file, and ship runs
// only the entries after it.
func (f Flow) Plans() bool { return len(f) > 0 && f[0].String() == "plan" }

// Ship is what /f10:ship runs: the flow after a leading `plan`.
func (f Flow) Ship() Flow {
	if f.Plans() {
		return f[1:]
	}

	return f
}

// JudgeKind is what the judge at entry i judges: the plan before any
// `implement`, the change after one.
func (f Flow) JudgeKind(i int) string {
	if slices.ContainsFunc(f[:min(i, len(f))], func(e Entry) bool { return e.Step == "implement" }) {
		return "change"
	}

	return "plan"
}

// PublishedBefore reports a pr or push among the entries before i, which
// a CI review needs.
func (f Flow) PublishedBefore(i int) bool {
	return slices.ContainsFunc(f[:min(i, len(f))], func(e Entry) bool { return e.Step == "pr" || e.Step == "push" })
}

// Delivered is the last delivering entry's step, "" with none.
func (f Flow) Delivered() string {
	last := ""

	for _, e := range f {
		if slices.Contains(Delivering, e.Step) {
			last = e.Step
		}
	}

	return last
}

// Delivering are the generic steps whose output is a run's shipment.
var Delivering = []string{"implement", "commit", "push", "pr", "deploy"}

// Guarded are the entries ship's catchup guard runs before.
var Guarded = []string{"implement", "pr", "judge"}

// GenericSteps is every generic step a flow entry may name, plus finish,
// which drive runs. A label never takes one of these names, so status and
// stats never show a step that did not run.
var GenericSteps = []string{
	"brainstorm", "capture", "catchup", "cleanup", "commit", "debug", "demo", "deploy", "explain", "fetch",
	"finish", "handoff", "implement", "judge", "plan", "pr", "push", "resolve", "review", "standup",
}

// MarshalJSON writes the flow as its written entries, labels in brackets.
func (f Flow) MarshalJSON() ([]byte, error) { return json.Marshal(f.Written()) }

// UnmarshalJSON reads what MarshalJSON writes.
func (f *Flow) UnmarshalJSON(b []byte) error {
	var written []string
	if err := json.Unmarshal(b, &written); err != nil {
		return err
	}

	*f = Of(written)

	return nil
}
