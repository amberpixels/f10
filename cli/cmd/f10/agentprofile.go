package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A step's agent profile comes from three layers of one map, later winning
// key by key (the user's ~/.f10/agents.md, the project's agents:, a run's
// flag as `default`), then `default` overlaid segment by segment by
// `<step>` and `<step>.<kind>`. Only the task agent and the spawning steps
// read a key; any other key is accepted and idle until a lever exists.

// An agentEntry is one key of the merged map with where it came from.
type agentEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// agentMap is the merged map, by key.
type agentMap map[string]agentEntry

// agentFlagOrigin is the origin of a profile a command line gave.
const agentFlagOrigin = "--agent"

// agentLayers merges the user's file, the project's map and a flag's
// profile, later winning key by key. flag "" adds nothing.
func agentLayers(eff *facts.Effective, flag string) (agentMap, error) {
	m := agentMap{}

	user, path, err := readAgentsConfig()
	if err != nil {
		return nil, err
	}

	for key, value := range user {
		m[key] = agentEntry{Key: key, Value: value, Origin: "declared (user, " + path + ")"}
	}

	if eff != nil {
		for key, value := range eff.Agents {
			m[key] = agentEntry{Key: key, Value: value, Origin: eff.AgentsOrigin[key]}
		}
	}

	if flag = strings.TrimSpace(flag); flag != "" {
		if _, err := agentctl.ParseProfile(flag); err != nil {
			return nil, fmt.Errorf("--agent %w", err)
		}

		m = m.with(flag, agentFlagOrigin)
	}

	return m, nil
}

// agentKeyFlag holds a run's profile in the map: a key no file can declare
// (CheckAgentKey refuses it), laid over `default` segment by segment, so
// `--agent low` keeps the map's model.
const agentKeyFlag = "--agent"

// with is m with a run's profile on top: the flag, a drive task's own
// suffix, or the branch's record. A later one lays over an earlier one
// segment by segment, so `FT-1:low --agent sonnet` runs sonnet at low.
func (m agentMap) with(value, origin string) agentMap {
	if value = strings.TrimSpace(value); value == "" {
		return m
	}

	out := maps.Clone(m)
	if out == nil {
		out = agentMap{}
	}

	if prior, ok := out[agentKeyFlag]; ok {
		over, errOver := agentctl.ParseProfile(value)
		under, errUnder := agentctl.ParseProfile(prior.Value)

		if errOver == nil && errUnder == nil {
			value, origin = over.Over(under).Short(), origin+" over "+prior.Origin
		}
	}

	out[agentKeyFlag] = agentEntry{Key: agentKeyFlag, Value: value, Origin: origin}

	return out
}

// withBranch lays the profile a branch records under the run, so a step's
// subagent runs on what the task agent was started on; nothing recorded
// leaves m as it is.
func (m agentMap) withBranch(ctx context.Context, main, branch string) (agentMap, error) {
	if branch == "" {
		return m, nil
	}

	recorded, err := recordedProfile(ctx, main, branch)
	if err != nil {
		return nil, err
	}

	if recorded.IsDefault() {
		return m, nil
	}

	return m.with(recorded.Short(), "recorded on branch "+branch), nil
}

// A resolvedProfile is a step's profile with the key and layer each
// segment came from, and the model's correction or fallback, if any.
type resolvedProfile struct {
	Profile agentctl.Profile
	Sources map[string]string // "model", "effort" to "<key> (<origin>)"
	Note    string            // the model read as another name
	Fell    string            // the model as typed that no family matched, dropped to the kind's default
}

