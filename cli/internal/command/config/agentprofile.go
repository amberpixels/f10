package config

import (
	"cmp"
	"context"
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
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A step's profile is the merged agents map with a run's profile laid over
// `default`, then `default` overlaid segment by segment by `<step>` and
// `<step>.<kind>`. Only the task agent and the spawning steps read a key;
// any other key is accepted and idle until a lever exists.

// agentReach says what runs on a step's key, or "" when nothing does yet.
var agentReach = map[string]string{
	facts.AgentKeyDefault: "the task's agent (f10 start, f10 drive)",
	"review":              "the blind reviewer, a subagent",
	"judge":               "the blind judge (judge --blind), a subagent",
	"cleanup":             "the rules check, a subagent",
}

// AgentReachNone is the line for a step no lever reaches.
const AgentReachNone = "no lever yet: runs inline on the task agent's profile"

func AgentsCommand() *cli.Command {
	return &cli.Command{
		Name:      "agents",
		Usage:     "print the agent profile each step runs on, and where each segment came from",
		ArgsUsage: "[<step>...]",
		Description: "With no step, the merged agents map: the user's ~/.f10/agents.md, the project's agents:, " +
			"later winning key by key, each value with its origin; the run's pace (--pace, the branch's, the project's) " +
			"lays its effort dials on top. With steps, one line per step: its profile " +
			"(kind:model:effort), the key and layer each segment came from, and what runs on it; " +
			"a step scoped by task kind prints a line per kind unless --kind picks one.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "kind",
				Usage: "the task `KIND` (feature, bug, refactor, spike) a scoped key applies to",
			},
			&cli.StringFlag{Name: "agent", Usage: "a run's `PROFILE`, laid over default as f10 start --agent does"},
			runplan.PaceFlag(),
			ui.JSONFlag("emit the same lines as JSON"),
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

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{
		Pace:    cmd.String("pace"),
		Profile: cmd.String("agent"),
		Main:    cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Branch:  gitx.CurrentBranch(ctx, t.Layout.CheckoutRoot),
	})
	if err != nil {
		return err
	}

	// no flag: the branch's record is what the task agent started on, so a
	// spawning step's subagent follows it
	m, err := run.Agents(ctx)
	if err != nil {
		return err
	}

	steps := cmd.Args().Slice()
	if len(steps) == 0 {
		return PrintAgentMap(cmd.Writer, m, cmd.Bool("json"))
	}

	var lines []agentLine

	for _, step := range steps {
		step = strings.ToLower(strings.TrimSpace(step))
		if step == "" {
			continue
		}

		kinds := []string{kind}
		if kind == "" {
			kinds = append(kinds, m.KindsScoped(step)...)
		}

		for _, k := range kinds {
			lines = append(lines, AgentLineFor(m, step, k, runplan.ClaudeSettingsPath()))
		}
	}

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, lines)
	}

	for _, l := range lines {
		fmt.Fprintln(cmd.Writer, l.String())
	}

	return nil
}

func AgentLineFor(m runplan.AgentMap, step, kind, settings string) agentLine {
	r := m.ProfileFor(step, kind)
	toolModel, toolEffort := r.Profile.Tool()

	reach, ok := agentReach[step]
	if !ok {
		reach = AgentReachNone
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
		Notes:   r.Lines(settings),
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

	if l.Reach != AgentReachNone {
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

// PrintAgentMap prints the merged map, one key per line with its origin.
func PrintAgentMap(w io.Writer, m runplan.AgentMap, asJSON bool) error {
	keys := slices.Sorted(maps.Keys(m))

	if asJSON {
		entries := make([]runplan.AgentEntry, 0, len(keys))
		for _, k := range keys {
			entries = append(entries, m[k])
		}

		return ui.JSON(w, entries)
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
