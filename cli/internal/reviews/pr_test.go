package reviews_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestPullNumberOf(t *testing.T) {
	for token, want := range map[string]int{
		"12":                             12,
		"#12":                            12,
		"!12":                            12,
		"https://github.com/o/r/pull/12": 12,
		"https://gitlab.example.com/g/r/-/merge_requests/7": 7,
		"FT-12":                            0,
		"https://github.com/o/r/issues/12": 0,
		"":                                 0,
	} {
		if got := reviews.PullNumberOf(token); got != want {
			t.Errorf("pullNumberOf(%q) = %d, want %d", token, got, want)
		}
	}
}

// Only an open request is the branch's: a closed one before it is history.
func TestOpenPullOfSkipsClosed(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh pr list --head feat --state all --json "+forge.HeadFields+" --limit 100",
		`[{"number":3,"state":"CLOSED","createdAt":"2026-10-02T00:00:00Z"},`+
			`{"number":5,"state":"OPEN","createdAt":"2026-10-01T00:00:00Z"}]`)

	tg := &target.Target{Dir: t.TempDir(), Probes: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	if p, err := reviews.OpenPullOf(t.Context(), tg, "feat"); err != nil || p.Number != 5 {
		t.Errorf("openPullOf = %d, %v; want 5", p.Number, err)
	}
}

// A finished task's branch is gone, so its newest request, merged or not,
// stands for the change.
func TestLastPullOfTakesTheNewestAnyState(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh pr list --head REPO-7 --state all --json "+forge.HeadFields+" --limit 100",
		`[{"number":3,"state":"CLOSED","createdAt":"2026-10-01T00:00:00Z"},`+
			`{"number":5,"state":"MERGED","baseRefName":"main","createdAt":"2026-10-02T00:00:00Z"}]`)

	tg := &target.Target{Dir: t.TempDir(), Probes: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	if p, err := reviews.LastPullOf(t.Context(), tg, "REPO-7"); err != nil || p.Number != 5 || p.Base != "main" {
		t.Errorf("lastPullOf = #%d on %q, %v; want #5 on main", p.Number, p.Base, err)
	}
}

// A PR's head branch names its task by the link start writes, else by the
// id in its name.
func TestBranchTask(t *testing.T) {
	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "config", branchmeta.Task.For("picked-up"), "REPO-3")

	tg, err := target.At(t.Context(), dir, false, "pr")
	if err != nil {
		t.Fatal(err)
	}

	named := tg.Effective.IDPrefix + "-4"

	for branch, want := range map[string]string{"picked-up": "REPO-3", named + "/slug": named, "other": ""} {
		got := ""
		if r := reviews.BranchTask(t.Context(), tg, dir, branch); r != nil {
			got = r.ID
		}

		if got != want {
			t.Errorf("branchTask(%q) = %q, want %q", branch, got, want)
		}
	}
}
