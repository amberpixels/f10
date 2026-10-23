package main

import (
	"bytes"
	"encoding/json"
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
		{"waiting", rowFacts{after: "FT-1"}, stageWaiting, "after FT-1"},
		{"base done", rowFacts{after: "FT-1", baseDone: true}, stageNotStarted, ""},
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

	rows := src.rowsFrom(t.Context(), []childRow{
		{ID: "FT-1", Title: "trailers"},
		{ID: "FT-2", Title: "state block"},
		{ID: "FT-3", Title: "look-around"},
		{ID: "FT-4", Title: "priority"},
		{ID: "FT-5", Title: "board"},
		{ID: "FT-6", Title: "after board", After: "FT-5", Depth: 1},
		{ID: "FT-7", Title: "after trailers", After: "FT-1", Depth: 1},
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

func TestRenderRows(t *testing.T) {
	rows := []taskRow{
		{
			ID: "FT-117", Title: "trailers + traces switch", Stage: stageDone, Step: "finish",
			PR: "https://github.com/a/b/pull/128", PRState: "merged",
		},
		{
			ID: "FT-118", Title: "PR state block", Stage: stageReviewing, Step: "review",
			PR: "https://github.com/a/b/pull/129", PRState: pullOpen,
		},
		{ID: "FT-120", Title: "look-around + f10 board", Depth: 1, Stage: stageWaiting, Step: "after FT-118"},
		{ID: "FT-121", Title: "a halted one", Stage: stageHalted, Step: "ship", Note: "agent idle", Next: "rerun"},
		{ID: "FT-122", Title: "an asking one", Stage: stageAsking, Step: "judge", Ask: "1. Proceed? [yes | no]"},
	}

	var out bytes.Buffer
	renderRows(&out, rows, 0)

	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("color off a terminal:\n%q", got)
	}

	for _, want := range []string{
		"FT-117    done       trailers + traces switch  finish        #128 merged\n",
		"FT-118    reviewing  PR state block            review        #129\n",
		"  FT-120  waiting    look-around + f10 board   after FT-118\n",
		"          halt: agent idle\n",
		"          next: rerun\n",
		"          ask: 1. Proceed? [yes | no]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}

	out.Reset()
	renderRows(&out, rows[:1], 50)

	if line := strings.TrimSuffix(out.String(), "\n"); len([]rune(line)) > 50 || !strings.Contains(line, "…") {
		t.Errorf("a 50-column line = %q, want the title cut to fit", line)
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
