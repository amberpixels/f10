package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
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
		// resolve called by a review step through the Skill tool: still the ship run
		sessionEvent(at(5), "s1", ledger.RunStarted, ledger.Payload{"skill": "resolve"}),
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

// A round both halves wrote counts once for each half, by its blocks'
// source.
func TestReviewRoundsSplitBothHalves(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "reviews", "FT-1", "1.md"), `task: FT-1
round: 1
reviewer: local, claude[bot]
verdict: findings

## Findings

### 1.1 Off by one
category: correctness
source: local
verdict: fix

### 1.2 Missing test
category: tests
source: remote
verdict: skip +reply

### 1.3 Which default?
category: design
source: remote
verdict: ask
`)

	o := fileOutcomes(root, "FT-1", time.Time{})
	if want := []reviewRound{{Round: "1", Found: 1, Fixed: 1}}; !slices.Equal(o.local, want) {
		t.Errorf("local = %+v, want %+v", o.local, want)
	}

	if want := []reviewRound{{Round: "1", Found: 2, Skipped: 1, Ask: 1}}; !slices.Equal(o.remote, want) {
		t.Errorf("remote = %+v, want %+v", o.remote, want)
	}

	if o.localRaised != 1 || o.remoteRaised != 2 {
		t.Errorf("raised local %d, remote %d", o.localRaised, o.remoteRaised)
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
	writeFile(t, filepath.Join(reviews, "4.md"), "task: FT-1\nround: 1\nreviewer: claude[bot]\nverdict: clean\n")

	o := fileOutcomes(root, "FT-1", time.Time{})
	want := []reviewRound{
		{Round: "1", Found: 3, Fixed: 1, Skipped: 1, Ask: 1},
		{Round: "2", Found: 2, Fixed: 1, Ask: 1},
	}

	if !slices.Equal(o.local, want) {
		t.Errorf("local rounds = %+v, want %+v", o.local, want)
	}

	if !slices.Equal(o.remote, []reviewRound{{Round: "1"}}) || o.remoteAt.IsZero() {
		t.Errorf("remote rounds = %+v at %v, want 4.md's clean round", o.remote, o.remoteAt)
	}

	verdicts := filepath.Join(root, "verdicts", "FT-1")
	writeFile(t, filepath.Join(verdicts, "9.md"), "task: FT-1\nverdict: rethink\nreason: old\n")
	writeFile(t, filepath.Join(verdicts, "10.md"),
		"task: FT-1\nverdict: proceed with changes\nreason: tighten the parser\n")
	writeFile(t, filepath.Join(verdicts, "11.md"), "subject: idea something else\nverdict: stop\n")
	writeFile(t, filepath.Join(verdicts, "12.md"), "task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: sound\n")

	o = fileOutcomes(root, "FT-1", time.Time{})
	if j := o.changeJudge; j == nil || j.Verdict != "proceed with changes" || j.Reason != "tighten the parser" {
		t.Errorf("change judge = %+v, want 10.md's verdict", j)
	}

	if j := o.planJudge; j == nil || j.Verdict != "proceed" {
		t.Errorf("plan judge = %+v, want 12.md's verdict", j)
	}

	if o := fileOutcomes(
		root,
		"FT-2",
		time.Time{},
	); o.local != nil || o.remote != nil || o.changeJudge != nil ||
		o.planJudge != nil {
		t.Errorf("a task with no files has outcomes: %+v", o)
	}
}
