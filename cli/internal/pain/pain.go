// Package pain is where changing code was costly over a range: per area,
// the review rounds, halts, replans, recurring fixed findings, regressions
// and implement time its tasks took, against the same figures over every
// task in the range. Audit reads it as where to look, never as findings.
package pain

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/history"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

// RecurringFloor is the fewest tasks of an area that fixed a category for it
// to recur: a skip is a reviewer's noise, a fix is a cost paid.
const RecurringFloor = 3

// AreaTasks is one area of the partition and the tasks whose commits in the
// range touched it.
type AreaTasks struct {
	Dir   string
	Tasks []string
}

// Input is the partition and the ledger rows of its tasks' sessions, with
// the project's regression_found rows (history.ReadLedger).
type Input struct {
	Areas  []AreaTasks
	Events []ledger.Event
	Since  time.Time
}

// Map is the areas over the project on at least one signal, each with the
// signals that put it there, and the regression lineages of the range.
type Map struct {
	Project  Figures   `json:"project"`
	Areas    []Area    `json:"areas"`
	Lineages []Lineage `json:"lineages"`
}

// Figures are the project's: medians over the tasks that recorded the
// figure, rates per task; a nil median had too few tasks to mean anything.
type Figures struct {
	Tasks          int      `json:"tasks"`
	MedianRounds   *float64 `json:"median_rounds"`
	HaltRate       float64  `json:"halts_per_task"`
	SupersedeRate  float64  `json:"superseded_per_task"`
	RegressionRate float64  `json:"regressions_per_task"`
	Density        *float64 `json:"implement_minutes_per_100_lines"`
}

// Area is one area's line: only the signals over the project are set.
type Area struct {
	Dir          string               `json:"dir"`
	Tasks        []string             `json:"tasks"`
	MedianRounds *float64             `json:"median_rounds,omitempty"`
	MaxRounds    int                  `json:"max_rounds,omitempty"`
	Halts        []Halt               `json:"halts,omitempty"`
	Superseded   []string             `json:"superseded,omitempty"` // a task per superseded plan
	Recurring    []history.Recurring  `json:"recurring,omitempty"`
	Regressions  []history.Regression `json:"regressions,omitempty"`
	Density      *float64             `json:"implement_minutes_per_100_lines,omitempty"`
	Line         string               `json:"line"`
}

// Halt is a drive stopping one of the area's tasks.
type Halt struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// Lineage is a task of the range and the bug tasks traced to it.
type Lineage struct {
	Origin string   `json:"origin"`
	Bugs   []string `json:"bugs"`
	Class  []string `json:"classes"`
}

// Build is the map: the facts of every task once, then each area against
// the project.
func Build(in Input) *Map {
	var all []string

	for _, a := range in.Areas {
		for _, t := range a.Tasks {
			if !slices.ContainsFunc(all, func(s string) bool { return strings.EqualFold(s, t) }) {
				all = append(all, t)
			}
		}
	}

	slices.Sort(all)

	facts := history.TaskFacts(in.Events, all, in.Since)
	m := &Map{Project: figures(all, facts), Areas: []Area{}, Lineages: lineages(all, facts)}

	for _, a := range in.Areas {
		if line, ok := area(a, facts, m.Project); ok {
			m.Areas = append(m.Areas, line)
		}
	}

	slices.SortFunc(m.Areas, func(x, y Area) int { return cmp.Compare(x.Dir, y.Dir) })

	return m
}

func figures(tasks []string, facts map[string]*history.Facts) Figures {
	f := Figures{Tasks: len(tasks)}
	if len(tasks) == 0 {
		return f
	}

	var halts, superseded, regressions int

	for _, t := range tasks {
		tf := facts[strings.ToUpper(t)]
		halts += tf.Halts
		superseded += tf.Superseded
		regressions += len(tf.Regressions)
	}

	n := float64(len(tasks))
	f.HaltRate, f.SupersedeRate, f.RegressionRate = float64(halts)/n, float64(superseded)/n, float64(regressions)/n
	f.MedianRounds, _ = medianRounds(tasks, facts)
	f.Density = medianDensity(tasks, facts)

	return f
}

