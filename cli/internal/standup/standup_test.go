package standup

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

var since = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)

func at(h int) time.Time { return since.Add(time.Duration(h) * time.Hour) }

func commit(sha, branch, subject string, h int) Commit {
	return Commit{SHA: sha, Branch: branch, Subject: subject, At: at(h), Parents: []string{"p"}}
}

func own(n int, head, base, status string, mods ...func(*HostPR)) HostPR {
	p := HostPR{
		Number: n, Title: "pr " + head, URL: "u", Status: status, Head: head, Base: base,
		Author: "me", CreatedAt: at(1),
	}
	if status == HostMerged {
		p.MergedAt = at(5)
	}

	for _, m := range mods {
		m(&p)
	}

	return p
}

func input(commits []Commit, prs []HostPR) Input {
	return Input{
		Prefix: "FT", Since: since, Checkout: "/repo", Host: "gh", DefaultBranch: "main",
		User: User{Email: "me@x", Login: "me"}, Commits: commits, PRs: prs,
	}
}

func taskByID(t *testing.T, r Report, id string) Task {
	t.Helper()

	for _, task := range r.Tasks {
		if task.ID == id {
			return task
		}
	}

	t.Fatalf("no task %s in %+v", id, r.Tasks)

	return Task{}
}

func numbers(prs []PR) []int {
	var out []int
	for _, p := range prs {
		out = append(out, p.Number)
	}

	return out
}

// Three PRs stacked for one task list root first; the third's host base was
// retargeted to main after its base merged, and the recorded f10 base puts
// it back in the chain.
func TestGroupChainsAStackInOneTask(t *testing.T) {
	in := input(nil, []HostPR{
		own(3, "FT-1/c", "main", HostOpen),
		own(1, "FT-1/a", "main", HostMerged),
		own(2, "FT-1/b", "FT-1/a", HostOpen, func(p *HostPR) { p.Draft = true }),
	})
	in.Bases = map[string]string{"FT-1/c": "FT-1/b"}

	task := taskByID(t, Group(in), "FT-1")

	if got := numbers(task.PRs); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("order = %v, want [1 2 3]", got)
	}

	if task.PRs[1].BasePR != 1 || task.PRs[2].BasePR != 2 {
		t.Errorf("base_pr = %d, %d, want 1, 2", task.PRs[1].BasePR, task.PRs[2].BasePR)
	}

	// merged #1, draft #2, ready #3: the draft is the least advanced
	if task.State != StateInProgress {
		t.Errorf("state = %q, want in progress", task.State)
	}

	if task.Title != "pr FT-1/a" {
		t.Errorf("title = %q, want the root's", task.Title)
	}
}

func TestGroupStackAcrossTasksRecordsAfter(t *testing.T) {
	r := Group(input(nil, []HostPR{
		own(1, "FT-1/a", "main", HostOpen),
		own(2, "FT-2/b", "FT-1/a", HostOpen),
	}))

	if got := taskByID(t, r, "FT-2"); got.After != "FT-1" || got.PRs[0].BasePR != 1 {
		t.Errorf("FT-2 = %+v, want after FT-1 and base_pr 1", got)
	}

	if got := taskByID(t, r, "FT-1"); got.After != "" {
		t.Errorf("FT-1 after = %q, want none", got.After)
	}
}

func TestGroupAttachesCommits(t *testing.T) {
	r := Group(input(
		[]Commit{
			commit("c1", "FT-1/a", "work", 2),
			commit("sq", "main", "Add a (#1)", 6),           // the squash commit
			commit("c2", "FT-1/a", "unpushed", 7),           // not in the host's list yet
			commit("d1", "main", "FT-3: direct fix", 3),     // id in the subject
			commit("x1", "main", "release v1", 4),           // no id anywhere
			commit("old", "FT-1/a", "rebased old work", -5), // authored before the period
		},
		[]HostPR{own(1, "FT-1/a", "main", HostOpen, func(p *HostPR) {
			p.Commits = []PRCommit{{SHA: "c1"}, {SHA: "old"}}
			p.MergeSHA = []string{"sq"}
		})},
	))

	pr := taskByID(t, r, "FT-1").PRs[0]

	var shas []string
	for _, c := range pr.Commits {
		shas = append(shas, c.SHA)
	}

	if !slices.Equal(shas, []string{"c1", "sq", "c2"}) {
		t.Errorf("PR commits = %v, want [c1 sq c2]", shas)
	}

	ft3 := taskByID(t, r, "FT-3")
	if len(ft3.Commits) != 1 || ft3.State != StateShipped {
		t.Errorf("FT-3 = %+v, want one commit, shipped (it is on main)", ft3)
	}

	if len(r.Other.Commits) != 1 || r.Other.Commits[0].SHA != "x1" {
		t.Errorf("other commits = %+v, want x1", r.Other.Commits)
	}
}

