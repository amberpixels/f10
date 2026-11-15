package history

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
)

var window = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func numbered(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}

	return b.String()
}

func neighbors(t *testing.T, r gitxtest.Repo, skip string, paths ...string) []Touch {
	t.Helper()

	got, err := Neighbors(t.Context(), r.Dir, "FT", paths, window, skip)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func ids(ts []Touch) []string {
	var out []string
	for _, tc := range ts {
		out = append(out, tc.Task)
	}

	return out
}

// Two tasks whose lines a third rewrote are stale: old pain on code that is
// gone does not apply.
func TestNeighborsDropRewrittenWork(t *testing.T) {
	r := gitxtest.Init(t, t.TempDir(), "main")
	r.Commit("2026-10-01T10:00:00Z", "b.go", numbered(1, 10), "first\n\nTask: FT-1")
	r.Commit("2026-10-02T10:00:00Z", "b.go", numbered(1, 20), "second\n\nTask: FT-2")
	r.Commit(
		"2026-10-03T10:00:00Z",
		"b.go",
		"line 1\n"+strings.ReplaceAll(numbered(2, 20), "line", "row"),
		"rewrite\n\nTask: FT-3",
	)

	b := Build(Input{Paths: []string{"b.go"}, Since: window, Touches: neighbors(t, r, "", "b.go")})

	if got := ids(b.Stale); !slices.Equal(got, []string{"FT-1", "FT-2"}) {
		t.Errorf("stale = %v, want FT-1 and FT-2", got)
	}

	if len(b.Neighbors) != 1 || b.Neighbors[0].Task != "FT-3" {
		t.Errorf("live = %+v, want FT-3 alone", b.Neighbors)
	}
}

// A bundle counts once under its lead, a Change: trailer names its task, a
// subject that opens with an id stands in for a missing trailer, and the
// task being planned is never its own neighbor.
func TestNeighborsByTrailerAndSubject(t *testing.T) {
	r := gitxtest.Init(t, t.TempDir(), "main")
	r.Commit("2026-10-01T10:00:00Z", "a.go", numbered(1, 5), "bundle\n\nTask: FT-4\nTask: FT-5")
	r.Commit("2026-10-02T10:00:00Z", "a.go", numbered(1, 10), "follow-up\n\nChange: FT-6.2")
	r.Commit("2026-10-03T10:00:00Z", "a.go", numbered(1, 15), "FT-7 no trailer")
	r.Commit("2026-10-04T10:00:00Z", "a.go", numbered(1, 20), "mine\n\nTask: FT-9")
	r.Commit("2026-10-05T10:00:00Z", "a.go", numbered(1, 25), "untracked")

	if got := ids(neighbors(t, r, "ft-9", "a.go")); !slices.Equal(got, []string{"FT-4", "FT-6", "FT-7"}) {
		t.Errorf("neighbors = %v, want FT-4, FT-6, FT-7", got)
	}
}

func TestNeighborsKeepTheWindow(t *testing.T) {
	r := gitxtest.Init(t, t.TempDir(), "main")
	r.Commit("2025-06-01T10:00:00Z", "a.go", numbered(1, 5), "old\n\nTask: FT-1")
	r.Commit("2026-10-01T10:00:00Z", "a.go", numbered(1, 10), "new\n\nTask: FT-2")

	if got := ids(neighbors(t, r, "", "a.go")); !slices.Equal(got, []string{"FT-2"}) {
		t.Errorf("neighbors = %v, want FT-2 alone", got)
	}
}

func live(tasks ...string) []Touch {
	var out []Touch
	for _, id := range tasks {
		out = append(out, Touch{Task: id, Added: 10, Standing: 10})
	}

	return out
}

func ev(at time.Time, task, kind string, p any) ledger.Event {
	e := ledger.MustEvent(kind, p)
	e.At, e.Session, e.Task = at, "s-"+task, task

	return e
}

// reviewed is a task's round opening with the categories it raised and the
// verdicts resolve recorded for them.
func reviewed(at time.Time, task string, rounds int, cats map[string]int32, verdicts ...string) []ledger.Event {
	var out []ledger.Event
	for r := 1; r <= rounds; r++ {
		out = append(out, ev(at, task, ledger.ReviewRoundOpened,
			&ledger.ReviewRoundOpenedPayload{Round: strconv.Itoa(r), Categories: cats}))
	}

	for i, v := range verdicts {
		cat, word, _ := strings.Cut(v, "=")
		out = append(out, ev(at, task, ledger.FindingVerdict,
			&ledger.FindingVerdictPayload{Round: "1", Finding: strconv.Itoa(i + 1), Category: cat, Verdict: word}))
	}

	return out
}

// Three tasks fixing the same tests-and-docs gap name the category with
// their ids; the statistics need three neighbors.
func TestRecurringFindingNamesItsTasks(t *testing.T) {
	at := window.Add(24 * time.Hour)
	docs := map[string]int32{"tests and docs": 1}

	var events []ledger.Event
	for i, id := range []string{"FT-1", "FT-2", "FT-3"} {
		events = append(events, reviewed(at, id, i+2, docs, "tests and docs=fix")...)
	}

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: live("FT-1", "FT-2", "FT-3"), Events: events})
	text := strings.Join(b.Lines(), "\n")

	for _, want := range []string{
		"recurring finding: tests and docs in 3 of 3 reviewed (FT-1, FT-2, FT-3), 3 fixed, 0 skipped",
		"median review rounds: 3 (FT-1 2, FT-2 3, FT-3 4)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestTwoNeighborsGetNoStatistic(t *testing.T) {
	at := window.Add(24 * time.Hour)
	docs := map[string]int32{"tests and docs": 1}

	events := append(reviewed(at, "FT-1", 3, docs, "tests and docs=fix"), reviewed(at, "FT-2", 3, docs)...)
	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: live("FT-1", "FT-2"), Events: events})

	if b.Aggregates != nil {
		t.Errorf("aggregates = %+v, want none under %d neighbors", b.Aggregates, MinSample)
	}

	text := strings.Join(b.Lines(), "\n")
	for _, banned := range []string{"median", "recurring"} {
		if strings.Contains(text, banned) {
			t.Errorf("printed %q with two neighbors:\n%s", banned, text)
		}
	}

	if !strings.Contains(text, "FT-1:") || !strings.Contains(text, "FT-2:") {
		t.Errorf("the two neighbors are not listed:\n%s", text)
	}
}

