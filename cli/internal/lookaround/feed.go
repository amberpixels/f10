package lookaround

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// DeriveFeed turns facts into the feed over the window: one row per task
// whose requests had activity there, one per such request naming no task,
// each in the one group its activity reads as by the rules docs/board.md
// lists, one per task created there, and the open requests with none
// counted in the quiet line. Like Derive it reads no clock and does no I/O.
func DeriveFeed(f Facts, now time.Time, w Window) Board {
	b := Board{
		Version:     Version,
		GeneratedAt: now,
		View:        ViewFeed,
		Project:     f.Project,
		Window:      w,
		Sources:     f.Sources,
		Rows:        []Row{},
		Feed:        &Feed{Rows: []FeedRow{}},
	}

	if b.Sources == nil {
		b.Sources = []Source{}
	}

	tasks := map[string]*Task{}
	for i := range f.Tasks {
		tasks[strings.ToUpper(f.Tasks[i].ID)] = &f.Tasks[i]
	}

	if a := f.Activity; a != nil {
		b.Feed.Quiet = Quiet{Count: a.QuietCount, Oldest: a.QuietOldest}

		byTask := map[string][]requestRow{}

		var order []string

		for _, t := range a.Requests {
			if r, ok := feedRow(f, t, w, tasks); ok {
				if len(membersOf(f.Prefix, feedPR(t))) == 0 {
					b.Feed.Rows = append(b.Feed.Rows, r)

					continue
				}

				id := strings.ToUpper(r.Key)
				if _, seen := byTask[id]; !seen {
					order = append(order, id)
				}

				byTask[id] = append(byTask[id], requestRow{r, t})

				continue
			}

			// an open request with no row is quiet: touched before this
			// window (read from the earliest span the default may show), or
			// in it only by what the feed does not group (a comment, a
			// label, a commit dated before the window)
			if t.Status == forge.Open && !t.CreatedAt.After(windowEnd(w, now)) {
				b.Feed.Quiet.Count++
				if b.Feed.Quiet.Oldest.IsZero() || t.CreatedAt.Before(b.Feed.Quiet.Oldest) {
					b.Feed.Quiet.Oldest = t.CreatedAt
				}
			}
		}

		for _, id := range order {
			b.Feed.Rows = append(b.Feed.Rows, taskRow(f, byTask[id]))
		}
	}

	for _, t := range f.Tasks {
		if !inWindow(w, t.Created) {
			continue
		}

		task := t
		r := FeedRow{
			Group: GroupNewTasks, Key: t.ID, Task: &task, From: FromTracker, Summary: "created", At: t.Created.UTC(),
		}

		if t.Assignee != "" {
			r.Who.Assignees = []string{t.Assignee}
		}

		b.Feed.Rows = append(b.Feed.Rows, r)
	}

	for _, tip := range f.Tips {
		if !inWindow(w, tip.At) {
			continue
		}

		key := tip.Branch
		if id := ref.Match(f.Prefix, tip.Branch); id != "" {
			key = id
		}

		r := FeedRow{
			Group: GroupUpdated, Key: key, Task: tasks[strings.ToUpper(key)], From: FromLocal,
			Summary: "committed on " + tip.Branch, At: tip.At.UTC(),
		}

		b.Feed.Rows = append(b.Feed.Rows, r)
	}

	for i := range b.Feed.Rows {
		r := &b.Feed.Rows[i]

		assignee := ""
		if r.Task != nil {
			assignee = r.Task.Assignee
		}

		r.Who.Person = personOf(f.People, assignee, r.Who.Author, nil)
	}

	slices.SortStableFunc(b.Feed.Rows, func(a, c FeedRow) int {
		if d := slices.Index(Groups, a.Group) - slices.Index(Groups, c.Group); d != 0 {
			return d
		}

		return c.At.Compare(a.At)
	})

	return b
}

// ChooseWindow is the default's span: today when the facts give today's
// feed a row, else the last working day, marked as the fallback. Activity
// that makes no row (a review comment) keeps the fallback. The facts are
// read from the last working day's start, so one collection answers both.
func ChooseWindow(today, lastWorkday Window, facts ...Facts) Window {
	for _, f := range facts {
		if len(DeriveFeed(f, today.End, today).Feed.Rows) > 0 {
			return today
		}
	}

	lastWorkday.Fallback = true

	return lastWorkday
}

func inWindow(w Window, t time.Time) bool {
	return !t.IsZero() && !t.Before(w.Since) && (w.End.IsZero() || t.Before(w.End))
}

func windowEnd(w Window, now time.Time) time.Time {
	if w.End.IsZero() {
		return now
	}

	return w.End
}

