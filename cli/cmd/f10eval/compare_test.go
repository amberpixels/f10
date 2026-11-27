package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSet(t *testing.T, reps ...Report) string {
	t.Helper()

	dir := t.TempDir()

	for i, r := range reps {
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(
			filepath.Join(dir, r.Scenario+"."+string(rune('a'+i))+".json"),
			body,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func planted(found, outside int, cost float64) Report {
	return Report{
		Scenario:   "review",
		Commit:     "abc",
		Pass:       true,
		CostUSD:    cost,
		DurationMS: 60000,
		Checks: []Check{
			{Name: "planted", Pass: true, Planted: &PlantedCount{Found: found, Total: 4, Outside: outside}},
		},
	}
}

func failed(r Report) Report {
	r.Pass = false
	r.Checks = []Check{r.Checks[0]}
	r.Checks[0].Pass = false

	return r
}

func compareOut(t *testing.T, base, variant string) string {
	t.Helper()

	var b bytes.Buffer
	if err := compareMain(&b, "/", []string{base, variant}); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func row(out, name string) string {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), name+" ") {
			return l
		}
	}

	return ""
}

func TestCompareMarksOnlyBeyondTheSpread(t *testing.T) {
	t.Parallel()

	base := writeSet(t, planted(3, 1, 1), planted(2, 1, 1.2), planted(3, 2, 1.1))
	overlap := writeSet(t, planted(3, 0, 1.15), planted(4, 2, 1.0), planted(4, 1, 1.3))
	apart := writeSet(t, planted(4, 3, 0.5), planted(4, 4, 0.6), planted(4, 3, 0.4))

	out := compareOut(t, base, overlap)
	for _, m := range []string{"planted found", "planted outside", "cost"} {
		if r := row(out, m); strings.Contains(r, "better") || strings.Contains(r, "worse") {
			t.Errorf("overlapping ranges marked: %q", r)
		}
	}

	out = compareOut(t, base, apart)
	for m, want := range map[string]string{"planted found": "better", "planted outside": "worse", "cost": "better"} {
		if r := row(out, m); !strings.HasSuffix(strings.TrimSpace(r), want) {
			t.Errorf("%s row %q, want %s", m, r, want)
		}
	}

	if r := row(out, "planted found"); !strings.Contains(r, "2.67 [2-3] n=3") || !strings.Contains(r, "4 [4-4] n=3") {
		t.Errorf("found row %q", r)
	}
}

func TestComparePassRate(t *testing.T) {
	t.Parallel()

	p := planted(4, 0, 1)
	five := writeSet(t, p, p, p, p, p)
	two := writeSet(t, p, p, failed(p), failed(p), failed(p))
	four := writeSet(t, p, p, p, p, failed(p))

	if r := row(
		compareOut(t, five, two),
		"pass",
	); !strings.Contains(r, "5/5") || !strings.Contains(r, "2/5") ||
		!strings.HasSuffix(strings.TrimSpace(r), "worse") {
		t.Errorf("5/5 against 2/5: %q", r)
	}

	if r := row(compareOut(t, five, four), "pass"); strings.Contains(r, "worse") {
		t.Errorf("5/5 against 4/5 marked: %q", r)
	}

	if r := row(compareOut(t, five, four), "check planted"); !strings.Contains(r, "4/5") {
		t.Errorf("check row %q", r)
	}
}

func TestCompareErroredRunsSampleNothingElse(t *testing.T) {
	t.Parallel()

	p := planted(4, 0, 1)
	setup := Report{Scenario: "review", Commit: "abc", Error: "git checkout: exit 1"}
	base := writeSet(t, p, p, p)
	variant := writeSet(t, p, p, setup)

	out := compareOut(t, base, variant)

	if r := row(out, "errors"); !strings.Contains(r, "0/3") || !strings.Contains(r, "1/3") {
		t.Errorf("errors row %q", r)
	}

	if r := row(out, "cost"); !strings.Contains(r, "$1.00 [$1.00-$1.00] n=2") {
		t.Errorf("cost row counts the errored run: %q", r)
	}

	if r := row(out, "pass"); !strings.Contains(r, "2/2") {
		t.Errorf("pass row counts the errored run: %q", r)
	}
}

func TestCompareCountsATurnErrorEverywhere(t *testing.T) {
	t.Parallel()

	p := planted(4, 0, 1)
	spent := failed(planted(2, 0, 5))
	spent.Error = "turn 1: budget"

	out := compareOut(t, writeSet(t, p, p, p), writeSet(t, p, p, spent))

	for name, want := range map[string]string{"errors": "1/3", "pass": "2/3", "cost": "n=3", "planted found": "n=3"} {
		if r := row(out, name); !strings.Contains(r, want) {
			t.Errorf("%s row %q lacks %q", name, r, want)
		}
	}
}

func TestCompareListsUnmatchedGroupsAndWarns(t *testing.T) {
	t.Parallel()

	other := planted(4, 0, 1)
	other.Scenario, other.Commit = "feature", "def"
	high := planted(4, 0, 1)
	high.Agent = "opus:high"

	base := writeSet(t, planted(4, 0, 1), other)
	variant := writeSet(t, planted(4, 0, 1), high)

	out := compareOut(t, base, variant)

	for _, want := range []string{
		"not compared: feature: baseline only",
		"not compared: review (opus:high): variant only",
		"holds reports from 2 commits",
		"2 of 2 groups have one run, so none of their rows is marked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCompareMarksNothingOnOneRun(t *testing.T) {
	t.Parallel()

	out := compareOut(t, writeSet(t, planted(1, 0, 1)), writeSet(t, planted(4, 3, 3)))
	if strings.Contains(out, "better") || strings.Contains(out, "worse") {
		t.Errorf("one run per side marked:\n%s", out)
	}
}

func TestCompareNeedsTwoDirs(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := compareMain(&b, "/", []string{"one"}); err == nil {
		t.Error("one dir accepted")
	}

	if err := compareMain(&b, "/", []string{t.TempDir(), t.TempDir()}); err == nil {
		t.Error("an empty dir accepted")
	}
}

func withPlumbing(r Report, calls int, seconds, cost float64) Report {
	r.Plumbing = &Plumbing{Calls: calls, TotalCalls: 40, Seconds: seconds, CostUSD: cost}

	return r
}

// Less plumbing is better, and a baseline from before the field prints "-"
// with no mark.
func TestComparePlumbingRows(t *testing.T) {
	t.Parallel()

	p := planted(4, 0, 1)
	base := writeSet(t, withPlumbing(p, 12, 40, 0.4), withPlumbing(p, 14, 45, 0.5))
	variant := writeSet(t, withPlumbing(p, 6, 20, 0.2), withPlumbing(p, 7, 22, 0.25))

	out := compareOut(t, base, variant)

	for name, want := range map[string]string{
		"plumbing calls": "13 [12-14] n=2",
		"plumbing time":  "42s [40s-45s] n=2",
		"plumbing cost":  "$0.45 [$0.40-$0.50] n=2",
	} {
		if r := row(out, name); !strings.Contains(r, want) || !strings.HasSuffix(strings.TrimSpace(r), "better") {
			t.Errorf("%s row %q, want %q marked better", name, r, want)
		}
	}

	out = compareOut(t, writeSet(t, p, p), variant)
	if r := row(out, "plumbing calls"); !strings.Contains(r, " - ") || strings.Contains(r, "better") {
		t.Errorf("an old baseline's plumbing row %q, want - and no mark", r)
	}
}
