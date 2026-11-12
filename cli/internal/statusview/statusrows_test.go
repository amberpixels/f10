package statusview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func TestStageOf(t *testing.T) {
	run := func(capture, plan, ship, leaf string) *state.Run {
		return &state.Run{Capture: capture, Plan: plan, Ship: ship, Leaf: leaf}
	}
	asking := run("prior", "done", "blocked", "judge")
	asking.Ask = "1. Merge? [yes | no]"

	type facts = lookaround.TaskFacts

	merged := &lookaround.PR{Status: forge.Merged}

	for _, tc := range []struct {
		name        string
		f           facts
		baseDone    bool
		stage, step string
	}{
		{"finished", facts{Finished: true}, false, StageDone, "finish"},
		{"merged", facts{PR: merged, Run: run("prior", "done", "done", "")}, false, StageDone, "finish"},
		{"ask", facts{Run: asking}, false, stageAsking, "judge"},
		{
			"drive halt",
			facts{Halt: &lookaround.Halt{Skill: "ship"}, Run: run("prior", "done", "running", "implement")},
			false, stageHalted, "ship",
		},
		{"run stopped", facts{Run: run("prior", "done", "failed", "pr")}, false, stageHalted, "pr"},
		{"implementing", facts{Run: run("prior", "done", "running", "cleanup")}, false, stageImplementing, "cleanup"},
		{"reviewing", facts{Run: run("prior", "done", "running", "review")}, false, stageReviewing, "review"},
		{"resolving", facts{Run: run("prior", "done", "running", "resolve")}, false, stageResolving, "resolve"},
		{"judging", facts{Run: run("prior", "done", "running", "judge")}, false, stageJudging, "judge"},
		{
			"shipped",
			facts{Run: &state.Run{Capture: "prior", Plan: "done", Ship: "done", Final: "pr"}},
			false, stageShipped, "pr",
		},
		{"planning", facts{Run: run("skipped", "running", "upcoming", "")}, false, stagePlanning, "plan"},
		{"planned", facts{Run: run("skipped", "done", "upcoming", "")}, false, stagePlanned, "plan"},
		{"after, no drive", facts{Base: "FT-1"}, false, stageNotStarted, "after FT-1"},
		{"waiting", facts{Base: "FT-1", Driven: true}, false, stageWaiting, "after FT-1"},
		{
			"parked",
			facts{Run: &state.Run{Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "implement", Wait: "FT-1"}},
			false, stageWaiting, "on FT-1",
		},
		{"base done", facts{Base: "FT-1"}, true, stageNotStarted, ""},
		{"queued", facts{Queued: true, Base: "FT-1"}, false, stageQueued, ""},
		{
			"parked by its drive",
			facts{Run: run("skipped", "done", "upcoming", ""), Base: "FT-1", DrivePhase: drivestore.PhaseParked},
			false, stageWaiting, "on FT-1",
		},
		{
			"finish waits for its base",
			facts{
				Run:  &state.Run{Capture: "prior", Plan: "done", Ship: "done", Final: "pr"},
				Base: "FT-1", DrivePhase: drivestore.PhaseWaiting,
			},
			false, stageWaiting, "to finish after FT-1",
		},
		{"nothing", facts{}, false, stageNotStarted, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if stage, step := stageOf(tc.f, tc.baseDone); stage != tc.stage || step != tc.step {
				t.Errorf("stageOf = %q %q, want %q %q", stage, step, tc.stage, tc.step)
			}
		})
	}
}

