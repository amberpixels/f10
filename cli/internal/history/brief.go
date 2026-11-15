package history

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
)

// MinSample is the fewest live neighbors a statistic is printed for: under
// it the neighbors are listed and no median or rate is given.
const MinSample = 3

// Brief is everything the command prints, the text form a capped view of it.
type Brief struct {
	Paths       []string     `json:"paths"`
	Since       time.Time    `json:"since"`
	Neighbors   []Neighbor   `json:"neighbors"`
	Stale       []Touch      `json:"stale"`
	Regressions []Regression `json:"regressions"`
	Aggregates  *Aggregates  `json:"aggregates"`
	Decisions   []Note       `json:"decisions"`
	Lessons     []Note       `json:"lessons"`
}

// Empty reports a brief with nothing to say.
func (b *Brief) Empty() bool {
	return len(b.Neighbors) == 0 && len(b.Stale) == 0 && len(b.Regressions) == 0 &&
		len(b.Decisions) == 0 && len(b.Lessons) == 0
}

// Neighbor is a live task's touch and what the ledger recorded about it.
type Neighbor struct {
	Touch
	Facts

	Kind string `json:"kind,omitempty"`
}

// Count is a finding category in one task: raised as each round opened
// counted it, fixed and skipped as resolve's verdicts recorded them. Raised
// is the reliable one, because a verdict row carries its category only when
// the findings file wrote one beside it.
type Count struct {
	Raised  int `json:"raised"`
	Fixed   int `json:"fixed"`
	Skipped int `json:"skipped"`
}

// Regression is a bug traced to these files or to a neighbor.
type Regression struct {
	Bug    string `json:"bug"`
	Origin string `json:"origin"` // the origin task, else pre-f10 or outside-f10
	Class  string `json:"class,omitempty"`
}

// Aggregates are the statistics over the reviewed neighbors, those the
// ledger holds a review round for, once there are MinSample of them: older
// work predates the ledger, and counting it would dilute every rate.
type Aggregates struct {
	N            int         `json:"n"` // the reviewed neighbors
	MedianRounds float64     `json:"median_rounds"`
	Recurring    []Recurring `json:"recurring"`
	Regressions  int         `json:"regressions"`
}

// Recurring is a finding category raised in at least half the reviewed
// neighbors.
type Recurring struct {
	Category string   `json:"category"`
	Tasks    []string `json:"tasks"`
}

// Note is a lore lesson or decision on one of the paths.
type Note struct {
	ID       string          `json:"id"`
	Change   string          `json:"change"`
	Path     string          `json:"path"`
	Anchors  []string        `json:"anchors"` // on the paths, as `f10 lore add --anchor` takes them
	Text     string          `json:"text"`
	Chosen   string          `json:"chosen,omitempty"`
	Rejected []lore.Rejected `json:"rejected,omitempty"`
}

// Input is what Build reads: the touches git found, the ledger rows of their
// sessions with every regression_found row of the project, the lore on the
// paths, and each task's plan kind.
type Input struct {
	Paths     []string
	Since     time.Time
	Touches   []Touch
	Events    []ledger.Event
	Lessons   []lore.Entry
	Decisions []lore.Entry
	Kinds     map[string]string
}

// Build is the brief. Counts and outcomes come from the ledger alone and
// reasons from lore alone, so a finding written to both counts once.
func Build(in Input) *Brief {
	b := &Brief{
		Paths: in.Paths, Since: in.Since,
		Neighbors: []Neighbor{}, Stale: []Touch{}, Regressions: []Regression{},
		Decisions: notes(in.Decisions, in.Paths), Lessons: notes(in.Lessons, in.Paths),
	}

	for _, t := range in.Touches {
		if t.Stale() {
			b.Stale = append(b.Stale, t)

			continue
		}

		b.Neighbors = append(b.Neighbors, Neighbor{Touch: t, Kind: in.Kinds[t.Task]})
	}

	tasks := make([]string, len(b.Neighbors))
	for i, n := range b.Neighbors {
		tasks[i] = n.Task
	}

	facts := TaskFacts(in.Events, tasks, in.Since)
	for i := range b.Neighbors {
		b.Neighbors[i].Facts = *facts[strings.ToUpper(b.Neighbors[i].Task)]
	}

	b.Regressions = regressions(in.Events, in.Paths, in.Since, facts)

	b.Aggregates = aggregate(b.Neighbors, len(b.Regressions))

	return b
}

