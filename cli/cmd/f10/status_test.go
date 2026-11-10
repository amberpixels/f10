package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
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

	own, others := statusview.TaskRuns([]*state.Run{catchup, capture, ship, other, fresh, fetching, planning}, "GH-1")

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

	h := hostops.NewHost("gh", root)
	in := statusview.TaskStatusInput{ID: "GH-1", Branch: "GH-1", Root: root, Plan: plan, Host: &h}
	capture := &state.Run{Capture: "running", Plan: "upcoming", Ship: "upcoming"}

	ship := statusview.ReadShipment(t.Context(), in, nil, ledger.TaskRef{})
	ship.AskPR(t.Context(), in)

	var out bytes.Buffer
	statusview.TaskFallback(in, []*state.Run{capture}, ship).Render(&out, 0, false)

	for _, want := range []string{
		"none for GH-1 in " + root,
		"- · capture running",
		plan,
		"pr:        ↗ https://gh/pr/9",
		"pr state:    open",
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
	h := hostops.NewHost("gh", root)
	in := statusview.TaskStatusInput{
		ID:     "GH-1",
		Branch: "GH-1",
		Root:   root,
		Plan:   filepath.Join(root, "GH-1.md"),
		Host:   &h,
	}

	ship := statusview.ReadShipment(t.Context(), in, nil, ledger.TaskRef{})
	ship.AskPR(t.Context(), in)

	fb := statusview.TaskFallback(in, nil, ship)

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

			own, _ := statusview.TaskRuns([]*state.Run{r}, "GH-1")

			if got := len(own) == 1; got != tc.own {
				t.Errorf("own = %v, want %v for %+v", got, tc.own, *r)
			}
		})
	}
}

// A driven agent's run is its own task's, so its ask still renders.
func TestRenderRunKeepsTheAsk(t *testing.T) {
	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Keep it?"}

	own, _ := statusview.TaskRuns([]*state.Run{r}, "GH-1")
	if len(own) != 1 {
		t.Fatal("the driven run is not the task's own")
	}

	var out bytes.Buffer
	statusview.RunRows(&out, own[0], statusview.RunView{ShowRoot: true, Agent: "opus:high", Now: time.Now()})

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
	statusview.RunRows(&out, r, statusview.RunView{Headed: true, Now: time.Now()})

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
		if got := statusview.CurrentPhase(&tc.run); got != tc.want {
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
		{Session: "other", Task: "DM-7", Root: repo, Capture: "done", Plan: "running", Ship: "upcoming"},
	} {
		if err := r.Save(t.Context(), dir, time.Now()); err != nil {
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

	r := &state.Run{Session: "sess-1", Task: "DM-9", Root: repo, Capture: "done", Plan: "running", Ship: "upcoming"}
	if err := r.Save(t.Context(), dir, time.Now()); err != nil {
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
			if err := r.Save(t.Context(), dir, time.Now()); err != nil {
				t.Fatal(err)
			}

			tg, err := target.At(t.Context(), repo, false, "status")
			if err != nil {
				t.Fatal(err)
			}

			if got := cmp.Or(
				statusview.SessionTaskID("sess-1"),
				statusview.CheckoutTaskID(t.Context(), tg),
			); got != tc.want {
				t.Errorf("bare task = %q, want %q", got, tc.want)
			}
		})
	}
}

func runStatusJSON(t *testing.T, args ...string) []statusview.RunJSON {
	t.Helper()

	r := runApp(t, append([]string{"status", "--json"}, args...)...)
	if r.err != nil {
		t.Fatalf("status %v: %v\n%s", args, r.err, r.combined)
	}

	var runs []statusview.RunJSON
	if err := json.Unmarshal([]byte(r.combined), &runs); err != nil {
		t.Fatalf("status %v: not a run list: %v\n%s", args, err, r.combined)
	}

	return runs
}

// A directory name stands in for a branch only in a linked worktree on a
// detached HEAD; a branch with no id in a directory named with one guesses none.
func TestBareTaskIDFromTheWorktreeName(t *testing.T) {
	repo := tracedRepo(t, "work", "")
	t.Setenv("F10_STATE_DIR", t.TempDir())

	named := filepath.Join(filepath.Dir(repo), "demo.dm-2")
	git(t, repo, "worktree", "add", "-q", "-b", "side", named)

	detached := filepath.Join(filepath.Dir(repo), "demo.DM-4-fix")
	git(t, repo, "worktree", "add", "-q", "--detach", detached)

	for dir, want := range map[string]string{repo: "", named: "", detached: "DM-4"} {
		tg, err := target.At(t.Context(), dir, false, "status")
		if err != nil {
			t.Fatal(err)
		}

		if got := statusview.CheckoutTaskID(t.Context(), tg); got != want {
			t.Errorf("checkoutTaskID in %s = %q, want %q", filepath.Base(dir), got, want)
		}
	}
}

