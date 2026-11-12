package statusview

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func TestTaskRuns(t *testing.T) {
	ship := &state.Run{Session: "ship", Task: "gh-1", Capture: "prior", Plan: "done", Ship: "blocked", Ask: "1. Merge?"}
	fresh := &state.Run{Session: "fresh", Skill: "ship", Capture: "upcoming", Plan: "upcoming", Ship: "upcoming"}
	fetching := &state.Run{Session: "fetching", Skill: "ship", Capture: "skipped", Plan: "running", Ship: "upcoming"}
	capture := &state.Run{Session: "capture", Capture: "running", Plan: "upcoming", Ship: "upcoming"}
	other := &state.Run{Session: "other", Task: "GH-7", Capture: "done", Plan: "running", Ship: "upcoming"}
	// a later session in the worktree that only caught up: `state done` starts
	// its run fresh, with no skill, since the hook seeds phase skills alone
	catchup := &state.Run{Session: "catchup", Capture: "upcoming", Plan: "upcoming", Ship: "upcoming", Done: "catchup"}
	planning := &state.Run{Session: "planning", Skill: "plan", Capture: "upcoming", Plan: "running", Ship: "upcoming"}

	own, others := taskRuns([]*state.Run{catchup, capture, ship, other, fresh, fetching, planning}, "GH-1")

	if got := sessions(own); got != "ship fresh fetching planning" {
		t.Errorf("own = %q, want the matching task and the runs fetch has not named yet, in order", got)
	}

	if got := sessions(others); got != "catchup capture other" {
		t.Errorf("others = %q, want the catchup, the capture and the other task", got)
	}
}