// regressions is the newest attribution of each bug found since the window
// opened that names these paths or a live neighbor.
func regressions(events []ledger.Event, paths []string, since time.Time, live map[string]*Facts) []Regression {
	out := []Regression{}

	for _, f := range latestRegressions(events, since) {
		if live[strings.ToUpper(f.OriginTask)] == nil && !onPaths(f.Lines, paths) {
			continue
		}

		out = append(out, Regression{Bug: f.Bug, Origin: cmp.Or(f.OriginTask, f.Origin, "unknown"), Class: f.Class})
	}

	return out
}

// onPaths reports a `path:start-end` range on one of paths.
func onPaths(lines, paths []string) bool {
	for _, l := range lines {
		path, _, _ := strings.Cut(l, ":")
		if slices.Contains(paths, path) {
			return true
		}
	}

	return false
}

// aggregate is the statistics over the reviewed neighbors, nil under
// MinSample of them.
//
//nolint:lostfield // a statistic over the neighbors, not a conversion of one
func aggregate(ns []Neighbor, regressions int) *Aggregates {
	var (
		rounds   []float64
		raisedIn = map[string][]string{}
	)

	for _, n := range ns {
		if n.Rounds == 0 {
			continue
		}

		rounds = append(rounds, float64(n.Rounds))

		for cat, c := range n.Categories {
			if c.Raised > 0 || c.Fixed > 0 {
				raisedIn[cat] = append(raisedIn[cat], n.Task)
			}
		}
	}

	if len(rounds) < MinSample {
		return nil
	}

	a := &Aggregates{N: len(rounds), MedianRounds: median(rounds), Recurring: []Recurring{}, Regressions: regressions}

	for cat, tasks := range raisedIn {
		if 2*len(tasks) >= a.N {
			a.Recurring = append(a.Recurring, Recurring{Category: cat, Tasks: tasks})
		}
	}

	slices.SortFunc(a.Recurring, func(x, y Recurring) int {
		return cmp.Or(cmp.Compare(len(y.Tasks), len(x.Tasks)), cmp.Compare(x.Category, y.Category))
	})

	return a
}

func median(vs []float64) float64 {
	s := slices.Sorted(slices.Values(vs))
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}

	return s[len(s)/2]
}

// notes is the entries once each, on the first of paths they anchor to.
func notes(entries []lore.Entry, paths []string) []Note {
	out := []Note{}
	seen := map[string]bool{}

	for _, e := range entries {
		if seen[e.ID] || catchupDecision(e) {
			continue
		}

		seen[e.ID] = true

		n := Note{
			ID: e.ID, Change: e.Change, Text: firstLine(e.Body), Chosen: e.Chosen, Rejected: e.Rejected,
			Anchors: []string{},
		}

		for _, a := range e.Anchors {
			if slices.Contains(paths, a.Path) {
				n.Path = cmp.Or(n.Path, a.Path)
				n.Anchors = append(n.Anchors, a.String())
			}
		}

		out = append(out, n)
	}

	return out
}

// decode reads a row a newer f10 wrote as far as this one knows it.
func decode[T any](e ledger.Event) (T, bool) {
	p, err := ledger.Decode[T](e)

	return p, err == nil || errors.Is(err, ledger.ErrNewerVersion)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return strings.TrimSpace(line)
}

// ReadLedger is every ledger row Build needs: the sessions of tasks, by the
// scoped read, and the project's regression_found rows, whose task is the
// bug rather than a neighbor. Both reads are indexed, so neither grows with
// the rest of the ledger.
func ReadLedger(ctx context.Context, path, project string, tasks []string) ([]ledger.Event, error) {
	var events []ledger.Event

	if len(tasks) > 0 {
		var err error
		if events, err = ledger.ReadScope(ctx, path, ledger.TaskScope(project, tasks...)); err != nil {
			return nil, err
		}
	}

	found, err := ledger.ReadEvents(ctx, path, ledger.Filter{Project: project, Kind: ledger.RegressionFound})
	if err != nil {
		return nil, err
	}

	return append(events, found...), nil
}

// catchupDecision is a merge conflict's settlement, whose body the catchup
// step opens with `catchup: <path>`: which side a merge kept says nothing
// about the design a plan must respect, and would crowd out what does.
func catchupDecision(e lore.Entry) bool {
	return e.Kind == lore.KindDecision && strings.HasPrefix(strings.TrimSpace(e.Body), "catchup:")
}
