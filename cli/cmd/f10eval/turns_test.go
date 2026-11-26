package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// fakeClaude puts a claude on PATH that answers turn n with session sess-n,
// logs each call's arguments, and fails the turn named by failAt.
func fakeClaude(t *testing.T, failAt string) string {
	t.Helper()

	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := `#!/bin/sh
n=$(( $(cat "` + log + `" 2>/dev/null | wc -l) + 1 ))
echo "$*" >> "` + log + `"
echo '{"type":"system","subtype":"init","session_id":"sess-'$n'"}'
echo 'not json'
echo '{"type":"assistant","message":{"id":"m'$n'","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t'$n'","name":"Skill","input":{"skill":"f10:plan"}}],"usage":{"input_tokens":10,"output_tokens":10}}}'
if [ "$n" = "` + failAt + `" ]; then
  echo '{"type":"result","total_cost_usd":0.25,"duration_ms":5,"is_error":true,"result":"boom"}'
  exit 1
fi
echo "{\"type\":\"result\",\"total_cost_usd\":0.25,\"duration_ms\":5,\"result\":\"reply $n\",\"session_id\":\"sess-$n\"}"
`

	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return log
}

func turnsRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	repo := filepath.Join(root, "evals", "scenarios", "s", "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return root
}

// Each later turn resumes the session the turn before printed, the report
// sums every turn, and the rubric grades the last turn's reply.
//
//nolint:paralleltest // fakeClaude sets PATH
func TestTurnsThreadTheSession(t *testing.T) {
	log := fakeClaude(t, "")
	root := turnsRoot(t)

	var graded string

	s := Scenario{
		Name: "s", Task: "EV-1", Prompts: []string{"one", "two", "three"},
		Checks: []string{"rubric"}, Rubric: &Rubric{Lines: []string{"a"}, Reply: true},
	}
	checks["rubric"] = func(_ context.Context, _ string, s Scenario) Check {
		graded = s.reply

		return Check{Name: "rubric", Pass: true}
	}

	t.Cleanup(func() { checks["rubric"] = checkRubric })

	def, _, _ := agentctl.Profile{}.Resolve()
	rep := runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})

	if rep.Error != "" || rep.CostUSD != 0.75 || rep.DurationMS != 15 {
		t.Errorf("report %+v, want three turns summed and no error", rep)
	}

	if graded != "reply 3" {
		t.Errorf("graded %q, want the last turn's reply", graded)
	}

	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	calls := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(calls) != 3 || strings.Contains(calls[0], "--resume") ||
		!strings.Contains(calls[1], "--resume sess-1 two") || !strings.Contains(calls[2], "--resume sess-2 three") {
		t.Errorf("calls %q, want each later turn resuming the one before", calls)
	}
}

//nolint:paralleltest // fakeClaude sets PATH
func TestATurnThatFailsEndsTheRun(t *testing.T) {
	log := fakeClaude(t, "2")
	root := turnsRoot(t)

	s := Scenario{Name: "s", Task: "EV-1", Prompts: []string{"one", "two", "three"}}

	def, _, _ := agentctl.Profile{}.Resolve()
	rep := runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})

	if rep.Error != "turn 2: boom" || rep.CostUSD != 0.5 || rep.Pass {
		t.Errorf("report %+v, want it stopped at turn 2 with two turns' cost", rep)
	}

	body, _ := os.ReadFile(log)
	if n := strings.Count(string(body), "\n"); n != 2 {
		t.Errorf("%d calls, want none after the failed turn", n)
	}
}
