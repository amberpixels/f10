package standup

import (
	"slices"
	"testing"
	"time"
)

// Days are read in a fixed zone east of UTC, so a UTC time near midnight
// lands on the next local day.
var kyiv = time.FixedZone("EEST", 3*60*60)

// local is an hour of a 2026 day in kyiv.
func local(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, kyiv) }

// 2026-10-12 is a Monday.
func TestResolve(t *testing.T) {
	monday, wednesday := local(10, 12, 9), local(10, 14, 9)
	since := local(10, 1, 0)

	cases := []struct {
		name       string
		now        time.Time
		since      time.Time
		week       bool
		last       *Last
		want       time.Time
		wantPeriod string
	}{
		{"an explicit since wins", monday, since, false, &Last{At: monday, Since: since}, since, PeriodSince},
		{"week, on a Monday", monday, time.Time{}, true, nil, local(10, 5, 0), PeriodWeek},
		{"week, on a Wednesday", wednesday, time.Time{}, true, nil, local(10, 5, 0), PeriodWeek},
		{
			"Friday's standup, read on Monday", monday,
			time.Time{},
			false,
			&Last{At: local(10, 9, 10), Since: local(10, 8, 10)}, local(10, 9, 10), PeriodLastStandup,
		},
		{
			"a standup reported this morning gives its own start again", monday,
			time.Time{},
			false,
			&Last{At: local(10, 12, 8), Since: local(10, 9, 10)}, local(10, 9, 10), PeriodLastStandup,
		},
		{"nothing stored", monday, time.Time{}, false, nil, time.Time{}, PeriodLastWorkingDay},
	}

	for _, c := range cases {
		got, period := Resolve(c.now, c.since, c.week, c.last)
		if !got.Equal(c.want) || period != c.wantPeriod {
			t.Errorf("%s: Resolve = %v %s, want %v %s", c.name, got, period, c.want, c.wantPeriod)
		}
	}
}

// The same day in UTC can be another day locally: the stored standup is
// compared in now's zone.
func TestResolveComparesDaysLocally(t *testing.T) {
	now := local(10, 12, 9)
	last := &Last{At: time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC), Since: local(10, 9, 10)}

	if got, _ := Resolve(now, time.Time{}, false, last); !got.Equal(last.Since) {
		t.Errorf("Resolve = %v, want the reuse of %v: 22:30 UTC is 01:30 here, today", got, last.Since)
	}
}

func TestLastWorkingDay(t *testing.T) {
	monday := local(10, 12, 9)
	weekend := DefaultWorkdays()
	weekend[time.Saturday] = true

	cases := []struct {
		name     string
		workdays Workdays
		activity []time.Time
		want     time.Time
	}{
		{
			"Friday's work, read on Monday",
			DefaultWorkdays(),
			[]time.Time{local(10, 9, 15)},
			local(10, 9, 0),
		},
		{
			"a Saturday off the calendar is skipped", DefaultWorkdays(),
			[]time.Time{local(10, 7, 11), local(10, 10, 11)},
			local(10, 7, 0),
		},
		{"a working Saturday counts", weekend, []time.Time{local(10, 10, 11)}, local(10, 10, 0)},
		{
			"today's work does not make today the start",
			DefaultWorkdays(),
			[]time.Time{local(10, 12, 8)},
			local(10, 9, 0),
		},
		{"no activity: the previous working day", DefaultWorkdays(), nil, local(10, 9, 0)},
		{
			"activity past the window does not count",
			DefaultWorkdays(),
			[]time.Time{local(9, 1, 10)},
			local(10, 9, 0),
		},
	}

	for _, c := range cases {
		if got := LastWorkingDay(monday, c.workdays, c.activity); !got.Equal(c.want) {
			t.Errorf("%s: LastWorkingDay = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestActivityTimesAndDays(t *testing.T) {
	since := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	merged := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	late := time.Date(2026, 10, 9, 22, 0, 0, 0, time.UTC) // Saturday 01:00 in kyiv

	r := Report{
		Since: since,
		Tasks: []Task{{ID: "FT-1", PRs: []PR{{
			CreatedAt: since.AddDate(0, 0, -3), // opened before the period: not activity in it
			MergedAt:  &merged,
			Commits:   []Commit{{At: late}},
		}}}},
		Other:    Other{Commits: []Commit{{At: since.Add(10 * time.Hour)}}},
		Reviewed: []ReviewedPR{{Actions: []Action{{Kind: ActionReviewed, At: merged}}}},
	}

	got := Days(ActivityTimes(r), kyiv)
	if want := []string{"2026-10-09", "2026-10-10", "2026-10-12"}; !slices.Equal(got, want) {
		t.Errorf("Days = %v, want %v", got, want)
	}

	if got := Days(nil, kyiv); got == nil || len(got) != 0 {
		t.Errorf("Days(nil) = %#v, want an empty list, which marshals as []", got)
	}
}
