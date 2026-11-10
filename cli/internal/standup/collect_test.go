package standup

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The collectors are exercised against a scripted git and gh: what each
// asks and what it reads back.

var standupSince = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)

const (
	sinceArg = "--since=2026-10-14T00:00:00Z"
	ghStamp  = "2026-10-14T00:00:00Z"
	headRef  = "git symbolic-ref --short refs/remotes/origin/HEAD"
)

// fakes answers every git and host call from a script and fails the test
// on one it was not given; a key scripted with several answers hands them
// out in order, repeating the last.
type fakes struct {
	mu      sync.Mutex
	t       *testing.T
	calls   []string
	answers map[string][]shell.Result
	meta    *branchmetatest.Memory
}

func newFakes(t *testing.T) *fakes {
	t.Helper()

	f := &fakes{t: t, answers: map[string][]shell.Result{}, meta: branchmetatest.Use(t)}
	prevExec, prevCapture := gitx.Exec, shell.Capture

	gitx.Exec = func(_ context.Context, _ string, args ...string) (string, error) {
		res := f.answer("git " + strings.Join(args, " "))
		if res.Code != 0 {
			return "", &gitx.ExitError{
				Args: args, Code: res.Code, Stdout: strings.TrimSpace(res.Stdout), Stderr: res.Stderr,
			}
		}

		return strings.TrimSpace(res.Stdout), nil
	}

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		return f.answer(filepath.Base(name) + " " + strings.Join(args, " ")), nil
	}

	t.Cleanup(func() { gitx.Exec, shell.Capture = prevExec, prevCapture })

	return f
}

func (f *fakes) answer(key string) shell.Result {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, key)

	queue, ok := f.answers[key]
	if !ok {
		f.t.Fatalf("unscripted call: %q", key)
	}

	if len(queue) > 1 {
		f.answers[key] = queue[1:]
	}

	return queue[0]
}

func (f *fakes) script(key string, stdouts ...string) {
	f.answers[key] = nil
	for _, s := range stdouts {
		f.answers[key] = append(f.answers[key], shell.Result{Stdout: s})
	}
}

func (f *fakes) fail(key string, res shell.Result) { f.answers[key] = []shell.Result{res} }

func (f *fakes) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

func ghSearch(query, fields string) string {
	return "gh pr list --state all --search " + query + " --json " + fields + " --limit 1000"
}