// profileFor is the profile step runs on for a task of kind ("" for any).
// Every value went through the vocabulary when it was read, so a parse
// error here is a programming error, not the user's.
func (m agentMap) profileFor(step, kind string) resolvedProfile {
	keys := []string{facts.AgentKeyDefault, agentKeyFlag}
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

// kindsScoped lists the task kinds the map scopes step by, sorted.
func (m agentMap) kindsScoped(step string) []string {
	var kinds []string

	for key := range m {
		if s, kind, ok := strings.Cut(key, "."); ok && s == step {
			kinds = append(kinds, kind)
		}
	}

	slices.Sort(kinds)

	return kinds
}

// agentReach says what runs on a step's key, or "" when nothing does yet.
var agentReach = map[string]string{
	facts.AgentKeyDefault: "the task's agent (f10 start, f10 drive)",
	"review":              "the blind reviewer, a subagent",
	"judge":               "the blind judge (judge --blind), a subagent",
	"cleanup":             "the rules check, a subagent",
}

// agentReachNone is the line for a step no lever reaches.
const agentReachNone = "no lever yet: runs inline on the task agent's profile"

// modelWarning is the line a model no family matches prints, once, before
// the agent starts on the kind's default instead.
func modelWarning(typed, settings string) string {
	fallback := "claude's default model"
	if d := claudeDefaultModel(settings); d != "" {
		fallback = d + " (your claude default)"
	}

	warning := "!!! --model " + typed + " is not a model name f10 recognizes: the agent starts on " + fallback + " instead"

	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")).Render(warning)
}

// lines are what a resolved profile prints beneath a report: the model
// read as another name, or the warning for one no family matched.
func (r resolvedProfile) lines(settings string) []string {
	var out []string

	if r.Note != "" {
		out = append(out, r.Note)
	}

	if r.Fell != "" {
		out = append(out, modelWarning(r.Fell, settings))
	}

	return out
}

func agentsCommand() *cli.Command {
	return &cli.Command{
		Name:      "agents",
		Usage:     "print the agent profile each step runs on, and where each segment came from",
		ArgsUsage: "[<step>...]",
		Description: "With no step, the merged agents map: the user's ~/.f10/agents.md, the project's agents:, " +
			"later winning key by key, each value with its origin. With steps, one line per step: its profile " +
			"(kind:model:effort), the key and layer each segment came from, and what runs on it; " +
			"a step scoped by task kind prints a line per kind unless --kind picks one.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "kind",
				Usage: "the task `KIND` (feature, bug, refactor, spike) a scoped key applies to",
			},
			&cli.StringFlag{Name: "agent", Usage: "a run's `PROFILE`, laid over default as f10 start --agent does"},
			&cli.BoolFlag{Name: "json", Usage: "emit the same lines as JSON"},
		},
		Action: runAgents,
	}
}

// An agentLine is one step as `f10 agents` prints it.
type agentLine struct {
	Step    string            `json:"step"`
	Kind    string            `json:"kind,omitempty"` // the task kind the line is for
	Profile string            `json:"profile"`
	Agent   string            `json:"agent"` // the agent kind
	Model   string            `json:"model,omitempty"`
	Effort  string            `json:"effort,omitempty"`
	Tool    agentTool         `json:"tool"`
	Sources map[string]string `json:"sources,omitempty"`
	Reach   string            `json:"reach"`
	Notes   []string          `json:"notes,omitempty"`
}

// An agentTool is the profile as the Agent tool takes it.
type agentTool struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

func runAgents(ctx context.Context, cmd *cli.Command) error {
	kind := strings.ToLower(strings.TrimSpace(cmd.String("kind")))
	if kind != "" && !slices.Contains(facts.TaskKinds, kind) {
		return fmt.Errorf("--kind %s is not a task kind (%s)", kind, strings.Join(facts.TaskKinds, ", "))
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	m, err := agentLayers(t.eff, cmd.String("agent"))
	if err != nil {
		return err
	}

	// no flag: the branch's record is what the task agent started on, so a
	// spawning step's subagent follows it
	if strings.TrimSpace(cmd.String("agent")) == "" {
		main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
		if m, err = m.withBranch(ctx, main, gitx.CurrentBranch(ctx, t.lay.CheckoutRoot)); err != nil {
			return err
		}
	}

	steps := cmd.Args().Slice()
	if len(steps) == 0 {
		return printAgentMap(cmd.Writer, m, cmd.Bool("json"))
	}

	var lines []agentLine

	for _, step := range steps {
		step = strings.ToLower(strings.TrimSpace(step))
		if step == "" {
			continue
		}

		kinds := []string{kind}
		if kind == "" {
			kinds = append(kinds, m.kindsScoped(step)...)
		}

		for _, k := range kinds {
			lines = append(lines, agentLineFor(m, step, k, claudeSettingsPath()))
		}
	}

	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, lines)
	}

	for _, l := range lines {
		fmt.Fprintln(cmd.Writer, l.String())
	}

	return nil
}

