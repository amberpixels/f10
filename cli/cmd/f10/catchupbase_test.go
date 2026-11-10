package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// newCatchupRepo is a clone with main pushed and FT-1/a branched from it;
// origin's main then moves on, so FT-1/a has something to bring in.
func newCatchupRepo(t *testing.T) gitRepo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}

	r := cloneRepo(t, "work")
	r.at(before, "checkout", "-q", "-b", "main")
	r.commit(before, "main.go", "package main\n", "init")
	r.at(before, "push", "-q", "-u", "origin", "main")
	r.at(before, "remote", "set-head", "origin", "main")

	r.at(before, "checkout", "-q", "-b", "FT-1/a")
	r.commit(before, "a.go", "package main // a\n", "a")

	r.at(before, "checkout", "-q", "main")
	r.commit(during, "b.go", "package main // b\n", "b")
	r.at(during, "push", "-q", "origin", "main")
	r.at(during, "checkout", "-q", "FT-1/a")

	return r
}

func (r gitRepo) input(guard string) stack.CatchupInput {
	return stack.CatchupInput{
		Dir: r.dir, Main: r.dir, Guard: guard,
		Catchup: "rebase", CatchupDefault: true, Stacked: "merge", StackedDefault: true,
	}
}

func (r gitRepo) resolve(in stack.CatchupInput) stack.CatchupPlan {
	r.t.Helper()

	p, err := stack.ResolveCatchup(r.t.Context(), in)
	if err != nil {
		r.t.Fatal(err)
	}

	return p
}

func (r gitRepo) sha(ref string) string {
	r.t.Helper()
	return r.at(before, "rev-parse", ref)
}

func wantPlan(t *testing.T, got stack.CatchupPlan, base, found, method string) {
	t.Helper()

	if got.Base != base || got.Found != found || got.Method != method {
		t.Errorf("base %q found %q method %q (why %q), want %q %q %q",
			got.Base, got.Found, got.Method, got.Why, base, found, method)
	}
}

func TestCatchupBaseDefaultBranch(t *testing.T) {
	r := newCatchupRepo(t)

	p := r.resolve(r.input(""))
	wantPlan(t, p, "origin/main", stack.FoundDefault, stack.MethodRebase)

	if !strings.Contains(p.Why, "by default") || p.Mark != string(branchmeta.Landed)+" main" ||
		p.SHA != r.sha("origin/main") {
		t.Errorf("why %q mark %q sha %q", p.Why, p.Mark, p.SHA)
	}

	// the guard before implement only brings in a recorded base
	wantPlan(t, r.resolve(r.input("implement")), "", "", stack.MethodSkip)
}

func TestCatchupBaseNothingToBringIn(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "merge", "-q", "--no-edit", "origin/main")

	p := r.resolve(r.input(""))
	wantPlan(t, p, "origin/main", stack.FoundDefault, stack.MethodNone)

	if p.Mark != "" {
		t.Errorf("mark %q on a branch with nothing to bring in", p.Mark)
	}
}

func TestCatchupBaseLanded(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", branchmeta.Landed.For("FT-1/a"), "main")
	r.at(during, "branch", "-f", "main", "main~1")

	// local main behind origin's: origin's
	wantPlan(t, r.resolve(r.input("")), "origin/main", stack.FoundLanded, stack.MethodRebase)

	// local main holding origin's and more (a local finish): local
	r.at(during, "checkout", "-q", "main")
	r.at(during, "merge", "-q", "--ff-only", "origin/main")
	r.commit(later, "c.go", "package main // c\n", "c, never pushed")
	r.at(later, "checkout", "-q", "FT-1/a")

	wantPlan(t, r.resolve(r.input("")), "main", stack.FoundLanded, stack.MethodRebase)

	// before a PR, what the PR targets
	wantPlan(t, r.resolve(r.input("pr")), "origin/main", stack.FoundLanded, stack.MethodRebase)
}