// The issue's repro: GH-1 shipped (plan saved, PR open), then its agent
// captured another task. Status names what the agent is on and reports
// the durable facts instead of the capture run.
func TestStatusFallbackReportsPlanAndPR(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.GhView, testkit.PrOpen)

	root := t.TempDir()
	plan := filepath.Join(root, ".f10", "plans", "GH-1.md")

	if err := os.MkdirAll(filepath.Dir(plan), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(plan, []byte("# GH-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := hostops.NewHost("gh", root)
	in := taskStatusInput{ID: "GH-1", Branch: "GH-1", Root: root, Plan: plan, Host: &h}
	capture := &state.Run{Capture: "running", Plan: "upcoming", Ship: "upcoming"}

	ship := readShipment(t.Context(), in, nil, ledger.TaskRef{})
	ship.AskPR(t.Context(), in)

	var out bytes.Buffer
	taskFallback(in, []*state.Run{capture}, ship).Render(&out, 0, false)

	for _, want := range []string{
		"none for GH-1 in " + root,
		"- · capture running",
		plan,
		"pr:        ↗ https://gh/pr/9",
		"pr state:    #9 open",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// An open PR's row carries its head's checks, failed jobs named; a checks
// read that fails leaves the row without them.
func TestStatusPRRowCarriesTheChecks(t *testing.T) {
	for _, c := range []struct {
		name, checks, want string
	}{
		{"red", `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"h3ad",` +
			`"statusCheckRollup":{"contexts":{"nodes":[{"__typename":"CheckRun","name":"lint",` +
			`"status":"COMPLETED","conclusion":"FAILURE"}]}}}}]}}}}}`, "#9 open · checks red (lint)"},
		{"pending", `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"h3ad",` +
			`"statusCheckRollup":{"contexts":{"nodes":[{"__typename":"CheckRun","name":"test",` +
			`"status":"QUEUED"}]}}}}]}}}}}`, "#9 open · checks pending (test)"},
		{"unread", "FAIL", "#9 open\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			f.Script(testkit.GhView, testkit.PrOpen)

			if c.checks == "FAIL" {
				f.Fail(testkit.GhChecks+"9", shell.Result{Code: 1, Stderr: "rate limited"})
			} else {
				f.Script(testkit.GhChecks+"9", c.checks)
			}

			h := hostops.NewHost("gh", t.TempDir())
			in := taskStatusInput{ID: "GH-1", Branch: "GH-1", Host: &h}

			ship := readShipment(t.Context(), in, nil, ledger.TaskRef{})
			ship.AskPR(t.Context(), in)

			var out bytes.Buffer
			taskFallback(in, nil, ship).Render(&out, 0, false)

			if !strings.Contains(out.String(), "pr state:    "+c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, out.String())
			}
		})
	}
}

// No plan file, no PR and no other run: only the task and the run rows.
func TestStatusFallbackLeavesOutWhatDoesNotExist(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Fail(testkit.GhView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})

	root := t.TempDir()
	h := hostops.NewHost("gh", root)
	in := taskStatusInput{
		ID:     "GH-1",
		Branch: "GH-1",
		Root:   root,
		Plan:   filepath.Join(root, "GH-1.md"),
		Host:   &h,
	}

	ship := readShipment(t.Context(), in, nil, ledger.TaskRef{})
	ship.AskPR(t.Context(), in)

	fb := taskFallback(in, nil, ship)

	var out bytes.Buffer
	fb.Render(&out, 0, false)
	assertOneColumn(t, out.String())

	for _, absent := range []string{"agent", "plan", "pr"} {
		if strings.Contains(out.String(), absent) {
			t.Errorf("output has a %q row:\n%s", absent, out.String())
		}
	}

	if !strings.Contains(out.String(), "none for GH-1") {
		t.Errorf("output lacks the run row:\n%s", out.String())
	}

	out.Reset()

	if err := ui.JSON(&out, fb); err != nil {
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

// The runs the state writer leaves at each point, classified against the
// task: a ship seeded from a reference, through fetch, with the --local word
// before the id, and a capture, which is about another task.
func TestTaskRunsFromTheWriter(t *testing.T) {
	seed := func(skill, args string) func(r *state.Run) {
		return func(r *state.Run) { r.Seed(t.Context(), skill, args, "/work/acme") }
	}
	set := func(phase, status string) func(r *state.Run) {
		return func(r *state.Run) { _ = r.Set(phase, status, "") }
	}
	// what `f10 state done <step>` writes into a session's fresh run
	done := func(step string) func(r *state.Run) {
		return func(r *state.Run) { _ = r.SetDone(step, time.Now()) }
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
		{"catchup alone", []func(*state.Run){done("catchup")}, false},
		{"plan a bare number", []func(*state.Run){seed("plan", "1")}, true},
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
	RunRows(&out, own[0], RunView{ShowRoot: true, Agent: "opus:high", Now: time.Now()})

	if !strings.Contains(out.String(), "opus:high") {
		t.Errorf("output lacks the agent row:\n%s", out.String())
	}

	if !strings.Contains(out.String(), "1. Keep it?") {
		t.Errorf("output lacks the ask:\n%s", out.String())
	}
}

// The run block names the final entry by its label, and the base task a
// driven ship waits on.
func TestRunRowsFinalLabelAndWait(t *testing.T) {
	r := &state.Run{
		Task: "GH-1", Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "implement",
		Pipeline: []string{"implement", "pr", "review", "pr"}, Labels: []string{"", "", "", "pr-polish"},
		Final: "pr", Wait: "GH-0",
	}

	var out bytes.Buffer
	RunRows(&out, r, RunView{Headed: true, Now: time.Now()})

	got := out.String()

	if !strings.Contains(got, "pr-polish") {
		t.Errorf("the final row shows the bare step, not its label:\n%s", got)
	}

	if !strings.Contains(got, "wait") || !strings.Contains(got, "GH-0") {
		t.Errorf("output lacks the wait row:\n%s", got)
	}
}

func TestCurrentPhase(t *testing.T) {
	for _, tc := range []struct {
		run  state.Run
		want string
	}{
		{state.Run{Capture: "upcoming", Plan: "upcoming", Ship: "upcoming"}, "capture upcoming"},
		{state.Run{Capture: "running", Plan: "upcoming", Ship: "upcoming"}, "capture running"},
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

func TestBareTaskID(t *testing.T) {
	for _, tc := range []struct {
		name, branch, session, want string
	}{
		{"session task wins", "DM-5/fix", "DM-9", "DM-9"},
		{"branch without a session task", "DM-5/fix", "", "DM-5"},
		{"nothing names one", "main", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := testkit.TracedRepo(t, tc.branch, "")

			dir := t.TempDir()
			t.Setenv("F10_STATE_DIR", dir)
			t.Setenv("F10_SESSION_ID", "sess-1")

			r := &state.Run{Session: "sess-1", Task: tc.session, Root: repo}
			if err := r.Save(t.Context(), dir, time.Now()); err != nil {
				t.Fatal(err)
			}

			tg, err := target.At(t.Context(), repo, false, "status")
			if err != nil {
				t.Fatal(err)
			}

			if got := cmp.Or(
				sessionTaskID("sess-1"),
				checkoutTaskID(t.Context(), tg),
			); got != tc.want {
				t.Errorf("bare task = %q, want %q", got, tc.want)
			}
		})
	}
}

// A directory name stands in for a branch only in a linked worktree on a
// detached HEAD; a branch with no id in a directory named with one guesses none.
func TestBareTaskIDFromTheWorktreeName(t *testing.T) {
	repo := testkit.TracedRepo(t, "work", "")
	t.Setenv("F10_STATE_DIR", t.TempDir())

	named := filepath.Join(filepath.Dir(repo), "demo.dm-2")
	testkit.Git(t, repo, "worktree", "add", "-q", "-b", "side", named)

	detached := filepath.Join(filepath.Dir(repo), "demo.DM-4-fix")
	testkit.Git(t, repo, "worktree", "add", "-q", "--detach", detached)

	for dir, want := range map[string]string{repo: "", named: "", detached: "DM-4"} {
		tg, err := target.At(t.Context(), dir, false, "status")
		if err != nil {
			t.Fatal(err)
		}

		if got := checkoutTaskID(t.Context(), tg); got != want {
			t.Errorf("checkoutTaskID in %s = %q, want %q", filepath.Base(dir), got, want)
		}
	}
}

// The url and PR come from the ledger, the plan and handoff only when their
// files exist, and no commit row without the main checkout's refs to read.
func TestReadShipmentFromTheLedger(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "GH-1.md")

	if err := os.WriteFile(plan, []byte("# GH-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := taskStatusInput{ID: "GH-1", Branch: "GH-1", Plan: plan, Handoff: filepath.Join(dir, "none.md")}
	events := []ledger.Event{{
		Kind:    ledger.PROpened,
		Task:    "GH-1",
		Payload: ledger.NewPayload(ledger.Payload{"url": "https://gh/pr/9"}),
	}}

	known := ledger.TaskRef{ID: "GH-1", Title: "Add a retry", URL: "https://gh/issues/1"}
	s := readShipment(t.Context(), in, events, known)

	want := Shipment{
		Task: "GH-1", Title: "Add a retry", URL: "https://gh/issues/1", Branch: "GH-1",
		PR: "https://gh/pr/9", Plan: plan,
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("shipment = %+v, want %+v", s, want)
	}
}

// A PR opened by a script around gh leaves only pushes in the ledger: the
// PR row comes from the host by branch. With no push the host is not asked.
func TestReadShipmentPRByBranch(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.GhView, testkit.PrOpen)

	h := hostops.NewHost("gh", t.TempDir())
	in := taskStatusInput{ID: "GH-1", Branch: "GH-1", Host: &h}
	pushed := []ledger.Event{{Kind: ledger.Pushed, Task: "GH-1", Payload: ledger.NewPayload(ledger.Payload{})}}

	if s := readShipment(t.Context(), in, nil, ledger.TaskRef{}); s.PR != "" {
		t.Errorf("an unpushed task has pr %q, want none", s.PR)
	}

	if f.Called(testkit.GhView) {
		t.Error("status asked the host for a task that never pushed")
	}

	s := readShipment(t.Context(), in, pushed, ledger.TaskRef{})
	if s.PR != "https://gh/pr/9" {
		t.Errorf("pr = %q, want the branch's PR", s.PR)
	}

	// the fallback reuses that answer rather than asking the host again
	f.Script(testkit.GhView)
	s.AskPR(t.Context(), in)

	if fb := taskFallback(in, nil, s); fb.PR != "https://gh/pr/9" || fb.PRState != "open" {
		t.Errorf("fallback pr = %q (%s), want the shipment's PR and its state", fb.PR, fb.PRState)
	}
}

// --json carries the same files: on each run under a shipment, and on the
// fallback.
func TestStatusJSONCarriesTheFiles(t *testing.T) {
	head := Shipment{
		Task:     "GH-1",
		Verdicts: []verdictPath{{Subject: "plan", Path: "/s/verdicts/GH-1/1.md"}},
		Reviews:  []reviewPath{{Round: "1", Path: "/s/reviews/GH-1/1.md"}},
	}

	var out bytes.Buffer

	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "done", Ship: "done"}
	if err := writeRuns(
		StatusView{Out: &out, JSON: true},
		[]*state.Run{r},
		[][]gate{nil},
		&head,
		false,
		"",
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	var runs []RunJSON
	if err := json.Unmarshal(out.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}

	if len(runs) != 1 || !reflect.DeepEqual(runs[0].Verdicts, head.Verdicts) ||
		!reflect.DeepEqual(runs[0].Reviews, head.Reviews) || runs[0].Shipment == nil {
		t.Errorf("run JSON lacks the files:\n%s", out.String())
	}

	out.Reset()

	fb := taskFallback(taskStatusInput{ID: "GH-1"}, nil, head)
	if err := ui.JSON(&out, fb); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "/s/verdicts/GH-1/1.md") || !strings.Contains(got, "/s/reviews/GH-1/1.md") {
		t.Errorf("fallback JSON lacks the files:\n%s", got)
	}
}

// A headed run prints the block once and drops its own task and url rows.
func TestWriteRunsUnderTheShipment(t *testing.T) {
	r := &state.Run{Task: "GH-1", URL: "https://gh/issues/1", Capture: "prior", Plan: "done", Ship: "done"}
	head := Shipment{Task: "GH-1", URL: "https://gh/issues/1"}

	var out bytes.Buffer

	if err := writeRuns(
		StatusView{Out: &out},
		[]*state.Run{r},
		[][]gate{nil},
		&head,
		false,
		"",
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "task:       GH-1\nurl:      ↗ https://gh/issues/1\ncapture:    prior\n") {
		t.Errorf("the run rows do not go on under the block:\n%s", got)
	}

	if strings.Count(got, "GH-1") != 1 {
		t.Errorf("the task prints more than once:\n%s", got)
	}

	assertOneColumn(t, got)
}

// A gate row's label is indented and its text may hold a colon of its own;
// the label still ends at the first colon, its value in the head's column.
func TestWriteRunsGatesShareTheColumn(t *testing.T) {
	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "running", Ship: "upcoming"}
	head := Shipment{Task: "GH-1", Branch: "GH-1/some-branch"}
	gates := []gate{{Step: "judge", Judged: "plan", State: "upcoming"}}

	var out bytes.Buffer

	if err := writeRuns(
		StatusView{Out: &out},
		[]*state.Run{r},
		[][]gate{gates},
		&head,
		true,
		"",
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "\n  judge:    plan: upcoming\n") {
		t.Errorf("the gate row is not `  judge: plan: upcoming` in the column:\n%s", got)
	}

	assertOneColumn(t, got)
}

// A planned task's file row and its plan phase row never share a key.
func TestWriteRunsNamesThePlanFileApart(t *testing.T) {
	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "done", Ship: "running"}
	head := Shipment{Task: "GH-1", Plan: "/work/.f10/plans/GH-1.md"}

	var out bytes.Buffer

	if err := writeRuns(
		StatusView{Out: &out},
		[]*state.Run{r},
		[][]gate{nil},
		&head,
		false,
		"",
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "plan file:  ") || strings.Count(got, "\nplan:") != 1 {
		t.Errorf("want one `plan file:` row and one `plan:` row:\n%s", got)
	}

	assertOneColumn(t, got)
}

// assertOneColumn fails unless every line is `label: value` with its value,
// past the marker cell, at one column.
func assertOneColumn(t *testing.T, out string) {
	t.Helper()

	col := -1

	for line := range strings.Lines(strings.TrimRight(out, "\n")) {
		label, rest, ok := strings.Cut(strings.TrimSuffix(line, "\n"), ":")
		if !ok || strings.TrimSpace(label) == "" {
			t.Errorf("line is not `label: value`: %q\n%s", line, out)

			continue
		}

		lead := len([]rune(rest)) - len([]rune(strings.TrimLeft(rest, " ↗")))
		at := len([]rune(label)) + 1 + lead

		if col == -1 {
			col = at
		} else if at != col {
			t.Errorf("value of %q starts at column %d, want %d:\n%s", strings.TrimSpace(label), at, col, out)
		}
	}
}

// The commit row waits for a commit of the branch's own: a fresh branch's
// HEAD is its base's, and a sha there would name nothing the task made.
func TestReadShipmentCommitRow(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	r.At(testkit.Before, "checkout", "-q", "-b", "FT-2/b", "origin/main")

	in := taskStatusInput{ID: "FT-2", Branch: "FT-2/b", Main: r.Dir}

	if s := readShipment(t.Context(), in, nil, ledger.TaskRef{}); s.Commit != "" {
		t.Errorf("a fresh branch has commit %q, want none", s.Commit)
	}

	r.Commit(testkit.During, "c.go", "package main // c\n", "c")

	want := r.At(testkit.During, "rev-parse", "--short", "HEAD")
	if s := readShipment(t.Context(), in, nil, ledger.TaskRef{}); s.Commit != want {
		t.Errorf("commit = %q, want the branch head %q", s.Commit, want)
	}
}

// Under a caller's deadline the host reads stop short of it, so the caller's
// own context is still live when they give up.
func TestAskPRLeavesTheCallersDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), hostMargin/2)
	defer cancel()

	inner, stop := withinDeadline(ctx)
	defer stop()

	if inner.Err() == nil {
		t.Error("the host reads got time past the margin")
	}

	if ctx.Err() != nil {
		t.Error("the caller's context ran out with the host reads")
	}
}
