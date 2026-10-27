package findings

import (
	"errors"
	"reflect"
	"testing"
)

const roundTwo = `task: GH-13
round: 2
reviewed: e6af491 dirty
base: main e6af491
reviewer: local
verdict: findings

## Prior round

### 1.3 Asks fire once per round, not once per entry - explanation accepted
The reason cites the pipeline rule that a round with a fix holds its asks.
where: steps/resolve.md:62
verdict: skip +reply
reason: accepted as written

## Categories

- correctness and logic: 1
- architecture and guidelines: none found
- security and performance: none found
- tests and docs: 2

## Findings

### 2.1 A second review on an unsettled round burns the last round
summary: the round number comes from the file count
where: steps/review.md:128
category: correctness and logic
Round two exists so fixes get read. Fix: decline.
verdict: fix
reason: the Rounds section now declines

### 2.2 No test for the decline
where: steps/review.md:130
category: tests and docs
`

func TestParse(t *testing.T) {
	f, err := Parse(roundTwo)
	if err != nil {
		t.Fatal(err)
	}

	if f.Task != "GH-13" || f.Round != "2" || f.Reviewed != "e6af491 dirty" || f.Reviewer != "local" ||
		f.Verdict != "findings" {
		t.Errorf("header %+v", f)
	}

	wantCats := map[string]int{
		"correctness and logic":       1,
		"architecture and guidelines": 0,
		"security and performance":    0,
		"tests and docs":              2,
	}
	if !reflect.DeepEqual(f.Categories, wantCats) {
		t.Errorf("categories %v", f.Categories)
	}

	want := []Finding{
		{
			ID:      "1.3",
			Title:   "Asks fire once per round, not once per entry",
			Prior:   true,
			Status:  "explanation accepted",
			Verdict: "skip +reply",
			Reason:  "accepted as written",
		},
		{
			ID:       "2.1",
			Title:    "A second review on an unsettled round burns the last round",
			Category: "correctness and logic",
			Verdict:  "fix",
			Reason:   "the Rounds section now declines",
		},
		{ID: "2.2", Title: "No test for the decline", Category: "tests and docs"},
	}
	if !reflect.DeepEqual(f.Findings, want) {
		t.Errorf("findings\n got %+v\nwant %+v", f.Findings, want)
	}
}

// A file both halves wrote tags each block with its source; an untagged
// block in it is the local reviewer's.
func TestParseBothHalves(t *testing.T) {
	f, err := Parse("task: GH-13\nround: 1\nreviewer: local, claude[bot]\nreview: https://x/1\nverdict: findings\n\n" +
		"## Findings\n\n### 1.1 Local one\nwhere: a.go:1\ncategory: tests and docs\nsource: local\n\n" +
		"### 1.2 Remote one\nwhere: b.go:2\ncategory: tests and docs\nsource: Remote\n\n" +
		"### 1.3 Untagged\nwhere: c.go:3\ncategory: tests and docs\n")
	if err != nil {
		t.Fatal(err)
	}

	if local, remote := f.Halves(); !local || !remote {
		t.Errorf("halves = %v, %v", local, remote)
	}

	for i, want := range []bool{false, true, false} {
		if got := f.Remote(f.Findings[i]); got != want {
			t.Errorf("%s remote = %v, want %v", f.Findings[i].ID, got, want)
		}
	}

	remoteOnly := &File{Reviewer: "claude[bot]"}
	if !remoteOnly.Remote(Finding{}) {
		t.Error("an untagged block in a remote reviewer's file is remote")
	}

	if local, remote := (&File{}).Halves(); local || remote {
		t.Error("no reviewer names no half")
	}
}

func TestParseWithoutHeader(t *testing.T) {
	if _, err := Parse("# findings\n"); !errors.Is(err, ErrNoHeader) {
		t.Errorf("err = %v", err)
	}
}
