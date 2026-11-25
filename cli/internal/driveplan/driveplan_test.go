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

	want44 := driveplan.Base{ID: "FT-44", Reason: "its After: line"}
	if e := got["FT-47+FT-48"]; len(e.Bases) != 1 || e.Bases[0] != want44 {
		t.Errorf("FT-47+FT-48 = %+v, want one base FT-44", e)
	}

	if p.Waits != 3 || p.Finishes != 3 || p.Parallel != 4 {
		t.Errorf("saving %d waits %d finishes, parallel %d", p.Waits, p.Finishes, p.Parallel)
	}

	var out bytes.Buffer
	if err := p.Write(&out, nil); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"level 1", "level 2", "f10 start FT-41+FT-42+FT-43", "FT-44,FT-47+FT-48", "FT-47+FT-48 after FT-44", "saves about 3 CI waits and 3 finishes", driveplan.Tradeoff} {
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
	builds.BuildsOn = []string{"FT-1"}

	// a bug and a feature on one file cannot bundle, and side by side they conflict
	bug, feat := small("FT-3", "forge", 20), small("FT-4", "forge", 20)
	bug.Kind = driveplan.KindBug
	bug.Files, feat.Files = []string{"forge.go"}, []string{"forge.go"}

	p := propose(t, small("FT-1", "ledger", 50), builds, bug, feat)
	got := entries(p)

	if b := got["FT-2"].Bases; !slices.Equal(b, []driveplan.Base{{ID: "FT-1", Reason: "builds on FT-1's code"}}) {
		t.Errorf("FT-2 bases %+v", b)
	}

	want := driveplan.Base{ID: "FT-3", Reason: "shares forge.go with FT-3: run side by side they would conflict"}
	if b := got["FT-4"].Bases; !slices.Equal(b, []driveplan.Base{want}) {
		t.Errorf("FT-4 bases %+v", b)
	}

	if stageOf(p, "FT-2") != 2 || stageOf(p, "FT-4") != 2 {
		t.Errorf("stages %+v", p.Levels)
	}

	if !slices.Equal(p.Args, []string{"FT-1,FT-2", "FT-3,FT-4", "--parallel", "2"}) {
		t.Errorf("args %v", p.Args)
	}

	// an order the user declined is not proposed again
	feat.After = []string{driveplan.NoOrder}

	p = propose(t, bug, feat)
	if len(entries(p)["FT-4"].Bases) != 0 || stageOf(p, "FT-4") != 1 {
		t.Errorf("declined order came back: %+v", p.Levels)
	}
}

// order is the command's entries, the --parallel flag cut
func order(p driveplan.Plan) []string {
	if k := slices.Index(p.Args, "--parallel"); k >= 0 {
		return p.Args[:k]
	}

	return p.Args
}