func agentLineFor(m agentMap, step, kind, settings string) agentLine {
	r := m.profileFor(step, kind)
	toolModel, toolEffort := r.Profile.Tool()

	reach, ok := agentReach[step]
	if !ok {
		reach = agentReachNone
	}

	return agentLine{
		Step:    step,
		Kind:    kind,
		Profile: r.Profile.String(),
		Agent:   r.Profile.Kind,
		Model:   r.Profile.Model,
		Effort:  r.Profile.Effort,
		Tool:    agentTool{Model: toolModel, Effort: toolEffort},
		Sources: r.Sources,
		Reach:   reach,
		Notes:   r.lines(settings),
	}
}

// String is the line: `review: claude:opus:high  (model: default (declared
// (user, ~/.f10/agents.md)), effort: review (declared (main, frontmatter)))
// Agent tool: model opus, effort high  - the blind reviewer, a subagent`.
func (l agentLine) String() string {
	name := l.Step
	if l.Kind != "" {
		name += "." + l.Kind
	}

	var b strings.Builder

	b.WriteString(name + ": " + l.Profile)

	var from []string

	for _, seg := range []string{"model", "effort"} {
		if s := l.Sources[seg]; s != "" {
			from = append(from, seg+": "+s)
		}
	}

	if len(from) > 0 {
		b.WriteString("  (" + strings.Join(from, ", ") + ")")
	} else {
		b.WriteString("  (the kind's default: no flag passed)")
	}

	if l.Reach != agentReachNone {
		var tool []string
		if l.Tool.Model != "" {
			tool = append(tool, "model "+l.Tool.Model)
		}

		if l.Tool.Effort != "" {
			tool = append(tool, "effort "+l.Tool.Effort)
		}

		if len(tool) > 0 {
			b.WriteString("  Agent tool: " + strings.Join(tool, ", "))
		}
	}

	b.WriteString("  - " + l.Reach)

	for _, n := range l.Notes {
		b.WriteString("\n  " + n)
	}

	return b.String()
}

// printAgentMap prints the merged map, one key per line with its origin.
func printAgentMap(w io.Writer, m agentMap, asJSON bool) error {
	keys := slices.Sorted(maps.Keys(m))

	if asJSON {
		entries := make([]agentEntry, 0, len(keys))
		for _, k := range keys {
			entries = append(entries, m[k])
		}

		return encodeJSON(w, entries)
	}

	if len(keys) == 0 {
		fmt.Fprintf(w, "agents: none declared; every step runs on the kind's default (%s, no flag passed)\n",
			agentctl.DefaultKind)

		return nil
	}

	width := 0
	for _, k := range keys {
		width = max(width, len(k)+1)
	}

	for _, k := range keys {
		e := m[k]
		fmt.Fprintf(w, "%-*s %s  %s\n", width, k+":", e.Value, lipgloss.NewStyle().Faint(true).Render(e.Origin))
	}

	return nil
}

// errBothAgentFlags is --model beside --agent: two defaults for one run.
var errBothAgentFlags = errors.New("--model and --agent both name the agent's profile: give one " +
	"(--model is the one-segment --agent, a model alone)")

// profileFlag is the profile a command line names: --agent as given, or
// --model, which reads a model only, never an effort word.
func profileFlag(agent, model string) (string, error) {
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
