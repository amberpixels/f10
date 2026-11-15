package history

import (
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/timing"
)

// Facts is what the ledger recorded about one task; a nil pointer was never
// recorded. The history brief, the audit's pain map and any later reader
// count from this one fold, so a round or a fix counts the same everywhere.
type Facts struct {
	Lines            *int             `json:"lines,omitempty"`
	ImplementMinutes *float64         `json:"implement_minutes,omitempty"`
	Rounds           int              `json:"rounds"`
	Categories       map[string]Count `json:"categories,omitempty"`
	Superseded       int              `json:"plans_superseded"`
	Halts            int              `json:"halts"`
	HaltReasons      []string         `json:"halt_reasons,omitempty"` // one per halt, "" where none was given
	Verdicts         []string         `json:"judge_verdicts,omitempty"`
	// Regressions are the bug tasks traced to this one, Classes their
	// review-miss classes in the same order
	Regressions []string `json:"regressions,omitempty"`
	Classes     []string `json:"-"`
}

// Series ties a task's review rows to the PR they reviewed: a follow-up
// PR's rounds restart at 1, so a round is its number with the reviewed sha
// of its latest opening, and a verdict belongs to that latest opening.
// Every reader of review rows keys them through it, so none merges two PRs.
type Series map[string]string // round: the reviewed sha of its latest opening

// Opened records a round's opening and returns the round's key.
func (s Series) Opened(round, reviewed string) string {
	s[round] = reviewed

	return round + "\x00" + reviewed
}

// Of is the key of the round a verdict on round belongs to.
func (s Series) Of(round string) string { return round + "\x00" + s[round] }

// Found is a bug's newest attribution.
type Found struct {
	ledger.RegressionFoundPayload

	Bug string
}

// TaskFacts folds the events into each task's facts, keyed by the upper-cased
// id: the newest verdict per finding and per verdict file standing, and the
// regressions found since traced to the task. Every task gets an entry.
func TaskFacts(events []ledger.Event, tasks []string, since time.Time) map[string]*Facts {
	facts := make(map[string]*Facts, len(tasks))
	for _, id := range tasks {
		facts[strings.ToUpper(id)] = &Facts{Categories: map[string]Count{}}
	}

	rounds := map[string]map[string]map[string]int{}      // task, review pass: the latest opening's categories
	series := map[string]Series{}                         // task: its review series
	findings := map[string]ledger.FindingVerdictPayload{} // task, round, series, finding
	judged := map[string]string{}                         // task, verdict file

	var judgedOrder []string

	for _, e := range events {
		key := strings.ToUpper(e.Task)

		f := facts[key]
		if f == nil {
			continue
		}

		switch e.Kind {
		case ledger.TaskSized:
			if p, ok := decode[ledger.TaskSizedPayload](e); ok {
				f.Lines = &p.Lines
			}
		case ledger.ReviewRoundOpened:
			if p, ok := decode[ledger.ReviewRoundOpenedPayload](e); ok && p.Round != "" {
				if rounds[key] == nil {
					rounds[key] = map[string]map[string]int{}
				}

				if series[key] == nil {
					series[key] = Series{}
				}

				rounds[key][series[key].Opened(p.Round, p.Reviewed)] = p.Categories
			}
		case ledger.FindingVerdict:
			if p, ok := decode[ledger.FindingVerdictPayload](e); ok {
				findings[key+"\x00"+series[key].Of(p.Round)+"\x00"+p.Finding] = p
			}
		case ledger.PlanSuperseded:
			f.Superseded++
		case ledger.TaskHalted:
			f.Halts++
			f.HaltReasons = append(f.HaltReasons, firstLine(e.Reason())) // "" for none, one per halt
		case ledger.JudgeVerdict:
			if p, ok := decode[ledger.JudgeVerdictPayload](e); ok && p.Verdict != "" {
				k := key + "\x00" + p.Path
				if _, seen := judged[k]; !seen {
					judgedOrder = append(judgedOrder, k)
				}

				judged[k] = p.Verdict
			}
		}
	}

	for key, byRound := range rounds {
		f := facts[key]
		f.Rounds = len(byRound)

		for _, cats := range byRound {
			for cat, raised := range cats {
				if raised > 0 {
					c := f.Categories[cat]
					c.Raised += raised
					f.Categories[cat] = c
				}
			}
		}
	}

	for k, p := range findings {
		key, _, _ := strings.Cut(k, "\x00")
		f := facts[key]

		if p.Category == "" {
			continue
		}

		c := f.Categories[p.Category]

		switch word, _, _ := strings.Cut(strings.ToLower(p.Verdict), " "); word {
		case "fix":
			c.Fixed++
		case "skip":
			c.Skipped++
		default:
			continue
		}

		f.Categories[p.Category] = c
	}

	for _, k := range judgedOrder {
		key, _, _ := strings.Cut(k, "\x00")
		facts[key].Verdicts = append(facts[key].Verdicts, judged[k])
	}

	for _, r := range latestRegressions(events, since) {
		if f := facts[strings.ToUpper(r.OriginTask)]; f != nil {
			f.Regressions = append(f.Regressions, r.Bug)
			f.Classes = append(f.Classes, r.Class)
		}
	}

	implementMinutes(events, facts)

	return facts
}

// latestRegressions is each bug's newest attribution found since, in the
// order the bugs were first found.
func latestRegressions(events []ledger.Event, since time.Time) []Found {
	latest := map[string]ledger.RegressionFoundPayload{}

	var order []string

	for _, e := range events {
		if e.Kind != ledger.RegressionFound || e.At.Before(since) {
			continue
		}

		p, ok := decode[ledger.RegressionFoundPayload](e)
		if !ok {
			continue
		}

		if _, seen := latest[e.Task]; !seen {
			order = append(order, e.Task)
		}

		latest[e.Task] = p
	}

	out := make([]Found, 0, len(order))
	for _, bug := range order {
		out = append(out, Found{Bug: bug, RegressionFoundPayload: latest[bug]})
	}

	return out
}

// implementMinutes sums each task's implement windows; one with none stays
// nil, never zero.
func implementMinutes(events []ledger.Event, facts map[string]*Facts) {
	for _, w := range timing.Windows(events) {
		f := facts[strings.ToUpper(w.Task)]
		if f == nil || w.Step != timing.Implement {
			continue
		}

		m := w.Active.Minutes()
		if f.ImplementMinutes != nil {
			m += *f.ImplementMinutes
		}

		f.ImplementMinutes = &m
	}
}
