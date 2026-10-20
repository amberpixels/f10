package standup

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// A Tracker is where a repository's task ids live. Repositories with the
// same Key share their ids, so the same id in each is one task; the command
// sets the key, since only it knows whether a tracker is the repository's
// own issues.
type Tracker struct {
	Kind   string `json:"kind"`
	Prefix string `json:"prefix"`
	Key    string `json:"-"`
}

// A Repo is one repository a project's activity came from.
type Repo struct {
	Name     string  `json:"name"`
	Checkout string  `json:"checkout"`
	Host     *string `json:"host"`
	User     User    `json:"user"`
}

// A Project is the activity on one tracker, across its repositories.
type Project struct {
	Name     string       `json:"name"`
	Tracker  Tracker      `json:"tracker"`
	Repos    []Repo       `json:"repos"`
	Tasks    []Task       `json:"tasks"`
	Other    Other        `json:"other"`
	Reviewed []ReviewedPR `json:"reviewed"`
}

// Standup is the report `f10 standup --json` prints: one period, the
// projects in the order their first repository was given.
type Standup struct {
	Schema   int       `json:"schema"`
	Since    time.Time `json:"since"`
	Period   string    `json:"period,omitempty"` // how since was chosen
	Days     []string  `json:"days"`             // local dates with activity; SetDays sets it
	Projects []Project `json:"projects"`
}

// Merge joins per-repository reports, read over one period, into projects,
// one per tracker key,
// and the tasks in a project by id. A task's PRs stay in chain order per
// repository, repository by repository: a base_pr is a number in one
// repository, so a chain never crosses two.
func Merge(since time.Time, period string, reports []Report) Standup {
	since = since.UTC()
	out := Standup{Schema: Schema, Since: since, Period: period, Days: []string{}, Projects: []Project{}}

	byKey := map[string]int{}
	byID := []map[string]int{}

	for _, r := range reports {
		i, ok := byKey[r.Tracker.Key]
		if !ok {
			i = len(out.Projects)
			byKey[r.Tracker.Key] = i
			byID = append(byID, map[string]int{})
			out.Projects = append(out.Projects, Project{
				Name: r.Project, Tracker: r.Tracker, Repos: []Repo{}, Tasks: []Task{},
				Other: Other{PRs: []PR{}, Commits: []Commit{}}, Reviewed: []ReviewedPR{},
			})
		}

		p := &out.Projects[i]
		p.Repos = append(p.Repos, Repo{Name: r.Repo, Checkout: r.Checkout, Host: r.Host, User: r.User})

		for _, t := range r.Tasks {
			j, seen := byID[i][t.ID]
			if !seen {
				byID[i][t.ID] = len(p.Tasks)
				p.Tasks = append(p.Tasks, t)

				continue
			}

			p.Tasks[j] = mergeTask(p.Tasks[j], t, since)
		}

		p.Other.PRs = append(p.Other.PRs, r.Other.PRs...)
		p.Other.Commits = append(p.Other.Commits, r.Other.Commits...)
		p.Reviewed = append(p.Reviewed, r.Reviewed...)
	}

	for i := range out.Projects {
		p := &out.Projects[i]
		slices.SortStableFunc(p.Tasks, func(a, b Task) int { return compareIDs(a.ID, b.ID) })
		sortCommits(p.Other.Commits)
	}

	return out
}

// mergeTask adds another repository's activity on a task. The state is
// combined, not recomputed: each repository's came from its own default
// branch, which differ between repositories.
func mergeTask(a, b Task, since time.Time) Task {
	a.PRs = append(slices.Clone(a.PRs), b.PRs...)
	a.Commits = append(slices.Clone(a.Commits), b.Commits...)
	sortCommits(a.Commits)

	a.State = combineStates(a.State, b.State)
	a.Class = taskClass(a, since, time.Time{})
	a.Title = cmp.Or(a.Title, b.Title)
	a.After = cmp.Or(a.After, b.After)

	a.Riders = slices.Clone(a.Riders)
	for _, id := range b.Riders {
		if !slices.Contains(a.Riders, id) {
			a.Riders = append(a.Riders, id)
		}
	}

	return a
}

// combineStates is taskState's rule over two repositories: the least
// advanced unfinished state, else shipped if either shipped, else closed.
func combineStates(a, b string) string {
	ra, openA := rank[a]
	rb, openB := rank[b]

	switch {
	case openA && openB:
		if rb < ra {
			return b
		}

		return a
	case openA:
		return a
	case openB:
		return b
	case a == StateShipped || b == StateShipped:
		return StateShipped
	default:
		return StateClosed
	}
}

// sortCommits orders commits by author date, then sha, as Group does.
func sortCommits(cs []Commit) {
	slices.SortFunc(cs, func(a, b Commit) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.SHA, b.SHA))
	})
}
