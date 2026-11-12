package drive

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// FT-422's catchup: the agent started go test in the background at
// 23:39:37 and went idle; the drive looked at 23:40:49, past the grace, with
// no catchup report.
func TestStalledReadsBackgroundWorkAsWorking(t *testing.T) {
	day := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	sent := day.Add(23*time.Hour + 38*time.Minute + 34*time.Second)
	launched := day.Add(23*time.Hour + 39*time.Minute + 37*time.Second)
	look := day.Add(23*time.Hour + 40*time.Minute + 49*time.Second)

	cases := map[string]struct {
		bg   time.Time
		now  time.Time
		want bool
	}{
		"background go test pending":  {bg: launched, now: look},
		"no background work":          {now: look, want: true},
		"background past verifyLimit": {bg: launched, now: launched.Add(stack.VerifyLimit), want: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := stalled(
				agentctl.StatusIdle,
				nil,
				"catchup",
				sent,
				look.Sub(launched),
				stallGrace,
				tc.bg,
				tc.now,
			)
			if got != tc.want {
				t.Errorf("stalled = %v, want %v", got, tc.want)
			}
		})
	}
}
