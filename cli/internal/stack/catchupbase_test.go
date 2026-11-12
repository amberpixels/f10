package stack_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func wantPlan(t *testing.T, got stack.CatchupPlan, base, found, method string) {
	t.Helper()

	if got.Base != base || got.Found != found || got.Method != method {
		t.Errorf("base %q found %q method %q (why %q), want %q %q %q",
			got.Base, got.Found, got.Method, got.Why, base, found, method)
	}
}

func TestCatchupBaseDefaultBranch(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	p := r.Resolve(r.Input(""))
	wantPlan(t, p, "origin/main", stack.FoundDefault, stack.MethodRebase)

	if !strings.Contains(p.Why, "by default") || p.Mark != string(branchmeta.Landed)+" main" ||
		p.SHA != r.Sha("origin/main") {
		t.Errorf("why %q mark %q sha %q", p.Why, p.Mark, p.SHA)
	}

	// the guard before implement only brings in a recorded base
	wantPlan(t, r.Resolve(r.Input("implement")), "", "", stack.MethodSkip)
}

func TestCatchupBaseNothingToBringIn(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "merge", "-q", "--no-edit", "origin/main")

	p := r.Resolve(r.Input(""))
	wantPlan(t, p, "origin/main", stack.FoundDefault, stack.MethodNone)

	if p.Mark != "" {
		t.Errorf("mark %q on a branch with nothing to bring in", p.Mark)
	}
}

func TestCatchupBaseLanded(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "config", branchmeta.Landed.For("FT-1/a"), "main")
	r.At(testkit.During, "branch", "-f", "main", "main~1")

	// local main behind origin's: origin's
	wantPlan(t, r.Resolve(r.Input("")), "origin/main", stack.FoundLanded, stack.MethodRebase)

	// local main holding origin's and more (a local finish): local
	r.At(testkit.During, "checkout", "-q", "main")
	r.At(testkit.During, "merge", "-q", "--ff-only", "origin/main")
	r.Commit(testkit.Later, "c.go", "package main // c\n", "c, never pushed")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")

	wantPlan(t, r.Resolve(r.Input("")), "main", stack.FoundLanded, stack.MethodRebase)

	// before a PR, what the PR targets
	wantPlan(t, r.Resolve(r.Input("pr")), "origin/main", stack.FoundLanded, stack.MethodRebase)
}

func TestCatchupBaseRecordedBranch(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	r.At(testkit.During, "checkout", "-q", "-b", "FT-2/b", "origin/main")
	r.Commit(testkit.During, "base.go", "package main // base\n", "base")
	r.At(testkit.During, "push", "-q", "-u", "origin", "FT-2/b")
	r.Commit(testkit.Later, "base.go", "package main // base, unpushed\n", "base, unpushed")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	p := r.Resolve(r.Input(""))
	wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodMerge)

	if p.Mark != string(branchmeta.Moved)+" "+r.Sha("FT-2/b") {
		t.Errorf("mark %q, want the base's sha", p.Mark)
	}

	wantPlan(t, r.Resolve(r.Input("pr")), "origin/FT-2/b", stack.FoundAfter, stack.MethodMerge)

	in := r.Input("")
	in.Stacked, in.StackedDefault = "rebase", false
	wantPlan(t, r.Resolve(in), "FT-2/b", stack.FoundAfter, stack.MethodRebase)

	// a base never pushed is no PR target yet
	r.At(testkit.Later, "push", "-q", "origin", "--delete", "FT-2/b")
	r.At(testkit.Later, "fetch", "-q", "--prune")
	wantPlan(t, r.Resolve(r.Input("pr")), "", stack.FoundAfter, stack.MethodSkip)
}

