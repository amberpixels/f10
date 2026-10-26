package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

func sessionEvent(at time.Time, session, kind string, p ledger.Payload) ledger.Event {
	e := event(at, "FT-1", kind, p)
	e.Session = session

	return e
}

func shipClaim(at time.Time, session, step string) ledger.Event {
	return sessionEvent(at, session, ledger.StateSet, ledger.Payload{"args": []any{"ship", "running", step}})
}

func TestShipSteps(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	events := []ledger.Event{
		// a plan run first in the same session: none of its claims count
		sessionEvent(at(0), "s1", ledger.RunStarted, ledger.Payload{"skill": "plan"}),
		sessionEvent(at(0), "s1", ledger.StateSet, ledger.Payload{"args": []any{"plan", "running"}}),
		sessionEvent(at(1), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		shipClaim(at(2), "s1", "implement"),
		shipClaim(at(3), "s1", "cleanup"),
		shipClaim(at(4), "s1", "pr"),
		sessionEvent(at(4), "s1", ledger.StateNote, ledger.Payload{"args": []any{"why"}}),
		shipClaim(at(5), "s1", "review"),
		// a driven run resuming at the step it blocked on
		sessionEvent(at(6), "s1", ledger.StateSet, ledger.Payload{"args": []any{"ship", "blocked", "judge"}}),
		shipClaim(at(6), "s1", "judge"),
		shipClaim(at(7), "s1", "judge"),
		shipClaim(at(8), "s1", "pr"),
		sessionEvent(at(9), "s1", ledger.StateSet, ledger.Payload{"args": []any{"ship", "done"}}),
		// another session's claims
		shipClaim(at(5), "s2", "deploy"),
		// a later run in the same session
		sessionEvent(at(20), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		shipClaim(at(21), "s1", "implement"),
	}

	want := []string{"implement", "cleanup", "pr", "review", "judge", "pr"}
	if got := shipSteps(events, "s1", at(9)); !slices.Equal(got, want) {
		t.Errorf("shipSteps = %v, want %v", got, want)
	}

	if got := shipSteps(events, "s1", at(30)); !slices.Equal(got, []string{"implement"}) {
		t.Errorf("the later run: shipSteps = %v, want [implement]", got)
	}

	// claims with no ship run started in the session: an older run
	if got := shipSteps(events, "s2", at(30)); got != nil {
		t.Errorf("no ship run: shipSteps = %v, want nil", got)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReviewAndJudgeFromFiles(t *testing.T) {
	root := t.TempDir()
	reviews := filepath.Join(root, "reviews", "FT-1")

	writeFile(t, filepath.Join(reviews, "1.md"), `task: FT-1
round: 1
verdict: findings

## Findings

### 1.1 Off by one
category: correctness
verdict: fix

### 1.2 Name drift
category: naming
verdict: skip +reply
reason: house style

### 1.3 Which default?
category: design
verdict: ask
`)
	writeFile(t, filepath.Join(reviews, "2.md"), `task: FT-1
round: 2
verdict: findings

## Prior round

### 1.1 Off by one - fixed

### 1.3 Which default? - still open
verdict: fix

## Findings

### 2.1 Leaked handle
category: correctness
`)
	writeFile(t, filepath.Join(reviews, "3.md"), "no header here\n")

	got := reviewRounds(root, "FT-1")
	want := []reviewRound{
		{Round: "1", Found: 3, Fixed: 1, Skipped: 1, Ask: 1},
		{Round: "2", Found: 2, Fixed: 1, Ask: 1},
	}

	if !slices.Equal(got, want) {
		t.Errorf("reviewRounds = %+v, want %+v", got, want)
	}

	verdicts := filepath.Join(root, "verdicts", "FT-1")
	writeFile(t, filepath.Join(verdicts, "9.md"), "task: FT-1\nverdict: rethink\nreason: old\n")
	writeFile(t, filepath.Join(verdicts, "10.md"),
		"task: FT-1\nverdict: proceed with changes\nreason: tighten the parser\n")
	writeFile(t, filepath.Join(verdicts, "11.md"), "subject: idea something else\nverdict: stop\n")

	j := latestJudge(root, "FT-1")
	if j == nil || j.Verdict != "proceed with changes" || j.Reason != "tighten the parser" {
		t.Errorf("latestJudge = %+v, want 10.md's verdict", j)
	}

	if reviewRounds(root, "FT-2") != nil || latestJudge(root, "FT-2") != nil {
		t.Error("a task with no files has a review or judge")
	}
}

func TestHistoryLines(t *testing.T) {
	h := runHistory{
		Steps:  []string{"implement", "pr"},
		Review: []reviewRound{{Round: "1", Found: 3, Fixed: 2, Skipped: 1}, {Round: "2"}},
		Judge:  &judgeOutcome{Verdict: "proceed", Reason: "sound"},
	}

	want := [][2]string{
		{"steps", "implement, pr"},
		{"review", "round 1: 3 found (2 fixed, 1 skipped); round 2: clean"},
		{"judge", "proceed - sound"},
	}

	if got := h.lines(); !slices.Equal(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}

	if got := (runHistory{Review: []reviewRound{{Round: "1", Found: 1, Ask: 1}}}).lines(); got[0][1] != "1 found (1 ask)" {
		t.Errorf("one round = %q", got[0][1])
	}

	if got := (runHistory{}).lines(); got != nil {
		t.Errorf("an empty history prints %v", got)
	}
}

func TestRenderRunHistory(t *testing.T) {
	r := &state.Run{Task: "FT-1", Capture: "prior", Plan: "done", Ship: "done", Final: "pr"}

	var before, with bytes.Buffer

	renderRun(&before, r, nil, false, time.Now(), 0)
	renderRun(&with, r, &runHistory{}, false, time.Now(), 0)

	// an older run with nothing recorded prints as it did
	if before.String() != with.String() {
		t.Errorf("an empty history changed the block:\n%s\nvs\n%s", before.String(), with.String())
	}

	var out bytes.Buffer

	h := &runHistory{Steps: []string{"implement", "pr"}, Judge: &judgeOutcome{Verdict: "proceed"}}
	renderRun(&out, r, h, false, time.Now(), 0)

	for _, s := range []string{"implement, pr", "proceed"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}

	raw, err := json.Marshal([]runJSON{{Run: r, History: &runHistory{Steps: []string{"pr"}}}, {Run: r}})
	if err != nil {
		t.Fatal(err)
	}

	var back []map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}

	hj, _ := back[0]["history"].(map[string]any)
	if back[0]["ship"] != "done" || hj["steps"] == nil || hj["judge"] != nil {
		t.Errorf("history JSON = %s", raw)
	}

	if _, ok := back[1]["history"]; ok {
		t.Errorf("a run with no history carries one: %s", raw)
	}
}

func TestFinishedRowHistory(t *testing.T) {
	row := taskRow{ID: "FT-1", Title: "Thing", Stage: stageDone, Step: "finish", History: &runHistory{
		Steps:  []string{"implement", "pr"},
		Review: []reviewRound{{Round: "1", Found: 1, Fixed: 1}},
		Judge:  &judgeOutcome{Verdict: "proceed"},
	}}

	var md bytes.Buffer

	markdownRows(&md, []taskRow{row})

	for _, s := range []string{"| | | steps: implement, pr | | |", "review: 1 found (1 fixed)", "judge: proceed"} {
		if !strings.Contains(md.String(), s) {
			t.Errorf("markdown lacks %q:\n%s", s, md.String())
		}
	}

	var plain bytes.Buffer

	renderRows(&plain, []taskRow{row}, 0)

	if !strings.Contains(plain.String(), "steps: implement, pr") {
		t.Errorf("rows lack the steps:\n%s", plain.String())
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []string{`"history":{"steps":["implement","pr"]`, `"judge":{"verdict":"proceed"}`} {
		if !strings.Contains(string(raw), s) {
			t.Errorf("row JSON lacks %s: %s", s, raw)
		}
	}
}
