package board

import (
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// A boardSpan is the window the argument named, or for the feed's default
// two candidates chosen between once the facts are in.
type boardSpan struct {
	Named              *lookaround.Window
	Today, LastWorkday lookaround.Window
}

// From is where collection starts: the named window's start, or the
// earlier candidate's, so one collection answers both.
func (s boardSpan) From() time.Time {
	if s.Named != nil {
		return s.Named.Since
	}

	return s.LastWorkday.Since
}

const BoardStateWindow = 7 * 24 * time.Hour

// ResolveBoardSpan goes through calendar, which owns every calendar rule.
// Working days are read only when the default or `last-workday` needs them;
// the state view takes a start alone, seven days back without a window.
func ResolveBoardSpan(
	arg string, state bool, now time.Time, workdays func() (calendar.Workdays, error),
) (boardSpan, error) {
	arg = strings.TrimSpace(arg)

	if state && arg == "" {
		return boardSpan{Named: &lookaround.Window{Since: now.Add(-BoardStateWindow)}}, nil
	}

	// a broken standup.md is its own error, not a window misspelled
	var workdaysErr error

	read := func() (calendar.Workdays, error) {
		w, err := workdays()
		workdaysErr = err

		return w, err
	}

	window := func(name string) (lookaround.Window, error) {
		period, err := calendar.Parse(name, now, calendar.Windows, read)
		if err != nil {
			if workdaysErr != nil {
				return lookaround.Window{}, workdaysErr
			}

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

		return boardSpan{Named: &w}, err
	}

	today, err := window("today")
	if err != nil {
		return boardSpan{}, err
	}

	last, err := window("last-workday")
	if err != nil {
		return boardSpan{}, err
	}

	return boardSpan{Today: today, LastWorkday: last}, nil
}
