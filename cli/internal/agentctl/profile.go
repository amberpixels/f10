package agentctl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/model"
)

// A Profile is what one step's agent runs on, `claude:opus:high`. An empty
// segment is left to the layer below; one still empty when the agent
// starts is the kind's own default, no flag passed.
type Profile struct {
	Kind   string
	Model  string
	Effort string
}

// DefaultKind is the kind a profile has when no layer names one.
const DefaultKind = "claude"

// A Kind is one agent backend's vocabulary: the models it takes, the effort
// words it knows, how a profile goes on its command line and how the same
// profile is handed to a subagent through the Agent tool.
type Kind struct {
	Efforts []string
	// resolveModel reads a model as typed: the value the kind takes, a note
	// when that differs from what was typed, and false for no model at all
	resolveModel func(raw string) (value, note string, ok bool)
	// args is the command line for a resolved model and effort
	args func(model, effort string) []string
	// toolModel is the model as the Agent tool names one, "" for none
	toolModel func(model string) string
}

// Kinds is every backend kind a profile may name.
var Kinds = map[string]Kind{
	DefaultKind: {
		Efforts:      []string{"low", "medium", "high", "xhigh", "max"},
		resolveModel: model.Resolve,
		args: func(m, e string) []string {
			var out []string
			if m != "" {
				out = append(out, "--model", m)
			}

			if e != "" {
				out = append(out, "--effort", e)
			}

			return out
		},
		toolModel: toolFamily,
	},
}

// KindNames lists the registered kinds for messages.
func KindNames() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for name := range Kinds {
			if !yield(name) {
				return
			}
		}
	})
}

// ParseProfile reads raw as typed: `kind:model:effort`, `model:effort`, or
// one segment, a model unless it is exactly one of the kind's effort words.
// The kind is checked against Kinds and the effort against that kind; the
// model stays as typed, since resolving it is the kind's (Resolve). An
// empty profile parses to the zero Profile: nothing chosen.
func ParseProfile(raw string) (Profile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil
	}

	segs := strings.Split(raw, ":")
	for _, s := range segs {
		if strings.TrimSpace(s) == "" {
			return Profile{}, fmt.Errorf(
				"%q: an empty segment; a profile is kind:model:effort, model:effort or one of them", raw)
		}
	}

	var p Profile

	switch len(segs) {
	case 3:
		p = Profile{Kind: strings.ToLower(segs[0]), Model: segs[1], Effort: strings.ToLower(segs[2])}
	case 2:
		p = Profile{Model: segs[0], Effort: strings.ToLower(segs[1])}
	case 1:
		p = Profile{Model: segs[0]}
		if isEffort(DefaultKind, strings.ToLower(segs[0])) {
			p = Profile{Effort: strings.ToLower(segs[0])}
		}
	default:
		return Profile{}, fmt.Errorf("%q: more than three segments; a profile is kind:model:effort", raw)
	}

	if p.Kind != "" {
		if _, ok := Kinds[p.Kind]; !ok {
			return Profile{}, fmt.Errorf(
				"%q: %s is not an agent kind (%s)", raw, p.Kind, strings.Join(KindNames(), ", "))
		}
	}

	if p.Effort != "" && !isEffort(p.kind(), p.Effort) {
		return Profile{}, fmt.Errorf("%q: %s is not an effort %s knows (%s)",
			raw, p.Effort, p.kind(), strings.Join(Kinds[p.kind()].Efforts, ", "))
	}

	return p, nil
}

func isEffort(kind, word string) bool {
	return slices.Contains(Kinds[kind].Efforts, word)
}

// Over is p laid over under: each segment p names wins, the rest is
// under's. The result's kind is named, the default when neither names one.
func (p Profile) Over(under Profile) Profile {
	out := under
	if p.Kind != "" {
		out.Kind = p.Kind
	}

	if p.Model != "" {
		out.Model = p.Model
	}

	if p.Effort != "" {
		out.Effort = p.Effort
	}

	out.Kind = out.kind()

	return out
}

// Resolve reads p's model through its kind: the profile with the model as
// the kind takes it, a note when that differs from what was typed, and
// false when the model names none the kind knows, in which case the model
// is dropped so the kind's default runs instead. An empty model resolves to
// itself.
func (p Profile) Resolve() (Profile, string, bool) {
	k := Kinds[p.kind()]
	out := p
	out.Kind = p.kind()

	if p.Model == "" {
		return out, "", true
	}

	value, note, ok := k.resolveModel(p.Model)
	if !ok {
		out.Model = ""

		return out, "", false
	}

	out.Model = value

	return out, note, true
}

// Args is the command line p puts on the agent's start, through its kind.
func (p Profile) Args() []string {
	return Kinds[p.kind()].args(p.Model, p.Effort)
}

// Tool is p as the Agent tool takes it: the model's family and the effort
// word, either "" when the profile leaves it to the tool.
func (p Profile) Tool() (string, string) {
	return Kinds[p.kind()].toolModel(p.Model), p.Effort
}

// IsDefault reports a profile that passes nothing: the kind's own default.
func (p Profile) IsDefault() bool {
	return p.Model == "" && p.Effort == ""
}

// String is the profile in full, the kind always named: `claude:opus:high`,
// `claude::high` for an effort alone.
func (p Profile) String() string {
	return p.kind() + ":" + p.Model + ":" + p.Effort
}

// Short is the profile as a person types it: a default kind and empty
// segments dropped, `opus:high`, `opus`, `high`; "" for the default.
func (p Profile) Short() string {
	var segs []string

	if p.Kind != "" && p.Kind != DefaultKind {
		segs = append(segs, p.Kind)
	}

	switch {
	case p.Model != "" && p.Effort != "":
		segs = append(segs, p.Model, p.Effort)
	case p.Model != "":
		segs = append(segs, p.Model)
	case p.Effort != "":
		segs = append(segs, p.Effort)
	}

	return strings.Join(segs, ":")
}

// kind is the profile's kind, the default when none is named.
func (p Profile) kind() string {
	if p.Kind == "" {
		return DefaultKind
	}

	return p.Kind
}

// ParseRecorded reads a profile String wrote back, every segment as it
// was; a bare model from before profiles (the old branch record) reads as
// a model-only profile.
func ParseRecorded(s string) Profile {
	segs := strings.Split(strings.TrimSpace(s), ":")
	if len(segs) != 3 {
		return Profile{Model: strings.TrimSpace(s)}
	}

	return Profile{Kind: segs[0], Model: segs[1], Effort: segs[2]}
}

// toolFamily is a claude model as the Agent tool names it: a family as is,
// a full `claude-<family>-...` id cut to its family, anything else "".
func toolFamily(m string) string {
	lower := strings.ToLower(strings.TrimSpace(m))
	if slices.Contains(model.Families, lower) {
		return lower
	}

	rest, ok := strings.CutPrefix(lower, "claude-")
	if !ok {
		return ""
	}

	family, _, _ := strings.Cut(rest, "-")
	if slices.Contains(model.Families, family) {
		return family
	}

	return ""
}