// A bug traced to a neighbor and one traced to code on the paths both
// count; one elsewhere does not, nor one found before the window.
func TestRegressionsOnThePathsOrFromANeighbor(t *testing.T) {
	at := window.Add(24 * time.Hour)
	found := func(at time.Time, bug, origin string, lines ...string) ledger.Event {
		return ev(at, bug, ledger.RegressionFound, &ledger.RegressionFoundPayload{OriginTask: origin, Lines: lines})
	}

	events := []ledger.Event{
		found(at, "FT-20", "FT-1", "z.go:1-2"),
		found(at, "FT-21", "", "a.go:3-4"),
		found(at, "FT-22", "FT-99", "z.go:1-2"),
		found(window.Add(-time.Hour), "FT-23", "FT-1", "a.go:1-1"),
	}

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: live("FT-1"), Events: events})

	var bugs []string
	for _, r := range b.Regressions {
		bugs = append(bugs, r.Bug)
	}

	if !slices.Equal(bugs, []string{"FT-20", "FT-21"}) {
		t.Errorf("regressions = %v, want FT-20 and FT-21", bugs)
	}

	if got := b.Neighbors[0].Regressions; !slices.Equal(got, []string{"FT-20"}) {
		t.Errorf("FT-1 shipped %v, want FT-20", got)
	}
}