// The drive and the watcher check the sha f10-base-moved names against
// origin, so the catchup integrates that sha, never a newer local tip.
func TestCatchupBaseMovedMarkNamesTheSha(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	r.At(testkit.During, "checkout", "-q", "-b", "FT-2/b", "origin/main")
	r.Commit(testkit.During, "base.go", "package main // base\n", "base")
	r.At(testkit.During, "push", "-q", "-u", "origin", "FT-2/b")
	r.Commit(testkit.Later, "base.go", "package main // base, unpushed\n", "base, unpushed")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	pushed := r.Sha("origin/FT-2/b")
	r.At(testkit.Later, "config", branchmeta.Moved.For("FT-1/a"), pushed)

	for _, guard := range []string{"", "implement"} {
		p := r.Resolve(r.Input(guard))
		wantPlan(t, p, pushed, stack.FoundAfter, stack.MethodMerge)

		if !strings.HasPrefix(p.How, string(branchmeta.Moved)+": "+gitx.ShortSHA(pushed)) {
			t.Errorf("guard %q: how %q, want it to name the mark", guard, p.How)
		}

		if p.SHA != pushed || p.Mark != string(branchmeta.Moved)+" "+pushed || p.Code != stack.CodeYes {
			t.Errorf("guard %q: sha %q mark %q code %q, want the marked sha %s", guard, p.SHA, p.Mark, p.Code, pushed)
		}
	}

	// the PR's target stays origin's, whatever the mark says
	wantPlan(t, r.Resolve(r.Input("pr")), "origin/FT-2/b", stack.FoundAfter, stack.MethodMerge)

	// the drive marks a local head; a later local commit stays out
	marked := r.Sha("FT-2/b")
	r.At(testkit.Later, "config", branchmeta.Moved.For("FT-1/a"), marked)
	r.At(testkit.Later, "checkout", "-q", "FT-2/b")
	r.Commit(testkit.Later, "base.go", "package main // base, after the mark\n", "base, after the mark")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")

	if p := r.Resolve(r.Input("")); p.SHA != marked {
		t.Errorf("sha %q, want the marked local head %s", p.SHA, marked)
	}

	// a marked sha already in the branch is nothing to bring in
	r.At(testkit.Later, "merge", "-q", "--no-edit", marked)

	p := r.Resolve(r.Input(""))
	if p.Method != stack.MethodNone || p.Mark != "" {
		t.Errorf("method %q mark %q, want none and no mark", p.Method, p.Mark)
	}

	// a base rewritten since the mark no longer holds it: its old commits stay out
	r.At(testkit.Later, "checkout", "-q", "FT-2/b")
	r.Commit(testkit.Later, "base.go", "package main // base, dropped\n", "base, dropped")
	dropped := r.Sha("HEAD")
	r.At(testkit.Later, "reset", "-q", "--hard", "HEAD~1")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "config", branchmeta.Moved.For("FT-1/a"), dropped)

	_, err := stack.ResolveCatchup(t.Context(), r.Input(""))
	if err == nil || !strings.Contains(err.Error(), "no longer holds") {
		t.Errorf("a mark the base no longer holds: err %v, want it refused", err)
	}

	r.At(testkit.Later, "config", branchmeta.Moved.For("FT-1/a"), strings.Repeat("0", len(marked)))

	if _, err := stack.ResolveCatchup(
		t.Context(),
		r.Input(""),
	); err == nil ||
		!strings.Contains(err.Error(), string(branchmeta.Moved)) {
		t.Errorf("a mark naming no commit: err %v, want one naming %s", err, string(branchmeta.Moved))
	}
}

func TestCatchupBaseRecordedBranchGone(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-9/gone")

	p := r.Resolve(r.Input("implement"))
	wantPlan(t, p, "origin/main", stack.FoundGone, stack.MethodRebase)

	if p.Mark != string(branchmeta.Landed)+" main" {
		t.Errorf("mark %q", p.Mark)
	}
}