// A running drive holds every member of its bundles; a stopped, cancelled
// or done drive holds none.
func TestDrivenTasks(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	for _, rec := range []*drivestore.Record{
		{Tasks: []drivestore.Task{{ID: "FT-1"}}},
		{Cancelled: &now, Tasks: []drivestore.Task{{ID: "FT-5"}}},
		{Tasks: []drivestore.Task{{ID: "FT-6", Outcome: &drivestore.Outcome{Row: "ok"}}}},
		{Tasks: []drivestore.Task{{ID: "ft-2_v2+FT-3"}}},
	} {
		if err := drivestore.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	for _, n := range []int{2, 3, 4} {
		lease, err := drivestore.Acquire(root, drivestore.DriveKey(n), n)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(lease.Release)
	}

	got := drivenTasks(drivestore.Active(root))
	want := map[string]bool{"FT-2": true, "FT-3": true}

	if len(got) != len(want) || !got["FT-2"] || !got["FT-3"] {
		t.Errorf("driven = %v, want %v", got, want)
	}
}

func event(at time.Time, task, kind string, p ledger.Payload) ledger.Event {
	return ledger.Event{At: at, Task: task, Kind: kind, Payload: ledger.NewPayload(p)}
}

// What the ledger keeps once a child's worktree is gone: the last claim,
// the PR, a finish run from the main checkout on another task's session,
// and a halt drive saw after the run's last word.
func TestRowsFromTheLedger(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	src := rowSource{Events: []ledger.Event{
		event(
			at(1),
			"FT-1",
			ledger.StateSet,
			ledger.Payload{"capture": "prior", "plan": "done", "ship": "running", "leaf": "pr"},
		),
		event(at(2), "FT-1", ledger.PROpened, ledger.Payload{"url": "https://github.com/a/b/pull/128"}),
		event(at(3), "FT-9", ledger.TaskFinished, ledger.Payload{"args": []any{"FT-1", "--yes"}}),
		// finish typed with the bare number, resolved to its id
		event(at(3), "", ledger.TaskFinished, ledger.Payload{"args": []any{"--yes", "8"}}),
		// a finish that named no task finishes none
		event(at(3), "", ledger.TaskFinished, ledger.Payload{"args": []any{"--yes"}}),

		event(
			at(1),
			"FT-2",
			ledger.StateSet,
			ledger.Payload{"capture": "prior", "plan": "done", "ship": "running", "leaf": "implement"},
		),
		event(at(4), "FT-2", ledger.TaskHalted, ledger.Payload{"skill": "ship", "reason": "agent idle"}),

		// a halt the run then moved past is no halt
		event(at(1), "FT-3", ledger.TaskHalted, ledger.Payload{"skill": "ship", "reason": "old"}),
		event(
			at(2),
			"FT-3",
			ledger.StateSet,
			ledger.Payload{"capture": "prior", "plan": "done", "ship": "running", "leaf": "review"},
		),

		event(at(1), "FT-4", ledger.StateAsk, ledger.Payload{
			"capture": "prior", "plan": "done", "ship": "blocked", "leaf": "judge", "ask": "1. Proceed? [yes | no]",
		}),
		event(at(1), "FT-5", ledger.StateSet, ledger.Payload{
			"capture": "prior",
			"plan":    "done",
			"ship":    "failed",
			"leaf":    "pr",
			"note":    "push rejected",
			"next":    "rebase",
		}),
	}}
	src.Driven = map[string]bool{"FT-6": true}
	src.Resolve = func(tok string) string {
		if tok == "" {
			t.Error("resolved an empty finish argument")
		}

		return "FT-" + strings.TrimPrefix(tok, "FT-")
	}

	rows := src.RowsFrom(t.Context(), []stack.ChildRow{
		{ID: "FT-1", Title: "trailers"},
		{ID: "FT-2", Title: "state block"},
		{ID: "FT-3", Title: "look-around"},
		{ID: "FT-4", Title: "priority"},
		{ID: "FT-5", Title: "board"},
		{ID: "FT-6", Title: "after board", After: "FT-5", Depth: 1},
		{ID: "FT-7", Title: "after trailers", After: "FT-1", Depth: 1},
		{ID: "FT-8", Title: "finished by number"},
		{ID: "FT-10", Title: "after board, no drive", After: "FT-5", Depth: 1},
	})

	got := map[string]TaskRow{}
	for _, r := range rows {
		got[r.ID] = r
	}

	check := func(id, stage, step string) {
		t.Helper()

		if r := got[id]; r.Stage != stage || r.Step != step {
			t.Errorf("%s = %q %q, want %q %q", id, r.Stage, r.Step, stage, step)
		}
	}

	check("FT-1", StageDone, "finish")
	check("FT-2", stageHalted, "ship")
	check("FT-3", stageReviewing, "review")
	check("FT-4", stageAsking, "judge")
	check("FT-5", stageHalted, "pr")
	check("FT-6", stageWaiting, "after FT-5")
	check("FT-7", stageNotStarted, "")
	check("FT-8", StageDone, "finish")
	check("FT-10", stageNotStarted, "after FT-5")

	if r := got["FT-1"]; r.PR != "https://github.com/a/b/pull/128" {
		t.Errorf("FT-1 PR = %q, want the ledger's url", r.PR)
	}

	if r := got["FT-2"]; r.Note != "agent idle" {
		t.Errorf("FT-2 note = %q, want drive's reason", r.Note)
	}

	if r := got["FT-5"]; r.Note != "push rejected" || r.Next != "rebase" {
		t.Errorf("FT-5 = %+v, want the run's note and next", r)
	}

	if r := got["FT-4"]; r.Ask != "1. Proceed? [yes | no]" {
		t.Errorf("FT-4 ask = %q", r.Ask)
	}
}

// statusGolden compares out with testdata/status/<name>.golden, rewriting
// it under -update.
func statusGolden(t *testing.T, name, out string) {
	t.Helper()

	path := filepath.Join("testdata", "status", name+".golden")
	if *testkit.UpdateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if out != string(want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, out, want)
	}
}

