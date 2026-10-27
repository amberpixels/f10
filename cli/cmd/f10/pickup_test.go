package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/herdr"
)

func TestPullToken(t *testing.T) {
	cases := []struct {
		token, number string
		probe         bool
	}{
		{token: "https://github.com/o/r/pull/12", number: "12"},
		{token: "https://github.com/o/r/pull/12/files", number: "12"},
		{token: "https://gitlab.com/g/p/-/merge_requests/7", number: "7"},
		{token: "!7", number: "7"},
		{token: "#12", number: "12", probe: true},
		{token: "https://github.com/o/r/issues/12"},
		{token: "GH-12"},
		{token: "12"},
		{token: "#"},
		{token: "#12x"},
	}

	for _, c := range cases {
		number, probe := pullToken(c.token)
		if number != c.number || probe != c.probe {
			t.Errorf("pullToken(%q) = %q, %v; want %q, %v", c.token, number, probe, c.number, c.probe)
		}
	}
}

func TestPickupTask(t *testing.T) {
	gh := func(body, title, branch string) pull {
		return pull{URL: "https://github.com/o/r/pull/30", Body: body, Title: title, HeadRefName: branch}
	}

	cases := []struct {
		name       string
		hostIssues bool
		p          pull
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
			p: pull{
				WebURL:      "https://gitlab.com/g/p/-/merge_requests/3",
				Description: "Closes https://gitlab.com/g/p/-/issues/9",
			},
			want: "GH-9",
		},
	}

	for _, c := range cases {
		if got := pickupTask("GH", c.hostIssues, c.p); got != c.want {
			t.Errorf("%s: pickupTask = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRefusePull(t *testing.T) {
	open := pull{State: "OPEN", URL: "https://gh/pull/3", HeadRefName: "x"}
	if err := refusePull(open); err != nil {
		t.Errorf("an open PR from origin was refused: %v", err)
	}

	fork := open
	fork.IsCrossRepository = true

	if err := refusePull(fork); err == nil || !strings.Contains(err.Error(), "forks are not supported yet") {
		t.Errorf("a fork PR = %v, want the fork refusal", err)
	}

	glFork := pull{
		State:           "opened",
		WebURL:          "https://gl/mr/3",
		SourceBranch:    "x",
		SourceProjectID: 2,
		TargetProjectID: 1,
	}
	if err := refusePull(glFork); err == nil {
		t.Error("a GitLab MR from another project was not refused")
	}

	merged := open
	merged.State = "MERGED"

	if err := refusePull(merged); err == nil {
		t.Error("a merged PR was not refused")
	}
}

var pickGH = &pickup{
	number: "30",
	url:    "https://github.com/o/r/pull/30",
	branch: "alice/fix-login",
	view:   "gh pr view 30 --comments",
	diff:   "gh pr diff 30",
}

func TestPickupOf(t *testing.T) {
	p := pull{URL: pickGH.url, HeadRefName: pickGH.branch}
	if got := newHost("gh", "").pickupOf("30", p); *got != *pickGH {
		t.Errorf("pickupOf on gh = %+v", got)
	}

	gl := newHost("glab", "").pickupOf("7", pull{WebURL: "https://gl/mr/7", SourceBranch: "b"})
	if gl.view != "glab mr view 7 --comments" || gl.diff != "glab mr diff 7" || gl.branch != "b" {
		t.Errorf("pickupOf on glab = %+v", gl)
	}
}

// A picked-up PR is summarised and discussed with no driven marker, or
// planned from its diff with --plan; never shipped.
func TestPromptForAPickup(t *testing.T) {
	for _, mode := range []string{modeDefault, modeDiscuss} {
		got := prompt(mode, "GH-1", nil, "", pickGH, "")
		for _, want := range []string{"f10 task read GH-1", "gh pr view 30 --comments", "gh pr diff 30", "stop and wait"} {
			if !strings.Contains(got, want) {
				t.Errorf("prompt(%q) for a PR = %q, missing %q", mode, got, want)
			}
		}

		if strings.Contains(got, drivenMarker) || strings.Contains(got, "/f10:ship") {
			t.Errorf("prompt(%q) for a PR = %q, want plain text with no ship", mode, got)
		}
	}

	want := "/f10:plan GH-1 continue PR " + pickGH.url + ": its diff is the starting point " + drivenMarker
	if got := prompt(modePlan, "GH-1", nil, "", pickGH, ""); got != want {
		t.Errorf("prompt(plan) for a PR = %q, want %q", got, want)
	}

	if got := prompt(modeDefault, "GH-1", nil, "/p/GH-1.md", pickGH, ""); !strings.Contains(got, "/p/GH-1.md") {
		t.Errorf("prompt for a PR with a plan = %q, want the plan named", got)
	}
}

func TestRefusePickupFlags(t *testing.T) {
	if err := refusePickupFlags(modePlan, "", ""); err != nil {
		t.Errorf("--plan with a PR was refused: %v", err)
	}

	for _, c := range [][3]string{{modeLocal, "", ""}, {modeDefault, "GH-7", ""}, {modeDefault, "", "main"}} {
		if err := refusePickupFlags(c[0], c[1], c[2]); err == nil {
			t.Errorf("refusePickupFlags%v = nil, want a refusal", c)
		}
	}
}

// The PR's branch exists only on origin: start creates the local branch
// tracking it, checks it out, links it to the task, and prompts the summary.
func TestStartPicksUpAPR(t *testing.T) {
	const wt = "/code/repo.alice-fix-login"

	f := newFakes(t)
	f.fail("git rev-parse --verify --quiet refs/heads/alice/fix-login", missing)
	f.script("git branch --track alice/fix-login origin/alice/fix-login", "")
	f.script(worktrees, porcelain(), porcelain("alice/fix-login", wt))
	f.script("git worktree add "+wt+" alice/fix-login", "")
	f.script("git config branch.alice/fix-login.f10-task GH-1", "")
	f.script("herdr worktree open --cwd /code/repo --path "+wt+" --label alice/fix-login", opened)
	f.script("herdr agent start "+herdr.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+herdr.AgentName("GH-1", "", wt)+" "+prompt(modeDefault, "GH-1", nil, "", pickGH, ""),
		"{}",
	)

	var out bytes.Buffer

	if err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, pick: pickGH}); err != nil {
		t.Fatal(err)
	}

	tracked := indexAfter(f.calls, "git branch --track alice/fix-login origin/alice/fix-login", -1)
	if tracked < 0 || indexAfter(f.calls, "git worktree add "+wt+" alice/fix-login", tracked) < 0 {
		t.Errorf("the tracking branch was not created before its worktree: %v", f.calls)
	}

	if !f.called("git config branch.alice/fix-login.f10-task GH-1") {
		t.Errorf("the branch was not linked to its task: %v", f.calls)
	}

	if f.called("git for-each-ref") || f.called("git symbolic-ref") {
		t.Errorf("a picked-up PR looked for a task branch or a base: %v", f.calls)
	}

	for _, want := range []string{"pr:", pickGH.url, "branch:", "alice/fix-login", "tracking origin/alice/fix-login"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// Plain `start <id>` whose branch only origin has tracks it rather than
// creating a second branch from main.
func TestStartTracksAnOriginOnlyBranch(t *testing.T) {
	const wt = "/code/repo.GH-1-slug"

	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsOrigin, "refs/remotes/origin/GH-1/slug\n")
	f.script("git branch --track GH-1/slug origin/GH-1/slug", "")
	f.script(worktrees, porcelain(), porcelain("GH-1/slug", wt))
	f.script("git worktree add "+wt+" GH-1/slug", "")
	f.script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/slug", opened)
	f.script("herdr agent start "+herdr.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+herdr.AgentName("GH-1", "", wt)+" "+prompt(modeDefault, "GH-1", nil, "", nil, ""),
		"{}",
	)

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("git branch --track GH-1/slug origin/GH-1/slug") || f.called("git worktree add -b") {
		t.Errorf("origin's branch was not tracked: %v", f.calls)
	}
}

// A branch linked to the task wins over the name patterns, local or on
// origin; two linked is an error; a suffix skips the link.
func TestExistingBranchFindsLinksAndOrigin(t *testing.T) {
	f := newFakes(t)
	f.script(linkedKeys, "branch.alice/fix.f10-task GH-1\nbranch.other.f10-task GH-2")
	f.script(refsLocal, heads("GH-1"))

	got, err := existingBranch(t.Context(), "/code/repo", "GH-1", "GH-1", "")
	if err != nil || got != (foundBranch{name: "alice/fix"}) {
		t.Errorf("a linked branch = %+v, %v; want alice/fix", got, err)
	}

	f.script(refsLocal, heads("GH-1", "GH-1-v2"))

	got, err = existingBranch(t.Context(), "/code/repo", "GH-1-v2", "GH-1", "v2")
	if err != nil || got.name != "GH-1-v2" {
		t.Errorf("a suffix = %+v, %v; want the link skipped", got, err)
	}

	f.script(linkedKeys, "branch.a.f10-task GH-1\nbranch.b.f10-task gh-1")

	if _, err = existingBranch(t.Context(), "/code/repo", "GH-1", "GH-1", ""); err == nil {
		t.Error("two linked branches were not an error")
	}

	f.script(linkedKeys, "")
	f.script(refsLocal, "")
	f.script(refsOrigin, "refs/remotes/origin/GH-1/a\nrefs/remotes/origin/GH-1/b\n")

	if _, err = existingBranch(t.Context(), "/code/repo", "GH-1", "GH-1", ""); err == nil {
		t.Error("two task branches on origin were not an error")
	}

	f.script(refsOrigin, "refs/remotes/origin/GH-1/a\n")

	got, err = existingBranch(t.Context(), "/code/repo", "GH-1", "GH-1", "")
	if err != nil || got != (foundBranch{name: "GH-1/a", remote: true}) {
		t.Errorf("an origin-only branch = %+v, %v", got, err)
	}

	// a branch only origin has is checked out nowhere: nothing to forward to
	f.script(worktrees, porcelain())

	name, path, err := taskCheckout(t.Context(), "/code/repo", nil, gh1, "")
	if name != "" || path != "" || err != nil {
		t.Errorf("taskCheckout on an origin-only branch = %q, %q, %v", name, path, err)
	}
}

func TestLinkedBranchCountsForAfter(t *testing.T) {
	f := newFakes(t)
	f.script(linkedKeys, "branch.alice/base.f10-task GH-7")

	if got, err := taskBranch(t.Context(), "/code/repo", "GH-7"); err != nil || got != "alice/base" {
		t.Errorf("taskBranch on a linked branch = %q, %v", got, err)
	}

	if ok, err := hasTaskBranch(t.Context(), "/code/repo", "GH-7"); !ok || err != nil {
		t.Errorf("hasTaskBranch on a linked branch = %v, %v", ok, err)
	}
}

// A PR naming no task exits 3, which the start skill turns into a question;
// --task wins over what the PR names; --task with no PR is refused.
func TestPickedTask(t *testing.T) {
	none := pull{URL: pickGH.url, Title: "fix login", HeadRefName: pickGH.branch}

	_, err := pickedTask("GH", true, pickGH, none, "")

	var coder cli.ExitCoder
	if !errors.As(err, &coder) || coder.ExitCode() != exitNoTask || !strings.Contains(err.Error(), "--task") {
		t.Errorf("a PR naming no task = %v, want exit %d naming --task", err, exitNoTask)
	}

	if got, err := pickedTask("GH", true, pickGH, pull{Body: "Closes #4"}, "GH-9"); got != "GH-9" || err != nil {
		t.Errorf("--task with a PR = %q, %v; want GH-9", got, err)
	}

	if got, err := pickedTask("GH", true, pickGH, pull{Body: "Closes #4"}, ""); got != "GH-4" || err != nil {
		t.Errorf("a PR naming its task = %q, %v; want GH-4", got, err)
	}

	if _, err := pickedTask("GH", true, nil, pull{}, "GH-9"); err == nil {
		t.Error("--task with no PR was not refused")
	}

	if got, err := pickedTask("GH", true, nil, pull{}, ""); got != "" || err != nil {
		t.Errorf("no PR and no --task = %q, %v", got, err)
	}
}

// Only the host's "no such PR" turns a #n back into a task id; a login or
// network failure is the error.
func TestNoSuchPull(t *testing.T) {
	no := errors.New("gh: GraphQL: Could not resolve to a PullRequest with the number of 85. (repository.pullRequest)")
	if !noSuchPull(no) {
		t.Error("gh's no-such-PR answer was not recognised")
	}

	if noSuchPull(errors.New("gh: To get started with GitHub CLI, please run:  gh auth login")) {
		t.Error("a logged-out gh was read as no PR")
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
			f := newFakes(t)
			f.script(force, "")

			for key, ok := range map[string]bool{ahead: c.upToDate, behind: c.behind} {
				if ok {
					f.script(key, "")
				} else {
					f.fail(key, missing)
				}
			}

			note, err := syncPickup(t.Context(), "/code/repo", "b", c.checkedOut)
			if err != nil {
				t.Fatal(err)
			}

			if (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
				t.Errorf("note = %q, want %q", note, c.note)
			}

			if f.called(force) != c.forced {
				t.Errorf("forced = %v, want %v", f.called(force), c.forced)
			}
		})
	}
}
