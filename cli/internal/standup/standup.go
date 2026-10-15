// Package standup groups one person's activity in a checkout by task: the
// commits they authored and the PRs or MRs they opened, reviewed or merged,
// collected by `f10 standup` from git and the host CLI. Group is a pure
// function, so the same history gives the same report byte for byte, and
// reports from several checkouts can later be merged by task id.
//
// The JSON shape is a contract for the /f10:standup skill and outside
// consumers, documented in docs/cli.md. A breaking change bumps Schema.
package standup

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/ref"
)

// Schema is the version of the report's JSON shape.
const Schema = 1

// Where a PR or a task ended the period, least advanced first. Closed is a
// PR closed unmerged; it never decides a task's state while anything else
// does.
const (
	StateStarted    = "started"
	StateInProgress = "in progress"
	StateInReview   = "in review"
	StateShipped    = "shipped"
	StateClosed     = "closed"
)

// The host states a PR arrives in, normalised from gh's and glab's spellings.
const (
	HostOpen   = "open"
	HostMerged = "merged"
	HostClosed = "closed"
)

// What the user did to someone else's PR.
const (
	ActionReviewed = "reviewed"
	ActionMerged   = "merged"
)

// A Commit is one commit the user authored in the period.
type Commit struct {
	SHA     string    `json:"sha"`
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`     // author date
	Branch  string    `json:"branch"` // the branch it was reached from, remote prefix dropped
	Parents []string  `json:"parents"`
	Class   string    `json:"class"`
	Upkeep  string    `json:"upkeep,omitempty"`

	// What upkeep.go decides the class from, collected beside the commit.
	Files     []File `json:"-"`
	BaseMerge bool   `json:"-"` // a merge whose second parent is on the base's first-parent line
	ParentCI  *CI    `json:"-"` // nil unless asked and the host had checks
}

// A Review is one review event on a PR: a submitted review, an approval or
// a comment, by anyone. Group keeps the user's.
type Review struct {
	Author string
	At     time.Time
}

// A HostPR is a PR or MR as the host reported it, before grouping.
type HostPR struct {
	Number    int
	Title     string
	URL       string
	Status    string // HostOpen, HostMerged or HostClosed
	Draft     bool
	Empty     bool // no changes yet: no changed files on GitHub, no commits on GitLab
	Head      string
	Base      string
	Author    string
	CreatedAt time.Time
	MergedAt  time.Time // zero unless merged
	MergedBy  string
	Commits   []PRCommit // the commits the host lists for it; read for the user's own PRs only
	MergeSHA  []string   // the merge and squash commits it landed as
	Reviews   []Review   // read for other people's PRs only

	ForcePushes []ForcePush // the user's own, on their own PRs
}

// A PRCommit is one commit a PR lists. A rebase merge lands it on the
// default branch under a new sha, keeping its author date and subject.
type PRCommit struct {
	SHA     string
	Subject string
	At      time.Time
}

// User is who the report is about: a git identity and a host login.
type User struct {
	Email string `json:"email"`
	Login string `json:"login,omitempty"`
}

// Input is everything Group needs, already collected.
type Input struct {
	Prefix        string // task id prefix; empty matches no id
	Since         time.Time
	Checkout      string
	Host          string // "gh", "glab", or "" with no host
	DefaultBranch string // local name, "main"
	User          User
	Commits       []Commit          // the user's, from git
	PRs           []HostPR          // the user's own and the ones they reviewed or merged
	Bases         map[string]string // head branch to the base branch `f10 start --after` recorded
	UpkeepPaths   []string          // the project's globs, on top of the default upkeep paths
}

// PR is one of the user's PRs in the report.
type PR struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	Head      string     `json:"head"`
	Base      string     `json:"base"`
	BasePR    int        `json:"base_pr,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	Commits   []Commit   `json:"commits"`

	ForcePushes []ForcePush `json:"force_pushes"`
}

// Task is the activity carrying one task id.
type Task struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	State   string   `json:"state"`
	Class   string   `json:"class"`
	After   string   `json:"after,omitempty"`
	Days    []Day    `json:"days"` // the command sets it
	PRs     []PR     `json:"prs"`
	Commits []Commit `json:"commits"` // commits on no PR of the user's
}