func TestCatchupMethod(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	for _, tc := range []struct {
		name   string
		edit   func(*stack.CatchupInput)
		method string
	}{
		{"declared merge", func(in *stack.CatchupInput) { in.Catchup, in.CatchupDefault = "merge", false }, stack.MethodMerge},
		{"off by name", func(in *stack.CatchupInput) { in.Catchup, in.CatchupDefault = "off", false }, stack.MethodRebase},
		{"off as a guard", func(in *stack.CatchupInput) { in.Catchup, in.CatchupDefault, in.Guard = "off", false, "pr" }, stack.MethodSkip},
		{"stopped but not on origin", func(in *stack.CatchupInput) { in.ShipStopped = true }, stack.MethodRebase},
	} {
		in := r.Input("")
		tc.edit(&in)

		if got := r.Resolve(in); got.Method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", tc.name, got.Method, got.Why, tc.method)
		}
	}

	r.At(testkit.During, "push", "-q", "-u", "origin", "FT-1/a")

	stopped := r.Input("")
	stopped.ShipStopped = true

	declared := r.Input("pr")
	declared.CatchupDefault = false

	for name, tc := range map[string]struct {
		in     stack.CatchupInput
		method string
	}{
		"on origin, ship running":    {r.Input(""), stack.MethodRebase},
		"on origin, ship stopped":    {stopped, stack.MethodMerge},
		"on origin, as a guard":      {declared, stack.MethodMerge},
		"on origin, guard implement": {r.Input("implement"), stack.MethodSkip},
	} {
		if got := r.Resolve(tc.in); got.Method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", name, got.Method, got.Why, tc.method)
		}
	}

	r.At(testkit.During, "checkout", "-q", "main")

	if got := r.Resolve(r.Input("pr")); got.Method != stack.MethodSkip || got.Why != "on the default branch" {
		t.Errorf("guard on main: %q %q", got.Method, got.Why)
	}
}

func TestCatchupBaseDetached(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "checkout", "-q", "--detach")

	if _, err := stack.ResolveCatchup(
		t.Context(),
		r.Input(""),
	); err == nil ||
		!strings.Contains(err.Error(), "detached") {
		t.Errorf("err %v, want detached", err)
	}
}

func TestCatchupBaseOutput(t *testing.T) {
	var b strings.Builder

	p := stack.CatchupPlan{
		Branch: "FT-1/a",
		Base:   "origin/main",
		SHA:    "abc",
		Found:  stack.FoundDefault,
		How:    "h",
		Method: stack.MethodRebase,
		Why:    "w",
		Code:   stack.CodeNone,
	}
	if err := p.Write(&b); err != nil {
		t.Fatal(err)
	}

	want := "branch: FT-1/a\nbase: origin/main\nsha: abc\nfound: default branch\nhow: h\nmethod: rebase\nwhy: w\ncode: none\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestCatchupGuardOffStillPrintsTheBase(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "branch", "FT-2/b", "origin/main")
	r.At(testkit.During, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	in := r.Input("implement")
	in.Catchup, in.CatchupDefault = "off", false

	p := r.Resolve(in)
	wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodSkip)

	if p.SHA != r.Sha("FT-2/b") || p.Mark != "" || p.Code != stack.CodeNone {
		t.Errorf("sha %q mark %q code %q", p.SHA, p.Mark, p.Code)
	}
}

