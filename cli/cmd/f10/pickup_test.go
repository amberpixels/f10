package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
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

	if err := refusePull(merged); err == nil || !strings.Contains(err.Error(), "no merge commit") {
		t.Errorf("a merged PR with no merge commit = %v, want it refused for that", err)
	}

	merged.MergeCommit.OID = "abc"

	if err := refusePull(merged); err != nil {
		t.Errorf("a merged PR was refused: %v", err)
	}

	closed := open
	closed.State = "CLOSED"

	if err := refusePull(closed); err == nil || !strings.Contains(err.Error(), "no head commit") {
		t.Errorf("a closed PR with no head commit = %v, want it refused for that", err)
	}

	closed.HeadRefOID = "def"

	if err := refusePull(closed); err != nil {
		t.Errorf("a closed PR was refused: %v", err)
	}

	headless := open
	headless.HeadRefName = ""

	if err := refusePull(headless); err == nil {
		t.Error("an open PR with no head branch was not refused")
	}

	for _, state := range []string{"", "DRAFT"} {
		unknown := merged
		unknown.State = state

		if err := refusePull(unknown); err == nil || !strings.Contains(err.Error(), "open, merged and closed") {
			t.Errorf("a PR in state %q = %v, want it refused", state, err)
		}
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

	if got := newHost("gh", "").pickupOf("30", mergedPull()); *got != *pickMerged {
		t.Errorf("pickupOf on a merged PR = %+v, want %+v", got, pickMerged)
	}

	closed := p
	closed.State, closed.HeadRefOID = "CLOSED", "def4567"
	closed.ClosedAt = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	got := newHost("gh", "").pickupOf("30", closed)
	if got.sha != "def4567" || got.headRef != "refs/pull/30/head" ||
		got.state != "closed without a merge on 2026-10-13" {
		t.Errorf("pickupOf on a closed PR = %+v", got)
	}

	// glab names nobody for a squash it did itself, and keeps its MR heads under its own ref
	glMerged := pull{WebURL: "https://gl/mr/7", SourceBranch: "b", State: "merged", SquashCommitSHA: "aaa"}
	got = newHost("glab", "").pickupOf("7", glMerged)
	if got.sha != "aaa" || got.state != "merged" || got.headRef != "" {
		t.Errorf("pickupOf on a merged MR = %+v", got)
	}

	glClosed := pull{WebURL: "https://gl/mr/7", SourceBranch: "b", State: "closed", SHA: "bbb"}
	got = newHost("glab", "").pickupOf("7", glClosed)
	if got.sha != "bbb" || got.headRef != "refs/merge-requests/7/head" {
		t.Errorf("pickupOf on a closed MR = %+v", got)
	}
}

// mergedPull is pickGH's PR merged by alice, as gh answers for it.
func mergedPull() pull {
	p := pull{URL: pickGH.url, HeadRefName: pickGH.branch, State: "MERGED", HeadRefOID: "def4567"}
	p.MergeCommit.OID = "abc1234abc"
	p.MergedBy.Login = "alice"
	p.MergedAt = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)

	return p
}

var pickMerged = &pickup{
	number: "30",
	url:    pickGH.url,
	branch: pickGH.branch,
	view:   pickGH.view,
	diff:   pickGH.diff,
	sha:    "abc1234abc",
	state:  "merged by alice on 2026-10-12",
}

// A landed PR's commit is fetched before its worktree: a closed PR's head
// through the host's ref, and one origin does not hold fails naming it.
func TestFetchPickup(t *testing.T) {
	f := newFakes(t)
	f.script("git fetch origin", "")
	f.script("git fetch origin refs/pull/30/head", "")
	f.script("git rev-parse --verify --quiet def4567^{commit}", "def4567")

	closed := *pickMerged
	closed.sha, closed.state, closed.headRef = "def4567", "closed without a merge", "refs/pull/30/head"

	if err := fetchPickup(t.Context(), "/code/repo", &closed); err != nil {
		t.Fatal(err)
	}

	if !f.called("git fetch origin refs/pull/30/head") {
		t.Errorf("the closed PR's head was not fetched: %v", f.calls)
	}

	f = newFakes(t)
	f.script("git fetch origin", "")
	f.fail("git rev-parse --verify --quiet abc1234abc^{commit}", missing)

	err := fetchPickup(t.Context(), "/code/repo", pickMerged)
	if err == nil || !strings.Contains(err.Error(), "abc1234abc") || !strings.Contains(err.Error(), "merged by alice") {
		t.Errorf("a merge commit origin lacks = %v, want it named", err)
	}

	if f.called("git fetch origin refs/pull") {
		t.Errorf("a merged PR fetched a pull ref: %v", f.calls)
	}

	if err := fetchPickup(t.Context(), "/code/repo", pickGH); err != nil {
		t.Errorf("an open PR's fetch: %v", err)
	}
}