func TestNoHistory(t *testing.T) {
	r := gitxtest.Init(t, t.TempDir(), "main")
	r.Commit("2026-10-01T10:00:00Z", "a.go", "x\n", "untracked")

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: neighbors(t, r, "", "a.go")})
	if got := b.Lines(); !slices.Equal(got, []string{NoHistory}) {
		t.Errorf("lines = %q, want %q", got, NoHistory)
	}
}

// The text never passes the cap, and every line cites an id.
func TestTextIsCapped(t *testing.T) {
	var tasks []string
	for i := 1; i <= 30; i++ {
		tasks = append(tasks, fmt.Sprintf("FT-%d", i))
	}

	decision := lore.Entry{
		ID: "01DECISION", Kind: lore.KindDecision, Change: "FT-1", Chosen: "a cache",
		Rejected: []lore.Rejected{{Option: "no cache", Reason: "slow"}}, Anchors: []lore.Anchor{{Path: "a.go"}},
	}

	b := Build(
		Input{Paths: []string{"a.go"}, Since: window, Touches: live(tasks...), Decisions: []lore.Entry{decision}},
	)
	lines := b.Lines()

	if len(lines) != MaxLines {
		t.Fatalf("%d lines, want %d", len(lines), MaxLines)
	}

	for _, l := range lines {
		if !strings.Contains(l, "FT-") && !strings.Contains(l, "01DECISION") {
			t.Errorf("line cites no id: %q", l)
		}
	}

	if !strings.Contains(lines[1], "decision 01DECISION (FT-1, a.go): chose a cache; rejected no cache (slow)") {
		t.Errorf("decision line = %q", lines[1])
	}
}

func TestPlanKind(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("FT-1.md", "# FT-1\n\nRoles: x\nKind: bug\n")
	write("archive/FT-2.1.md", "Kind: refactor\n")
	write("archive/FT-2.2.md", "Kind: spike\n")
	write("archive/FT-3.md", "# no kind line\n")

	for task, want := range map[string]string{"FT-1": "bug", "FT-2": "spike", "FT-3": "feature", "FT-4": ""} {
		if got := PlanKind(dir, task); got != want {
			t.Errorf("PlanKind(%s) = %q, want %q", task, got, want)
		}
	}
}

// A follow-up PR's series restarts at round 1, so rounds are told apart by
// their reviewed sha; neighbors with no review recorded stay out of the
// rates, because they predate the ledger.
func TestStatisticsCountReviewedNeighborsOnly(t *testing.T) {
	at := window.Add(24 * time.Hour)
	opened := func(task, round, sha string) ledger.Event {
		return ev(at, task, ledger.ReviewRoundOpened, &ledger.ReviewRoundOpenedPayload{
			Round: round, Reviewed: sha, Categories: map[string]int32{"correctness and logic": 1},
		})
	}

	events := []ledger.Event{
		opened("FT-1", "1", "aaa"), opened("FT-1", "2", "bbb"), opened("FT-1", "1", "ccc"),
		opened("FT-2", "1", "ddd"), opened("FT-2", "2", "eee"),
		opened("FT-3", "1", "fff"),
	}

	b := Build(Input{
		Paths: []string{"a.go"}, Since: window, Events: events,
		Touches: live("FT-1", "FT-2", "FT-3", "FT-4", "FT-5", "FT-6", "FT-7"),
	})

	if b.Neighbors[0].Rounds != 3 {
		t.Errorf("FT-1 rounds = %d, want 3 over two series", b.Neighbors[0].Rounds)
	}

	a := b.Aggregates
	if a == nil || a.N != 3 || a.MedianRounds != 2 {
		t.Fatalf("aggregates = %+v, want 3 reviewed with a median of 2", a)
	}

	if len(a.Recurring) != 1 || !slices.Equal(a.Recurring[0].Tasks, []string{"FT-1", "FT-2", "FT-3"}) {
		t.Errorf("recurring = %+v, want correctness and logic in all three reviewed", a.Recurring)
	}
}