// The code row reads the base branch by the drive's rule: a merge of the
// base's own base is no code, a commit of its own is.
func TestCatchupBaseCodeRow(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "branch", "FT-2/b", "FT-1/a")
	r.At(testkit.During, "config", branchmeta.AfterBranch.For("FT-2/b"), "FT-1/a")
	r.At(testkit.During, "checkout", "-q", "-b", "FT-3/c", "FT-2/b")
	r.At(testkit.During, "config", branchmeta.AfterBranch.For("FT-3/c"), "FT-2/b")

	r.At(testkit.During, "checkout", "-q", "FT-1/a")
	r.Commit(testkit.During, "a2.go", "package main // a2\n", "a2")
	r.At(testkit.During, "checkout", "-q", "FT-2/b")
	r.At(testkit.During, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")
	r.At(testkit.During, "checkout", "-q", "FT-3/c")

	if p := r.Resolve(r.Input("implement")); p.Found != stack.FoundAfter || p.Code != stack.CodeNone {
		t.Errorf("merge only: found %q code %q, want %q %q", p.Found, p.Code, stack.FoundAfter, stack.CodeNone)
	}

	r.At(testkit.During, "checkout", "-q", "FT-2/b")
	r.Commit(testkit.During, "b2.go", "package main // b2\n", "b2")
	r.At(testkit.During, "checkout", "-q", "FT-3/c")

	if p := r.Resolve(r.Input("implement")); p.Code != stack.CodeYes {
		t.Errorf("own commit: code %q, want %q", p.Code, stack.CodeYes)
	}

	// a catchup by name prints the row too
	if p := r.Resolve(r.Input("")); p.Code == "" {
		t.Error("a recorded base printed no code row")
	}

	// a landed base prints none: its code is in the default branch
	r.At(testkit.During, "config", branchmeta.Landed.For("FT-3/c"), "main")

	if p := r.Resolve(r.Input("")); p.Code != "" {
		t.Errorf("landed: code %q, want none printed", p.Code)
	}
}

// No origin/HEAD fails with its remedy, as pr base and the drive do, rather
// than guess main on a repo whose default may be master.
func TestCatchupBaseWithoutOriginHeadFails(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.During, "config", branchmeta.Landed.For("FT-1/a"), "main")
	r.At(testkit.During, "remote", "set-head", "origin", "-d")

	_, err := stack.ResolveCatchup(t.Context(), r.Input(""))
	if err == nil || !strings.Contains(err.Error(), "set-head origin -a") {
		t.Errorf("err = %v, want the set-head remedy", err)
	}
}

// A base whose history was rewritten under the branch is rebased onto from
// its old tip, so its old commits never come back as conflicts.
func TestCatchupBaseRewritten(t *testing.T) {
	r, tip := testkit.NewStackedRepo(t)

	// FT-2/b rebased onto origin/main, which moved on
	r.At(testkit.Later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.Commit(testkit.Later, "help.go", "package main // help, rebased\n", "help")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")

	for _, guard := range []string{"", "implement"} {
		p := r.Resolve(r.Input(guard))
		wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodOnto)

		if p.Upstream != tip {
			t.Errorf("guard %q: upstream %q, want the old tip %s", guard, p.Upstream, tip)
		}
	}

	// the guard before pr keeps a branch on origin merging, so its push never forces
	r.At(testkit.Later, "push", "-q", "-u", "origin", "FT-1/a", "FT-2/b")

	if p := r.Resolve(r.Input("pr")); p.Method != stack.MethodMerge || p.Upstream != "" {
		t.Errorf("pr guard on origin: method %q upstream %q, want merge and none", p.Method, p.Upstream)
	}
}

func TestCatchupBaseSquashLanded(t *testing.T) {
	r, tip := testkit.NewStackedRepo(t)

	// FT-2/b squash-merged into main, then finished
	r.At(testkit.Later, "checkout", "-q", "main")
	r.At(testkit.Later, "merge", "-q", "--ff-only", "origin/main")
	r.At(testkit.Later, "merge", "-q", "--squash", "FT-2/b")
	r.At(testkit.Later, "commit", "-q", "-m", "FT-2 squashed")
	r.At(testkit.Later, "push", "-q", "origin", "main")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "config", "--unset", branchmeta.AfterBranch.For("FT-1/a"))
	r.At(testkit.Later, "config", branchmeta.Landed.For("FT-1/a"), "main")

	p := r.Resolve(r.Input(""))
	wantPlan(t, p, "main", stack.FoundLanded, stack.MethodOnto)

	if p.Upstream != tip {
		t.Errorf("upstream %q, want the old tip %s", p.Upstream, tip)
	}
}

