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
// its period: commits, PRs opened or merged, force-pushes, reviews and
// merges of others'.
func ActivityTimes(r Report) []time.Time {
	return activityTimes(r.Tasks, r.Other, r.Reviewed, r.Since)
}

func activityTimes(tasks []Task, other Other, reviewed []ReviewedPR, since time.Time) []time.Time {
	var out []time.Time

	for _, t := range tasks {
		out = append(out, taskTimes(t, since)...)
	}

	out = append(out, taskTimes(Task{PRs: other.PRs, Commits: other.Commits}, since)...)

	for _, p := range reviewed {
		out = append(out, actionTimes(p, since)...)
	}

	return out
}

// A Day is a local date a task had activity on, and the class of that
// day's activity alone: a task worked on Monday and only caught up with
// its base on Tuesday is work on one and upkeep on the other.
type Day struct {
	Date  string `json:"date"`
	Class string `json:"class"`
}

// SetDays fills in the local dates with activity: the report's, across
// every project, each task's with that day's class, and each reviewed
// PR's. All three read the same times, so a task's day is always one of
// the report's. It runs after Merge, so a task merged across repositories
// gets the days of all of them.
func SetDays(s *Standup, loc *time.Location) {
	var all []time.Time

	for i := range s.Projects {
		p := &s.Projects[i]
		all = append(all, activityTimes(p.Tasks, p.Other, p.Reviewed, s.Since)...)

		for j := range p.Tasks {
			setTaskDays(&p.Tasks[j], s.Since, loc)
		}

		for j := range p.Reviewed {
			p.Reviewed[j].Days = Days(actionTimes(p.Reviewed[j], s.Since), loc)
		}
	}

	s.Days = Days(all, loc)
}

func setTaskDays(t *Task, since time.Time, loc *time.Location) {
	t.Days = []Day{}

	for _, d := range Days(taskTimes(*t, since), loc) {
		from, err := time.ParseInLocation(time.DateOnly, d, loc)
		if err != nil {
			continue
		}

		to := from.AddDate(0, 0, 1)
		if from.Before(since) {
			from = since
		}

		t.Days = append(t.Days, Day{Date: d, Class: taskClass(*t, from, to)})
	}
}

// taskTimes is the times a task's activity happened in the period.
func taskTimes(t Task, since time.Time) []time.Time {
	var out []time.Time

	add := func(at time.Time) {
		if inWindow(at, since, time.Time{}) {
			out = append(out, at)
		}
	}

	for _, p := range t.PRs {
		add(p.CreatedAt)

		if p.MergedAt != nil {
			add(*p.MergedAt)
		}

		for _, c := range p.Commits {
			add(c.At)
		}

		for _, fp := range p.ForcePushes {
			add(fp.At)
		}
	}

	for _, c := range t.Commits {
		add(c.At)
	}

	return out
}

func actionTimes(p ReviewedPR, since time.Time) []time.Time {
	var out []time.Time

	for _, a := range p.Actions {
		if inWindow(a.At, since, time.Time{}) {
			out = append(out, a.At)
		}
	}

	return out
}

// inWindow reports whether at is set and falls from from until to; a zero
// to has no end.
func inWindow(at, from, to time.Time) bool {
	return !at.IsZero() && !at.Before(from) && (to.IsZero() || at.Before(to))
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