func TestRefBranch(t *testing.T) {
	remotes := []string{"origin", "up"}

	for in, want := range map[string]string{
		"FT-1/x":                "FT-1/x",
		"origin/FT-1/x":         "FT-1/x",
		"refs/remotes/up/a":     "a",
		"refs/heads/feature/ab": "feature/ab",
		"origin/HEAD":           "main",
		"other/x":               "other/x", // not a remote: a branch with a slash
	} {
		if got := refBranch(in, remotes, "main"); got != want {
			t.Errorf("refBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUserCommits(t *testing.T) {
	f := newFakes(t)

	rec := func(fields ...string) string { return strings.Join(fields, "\x1f") }
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat, strings.Join([]string{
		rec("a1", "Me@X.io", "2026-10-14T10:00:00+03:00", "p1", "origin/FT-2/b", "work on b"),
		rec("a2", "me@x.io", "2026-10-14T11:00:00+03:00", "p2 p3", "FT-2/b", "Merge Main: into b"),
		rec("m1", "me@x.io", "2026-10-14T09:00:00+03:00", "p0", "FT-2/b", "release: v1"),
		rec("z1", "notme@x.io", "2026-10-14T12:00:00+03:00", "p4", "FT-2/b", "by someone named me"),
	}, "\n"))
	f.script("git log --format=%H "+sinceArg+" origin/main", "m1")

	got, err := userCommits(t.Context(), ".", "me@x.io", standupSince, "origin/main", "main", []string{"origin"})
	if err != nil {
		t.Fatal(err)
	}

	want := []Commit{
		{
			SHA:     "a1",
			Subject: "work on b",
			At:      time.Date(2026, 10, 14, 7, 0, 0, 0, time.UTC),
			Branch:  "FT-2/b",
			Parents: []string{"p1"},
		},
		{
			SHA:     "a2",
			Subject: "Merge Main: into b",
			At:      time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC),
			Branch:  "FT-2/b",
			Parents: []string{"p2", "p3"},
		},
		{
			SHA:     "m1",
			Subject: "release: v1",
			At:      time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC),
			Branch:  "main",
			Parents: []string{"p0"},
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d commits, want %d: %+v", len(got), len(want), got)
	}

	for i := range want {
		g, w := got[i], want[i]
		if g.SHA != w.SHA || g.Subject != w.Subject || !g.At.Equal(w.At) || g.Branch != w.Branch ||
			!slices.Equal(g.Parents, w.Parents) {
			t.Errorf("commit %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestRecordedBases(t *testing.T) {
	f := newFakes(t)
	f.meta.Seed("FT-2/b", branchmeta.AfterBranch, "FT-1/a")
	f.meta.Seed("FT-3", branchmeta.AfterBranch, "FT-2/b")

	got := recordedBases(t.Context(), ".")
	if got["FT-2/b"] != "FT-1/a" || got["FT-3"] != "FT-2/b" || len(got) != 2 {
		t.Errorf("bases = %v", got)
	}
}

const numstatShow = "git show --no-renames --numstat " + numstatFormat

func scriptStandupGit(f *fakes, email, originHead string) {
	f.script("git config user.email", email)
	f.script("git remote", "origin")
	f.script(headRef, originHead)
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat,
		strings.Join([]string{"a1", "me@x.io", "2026-10-14T10:00:00Z", "p1", "FT-2/b", "work"}, "\x1f"))
	f.script("git log --format=%H "+sinceArg+" origin/main", "")
	f.script(numstatShow+" a1", "")
}

func TestCollectStandupWithoutHostReportsCommitsOnly(t *testing.T) {
	f := newFakes(t)
	scriptStandupGit(f, "me@x.io", "origin/main")

	in, err := Collect(t.Context(), Source{Dir: ".", Main: ".", Prefix: "FT"}, standupSince)
	if err != nil {
		t.Fatal(err)
	}

	if in.Host != "" || in.User.Login != "" || len(in.PRs) != 0 || len(in.Commits) != 1 || in.DefaultBranch != "main" {
		t.Errorf("input = %+v, want one commit, no host, default main", in)
	}
}

func TestCollectStandupFailsOnAFailingHost(t *testing.T) {
	f := newFakes(t)
	scriptStandupGit(f, "me@x.io", "origin/main")
	f.answers["gh api user"] = []shell.Result{{Code: 1, Stderr: "not logged in"}}

	_, err := Collect(
		t.Context(),
		Source{Dir: ".", Main: ".", Host: &forge.Host{Name: "gh", Dir: "."}},
		standupSince,
	)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("err = %v, want gh's own error", err)
	}
}

func TestCollectStandupNeedsAGitEmail(t *testing.T) {
	f := newFakes(t)
	f.script("git config user.email", "")

	_, err := Collect(t.Context(), Source{Dir: "."}, standupSince)
	if err == nil || !strings.Contains(err.Error(), "user.email") {
		t.Errorf("err = %v, want the user.email hint", err)
	}
}

func TestStandupDefaultFallsBackToTheHost(t *testing.T) {
	f := newFakes(t)
	f.script(headRef, "")
	f.script("gh repo view --json defaultBranchRef", `{"defaultBranchRef":{"name":"trunk"}}`)
	f.script("git rev-parse --verify --quiet refs/remotes/origin/trunk", "abc")

	ref, name, err := defaultRef(
		t.Context(),
		Source{Dir: ".", Host: &forge.Host{Name: "gh", Dir: "."}},
		[]string{"origin"},
	)
	if err != nil || ref != "refs/remotes/origin/trunk" || name != "trunk" {
		t.Errorf("default = %q, %q, %v, want origin's trunk", ref, name, err)
	}

	if _, _, err := defaultRef(
		t.Context(),
		Source{Dir: "."},
		[]string{"origin"},
	); err == nil ||
		!strings.Contains(err.Error(), "set-head") {
		t.Errorf("err = %v, want the set-head hint", err)
	}
}

func TestCommitFacts(t *testing.T) {
	f := newFakes(t)

	f.script(
		numstatShow+" c1 c2",
		"\x1ec1\n\n3\t1\tgo.sum\n10\t2\tapi/client.go\n-\t-\tlogo.png\n\x1ec2\n\n1\t1\tz_gen.go",
	)
	f.script("git check-attr linguist-generated -- api/client.go go.sum logo.png z_gen.go",
		"api/client.go: linguist-generated: unspecified\ngo.sum: linguist-generated: unspecified\n"+
			"logo.png: linguist-generated: set\nz_gen.go: linguist-generated: unspecified")
	f.script("git grep -l -E "+generatedHeader+" c1 c2 -- api/client.go z_gen.go", "c1:api/client.go")
	// m1 merged main's tip, m2 merged a feature branch main has since taken in
	f.script("git rev-list --first-parent --parents origin/main --not mainTip", "")
	f.script("git rev-parse --verify --quiet origin/main^{commit}", "mainTip")
	f.script("git rev-list --first-parent --parents origin/main --not feat", "t3 t2\nt2 t1\nt1 mergeOfFeat")

	commits := []Commit{
		{SHA: "c1", Parents: []string{"p"}, Branch: "FT-1/a"},
		{SHA: "c2", Parents: []string{"p"}, Branch: "FT-1/a"},
		{SHA: "m1", Parents: []string{"c2", "mainTip"}, Branch: "FT-1/a"},
		{SHA: "m2", Parents: []string{"c2", "feat"}, Branch: "FT-1/a"},
	}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", nil)

	c1 := commits[0].Files
	if len(c1) != 3 || c1[0].Path != "go.sum" || c1[0].Added != 3 || c1[1].Generated != true ||
		!c1[2].Binary || !c1[2].Generated {
		t.Errorf("c1 files = %+v", c1)
	}

	if !commits[2].BaseMerge || commits[3].BaseMerge {
		t.Errorf("base merges: m1 %v, m2 %v; want m1 only", commits[2].BaseMerge, commits[3].BaseMerge)
	}
}

func TestOnFirstParentLineOfARecordedBase(t *testing.T) {
	f := newFakes(t)
	f.script("git rev-parse --verify --quiet refs/remotes/origin/FT-1/a", "baseTip")
	f.script("git rev-list --first-parent --parents refs/remotes/origin/FT-1/a --not b2", "baseTip b2")

	commits := []Commit{{SHA: "m", Parents: []string{"x", "b2"}, Branch: "FT-2/b"}}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", map[string]string{"FT-2/b": "FT-1/a"})

	if !commits[0].BaseMerge {
		t.Error("a merge of the recorded base is a base merge")
	}

	if f.called("git rev-list --first-parent --parents origin/main") {
		t.Error("walked main after the recorded base already answered")
	}
}

func TestSamePatches(t *testing.T) {
	f := newFakes(t)
	f.script("git range-diff --no-color origin/main b0 b1",
		"1:  aaaaaaa = 1:  bbbbbbb FT-1: one\n2:  ccccccc = 2:  ddddddd FT-1: two")
	f.script("git range-diff --no-color origin/main b1 b2",
		"1:  aaaaaaa = 1:  bbbbbbb FT-1: one\n2:  ccccccc ! 2:  eeeeeee FT-1: two\n    @@ x.go\n    -a\n    +b")
	f.script("git range-diff --no-color origin/main b2 b3", "1:  aaaaaaa < -:  ------- FT-1: dropped")
	f.fail(
		"git range-diff --no-color origin/main gone b4",
		shell.Result{Code: 128, Stderr: "fatal: bad revision 'gone'"},
	)

	for _, tc := range []struct {
		before, after string
		want          string
	}{{"b0", "b1", "true"}, {"b1", "b2", "false"}, {"b2", "b3", "false"}, {"gone", "b4", "unknown"}, {"", "b5", "unknown"}} {
		got := "unknown"
		if p := samePatches(t.Context(), ".", "origin/main", tc.before, tc.after); p != nil {
			got = strconv.FormatBool(*p)
		}

		if got != tc.want {
			t.Errorf("%s..%s = %s, want %s", tc.before, tc.after, got, tc.want)
		}
	}
}

func TestCommitFactsMergeOnTheDefaultBranch(t *testing.T) {
	f := newFakes(t)
	f.fail("git merge-base --is-ancestor local origin/main", shell.Result{Code: 1})
	f.script("git merge-base --is-ancestor pushed origin/main", "")
	f.script("git rev-list --first-parent --parents origin/main --not remoteTip", "pushed mine remoteTip\nmine m0")

	commits := []Commit{
		{SHA: "local", Parents: []string{"mine", "remoteTip"}, Branch: "main"},
		{SHA: "pushed", Parents: []string{"mine", "remoteTip"}, Branch: "main"},
	}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", nil)

	if commits[0].BaseMerge || commits[1].BaseMerge {
		t.Errorf("base merge: unpushed %v, pushed %v; want neither", commits[0].BaseMerge, commits[1].BaseMerge)
	}
}

func TestCollectStandupToleratesAnUnknownParent(t *testing.T) {
	f := newFakes(t)
	f.script("git config user.email", "me@x.io")
	f.script("git remote", "origin")
	f.script(headRef, "origin/main")
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat,
		"t1\x1fme@x.io\x1f2026-10-14T10:00:00Z\x1fp1\x1fFT-2/b\x1ftest tweak\n"+
			"t0\x1fme@x.io\x1f2026-10-13T10:00:00Z\x1fp0\x1fFT-2/b\x1fold test tweak")
	f.script("git log --format=%H "+sinceArg+" origin/main", "")
	f.script(numstatShow+" t1 t0", "\x1et1\n\n1\t1\ta_test.go\n\x1et0\n\n1\t1\ta_test.go")

	f.script("git check-attr linguist-generated -- a_test.go", "a_test.go: linguist-generated: unspecified")
	f.script("git grep -l -E "+generatedHeader+" t1 t0 -- a_test.go", "")
	f.script("gh api user", `{"login":"me"}`)
	f.script(ghSearch("author:me updated:>="+ghStamp, forge.OwnFields), `[]`)
	f.script(ghSearch("reviewed-by:me updated:>="+ghStamp, forge.ForeignFields+",reviews"), `[]`)
	f.script(ghSearch("is:merged merged:>="+ghStamp, forge.ForeignFields), `[]`)
	f.fail("gh api repos/{owner}/{repo}/commits/p1/check-runs?per_page=100",
		shell.Result{Code: 1, Stderr: "No commit found for SHA: p1 (HTTP 422)"})

	in, err := Collect(
		t.Context(),
		Source{Dir: ".", Main: ".", Prefix: "FT", Host: &forge.Host{Name: "gh", Dir: "."}},
		standupSince,
	)
	if err != nil {
		t.Fatalf("a parent the host never saw failed the report: %v", err)
	}

	if in.Commits[0].ParentCI != nil {
		t.Errorf("CI = %+v, want unknown", in.Commits[0].ParentCI)
	}

	if f.called("gh api repos/{owner}/{repo}/commits/p0/") {
		t.Error("asked CI for a commit from before the period")
	}
}
