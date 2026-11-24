package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// cannedGrader answers with lines and cost, and counts its calls in calls.
func cannedGrader(calls *int, cost float64, lines []graderLine, err error) grader {
	return func(_ context.Context, _ []string, _ string) (graderResult, error) {
		*calls++

		return graderResult{CostUSD: cost, Lines: lines}, err
	}
}

func rubricScenario(g grader) Scenario {
	return Scenario{
		Task:   "T-1",
		Prompt: "/f10:brainstorm secret-prompt",
		Checks: []string{"rubric"},
		Rubric: &Rubric{Lines: []string{"names the risk", "stays under ten lines"}, Reply: true},
		reply:  "The risk is a stale cache.",
		grade:  g,
	}
}

func TestRubricCheck(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		lines  []graderLine
		err    error
		pass   bool
		detail string
	}{
		"all pass": {
			lines:  []graderLine{{Line: 1, Pass: true, Reason: "names it"}, {Line: 2, Pass: true, Reason: "one line"}},
			pass:   true,
			detail: "2 of 2 lines pass",
		},
		"one fails": {
			lines:  []graderLine{{Line: 1, Pass: true, Reason: "names it"}, {Line: 2, Pass: false, Reason: "twelve lines"}},
			detail: "1 of 2 lines pass; line 2: twelve lines",
		},
		"one not graded": {
			lines:  []graderLine{{Line: 1, Pass: true, Reason: "names it"}},
			detail: "line 2: not graded",
		},
		"grader error": {
			err:    errors.New("grade is not JSON"),
			detail: "grader: grade is not JSON",
		},
	} {
		calls := 0
		got := checkRubric(t.Context(), t.TempDir(), rubricScenario(cannedGrader(&calls, 0.03, c.lines, c.err)))

		if got.Pass != c.pass || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s = %+v, want pass %v with %q", name, got, c.pass, c.detail)
		}

		if calls != 1 || got.Rubric == nil || got.Rubric.CostUSD != 0.03 || got.Rubric.Model != graderModel {
			t.Errorf("%s: %d calls, rubric %+v", name, calls, got.Rubric)
		}

		if c.err == nil && (len(got.Rubric.Lines) != 2 || got.Rubric.Lines[1].Line != "stays under ten lines") {
			t.Errorf("%s: lines %+v, want both with their text", name, got.Rubric.Lines)
		}
	}
}

// Missing output is named without a grader call, since grading nothing
// would spend to say so.
func TestRubricCheckGradesNothingMissing(t *testing.T) {
	t.Parallel()

	calls := 0

	s := rubricScenario(cannedGrader(&calls, 0, nil, nil))
	s.reply = "  "
	s.Rubric.Files = []string{"PLAN.md"}

	got := checkRubric(t.Context(), t.TempDir(), s)
	if got.Pass || calls != 0 || !strings.Contains(got.Detail, "no reply to grade") ||
		!strings.Contains(got.Detail, "no file PLAN.md to grade") {
		t.Errorf("got %+v after %d calls", got, calls)
	}
}

// The prompt holds the numbered lines and the graded output, and nothing
// else from the scenario.
func TestRubricPrompt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("# the plan body"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := rubricScenario(nil)
	s.Rubric.Files = []string{"PLAN.md"}

	prompt, problems := rubricPrompt(dir, s)
	if len(problems) > 0 {
		t.Fatal(problems)
	}

	for _, want := range []string{
		"1. names the risk\n2. stays under ten lines", "<reply>\nThe risk is a stale cache.\n</reply>",
		"<file path=\"PLAN.md\">\n# the plan body\n</file>",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "secret-prompt") || strings.Contains(prompt, "T-1") {
		t.Errorf("prompt carries the scenario:\n%s", prompt)
	}
}

func TestParseGrade(t *testing.T) {
	t.Parallel()

	g, err := parseGrade([]byte(`{"total_cost_usd":0.01,"result":"x",`+
		`"structured_output":{"lines":[{"line":1,"pass":true,"reason":"ok"}]}}`), nil)
	if err != nil || g.CostUSD != 0.01 || len(g.Lines) != 1 || !g.Lines[0].Pass {
		t.Errorf("structured: %+v %v", g, err)
	}

	g, err = parseGrade([]byte(`{"total_cost_usd":0.02,`+
		`"result":"{\"lines\":[{\"line\":1,\"pass\":false,\"reason\":\"no\"}]}"}`), nil)
	if err != nil || len(g.Lines) != 1 || g.Lines[0].Reason != "no" {
		t.Errorf("result fallback: %+v %v", g, err)
	}

	// the spend happened, so the cost stays on an unusable grade
	g, err = parseGrade([]byte(`{"total_cost_usd":0.04,"result":"I cannot grade this"}`), nil)
	if err == nil || g.CostUSD != 0.04 {
		t.Errorf("malformed grade: %+v %v", g, err)
	}

	if _, err = parseGrade([]byte(`{"is_error":true,"result":"budget exceeded"}`), nil); err == nil ||
		err.Error() != "budget exceeded" {
		t.Errorf("an error result: %v", err)
	}

	if _, err = parseGrade(nil, errors.New("exit status 1")); err == nil {
		t.Error("a failed call with no output passed")
	}
}

func TestGraderArgs(t *testing.T) {
	t.Parallel()

	args := graderArgs("the prompt")
	for _, want := range [][]string{
		{"--model", graderModel}, {"--tools", ""}, {"--setting-sources", ""}, {"--json-schema", graderSchema},
	} {
		i := slices.Index(args, want[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != want[1] {
			t.Errorf("args lack %q: %q", want, args)
		}
	}

	if slices.Contains(args, "--bare") || args[len(args)-1] != "the prompt" {
		t.Errorf("args %q", args)
	}
}

func TestGraderCost(t *testing.T) {
	t.Parallel()

	list := []Check{{Name: "plan"}, {Name: "rubric", Rubric: &RubricResult{CostUSD: 0.25}}}
	if got := graderCost(list); got != 0.25 {
		t.Errorf("grader cost %v", got)
	}
}

// A rubric that cannot grade, or that no check runs, is refused at load.
func TestLoadScenariosRefusesABadRubric(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		body string
		err  string
	}{
		"ok":               {`{"checks":["rubric"],"rubric":{"lines":["a"],"reply":true}}`, ""},
		"no check":         {`{"rubric":{"lines":["a"],"reply":true}}`, "no rubric check"},
		"no rubric":        {`{"checks":["rubric"]}`, "no rubric lines"},
		"no lines":         {`{"checks":["rubric"],"rubric":{"reply":true}}`, "no rubric lines"},
		"grades no output": {`{"checks":["rubric"],"rubric":{"lines":["a"]}}`, "neither the reply nor any file"},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "s"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "s", "scenario.json"), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := loadScenarios(dir, "")
		if (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v, want %q", name, err, c.err)
		}
	}
}