// Other is the user's activity carrying no task id.
type Other struct {
	PRs     []PR     `json:"prs"`
	Commits []Commit `json:"commits"`
}

// Action is one thing the user did to someone else's PR.
type Action struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

// ReviewedPR is someone else's PR the user reviewed or merged.
type ReviewedPR struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	State   string   `json:"state"`
	Author  string   `json:"author"`
	Actions []Action `json:"actions"`
	Days    []string `json:"days"` // local dates of the actions; the command sets it
}

// Report is the standup for one checkout.
type Report struct {
	Schema   int          `json:"schema"`
	Since    time.Time    `json:"since"`
	Period   string       `json:"period,omitempty"` // how since was chosen; the command sets it
	Days     []string     `json:"days"`             // local dates with activity; the command sets it
	Checkout string       `json:"checkout"`
	Host     *string      `json:"host"`
	User     User         `json:"user"`
	Tasks    []Task       `json:"tasks"`
	Other    Other        `json:"other"`
	Reviewed []ReviewedPR `json:"reviewed"`
}

// Group sorts the collected activity into tasks.
func Group(in Input) Report {
	since := in.Since.UTC()

	rep := Report{
		Schema:   Schema,
		Since:    since,
		Days:     []string{},
		Checkout: in.Checkout,
		User:     in.User,
		Tasks:    []Task{},
		Other:    Other{PRs: []PR{}, Commits: []Commit{}},
		Reviewed: []ReviewedPR{},
	}

	if in.Host != "" {
		rep.Host = &in.Host
	}

	own, foreign := splitPRs(in.PRs, in.User.Login)
	commits := periodCommits(in.Commits, since)

	for i := range commits {
		commits[i].Class, commits[i].Upkeep = classifyCommit(commits[i], in.UpkeepPaths)
	}

	// a commit belongs to the PR whose commit list or merge commit names it;
	// a commit on the default branch to the merged PR listing its author date
	// and subject, which is what a rebase merge keeps; else to an open PR
	// whose head is the branch it was reached from
	attached := make([][]Commit, len(own))
	bySHA := map[string]int{}
	rebased := map[string]int{}
	openHead := map[string]int{}

	for i, p := range own {
		shas := slices.Clone(p.MergeSHA)

		for _, c := range p.Commits {
			shas = append(shas, c.SHA)

			if _, ok := rebased[landedKey(c.At, c.Subject)]; !ok && p.Status == HostMerged {
				rebased[landedKey(c.At, c.Subject)] = i
			}
		}

		for _, sha := range shas {
			if _, ok := bySHA[sha]; !ok {
				bySHA[sha] = i
			}
		}

		if p.Status == HostOpen {
			openHead[p.Head] = i
		}
	}

	var loose []Commit

	for _, c := range commits {
		if i, ok := bySHA[c.SHA]; ok {
			attached[i] = append(attached[i], c)
		} else if i, ok := rebased[landedKey(c.At, c.Subject)]; ok && c.Branch == in.DefaultBranch {
			attached[i] = append(attached[i], c)
		} else if i, ok := openHead[c.Branch]; ok && c.Branch != in.DefaultBranch {
			attached[i] = append(attached[i], c)
		} else {
			loose = append(loose, c)
		}
	}

	basePR := chainBases(own, in.Bases, in.DefaultBranch)
	tasks := map[string]*Task{}
	taskOf := map[int]string{} // PR number to task id, for after

	task := func(id string) *Task {
		if t, ok := tasks[id]; ok {
			return t
		}

		t := &Task{ID: id, PRs: []PR{}, Commits: []Commit{}}
		tasks[id] = t

		return t
	}

	for i, p := range own {
		pushes := periodPushes(p.ForcePushes, since)
		if !inPeriod(p, since, len(attached[i])+len(pushes) > 0) {
			continue
		}

		out := PR{
			Number: p.Number, Title: p.Title, URL: p.URL, State: prState(p), Draft: p.Draft,
			Head: p.Head, Base: p.Base, BasePR: basePR[p.Number], CreatedAt: p.CreatedAt.UTC(),
			Commits: nonNil(attached[i]), ForcePushes: pushes,
		}

		if !p.MergedAt.IsZero() {
			at := p.MergedAt.UTC()
			out.MergedAt = &at
		}

		id := cmp.Or(ref.Match(in.Prefix, p.Head), ref.Match(in.Prefix, p.Title))
		if id == "" {
			rep.Other.PRs = append(rep.Other.PRs, out)

			continue
		}

		taskOf[p.Number] = id
		t := task(id)
		t.PRs = append(t.PRs, out)
	}

	for _, c := range loose {
		id := cmp.Or(ref.Match(in.Prefix, c.Branch), ref.Match(in.Prefix, c.Subject))
		if id == "" {
			rep.Other.Commits = append(rep.Other.Commits, c)

			continue
		}

		t := task(id)
		t.Commits = append(t.Commits, c)
	}

	// a base PR outside the period still names the task this one comes after
	for _, p := range own {
		if _, ok := taskOf[p.Number]; !ok {
			if id := cmp.Or(ref.Match(in.Prefix, p.Head), ref.Match(in.Prefix, p.Title)); id != "" {
				taskOf[p.Number] = id
			}
		}
	}

	for _, t := range tasks {
		t.PRs = chainOrder(t.PRs)
		t.State = taskState(*t, in.DefaultBranch)
		t.Class = taskClass(*t, since, time.Time{})

		if len(t.PRs) > 0 {
			root := t.PRs[0]
			t.Title = root.Title

			if base, ok := taskOf[root.BasePR]; ok && base != t.ID {
				t.After = base
			}
		}

		rep.Tasks = append(rep.Tasks, *t)
	}

	slices.SortFunc(rep.Tasks, func(a, b Task) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(rep.Other.PRs, func(a, b PR) int { return a.Number - b.Number })

	rep.Reviewed = reviewed(foreign, in.User.Login, since)

	return rep
}

func landedKey(at time.Time, subject string) string {
	return at.UTC().Format(time.RFC3339) + "\x00" + subject
}

// splitPRs dedupes by number and splits the user's own PRs from others'.
func splitPRs(prs []HostPR, login string) ([]HostPR, []HostPR) {
	sorted := slices.Clone(prs)
	slices.SortStableFunc(sorted, func(a, b HostPR) int { return a.Number - b.Number })
	sorted = slices.CompactFunc(sorted, func(a, b HostPR) bool { return a.Number == b.Number })

	var own, foreign []HostPR

	for _, p := range sorted {
		if login != "" && strings.EqualFold(p.Author, login) {
			own = append(own, p)
		} else {
			foreign = append(foreign, p)
		}
	}

	return own, foreign
}

// periodCommits keeps the commits authored in the period, sorted. Git's
// --since reads the committer date, which a rebase or a merge replay moves
// to today; the author date is when the work was done.
func periodCommits(commits []Commit, since time.Time) []Commit {
	var out []Commit

	for _, c := range commits {
		if c.At.Before(since) {
			continue
		}

		c.At = c.At.UTC()
		c.Parents = nonNil(c.Parents)
		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b Commit) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.SHA, b.SHA))
	})

	return out
}

