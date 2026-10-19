package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

func TestTaskRuns(t *testing.T) {
	ship := &state.Run{Session: "ship", Task: "gh-1", Capture: "prior", Plan: "done", Ship: "blocked", Ask: "1. Merge?"}
	fresh := &state.Run{Session: "fresh", Capture: "pending", Plan: "pending", Ship: "pending"}
	capture := &state.Run{Session: "capture", Capture: "running", Plan: "pending", Ship: "pending"}
	other := &state.Run{Session: "other", Task: "GH-7", Capture: "done", Plan: "running", Ship: "pending"}

	own, others := taskRuns([]*state.Run{capture, ship, other, fresh}, "GH-1")

	if got := sessions(own); got != "ship fresh" {
		t.Errorf("own = %q, want the matching task and the fresh ship, in order", got)
	}

	if got := sessions(others); got != "capture other" {
		t.Errorf("others = %q, want the capture and the other task", got)
	}
}

// The issue's repro: GH-1 shipped (plan saved, PR open), then its agent
// captured another task. Status names what the agent is on and reports
// the durable facts instead of the capture run.
func TestStatusFallbackReportsPlanAndPR(t *testing.T) {
	f := newFakes(t)
	f.script(ghView, prOpen)

	root := t.TempDir()
	plan := filepath.Join(root, ".f10", "plans", "GH-1.md")

	if err := os.MkdirAll(filepath.Dir(plan), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(plan, []byte("# GH-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := host{name: "gh", dir: root}
	in := taskStatusInput{id: "GH-1", branch: "GH-1", root: root, plan: plan, host: &h}
	capture := &state.Run{Capture: "running", Plan: "pending", Ship: "pending"}

	var out bytes.Buffer
	statusFallback(t.Context(), &out, in, []*state.Run{capture}, 0)

	for _, want := range []string{
		"none for GH-1 in " + root,
		"- · capture running",
		plan,
		"https://gh/pr/9 (open)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// No plan file, no PR and no other run: only the task and the run rows.
func TestStatusFallbackLeavesOutWhatDoesNotExist(t *testing.T) {
	f := newFakes(t)
	f.fail(ghView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})

	root := t.TempDir()
	h := host{name: "gh", dir: root}
	in := taskStatusInput{id: "GH-1", branch: "GH-1", root: root, plan: filepath.Join(root, "GH-1.md"), host: &h}

	var out bytes.Buffer
	statusFallback(t.Context(), &out, in, nil, 0)

	for _, absent := range []string{"agent", "plan", "pr "} {
		if strings.Contains(out.String(), absent) {
			t.Errorf("output has a %q row:\n%s", absent, out.String())
		}
	}

	if !strings.Contains(out.String(), "none for GH-1") {
		t.Errorf("output lacks the run row:\n%s", out.String())
	}
}

func TestCurrentPhase(t *testing.T) {
	for _, tc := range []struct {
		run  state.Run
		want string
	}{
		{state.Run{Capture: "pending", Plan: "pending", Ship: "pending"}, "capture pending"},
		{state.Run{Capture: "running", Plan: "pending", Ship: "pending"}, "capture running"},
		{state.Run{Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge"}, "ship blocked at judge"},
	} {
		if got := currentPhase(&tc.run); got != tc.want {
			t.Errorf("currentPhase(%+v) = %q, want %q", tc.run, got, tc.want)
		}
	}
}

func sessions(runs []*state.Run) string {
	names := make([]string, 0, len(runs))
	for _, r := range runs {
		names = append(names, r.Session)
	}

	return strings.Join(names, " ")
}
