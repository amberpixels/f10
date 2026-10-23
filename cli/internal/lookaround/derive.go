package lookaround

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// Derive turns facts into the board: one row per task or taskless
// request, its stage decided by the rules docs/board.md lists in order,
// the first that matches winning, and each rule naming where its evidence
// came from. It reads no clock and does no I/O: now and since are the
// board's.
func Derive(f Facts, now, since time.Time) Board {
	b := Board{
		Version:     Version,
		GeneratedAt: now,
		Project:     f.Project,
		Window:      Window{Since: since},
		Sources:     f.Sources,
		Rows:        []Row{},
	}

	if b.Sources == nil {
		b.Sources = []Source{}
	}

	rows := gather(f, since)

	for _, r := range rows {
		r.stage(f.Local != nil, since)
	}

	settleBases(rows)

	for _, r := range rows {
		if r.dropped {
			continue
		}

		r.next()
		b.Rows = append(b.Rows, r.Row)
	}

	slices.SortStableFunc(b.Rows, func(a, c Row) int {
		if d := slices.Index(Stages, a.Stage) - slices.Index(Stages, c.Stage); d != 0 {
			return d
		}

		if a.Stage == StageNext {
			return priorityRank(a.Task) - priorityRank(c.Task)
		}

		return c.LastActivity.Compare(a.LastActivity)
	})

	return b
}

// A row is one Row under construction, with the facts it was built from.
type row struct {
	Row

	prs      []PR   // every request naming the row's task, open first
	pr       *PR    // the one the stage reads: open, else merged in the window
	lead     string // the bundle's lead, for a rider sharing its branch and run
	run      *state.Run
	branch   *Branch
	worktree *Worktree
	plan     bool
	findings *Findings
	verdict  string
	finished bool
	base     string // the base task's id, from the branch, the block or the body
	baseFrom string
	dropped  bool
}

// gather builds the rows: one per tracker task, then one per request,
// joined to its task where it names one, then the local facts on each.
func gather(f Facts, since time.Time) []*row {
	var rows []*row

	index := map[string]*row{}

	find := func(key string) *row {
		if r, ok := index[strings.ToUpper(key)]; ok {
			return r
		}

		r := &row{}
		r.Key = key
		index[strings.ToUpper(key)] = r
		rows = append(rows, r)

		return r
	}

	for _, t := range f.Tasks {
		r := find(t.ID)
		task := t
		r.Task = &task
	}

	for _, p := range f.PRs {
		if p.Status == forge.Merged && p.MergedAt.Before(since) || p.Status == forge.Closed {
			continue
		}

		members := membersOf(f.Prefix, p)
		if len(members) == 0 {
			members = []string{fmt.Sprintf("#%d", p.Number)}
		}

		// a bundle's request is every member's
		for i, key := range members {
			r := find(key)
			r.prs = append(r.prs, p)

			if i > 0 {
				r.lead = members[0]
			}
		}
	}

	for _, r := range rows {
		slices.SortStableFunc(r.prs, func(a, c PR) int {
			if (a.Status == forge.Open) != (c.Status == forge.Open) {
				if a.Status == forge.Open {
					return -1
				}

				return 1
			}

			return c.MergedAt.Compare(a.MergedAt)
		})

		if len(r.prs) > 0 {
			r.pr = &r.prs[0]
		}
	}

	if f.Local != nil {
		// rows grows under local's own find calls, so it reads them through
		// the closure, never a copy of the slice
		local(f, find, func() []*row { return rows })
	}

	for _, r := range rows {
		r.fill()
	}

	return rows
}

// membersOf is the tasks a request names, lead first: a bundle spelled
// in its head branch, else the one its branch, its title, the state block
// (riders included) or the first trailer names.
func membersOf(prefix string, p PR) []string {
	if members := ref.Members(prefix, p.Head); members != nil {
		return members
	}

	if id := ref.Match(prefix, p.Head); id != "" {
		return []string{id}
	}

	if id := ref.Match(prefix, p.Title); id != "" {
		return []string{id}
	}

	if p.Block != nil && p.Block.Task != "" {
		return append([]string{p.Block.Task}, p.Block.Riders...)
	}

	for _, t := range p.Trailers {
		if len(t.Tasks) > 0 {
			return t.Tasks
		}
	}

	return nil
}

