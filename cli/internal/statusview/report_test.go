package statusview

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/state"
)

// A report travels from the daemon to the client as JSON, so every shape
// must print the same bytes, text and --json, after the trip as before it.
func TestReportPrintsTheSameAfterAJSONTrip(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	ship := Shipment{
		Task:    "GH-1",
		Title:   "Rate limits",
		URL:     "https://gh/issues/1",
		Branch:  "GH-1/rate-limits",
		PR:      "https://gh/pr/9",
		PRState: "open",
		Commit:  "abc1234",
		Plan:    "/s/plans/GH-1.md",
		Checks: &forge.HeadChecks{
			State:  forge.Red,
			SHA:    "abc1234",
			Failed: []forge.Check{{Name: "test", URL: "https://ci/1"}},
		},
		Verdicts: []verdictPath{{Subject: "plan", Path: "/s/verdicts/GH-1/1.md"}},
		Reviews:  []reviewPath{{Round: "1", Path: "/s/reviews/GH-1/1.md"}},
	}
	gates := []gate{
		{
			Step:    "judge",
			Judged:  "plan",
			State:   "done",
			Outcome: &archive.JudgeOutcome{Verdict: "proceed", Reason: "holds"},
		},
		{Step: "review", Label: "review:local", Local: &reviewHalf{
			State: "done", Rounds: []reviewRound{{Round: "1", Found: 3, Fixed: 2, Skipped: 1}},
		}},
		{Step: "pr", State: "running", Via: "ship"},
	}
	run := &state.Run{
		Session: "s1", Task: "GH-1", Root: "/code/repo.GH-1", Skill: "ship", Capture: "prior", Plan: "done",
		Ship: "running", Leaf: "pr", At: 4, Pipeline: []string{"judge", "implement", "review:local", "pr"},
		Ask: "1. Merge? [yes | no]", Updated: at, DoneAt: at,
	}
	row := TaskRow{
		ID: "GH-2", Title: "Reject excess", URL: "https://gh/issues/2", Depth: 1, After: "GH-1", Stage: "asking",
		Step: "judge", PR: "https://gh/pr/10", PRState: "open", Checks: "green", Ask: "1. Go? [yes | no]", Gates: gates,
	}
	fb := taskFallback(taskStatusInput{ID: "GH-1", Root: "/code/repo.GH-1"}, nil, ship)

	for name, rep := range map[string]Report{
		"line":     lineReport("no live f10 run in %s (--all lists every session)", "/code/repo"),
		"headed":   runsReport([]*state.Run{run}, [][]gate{gates}, &ship, true, "opus"),
		"listed":   runsReport([]*state.Run{run, run}, make([][]gate, 2), nil, true, ""),
		"rows":     rowsReport([]TaskRow{row, {ID: "GH-3", Title: "Count", Stage: StageDone}}, "GH-1"),
		"fallback": {Kind: ReportFallback, Fallback: &fb},
		"finished": {Kind: ReportFinished, Finished: &finished{Row: row, Shipment: ship}},
	} {
		raw, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}

		var back Report
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if back.Asks != rep.Asks {
			t.Errorf("%s: asks %v after the trip, want %v", name, back.Asks, rep.Asks)
		}

		for _, v := range []StatusView{{JSON: true}, {Plain: true}, {}} {
			var before, after bytes.Buffer

			v.Out = &before
			if err := Render(v, rep); err != nil {
				t.Fatal(err)
			}

			v.Out = &after
			if err := Render(v, back); err != nil {
				t.Fatal(err)
			}

			if before.String() != after.String() {
				t.Errorf("%s (json %v, plain %v) prints differently after the trip:\nbefore:\n%s\nafter:\n%s",
					name, v.JSON, v.Plain, before.String(), after.String())
			}

			if before.Len() == 0 {
				t.Errorf("%s (json %v, plain %v) printed nothing", name, v.JSON, v.Plain)
			}
		}
	}
}