func TestCatchupBaseRecordedBranch(t *testing.T) {
	r := newCatchupRepo(t)

	r.at(during, "checkout", "-q", "-b", "FT-2/b", "origin/main")
	r.commit(during, "base.go", "package main // base\n", "base")
	r.at(during, "push", "-q", "-u", "origin", "FT-2/b")
	r.commit(later, "base.go", "package main // base, unpushed\n", "base, unpushed")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	p := r.resolve(r.input(""))
	wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodMerge)

	if p.Mark != string(branchmeta.Moved)+" "+r.sha("FT-2/b") {
		t.Errorf("mark %q, want the base's sha", p.Mark)
	}

	wantPlan(t, r.resolve(r.input("pr")), "origin/FT-2/b", stack.FoundAfter, stack.MethodMerge)

	in := r.input("")
	in.Stacked, in.StackedDefault = "rebase", false
	wantPlan(t, r.resolve(in), "FT-2/b", stack.FoundAfter, stack.MethodRebase)

	// a base never pushed is no PR target yet
	r.at(later, "push", "-q", "origin", "--delete", "FT-2/b")
	r.at(later, "fetch", "-q", "--prune")
	wantPlan(t, r.resolve(r.input("pr")), "", stack.FoundAfter, stack.MethodSkip)
}

// The drive and the watcher check the sha f10-base-moved names against
// origin, so the catchup integrates that sha, never a newer local tip.
func TestCatchupBaseMovedMarkNamesTheSha(t *testing.T) {
	r := newCatchupRepo(t)

	r.at(during, "checkout", "-q", "-b", "FT-2/b", "origin/main")
	r.commit(during, "base.go", "package main // base\n", "base")
	r.at(during, "push", "-q", "-u", "origin", "FT-2/b")
	r.commit(later, "base.go", "package main // base, unpushed\n", "base, unpushed")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	pushed := r.sha("origin/FT-2/b")
	r.at(later, "config", branchmeta.Moved.For("FT-1/a"), pushed)

	for _, guard := range []string{"", "implement"} {
		p := r.resolve(r.input(guard))
		wantPlan(t, p, pushed, stack.FoundAfter, stack.MethodMerge)

		if !strings.HasPrefix(p.How, string(branchmeta.Moved)+": "+gitx.ShortSHA(pushed)) {
			t.Errorf("guard %q: how %q, want it to name the mark", guard, p.How)
		}

		if p.SHA != pushed || p.Mark != string(branchmeta.Moved)+" "+pushed || p.Code != stack.CodeYes {
			t.Errorf("guard %q: sha %q mark %q code %q, want the marked sha %s", guard, p.SHA, p.Mark, p.Code, pushed)
		}
	}

	// the PR's target stays origin's, whatever the mark says
	wantPlan(t, r.resolve(r.input("pr")), "origin/FT-2/b", stack.FoundAfter, stack.MethodMerge)

	// the drive marks a local head; a later local commit stays out
	marked := r.sha("FT-2/b")
	r.at(later, "config", branchmeta.Moved.For("FT-1/a"), marked)
	r.at(later, "checkout", "-q", "FT-2/b")
	r.commit(later, "base.go", "package main // base, after the mark\n", "base, after the mark")
	r.at(later, "checkout", "-q", "FT-1/a")

	if p := r.resolve(r.input("")); p.SHA != marked {
		t.Errorf("sha %q, want the marked local head %s", p.SHA, marked)
	}

	// a marked sha already in the branch is nothing to bring in
	r.at(later, "merge", "-q", "--no-edit", marked)

	p := r.resolve(r.input(""))
	if p.Method != stack.MethodNone || p.Mark != "" {
		t.Errorf("method %q mark %q, want none and no mark", p.Method, p.Mark)
	}

	// a base rewritten since the mark no longer holds it: its old commits stay out
	r.at(later, "checkout", "-q", "FT-2/b")
	r.commit(later, "base.go", "package main // base, dropped\n", "base, dropped")
	dropped := r.sha("HEAD")
	r.at(later, "reset", "-q", "--hard", "HEAD~1")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", branchmeta.Moved.For("FT-1/a"), dropped)

	_, err := stack.ResolveCatchup(t.Context(), r.input(""))
	if err == nil || !strings.Contains(err.Error(), "no longer holds") {
		t.Errorf("a mark the base no longer holds: err %v, want it refused", err)
	}

	r.at(later, "config", branchmeta.Moved.For("FT-1/a"), strings.Repeat("0", len(marked)))

	if _, err := stack.ResolveCatchup(
		t.Context(),
		r.input(""),
	); err == nil ||
		!strings.Contains(err.Error(), string(branchmeta.Moved)) {
		t.Errorf("a mark naming no commit: err %v, want one naming %s", err, string(branchmeta.Moved))
	}
}