// periodPushes keeps the force-pushes made in the period, classified and
// sorted.
func periodPushes(pushes []ForcePush, since time.Time) []ForcePush {
	out := []ForcePush{}

	for _, p := range pushes {
		if p.At.Before(since) {
			continue
		}

		p.At = p.At.UTC()
		p.Class, p.Upkeep = classifyPush(p)
		out = append(out, p)
	}

	slices.SortFunc(out, func(a, b ForcePush) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.After, b.After))
	})

	return out
}

// inPeriod is an own PR the user did something with: opened or merged in
// the period, or carrying a commit or a force-push of theirs from it. A
// rebase keeps author dates, so a rebase-only day shows as a force-push
// alone.
func inPeriod(p HostPR, since time.Time, active bool) bool {
	return active || !p.CreatedAt.Before(since) || (!p.MergedAt.IsZero() && !p.MergedAt.Before(since))
}

// chainBases maps each PR to the PR whose head is its base. The base f10
// recorded wins over the host's, which the host retargets to the default
// branch once the base PR merges.
func chainBases(own []HostPR, bases map[string]string, def string) map[int]int {
	byHead := map[string]int{}

	for _, p := range own {
		// the newest PR for a head is the one a stack sits on
		byHead[p.Head] = max(byHead[p.Head], p.Number)
	}

	out := map[int]int{}

	for _, p := range own {
		base := cmp.Or(bases[p.Head], p.Base)
		if base == "" || base == def {
			continue
		}

		if n, ok := byHead[base]; ok && n != p.Number {
			out[p.Number] = n
		}
	}

	return out
}