func proposeJoins(t *testing.T, tasks ...driveplan.Task) driveplan.Plan {
	t.Helper()

	p, err := driveplan.Propose(driveplan.Input{Tasks: tasks, MaxLines: 300, Joins: true})
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// alone keeps a task out of every bundle, so the order alone is tested
func alone(id string, files ...string) driveplan.Task {
	t := small(id, id, 20)
	t.Alone, t.Files = true, files

	return t
}

func TestSeveralBases(t *testing.T) {
	t.Parallel()

	// two tasks it builds on make a join
	c := alone("FT-3")
	c.BuildsOn = []string{"FT-1", "FT-2"}

	p := proposeJoins(t, alone("FT-1"), alone("FT-2"), c)
	if !slices.Equal(order(p), []string{"FT-1", "FT-2", "FT-3@FT-1@FT-2"}) || stageOf(p, "FT-3") != 2 {
		t.Errorf("join: args %v, levels %+v", order(p), p.Levels)
	}

	// a body base and a shared file make a join, the file's base second
	d := alone("FT-4", "x.go")
	d.Base = "FT-1"

	p = proposeJoins(t, alone("FT-1"), alone("FT-2", "x.go"), d)
	if !slices.Equal(order(p), []string{"FT-1", "FT-2", "FT-4@FT-1@FT-2"}) {
		t.Errorf("body and file: args %v", order(p))
	}

	// three on one file stay a chain: the nearest base reaches the farther
	p = proposeJoins(t, alone("FT-1", "x.go"), alone("FT-2", "x.go"), alone("FT-3", "x.go"))
	if !slices.Equal(order(p), []string{"FT-1,FT-2,FT-3"}) {
		t.Errorf("chain: args %v", order(p))
	}

	// a shared file with an entry that already waits on this one adds nothing
	e := alone("FT-5", "y.go")
	e.Base = "FT-6"

	p = proposeJoins(t, e, alone("FT-6", "y.go"))
	if !slices.Equal(order(p), []string{"FT-6,FT-5"}) {
		t.Errorf("reverse: args %v", order(p))
	}

	// the user's bases replace the rest
	f := alone("FT-7", "x.go")
	f.Base, f.After = "FT-9", []string{"FT-1", "FT-2"}

	p = proposeJoins(t, alone("FT-1", "x.go"), alone("FT-2"), f)
	if !slices.Equal(order(p), []string{"FT-1", "FT-2", "FT-7@FT-1@FT-2"}) {
		t.Errorf("user's bases: args %v", order(p))
	}
}

func TestNoJoinWithoutFinish(t *testing.T) {
	t.Parallel()

	c := alone("FT-3")
	c.BuildsOn = []string{"FT-1", "FT-2"}

	p := propose(t, alone("FT-1"), alone("FT-2"), c)
	if !slices.Contains(p.Args, "FT-1,FT-3") || stageOf(p, "FT-3") != 2 {
		t.Errorf("args %v", p.Args)
	}

	b := entries(p)["FT-3"].Bases
	if len(b) != 2 || b[0].Dropped || !b[1].Dropped {
		t.Fatalf("bases %+v, want FT-1 kept and FT-2 dropped", b)
	}

	var out bytes.Buffer
	if err := p.Write(&out, nil); err != nil {
		t.Fatal(err)
	}

	if text := out.String(); !strings.Contains(text, "FT-3 not after FT-2") ||
		!strings.Contains(text, "a join needs a flow that finishes") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestCommandSpelling(t *testing.T) {
	t.Parallel()

	// two dependents on one base: the first chains, the second names it
	b, c := alone("FT-2"), alone("FT-3")
	b.Base, c.Base = "FT-1", "FT-1"

	if p := proposeJoins(t, alone("FT-1"), b, c); !slices.Equal(order(p), []string{"FT-1,FT-2", "FT-3@FT-1"}) {
		t.Errorf("two dependents: %v", order(p))
	}

	// a base inside a bundle is spelled as the bundle's lead, a profile last
	r1, r2 := small("FT-4", "ui", 20), small("FT-5", "ui", 20)
	d, e := alone("FT-6"), alone("FT-7")
	d.Base, d.Profile = "FT-5", "opus"
	e.Base = "FT-5"

	p := proposeJoins(t, r1, r2, d, e)
	if !slices.Equal(order(p), []string{"FT-4+FT-5,FT-6:opus", "FT-7@FT-4"}) {
		t.Errorf("bundle base: %v", order(p))
	}

	// a base outside the list is spelled as it is
	f := alone("FT-8")
	f.Base = "FT-99"

	if p := proposeJoins(t, f); !slices.Equal(order(p), []string{"FT-8@FT-99"}) {
		t.Errorf("outside base: %v", order(p))
	}
}

// a join on an open task outside the list is refused by the start, so the
// open base is kept alone. A finished one is no wait, and the join stays.
func TestJoinOnAnOutsideBase(t *testing.T) {
	t.Parallel()

	c := alone("FT-3")
	c.Base, c.BuildsOn = "FT-90", []string{"FT-1"}

	p := proposeJoins(t, alone("FT-1"), c)
	if !slices.Equal(order(p), []string{"FT-1", "FT-3@FT-90"}) {
		t.Errorf("open outside base: %v", order(p))
	}

	if b := entries(p)["FT-3"].Bases; len(b) != 2 || b[0].Dropped || !b[1].Dropped ||
		!strings.Contains(b[1].Reason, "FT-90 is open outside the list") {
		t.Errorf("bases %+v", b)
	}

	finished, err := driveplan.Propose(driveplan.Input{
		Tasks: []driveplan.Task{alone("FT-1"), c}, MaxLines: 300, Joins: true, Finished: []string{"ft-90"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(finished.Args, []string{"FT-1", "FT-3@FT-90@FT-1"}) {
		t.Errorf("finished outside base: %v", finished.Args)
	}
}

// two bases in one bundle are one base, spelled once
func TestBasesInOneBundleCollapse(t *testing.T) {
	t.Parallel()

	d := alone("FT-6")
	d.BuildsOn = []string{"FT-4", "FT-5"}

	p := propose(t, small("FT-4", "ui", 20), small("FT-5", "ui", 20), d)
	if !slices.Equal(order(p), []string{"FT-4+FT-5,FT-6"}) {
		t.Errorf("args %v", order(p))
	}

	if b := entries(p)["FT-6"].Bases; len(b) != 1 || b[0].Dropped {
		t.Errorf("bases %+v, want the bundle once, kept even with no join allowed", b)
	}
}

// a dropped order the body still states is named, since the start reads it
func TestDroppedOrderTheBodyStillStates(t *testing.T) {
	t.Parallel()

	a := alone("FT-3")
	a.Base, a.After = "FT-9", []string{driveplan.NoOrder}

	p := propose(t, a)

	var out bytes.Buffer
	if err := p.Write(&out, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "`f10 task update FT-3 --after none`") {
		t.Errorf("output:\n%s", out.String())
	}

	b := alone("FT-4")
	b.After = []string{driveplan.NoOrder}

	if q := propose(t, b); len(q.Unordered) != 0 {
		t.Errorf("no body base, yet %v", q.Unordered)
	}
}

func TestBundleSplitsOnDifferentBases(t *testing.T) {
	t.Parallel()

	a, b := small("FT-1", "ui", 20), small("FT-2", "ui", 20)
	a.Base, b.Base = "FT-8", "FT-9"

	got := entries(proposeJoins(t, a, b))
	if _, ok := got["FT-1+FT-2"]; ok {
		t.Errorf("bundled two tasks on different bases: %+v", got)
	}

	if r := got["FT-2"].Reason; r != "alone: its bases differ from FT-1's" {
		t.Errorf("FT-2 reason %q", r)
	}
}

func TestCycleThroughTwoBases(t *testing.T) {
	t.Parallel()

	a, b, c := alone("FT-1"), alone("FT-2"), alone("FT-3")
	a.BuildsOn = []string{"FT-3"}
	c.BuildsOn = []string{"FT-2", "FT-1"}

	_, err := driveplan.Propose(driveplan.Input{Tasks: []driveplan.Task{a, b, c}, MaxLines: 300, Joins: true})
	if err == nil || !strings.Contains(err.Error(), "circle") {
		t.Errorf("want a cycle error, got %v", err)
	}
}

func TestOverrides(t *testing.T) {
	t.Parallel()

	kept, freed := small("FT-2", "ui", 20), small("FT-3", "ui", 20)
	kept.Alone = true
	freed.Base, freed.After = "FT-9", []string{driveplan.NoOrder}

	p := propose(t, small("FT-1", "ui", 20), kept, freed)
	got := entries(p)

	if _, ok := got["FT-1+FT-3"]; !ok {
		t.Errorf("want FT-1+FT-3 once FT-3's After: is dropped, got %+v", got)
	}

	if got["FT-2"].Reason != "alone: you kept it alone" {
		t.Errorf("FT-2 reason %q", got["FT-2"].Reason)
	}

	if b := got["FT-1+FT-3"].Bases; len(b) != 0 {
		t.Errorf("dropped order kept bases %+v", b)
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

// A bundle the user named stays whole whichever member is typed first, and a
// rule-formed chain inside one area bundles in either order too.
func TestBundleDoesNotDependOnMemberOrder(t *testing.T) {
	named := func(lead, rider string) driveplan.Plan {
		t623, t624 := small("FT-623", "drive", 40), small("FT-624", "plan", 40)
		t623.Base, t624.Base = "FT-622", "FT-623"

		byID := map[string]*driveplan.Task{"FT-623": &t623, "FT-624": &t624}
		byID[rider].With = lead

		return propose(t, *byID[lead], *byID[rider])
	}

	for _, pair := range [][2]string{{"FT-623", "FT-624"}, {"FT-624", "FT-623"}} {
		p := named(pair[0], pair[1])

		want := pair[0] + "+" + pair[1] + "@FT-622"
		if len(p.Args) != 1 || p.Args[0] != want {
			t.Errorf("%s+%s: args %v, want [%s]", pair[0], pair[1], p.Args, want)
		}
	}

	// the rules alone: FT-2 builds on FT-1, both after FT-9 outside the list
	t1, t2 := small("FT-1", "ui", 20), small("FT-2", "ui", 20)
	t1.Base, t2.Base = "FT-9", "FT-1"

	for _, tasks := range [][]driveplan.Task{{t1, t2}, {t2, t1}} {
		if p := propose(t, tasks...); p.Bundles != 1 {
			t.Errorf("%s first: %d bundles, args %v; want one", tasks[0].ID, p.Bundles, p.Args)
		}
	}
}

// A cap the user typed is printed, a cap of 1 included, and the carried
// flags reach the command however the proposal is printed.
func TestCommandCarriesWhatWasTyped(t *testing.T) {
	in := driveplan.Input{
		Tasks:    []driveplan.Task{small("FT-41", "ui", 20), small("FT-42", "ui", 20), small("FT-45", "forge", 80)},
		MaxLines: 300, Parallel: 1, TypedParallel: true, Carry: []string{"--pace", "fast"},
	}

	p, err := driveplan.Propose(in)
	if err != nil {
		t.Fatal(err)
	}

	want := "f10 start FT-41+FT-42 FT-45 --parallel 1 --pace fast"
	if got := p.Command(); got != want {
		t.Errorf("command %q, want %q", got, want)
	}

	if hint := p.Hint(); !strings.HasSuffix(hint, want) {
		t.Errorf("hint %q does not end in the command", hint)
	}

	var b bytes.Buffer
	if err := p.Write(&b, nil); err != nil || !strings.Contains(b.String(), "\n"+want+"\n") {
		t.Errorf("text %q, %v", b.String(), err)
	}

	// the default ceiling is no typed cap: a cap of 1 from it prints nothing
	in.TypedParallel, in.Carry = false, nil
	if p, _ := driveplan.Propose(in); p.Command() != "f10 start FT-41+FT-42 FT-45" {
		t.Errorf("command %q", p.Command())
	}
}

// Two attempts of one task are two entries, as a start opens them: each
// stays alone and keeps its suffix in the command.
func TestTwoAttemptsOfOneTask(t *testing.T) {
	a, b := small("FT-1", "ui", 20), small("FT-1", "ui", 20)
	a.Suffix, b.Suffix = "a", "b"

	p := propose(t, a, b)
	if got := p.Command(); got != "f10 start FT-1-a FT-1-b --parallel 2" {
		t.Errorf("command %q", got)
	}

	if _, err := driveplan.Propose(driveplan.Input{Tasks: []driveplan.Task{a, a}, MaxLines: 300}); err == nil ||
		!strings.Contains(err.Error(), "FT-1-a is listed twice") {
		t.Errorf("one attempt typed twice: err = %v", err)
	}
}