// feedRow is one touched request's row, or false when nothing it did
// falls in the window (a comment, a label, or activity outside it). The
// first rule that matches decides the group: finished, started,
// reviewed, updated.
func feedRow(f Facts, t forge.Touched, w Window, tasks map[string]*Task) (FeedRow, bool) {
	pr := feedPR(t)

	key := fmt.Sprintf("#%d", t.Number)
	members := membersOf(f.Prefix, pr)

	if len(members) > 0 {
		key = members[0]
	}

	r := FeedRow{
		Key:  key,
		Task: tasks[strings.ToUpper(key)],
		PR: &PRRef{
			Number: t.Number, Title: t.Title, URL: t.URL, State: t.Status, Draft: t.Draft, Head: t.Head, Base: t.Base,
		},
		Who: Who{Author: t.Author, Assignees: t.Assignees},
	}

	var (
		commits, merges, pushes int
		ready                   bool
		verdict                 *forge.Event
		steps                   []string
		requested               = map[string]time.Time{}
		reviewed                = map[string]time.Time{}
	)

	for _, e := range t.Events {
		if !inWindow(w, e.At) {
			continue
		}

		if e.At.After(r.At) {
			r.At = e.At.UTC()
		}

		switch e.Kind {
		case forge.EventCommit, forge.EventMergeFromBase:
			if e.Kind == forge.EventCommit {
				commits++
			} else {
				merges++
			}

			if p := stepOf(e.Message); p != "" {
				steps = append(steps, p)
			}
		case forge.EventForcePush:
			pushes++
		case forge.EventReady:
			ready = true
		case forge.EventReviewRequested:
			requested[e.Actor] = e.At
		case forge.EventReview:
			reviewed[e.Actor] = e.At

			if e.State == forge.Approved || e.State == forge.ChangesRequested {
				if verdict == nil || e.At.After(verdict.At) {
					verdict = &e
				}
			}
		}
	}

	// a reviewer asked in the window and not heard from since owes the move
	for _, name := range sortedKeys(requested) {
		if at, ok := reviewed[name]; !ok || at.Before(requested[name]) {
			r.Owes = name

			break
		}
	}

	merged := t.Status == forge.Merged && inWindow(w, t.MergedAt)
	closedHere := t.Status == forge.Closed && inWindow(w, t.ClosedAt)
	opened := inWindow(w, t.CreatedAt)
	reviewStepped := slices.ContainsFunc(steps, reviewStep)

	switch {
	case merged:
		r.Group, r.From, r.Owes, r.Summary = GroupFinished, FromHost, "", "merged"
		if t.MergedBy != "" && t.MergedBy != t.Author {
			r.Summary += " by " + t.MergedBy
		}
	case closedHere:
		r.Group, r.From, r.Owes, r.Abandoned, r.Summary = GroupFinished, FromHost, "", true, "abandoned"
	case opened || ready:
		r.Group, r.From = GroupStarted, FromHost

		switch {
		case !opened:
			r.Summary = "ready for review"
		case t.Draft:
			r.Summary = "opened as a draft"
		default:
			r.Summary = "opened"
		}
	case verdict != nil || reviewStepped:
		r.Group, r.From = GroupReviewed, FromGuessed
		if reviewStepped {
			r.From = FromTrace
		}

		if verdict != nil {
			word := "approved"
			if verdict.State == forge.ChangesRequested {
				word = "changes requested"
			}

			r.Summary, r.Owes = word+" by "+verdict.Actor, t.Author
		} else {
			r.Summary = steps[len(steps)-1] + " pushed"
		}
	case commits+merges+pushes > 0 || len(requested) > 0:
		r.Group, r.From = GroupUpdated, FromGuessed
		if len(steps) > 0 {
			r.From = FromTrace
		}

		r.Summary = updateSummary(commits, merges, pushes, t.Base, steps)
		if r.Summary == "" {
			r.Summary = "review requested"
		}
	default:
		return FeedRow{}, false
	}

	if r.At.IsZero() {
		r.At = latestOf(t.MergedAt, t.ClosedAt, t.CreatedAt).UTC()
	}

	return r, true
}

// stepOf is the step a commit's Step: trailer names, else "".
func stepOf(message string) string {
	p := trace.Parse(message)
	if len(p.Steps) > 0 {
		return p.Steps[len(p.Steps)-1]
	}

	return p.Step
}

