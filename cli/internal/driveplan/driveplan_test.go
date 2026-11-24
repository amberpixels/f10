package driveplan_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/driveplan"
)

func small(id, area string, lines int) driveplan.Task {
	return driveplan.Task{ID: id, Areas: []string{area}, Lines: lines}
}

// fixture is the parent the task describes: three small tasks in one area,
// three independent areas, two After: one of those, a spike, a migration.
func fixture() []driveplan.Task {
	t47, t48 := small("FT-47", "status", 40), small("FT-48", "status", 30)
	t47.Base, t48.Base = "FT-44", "FT-44"

	spike := small("FT-49", "drive", 0)
	spike.Kind = driveplan.KindSpike

	migration := small("FT-50", "ledger", 50)
	migration.Risky = "migration"

	return []driveplan.Task{
		small("FT-41", "ui", 20), small("FT-42", "ui", 20), small("FT-43", "ui", 20),
		small("FT-44", "ledger", 80), small("FT-45", "forge", 80), small("FT-46", "docs", 80),
		t47, t48, spike, migration,
	}
}

func propose(t *testing.T, tasks ...driveplan.Task) driveplan.Plan {
	t.Helper()

	p, err := driveplan.Propose(driveplan.Input{Tasks: tasks, MaxLines: 300, Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func entries(p driveplan.Plan) map[string]driveplan.Entry {
	out := map[string]driveplan.Entry{}

	for _, st := range p.Levels {
		for _, e := range st.Entries {
			out[e.Token()] = e
		}
	}

	return out
}

func stageOf(p driveplan.Plan, token string) int {
	for n, st := range p.Levels {
		if slices.ContainsFunc(st.Entries, func(e driveplan.Entry) bool { return e.Token() == token }) {
			return n + 1
		}
	}

	return 0
}

func TestFixtureParent(t *testing.T) {
	t.Parallel()

	p := propose(t, fixture()...)
	got := entries(p)

	for token, stage := range map[string]int{
		"FT-41+FT-42+FT-43": 1, "FT-44": 1, "FT-45": 1, "FT-46": 1,
		"FT-47+FT-48": 2, "FT-49": 1, "FT-50": 1,
	} {
		if stageOf(p, token) != stage {
			t.Errorf("%s: stage %d, want %d (stages %+v)", token, stageOf(p, token), stage, p.Levels)
		}
	}

	if r := got["FT-49"].Reason; r != "alone: spike, it merges nothing" {
		t.Errorf("spike reason %q", r)
	}

	if r := got["FT-50"].Reason; r != "alone: migration" {
		t.Errorf("migration reason %q", r)
	}

	if e := got["FT-47+FT-48"]; e.Base != "FT-44" || !strings.Contains(e.Reason, "After: FT-44") {
		t.Errorf("FT-47+FT-48 = %+v, want After: FT-44", e)
	}

	if p.Waits != 3 || p.Finishes != 3 || p.Parallel != 4 {
		t.Errorf("saving %d waits %d finishes, parallel %d", p.Waits, p.Finishes, p.Parallel)
	}

	var out bytes.Buffer
	if err := p.Write(&out, nil); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"level 1", "level 2", "f10 start FT-41+FT-42+FT-43", "saves about 3 CI waits and 3 finishes", driveplan.Tradeoff} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestSameFileBundles(t *testing.T) {
	t.Parallel()

	a, b := small("FT-1", "ui", 20), small("FT-2", "docs", 20)
	a.Files, b.Files = []string{"cli/x.go"}, []string{"cli/x.go"}

	got := entries(propose(t, a, b))
	if e, ok := got["FT-1+FT-2"]; !ok || !strings.Contains(e.Reason, "same files (cli/x.go)") {
		t.Errorf("want FT-1+FT-2 on the shared file, got %+v", got)
	}
}

func TestSplitOverTheCap(t *testing.T) {
	t.Parallel()

	p := propose(t,
		small("FT-1", "ui", 150), small("FT-2", "ui", 120), small("FT-3", "ui", 100),
	)
	got := entries(p)

	if _, ok := got["FT-1+FT-2"]; !ok {
		t.Fatalf("want FT-1+FT-2 under the cap, got %+v", got)
	}

	if r := got["FT-3"].Reason; !strings.HasPrefix(r, "split: 370 lines") || !strings.Contains(r, "cap of 300") {
		t.Errorf("FT-3 reason %q", r)
	}

	big := small("FT-4", "ui", 400)
	if r := entries(propose(t, big))["FT-4"].Reason; r != "alone: 400 lines, over the cap of 300" {
		t.Errorf("FT-4 reason %q", r)
	}
}

func TestBugNeverWithFeature(t *testing.T) {
	t.Parallel()

	bug := small("FT-2", "ui", 20)
	bug.Kind = driveplan.KindBug

	got := entries(propose(t, small("FT-1", "ui", 20), bug, small("FT-3", "ui", 20)))
	if _, ok := got["FT-1+FT-3"]; !ok {
		t.Errorf("want the features bundled, got %+v", got)
	}

	if r := got["FT-2"].Reason; r != "alone: a bug fix keeps its own PR" {
		t.Errorf("bug reason %q", r)
	}
}

func TestOtherFlowAlone(t *testing.T) {
	t.Parallel()

	docs := small("FT-2", "ui", 20)
	docs.Flow = "prose"

	got := entries(propose(t, small("FT-1", "ui", 20), docs, small("FT-3", "ui", 20)))
	if _, ok := got["FT-1+FT-3"]; !ok {
		t.Errorf("want FT-1+FT-3, got %+v", got)
	}

	if r := got["FT-2"].Reason; !strings.HasPrefix(r, "alone: other flow") {
		t.Errorf("FT-2 reason %q", r)
	}
}

func TestProposedOrders(t *testing.T) {
	t.Parallel()

	builds := small("FT-2", "status", 20)
	builds.BuildsOn = "FT-1"

	// a bug and a feature on one file cannot bundle, and side by side they conflict
	bug, feat := small("FT-3", "forge", 20), small("FT-4", "forge", 20)
	bug.Kind = driveplan.KindBug
	bug.Files, feat.Files = []string{"forge.go"}, []string{"forge.go"}

	p := propose(t, small("FT-1", "ledger", 50), builds, bug, feat)

	want := []driveplan.Edit{
		{Task: "FT-2", After: "FT-1", Reason: "builds on FT-1's code"},
		{Task: "FT-4", After: "FT-3", Reason: "shares forge.go with FT-3: run side by side they would conflict"},
	}
	if !slices.Equal(p.Edits, want) {
		t.Errorf("edits %+v, want %+v", p.Edits, want)
	}

	if stageOf(p, "FT-2") != 2 || stageOf(p, "FT-4") != 2 {
		t.Errorf("stages %+v", p.Levels)
	}

	// an order the user declined is not proposed again
	feat.After = "none"

	p = propose(t, bug, feat)
	if len(p.Edits) != 0 || stageOf(p, "FT-4") != 1 {
		t.Errorf("declined order came back: %+v", p.Edits)
	}
}

func TestOverrides(t *testing.T) {
	t.Parallel()

	kept, freed := small("FT-2", "ui", 20), small("FT-3", "ui", 20)
	kept.Alone = true
	freed.Base, freed.After = "FT-9", "none"

	p := propose(t, small("FT-1", "ui", 20), kept, freed)
	got := entries(p)

	if _, ok := got["FT-1+FT-3"]; !ok {
		t.Errorf("want FT-1+FT-3 once FT-3's After: is dropped, got %+v", got)
	}

	if got["FT-2"].Reason != "alone: you kept it alone" {
		t.Errorf("FT-2 reason %q", got["FT-2"].Reason)
	}

	if !slices.Contains(p.Edits, driveplan.Edit{Task: "FT-3", After: "none", Reason: "you dropped its order"}) {
		t.Errorf("edits %+v", p.Edits)
	}

	with := small("FT-5", "docs", 20)
	with.With = "FT-4"

	if _, ok := entries(propose(t, small("FT-4", "ledger", 20), with))["FT-4+FT-5"]; !ok {
		t.Error("want the user's bundle FT-4+FT-5")
	}
}

func TestNotSizedStaysAlone(t *testing.T) {
	t.Parallel()

	p := propose(t, small("FT-1", "ui", 0), small("FT-2", "ui", 0))
	if r := entries(p)["FT-1"].Reason; r != "alone: not sized" || p.Saving() != "" || p.Hint() != "" {
		t.Errorf("reason %q, saving %q", r, p.Saving())
	}
}

func TestCycleRefused(t *testing.T) {
	t.Parallel()

	a, b := small("FT-1", "ui", 20), small("FT-2", "docs", 20)
	a.Base, b.Base = "FT-2", "FT-1"

	if _, err := driveplan.Propose(driveplan.Input{Tasks: []driveplan.Task{a, b}, MaxLines: 300}); err == nil ||
		!strings.Contains(err.Error(), "circle") {
		t.Errorf("want a cycle error, got %v", err)
	}
}

func TestProfileOnTheEntry(t *testing.T) {
	t.Parallel()

	a, b := small("FT-1", "ui", 20), small("FT-2", "ui", 20)
	a.Profile, b.Profile = "opus", "opus"

	p := propose(t, a, b)
	if !slices.Equal(p.Args, []string{"FT-1+FT-2:opus"}) {
		t.Errorf("args %v", p.Args)
	}
}

// a pair the user named passes the cap, but the tasks the rules join to it do not
func TestNamedPairKeepsTheCapForOthers(t *testing.T) {
	t.Parallel()

	c := small("FT-3", "docs", 20)
	c.With = "FT-1"

	p := propose(t, small("FT-1", "ui", 200), small("FT-2", "ui", 200), c)
	got := entries(p)

	if _, ok := got["FT-1+FT-3"]; !ok {
		t.Fatalf("want the named pair FT-1+FT-3, got %+v", got)
	}

	if r := got["FT-2"].Reason; !strings.HasPrefix(r, "split:") {
		t.Errorf("FT-2 rode in uncapped, reason %q", r)
	}

	// the user's own bundle is no saving a hint may offer
	if p.Formed.Waits != 0 || p.Hint() != "" {
		t.Errorf("formed %+v, hint %q", p.Formed, p.Hint())
	}
}