func TestCatchupBaseRecordedBranchGone(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-9/gone")

	p := r.resolve(r.input("implement"))
	wantPlan(t, p, "origin/main", stack.FoundGone, stack.MethodRebase)

	if p.Mark != string(branchmeta.Landed)+" main" {
		t.Errorf("mark %q", p.Mark)
	}
}

func TestCatchupMethod(t *testing.T) {
	r := newCatchupRepo(t)

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
		in := r.input("")
		tc.edit(&in)

		if got := r.resolve(in); got.Method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", tc.name, got.Method, got.Why, tc.method)
		}
	}

	r.at(during, "push", "-q", "-u", "origin", "FT-1/a")

	stopped := r.input("")
	stopped.ShipStopped = true

	declared := r.input("pr")
	declared.CatchupDefault = false

	for name, tc := range map[string]struct {
		in     stack.CatchupInput
		method string
	}{
		"on origin, ship running":    {r.input(""), stack.MethodRebase},
		"on origin, ship stopped":    {stopped, stack.MethodMerge},
		"on origin, as a guard":      {declared, stack.MethodMerge},
		"on origin, guard implement": {r.input("implement"), stack.MethodSkip},
	} {
		if got := r.resolve(tc.in); got.Method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", name, got.Method, got.Why, tc.method)
		}
	}

	r.at(during, "checkout", "-q", "main")

	if got := r.resolve(r.input("pr")); got.Method != stack.MethodSkip || got.Why != "on the default branch" {
		t.Errorf("guard on main: %q %q", got.Method, got.Why)
	}
}

func TestCatchupBaseDetached(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "checkout", "-q", "--detach")

	if _, err := stack.ResolveCatchup(
		t.Context(),
		r.input(""),
	); err == nil ||
		!strings.Contains(err.Error(), "detached") {
		t.Errorf("err %v, want detached", err)
	}
}

func TestShipStopped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	if shipStopped() {
		t.Error("no run file read as stopped")
	}

	for status, want := range map[string]bool{"failed": true, "partial": true, "running": false, "blocked": false} {
		if err := (&state.Run{Session: "sess-1", Ship: status}).Save(t.Context(), dir, time.Now()); err != nil {
			t.Fatal(err)
		}

		if got := shipStopped(); got != want {
			t.Errorf("ship %s: stopped %v, want %v", status, got, want)
		}
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
	r := newCatchupRepo(t)
	r.at(during, "branch", "FT-2/b", "origin/main")
	r.at(during, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	in := r.input("implement")
	in.Catchup, in.CatchupDefault = "off", false

	p := r.resolve(in)
	wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodSkip)

	if p.SHA != r.sha("FT-2/b") || p.Mark != "" || p.Code != stack.CodeNone {
		t.Errorf("sha %q mark %q code %q", p.SHA, p.Mark, p.Code)
	}
}

// The code row reads the base branch by the drive's rule: a merge of the
// base's own base is no code, a commit of its own is.
func TestCatchupBaseCodeRow(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "branch", "FT-2/b", "FT-1/a")
	r.at(during, "config", branchmeta.AfterBranch.For("FT-2/b"), "FT-1/a")
	r.at(during, "checkout", "-q", "-b", "FT-3/c", "FT-2/b")
	r.at(during, "config", branchmeta.AfterBranch.For("FT-3/c"), "FT-2/b")

	r.at(during, "checkout", "-q", "FT-1/a")
	r.commit(during, "a2.go", "package main // a2\n", "a2")
	r.at(during, "checkout", "-q", "FT-2/b")
	r.at(during, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")
	r.at(during, "checkout", "-q", "FT-3/c")

	if p := r.resolve(r.input("implement")); p.Found != stack.FoundAfter || p.Code != stack.CodeNone {
		t.Errorf("merge only: found %q code %q, want %q %q", p.Found, p.Code, stack.FoundAfter, stack.CodeNone)
	}

	r.at(during, "checkout", "-q", "FT-2/b")
	r.commit(during, "b2.go", "package main // b2\n", "b2")
	r.at(during, "checkout", "-q", "FT-3/c")

	if p := r.resolve(r.input("implement")); p.Code != stack.CodeYes {
		t.Errorf("own commit: code %q, want %q", p.Code, stack.CodeYes)
	}

	// a catchup by name prints the row too
	if p := r.resolve(r.input("")); p.Code == "" {
		t.Error("a recorded base printed no code row")
	}

	// a landed base prints none: its code is in the default branch
	r.at(during, "config", branchmeta.Landed.For("FT-3/c"), "main")

	if p := r.resolve(r.input("")); p.Code != "" {
		t.Errorf("landed: code %q, want none printed", p.Code)
	}
}

