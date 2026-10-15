package standup

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// repoInput is input for one repository on a tracker.
func repoInput(repo, key, def string, commits []Commit, prs []HostPR) Input {
	in := input(commits, prs)
	in.Repo, in.Project, in.DefaultBranch = repo, repo, def
	in.Tracker = Tracker{Kind: "Jira", Prefix: "FT", Key: key}

	return in
}

// Two repositories on one tracker merge a task: both PRs, each with its
// repository, and a number repeated across them never chains.
func TestMergeJoinsTasksOnOneTracker(t *testing.T) {
	a := Group(repoInput("svc-a", "jira", "main", nil, []HostPR{own(12, "FT-1/a", "main", HostMerged)}))
	b := Group(repoInput("svc-b", "jira", "main", nil, []HostPR{
		own(12, "FT-1/b", "main", HostOpen),
		own(13, "FT-1/c", "FT-1/b", HostOpen),
	}))

	s := Merge(since, "", []Report{a, b})

	if len(s.Projects) != 1 || len(s.Projects[0].Repos) != 2 {
		t.Fatalf("projects = %+v, want one with two repos", s.Projects)
	}

	tasks := s.Projects[0].Tasks
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want FT-1 once", tasks)
	}

	var got []string
	for _, p := range tasks[0].PRs {
		got = append(got, p.Repo+"#"+strconv.Itoa(p.Number))
	}

	if want := "svc-a#12 svc-b#12 svc-b#13"; strings.Join(got, " ") != want {
		t.Errorf("PRs = %s, want %s", strings.Join(got, " "), want)
	}

	if tasks[0].PRs[0].BasePR != 0 || tasks[0].PRs[2].BasePR != 12 {
		t.Errorf("base_pr = %d, %d: a chain stays within svc-b", tasks[0].PRs[0].BasePR, tasks[0].PRs[2].BasePR)
	}

	// shipped in one repository, in review in the other: still in review
	if tasks[0].State != StateInReview {
		t.Errorf("state = %s, want %s", tasks[0].State, StateInReview)
	}
}

// Each repository's state comes from its own default branch: a commit
// landed on master in one and main in the other is shipped in both.
func TestMergeKeepsEachDefaultBranch(t *testing.T) {
	a := Group(repoInput("svc-a", "jira", "main", []Commit{commit("a1", "main", "FT-1 x", 2)}, nil))
	b := Group(repoInput("svc-b", "jira", "master", []Commit{commit("b1", "master", "FT-1 y", 3)}, nil))

	task := Merge(since, "", []Report{a, b}).Projects[0].Tasks[0]
	if task.State != StateShipped {
		t.Errorf("state = %s, want shipped", task.State)
	}

	if len(task.Commits) != 2 || task.Commits[0].Repo != "svc-a" || task.Commits[1].Repo != "svc-b" {
		t.Errorf("commits = %+v, want both, by author date", task.Commits)
	}
}

// Different tracker keys stay separate projects, even with one prefix.
func TestMergeKeepsTrackersApart(t *testing.T) {
	a := Group(repoInput("r1", "repo-1", "main", []Commit{commit("a1", "FT-1/x", "w", 2)}, nil))
	b := Group(repoInput("r2", "repo-2", "main", []Commit{commit("b1", "FT-1/x", "w", 2)}, nil))

	s := Merge(since, "", []Report{a, b})
	if len(s.Projects) != 2 || s.Projects[0].Name != "r1" || s.Projects[1].Name != "r2" {
		t.Fatalf("projects = %+v, want r1 then r2", s.Projects)
	}

	for _, p := range s.Projects {
		if len(p.Tasks) != 1 || len(p.Tasks[0].Commits) != 1 {
			t.Errorf("%s tasks = %+v, want its own FT-1", p.Name, p.Tasks)
		}
	}
}

func TestCombineStates(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{StateShipped, StateInReview, StateInReview},
		{StateInReview, StateStarted, StateStarted},
		{StateClosed, StateShipped, StateShipped},
		{StateClosed, StateClosed, StateClosed},
		{StateInProgress, StateClosed, StateInProgress},
	}

	for _, c := range cases {
		if got := combineStates(c.a, c.b); got != c.want {
			t.Errorf("combineStates(%s, %s) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

// The same reports give the same bytes.
func TestMergeIsDeterministic(t *testing.T) {
	reports := func() []Report {
		return []Report{
			Group(repoInput("svc-a", "jira", "main", []Commit{commit("a1", "FT-2/a", "w", 2)}, nil)),
			Group(repoInput("svc-b", "jira", "main", []Commit{commit("b1", "FT-2/b", "w", 2)}, nil)),
		}
	}

	first, err := json.Marshal(Merge(since, "", reports()))
	if err != nil {
		t.Fatal(err)
	}

	again, err := json.Marshal(Merge(since, "", reports()))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, again) {
		t.Errorf("merge changed between runs:\n%s\n%s", first, again)
	}
}
