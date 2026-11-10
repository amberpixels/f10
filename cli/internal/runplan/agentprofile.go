package runplan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/facts"
)

// An AgentEntry is one key of the merged map with where it came from.
type AgentEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// AgentMap is the merged map, by key.
type AgentMap map[string]AgentEntry

// With is m with a run's profile on top: the flag, a drive task's own
// suffix, or the branch's record. A later one lays over an earlier one
// segment by segment, so `FT-1:low --agent sonnet` runs sonnet at low.
func (m AgentMap) With(value, origin string) AgentMap {
	if value = strings.TrimSpace(value); value == "" {
		return m
	}

	out := maps.Clone(m)
	if out == nil {
		out = AgentMap{}
	}

	if prior, ok := out[AgentKeyFlag]; ok {
		over, errOver := agentctl.ParseProfile(value)
		under, errUnder := agentctl.ParseProfile(prior.Value)

		if errOver == nil && errUnder == nil {
			value, origin = over.Over(under).Short(), origin+" over "+prior.Origin
		}
	}

	out[AgentKeyFlag] = AgentEntry{Key: AgentKeyFlag, Value: value, Origin: origin}

	return out
}

// WithBranch lays the profile a branch records under the run, so a step's
// subagent runs on what the task agent was started on; nothing recorded
// leaves m as it is.
func (m AgentMap) WithBranch(ctx context.Context, main, branch string) (AgentMap, error) {
	if branch == "" {
		return m, nil
	}

	recorded, err := RecordedProfile(ctx, main, branch)
	if err != nil {
		return nil, err
	}

	if recorded.IsDefault() {
		return m, nil
	}

	return m.With(recorded.Short(), "recorded on branch "+branch), nil
}

// ProfileFor is the profile step runs on for a task of kind ("" for any).
// Every value went through the vocabulary when it was read, so a parse
// error here is a programming error, not the user's.
func (m AgentMap) ProfileFor(step, kind string) resolvedProfile {
	keys := []string{facts.AgentKeyDefault, AgentKeyFlag}
	if step != "" && step != facts.AgentKeyDefault {
		keys = append(keys, step)
		if kind != "" {
			keys = append(keys, step+"."+kind)
		}
	}

	r := resolvedProfile{Sources: map[string]string{}}

	var p agentctl.Profile

	for _, key := range keys {
		e, ok := m[key]
		if !ok {
			continue
		}

		over, err := agentctl.ParseProfile(e.Value)
		if err != nil {
			continue
		}

		p = over.Over(p)

		from := key + " (" + e.Origin + ")"
		if over.Model != "" {
			r.Sources["model"] = from
		}

		if over.Effort != "" {
			r.Sources["effort"] = from
		}
	}

	typed := p.Model

	resolved, note, ok := p.Resolve()
	r.Profile, r.Note = resolved, note

	if !ok && typed != "" {
		r.Fell = typed
		delete(r.Sources, "model")
	}

	return r
}

// KindsScoped lists the task kinds the map scopes step by, sorted.
func (m AgentMap) KindsScoped(step string) []string {
	var kinds []string

	for key := range m {
		if s, kind, ok := strings.Cut(key, "."); ok && s == step {
			kinds = append(kinds, kind)
		}
	}

	slices.Sort(kinds)

	return kinds
}

// withPace lays a pace's effort dials over the step keys they name, so a
// fast pace's judge runs at low effort on the map's judge model.
func (m AgentMap) withPace(set *facts.PaceSet, pace runPace) AgentMap {
	if pace.Name == "" {
		return m
	}

	out := maps.Clone(m)
	if out == nil {
		out = AgentMap{}
	}

	capped, caps := set.Capped(pace.Name)

	for step, d := range capped.Steps {
		if d.Effort == "" || d.Off {
			continue
		}

		value, origin := d.Effort, "pace "+pace.Name+" ("+set.Origins[pace.Name+"."+step].String()+")"
		if c, ok := caps[step+".effort"]; ok {
			origin = c.Origin + "; " + c.Capped
		}

		if prior, ok := out[step]; ok {
			under, err := agentctl.ParseProfile(prior.Value)
			over, errOver := agentctl.ParseProfile(value)

			if err == nil && errOver == nil {
				value, origin = over.Over(under).Short(), origin+" over "+prior.Origin
			}
		}

		out[step] = AgentEntry{Key: step, Value: value, Origin: origin}
	}

	return out
}

// AgentFlagOrigin is the origin of a profile a command line gave.
const AgentFlagOrigin = "--agent"

// AgentMapOf is the map facts merged, each key with its layer.
func AgentMapOf(eff *facts.Effective) AgentMap {
	m := AgentMap{}

	for key, value := range eff.Agents {
		m[key] = AgentEntry{Key: key, Value: value, Origin: eff.AgentsOrigin[key].String()}
	}

	return m
}

// AgentKeyFlag holds a run's profile in the map: a key no file can declare
// (CheckAgentKey refuses it), laid over `default` segment by segment, so
// `--agent low` keeps the map's model.
const AgentKeyFlag = "--agent"

// A resolvedProfile is a step's profile with the key and layer each
// segment came from, and the model's correction or fallback, if any.
type resolvedProfile struct {
	Profile agentctl.Profile
	Sources map[string]string // "model", "effort" to "<key> (<origin>)"
	Note    string            // the model read as another name
	Fell    string            // the model as typed that no family matched, dropped to the kind's default
}

// Lines are what a resolved profile prints beneath a report: the model
// read as another name, or the warning for one no family matched.
func (r resolvedProfile) Lines(settings string) []string {
	var out []string

	if r.Note != "" {
		out = append(out, r.Note)
	}

	if r.Fell != "" {
		out = append(out, modelWarning(r.Fell, settings))
	}

	return out
}

// modelWarning is the line a model no family matches prints, once, before
// the agent starts on the kind's default instead.
func modelWarning(typed, settings string) string {
	fallback := "claude's default model"
	if d := ClaudeDefaultModel(settings); d != "" {
		fallback = d + " (your claude default)"
	}

	warning := "!!! --model " + typed + " is not a model name f10 recognizes: the agent starts on " + fallback + " instead"

	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")).Render(warning)
}

// errBothAgentFlags is --model beside --agent: two defaults for one run.
var errBothAgentFlags = errors.New("--model and --agent both name the agent's profile: give one " +
	"(--model is the one-segment --agent, a model alone)")

// ProfileFlag is the profile a command line names: --agent as given, or
// --model, which reads a model only, never an effort word.
func ProfileFlag(agent, model string) (string, error) {
	agent, model = strings.TrimSpace(agent), strings.TrimSpace(model)

	switch {
	case agent != "" && model != "":
		return "", errBothAgentFlags
	case agent != "":
		return agent, nil
	case model == "":
		return "", nil
	case strings.Contains(model, ":"):
		return "", fmt.Errorf("--model %s: a model alone; a kind or an effort goes on --agent %s", model, model)
	}

	if p, err := agentctl.ParseProfile(model); err == nil && p.Effort != "" {
		return "", fmt.Errorf("--model %s names an effort, not a model: --agent %s sets the effort", model, model)
	}

	return model, nil
}
