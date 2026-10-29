package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/state"
)

// newCatchupRepo is a clone with main pushed and FT-1/a branched from it;
// origin's main then moves on, so FT-1/a has something to bring in.
func newCatchupRepo(t *testing.T) gitRepo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	r := gitRepo{t, filepath.Join(root, "work")}

	gitRepo{t, root}.at(before, "init", "-q", "--bare", "--initial-branch=main", origin)
	gitRepo{t, root}.at(before, "clone", "-q", origin, r.dir)
	r.at(before, "config", "user.email", "me@x.io")
	r.at(before, "config", "user.name", "Me")
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

func (r gitRepo) input(guard string) catchupInput {
	return catchupInput{
		dir: r.dir, main: r.dir, guard: guard,
		catchup: "rebase", catchupDefault: true, stacked: "merge", stackedDefault: true,
	}
}

func (r gitRepo) resolve(in catchupInput) catchupPlan {
	r.t.Helper()

	p, err := resolveCatchup(r.t.Context(), in)
	if err != nil {
		r.t.Fatal(err)
	}

	return p
}

func (r gitRepo) sha(ref string) string {
	r.t.Helper()
	return r.at(before, "rev-parse", ref)
}

func wantPlan(t *testing.T, got catchupPlan, base, found, method string) {
	t.Helper()

	if got.base != base || got.found != found || got.method != method {
		t.Errorf("base %q found %q method %q (why %q), want %q %q %q",
			got.base, got.found, got.method, got.why, base, found, method)
	}
}

func TestCatchupBaseDefaultBranch(t *testing.T) {
	r := newCatchupRepo(t)

	p := r.resolve(r.input(""))
	wantPlan(t, p, "origin/main", foundDefault, methodRebase)

	if !strings.Contains(p.why, "by default") || p.mark != cfgLanded+" main" || p.sha != r.sha("origin/main") {
		t.Errorf("why %q mark %q sha %q", p.why, p.mark, p.sha)
	}

	// the guard before implement only brings in a recorded base
	wantPlan(t, r.resolve(r.input("implement")), "", "", methodSkip)
}

func TestCatchupBaseNothingToBringIn(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "merge", "-q", "--no-edit", "origin/main")

	p := r.resolve(r.input(""))
	wantPlan(t, p, "origin/main", foundDefault, methodNone)

	if p.mark != "" {
		t.Errorf("mark %q on a branch with nothing to bring in", p.mark)
	}
}

func TestCatchupBaseLanded(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", "branch.FT-1/a."+cfgLanded, "main")
	r.at(during, "branch", "-f", "main", "main~1")

	// local main behind origin's: origin's
	wantPlan(t, r.resolve(r.input("")), "origin/main", foundLanded, methodRebase)

	// local main holding origin's and more (a local finish): local
	r.at(during, "checkout", "-q", "main")
	r.at(during, "merge", "-q", "--ff-only", "origin/main")
	r.commit(later, "c.go", "package main // c\n", "c, never pushed")
	r.at(later, "checkout", "-q", "FT-1/a")

	wantPlan(t, r.resolve(r.input("")), "main", foundLanded, methodRebase)

	// before a PR, what the PR targets
	wantPlan(t, r.resolve(r.input("pr")), "origin/main", foundLanded, methodRebase)
}

func TestCatchupBaseRecordedBranch(t *testing.T) {
	r := newCatchupRepo(t)

	r.at(during, "checkout", "-q", "-b", "FT-2/b", "origin/main")
	r.commit(during, "base.go", "package main // base\n", "base")
	r.at(during, "push", "-q", "-u", "origin", "FT-2/b")
	r.commit(later, "base.go", "package main // base, unpushed\n", "base, unpushed")
	r.at(later, "checkout", "-q", "FT-1/a")
	r.at(later, "config", "branch.FT-1/a."+cfgAfterBranch, "FT-2/b")

	p := r.resolve(r.input(""))
	wantPlan(t, p, "FT-2/b", foundAfter, methodMerge)

	if p.mark != cfgMoved+" "+r.sha("FT-2/b") {
		t.Errorf("mark %q, want the base's sha", p.mark)
	}

	wantPlan(t, r.resolve(r.input("pr")), "origin/FT-2/b", foundAfter, methodMerge)

	in := r.input("")
	in.stacked, in.stackedDefault = "rebase", false
	wantPlan(t, r.resolve(in), "FT-2/b", foundAfter, methodRebase)

	// a base never pushed is no PR target yet
	r.at(later, "push", "-q", "origin", "--delete", "FT-2/b")
	r.at(later, "fetch", "-q", "--prune")
	wantPlan(t, r.resolve(r.input("pr")), "", foundAfter, methodSkip)
}

func TestCatchupBaseRecordedBranchGone(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", "branch.FT-1/a."+cfgAfterBranch, "FT-9/gone")

	p := r.resolve(r.input("implement"))
	wantPlan(t, p, "origin/main", foundGone, methodRebase)

	if p.mark != cfgLanded+" main" {
		t.Errorf("mark %q", p.mark)
	}
}