// local attaches this machine's facts to the rows they name: a branch
// by the tasks it is linked to or spells, a run by its task and the
// riders sharing its branch, plans, findings, verdicts and the ledger by
// id.
func local(f Facts, find func(string) *row, all func() []*row) {
	l := f.Local

	// one branch per task, chosen the same way every run: the one with a
	// worktree, then the one linked by config, then the last by name
	type candidate struct {
		name     string
		branch   Branch
		worktree *Worktree
		lead     string
	}

	best := map[string]candidate{}

	for _, name := range slices.Sorted(maps.Keys(l.Branches)) {
		br := l.Branches[name]

		members := append([]string{br.Task}, br.Riders...)
		if br.Task == "" {
			if members = ref.Members(f.Prefix, name); members == nil {
				members = []string{ref.Match(f.Prefix, name)}
			}
		}

		c := candidate{name: name, branch: br, lead: members[0]}

		for i := range l.Worktrees {
			if l.Worktrees[i].Branch == name {
				c.worktree = &l.Worktrees[i]
			}
		}

		for _, id := range members {
			if id == "" {
				continue
			}

			have, seen := best[strings.ToUpper(id)]
			if !seen ||
				rank(c.worktree != nil, c.branch.Task != "") >= rank(have.worktree != nil, have.branch.Task != "") {
				best[strings.ToUpper(id)] = c
			}
		}
	}

	for id, c := range best {
		r := find(id)
		b := c.branch
		r.branch = &b
		r.worktree = c.worktree
		r.Where.Branch = c.name

		if !strings.EqualFold(c.lead, id) {
			r.lead = c.lead
		}
	}

	for i := range l.Runs {
		run := &l.Runs[i]
		if run.Task == "" {
			continue
		}

		for _, r := range append([]*row{find(run.Task)}, ridersOf(all(), run.Task)...) {
			if r.run == nil || run.Updated.After(r.run.Updated) {
				r.run = run
			}
		}
	}

	// a plan counts for a task in the project's id format; a slug's plan
	// (a planless description) names no task
	for id := range l.Plans {
		if !l.Archived[id] && strings.EqualFold(ref.Match(f.Prefix, id), id) {
			find(id).plan = true
		}
	}

	for id, fd := range l.Findings {
		r := find(id)
		found := fd
		r.findings = &found
	}

	for id, v := range l.Verdicts {
		find(id).verdict = v
	}

	for id := range l.Finished {
		find(id).finished = true
	}

	for _, r := range all() {
		if r.worktree != nil {
			r.Where.Worktree = r.worktree.Path

			if ws, ok := l.Workspaces[r.worktree.Path]; ok {
				r.Where.Workspace = cmp.Or(ws.Label, ws.ID)
				r.Who.AgentStatus = ws.AgentStatus
			}

			// the one agent in a bundle's worktree is named after the lead
			if !strings.HasPrefix(r.Key, "#") {
				r.Who.Agent = AgentFor(cmp.Or(r.lead, r.Key), r.Where.Branch, r.worktree.Path)
			}
		}

		if r.branch != nil {
			r.Who.Model = r.branch.Model
		}
	}
}

