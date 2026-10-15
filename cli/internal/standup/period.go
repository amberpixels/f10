package standup

import (
	"slices"
	"time"
)

// How a report's start was chosen, echoed as its period.
const (
	PeriodLastStandup    = "last_standup"
	PeriodLastWorkingDay = "last_working_day"
	PeriodSince          = "since"
	PeriodWeek           = "week"
)

// FallbackDays is how far back a run with no stored standup looks for the
// last working day with activity.
const FallbackDays = 14

// Last is the standup the user last reported: when, and the start it used.
type Last struct {
	At    time.Time `json:"at"`
	Since time.Time `json:"since"`
}

// Workdays is the working weekdays, indexed by time.Weekday. Only a run
// with no stored standup reads it.
type Workdays [7]bool

// DefaultWorkdays is Monday to Friday.
func DefaultWorkdays() Workdays {
	var w Workdays
	for d := time.Monday; d <= time.Friday; d++ {
		w[d] = true
	}

	return w
}

// Resolve picks the period's start: an explicit since, the last week, or
// the last reported standup. A standup reported earlier today gives its own
// start again, so a second report the same day is not empty. With nothing
// stored it returns PeriodLastWorkingDay and a zero time, for the caller to
// settle with LastWorkingDay once it has the activity. Days are in now's
// location.
func Resolve(now, since time.Time, week bool, last *Last) (time.Time, string) {
	switch {
	case !since.IsZero():
		return since, PeriodSince
	case week:
		monday := midnight(now).AddDate(0, 0, -(int(now.Weekday())+6)%7)

		return monday.AddDate(0, 0, -7), PeriodWeek
	case last == nil:
		return time.Time{}, PeriodLastWorkingDay
	case sameDay(last.At.In(now.Location()), now):
		return last.Since, PeriodLastStandup
	default:
		return last.At, PeriodLastStandup
	}
}

// FallbackFrom is the start of the window LastWorkingDay looks through.
func FallbackFrom(now time.Time) time.Time {
	return midnight(now).AddDate(0, 0, -FallbackDays)
}

// LastWorkingDay is local midnight of the latest working day before today
// with activity at any of the times. With none in the window, it is the
// previous working day, so a first run never reaches back weeks.
func LastWorkingDay(now time.Time, w Workdays, activity []time.Time) time.Time {
	active := map[string]bool{}
	for _, d := range Days(activity, now.Location()) {
		active[d] = true
	}

	today := midnight(now)

	for i := 1; i <= FallbackDays; i++ {
		day := today.AddDate(0, 0, -i)
		if w[day.Weekday()] && active[day.Format(time.DateOnly)] {
			return day
		}
	}

	for i := 1; i <= 7; i++ {
		if day := today.AddDate(0, 0, -i); w[day.Weekday()] {
			return day
		}
	}

	return today.AddDate(0, 0, -1)
}

// ActivityTimes is every time in a report the user did something within
// its period: commits, PRs opened or merged, reviews and merges of others'.
func ActivityTimes(r Report) []time.Time {
	var out []time.Time

	add := func(t time.Time) {
		if !t.IsZero() && !t.Before(r.Since) {
			out = append(out, t)
		}
	}

	prs := func(ps []PR) {
		for _, p := range ps {
			add(p.CreatedAt)

			if p.MergedAt != nil {
				add(*p.MergedAt)
			}

			for _, c := range p.Commits {
				add(c.At)
			}
		}
	}

	for _, t := range r.Tasks {
		prs(t.PRs)

		for _, c := range t.Commits {
			add(c.At)
		}
	}

	prs(r.Other.PRs)

	for _, c := range r.Other.Commits {
		add(c.At)
	}

	for _, p := range r.Reviewed {
		for _, a := range p.Actions {
			add(a.At)
		}
	}

	return out
}

// Days is the dates, YYYY-MM-DD in loc, that the times fall on: ascending,
// each once.
func Days(times []time.Time, loc *time.Location) []string {
	out := []string{}
	for _, t := range times {
		out = append(out, t.In(loc).Format(time.DateOnly))
	}

	slices.Sort(out)

	return slices.Compact(out)
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()

	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()

	return ay == by && am == bm && ad == bd
}
