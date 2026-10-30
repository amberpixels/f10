package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

func TestStageOf(t *testing.T) {
	run := func(capture, plan, ship, leaf string) *state.Run {
		return &state.Run{Capture: capture, Plan: plan, Ship: ship, Leaf: leaf}
	}
	asking := run("prior", "done", "blocked", "judge")
	asking.Ask = "1. Merge? [yes | no]"

	for _, tc := range []struct {
		name        string
		f           rowFacts
		stage, step string
	}{
		{"finished", rowFacts{finished: true}, stageDone, "finish"},
		{"merged", rowFacts{merged: true, run: run("prior", "done", "done", "")}, stageDone, "finish"},
		{"ask", rowFacts{run: asking}, stageAsking, "judge"},
		{"drive halt", rowFacts{halt: &ledgerHalt{skill: "ship"}, run: run("prior", "done", "running", "implement")}, stageHalted, "ship"},
		{"run stopped", rowFacts{run: run("prior", "done", "failed", "pr")}, stageHalted, "pr"},
		{"implementing", rowFacts{run: run("prior", "done", "running", "cleanup")}, stageImplementing, "cleanup"},
		{"reviewing", rowFacts{run: run("prior", "done", "running", "review")}, stageReviewing, "review"},
		{"resolving", rowFacts{run: run("prior", "done", "running", "resolve")}, stageResolving, "resolve"},
		{"judging", rowFacts{run: run("prior", "done", "running", "judge")}, stageJudging, "judge"},
		{"shipped", rowFacts{run: &state.Run{Capture: "prior", Plan: "done", Ship: "done", Final: "pr"}}, stageShipped, "pr"},
		{"planning", rowFacts{run: run("skipped", "running", "pending", "")}, stagePlanning, "plan"},
		{"planned", rowFacts{run: run("skipped", "done", "pending", "")}, stagePlanned, "plan"},
		{"waiting", rowFacts{after: "FT-1"}, stageWaiting, "after FT-1"},
		{
			"parked",
			rowFacts{run: &state.Run{Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "implement", Wait: "FT-1"}},
			stageWaiting, "on FT-1",
		},
		{"base done", rowFacts{after: "FT-1", baseDone: true}, stageNotStarted, ""},
		{"queued", rowFacts{queued: true, after: "FT-1"}, stageQueued, ""},
		{"nothing", rowFacts{}, stageNotStarted, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if stage, step := stageOf(tc.f); stage != tc.stage || step != tc.step {
				t.Errorf("stageOf = %q %q, want %q %q", stage, step, tc.stage, tc.step)
			}
		})
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

	src := rowSource{events: []ledger.Event{
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
	src.resolve = func(tok string) string {
		if tok == "" {
			t.Error("resolved an empty finish argument")
		}

		return "FT-" + strings.TrimPrefix(tok, "FT-")
	}

	rows := src.rowsFrom(t.Context(), []childRow{
		{ID: "FT-1", Title: "trailers"},
		{ID: "FT-2", Title: "state block"},
		{ID: "FT-3", Title: "look-around"},
		{ID: "FT-4", Title: "priority"},
		{ID: "FT-5", Title: "board"},
		{ID: "FT-6", Title: "after board", After: "FT-5", Depth: 1},
		{ID: "FT-7", Title: "after trailers", After: "FT-1", Depth: 1},
		{ID: "FT-8", Title: "finished by number"},
	})

	got := map[string]taskRow{}
	for _, r := range rows {
		got[r.ID] = r
	}

	check := func(id, stage, step string) {
		t.Helper()

		if r := got[id]; r.Stage != stage || r.Step != step {
			t.Errorf("%s = %q %q, want %q %q", id, r.Stage, r.Step, stage, step)
		}
	}

	check("FT-1", stageDone, "finish")
	check("FT-2", stageHalted, "ship")
	check("FT-3", stageReviewing, "review")
	check("FT-4", stageAsking, "judge")
	check("FT-5", stageHalted, "pr")
	check("FT-6", stageWaiting, "after FT-5")
	check("FT-7", stageNotStarted, "")
	check("FT-8", stageDone, "finish")

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
	rows := []taskRow{
		{
			ID: "FT-206", URL: gh("issues", 206), Title: "Show who runs a driven finish", Stage: stageDone,
			Step: "finish", PR: gh("pull", 208), PRState: "merged",
		},
		{
			ID: "FT-210", URL: gh("issues", 210), Title: "Keep a drive as a record", Stage: stageDone,
			Step: "finish", PR: gh("pull", 216), PRState: "merged",
		},
		{
			ID: "FT-212", URL: gh("issues", 212), Title: "Run drives in a per-user daemon", Depth: 1,
			After: "FT-210", Stage: stageReviewing, Step: "review", PR: gh("pull", 219), PRState: pullOpen,
			Gates: []gate{{Step: "judge", Judged: "plan", State: gateDone, Outcome: &judgeOutcome{Verdict: "proceed"}}},
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
	if err := renderRows(&out, rows, rowView{parent: "FT-209", plain: true, width: 100}); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "|") && strings.Contains(got, "|---") {
		t.Errorf("plain output carries escapes or a table:\n%s", got)
	}

	statusGolden(t, "parent", got)

	// on a terminal the id and the PR are links that take no columns
	out.Reset()

	if err := renderRows(&out, rows[:1], rowView{term: true, width: 80}); err != nil {
		t.Fatal(err)
	}

	line, _, _ := strings.Cut(out.String(), "\n")
	if !strings.Contains(line, hyperlink(rows[0].URL, "FT-206")) ||
		!strings.Contains(line, hyperlink(rows[0].PR, "\U000F062D #208 merged")) {
		t.Errorf("a terminal line = %q, want the id and the PR linked", line)
	}

	out.Reset()

	if err := encodeJSON(&out, rows[:1]); err != nil {
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
	row := taskRow{
		ID: "FT-210", URL: "https://github.com/a/b/issues/210", Title: "Keep a drive as a record",
		Stage: stageDone, Step: "finish", PR: "https://github.com/a/b/pull/216", PRState: "merged", full: true,
		Gates: []gate{
			{Step: "judge", Judged: "plan", State: gateDone, Outcome: &judgeOutcome{Verdict: "proceed"}},
			{Step: "implement", State: gateDone},
			{Step: "cleanup", State: gateDone},
			{Step: "pr", State: gateDone},
		},
	}

	var out bytes.Buffer
	if err := renderRows(&out, []taskRow{row}, rowView{plain: true, width: 100}); err != nil {
		t.Fatal(err)
	}

	statusGolden(t, "finished", out.String())
}

// A task with a run is read from its checkout and never asks for children;
// with no run, a parent lists its children, a finished task its done row,
// and anything else keeps the error, a failed children read warned about.
func TestRouteTask(t *testing.T) {
	noRun := fmt.Errorf("FT-1 has no branch here: %w", errNoRunToRead)
	kids := []taskRow{{ID: "FT-2", Stage: stageDone}}
	done := &taskRow{ID: "FT-1", Stage: stageDone}

	for _, tc := range []struct {
		name     string
		status   error
		children []taskRow
		childErr error
		finished *taskRow
		rows     int
		err      error
		warned   bool
	}{
		{name: "has a run", rows: 0},
		{name: "parent", status: noRun, children: kids, rows: 1},
		{name: "finished", status: noRun, childErr: errNoParentLink, finished: done, rows: 1},
		{name: "neither", status: noRun, err: errNoRunToRead},
		{name: "host down", status: noRun, childErr: errors.New("HTTP 502"), err: errNoRunToRead, warned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := false

			var warn bytes.Buffer

			in, rows, err := routeTask(&warn, taskRoute{
				status: func() (taskStatusInput, error) { return taskStatusInput{id: "FT-1"}, tc.status },
				children: func() ([]taskRow, error) {
					asked = true

					return tc.children, tc.childErr
				},
				finished: func() (*taskRow, error) { return tc.finished, nil },
			})

			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}

			if tc.status == nil && (in == nil || asked) {
				t.Errorf("a task with a run: in = %v, children asked = %v", in, asked)
			}

			if len(rows) != tc.rows {
				t.Errorf("rows = %+v, want %d", rows, tc.rows)
			}

			if got := warn.Len() > 0; got != tc.warned {
				t.Errorf("warned = %q, want a warning %v", warn.String(), tc.warned)
			}
		})
	}
}

// A shipped task whose finish drive holds names it in the step column.
func TestRowStepNamesDriveFinish(t *testing.T) {
	f := rowFacts{run: &state.Run{Plan: "done", Ship: "done"}}

	row := taskRow{Gates: []gate{{Step: "finish", State: gateRunning, Via: viaDrive, Action: "merging #204"}}}
	row.finish(f)

	if row.Stage != stageShipped || row.Step != "finish: running (drive): merging #204" {
		t.Errorf("row = %s / %q, want shipped / the finish action", row.Stage, row.Step)
	}

	plain := taskRow{Gates: []gate{{Step: "finish", State: gatePending}}}
	plain.finish(f)

	if plain.Step != "ship" {
		t.Errorf("a pending finish changed the step to %q", plain.Step)
	}
}

// An open or merged PR leads with its icon, a closed one with none, and
// F10_STATUS_GLYPHS replaces the icons for a terminal without the font.
func TestPRColumnIcons(t *testing.T) {
	pr := "https://github.com/a/b/pull/7"

	for state, want := range map[string]string{
		pullOpen:   "\U000F04C2 #7",
		pullMerged: "\U000F062D #7 merged",
		"closed":   "#7 closed",
	} {
		if got := prColumn(taskRow{PR: pr, PRState: state}); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}

	t.Setenv("F10_STATUS_GLYPHS", "PR MR rv jd")

	if got := prColumn(taskRow{PR: pr, PRState: pullMerged}); got != "MR #7 merged" {
		t.Errorf("with F10_STATUS_GLYPHS: %q", got)
	}

	t.Setenv("F10_STATUS_GLYPHS", "too few")

	if got := prColumn(taskRow{PR: pr}); got != "\U000F04C2 #7" {
		t.Errorf("a short F10_STATUS_GLYPHS replaced the icons: %q", got)
	}
}