// A picked-up PR is summarised and discussed with no driven marker, or
// planned from its diff with --plan; never shipped.
func TestPromptForAPickup(t *testing.T) {
	for _, mode := range []string{modeDefault, modeDiscuss} {
		got := prompt(mode, shipFlow{}, "GH-1", nil, "", pickGH, "")
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
	if got := prompt(modePlan, shipFlow{}, "GH-1", nil, "", pickGH, ""); got != want {
		t.Errorf("prompt(plan) for a PR = %q, want %q", got, want)
	}

	if got := prompt(
		modeDefault,
		shipFlow{},
		"GH-1",
		nil,
		"/p/GH-1.md",
		pickGH,
		"",
	); !strings.Contains(
		got,
		"/p/GH-1.md",
	) {
		t.Errorf("prompt for a PR with a plan = %q, want the plan named", got)
	}

	got := prompt(modeDefault, shipFlow{}, "GH-1", nil, "", pickMerged, "")
	if !strings.Contains(got, "that the PR is merged by alice on 2026-10-12 and what its diff does") ||
		strings.Contains(got, "open threads") {
		t.Errorf("prompt for a merged PR = %q, want its state named before the diff", got)
	}
}

// --plan on a landed PR is refused: going on from it is a task of its own.
func TestRefuseLandedPlan(t *testing.T) {
	err := refuseLandedPlan(modePlan, pickMerged)
	if err == nil || !strings.Contains(err.Error(), "merged by alice") {
		t.Errorf("--plan on a merged PR = %v, want a refusal naming its state", err)
	}

	for _, c := range []struct {
		mode string
		pick *pickup
	}{{modeDefault, pickMerged}, {modeDiscuss, pickMerged}, {modePlan, pickGH}, {modePlan, nil}} {
		if err := refuseLandedPlan(c.mode, c.pick); err != nil {
			t.Errorf("refuseLandedPlan(%q, %v) = %v, want nil", c.mode, c.pick, err)
		}
	}
}

func TestRefusePickupFlags(t *testing.T) {
	if err := refusePickupFlags("", "", ""); err != nil {
		t.Errorf("a PR with no flow named was refused: %v", err)
	}

	for _, c := range [][3]string{{"local", "", ""}, {"direct", "", ""}, {"", "GH-7", ""}, {"", "", "main"}} {
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
	f.script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			wt,
		)+" "+prompt(
			modeDefault,
			shipFlow{},
			"GH-1",
			nil,
			"",
			pickGH,
			"",
		),
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

// A merged PR is checked out at its merge commit on the task's own branch,
// tracking nothing: origin's branches are never looked at, the branch is
// handed back from the watcher, and the agent is told the PR's state.
func TestStartPicksUpAMergedPR(t *testing.T) {
	const wt = "/code/repo.GH-1-fix-login"

	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1/fix-login", wt))
	f.script("git worktree add -b GH-1/fix-login "+wt+" abc1234abc", "")
	f.script("git config branch.GH-1/fix-login.f10-task GH-1", "")
	f.script("git config branch.GH-1/fix-login.f10-watch false", "")
	f.script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/fix-login", opened)
	f.script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", wt)+" "+
			prompt(modeDefault, shipFlow{}, "GH-1", nil, "", pickMerged, ""),
		"{}",
	)

	var out bytes.Buffer

	in := startInput{main: "/code/repo", task: gh1, pick: pickMerged, title: titled("Fix login", nil)}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	for _, never := range []string{"git branch --track", "git merge-base", "git symbolic-ref", refsOrigin} {
		if f.called(never) {
			t.Errorf("a merged PR ran %q: %v", never, f.calls)
		}
	}

	if !f.called("git config branch.GH-1/fix-login.f10-watch false") {
		t.Errorf("the branch was not handed back from the watcher: %v", f.calls)
	}

	for _, want := range []string{
		"pr:", pickGH.url, "branch:", "GH-1/fix-login",
		"merged by alice on 2026-10-12: GH-1/fix-login is local only at abc1234",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A branch the task already has here is reused as it is, the landed
// commit noted as ignored, and left watched: its own work may be live.
func TestStartPicksUpAMergedPROnItsOwnBranch(t *testing.T) {
	const wt = "/code/repo.GH-1-fix-login"

	f := newFakes(t)
	f.script(linkedKeys, "branch.GH-1/fix-login.f10-task GH-1")
	f.script(worktrees, porcelain("GH-1/fix-login", wt))
	f.script("git config branch.GH-1/fix-login.f10-task GH-1", "")
	f.script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/fix-login", opened)
	f.script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", wt)+" "+
			prompt(modeDefault, shipFlow{}, "GH-1", nil, "", pickMerged, ""),
		"{}",
	)

	var out bytes.Buffer

	in := startInput{main: "/code/repo", task: gh1, pick: pickMerged, title: titled("Fix login", nil)}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if f.called("git worktree add") || f.called("git config branch.GH-1/fix-login.f10-watch") {
		t.Errorf("the task's own worktree was not reused as it is: %v", f.calls)
	}

	for _, want := range []string{"reused the existing worktree", "landed commit ignored", "stays watched"} {
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
	f.script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			wt,
		)+" "+prompt(
			modeDefault,
			shipFlow{},
			"GH-1",
			nil,
			"",
			nil,
			"",
		),
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
