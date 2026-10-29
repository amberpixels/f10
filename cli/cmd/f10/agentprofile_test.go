package main

import (
	"bytes"
	"strings"
	"testing"
)

// A step's line names the profile, where each segment came from, the Agent
// tool form where a subagent runs on it, and the lever that reaches it.
func TestAgentLines(t *testing.T) {
	m := agentMap{
		"default":  {Key: "default", Value: "opus:high", Origin: "declared (user, ~/.f10/agents.md)"},
		"review":   {Key: "review", Value: "sonnet", Origin: "declared (main, frontmatter)"},
		"plan.bug": {Key: "plan.bug", Value: "medium", Origin: "declared (main, frontmatter)"},
	}

	cases := []struct {
		step, kind string
		want       []string
		notWant    string
	}{
		{
			step: "review",
			want: []string{
				"review: claude:sonnet:high",
				"model: review (declared (main, frontmatter))", "effort: default (declared (user, ~/.f10/agents.md))",
				"Agent tool: model sonnet, effort high", "the blind reviewer",
			},
		},
		{
			step: "plan", kind: "bug",
			want: []string{"plan.bug: claude:opus:medium", agentReachNone}, notWant: "Agent tool",
		},
		{step: "default", want: []string{"default: claude:opus:high", "the task's agent"}},
	}

	for _, c := range cases {
		line := agentLineFor(m, c.step, c.kind, "").String()
		for _, w := range c.want {
			if !strings.Contains(line, w) {
				t.Errorf("%s.%s line %q lacks %q", c.step, c.kind, line, w)
			}
		}

		if c.notWant != "" && strings.Contains(line, c.notWant) {
			t.Errorf("%s.%s line %q carries %q", c.step, c.kind, line, c.notWant)
		}
	}

	line := agentLineFor(agentMap{}, "implement", "", "").String()
	if !strings.Contains(line, "the kind's default: no flag passed") {
		t.Errorf("no map: %q", line)
	}

	if kinds := m.kindsScoped("plan"); len(kinds) != 1 || kinds[0] != "bug" {
		t.Errorf("kinds scoped for plan: %v", kinds)
	}

	var b bytes.Buffer
	if err := printAgentMap(&b, m, false); err != nil {
		t.Fatal(err)
	}

	if out := b.String(); !strings.HasPrefix(out, "default:") || !strings.Contains(out, "review:   sonnet") {
		t.Errorf("map:\n%s", out)
	}

	b.Reset()

	if err := printAgentMap(&b, agentMap{}, false); err != nil || !strings.Contains(b.String(), "none declared") {
		t.Errorf("empty map: %v %q", err, b.String())
	}
}
