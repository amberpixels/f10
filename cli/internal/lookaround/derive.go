package lookaround

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/trace"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// Derive is the board from facts: one row per task or taskless request,
// staged by the first matching rule in docs/board.md, each naming its
// evidence. No clock and no I/O: now and since are the board's.
func Derive(f Facts, now, since time.Time) Board {
	b := Board{
		Version:     Version,
		GeneratedAt: now,
		View:        ViewState,
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
		if d := StateRank(a) - StateRank(c); d != 0 {
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
	TaskFacts

	dropped bool
}

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
			members = []string{f.Project.Ref(p.Number)}
		}

		// a bundle's request is every member's
		for i, key := range members {
			r := find(key)
			r.PRs = append(r.PRs, p)

			if i > 0 {
				r.Lead = members[0]
			}
		}
	}

	for _, r := range rows {
		r.SetPRs(r.PRs)
	}

	if f.Local != nil {
		// rows grows under local's own find calls, so it reads them through
		// the closure, never a copy of the slice
		local(f, find, func() []*row { return rows })
	}

	for _, r := range rows {
		r.fill()
		r.person(f.People)
	}

	return rows
}

// membersOf is the tasks a request names, lead first, from its most
// explicit source: the head branch, the title, the state block, a trailer.
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

// local attaches this machine's facts to the rows they name.
func local(f Facts, find func(string) *row, all func() []*row) {
	l := f.Local

	// one branch per task, picked by rank so every run picks the same one
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
		r.Branch = &b
		r.Worktree = c.worktree
		r.Where.Branch = c.name

		if !strings.EqualFold(c.lead, id) {
			r.Lead = c.lead
		}
	}

	for i := range l.Runs {
		run := &l.Runs[i]
		if run.Task == "" {
			continue
		}

		for _, r := range append([]*row{find(run.Task)}, ridersOf(all(), run.Task)...) {
			if r.Run == nil || run.Updated.After(r.Run.Updated) {
				r.Run = run
			}
		}
	}

	// a plan counts for a task in the project's id format; a slug's plan
	// (a planless description) names no task
	for id := range l.Plans {
		if !l.Archived[id] && strings.EqualFold(ref.Match(f.Prefix, id), id) {
			find(id).Plan = true
		}
	}

	for id, fd := range l.Findings {
		r := find(id)
		found := fd
		r.Findings = &found
	}

	for id, v := range l.Verdicts {
		find(id).Verdict = v
	}

	for id := range l.Finished {
		if finishedNow(l, id) {
			r := find(id)
			r.Finished, r.FinishedFrom = true, FinishedByLedger
		}
	}

	for _, r := range all() {
		if r.Worktree != nil {
			r.Where.Worktree = r.Worktree.Path

			if ws, ok := l.Workspaces[r.Worktree.Path]; ok {
				r.Where.Workspace = cmp.Or(ws.Label, ws.ID)
				r.Who.AgentStatus = ws.AgentStatus
			}

			// the one agent in a bundle's worktree is named after the lead
			if !requestKey(r.Key) {
				r.Who.Agent = AgentFor(cmp.Or(r.Lead, r.Key), r.Where.Branch, r.Worktree.Path)
			}
		}

		if r.Branch != nil {
			r.Who.Model = r.Branch.Model
		}
	}
}

