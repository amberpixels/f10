package standup

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// A Tracker is where a repository's task ids live; repositories with the same
// Key share ids. The command sets Key, since only it knows whether a tracker
// is the repository's own issues.
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
	Drives   []Drive      `json:"drives"`
}

// Standup is the report `f10 standup --json` prints: one period, the
// projects in the order their first repository was given.
type Standup struct {
	Schema   int       `json:"schema"`
	Since    time.Time `json:"since"`
	Period   string    `json:"period,omitempty"` // how since was chosen
	Days     []string  `json:"days"`             // local dates with activity; SetDays sets it
	Projects []Project `json:"projects"`
	// Ledger is set when a ledger was read: its half is this machine's alone
	Ledger *LedgerNote `json:"ledger,omitempty"`
	// Warnings name what the host failed to give after a retry. The report
	// printed without it
	Warnings []string `json:"warnings"`
}

// Merge joins per-repository reports into projects, one per tracker key, and
// tasks by id. PRs stay in chain order repository by repository: a base_pr is
// a number in one repository, so a chain never crosses two.
//
//nolint:lostfield // Ledger is the command's, set once it knows a ledger exists
func Merge(since time.Time, period string, reports []Report) Standup {
	since = since.UTC()
	out := Standup{
		Schema:   Schema,
		Since:    since,
		Period:   period,
		Days:     []string{},
		Projects: []Project{},
		Warnings: []string{},
	}

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
				Other: Other{PRs: []PR{}, Commits: []Commit{}}, Reviewed: []ReviewedPR{}, Drives: []Drive{},
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
		p.Drives = append(p.Drives, r.Drives...)
		out.Warnings = append(out.Warnings, r.Warnings...)
	}

	for i := range out.Projects {
		p := &out.Projects[i]
		slices.SortStableFunc(p.Tasks, func(a, b Task) int { return compareIDs(a.ID, b.ID) })
		sortCommits(p.Other.Commits)
	}

	return out
}

// mergeTask adds another repository's activity. The state is combined, not
// recomputed, since each repository's came from its own default branch.
func mergeTask(a, b Task, since time.Time) Task {
	a.PRs = append(slices.Clone(a.PRs), b.PRs...)
	a.Commits = append(slices.Clone(a.Commits), b.Commits...)
	sortCommits(a.Commits)

	a.State = combineStates(a.State, b.State)
	a.Class = taskClass(a, since, time.Time{})
	a.Title = cmp.Or(a.Title, b.Title)
	a.After = cmp.Or(a.After, b.After)

	a.Activity = append(slices.Clone(a.Activity), b.Activity...)
	slices.SortStableFunc(a.Activity, func(x, y Activity) int { return x.At.Compare(y.At) })
	a.Blocked = cmp.Or(a.Blocked, b.Blocked)
	a.Stage = cmp.Or(a.Stage, b.Stage)
	a.Driven = max(a.Driven, b.Driven)
	a.ActiveMinutes += b.ActiveMinutes
	a.Ledger = a.Ledger || b.Ledger

	a.Riders = slices.Clone(a.Riders)
	for _, id := range b.Riders {
		if !slices.Contains(a.Riders, id) {
			a.Riders = append(a.Riders, id)
		}
	}

	return a
}

// combineStates is taskState's rule over two repositories.
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

func sortCommits(cs []Commit) {
	slices.SortFunc(cs, func(a, b Commit) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.SHA, b.SHA))
	})
}