// No origin/HEAD fails with its remedy, as pr base and the drive do, rather
// than guess main on a repo whose default may be master.
func TestCatchupBaseWithoutOriginHeadFails(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", branchmeta.Landed.For("FT-1/a"), "main")
	r.at(during, "remote", "set-head", "origin", "-d")

	_, err := stack.ResolveCatchup(t.Context(), r.input(""))
	if err == nil || !strings.Contains(err.Error(), "set-head origin -a") {
		t.Errorf("err = %v, want the set-head remedy", err)
	}
}

func TestCatchupBaseCommand(t *testing.T) {
	r := newCatchupRepo(t)

	dir := filepath.Join(r.dir, ".f10", "instructions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	md := "---\ncatchup: merge\n---\n# demo\n"
	if err := os.WriteFile(filepath.Join(dir, "project.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		return runApp(t, append([]string{"-C", r.dir, "pr", "catchup", "--no-fetch"}, args...)...).outErr()
	}

	for _, guard := range [][]string{nil, {"--guard", "judge"}} {
		out, err := run(guard...)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{"base: origin/main\n", "method: merge\n", "why: catchup: merge\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: no %q in\n%s", guard, want, out)
			}
		}
	}

	if _, err := run("--guard", "bogus"); err == nil || !strings.Contains(err.Error(), "want implement, pr or judge") {
		t.Errorf("--guard bogus: %v", err)
	}
}