func TestCatchupMethod(t *testing.T) {
	r := newCatchupRepo(t)

	for _, tc := range []struct {
		name   string
		edit   func(*catchupInput)
		method string
	}{
		{"declared merge", func(in *catchupInput) { in.catchup, in.catchupDefault = "merge", false }, methodMerge},
		{"off by name", func(in *catchupInput) { in.catchup, in.catchupDefault = "off", false }, methodRebase},
		{"off as a guard", func(in *catchupInput) { in.catchup, in.catchupDefault, in.guard = "off", false, "pr" }, methodSkip},
		{"stopped but not on origin", func(in *catchupInput) { in.shipStopped = true }, methodRebase},
	} {
		in := r.input("")
		tc.edit(&in)

		if got := r.resolve(in); got.method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", tc.name, got.method, got.why, tc.method)
		}
	}

	r.at(during, "push", "-q", "-u", "origin", "FT-1/a")

	stopped := r.input("")
	stopped.shipStopped = true

	declared := r.input("pr")
	declared.catchupDefault = false

	for name, tc := range map[string]struct {
		in     catchupInput
		method string
	}{
		"on origin, ship running":    {r.input(""), methodRebase},
		"on origin, ship stopped":    {stopped, methodMerge},
		"on origin, as a guard":      {declared, methodMerge},
		"on origin, guard implement": {r.input("implement"), methodSkip},
	} {
		if got := r.resolve(tc.in); got.method != tc.method {
			t.Errorf("%s: method %q (%s), want %q", name, got.method, got.why, tc.method)
		}
	}

	r.at(during, "checkout", "-q", "main")

	if got := r.resolve(r.input("pr")); got.method != methodSkip || got.why != "on the default branch" {
		t.Errorf("guard on main: %q %q", got.method, got.why)
	}
}

func TestCatchupBaseDetached(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "checkout", "-q", "--detach")

	if _, err := resolveCatchup(t.Context(), r.input("")); err == nil || !strings.Contains(err.Error(), "detached") {
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
		if err := (&state.Run{Session: "sess-1", Ship: status}).Save(dir, time.Now()); err != nil {
			t.Fatal(err)
		}

		if got := shipStopped(); got != want {
			t.Errorf("ship %s: stopped %v, want %v", status, got, want)
		}
	}
}

func TestCatchupBaseOutput(t *testing.T) {
	var b strings.Builder

	p := catchupPlan{
		branch: "FT-1/a",
		base:   "origin/main",
		sha:    "abc",
		found:  foundDefault,
		how:    "h",
		method: methodRebase,
		why:    "w",
		code:   codeNone,
	}
	if err := p.write(&b); err != nil {
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
	r.at(during, "config", "branch.FT-1/a."+cfgAfterBranch, "FT-2/b")

	in := r.input("implement")
	in.catchup, in.catchupDefault = "off", false

	p := r.resolve(in)
	wantPlan(t, p, "FT-2/b", foundAfter, methodSkip)

	if p.sha != r.sha("FT-2/b") || p.mark != "" || p.code != codeNone {
		t.Errorf("sha %q mark %q code %q", p.sha, p.mark, p.code)
	}
}

// The code row reads the base branch by the drive's rule: a merge of the
// base's own base is no code, a commit of its own is.
func TestCatchupBaseCodeRow(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "branch", "FT-2/b", "FT-1/a")
	r.at(during, "config", "branch.FT-2/b."+cfgAfterBranch, "FT-1/a")
	r.at(during, "checkout", "-q", "-b", "FT-3/c", "FT-2/b")
	r.at(during, "config", "branch.FT-3/c."+cfgAfterBranch, "FT-2/b")

	r.at(during, "checkout", "-q", "FT-1/a")
	r.commit(during, "a2.go", "package main // a2\n", "a2")
	r.at(during, "checkout", "-q", "FT-2/b")
	r.at(during, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")
	r.at(during, "checkout", "-q", "FT-3/c")

	if p := r.resolve(r.input("implement")); p.found != foundAfter || p.code != codeNone {
		t.Errorf("merge only: found %q code %q, want %q %q", p.found, p.code, foundAfter, codeNone)
	}

	r.at(during, "checkout", "-q", "FT-2/b")
	r.commit(during, "b2.go", "package main // b2\n", "b2")
	r.at(during, "checkout", "-q", "FT-3/c")

	if p := r.resolve(r.input("implement")); p.code != codeYes {
		t.Errorf("own commit: code %q, want %q", p.code, codeYes)
	}

	// a catchup by name prints the row too
	if p := r.resolve(r.input("")); p.code == "" {
		t.Error("a recorded base printed no code row")
	}

	// a landed base prints none: its code is in the default branch
	r.at(during, "config", "branch.FT-3/c."+cfgLanded, "main")

	if p := r.resolve(r.input("")); p.code != "" {
		t.Errorf("landed: code %q, want none printed", p.code)
	}
}

func TestCatchupBaseLandedWithoutOriginHead(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(during, "config", "branch.FT-1/a."+cfgLanded, "main")
	r.at(during, "remote", "set-head", "origin", "-d")

	wantPlan(t, r.resolve(r.input("")), "main", foundLanded, methodRebase)
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
		var out bytes.Buffer

		app := newApp()
		app.Writer = &out
		err := app.Run(t.Context(), append([]string{"f10", "-C", r.dir, "catchup", "base", "--no-fetch"}, args...))

		return out.String(), err
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