func TestDecisionCarriesItsAnchors(t *testing.T) {
	d := lore.Entry{
		ID: "01D", Kind: lore.KindDecision, Change: "FT-1", Chosen: "x",
		Anchors: []lore.Anchor{{Path: "a.go", Line: 3, EndLine: 9, Commit: "abcdef1"}, {Path: "z.go", Line: 1}},
	}

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Decisions: []lore.Entry{d}})
	if got := b.Decisions[0].Anchors; !slices.Equal(got, []string{"a.go:3-9@abcdef1"}) {
		t.Errorf("anchors = %v, want the one on a.go", got)
	}
}

// A follow-up PR's series restarts its finding ids, so its 1.1 is a finding
// of its own, not a rewrite of the first PR's.
func TestFollowUpVerdictsKeepTheFirstSeries(t *testing.T) {
	at := window.Add(24 * time.Hour)
	opened := func(sha string) ledger.Event {
		return ev(at, "FT-1", ledger.ReviewRoundOpened, &ledger.ReviewRoundOpenedPayload{Round: "1", Reviewed: sha})
	}
	verdict := func(cat, word string) ledger.Event {
		return ev(at, "FT-1", ledger.FindingVerdict,
			&ledger.FindingVerdictPayload{Round: "1", Finding: "1.1", Category: cat, Verdict: word})
	}

	events := []ledger.Event{
		opened("aaa"), verdict("tests and docs", "fix"),
		opened("bbb"), verdict("correctness and logic", "skip"),
	}

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: live("FT-1"), Events: events})
	cats := b.Neighbors[0].Categories

	if cats["tests and docs"].Fixed != 1 || cats["correctness and logic"].Skipped != 1 {
		t.Errorf("categories = %+v, want the first PR's fix and the follow-up's skip", cats)
	}
}

func TestCatchupDecisionsAreLeftOut(t *testing.T) {
	anchor := []lore.Anchor{{Path: "a.go", Line: 1}}
	design := lore.Entry{
		ID:      "01A",
		Kind:    lore.KindDecision,
		Change:  "FT-1",
		Chosen:  "x",
		Anchors: anchor,
		Body:    "why x",
	}
	merge := lore.Entry{
		ID: "01B", Kind: lore.KindDecision, Change: "FT-2", Chosen: "theirs", Anchors: anchor,
		Body: "catchup: a.go\n\nkept main's side",
	}

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Decisions: []lore.Entry{design, merge}})
	if len(b.Decisions) != 1 || b.Decisions[0].ID != "01A" {
		t.Errorf("decisions = %+v, want the design decision alone", b.Decisions)
	}
}

// TaskFacts counts tasks that touch no path the same way Build counts its
// neighbors, and keeps each halt's reason.
func TestTaskFactsMatchesTheBrief(t *testing.T) {
	at := window.Add(24 * time.Hour)
	events := reviewed(at, "FT-1", 2, map[string]int32{"tests and docs": 1}, "tests and docs=fix")
	events = append(
		events,
		ev(at, "FT-1", ledger.TaskHalted, &ledger.TaskHaltedPayload{Skill: "ship", Reason: "catchup conflict"}),
	)

	b := Build(Input{Paths: []string{"a.go"}, Since: window, Touches: live("FT-1"), Events: events})
	f := TaskFacts(events, []string{"ft-1"}, window)["FT-1"]

	if f.Rounds != b.Neighbors[0].Rounds ||
		f.Categories["tests and docs"] != b.Neighbors[0].Categories["tests and docs"] {
		t.Errorf("TaskFacts = %+v, brief = %+v", *f, b.Neighbors[0].Facts)
	}

	if !slices.Equal(f.HaltReasons, []string{"catchup conflict"}) || f.Halts != 1 {
		t.Errorf("halts = %d %v, want one with its reason", f.Halts, f.HaltReasons)
	}
}