// newStackedRepo is FT-1/a built on FT-2/b, which has a commit of its own,
// with the base tip start records.
func newStackedRepo(t *testing.T) (gitRepo, string) {
	t.Helper()

	r := newCatchupRepo(t)
	r.at(during, "checkout", "-q", "-b", "FT-2/b", "main")
	r.commit(during, "help.go", "package main // help\n", "help")
	r.at(during, "checkout", "-q", "-B", "FT-1/a", "FT-2/b")
	r.commit(during, "a.go", "package main // a\n", "a")
	r.at(during, "config", branchmeta.After.For("FT-1/a"), "FT-2")
	r.at(during, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	tip := r.sha("FT-2/b")
	r.at(during, "config", branchmeta.Tip.For("FT-1/a"), tip)

	return r, tip
}

// A base whose history was rewritten under the branch is rebased onto from
// its old tip, so its old commits never come back as conflicts.
func TestCatchupBaseRewritten(t *testing.T) {
	r, tip := newStackedRepo(t)

	// FT-2/b rebased onto origin/main, which moved on
	r.at(later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.commit(later, "help.go", "package main // help, rebased\n", "help")
	r.at(later, "checkout", "-q", "FT-1/a")

	for _, guard := range []string{"", "implement"} {
		p := r.resolve(r.input(guard))
		wantPlan(t, p, "FT-2/b", stack.FoundAfter, stack.MethodOnto)

		if p.Upstream != tip {
			t.Errorf("guard %q: upstream %q, want the old tip %s", guard, p.Upstream, tip)
		}
	}

	// the guard before pr keeps a branch on origin merging, so its push never forces
	r.at(later, "push", "-q", "-u", "origin", "FT-1/a", "FT-2/b")

	if p := r.resolve(r.input("pr")); p.Method != stack.MethodMerge || p.Upstream != "" {
		t.Errorf("pr guard on origin: method %q upstream %q, want merge and none", p.Method, p.Upstream)
	}
}

func TestCatchupBaseSquashLanded(t *testing.T) {
	r, tip := newStackedRepo(t)

	// FT-2/b squash-merged into main, then finished
	r.at(later, "checkout", "-q", "main")
	r.at(later, "merge", "-q", "--ff-only", "origin/main")
	r.at(later, "merge", "-q", "--squash", "FT-2/b")
	r.at(later, "commit", "-q", "-m", "FT-2 squashed")
	r.at(later, "push", "-q", "origin", "main")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", "--unset", branchmeta.AfterBranch.For("FT-1/a"))
	r.at(later, "config", branchmeta.Landed.For("FT-1/a"), "main")

	p := r.resolve(r.input(""))
	wantPlan(t, p, "main", stack.FoundLanded, stack.MethodOnto)

	if p.Upstream != tip {
		t.Errorf("upstream %q, want the old tip %s", p.Upstream, tip)
	}
}

func TestCatchupBaseTipNotRewritten(t *testing.T) {
	r, _ := newStackedRepo(t)

	// the base only moved on: today's method
	r.at(later, "checkout", "-q", "FT-2/b")
	r.commit(later, "b2.go", "package main // b2\n", "b2")
	r.at(later, "checkout", "-q", "FT-1/a")

	if p := r.resolve(r.input("")); p.Method != stack.MethodMerge || p.Upstream != "" {
		t.Errorf("moved on: method %q upstream %q, want merge and none", p.Method, p.Upstream)
	}

	// a tip the branch does not hold is stale: ignored
	r.at(later, "checkout", "-q", "-b", "elsewhere", "origin/main")
	r.commit(later, "x.go", "package main // x\n", "x")
	r.at(later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.commit(later, "help.go", "package main // help, rebased\n", "help")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", branchmeta.Tip.For("FT-1/a"), r.sha("elsewhere"))

	if p := r.resolve(r.input("")); p.Method == stack.MethodOnto {
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

// Releasing a dependent records its base tip before the base branch goes, so
// its catchup can drop the squashed commits.
func TestReleaseDependentsRecordsTip(t *testing.T) {
	r, tip := newStackedRepo(t)
	r.at(later, "config", "--unset", branchmeta.Tip.For("FT-1/a"))

	if _, err := releaseDependents(t.Context(), r.dir, "FT-2/b", "main", true); err != nil {
		t.Fatal(err)
	}

	if got := r.at(later, "config", branchmeta.Tip.For("FT-1/a")); got != tip {
		t.Errorf("tip %q, want %s", got, tip)
	}
}

// onto outranks every non-guard row: a stopped ship's catchup is the one the
// drive sends after a guard conflict, and a merge there conflicts again.
func TestCatchupBaseRewrittenOutranks(t *testing.T) {
	r, _ := newStackedRepo(t)
	r.at(later, "push", "-q", "-u", "origin", "FT-1/a")
	r.at(later, "checkout", "-q", "-B", "FT-2/b", "origin/main")
	r.commit(later, "help.go", "package main // help, rebased\n", "help")
	r.at(later, "checkout", "-q", "FT-1/a")

	stopped := r.input("")
	stopped.ShipStopped = true

	if p := r.resolve(stopped); p.Method != stack.MethodOnto {
		t.Errorf("stopped ship on origin: method %q (%s), want onto", p.Method, p.Why)
	}

	declared := r.input("")
	declared.Stacked, declared.StackedDefault = "merge", false

	p := r.resolve(declared)
	if p.Method != stack.MethodOnto || !strings.Contains(p.Why, "overrides catchup_stacked: merge") {
		t.Errorf("declared merge: method %q why %q, want onto naming the override", p.Method, p.Why)
	}
}

// A spike closed without merging never reached main: its dependent keeps no
// tip, so its catchup keeps the spike's code.
func TestReleaseDependentsSpikeClearsTip(t *testing.T) {
	r, _ := newStackedRepo(t)

	if _, err := releaseDependents(t.Context(), r.dir, "FT-2/b", "main", false); err != nil {
		t.Fatal(err)
	}

	if got, err := gitx.Config(t.Context(), r.dir, branchmeta.Tip.For("FT-1/a")); err != nil || got != "" {
		t.Errorf("tip %q (%v) after a spike closed, want none", got, err)
	}

	if p := r.resolve(r.input("")); p.Method == stack.MethodOnto {
		t.Errorf("method %q after a spike closed, want today's", p.Method)
	}
}

// A local base behind origin's never hides the newer tip the branch holds.
func TestRecordTipTakesTheNewerBase(t *testing.T) {
	r, _ := newStackedRepo(t)

	r.at(later, "checkout", "-q", "FT-2/b")
	r.commit(later, "b2.go", "package main // b2\n", "b2")
	r.at(later, "push", "-q", "-u", "origin", "FT-2/b")
	newer := r.sha("FT-2/b")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "branch", "-f", "FT-2/b", "FT-2/b~1")
	r.at(later, "merge", "-q", "--no-edit", "origin/FT-2/b")

	if err := worktree.RecordTip(t.Context(), r.dir, "FT-1/a", "FT-2/b"); err != nil {
		t.Fatal(err)
	}

	if got := r.at(later, "config", branchmeta.Tip.For("FT-1/a")); got != newer {
		t.Errorf("tip %q, want origin's %s", got, newer)
	}
}
