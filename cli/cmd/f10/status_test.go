package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

	h := newHost("gh", root)
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
	h := newHost("gh", root)
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

// The runs the state writer leaves at each point a task's own agent can be
// read from, classified against the task: a ship seeded from a reference,
// the same once fetch opens the plan phase, the --local pipeline word
// before the id, and a capture, which is about another task.
func TestTaskRunsFromTheWriter(t *testing.T) {
	seed := func(skill, args string) func(r *state.Run) {
		return func(r *state.Run) { r.Seed(skill, args, "/work/acme") }
	}
	set := func(phase, status string) func(r *state.Run) {
		return func(r *state.Run) { _ = r.Set(phase, status, "") }
	}

	for _, tc := range []struct {
		name  string
		steps []func(r *state.Run)
		own   bool
	}{
		{"ship seeded", []func(*state.Run){seed("ship", "GH-1")}, true},
		{"ship in fetch", []func(*state.Run){seed("ship", "GH-1"), set("plan", "running")}, true},
		{"ship local", []func(*state.Run){seed("ship", "local: GH-1")}, true},
		{"plan then ship local", []func(*state.Run){
			seed("plan", "GH-1"),
			func(r *state.Run) { _ = r.SetTask("GH-1", "") },
			set("plan", "done"),
			seed("ship", "local: GH-1"),
		}, true},
		{"capture", []func(*state.Run){seed("capture", "add a retry to the client")}, false},
		{"ship a description", []func(*state.Run){seed("ship", "add a retry to the client")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := state.Fresh("s")
			for _, step := range tc.steps {
				step(r)
			}

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
	renderRun(&out, own[0], nil, true, time.Now(), 0)

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

// A bare status on a task's branch prints what `status <id>` prints: the
// task's own run with its gates, not every run in the checkout.
func TestBareStatusReadsTheBranchTask(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix-retry", "")
	git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Chdir(repo)

	for _, r := range []*state.Run{
		{Session: "own", Task: "DM-5", Root: repo, Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"},
		{Session: "other", Task: "DM-7", Root: repo, Capture: "done", Plan: "running", Ship: "pending"},
	} {
		if err := r.Save(dir, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	bare := runStatusJSON(t)
	byID := runStatusJSON(t, "DM-5")

	if len(bare) != 1 || bare[0].Task != "DM-5" || len(bare[0].Gates) == 0 {
		t.Errorf("bare status = %+v, want DM-5's run alone, with its gates", bare)
	}

	if len(bare) != len(byID) || len(bare) > 0 && len(bare[0].Gates) != len(byID[0].Gates) {
		t.Errorf("bare status = %+v, status DM-5 = %+v; want the same", bare, byID)
	}
}

// A guessed task with no branch here leaves the bare answer as it was:
// the session's own run.
func TestBareStatusKeepsTheSessionRunForATaskWithNoBranch(t *testing.T) {
	repo := tracedRepo(t, "main", "")
	git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")
	t.Chdir(repo)

	r := &state.Run{Session: "sess-1", Task: "DM-9", Root: repo, Capture: "done", Plan: "running", Ship: "pending"}
	if err := r.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := runStatusJSON(t)
	if len(got) != 1 || got[0].Session != "sess-1" {
		t.Errorf("bare status = %+v, want the session's run", got)
	}
}

func TestBareTaskID(t *testing.T) {
	for _, tc := range []struct {
		name, branch, session, want string
	}{
		{"session task wins", "DM-5/fix", "DM-9", "DM-9"},
		{"branch without a session task", "DM-5/fix", "", "DM-5"},
		{"nothing names one", "main", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := tracedRepo(t, tc.branch, "")

			dir := t.TempDir()
			t.Setenv("F10_STATE_DIR", dir)
			t.Setenv("F10_SESSION_ID", "sess-1")

			r := &state.Run{Session: "sess-1", Task: tc.session, Root: repo}
			if err := r.Save(dir, time.Now()); err != nil {
				t.Fatal(err)
			}

			tg, err := targetAt(t.Context(), repo, false, "status")
			if err != nil {
				t.Fatal(err)
			}

			if got := bareTaskID(t.Context(), tg, "sess-1"); got != tc.want {
				t.Errorf("bareTaskID = %q, want %q", got, tc.want)
			}
		})
	}
}

func runStatusJSON(t *testing.T, args ...string) []runJSON {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out

	if err := app.Run(t.Context(), append([]string{"f10", "status", "--json"}, args...)); err != nil {
		t.Fatalf("status %v: %v\n%s", args, err, out.String())
	}

	var runs []runJSON
	if err := json.Unmarshal(out.Bytes(), &runs); err != nil {
		t.Fatalf("status %v: not a run list: %v\n%s", args, err, out.String())
	}

	return runs
}