func (r *row) fill() {
	if p := r.PR; p != nil {
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

	if r.Findings != nil {
		if r.Review == nil {
			r.Review = &ReviewFacts{}
		}

		if r.Findings.Round >= r.Review.Round {
			r.Review.Round, r.Review.Open = r.Findings.Round, r.Findings.Open
		}

		r.touch(r.Findings.At)
	}

	if r.Run != nil {
		r.touch(r.Run.Updated)
	}

	if r.Task != nil && r.Task.Assignee != "" {
		r.Who.Assignees = append([]string{r.Task.Assignee}, r.Who.Assignees...)
	}

	if r.PR != nil {
		for _, c := range r.PR.Commits {
			r.commit(c.Email)
		}
	}

	if r.Worktree != nil {
		for _, e := range r.Worktree.Emails {
			r.commit(e)
		}
	}

	var branchAfter, taskAfter string
	if r.Branch != nil {
		branchAfter = r.Branch.After
	}

	if r.Task != nil {
		taskAfter = r.Task.After
	}

	r.Base, r.BaseFrom = BaseOf(branchAfter, r.PR, taskAfter)
}

// BaseOf settles a task's base for status and the board alike: the base the
// branch records, else the request's state block, else the task's After line.
func BaseOf(branchAfter string, pr *PR, taskAfter string) (string, string) {
	var block *trace.State
	if pr != nil {
		block = pr.Block
	}

	switch {
	case branchAfter != "":
		return branchAfter, FromLocal
	case block != nil && block.After != "":
		return block.After, FromTrace
	case taskAfter != "":
		return taskAfter, FromGuessed
	}

	return "", ""
}

func (r *row) commit(email string) {
	if email == "" ||
		slices.ContainsFunc(r.Who.Committers, func(e string) bool { return strings.EqualFold(e, email) }) {
		return
	}

	r.Who.Committers = append(r.Who.Committers, email)
}

// person fills Who.Person. A host login claims the assignee too, since a
// tracker that is the host's issues names its assignees by login.
func (r *row) person(people []Person) {
	assignee := ""
	if r.Task != nil {
		assignee = r.Task.Assignee
	}

	r.Who.Person = personOf(people, assignee, r.Who.Author, r.Who.Committers)
}

func personOf(people []Person, assignee, author string, committers []string) string {
	committer := ""
	if len(committers) > 0 {
		committer = committers[0]
	}

	// a person claiming the assignee wins over one claiming the author,
	// whatever order the map lists them in
	byTracker := func(p Person) bool { return assignee != "" && strings.EqualFold(p.Tracker, assignee) }
	byHost := func(p Person) bool {
		return assignee != "" && forge.SameLogin(p.Host, assignee) ||
			author != "" && forge.SameLogin(p.Host, author)
	}
	byEmail := func(p Person) bool {
		return slices.ContainsFunc(committers, func(e string) bool {
			return slices.ContainsFunc(p.Emails, func(known string) bool { return strings.EqualFold(known, e) })
		})
	}

	for _, pick := range []func(Person) bool{byTracker, byHost, byEmail} {
		if i := slices.IndexFunc(people, pick); i >= 0 {
			return people[i].Name
		}
	}

	return cmp.Or(assignee, author, committer)
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

func ridersOf(rows []*row, lead string) []*row {
	var out []*row

	for _, r := range rows {
		if strings.EqualFold(r.Lead, lead) {
			out = append(out, r)
		}
	}

	return out
}

// touch keeps LastActivity in UTC so a board reads the same in every zone.
func (r *row) touch(times ...time.Time) {
	for _, t := range times {
		if t.After(r.LastActivity) {
			r.LastActivity = t.UTC()
		}
	}
}

func (r *row) live() bool { return r.Run != nil }

func (r *row) running() bool {
	return r.live() && (r.Run.Ship == "running" || r.Run.Plan == "running" || r.Run.Capture == "running")
}

func (r *row) stopped() bool {
	return r.live() && (state.Stopped(r.Run.Ship) || state.Stopped(r.Run.Plan) || state.Stopped(r.Run.Capture))
}

func (r *row) tracked() bool { return r.Task != nil && !closed(r.Task.Status) }

func (r *row) open() bool { return r.PR != nil && r.PR.Status == forge.Open }

type counts struct {
	Step              string
	Round, Open, Asks int
}

func (r *row) block() (counts, bool) {
	if r.PR == nil || r.PR.Block == nil {
		return counts{}, false
	}

	b := r.PR.Block

	return counts{Step: b.Step, Round: b.Round, Open: b.Open, Asks: b.Asks}, true
}

// reviewStep reads an entry as the flow names it: `review:local` is review.
func reviewStep(entry string) bool {
	step := flow.Parse(entry).Step

	return step == "review" || step == "resolve" || step == "judge"
}

// planJudge is a ship run at a judge its pipeline puts before implement:
// the plan is written and being checked, no code yet. The leaf's place
// tells the two judges of one pipeline apart, else its first entry counts.
func planJudge(run *state.Run) bool {
	if run.Ship != "running" {
		return false
	}

	if flow.Parse(run.Leaf).Step != "judge" {
		return false
	}

	at := run.At - 1
	if at < 0 || at >= len(run.Pipeline) || run.Pipeline[at] != run.Leaf {
		at = slices.Index(run.Pipeline, run.Leaf)
	}

	implement := slices.IndexFunc(run.Entries(), func(e flow.Entry) bool { return e.Step == "implement" })

	return at >= 0 && implement > at
}

// stage applies the rules in order; withLocal says the local collector
// ran, so an absence there is evidence.
func (r *row) stage(withLocal bool, since time.Time) {
	set := func(stage, from string) { r.Stage, r.StageFrom = stage, from }

	switch {
	case r.PR != nil && r.PR.Status == forge.Merged && !r.PR.MergedAt.Before(since):
		// only a worktree left on disk still holds work; a branch left
		// behind is a note on a done row
		r.Where.BranchLeft = !r.Finished && r.Worktree == nil &&
			((withLocal && r.Branch != nil) || (r.PR.HeadExists != nil && *r.PR.HeadExists))

		switch {
		case withLocal && r.Worktree != nil && !r.Finished:
			set(StageMergedUnfinished, FromLocal)
		case withLocal:
			set(StageDone, FromLocal)
		default:
			set(StageDone, FromGuessed)
		}

	case r.live() && r.Run.Ask != "":
		set(StageAsksWaiting, FromLocal)
	case blockAsks(r):
		set(StageAsksWaiting, FromTrace)

	case r.stopped():
		set(StageBlocked, FromLocal)
	// a verdict file is never archived, so it holds only a task still
	// open or with a request in the window
	case (r.Verdict == "rethink" || r.Verdict == "stop") && !r.live() && (r.tracked() || r.PR != nil):
		set(StageBlocked, FromLocal)

	case r.open() && !r.PR.Draft && r.PR.Mergeable != forge.Conflicts && r.PR.Mergeable != forge.Blocked &&
		r.PR.Checks != forge.Red && blockClean(r):
		set(StageReady, FromTrace)
	case r.open() && !r.PR.Draft && r.PR.Mergeable != forge.Conflicts && r.PR.Mergeable != forge.Blocked &&
		r.PR.Checks != forge.Red && r.PR.Decision == forge.Approved && r.PR.Block == nil:
		set(StageReady, FromGuessed)

	case r.running() && planJudge(r.Run):
		set(StagePlanned, FromLocal)
	case r.running() && r.Run.Ship == "running" && reviewStep(r.Run.Leaf):
		set(StageInReview, FromLocal)
	case withLocal && r.Findings != nil && r.Findings.Open > 0 && r.open():
		set(StageInReview, FromLocal)
	case blockInReview(r):
		set(StageInReview, FromTrace)
	case r.open() && (len(r.PR.Reviews) > 0 || len(r.PR.Reviewers) > 0 ||
		r.PR.Decision == forge.ChangesRequested || r.PR.RemoteOpen):
		set(StageInReview, FromGuessed)

	case r.open() && !r.PR.Draft && r.PR.Block != nil:
		set(StagePROpen, FromTrace)
	case r.open() && !r.PR.Draft:
		set(StagePROpen, FromGuessed)

	case r.running() && r.Run.Ship == "running":
		set(StageImplementing, FromLocal)
	case r.open() && r.PR.Draft:
		set(StageImplementing, FromGuessed)

	// local facts with no request behind them count for a task the
	// tracker still lists: a worktree or a plan left behind by a task
	// closed long ago is not work in flight
	case r.tracked() && r.Worktree != nil && r.Worktree.Ahead > 0:
		set(StageImplementing, FromLocal)
	case r.live() && r.Run.Plan == "running":
		set(StagePlanning, FromLocal)
	// a worktree with nothing ahead is a started task with no code yet
	case r.tracked() && (r.Plan || r.Worktree != nil):
		set(StagePlanned, FromLocal)

	case r.Task != nil && !closed(r.Task.Status):
		set(StageNext, FromTracker)

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
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}

	SettleBases(keys, func(i int) string { return rows[i].Base }, func(i, b int) {
		if b < 0 {
			return
		}

		r, base := rows[i], rows[b]

		// merging releases a dependent; the base's leftover worktree or
		// branch is its own chore
		holds := !base.dropped && base.Stage != StageDone && base.Stage != StageMergedUnfinished

		if holds && !r.running() && !r.dropped && !earlyStage(r.Stage) {
			r.Stage, r.StageFrom = StageBlocked, r.BaseFrom
			r.BlockedBy = []string{base.Key}
		}
	})
}

// earlyStage is a stage the base rule never overrides: the rules before it.
func earlyStage(stage string) bool {
	return stage == StageDone || stage == StageMergedUnfinished || stage == StageAsksWaiting || stage == StageBlocked
}

func priorityRank(t *Task) int {
	if t == nil {
		return len(trackerdriver.Priorities)
	}

	if i, ok := trackerdriver.PriorityRank(t.Priority); ok {
		return i
	}

	return len(trackerdriver.Priorities)
}
