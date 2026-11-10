// Package calendar is every calendar rule the board, standup and stats
// share, on years: working days, the week starting Monday, how a --since
// or a window is read, and the two default-window fallbacks.
package calendar

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/amberpixels/years"
)

// Workdays is the working weekdays, indexed by time.Weekday.
type Workdays [7]bool

// DefaultWorkdays is Monday to Friday.
func DefaultWorkdays() Workdays {
	var w Workdays
	for d := time.Monday; d <= time.Friday; d++ {
		w[d] = true
	}

	return w
}

func (w Workdays) days() []time.Weekday {
	var out []time.Weekday

	for d, on := range w {
		if on {
			out = append(out, time.Weekday(d))
		}
	}

	return out
}

// Forms is what a command's --since or window accepts.
type Forms int

const (
	// Instants is an RFC 3339 time or a YYYY-MM-DD date at local midnight.
	Instants Forms = iota
	// Offsets is a count of days or weeks back from now: 10d, 3w.
	Offsets
	// Windows is every years alias (today, last-workday, last-week...), a
	// date, a time or an offset.
	Windows
)

// offsetRE is a count of whole days or weeks. Hours mean nothing against a
// tracker's created date, and a month has no fixed length.
var offsetRE = regexp.MustCompile(`^([1-9]\d*)([dw])$`)

// OffsetDays reads a count of days or weeks (10d, 3w) as days. Both
// stats --since and stale_after read this one grammar.
func OffsetDays(value string) (int, error) {
	m := offsetRE.FindStringSubmatch(value)

	var n int
	if m != nil {
		n, _ = strconv.Atoi(m[1])
	}

	// a count past 3650 is a typo, and a large one would overflow a Duration
	if n == 0 || n > 3650 {
		return 0, fmt.Errorf("%q is not a number of days or weeks, like 10d or 3w", value)
	}

	if m[2] == "w" {
		n *= 7
	}

	return n, nil
}

// fixedClock is years' clock read at one instant, so every period a run
// cuts starts from the same now.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// Parse reads arg as the forms allow, at now. Working days are read only
// when arg names last-workday, so a broken config fails no other run. The
// error is bare: each command names its flag and its forms.
func Parse(arg string, now time.Time, forms Forms, workdays func() (Workdays, error)) (years.Period, error) {
	var opts []years.ParserOption

	switch forms {
	case Instants:
		// years' default layouts, aliases, offsets and epochs would each take
		// input --since rejects
		opts = []years.ParserOption{years.WithLayouts(time.RFC3339Nano, time.DateOnly)}
	case Offsets:
		if _, err := OffsetDays(arg); err != nil {
			return years.Period{}, err
		}

		opts = []years.ParserOption{years.AcceptRelativeOffsets()}
	case Windows:
		opts = slices.Clone(years.GetParserDefaults())

		if arg == lastWorkday {
			w, err := workdays()
			if err != nil {
				return years.Period{}, err
			}

			opts = append(opts, years.WithWorkdays(w.days()...))
		}
	}

	opts = append(opts,
		years.WithLocation(now.Location()), years.WithWeekStart(time.Monday), years.WithCustomClock(fixedClock{now}))

	return years.NewParser(opts...).JustParsePeriod(arg)
}

const lastWorkday = "last-workday"

// LastWeekStart is last week's Monday at local midnight.
func LastWeekStart(now time.Time) time.Time {
	p, _ := Parse("last-week", now, Windows, nil)

	return p.Start
}

// With no window named, the board shows today, else (once collection finds
// today empty) the previous working day by the calendar: LastWorkday. Standup
// starts at the last reported standup, else at the latest working day with
// activity, so a first report covers the last day worked: LastActiveWorkday.

// LastWorkday is local midnight of the working day before today's.
func LastWorkday(now time.Time, w Workdays) time.Time {
	p, _ := Parse(lastWorkday, now, Windows, func() (Workdays, error) { return w, nil })

	return p.Start
}

// FallbackDays is how far back LastActiveWorkday looks for activity.
const FallbackDays = 14

// FallbackFrom is the start of the window LastActiveWorkday looks through.
func FallbackFrom(now time.Time) time.Time {
	return midnight(now).AddDate(0, 0, -FallbackDays)
}

// LastActiveWorkday is local midnight of the latest working day before
// today with activity; with none in the window, LastWorkday, so a first run
// never reaches back weeks.
func LastActiveWorkday(now time.Time, w Workdays, activity []time.Time) time.Time {
	active := map[string]bool{}
	for _, t := range activity {
		active[t.In(now.Location()).Format(time.DateOnly)] = true
	}

	today := midnight(now)

	for i := 1; i <= FallbackDays; i++ {
		day := today.AddDate(0, 0, -i)
		if w[day.Weekday()] && active[day.Format(time.DateOnly)] {
			return day
		}
	}

	return LastWorkday(now, w)
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()

	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