// A parent's children as a tree: nested After: chains, a halted child with
// its halt and next, an asking child, and an unrelated task; every line a
// task row carries an id, and none of it is a markdown table.
func TestRenderRows(t *testing.T) {
	gh := func(kind string, n int) string { return "https://github.com/a/b/" + kind + "/" + strconv.Itoa(n) }
	rows := []TaskRow{
		{
			ID: "FT-206", URL: gh("issues", 206), Title: "Show who runs a driven finish", Stage: StageDone,
			Step: "finish", PR: gh("pull", 208), PRState: "merged",
		},
		{
			ID: "FT-210", URL: gh("issues", 210), Title: "Keep a drive as a record", Stage: StageDone,
			Step: "finish", PR: gh("pull", 216), PRState: "merged",
		},
		{
			ID:      "FT-212",
			URL:     gh("issues", 212),
			Title:   "Run drives in a per-user daemon",
			Depth:   1,
			After:   "FT-210",
			Stage:   stageReviewing,
			Step:    "review",
			PR:      gh("pull", 219),
			PRState: forge.Open,
			Gates: []gate{
				{
					Step:    "judge",
					Judged:  "plan",
					State:   gateDone,
					Outcome: &archive.JudgeOutcome{Verdict: "proceed"},
				},
			},
		},
		{
			ID: "FT-213", URL: gh("issues", 213), Title: "Keep asks in an inbox the user is told about",
			Depth: 2, After: "FT-212", Stage: stageHalted, Step: "implement",
			Note: "catchup guard: merging FT-212 conflicts in drives/record.go",
			Next: "/f10:catchup, then /f10:ship FT-213",
		},
		{
			ID: "FT-214", URL: gh("issues", 214), Title: "Let the daemon watch tasks no drive owns", Depth: 2,
			After: "FT-212", Stage: stageAsking, Step: "judge", Ask: "1. Proceed? [yes | no]",
		},
		{ID: "FT-230", URL: gh("issues", 230), Title: "An unrelated task", Stage: stageNotStarted},
	}

	var out bytes.Buffer
	if err := RenderRows(
		&out,
		rows,
		RowView{Parent: "FT-209", Plain: true, Width: 100},
	); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "|") && strings.Contains(got, "|---") {
		t.Errorf("plain output carries escapes or a table:\n%s", got)
	}

	statusGolden(t, "parent", got)

	// on a terminal the id and the PR are links that take no columns
	out.Reset()

	if err := RenderRows(&out, rows[:1], RowView{Term: true, Width: 80}); err != nil {
		t.Fatal(err)
	}

	line, _, _ := strings.Cut(out.String(), "\n")
	if !strings.Contains(line, render.Hyperlink(rows[0].URL, "FT-206")) ||
		!strings.Contains(line, render.Hyperlink(rows[0].PR, "\U000F062D #208 merged")) {
		t.Errorf("a terminal line = %q, want the id and the PR linked", line)
	}

	out.Reset()

	if err := ui.JSON(&out, rows[:1]); err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded[0]["stage"] != "done" || decoded[0]["pr_state"] != "merged" || decoded[0]["step"] != "finish" {
		t.Errorf("--json row = %v", decoded[0])
	}
}

