package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

func toolCall(name string, input map[string]string) eventBlock {
	raw, _ := json.Marshal(input)

	return eventBlock{Type: "tool_use", Name: name, Input: raw}
}

func TestIsPlumbing(t *testing.T) {
	t.Parallel()

	const plugin = "/Users/x/f10"

	bash := func(cmd string) eventBlock { return toolCall("Bash", map[string]string{"command": cmd}) }
	read := func(file string) eventBlock { return toolCall("Read", map[string]string{"file_path": file}) }
	skill := func(s string) eventBlock { return toolCall("Skill", map[string]string{"skill": s}) }

	for _, tc := range []struct {
		name string
		call eventBlock
		want bool
	}{
		{"project load", bash("f10-load-project.sh fetch investigate plan"), true},
		{"conventions load", bash("f10-load-conventions.sh"), true},
		{"load by full path", bash(plugin + "/bin/f10-load-project.sh implement"), true},
		{"help", bash("f10 help --agent"), true},
		{"forward", bash(`f10 forward EV-1 "/f10:plan EV-1 --driven"`), true},
		{"forward then echo", bash(`f10 forward EV-1 "/f10:plan EV-1"; echo "exit=$?"`), false},
		{"badge", bash("f10-state.sh set plan running"), true},
		{"badge chained", bash("f10-state.sh pipeline judge implement pr; f10-state.sh set ship running judge"), true},
		{"badge and load", bash("f10-state.sh set ship running implement && f10-load-project.sh implement"), true},
		{"lore with a semicolon in its message", bash(`f10 lore add intent --change EV-1 -m "a; b && c"`), true},
		{"trailers captured", bash(`trailers="$(f10 trace trailers --step implement)"`), true},
		{"trace state", bash("f10 trace state --step judge"), true},
		{"chained with work", bash("f10-state.sh set plan running; f10 task read EV-1; ls"), false},
		{"task read", bash("f10 task read EV-1"), false},
		{"pipeline", bash("f10 help --agent | head"), false},
		{"plain work", bash("go test ./..."), false},
		{"empty command", bash(""), false},
		{"plugin step", read(plugin + "/steps/plan.md"), true},
		{"plugin mode", read(plugin + "/modes/driven.md"), true},
		{"plugin convention", read(plugin + "/conventions/report.md"), true},
		{"repo's own steps dir", read("/repo/steps/plan.md"), false},
		{"repo file", read("/repo/main.go"), false},
		{"f10 skill", skill("f10:plan"), true},
		{"f10 skill with slash", skill("/f10:ship"), true},
		{"other skill", skill("simplify"), false},
		{"edit", toolCall("Edit", map[string]string{"file_path": plugin + "/steps/plan.md"}), false},
	} {
		if got := isPlumbing(tc.call, "/repo"); got != tc.want {
			t.Errorf("%s: isPlumbing = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// In the canned run the two f10 Skill calls and their results are plumbing:
// four lines of 1.5s each, and the two messages' estimated cost.
func TestPlumbingOfCannedRun(t *testing.T) {
	t.Parallel()

	events, at, err := readTranscript(filepath.Join("testdata", "runfolder", "feature-small.opus-high"))
	if err != nil {
		t.Fatal(err)
	}

	p, ids := plumbingOf(events, at)

	one := estimateUSD("claude-opus-5-5", eventUsage{Input: 1000, Output: 100})
	if p.Calls != 2 || p.TotalCalls != 9 || p.Seconds != 6 || math.Abs(p.CostUSD-2*one) > 1e-9 {
		t.Errorf("plumbing = %+v, want 2 of 9 calls, 6s, $%v", p, 2*one)
	}

	if !ids["t1"] || !ids["t3"] || len(ids) != 2 {
		t.Errorf("ids = %v, want t1 and t3", ids)
	}
}

// A message whose calls mix plumbing and work counts as work, while the
// plumbing call's own result still counts as plumbing.
func TestPlumbingOfMixedMessage(t *testing.T) {
	t.Parallel()

	use := func(msg, id, cmd string) event {
		raw, _ := json.Marshal(map[string]string{"command": cmd})

		return event{Type: "assistant", Message: &eventMessage{ID: msg, Content: []eventBlock{
			{Type: "tool_use", ID: id, Name: "Bash", Input: raw},
		}}}
	}
	result := func(id string) event {
		return event{Type: "user", Message: &eventMessage{Content: []eventBlock{{Type: "tool_result", ToolUseID: id}}}}
	}

	events := []event{
		use("m1", "a", "f10-state.sh set plan running"),
		use("m1", "b", "go test ./..."),
		result("a"),
		result("b"),
		use("m2", "c", "f10-load-project.sh plan"),
		result("c"),
	}

	p, _ := plumbingOf(events, []int64{1000, 2000, 5000, 9000, 10_000, 12_000})

	// result a (3s), then m2 (1s) and result c (2s)
	if p.Calls != 2 || p.TotalCalls != 3 || p.Seconds != 6 {
		t.Errorf("plumbing = %+v, want 2 of 3 calls and 6s", p)
	}
}

// A run with a folder carries its plumbing in the report.
//
//nolint:paralleltest // fakeClaude sets PATH
func TestRunReportsPlumbing(t *testing.T) {
	fakeClaude(t, "")
	root := turnsRoot(t)

	s := Scenario{Name: "s", Task: "EV-1", Prompts: []string{"one", "two"}}
	def, _, _ := agentctl.Profile{}.Resolve()
	rep := runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def,
		runOut{folder: filepath.Join(t.TempDir(), "s")})

	if p := rep.Plumbing; p == nil || p.Calls != 2 || p.TotalCalls != 2 {
		t.Errorf("plumbing = %+v, want both Skill calls counted", p)
	}

	bare := runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})
	if bare.Plumbing != nil {
		t.Errorf("a run with no folder has plumbing %+v, want none", bare.Plumbing)
	}
}
