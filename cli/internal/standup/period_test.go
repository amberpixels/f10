package standup

import (
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/calendar"
)

// Days are read in a fixed zone east of UTC, so a UTC time near midnight
// lands on the next local day.
var kyiv = time.FixedZone("EEST", 3*60*60)

// local is a day in October 2026.
func local(d, h int) time.Time { return time.Date(2026, time.October, d, h, 0, 0, 0, kyiv) }

// 2026-10-12 is a Monday.
func TestResolve(t *testing.T) {
	monday, wednesday := local(12, 9), local(14, 9)
	since := local(1, 0)

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
		{"week, on a Monday", monday, time.Time{}, true, nil, local(5, 0), PeriodWeek},
		{"week, on a Wednesday", wednesday, time.Time{}, true, nil, local(5, 0), PeriodWeek},
		{"week, on a Sunday", local(18, 9), time.Time{}, true, nil, local(5, 0), PeriodWeek},
		{
			"Friday's standup, read on Monday", monday,
			time.Time{},
			false,
			&Last{At: local(9, 10), Since: local(8, 10)}, local(9, 10), PeriodLastStandup,
		},
		{
			"a standup reported this morning gives its own start again", monday,
			time.Time{},
			false,
			&Last{At: local(12, 8), Since: local(9, 10)}, local(9, 10), PeriodLastStandup,
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
	now := local(12, 9)
	last := &Last{At: time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC), Since: local(9, 10)}

	if got, _ := Resolve(now, time.Time{}, false, last); !got.Equal(last.Since) {
		t.Errorf("Resolve = %v, want the reuse of %v: 22:30 UTC is 01:30 here, today", got, last.Since)
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

func TestActivityTimesCountForcePushes(t *testing.T) {
	since := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	pushed := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)

	// a rebase keeps author dates, so the force-push is the PR's only time in the period
	r := Report{Since: since, Tasks: []Task{{ID: "FT-1", PRs: []PR{{
		CreatedAt:   since.AddDate(0, 0, -3),
		ForcePushes: []ForcePush{{At: pushed, Class: ClassUpkeep, Upkeep: UpkeepRebase}},
	}}}}}

	if got, want := Days(ActivityTimes(r), kyiv), []string{"2026-10-12"}; !slices.Equal(got, want) {
		t.Errorf("Days = %v, want %v: a force-push is activity", got, want)
	}

	got := calendar.LastActiveWorkday(local(13, 9), calendar.DefaultWorkdays(), ActivityTimes(r))
	if !got.Equal(local(12, 0)) {
		t.Errorf("LastActiveWorkday = %v, want the force-push's Monday", got)
	}
}

func TestSetDays(t *testing.T) {
	since := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC) // Monday 01:30 in kyiv
	monday := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	r := Standup{Since: since, Projects: []Project{{
		Tasks: []Task{
			// worked on Friday, only merged main in on Monday
			{ID: "FT-1", PRs: []PR{{
				CreatedAt: since.AddDate(0, 0, -3),
				Commits: []Commit{
					{At: friday, Class: ClassWork},
					{At: late, Class: ClassUpkeep, Upkeep: UpkeepBaseMerge},
				},
			}}},
			// opened on Monday, merged on Tuesday: both are work
			{ID: "FT-2", PRs: []PR{{CreatedAt: monday, MergedAt: &tuesday}}},
			// a rebase on Tuesday and nothing else
			{ID: "FT-3", PRs: []PR{{
				CreatedAt:   since.AddDate(0, 0, -3),
				ForcePushes: []ForcePush{{At: tuesday, Class: ClassUpkeep, Upkeep: UpkeepRebase}},
			}}},
		},
		Reviewed: []ReviewedPR{{Actions: []Action{
			{Kind: ActionReviewed, At: friday}, {Kind: ActionReviewed, At: since.Add(-time.Hour)},
		}}},
	}}}

	SetDays(&r, kyiv)

	want := map[string][]Day{
		"FT-1": {{"2026-10-09", ClassWork}, {"2026-10-12", ClassUpkeep}},
		"FT-2": {{"2026-10-12", ClassWork}, {"2026-10-13", ClassWork}},
		"FT-3": {{"2026-10-13", ClassUpkeep}},
	}

	for _, task := range r.Projects[0].Tasks {
		if !slices.Equal(task.Days, want[task.ID]) {
			t.Errorf("%s days = %v, want %v", task.ID, task.Days, want[task.ID])
		}
	}

	if want := []string{"2026-10-09"}; !slices.Equal(r.Projects[0].Reviewed[0].Days, want) {
		t.Errorf(
			"reviewed days = %v, want %v: the review before the period is not in it",
			r.Projects[0].Reviewed[0].Days,
			want,
		)
	}

	if want := []string{"2026-10-09", "2026-10-12", "2026-10-13"}; !slices.Equal(r.Days, want) {
		t.Errorf("report days = %v, want %v", r.Days, want)
	}

	empty := Standup{Since: since, Projects: []Project{{Tasks: []Task{{ID: "FT-9"}}}}}
	SetDays(&empty, kyiv)

	if empty.Projects[0].Tasks[0].Days == nil {
		t.Error("a task with no activity has nil days, which marshals as null, want []")
	}
}
