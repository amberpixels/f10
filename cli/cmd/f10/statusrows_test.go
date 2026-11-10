package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
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
		{"finished", facts{Finished: true}, false, statusview.StageDone, "finish"},
		{"merged", facts{PR: merged, Run: run("prior", "done", "done", "")}, false, statusview.StageDone, "finish"},
		{"ask", facts{Run: asking}, false, statusview.StageAsking, "judge"},
		{
			"drive halt",
			facts{Halt: &lookaround.Halt{Skill: "ship"}, Run: run("prior", "done", "running", "implement")},
			false, statusview.StageHalted, "ship",
		},
		{"run stopped", facts{Run: run("prior", "done", "failed", "pr")}, false, statusview.StageHalted, "pr"},
		{"implementing", facts{Run: run("prior", "done", "running", "cleanup")}, false, statusview.StageImplementing, "cleanup"},
		{"reviewing", facts{Run: run("prior", "done", "running", "review")}, false, statusview.StageReviewing, "review"},
		{"resolving", facts{Run: run("prior", "done", "running", "resolve")}, false, statusview.StageResolving, "resolve"},
		{"judging", facts{Run: run("prior", "done", "running", "judge")}, false, statusview.StageJudging, "judge"},
		{
			"shipped",
			facts{Run: &state.Run{Capture: "prior", Plan: "done", Ship: "done", Final: "pr"}},
			false, statusview.StageShipped, "pr",
		},
		{"planning", facts{Run: run("skipped", "running", "upcoming", "")}, false, statusview.StagePlanning, "plan"},
		{"planned", facts{Run: run("skipped", "done", "upcoming", "")}, false, statusview.StagePlanned, "plan"},
		{"after, no drive", facts{Base: "FT-1"}, false, statusview.StageNotStarted, "after FT-1"},
		{"waiting", facts{Base: "FT-1", Driven: true}, false, statusview.StageWaiting, "after FT-1"},
		{
			"parked",
			facts{Run: &state.Run{Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "implement", Wait: "FT-1"}},
			false, statusview.StageWaiting, "on FT-1",
		},
		{"base done", facts{Base: "FT-1"}, true, statusview.StageNotStarted, ""},
		{"queued", facts{Queued: true, Base: "FT-1"}, false, statusview.StageQueued, ""},
		{
			"parked by its drive",
			facts{Run: run("skipped", "done", "upcoming", ""), Base: "FT-1", DrivePhase: drives.PhaseParked},
			false, statusview.StageWaiting, "on FT-1",
		},
		{
			"finish waits for its base",
			facts{
				Run:  &state.Run{Capture: "prior", Plan: "done", Ship: "done", Final: "pr"},
				Base: "FT-1", DrivePhase: drives.PhaseWaiting,
			},
			false, statusview.StageWaiting, "to finish after FT-1",
		},
		{"nothing", facts{}, false, statusview.StageNotStarted, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if stage, step := statusview.StageOf(tc.f, tc.baseDone); stage != tc.stage || step != tc.step {
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

	for _, rec := range []*drives.Record{
		{Tasks: []drives.Task{{ID: "FT-1"}}},
		{Cancelled: &now, Tasks: []drives.Task{{ID: "FT-5"}}},
		{Tasks: []drives.Task{{ID: "FT-6", Outcome: &drives.Outcome{Row: "ok"}}}},
		{Tasks: []drives.Task{{ID: "ft-2_v2+FT-3"}}},
	} {
		if err := drives.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	for _, n := range []int{2, 3, 4} {
		lease, err := drives.Acquire(root, drives.DriveKey(n), n)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(lease.Release)
	}

	got := statusview.DrivenTasks(drives.Active(root))
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

	src := statusview.RowSource{Events: []ledger.Event{
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

	got := map[string]statusview.TaskRow{}
	for _, r := range rows {
		got[r.ID] = r
	}

	check := func(id, stage, step string) {
		t.Helper()

		if r := got[id]; r.Stage != stage || r.Step != step {
			t.Errorf("%s = %q %q, want %q %q", id, r.Stage, r.Step, stage, step)
		}
	}

	check("FT-1", statusview.StageDone, "finish")
	check("FT-2", statusview.StageHalted, "ship")
	check("FT-3", statusview.StageReviewing, "review")
	check("FT-4", statusview.StageAsking, "judge")
	check("FT-5", statusview.StageHalted, "pr")
	check("FT-6", statusview.StageWaiting, "after FT-5")
	check("FT-7", statusview.StageNotStarted, "")
	check("FT-8", statusview.StageDone, "finish")
	check("FT-10", statusview.StageNotStarted, "after FT-5")

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
	if *updateGolden {
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

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// A parent's children as a tree: nested After: chains, a halted child with
// its halt and next, an asking child, and an unrelated task; every line a
// task row carries an id, and none of it is a markdown table.
func TestRenderRows(t *testing.T) {
	gh := func(kind string, n int) string { return "https://github.com/a/b/" + kind + "/" + strconv.Itoa(n) }
	rows := []statusview.TaskRow{
		{
			ID: "FT-206", URL: gh("issues", 206), Title: "Show who runs a driven finish", Stage: statusview.StageDone,
			Step: "finish", PR: gh("pull", 208), PRState: "merged",
		},
		{
			ID: "FT-210", URL: gh("issues", 210), Title: "Keep a drive as a record", Stage: statusview.StageDone,
			Step: "finish", PR: gh("pull", 216), PRState: "merged",
		},
		{
			ID:      "FT-212",
			URL:     gh("issues", 212),
			Title:   "Run drives in a per-user daemon",
			Depth:   1,
			After:   "FT-210",
			Stage:   statusview.StageReviewing,
			Step:    "review",
			PR:      gh("pull", 219),
			PRState: forge.Open,
			Gates: []statusview.Gate{
				{
					Step:    "judge",
					Judged:  "plan",
					State:   statusview.GateDone,
					Outcome: &archive.JudgeOutcome{Verdict: "proceed"},
				},
			},
		},
		{
			ID: "FT-213", URL: gh("issues", 213), Title: "Keep asks in an inbox the user is told about",
			Depth: 2, After: "FT-212", Stage: statusview.StageHalted, Step: "implement",
			Note: "catchup guard: merging FT-212 conflicts in drives/record.go",
			Next: "/f10:catchup, then /f10:ship FT-213",
		},
		{
			ID: "FT-214", URL: gh("issues", 214), Title: "Let the daemon watch tasks no drive owns", Depth: 2,
			After: "FT-212", Stage: statusview.StageAsking, Step: "judge", Ask: "1. Proceed? [yes | no]",
		},
		{ID: "FT-230", URL: gh("issues", 230), Title: "An unrelated task", Stage: statusview.StageNotStarted},
	}

	var out bytes.Buffer
	if err := statusview.RenderRows(
		&out,
		rows,
		statusview.RowView{Parent: "FT-209", Plain: true, Width: 100},
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

	if err := statusview.RenderRows(&out, rows[:1], statusview.RowView{Term: true, Width: 80}); err != nil {
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

// A finished task alone is a one-row tree with every gate on a line.
func TestRenderFinishedRow(t *testing.T) {
	row := statusview.TaskRow{
		ID:      "FT-210",
		URL:     "https://github.com/a/b/issues/210",
		Title:   "Keep a drive as a record",
		Stage:   statusview.StageDone,
		Step:    "finish",
		PR:      "https://github.com/a/b/pull/216",
		PRState: "merged",
		Full:    true,
		Gates: []statusview.Gate{
			{
				Step:    "judge",
				Judged:  "plan",
				State:   statusview.GateDone,
				Outcome: &archive.JudgeOutcome{Verdict: "proceed"},
			},
			{Step: "implement", State: statusview.GateDone},
			{Step: "cleanup", State: statusview.GateDone},
			{Step: "pr", State: statusview.GateDone},
		},
	}

	var out bytes.Buffer
	if err := statusview.RenderRows(
		&out,
		[]statusview.TaskRow{row},
		statusview.RowView{Plain: true, Width: 100},
	); err != nil {
		t.Fatal(err)
	}

	statusGolden(t, "finished", out.String())
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
		err      error
		warned   bool
	}{
		{name: "has a checkout", checkout: true, printed: true},
		{name: "finished", kids: "[]", archived: true, printed: true, stage: statusview.StageDone},
		{name: "parent", kids: `[{"number":2,"title":"kid","state":"open","html_url":"https://x/2","body":"","labels":[]}]`, printed: true},
		{name: "host down", fail: true, err: statusview.ErrNoRunToRead, warned: true},
		{name: "guessed", guessed: true, err: statusview.ErrNoRunToRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakes(t)
			t.Setenv("F10_STATE_DIR", t.TempDir())

			tg := childTarget(t, "gh")
			scriptNoBranch(f, "FT-2")

			if tc.checkout {
				f.script(refsKey("refs/heads/", "FT-1"), heads("FT-1/x"))
				f.script(worktrees, porcelain("FT-1/x", "/code/repo.FT-1"))
				f.absent = append(f.absent, "gh pr ", "git symbolic-ref ")
			} else {
				scriptNoBranch(f, "FT-1")
			}

			if tc.archived {
				tr := stack.TrackerFor(tg)
				if err := os.MkdirAll(tr.Storage.Archive(), 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(filepath.Join(tr.Storage.Archive(), "FT-1.md"), nil, 0o600); err != nil {
					t.Fatal(err)
				}

				f.fail(issueView, shell.Result{Code: 1, Stderr: "HTTP 502"})
			}
			f.absent = append(
				f.absent,
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
				f.fail(subIssues, shell.Result{Code: 1, Stderr: "HTTP 502"})
				f.fail(issueView, shell.Result{Code: 1, Stderr: "HTTP 502"})
			case tc.kids != "":
				f.script(subIssues, tc.kids)
			}

			rd, err := statusview.NewStatusReader(tg, time.Now())
			if err != nil {
				t.Fatal(err)
			}

			var out, warn bytes.Buffer

			p, err := rd.Task(
				t.Context(),
				statusview.StatusView{Out: &out, Warn: &warn, JSON: true},
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

			if (tc.guessed || tc.checkout) && f.called(subIssues) {
				t.Error("the tracker was asked for children")
			}

			if tc.stage != "" {
				var rows []statusview.TaskRow
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

	row := statusview.TaskRow{
		Gates: []statusview.Gate{
			{Step: "finish", State: statusview.GateRunning, Via: statusview.ViaDrive, Action: "merging #204"},
		},
	}
	row.Finish(f, false)

	if row.Stage != statusview.StageShipped || row.Step != "finish: running (drive): merging #204" {
		t.Errorf("row = %s / %q, want shipped / the finish action", row.Stage, row.Step)
	}

	plain := statusview.TaskRow{Gates: []statusview.Gate{{Step: "finish", State: statusview.GateUpcoming}}}
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
		if got := statusview.PRColumn(statusview.TaskRow{PR: pr, PRState: state}); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}

	t.Setenv("F10_STATUS_GLYPHS", "PR MR rv jd")

	if got := statusview.PRColumn(statusview.TaskRow{PR: pr, PRState: forge.Merged}); got != "MR #7 merged" {
		t.Errorf("with F10_STATUS_GLYPHS: %q", got)
	}

	t.Setenv("F10_STATUS_GLYPHS", "too few")

	if got := statusview.PRColumn(statusview.TaskRow{PR: pr}); got != "\U000F04C2 #7" {
		t.Errorf("a short F10_STATUS_GLYPHS replaced the icons: %q", got)
	}
}

// A halt drive records reads back through the ledger's readers.
func TestHaltPayloadReadsBack(t *testing.T) {
	p := drive.HaltPayload(drive.DriveResult{Skill: "ship", Reason: "stalled"})

	e := ledger.Event{Kind: ledger.TaskHalted, Payload: ledger.NewPayload(p)}
	if e.Skill() != "ship" || e.Reason() != "stalled" {
		t.Errorf("halt %v", e.Payload.Val)
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

	src := statusview.RowSource{
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
	} else if stage, _ := statusview.StageOf(f, false); stage != statusview.StageImplementing {
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