// A finished task prints the facts block, its PR's state and its gates as
// step rows, never a tree.
func TestRenderFinishedBlock(t *testing.T) {
	ship := Shipment{
		Task:    "FT-210",
		Title:   "Keep a drive as a record",
		URL:     "https://github.com/a/b/issues/210",
		PR:      "https://github.com/a/b/pull/216",
		PRState: "merged",
		Plan:    "/code/f10/.f10/plans/archive/FT-210.md",
		Demo:    "/code/f10/.f10/demo/FT-210/report.html",
		Verdicts: []verdictPath{
			{Subject: "plan", Path: "/code/f10/.f10/verdicts/FT-210/1.md"},
			{Subject: "change", Path: "/code/f10/.f10/verdicts/FT-210/2.md"},
		},
		Reviews: []reviewPath{
			{Round: "1", Path: "/code/f10/.f10/reviews/archive/FT-210.1/1.md"},
			{Round: "2", Path: "/code/f10/.f10/reviews/archive/FT-210.1/2.md"},
		},
	}
	gates := []gate{
		{Step: "judge", Judged: "plan", State: gateDone, Outcome: &archive.JudgeOutcome{Verdict: "proceed"}},
		{Step: "implement", State: gateDone},
		{Step: "cleanup", State: gateDone},
		{Step: "pr", State: gateDone},
	}

	rows := finishedFacts(ship, gates, false)

	var out bytes.Buffer
	if err := render.WriteFacts(&out, rows, render.LabelWidth(rows), 100); err != nil {
		t.Fatal(err)
	}

	statusGolden(t, "finished", out.String())
}

