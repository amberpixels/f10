package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/years"

	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// A boardSpan is the window a board covers: the one the argument named,
// or, for the feed's default, two candidates chosen between once the
// facts are in (today, else the last working day).
type boardSpan struct {
	named              *lookaround.Window
	today, lastWorkday lookaround.Window
}

// from is where collection starts: the named window's start, or the
// earlier candidate's, so one collection answers both.
func (s boardSpan) from() time.Time {
	if s.named != nil {
		return s.named.Since
	}

	return s.lastWorkday.Since
}

// boardStateWindow is how far back the state view's done rows go without
// a window named.
const boardStateWindow = 7 * 24 * time.Hour

// fixedClock is years' clock read at the board's now, so every span is
// cut from the same instant the board reports.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// resolveBoardSpan turns the window argument into the board's span
// through years, which owns every calendar rule: `today`, `yesterday`,
// `last-workday`, `this-week`, `last-week`, a date, or a time. Weeks
// start on Monday; working days come from workdays, read only when the
// default or `last-workday` needs them. The state view takes the named
// window's start for its done rows, seven days back without one.
func resolveBoardSpan(
	arg string, state bool, now time.Time, workdays func() (standup.Workdays, error),
) (boardSpan, error) {
	arg = strings.TrimSpace(arg)

	if state && arg == "" {
		return boardSpan{named: &lookaround.Window{Since: now.Add(-boardStateWindow)}}, nil
	}

	opts := append(slices.Clone(years.GetParserDefaults()),
		years.WithLocation(now.Location()), years.WithWeekStart(time.Monday), years.WithCustomClock(fixedClock{now}))

	if arg == "" || arg == "last-workday" {
		w, err := workdays()
		if err != nil {
			return boardSpan{}, err
		}

		var days []time.Weekday

		for d, on := range w {
			if on {
				days = append(days, time.Weekday(d))
			}
		}

		opts = append(opts, years.WithWorkdays(days...))
	}

	p := years.NewParser(opts...)

	window := func(name string) (lookaround.Window, error) {
		period, err := p.JustParsePeriod(name)
		if err != nil {
			return lookaround.Window{}, fmt.Errorf(
				"window %q: want today, yesterday, last-workday, this-week, last-week, a date or a time", name)
		}

		w := lookaround.Window{Since: period.Start, Chosen: name}
		if period.IsInstant() {
			w.Chosen = "since " + name
		} else {
			w.End = period.End
		}

		if state {
			w = lookaround.Window{Since: period.Start}
		}

		return w, nil
	}

	if arg != "" {
		w, err := window(arg)

		return boardSpan{named: &w}, err
	}

	today, err := window("today")
	if err != nil {
		return boardSpan{}, err
	}

	last, err := window("last-workday")
	if err != nil {
		return boardSpan{}, err
	}

	return boardSpan{today: today, lastWorkday: last}, nil
}
