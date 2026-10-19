package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

func TestTaskRuns(t *testing.T) {
	ship := &state.Run{Session: "ship", Task: "gh-1", Capture: "prior", Plan: "done", Ship: "blocked", Ask: "1. Merge?"}
	fresh := &state.Run{Session: "fresh", Capture: "pending", Plan: "pending", Ship: "pending"}
	fetching := &state.Run{Session: "fetching", Capture: "skipped", Plan: "running", Ship: "pending"}
	capture := &state.Run{Session: "capture", Capture: "running", Plan: "pending", Ship: "pending"}
	other := &state.Run{Session: "other", Task: "GH-7", Capture: "done", Plan: "running", Ship: "pending"}

	own, others := taskRuns([]*state.Run{capture, ship, other, fresh, fetching}, "GH-1")

	if got := sessions(own); got != "ship fresh fetching" {
		t.Errorf("own = %q, want the matching task and the runs fetch has not named yet, in order", got)
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
	taskFallback(t.Context(), in, []*state.Run{capture}).render(&out, 0)

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

	fb := taskFallback(t.Context(), in, nil)

	var out bytes.Buffer
	fb.render(&out, 0)

	for _, absent := range []string{"agent", "plan", "pr "} {
		if strings.Contains(out.String(), absent) {
			t.Errorf("output has a %q row:\n%s", absent, out.String())
		}
	}

	if !strings.Contains(out.String(), "none for GH-1") {
		t.Errorf("output lacks the run row:\n%s", out.String())
	}

	out.Reset()

	if err := encodeJSON(&out, fb); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out.String())
	}

	if _, ok := got["plan"]; ok {
		t.Errorf("JSON has a plan for a missing file: %s", out.String())
	}

	if others, ok := got["others"].([]any); !ok || len(others) != 0 {
		t.Errorf("JSON others = %v, want an empty list", got["others"])
	}
}

// The runs the state script leaves at each point a task's own agent can be
// read from, classified against the task: a ship seeded from a reference,
// the same once fetch opens the plan phase, the --local pipeline word
// before the id, and a capture, which is about another task.
func TestTaskRunsFromTheScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}

	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "..", "..", "..", "bin", "f10-state.sh")
	dir := t.TempDir()

	run := func(session string, args ...string) {
		t.Helper()

		cmd := exec.Command(script, args...)
		cmd.Env = append(os.Environ(), "F10_STATE_DIR="+dir, "F10_SESSION_ID="+session)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	load := func(session string) *state.Run {
		t.Helper()

		r, err := state.Load(state.Path(dir, session))
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	for _, tc := range []struct {
		name  string
		steps [][]string
		own   bool
	}{
		{"ship seeded", [][]string{{"seed", "ship", "", "GH-1", dir}}, true},
		{"ship in fetch", [][]string{{"seed", "ship", "", "GH-1", dir}, {"set", "plan", "running"}}, true},
		{"ship local", [][]string{{"seed", "ship", "", "local: GH-1", dir}}, true},
		{"plan then ship local", [][]string{
			{"seed", "plan", "", "GH-1", dir},
			{"task", "GH-1"},
			{"set", "plan", "done"},
			{"seed", "ship", "", "local: GH-1", dir},
		}, true},
		{"capture", [][]string{{"seed", "capture", "", "add a retry to the client", dir}}, false},
		{"ship a description", [][]string{{"seed", "ship", "", "add a retry to the client", dir}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := strings.ReplaceAll(tc.name, " ", "-")
			for _, args := range tc.steps {
				run(session, args...)
			}

			r := load(session)
			own, _ := taskRuns([]*state.Run{r}, "GH-1")

			if got := len(own) == 1; got != tc.own {
				t.Errorf("own = %v, want %v for %+v", got, tc.own, *r)
			}
		})
	}
}

// A driven agent's run is its own task's, so its ask still renders.
func TestRenderRunKeepsTheAsk(t *testing.T) {
	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Keep it?"}

	own, _ := taskRuns([]*state.Run{r}, "GH-1")
	if len(own) != 1 {
		t.Fatal("the driven run is not the task's own")
	}

	var out bytes.Buffer
	renderRun(&out, own[0], true, time.Now(), 0)

	if !strings.Contains(out.String(), "1. Keep it?") {
		t.Errorf("output lacks the ask:\n%s", out.String())
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