// A finished task's files are the series its last PR closed: the rounds
// f10 finish archived, and the verdicts written since the series before it.
func TestFinishedShipFiles(t *testing.T) {
	root := storage.Root(t.TempDir())
	write := func(path string, at time.Time) {
		t.Helper()

		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(
			path,
			[]byte("task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: ok\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Unix(1_800_000_000, 0)
	reviews := root.Reviews("FT-1")

	// the first PR's series, closed at t0+1h, and the second's, closed at t0+3h
	write(filepath.Join(reviews, "1.md"), t0)
	write(root.Verdicts("FT-1")+"/1.md", t0)

	if _, err := archive.ArchiveReviews(reviews, archive.SeriesEnd{PR: 1, At: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	write(filepath.Join(reviews, "1.md"), t0.Add(2*time.Hour))
	write(filepath.Join(reviews, "2.md"), t0.Add(2*time.Hour))
	write(root.Verdicts("FT-1")+"/2.md", t0.Add(2*time.Hour))
	write(root.Verdicts("FT-1")+"/3.md", t0.Add(2*time.Hour))

	if _, err := archive.ArchiveReviews(reviews, archive.SeriesEnd{PR: 2, At: t0.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	verdicts, rounds := FinishedShipFiles(root.Path(), "FT-1")
	if len(verdicts) != 2 || len(rounds) != 2 {
		t.Fatalf("verdicts %+v, rounds %+v: want two of each", verdicts, rounds)
	}

	for _, p := range append([]string{verdicts[0].Path, verdicts[1].Path}, rounds[0].Path, rounds[1].Path) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}

	if !strings.Contains(rounds[0].Path, filepath.Join("archive", "FT-1.2")) {
		t.Errorf("rounds read from %s, want the newest closed series", rounds[0].Path)
	}
}

// A task with a checkout is read there and never asks for children; with no
// run to read, a parent lists its children, a finished task its done row, and
// anything else keeps the error, a failed children read warned about; a
// guessed token asks the tracker nothing and prints nothing.
func TestStatusTaskWithNoRun(t *testing.T) {
	const (
		subIssues = "gh api --paginate repos/{owner}/{repo}/issues/1/sub_issues"
		issueView = "gh issue view 1 --json number,title,state,labels,createdAt,url,body,assignees"
	)

	for _, tc := range []struct {
		name     string
		kids     string
		fail     bool
		guessed  bool
		checkout bool // FT-1's branch is checked out in a worktree
		archived bool // FT-1's plan is archived: finished
		printed  bool
		stage    string // the one row printed, for a finished task
		text     bool   // read without --json: a finished task's block
		err      error
		warned   bool
	}{
		{name: "has a checkout", checkout: true, printed: true},
		{name: "finished", kids: "[]", archived: true, printed: true, stage: StageDone},
		{name: "finished, as text", kids: "[]", archived: true, printed: true, text: true},
		{name: "parent", kids: `[{"number":2,"title":"kid","state":"open","html_url":"https://x/2","body":"","labels":[]}]`, printed: true},
		{name: "host down", fail: true, err: errNoRunToRead, warned: true},
		{name: "guessed", guessed: true, err: errNoRunToRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			t.Setenv("F10_STATE_DIR", t.TempDir())

			tg := testkit.ChildTarget(t, "gh")
			testkit.ScriptNoBranch(f, "FT-2")

			if tc.checkout {
				f.Script(testkit.RefsKey("refs/heads/", "FT-1"), testkit.Heads("FT-1/x"))
				f.Script(testkit.Worktrees, testkit.Porcelain("FT-1/x", "/code/repo.FT-1"))
				f.Absent = append(f.Absent, "gh pr ", "git symbolic-ref ")
			} else {
				testkit.ScriptNoBranch(f, "FT-1")
			}

			if tc.archived {
				tr := stack.TrackerFor(tg)
				if err := os.MkdirAll(tr.Storage.Archive(), 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(filepath.Join(tr.Storage.Archive(), "FT-1.md"), nil, 0o600); err != nil {
					t.Fatal(err)
				}

				f.Fail(issueView, shell.Result{Code: 1, Stderr: "HTTP 502"})
			}
			f.Absent = append(
				f.Absent,
				"git rev-parse --show-toplevel",
				"git branch --show-current",
				"git config ",
				"git rev-parse ",
				"git remote",
				"git log ",
				"git worktree ",
				"gh api graphql ",
			)

			switch {
			case tc.fail:
				f.Fail(subIssues, shell.Result{Code: 1, Stderr: "HTTP 502"})
				f.Fail(issueView, shell.Result{Code: 1, Stderr: "HTTP 502"})
			case tc.kids != "":
				f.Script(subIssues, tc.kids)
			}

			rd, err := NewStatusReader(tg, time.Now())
			if err != nil {
				t.Fatal(err)
			}

			var out, warn bytes.Buffer

			p, err := rd.Task(
				t.Context(),
				StatusView{Out: &out, Warn: &warn, JSON: !tc.text, Plain: tc.text},
				"FT-1",
				tc.guessed,
			)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}

			if p.Printed != tc.printed {
				t.Errorf("printed = %v, want %v:\n%s", p.Printed, tc.printed, out.String())
			}

			if got := warn.Len() > 0; got != tc.warned {
				t.Errorf("warned = %q, want a warning %v", warn.String(), tc.warned)
			}

			if (tc.guessed || tc.checkout) && f.Called(subIssues) {
				t.Error("the tracker was asked for children")
			}

			if tc.text && (!strings.HasPrefix(out.String(), "task:") || strings.Contains(out.String(), "✓")) {
				t.Errorf("a finished task printed no block:\n%s", out.String())
			}

			if tc.stage != "" {
				var rows []TaskRow
				if err := json.Unmarshal(
					out.Bytes(),
					&rows,
				); err != nil || len(rows) != 1 ||
					rows[0].Stage != tc.stage {
					t.Errorf("rows = %+v (%v), want one %s row:\n%s", rows, err, tc.stage, out.String())
				}
			}
		})
	}
}

// A shipped task whose finish drive holds names it in the step column.
func TestRowStepNamesDriveFinish(t *testing.T) {
	f := lookaround.TaskFacts{Run: &state.Run{Plan: "done", Ship: "done"}}

	row := TaskRow{
		Gates: []gate{
			{Step: "finish", State: gateRunning, Via: viaDrive, Action: "merging #204"},
		},
	}
	row.Finish(f, false)

	if row.Stage != stageShipped || row.Step != "finish: running (drive): merging #204" {
		t.Errorf("row = %s / %q, want shipped / the finish action", row.Stage, row.Step)
	}

	plain := TaskRow{Gates: []gate{{Step: "finish", State: gateUpcoming}}}
	plain.Finish(f, false)

	if plain.Step != "ship" {
		t.Errorf("an upcoming finish changed the step to %q", plain.Step)
	}
}

// An open or merged PR leads with its icon, a closed one with none, and
// F10_STATUS_GLYPHS replaces the icons for a terminal without the font.
func TestPRColumnIcons(t *testing.T) {
	pr := "https://github.com/a/b/pull/7"

	for state, want := range map[string]string{
		forge.Open:   "\U000F04C2 #7",
		forge.Merged: "\U000F062D #7 merged",
		"closed":     "#7 closed",
	} {
		if got := prColumn(TaskRow{PR: pr, PRState: state}); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}

	t.Setenv("F10_STATUS_GLYPHS", "PR MR rv jd")

	if got := prColumn(TaskRow{PR: pr, PRState: forge.Merged}); got != "MR #7 merged" {
		t.Errorf("with F10_STATUS_GLYPHS: %q", got)
	}

	t.Setenv("F10_STATUS_GLYPHS", "too few")

	if got := prColumn(TaskRow{PR: pr}); got != "\U000F04C2 #7" {
		t.Errorf("a short F10_STATUS_GLYPHS replaced the icons: %q", got)
	}
}

// A task with a branch reads that branch's requests only: an older merged
// request on another head leaves its live run deciding the stage. Two rows
// of one id gather at once without sharing the requests they sort.
func TestGatherReadsTheBranchRequests(t *testing.T) {
	run := &state.Run{
		Task: "FT-5", Root: "/w/FT-5", Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement",
		Updated: time.Now(),
	}
	merged := lookaround.PR{Number: 40, Status: forge.Merged, Head: "FT-5/x", MergedAt: time.Now()}
	open := lookaround.PR{Number: 41, Status: forge.Open, Head: "FT-5/z"}

	src := rowSource{
		Live: []*state.Run{run},
		Checkout: func(_ context.Context, _, suffix string) (string, string) {
			if suffix == "" {
				return "FT-5/y", "/w/FT-5"
			}

			return "", ""
		},
		Requests: map[string][]lookaround.PR{"FT-5": {merged, open}},
	}

	if _, f := src.Gather(t.Context(), stack.ChildRow{ID: "FT-5"}); f.PR != nil {
		t.Errorf("FT-5/y read request #%d of another head", f.PR.Number)
	} else if stage, _ := stageOf(f, false); stage != stageImplementing {
		t.Errorf("stage = %s, want implementing", stage)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { src.Gather(t.Context(), stack.ChildRow{ID: "FT-5", Suffix: "2"}) })
	}

	wg.Wait()

	if got := src.Requests["FT-5"]; got[0].Number != 40 || got[1].Number != 41 {
		t.Errorf("the shared requests were reordered: #%d, #%d", got[0].Number, got[1].Number)
	}
}