// chainOrder lists a task's PRs root first, each followed by the PRs
// chained under it; siblings and roots by number.
func chainOrder(prs []PR) []PR {
	in := map[int]bool{}
	for _, p := range prs {
		in[p.Number] = true
	}

	children := map[int][]PR{}

	var roots []PR

	for _, p := range prs {
		if p.BasePR != 0 && in[p.BasePR] {
			children[p.BasePR] = append(children[p.BasePR], p)
		} else {
			roots = append(roots, p)
		}
	}

	byNumber := func(a, b PR) int { return a.Number - b.Number }
	slices.SortFunc(roots, byNumber)

	out := make([]PR, 0, len(prs))
	seen := map[int]bool{}

	var walk func(p PR)

	walk = func(p PR) {
		if seen[p.Number] {
			return
		}

		seen[p.Number] = true
		out = append(out, p)

		kids := children[p.Number]
		slices.SortFunc(kids, byNumber)

		for _, k := range kids {
			walk(k)
		}
	}

	for _, r := range roots {
		walk(r)
	}

	// a cycle has no root; its PRs still appear, by number
	rest := slices.DeleteFunc(slices.Clone(prs), func(p PR) bool { return seen[p.Number] })
	slices.SortFunc(rest, byNumber)

	return append(out, rest...)
}

func prState(p HostPR) string {
	switch {
	case p.Status == HostMerged:
		return StateShipped
	case p.Status == HostClosed:
		return StateClosed
	case p.Empty:
		return StateStarted
	case p.Draft:
		return StateInProgress
	default:
		return StateInReview
	}
}

var rank = map[string]int{StateStarted: 0, StateInProgress: 1, StateInReview: 2}

// taskState is the least advanced unfinished item: an open PR, or a commit
// on a branch other than the default with no PR. With nothing unfinished
// the task shipped if a PR merged or a commit landed on the default branch.
func taskState(t Task, def string) string {
	state, shipped := "", false

	least := func(s string) {
		if state == "" || rank[s] < rank[state] {
			state = s
		}
	}

	for _, p := range t.PRs {
		switch p.State {
		case StateShipped:
			shipped = true
		case StateClosed:
		default:
			least(p.State)
		}
	}

	for _, c := range t.Commits {
		if c.Branch == def {
			shipped = true
		} else {
			least(StateInProgress)
		}
	}

	switch {
	case state != "":
		return state
	case shipped:
		return StateShipped
	default:
		return StateClosed
	}
}

// reviewed is the others' PRs the user reviewed or merged in the period.
func reviewed(prs []HostPR, login string, since time.Time) []ReviewedPR {
	out := []ReviewedPR{}
	if login == "" {
		return out
	}

	for _, p := range prs {
		var acts []Action

		for _, r := range p.Reviews {
			if strings.EqualFold(r.Author, login) && !r.At.Before(since) {
				acts = append(acts, Action{Kind: ActionReviewed, At: r.At.UTC()})
			}
		}

		if strings.EqualFold(p.MergedBy, login) && !p.MergedAt.IsZero() && !p.MergedAt.Before(since) {
			acts = append(acts, Action{Kind: ActionMerged, At: p.MergedAt.UTC()})
		}

		if len(acts) == 0 {
			continue
		}

		slices.SortFunc(acts, func(a, b Action) int {
			return cmp.Or(a.At.Compare(b.At), strings.Compare(a.Kind, b.Kind))
		})
		acts = slices.Compact(acts)

		out = append(out, ReviewedPR{
			Number: p.Number, Title: p.Title, URL: p.URL, State: prState(p), Author: p.Author, Actions: acts,
		})
	}

	return out
}

// compareIDs orders task ids by prefix, then by number.
func compareIDs(a, b string) int {
	pa, na := splitID(a)
	pb, nb := splitID(b)

	return cmp.Or(strings.Compare(pa, pb), cmp.Compare(na, nb), strings.Compare(a, b))
}

func splitID(id string) (string, int) {
	prefix, num, ok := strings.CutLast(id, "-")
	if !ok {
		return id, 0
	}

	n, _ := strconv.Atoi(num)

	return prefix, n
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}
