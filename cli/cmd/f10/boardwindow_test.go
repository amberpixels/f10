package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/standup"
)

// Every named window is years' span at the board's now; the default is
// today and the last working day, the user's working days deciding which
// day that is; the state view takes a start alone.
func TestResolveBoardSpan(t *testing.T) {
	monday := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	monFri := func() (standup.Workdays, error) { return standup.DefaultWorkdays(), nil }
	sunThu := func() (standup.Workdays, error) {
		var w standup.Workdays
		for d := time.Sunday; d <= time.Thursday; d++ {
			w[d] = true
		}

		return w, nil
	}

	for _, c := range []struct {
		arg        string
		workdays   func() (standup.Workdays, error)
		since, end time.Time
		chosen     string
	}{
		{"today", monFri, day(26), day(27), "today"},
		{"yesterday", monFri, day(25), day(26), "yesterday"},
		{"last-workday", monFri, day(23), day(24), "last-workday"},
		{"last-workday", sunThu, day(25), day(26), "last-workday"},
		{"this-week", monFri, day(26), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "this-week"},
		{"last-week", monFri, day(19), day(26), "last-week"},
		{"2026-10-20", monFri, day(20), day(21), "2026-10-20"},
		{"2026-10-20T10:00:00Z", monFri, day(20).Add(10 * time.Hour), time.Time{}, "since 2026-10-20T10:00:00Z"},
	} {
		span, err := resolveBoardSpan(c.arg, false, monday, c.workdays)
		if err != nil || span.named == nil {
			t.Errorf("%s: %+v, %v", c.arg, span, err)

			continue
		}

		if w := *span.named; !w.Since.Equal(c.since) || !w.End.Equal(c.end) || w.Chosen != c.chosen {
			t.Errorf("%s = %+v, want %v to %v as %q", c.arg, w, c.since, c.end, c.chosen)
		}
	}

	span, err := resolveBoardSpan("", false, monday, monFri)
	if err != nil || span.named != nil || !span.today.Since.Equal(day(26)) || !span.lastWorkday.Since.Equal(day(23)) ||
		!span.from().Equal(day(23)) {
		t.Errorf("default = %+v, %v", span, err)
	}

	if span, _ := resolveBoardSpan("", true, monday, monFri); span.named == nil ||
		!span.named.Since.Equal(monday.Add(-boardStateWindow)) {
		t.Errorf("state default = %+v", span)
	}

	if span, _ := resolveBoardSpan("yesterday", true, monday, monFri); span.named == nil ||
		!span.named.Since.Equal(day(25)) || !span.named.End.IsZero() {
		t.Errorf("state yesterday = %+v", span)
	}

	broken := func() (standup.Workdays, error) { return standup.Workdays{}, errors.New("standup.md: bad") }
	if _, err := resolveBoardSpan("today", false, monday, broken); err != nil {
		t.Errorf("a named window read the working days: %v", err)
	}

	if _, err := resolveBoardSpan("", false, monday, broken); err == nil {
		t.Error("the default ignored a broken standup.md")
	}

	if _, err := resolveBoardSpan("someday", false, monday, monFri); err == nil ||
		!strings.Contains(err.Error(), "last-workday") {
		t.Errorf("someday: %v, want the windows named", err)
	}
}

func TestBoardWindowArgs(t *testing.T) {
	for _, args := range [][]string{{"today", "yesterday"}, {"--since", "today", "yesterday"}} {
		err := newApp().Run(t.Context(), append([]string{"f10", "board"}, args...))
		if err == nil || !strings.Contains(err.Error(), "window") {
			t.Errorf("%v: err = %v, want the window refused", args, err)
		}
	}
}
