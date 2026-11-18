package standup

import (
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// lastActive tells a day that held activity from the last working day it
// falls back to, so the fallback knows when the first pass can be trusted.
func TestLastActiveSaysWhetherTheDayHeldActivity(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, loc) // a Wednesday
	from := calendar.FallbackFrom(now)
	weekdays := calendar.Workdays{false, true, true, true, true, true, false}

	in := func(at ...time.Time) standup.Input {
		var cs []standup.Commit
		for _, a := range at {
			cs = append(cs, standup.Commit{
				SHA: a.Format(time.RFC3339), Subject: "x", At: a, Branch: "main", Parents: []string{"p"},
			})
		}

		return standup.Input{Since: from, DefaultBranch: "main", Commits: cs}
	}

	monday := time.Date(2026, 11, 16, 0, 0, 0, 0, loc)

	day, found := lastActive([]standup.Input{in(monday.Add(10 * time.Hour))}, now, weekdays)
	if !found || !day.Equal(monday) {
		t.Errorf("Monday's commit: %v found %v, want Monday found", day, found)
	}

	day, found = lastActive([]standup.Input{in(now.Add(-time.Hour))}, now, weekdays)
	if found || !day.Equal(time.Date(2026, 11, 17, 0, 0, 0, 0, loc)) {
		t.Errorf("only today's commit: %v found %v, want Tuesday by default, not found", day, found)
	}
}

// fallbackStart reads detail from the first pass's day when it found
// activity, else over the whole window, and stamps the final start on
// every input.
func TestFallbackStartPicksTheDetailWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) // a Wednesday
	from := calendar.FallbackFrom(now)
	weekdays := calendar.Workdays{false, true, true, true, true, true, false}
	monday := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	tuesday := monday.AddDate(0, 0, 1)

	for _, c := range []struct {
		name         string
		at           time.Time
		window, want time.Time
	}{
		{"activity on Monday", monday.Add(10 * time.Hour), monday, monday},
		{"activity only today", now.Add(-time.Hour), from, tuesday},
	} {
		ins := []standup.Input{{
			Since: from, DefaultBranch: "main",
			Commits: []standup.Commit{{SHA: "a", Subject: "x", At: c.at, Branch: "main", Parents: []string{"p"}}},
		}}

		var read []time.Time

		start := fallbackStart(ins, now, from, weekdays, func(at time.Time) { read = append(read, at) })

		if len(read) != 1 || !read[0].Equal(c.window) {
			t.Errorf("%s: detail read from %v, want once from %v", c.name, read, c.window)
		}

		if !start.Equal(c.want) || !ins[0].Since.Equal(c.want) {
			t.Errorf("%s: start %v, input since %v, want %v", c.name, start, ins[0].Since, c.want)
		}
	}
}

// The text report lists the warnings, since it printed without those PRs.
func TestRenderStandupListsWarnings(t *testing.T) {
	var b strings.Builder

	s := standup.Standup{Warnings: []string{"app#3: commits not read: gh: HTTP 504"}}
	if err := RenderStandup(&b, nil, s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(b.String(), "warnings\n  app#3: commits not read: gh: HTTP 504\n") {
		t.Errorf("report = %q, want the warning listed", b.String())
	}
}