func TestCatchupBaseTipNotRewritten(t *testing.T) {
	r, _ := testkit.NewStackedRepo(t)

	// the base only moved on: today's method
	r.At(testkit.Later, "checkout", "-q", "FT-2/b")
	r.Commit(testkit.Later, "b2.go", "package main // b2\n", "b2")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")

	if p := r.Resolve(r.Input("")); p.Method != stack.MethodMerge || p.Upstream != "" {
		t.Errorf("moved on: method %q upstream %q, want merge and none", p.Method, p.Upstream)
	}

	// a tip the branch does not hold is stale: ignored
	r.At(testkit.Later, "checkout", "-q", "-b", "elsewhere", "origin/main")
	r.Commit(testkit.Later, "x.go", "package main // x\n", "x")
	r.At(testkit.Later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.Commit(testkit.Later, "help.go", "package main // help, rebased\n", "help")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "config", branchmeta.Tip.For("FT-1/a"), r.Sha("elsewhere"))

	if p := r.Resolve(r.Input("")); p.Method == stack.MethodOnto {
		t.Errorf("stale tip: method %q, want today's", p.Method)
	}
}

func TestCatchupBaseOutputUpstream(t *testing.T) {
	var b strings.Builder

	p := stack.CatchupPlan{Branch: "FT-1/a", Base: "FT-2/b", SHA: "abc", Upstream: "old", Method: stack.MethodOnto}
	if err := p.Write(&b); err != nil {
		t.Fatal(err)
	}

	want := "branch: FT-1/a\nbase: FT-2/b\nsha: abc\nupstream: old\nmethod: onto\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

// onto outranks every non-guard row: a stopped ship's catchup is the one the
// drive sends after a guard conflict, and a merge there conflicts again.
func TestCatchupBaseRewrittenOutranks(t *testing.T) {
	r, _ := testkit.NewStackedRepo(t)
	r.At(testkit.Later, "push", "-q", "-u", "origin", "FT-1/a")
	r.At(testkit.Later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.Commit(testkit.Later, "help.go", "package main // help, rebased\n", "help")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")

	stopped := r.Input("")
	stopped.ShipStopped = true

	if p := r.Resolve(stopped); p.Method != stack.MethodOnto {
		t.Errorf("stopped ship on origin: method %q (%s), want onto", p.Method, p.Why)
	}

	declared := r.Input("")
	declared.Stacked, declared.StackedDefault = "merge", false

	p := r.Resolve(declared)
	if p.Method != stack.MethodOnto || !strings.Contains(p.Why, "overrides catchup_stacked: merge") {
		t.Errorf("declared merge: method %q why %q, want onto naming the override", p.Method, p.Why)
	}
}

// A local base behind origin's never hides the newer tip the branch holds.
func TestRecordTipTakesTheNewerBase(t *testing.T) {
	r, _ := testkit.NewStackedRepo(t)

	r.At(testkit.Later, "checkout", "-q", "FT-2/b")
	r.Commit(testkit.Later, "b2.go", "package main // b2\n", "b2")
	r.At(testkit.Later, "push", "-q", "-u", "origin", "FT-2/b")
	newer := r.Sha("FT-2/b")
	r.At(testkit.Later, "checkout", "-q", "FT-1/a")
	r.At(testkit.Later, "branch", "-f", "FT-2/b", "FT-2/b~1")
	r.At(testkit.Later, "merge", "-q", "--no-edit", "origin/FT-2/b")

	if err := stack.RecordTip(t.Context(), r.Dir, "FT-1/a", "FT-2/b"); err != nil {
		t.Fatal(err)
	}

	if got := r.At(testkit.Later, "config", branchmeta.Tip.For("FT-1/a")); got != newer {
		t.Errorf("tip %q, want origin's %s", got, newer)
	}
}