// area is a's line and whether any signal put it over the project.
func area(a AreaTasks, facts map[string]*history.Facts, p Figures) (Area, bool) {
	out := Area{Dir: a.Dir, Tasks: slices.Sorted(slices.Values(a.Tasks))}
	n := float64(len(a.Tasks))

	if n == 0 {
		return out, false
	}

	var (
		halts       []Halt
		superseded  []string
		regressions []history.Regression
		fixedIn     = map[string][]string{}
	)

	for _, t := range out.Tasks {
		f := facts[strings.ToUpper(t)]

		for i := range f.Halts {
			reason := "no reason recorded"
			if i < len(f.HaltReasons) && f.HaltReasons[i] != "" {
				reason = f.HaltReasons[i]
			}

			halts = append(halts, Halt{Task: t, Reason: reason})
		}

		for range f.Superseded {
			superseded = append(superseded, t)
		}

		for i, bug := range f.Regressions {
			regressions = append(regressions, history.Regression{Bug: bug, Origin: t, Class: f.Classes[i]})
		}

		for cat, c := range f.Categories {
			if c.Fixed > 0 {
				fixedIn[cat] = append(fixedIn[cat], t)
			}
		}
	}

	if m, top := medianRounds(out.Tasks, facts); m != nil && p.MedianRounds != nil && *m > *p.MedianRounds {
		out.MedianRounds, out.MaxRounds = m, top
	}

	if len(halts) > 0 && float64(len(halts))/n > p.HaltRate {
		out.Halts = halts
	}

	if len(superseded) > 0 && float64(len(superseded))/n > p.SupersedeRate {
		out.Superseded = superseded
	}

	if len(regressions) > 0 && float64(len(regressions))/n > p.RegressionRate {
		out.Regressions = regressions
	}

	for cat, tasks := range fixedIn {
		if len(tasks) >= RecurringFloor {
			out.Recurring = append(out.Recurring, history.Recurring{Category: cat, Tasks: tasks})
		}
	}

	slices.SortFunc(out.Recurring, func(x, y history.Recurring) int {
		return cmp.Or(cmp.Compare(len(y.Tasks), len(x.Tasks)), cmp.Compare(x.Category, y.Category))
	})

	if d := medianDensity(out.Tasks, facts); d != nil && p.Density != nil && *d > *p.Density {
		out.Density = d
	}

	out.Line = out.line(p)

	return out, out.MedianRounds != nil || out.Halts != nil || out.Superseded != nil ||
		out.Regressions != nil || out.Recurring != nil || out.Density != nil
}

// medianRounds is the median and the max over the tasks a round was
// recorded for, nil under history.MinSample of them.
func medianRounds(tasks []string, facts map[string]*history.Facts) (*float64, int) {
	var rounds []float64

	top := 0

	for _, t := range tasks {
		if r := facts[strings.ToUpper(t)].Rounds; r > 0 {
			rounds = append(rounds, float64(r))
			top = max(top, r)
		}
	}

	if len(rounds) < history.MinSample {
		return nil, 0
	}

	m := median(rounds)

	return &m, top
}

// medianDensity is the median implement minutes per 100 lines over the
// tasks that recorded both, nil under history.MinSample of them.
func medianDensity(tasks []string, facts map[string]*history.Facts) *float64 {
	var ds []float64

	for _, t := range tasks {
		f := facts[strings.ToUpper(t)]
		if f.ImplementMinutes != nil && f.Lines != nil && *f.Lines > 0 {
			ds = append(ds, *f.ImplementMinutes*100/float64(*f.Lines))
		}
	}

	if len(ds) < history.MinSample {
		return nil
	}

	m := median(ds)

	return &m
}

func lineages(tasks []string, facts map[string]*history.Facts) []Lineage {
	out := []Lineage{}

	for _, t := range tasks {
		if f := facts[strings.ToUpper(t)]; len(f.Regressions) > 0 {
			out = append(out, Lineage{Origin: t, Bugs: f.Regressions, Class: f.Classes})
		}
	}

	return out
}

func median(vs []float64) float64 {
	s := slices.Sorted(slices.Values(vs))
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}

	return s[len(s)/2]
}

// NoPain is the text form of a map with no area over the project.
const NoPain = "No area over the project median."

// Lines is the text form: one line per area.
func (m *Map) Lines() []string {
	if len(m.Areas) == 0 {
		return []string{NoPain}
	}

	out := make([]string, len(m.Areas))
	for i, a := range m.Areas {
		out[i] = a.Line
	}

	return out
}

func (a Area) line(p Figures) string {
	var parts []string

	if a.MedianRounds != nil {
		parts = append(parts, fmt.Sprintf("%s review rounds vs %s (max %d)",
			number(*a.MedianRounds), number(*p.MedianRounds), a.MaxRounds))
	}

	if len(a.Halts) > 0 {
		hs := make([]string, len(a.Halts))
		for i, h := range a.Halts {
			hs[i] = h.Task + ": " + h.Reason
		}

		parts = append(
			parts,
			fmt.Sprintf("%d %s (%s)", len(a.Halts), plural(len(a.Halts), "halt", "halts"), strings.Join(hs, "; ")),
		)
	}

	if len(a.Superseded) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%d %s (%s)",
			len(a.Superseded),
			plural(
				len(a.Superseded),
				"plan superseded",
				"plans superseded",
			),
			strings.Join(slices.Compact(a.Superseded), ", "),
		))
	}

	for _, r := range a.Recurring {
		parts = append(parts, fmt.Sprintf("%q fixed in %d (%s)", r.Category, len(r.Tasks), strings.Join(r.Tasks, ", ")))
	}

	if len(a.Regressions) > 0 {
		rs := make([]string, len(a.Regressions))
		for i, r := range a.Regressions {
			rs[i] = r.Bug + " from " + r.Origin
			if r.Class != "" {
				rs[i] += " (" + r.Class + ")"
			}
		}

		parts = append(parts, fmt.Sprintf("%d %s: %s", len(a.Regressions),
			plural(len(a.Regressions), "regression", "regressions"), strings.Join(rs, ", ")))
	}

	if a.Density != nil {
		parts = append(
			parts,
			fmt.Sprintf("%s implement minutes per 100 lines vs %s", number(*a.Density), number(*p.Density)),
		)
	}

	return a.Dir + ": " + strings.Join(parts, "; ")
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