// The ship report's rows in its order, each only when it has a value, the
// marker on the two urls alone.
func TestShipmentFacts(t *testing.T) {
	s := statusview.Shipment{
		Task:   "GH-1",
		Title:  "Add a retry",
		URL:    "https://gh/issues/1",
		Branch: "GH-1/add-a-retry",
		PR:     "https://gh/pr/9",
		Plan:   "/store/plans/GH-1.md",
	}

	var out bytes.Buffer
	if err := render.WriteReport(&out, s.Facts(false), nil); err != nil {
		t.Fatal(err)
	}

	want := "task:      GH-1\n" +
		"title:     Add a retry\n" +
		"url:     ↗ https://gh/issues/1\n" +
		"branch:    GH-1/add-a-retry\n" +
		"pr:      ↗ https://gh/pr/9\n" +
		"plan:      /store/plans/GH-1.md\n"
	if out.String() != want {
		t.Errorf("block =\n%s\nwant\n%s", out.String(), want)
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

	in := statusview.TaskStatusInput{ID: "GH-1", Branch: "GH-1", Plan: plan, Handoff: filepath.Join(dir, "none.md")}
	events := []ledger.Event{{
		Kind:    ledger.PROpened,
		Task:    "GH-1",
		Payload: ledger.NewPayload(ledger.Payload{"url": "https://gh/pr/9"}),
	}}

	known := ledger.TaskRef{ID: "GH-1", Title: "Add a retry", URL: "https://gh/issues/1"}
	s := statusview.ReadShipment(t.Context(), in, events, known)

	want := statusview.Shipment{
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
	f := newFakes(t)
	f.script(ghView, prOpen)

	h := hostops.NewHost("gh", t.TempDir())
	in := statusview.TaskStatusInput{ID: "GH-1", Branch: "GH-1", Host: &h}
	pushed := []ledger.Event{{Kind: ledger.Pushed, Task: "GH-1", Payload: ledger.NewPayload(ledger.Payload{})}}

	if s := statusview.ReadShipment(t.Context(), in, nil, ledger.TaskRef{}); s.PR != "" {
		t.Errorf("an unpushed task has pr %q, want none", s.PR)
	}

	if f.called(ghView) {
		t.Error("status asked the host for a task that never pushed")
	}

	s := statusview.ReadShipment(t.Context(), in, pushed, ledger.TaskRef{})
	if s.PR != "https://gh/pr/9" {
		t.Errorf("pr = %q, want the branch's PR", s.PR)
	}

	// the fallback reuses that answer rather than asking the host again
	f.script(ghView)
	s.AskPR(t.Context(), in)

	if fb := statusview.TaskFallback(in, nil, s); fb.PR != "https://gh/pr/9" || fb.PRState != "open" {
		t.Errorf("fallback pr = %q (%s), want the shipment's PR and its state", fb.PR, fb.PRState)
	}
}

// Every verdict of the open series and every findings file gets a row; an
// earlier PR's verdict stays out, and a subject judged twice takes numbers.
func TestShipFiles(t *testing.T) {
	storage := t.TempDir()
	verdicts := filepath.Join(storage, "verdicts", "FT-1")

	writeFile(t, filepath.Join(verdicts, "1.md"), "task: FT-1\nsubject: task FT-1\nverdict: proceed\nreason: old\n")

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(verdicts, "1.md"), old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := closeSeries(filepath.Join(storage, "reviews", "FT-1"), archive.SeriesEnd{Pr: "#7"}); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(verdicts, "2.md"), "task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: a\n")
	writeFile(t, filepath.Join(verdicts, "3.md"), "task: FT-1\nsubject: branch x\nverdict: proceed\nreason: b\n")
	writeFile(t, filepath.Join(verdicts, "4.md"), "task: FT-1\nsubject: pr 9\nverdict: proceed\nreason: c\n")
	writeFile(t, filepath.Join(storage, "reviews", "FT-1", "1.md"), "# Review\n")
	writeFile(t, filepath.Join(storage, "reviews", "FT-1", "2.md"), "# Review\n")

	s := statusview.Shipment{Task: "FT-1"}
	s.Verdicts, s.Reviews = statusview.ShipFiles(storage, "FT-1")

	var out bytes.Buffer
	if err := render.WriteReport(&out, s.Facts(false), nil); err != nil {
		t.Fatal(err)
	}

	want := "task:              FT-1\n" +
		"judge plan:        " + filepath.Join(verdicts, "2.md") + "\n" +
		"judge change 3:    " + filepath.Join(verdicts, "3.md") + "\n" +
		"judge change 4:    " + filepath.Join(verdicts, "4.md") + "\n" +
		"review 1:          " + filepath.Join(storage, "reviews", "FT-1", "1.md") + "\n" +
		"review 2:          " + filepath.Join(storage, "reviews", "FT-1", "2.md") + "\n"
	if out.String() != want {
		t.Errorf("block =\n%s\nwant\n%s", out.String(), want)
	}
}

// --json carries the same files: on each run under a shipment, and on the
// fallback.
func TestStatusJSONCarriesTheFiles(t *testing.T) {
	head := statusview.Shipment{
		Task:     "GH-1",
		Verdicts: []statusview.VerdictPath{{Subject: "plan", Path: "/s/verdicts/GH-1/1.md"}},
		Reviews:  []statusview.ReviewPath{{Round: "1", Path: "/s/reviews/GH-1/1.md"}},
	}

	var out bytes.Buffer

	r := &state.Run{Task: "GH-1", Capture: "prior", Plan: "done", Ship: "done"}
	if err := statusview.WriteRuns(
		statusview.StatusView{Out: &out, JSON: true},
		[]*state.Run{r},
		[][]statusview.Gate{nil},
		&head,
		false,
		"",
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	var runs []statusview.RunJSON
	if err := json.Unmarshal(out.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}

	if len(runs) != 1 || !reflect.DeepEqual(runs[0].Verdicts, head.Verdicts) ||
		!reflect.DeepEqual(runs[0].Reviews, head.Reviews) || runs[0].Shipment == nil {
		t.Errorf("run JSON lacks the files:\n%s", out.String())
	}

	out.Reset()

	fb := statusview.TaskFallback(statusview.TaskStatusInput{ID: "GH-1"}, nil, head)
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
	head := statusview.Shipment{Task: "GH-1", URL: "https://gh/issues/1"}

	var out bytes.Buffer

	if err := statusview.WriteRuns(
		statusview.StatusView{Out: &out},
		[]*state.Run{r},
		[][]statusview.Gate{nil},
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
	head := statusview.Shipment{Task: "GH-1", Branch: "GH-1/some-branch"}
	gates := []statusview.Gate{{Step: "judge", Judged: "plan", State: "upcoming"}}

	var out bytes.Buffer

	if err := statusview.WriteRuns(
		statusview.StatusView{Out: &out},
		[]*state.Run{r},
		[][]statusview.Gate{gates},
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
	head := statusview.Shipment{Task: "GH-1", Plan: "/work/.f10/plans/GH-1.md"}

	var out bytes.Buffer

	if err := statusview.WriteRuns(
		statusview.StatusView{Out: &out},
		[]*state.Run{r},
		[][]statusview.Gate{nil},
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

// A long run value wraps under the value column, never under the labels.
func TestWriteFactsWrapsUnderTheValueColumn(t *testing.T) {
	rows := []render.Fact{
		{Label: "url", Value: "https://gh/issues/1", URL: true},
		statusview.RunFact(
			"note",
			"stop: the change patches the symptom and the cause is the retry loop",
			lipgloss.NewStyle(),
		),
	}

	var out bytes.Buffer
	if err := render.WriteFacts(&out, rows, render.LabelWidth(rows), 40); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("the note did not wrap at 40 columns:\n%s", out.String())
	}

	col := strings.Index(lines[1], "stop:")
	for _, l := range lines[2:] {
		if len(l)-len(strings.TrimLeft(l, " ")) != col {
			t.Errorf("continuation %q does not start at column %d:\n%s", l, col, out.String())
		}

		if len(l) > 40 {
			t.Errorf("continuation %q runs past 40 columns", l)
		}
	}
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
	r := newCatchupRepo(t)

	r.at(before, "checkout", "-q", "-b", "FT-2/b", "origin/main")

	in := statusview.TaskStatusInput{ID: "FT-2", Branch: "FT-2/b", Main: r.dir}

	if s := statusview.ReadShipment(t.Context(), in, nil, ledger.TaskRef{}); s.Commit != "" {
		t.Errorf("a fresh branch has commit %q, want none", s.Commit)
	}

	r.commit(during, "c.go", "package main // c\n", "c")

	want := r.at(during, "rev-parse", "--short", "HEAD")
	if s := statusview.ReadShipment(t.Context(), in, nil, ledger.TaskRef{}); s.Commit != want {
		t.Errorf("commit = %q, want the branch head %q", s.Commit, want)
	}
}