// A rebase merge lands the PR's commits on main under new shas; their author
// date and subject still name the PR.
func TestGroupFoldsRebaseMergedCommits(t *testing.T) {
	r := Group(input(
		[]Commit{
			commit("r1", "main", "first", 2),
			commit("r2", "main", "second", 3),
			commit("r3", "FT-1/a", "first", 2),
		},
		[]HostPR{own(1, "FT-1/a", "main", HostMerged, func(p *HostPR) {
			p.Commits = []PRCommit{{SHA: "o1", Subject: "first", At: at(2)}, {SHA: "o2", Subject: "second", At: at(3)}}
			p.MergeSHA = []string{"r2"}
		})},
	))

	if got := len(taskByID(t, r, "FT-1").PRs[0].Commits); got != 2 {
		t.Errorf("PR commits = %d, want r1 and r2", got)
	}

	// only the default branch is a rebase merge's landing: r3 is elsewhere
	if len(r.Other.Commits) != 0 || len(taskByID(t, r, "FT-1").Commits) != 1 {
		t.Errorf(
			"loose = other %+v, task %+v; want r3 alone, on the task",
			r.Other.Commits,
			taskByID(t, r, "FT-1").Commits,
		)
	}
}

func TestGroupStates(t *testing.T) {
	cases := []struct {
		name string
		pr   HostPR
		want string
	}{
		{"merged", own(1, "FT-1/a", "main", HostMerged), StateShipped},
		{"ready", own(1, "FT-1/a", "main", HostOpen), StateInReview},
		{"draft", own(1, "FT-1/a", "main", HostOpen, func(p *HostPR) { p.Draft = true }), StateInProgress},
		{"empty", own(1, "FT-1/a", "main", HostOpen, func(p *HostPR) { p.Empty, p.Draft = true, true }), StateStarted},
		{"closed", own(1, "FT-1/a", "main", HostClosed), StateClosed},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := taskByID(t, Group(input(nil, []HostPR{c.pr})), "FT-1").State; got != c.want {
				t.Errorf("state = %q, want %q", got, c.want)
			}
		})
	}

	// commits on a task branch with no PR are in progress
	r := Group(input([]Commit{commit("c1", "FT-4/x", "wip", 1)}, nil))
	if got := taskByID(t, r, "FT-4").State; got != StateInProgress {
		t.Errorf("PR-less branch state = %q, want in progress", got)
	}
}

func TestGroupSkipsOwnPRsOutsideThePeriod(t *testing.T) {
	stale := own(1, "FT-1/a", "main", HostOpen, func(p *HostPR) { p.CreatedAt = at(-48) })

	if r := Group(input(nil, []HostPR{stale})); len(r.Tasks) != 0 {
		t.Errorf("tasks = %+v, want none: nothing happened to #1 in the period", r.Tasks)
	}
}

func TestGroupOtherAndReviewed(t *testing.T) {
	r := Group(input(nil, []HostPR{
		own(1, "fix-typo", "main", HostOpen),
		{
			Number: 9, Title: "theirs", Author: "bob", Status: HostMerged, MergedBy: "ME", MergedAt: at(8),
			Reviews: []Review{{Author: "me", At: at(3)}, {Author: "me", At: at(-30)}, {Author: "carol", At: at(4)}},
		},
		{
			Number:  8,
			Title:   "untouched",
			Author:  "bob",
			Status:  HostOpen,
			Reviews: []Review{{Author: "carol", At: at(2)}},
		},
	}))

	if len(r.Other.PRs) != 1 || r.Other.PRs[0].Number != 1 {
		t.Errorf("other PRs = %+v, want #1", r.Other.PRs)
	}

	if len(r.Reviewed) != 1 {
		t.Fatalf("reviewed = %+v, want only #9", r.Reviewed)
	}

	want := []Action{{ActionReviewed, at(3)}, {ActionMerged, at(8)}}
	if got := r.Reviewed[0].Actions; !slices.Equal(got, want) {
		t.Errorf("actions = %+v, want %+v", got, want)
	}
}

func TestGroupWithoutHost(t *testing.T) {
	in := input([]Commit{commit("c1", "FT-1/a", "w", 1)}, nil)
	in.Host, in.User.Login = "", ""

	out, err := json.Marshal(Group(in))
	if err != nil {
		t.Fatal(err)
	}

	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}

	if back["host"] != nil {
		t.Errorf("host = %v, want null", back["host"])
	}

	if _, ok := back["reviewed"].([]any); !ok {
		t.Errorf("reviewed = %v, want an empty array, not null", back["reviewed"])
	}
}

// The same history in any order gives the same bytes.
func TestGroupIsDeterministic(t *testing.T) {
	commits := []Commit{
		commit("c1", "FT-1/a", "a", 2), commit("c2", "FT-2/b", "b", 2), commit("c3", "main", "FT-3 x", 4),
		commit("c4", "main", "chore", 1), commit("c5", "FT-1/a", "a2", 3),
	}
	prs := []HostPR{
		own(1, "FT-1/a", "main", HostMerged, func(p *HostPR) { p.Commits = []PRCommit{{SHA: "c1"}} }),
		own(2, "FT-2/b", "FT-1/a", HostOpen),
		own(4, "FT-1/z", "FT-1/a", HostOpen),
		{Number: 7, Author: "bob", Status: HostOpen, Reviews: []Review{{Author: "me", At: at(5)}}},
	}

	first, err := json.Marshal(Group(input(commits, prs)))
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewPCG(1, 2))

	for range 20 {
		c, p := slices.Clone(commits), slices.Clone(prs)
		rng.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
		rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })

		got, err := json.Marshal(Group(input(c, p)))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got, first) {
			t.Fatalf("shuffled input changed the output:\n%s\n%s", first, got)
		}
	}
}