// fill copies the request's facts onto the row and finds its base.
func (r *row) fill() {
	if p := r.pr; p != nil {
		r.Where.PR = &PRRef{
			Number: p.Number, Title: p.Title, URL: p.URL, State: p.Status, Draft: p.Draft, Head: p.Head, Base: p.Base,
			Mergeable: p.Mergeable, Checks: p.Checks,
		}
		r.Who.Author = p.Author
		r.Who.Assignees = p.Assignees
		r.Who.Reviewers = p.Reviewers

		if r.Where.Branch == "" {
			r.Where.Branch = p.Head
		}

		rf := &ReviewFacts{RemoteOpen: p.RemoteOpen}
		if p.Block != nil {
			rf.Step, rf.Round, rf.Open, rf.Asks = p.Block.Step, p.Block.Round, p.Block.Open, p.Block.Asks
		}

		for _, rv := range p.Reviews {
			switch rv.State {
			case forge.Approved:
				rf.Approvals++
			case forge.ChangesRequested:
				rf.ChangesRequested++
			}
		}

		if *rf != (ReviewFacts{}) {
			r.Review = rf
		}

		r.touch(p.UpdatedAt, p.MergedAt)
	}

	if r.findings != nil {
		if r.Review == nil {
			r.Review = &ReviewFacts{}
		}

		if r.findings.Round >= r.Review.Round {
			r.Review.Round, r.Review.Open = r.findings.Round, r.findings.Open
		}

		r.touch(r.findings.At)
	}

	if r.run != nil {
		r.touch(r.run.Updated)
	}

	if r.Task != nil && r.Task.Assignee != "" {
		r.Who.Assignees = append([]string{r.Task.Assignee}, r.Who.Assignees...)
	}

	switch {
	case r.branch != nil && r.branch.After != "":
		r.base, r.baseFrom = r.branch.After, FromLocal
	case r.pr != nil && r.pr.Block != nil && r.pr.Block.After != "":
		r.base, r.baseFrom = r.pr.Block.After, FromTrace
	case r.Task != nil && r.Task.After != "":
		r.base, r.baseFrom = r.Task.After, FromGuessed
	}
}

// rank orders a task's candidate branches: a worktree first, then a
// config link, so the one being worked on wins.
func rank(worktree, linked bool) int {
	n := 0
	if worktree {
		n += 2
	}

	if linked {
		n++
	}

	return n
}

// ridersOf is the rows riding on lead's branch.
func ridersOf(rows []*row, lead string) []*row {
	var out []*row

	for _, r := range rows {
		if strings.EqualFold(r.lead, lead) {
			out = append(out, r)
		}
	}

	return out
}

// touch moves the row's last activity forward, in UTC, so a board reads
// the same in every zone.
func (r *row) touch(times ...time.Time) {
	for _, t := range times {
		if t.After(r.LastActivity) {
			r.LastActivity = t.UTC()
		}
	}
}

// live reports a run still reporting on the task.
func (r *row) live() bool { return r.run != nil }

func (r *row) running() bool {
	return r.live() && (r.run.Ship == "running" || r.run.Plan == "running" || r.run.Capture == "running")
}

func (r *row) stopped() bool {
	return r.live() && (state.Stopped(r.run.Ship) || state.Stopped(r.run.Plan) || state.Stopped(r.run.Capture))
}

// tracked is a task the tracker lists as open.
func (r *row) tracked() bool { return r.Task != nil && !closed(r.Task.Status) }

func (r *row) open() bool { return r.pr != nil && r.pr.Status == forge.Open }

// counts is what the state block says, where the row has one.
type counts struct {
	Step              string
	Round, Open, Asks int
}

func (r *row) block() (counts, bool) {
	if r.pr == nil || r.pr.Block == nil {
		return counts{}, false
	}

	b := r.pr.Block

	return counts{Step: b.Step, Round: b.Round, Open: b.Open, Asks: b.Asks}, true
}

func reviewStep(step string) bool {
	return step == "review" || step == "resolve" || step == "judge"
}