// updateSummary is what an update did, counted: commits, merges from the
// base, force pushes, and the step the trailers name.
func updateSummary(commits, merges, pushes int, base string, steps []string) string {
	var parts []string

	if commits > 0 {
		parts = append(parts, Plural(commits, "commit"))
	}

	if merges > 0 {
		m := "merged " + cmp.Or(base, "the base")
		if merges > 1 {
			m += fmt.Sprintf(" %d times", merges)
		}

		parts = append(parts, m)
	}

	if pushes > 0 {
		parts = append(parts, Plural(pushes, "force push"))
	}

	s := strings.Join(parts, ", ")
	if len(steps) > 0 && s != "" {
		s = steps[len(steps)-1] + ": " + s
	}

	return s
}

// A requestRow is one request with its row, before its task's rows fold.
type requestRow struct {
	row FeedRow
	t   forge.Touched
}

// taskRow folds a task's request rows into one: the latest-opened
// request leads, giving the row its group, request and summary, and every
// other request of the task opened before it is listed by how it relates
// (relationOf), the summary saying so.
//
//nolint:lostfield // a fold of the request rows, not a conversion
func taskRow(f Facts, reqs []requestRow) FeedRow {
	lead := slices.MaxFunc(reqs, func(a, c requestRow) int {
		return cmp.Or(a.t.CreatedAt.Compare(c.t.CreatedAt), cmp.Compare(a.t.Number, c.t.Number))
	})

	r := lead.row
	for _, q := range reqs {
		if q.row.At.After(r.At) {
			r.At = q.row.At
		}
	}

	var parts, followUps, replaced []string

	allMerged := r.Group == GroupFinished && lead.t.Status == forge.Merged

	for _, p := range otherRequests(f, lead.t, r.Key) {
		rel := relationOf(p, lead.t)
		r.Related = append(r.Related, RelatedPR{Number: p.Number, URL: p.URL, State: p.Status, Relation: rel})

		n := fmt.Sprintf("#%d", p.Number)

		switch rel {
		case RelationPart:
			parts = append(parts, n)
			allMerged = allMerged && p.Status == forge.Merged
		case RelationFollowUp:
			followUps = append(followUps, n)
		case RelationReplaced:
			replaced = append(replaced, n)
		}
	}

	if len(parts) > 0 && allMerged {
		r.Summary = fmt.Sprintf("merged in %d PRs", len(parts)+1)
		if lead.t.MergedBy != "" && lead.t.MergedBy != lead.t.Author {
			r.Summary += " by " + lead.t.MergedBy
		}
	}

	if len(replaced) > 0 {
		r.Summary += " (replaced " + strings.Join(replaced, ", ") + ")"
	}

	if len(followUps) > 0 {
		r.Summary += ", follow-up to " + followUps[len(followUps)-1]
	}

	return r
}

// relationOf is how an earlier request relates to a later one of its
// task: replaced when it closed unmerged, a follow-up's base when it
// merged before the later one opened (or at an unknown time), else a
// part, the two overlapping.
func relationOf(earlier PR, later forge.Touched) string {
	switch {
	case earlier.Status == forge.Closed:
		return RelationReplaced
	case earlier.Status == forge.Merged && !earlier.MergedAt.After(later.CreatedAt):
		return RelationFollowUp
	default:
		return RelationPart
	}
}

// otherRequests is the task's requests opened before the lead, in the
// window or found by the earlier search, each matched against the id by
// its branch or title, in the order they opened. One with no creation
// time counts as earlier.
func otherRequests(f Facts, lead forge.Touched, id string) []PR {
	var out []PR

	consider := func(p PR) {
		if p.Number == lead.Number || !p.CreatedAt.IsZero() && !p.CreatedAt.Before(lead.CreatedAt) ||
			slices.ContainsFunc(out, func(o PR) bool { return o.Number == p.Number }) {
			return
		}

		if slices.ContainsFunc(membersOf(f.Prefix, p), func(m string) bool { return strings.EqualFold(m, id) }) {
			out = append(out, p)
		}
	}

	if f.Activity != nil {
		for _, other := range f.Activity.Requests {
			consider(feedPR(other))
		}
	}

	for _, p := range f.Earlier[id] {
		consider(PR{PR: p})
	}

	slices.SortFunc(out, func(a, c PR) int {
		return cmp.Or(a.CreatedAt.Compare(c.CreatedAt), cmp.Compare(a.Number, c.Number))
	})

	return out
}

// Plural is n and word, the word in its plural past one: "-sh" takes
// "es" ("2 force pushes"). Every renderer counting a word goes through
// it.
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	if strings.HasSuffix(word, "sh") {
		return fmt.Sprintf("%d %ses", n, word)
	}

	return fmt.Sprintf("%d %ss", n, word)
}

func latestOf(times ...time.Time) time.Time {
	var out time.Time

	for _, t := range times {
		if t.After(out) {
			out = t
		}
	}

	return out
}

func sortedKeys(m map[string]time.Time) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
