package standup

import (
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