// stage applies the rules in order; withLocal says the local collector
// ran, so an absence there is evidence.
func (r *row) stage(withLocal bool, since time.Time) {
	set := func(stage, from string) { r.Stage, r.StageFrom = stage, from }

	switch {
	case r.pr != nil && r.pr.Status == forge.Merged && !r.pr.MergedAt.Before(since):
		switch {
		case withLocal && (r.worktree != nil || r.branch != nil) && !r.finished:
			set(StageMergedUnfinished, FromLocal)
		case r.pr.HeadExists != nil && *r.pr.HeadExists && !r.finished:
			set(StageMergedUnfinished, FromGuessed)
		case withLocal:
			set(StageDone, FromLocal)
		default:
			set(StageDone, FromGuessed)
		}

	case r.live() && r.run.Ask != "":
		set(StageAsksWaiting, FromLocal)
	case blockAsks(r):
		set(StageAsksWaiting, FromTrace)

	case r.stopped():
		set(StageBlocked, FromLocal)
	// a verdict file is never archived, so it holds only a task still
	// open or with a request in the window
	case (r.verdict == "rethink" || r.verdict == "stop") && !r.live() && (r.tracked() || r.pr != nil):
		set(StageBlocked, FromLocal)

	case r.open() && !r.pr.Draft && r.pr.Mergeable != forge.Conflicts && r.pr.Mergeable != forge.Blocked &&
		r.pr.Checks != forge.Red && blockClean(r):
		set(StageReady, FromTrace)
	case r.open() && !r.pr.Draft && r.pr.Mergeable != forge.Conflicts && r.pr.Mergeable != forge.Blocked &&
		r.pr.Checks != forge.Red && r.pr.Decision == forge.Approved && r.pr.Block == nil:
		set(StageReady, FromGuessed)

	case r.running() && reviewStep(r.run.Leaf):
		set(StageInReview, FromLocal)
	case withLocal && r.findings != nil && r.findings.Open > 0 && r.open():
		set(StageInReview, FromLocal)
	case blockInReview(r):
		set(StageInReview, FromTrace)
	case r.open() && (len(r.pr.Reviews) > 0 || len(r.pr.Reviewers) > 0 ||
		r.pr.Decision == forge.ChangesRequested || r.pr.RemoteOpen):
		set(StageInReview, FromGuessed)

	case r.open() && !r.pr.Draft && r.pr.Block != nil:
		set(StagePROpen, FromTrace)
	case r.open() && !r.pr.Draft:
		set(StagePROpen, FromGuessed)

	case r.running() && r.run.Ship == "running":
		set(StageImplementing, FromLocal)
	case r.open() && r.pr.Draft:
		set(StageImplementing, FromGuessed)

	// local facts with no request behind them count for a task the
	// tracker still lists: a worktree or a plan left behind by a task
	// closed long ago is not work in flight
	case r.tracked() && r.worktree != nil && r.worktree.Ahead > 0:
		set(StageImplementing, FromLocal)
	case r.tracked() && r.plan, r.running() && r.run.Plan == "running":
		set(StagePlanned, FromLocal)

	case r.Task != nil && !closed(r.Task.Status):
		set(StageNext, FromGuessed)

	default:
		r.dropped = true
	}
}

func blockAsks(r *row) bool {
	b, ok := r.block()

	return ok && b.Asks > 0
}

func blockClean(r *row) bool {
	b, ok := r.block()

	return ok && b.Round > 0 && b.Open == 0
}

func blockInReview(r *row) bool {
	b, ok := r.block()

	return ok && r.open() && reviewStep(b.Step) && b.Open > 0
}

// closed reads a tracker status as closed: the host's words and the
// usual tracker ones.
func closed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "merged", "resolved", "completed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

// settleBases applies the base rule in dependency order: a row whose base
// row is not merged is blocked, unless a run is working on it or it is
// already past the point a base can hold it (done, merged, asking).
func settleBases(rows []*row) {
	index := map[string]*row{}
	for _, r := range rows {
		index[strings.ToUpper(r.Key)] = r
	}

	done := map[*row]bool{}

	var settle func(r *row, path map[*row]bool)

	settle = func(r *row, path map[*row]bool) {
		if done[r] || path[r] {
			return
		}

		path[r] = true

		base, ok := index[strings.ToUpper(r.base)]
		if r.base != "" && ok && base != r {
			settle(base, path)

			// merging releases a dependent; the base's leftover worktree
			// or branch is its own chore
			holds := !base.dropped && base.Stage != StageDone && base.Stage != StageMergedUnfinished

			if holds && !r.running() && !r.dropped && !earlyStage(r.Stage) {
				r.Stage, r.StageFrom = StageBlocked, r.baseFrom
				r.BlockedBy = []string{base.Key}
			}
		}

		done[r] = true
	}

	for _, r := range rows {
		settle(r, map[*row]bool{})
	}
}

// earlyStage is a stage the base rule never overrides: the rules before it.
func earlyStage(stage string) bool {
	return stage == StageDone || stage == StageMergedUnfinished || stage == StageAsksWaiting || stage == StageBlocked
}

func priorityRank(t *Task) int {
	if t == nil {
		return len(driver.Priorities)
	}

	if i, ok := driver.PriorityRank(t.Priority); ok {
		return i
	}

	return len(driver.Priorities)
}
