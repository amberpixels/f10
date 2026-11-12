package start

import (
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func TestPickupTask(t *testing.T) {
	gh := func(body, title, branch string) forge.PR {
		return forge.PR{URL: "https://github.com/o/r/pull/30", Body: body, Title: title, Head: branch}
	}

	cases := []struct {
		name       string
		hostIssues bool
		p          forge.PR
		want       string
	}{
		{name: "closing keyword", hostIssues: true, p: gh("Some text.\n\nCloses #12", "", "alice/x"), want: "GH-12"},
		{name: "fixes with a colon", hostIssues: true, p: gh("fixes: #4", "", "x"), want: "GH-4"},
		{
			name:       "this repository's issue url",
			hostIssues: true,
			p:          gh("Resolves https://github.com/o/r/issues/8", "", "x"),
			want:       "GH-8",
		},
		{
			name:       "another repository's issue url is no task here",
			hostIssues: true,
			p:          gh("Resolves https://github.com/other/r/issues/8", "", "x"),
		},
		{name: "a keyword is ignored for a tracker that is not the host's", p: gh("Closes #12", "", "x")},
		{name: "an id in the body", p: gh("Part of GH-5.", "", "x"), want: "GH-5"},
		{name: "an id in the title", p: gh("", "GH-6: fix it", "x"), want: "GH-6"},
		{name: "an id in the branch", p: gh("", "fix it", "gh-7-fix"), want: "GH-7"},
		{name: "none", hostIssues: true, p: gh("no reference", "fix it", "alice/fix")},
		{
			name:       "a GitLab issue url",
			hostIssues: true,
			p: forge.PR{
				URL:  "https://gitlab.com/g/p/-/merge_requests/3",
				Body: "Closes https://gitlab.com/g/p/-/issues/9",
			},
			want: "GH-9",
		},
	}

	for _, c := range cases {
		if got := PickupTask("GH", c.hostIssues, c.p); got != c.want {
			t.Errorf("%s: pickupTask = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRefusePull(t *testing.T) {
	open := forge.PR{Status: forge.Open, URL: "https://gh/pull/3", Head: "x"}
	if err := RefusePull(open); err != nil {
		t.Errorf("an open PR from origin was refused: %v", err)
	}

	fork := open
	fork.Fork = true

	if err := RefusePull(fork); err == nil || !strings.Contains(err.Error(), "forks are not supported yet") {
		t.Errorf("a fork PR = %v, want the fork refusal", err)
	}

	glFork := forge.PR{
		Status: forge.Open,
		URL:    "https://gl/mr/3",
		Head:   "x",
		Fork:   true,
	}
	if err := RefusePull(glFork); err == nil {
		t.Error("a GitLab MR from another project was not refused")
	}

	merged := open
	merged.Status = forge.Merged

	if err := RefusePull(merged); err == nil || !strings.Contains(err.Error(), "no merge commit") {
		t.Errorf("a merged PR with no merge commit = %v, want it refused for that", err)
	}

	merged.Landed = "abc"

	if err := RefusePull(merged); err != nil {
		t.Errorf("a merged PR was refused: %v", err)
	}

	closed := open
	closed.Status = forge.Closed

	if err := RefusePull(closed); err == nil || !strings.Contains(err.Error(), "no head commit") {
		t.Errorf("a closed PR with no head commit = %v, want it refused for that", err)
	}

	closed.HeadSHA = "def"

	if err := RefusePull(closed); err != nil {
		t.Errorf("a closed PR was refused: %v", err)
	}

	headless := open
	headless.Head = ""

	if err := RefusePull(headless); err == nil {
		t.Error("an open PR with no head branch was not refused")
	}

	for _, state := range []string{"", "DRAFT"} {
		unknown := merged
		unknown.Status = forge.Status(state)

		if err := RefusePull(unknown); err == nil || !strings.Contains(err.Error(), "open, merged and closed") {
			t.Errorf("a PR in state %q = %v, want it refused", state, err)
		}
	}
}

// A landed PR's commit is fetched before its worktree: a closed PR's head
// through the host's ref, and one origin does not hold fails naming it.
func TestFetchPickup(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("git fetch origin", "")
	f.Script("git fetch origin refs/pull/30/head", "")
	f.Script("git rev-parse --verify --quiet def4567^{commit}", "def4567")

	closed := *testkit.PickMerged
	closed.SHA, closed.State, closed.HeadRef = "def4567", "closed without a merge", "refs/pull/30/head"

	if err := FetchPickup(t.Context(), "/code/repo", &closed); err != nil {
		t.Fatal(err)
	}

	if !f.Called("git fetch origin refs/pull/30/head") {
		t.Errorf("the closed PR's head was not fetched: %v", f.Calls)
	}

	f = testkit.NewFakes(t)
	f.Script("git fetch origin", "")
	f.Fail("git rev-parse --verify --quiet abc1234abc^{commit}", testkit.Missing)

	err := FetchPickup(t.Context(), "/code/repo", testkit.PickMerged)
	if err == nil || !strings.Contains(err.Error(), "abc1234abc") || !strings.Contains(err.Error(), "merged by alice") {
		t.Errorf("a merge commit origin lacks = %v, want it named", err)
	}

	if f.Called("git fetch origin refs/pull") {
		t.Errorf("a merged PR fetched a pull ref: %v", f.Calls)
	}

	if err := FetchPickup(t.Context(), "/code/repo", testkit.PickGH); err != nil {
		t.Errorf("an open PR's fetch: %v", err)
	}
}

// A PR naming no task exits 3, which the start skill turns into a question;
// --task wins over what the PR names; --task with no PR is refused.
func TestPickedTask(t *testing.T) {
	none := forge.PR{URL: testkit.PickGH.URL, Title: "fix login", Head: testkit.PickGH.Branch}

	_, err := PickedTask("GH", true, testkit.PickGH, none, "")

	var coder cli.ExitCoder
	if !errors.As(err, &coder) || coder.ExitCode() != ui.ExitDeclined || !strings.Contains(err.Error(), "--task") {
		t.Errorf("a PR naming no task = %v, want exit %d naming --task", err, ui.ExitDeclined)
	}

	if got, err := PickedTask(
		"GH",
		true,
		testkit.PickGH,
		forge.PR{Body: "Closes #4"},
		"GH-9",
	); got != "GH-9" ||
		err != nil {
		t.Errorf("--task with a PR = %q, %v; want GH-9", got, err)
	}

	if got, err := PickedTask(
		"GH",
		true,
		testkit.PickGH,
		forge.PR{Body: "Closes #4"},
		"",
	); got != "GH-4" ||
		err != nil {
		t.Errorf("a PR naming its task = %q, %v; want GH-4", got, err)
	}

	if _, err := PickedTask("GH", true, nil, forge.PR{}, "GH-9"); err == nil {
		t.Error("--task with no PR was not refused")
	}

	if got, err := PickedTask("GH", true, nil, forge.PR{}, ""); got != "" || err != nil {
		t.Errorf("no PR and no --task = %q, %v", got, err)
	}
}

// A picked-up PR's branch already here is brought to origin's head when it
// is behind and checked out nowhere, else noted.
func TestSyncPickup(t *testing.T) {
	const (
		ahead  = "git merge-base --is-ancestor refs/remotes/origin/b refs/heads/b"
		behind = "git merge-base --is-ancestor refs/heads/b refs/remotes/origin/b"
		force  = "git branch --force --track b origin/b"
	)

	cases := []struct {
		name             string
		upToDate, behind bool
		checkedOut       bool
		note             string
		forced           bool
	}{
		{name: "up to date", upToDate: true, behind: true},
		{name: "behind, checked out nowhere", behind: true, note: "fast-forwarded b", forced: true},
		{name: "behind, checked out", behind: true, checkedOut: true, note: "pull in its worktree"},
		{name: "diverged", note: "has diverged"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			f.Script(force, "")

			for key, ok := range map[string]bool{ahead: c.upToDate, behind: c.behind} {
				if ok {
					f.Script(key, "")
				} else {
					f.Fail(key, testkit.Missing)
				}
			}

			note, err := SyncPickup(t.Context(), "/code/repo", "b", c.checkedOut)
			if err != nil {
				t.Fatal(err)
			}

			if (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
				t.Errorf("note = %q, want %q", note, c.note)
			}

			if f.Called(force) != c.forced {
				t.Errorf("forced = %v, want %v", f.Called(force), c.forced)
			}
		})
	}
}

// Pickup reads a closing keyword exactly where finish rewrites one, so a
// task finish keeps open is the task pickup finds.
func TestClosingKeywordsAgree(t *testing.T) {
	for _, body := range []string{
		"Closes #12", "closed #12", "Closing #12", "Fixes #12", "fixing #12", "Resolves: #12",
		"Resolving #12", "Implements #12", "implemented #12",
		"Closes https://github.com/o/r/issues/12", "Resolves https://gitlab.com/o/r/-/work_items/12",
	} {
		rewritten := finish.KeepOpen(body, "12") != body

		host := "https://github.com/o/r/pull/30"
		if strings.Contains(body, "gitlab") {
			host = "https://gitlab.com/o/r/-/merge_requests/30"
		}

		picked := PickupTask("GH", true, forge.PR{URL: host, Body: body, Head: "x"}) == "GH-12"

		if !rewritten || !picked {
			t.Errorf("%q: finish rewrites %v, pickup finds %v; want both", body, rewritten, picked)
		}
	}
}
