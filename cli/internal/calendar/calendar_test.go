package calendar

import (
	"testing"
	"time"
)

// Days are read in a fixed zone east of UTC, so a UTC time near midnight
// lands on the next local day.
var kyiv = time.FixedZone("EEST", 3*60*60)

func local(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, kyiv) }

func monFri() (Workdays, error) { return DefaultWorkdays(), nil }

// Each form takes only its own spellings, so sharing one parser widens no
// command's --since.
func TestParseForms(t *testing.T) {
	now := local(10, 14, 9) // a Wednesday

	took := map[Forms][]string{
		Instants: {"2026-10-14T09:30:00+03:00", "2026-10-14"},
		Offsets:  {"30d", "2w", "3650d"},
		Windows:  {"today", "yesterday", "last-workday", "this-week", "last-week", "2026-10-01", "7d"},
	}
	refused := map[Forms][]string{
		Instants: {"", "2d", "yesterday", "2026-10-14 09:30", "1760000000"},
		Offsets:  {"", "0d", "3651d", "12h", "3mo", "2026-10-01", "yesterday"},
		Windows:  {"", "someday"},
	}

	for forms, args := range took {
		for _, arg := range args {
			if _, err := Parse(arg, now, forms, monFri); err != nil {
				t.Errorf("forms %d refused %q: %v", forms, arg, err)
			}
		}
	}

	for forms, args := range refused {
		for _, arg := range args {
			if _, err := Parse(arg, now, forms, monFri); err == nil {
				t.Errorf("forms %d took %q", forms, arg)
			}
		}
	}
}

func TestParseInstants(t *testing.T) {
	now := local(10, 14, 9)

	p, _ := Parse("2026-10-14T09:30:00+03:00", now, Instants, nil)
	if !p.Start.Equal(time.Date(2026, 10, 14, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 = %v", p.Start)
	}

	if p, _ := Parse("2026-10-10", now, Instants, nil); !p.Start.Equal(local(10, 10, 0)) {
		t.Errorf("date = %v, want local midnight", p.Start)
	}
}

// Only last-workday reads the working days.
func TestParseReadsWorkdaysOnlyForLastWorkday(t *testing.T) {
	read := 0
	count := func() (Workdays, error) { read++; return DefaultWorkdays(), nil }

	for _, arg := range []string{"today", "last-week", "2026-10-01"} {
		if _, err := Parse(arg, local(10, 14, 9), Windows, count); err != nil {
			t.Fatal(err)
		}
	}

	if read != 0 {
		t.Errorf("read the working days %d times for windows that do not need them", read)
	}
}

// 2026-10-12 is a Monday; last week starts on the 5th whatever today is.
func TestLastWeekStart(t *testing.T) {
	for _, now := range []time.Time{local(10, 12, 9), local(10, 14, 9), local(10, 18, 9)} {
		if got := LastWeekStart(now); !got.Equal(local(10, 5, 0)) {
			t.Errorf("LastWeekStart(%v) = %v, want Monday the 5th", now, got)
		}
	}
}

func TestLastWorkday(t *testing.T) {
	sunThu := Workdays{true, true, true, true, true, false, false}

	for _, c := range []struct {
		now  time.Time
		w    Workdays
		want time.Time
	}{
		{local(10, 12, 9), DefaultWorkdays(), local(10, 9, 0)},
		{local(10, 14, 9), DefaultWorkdays(), local(10, 13, 0)},
		{local(10, 12, 9), sunThu, local(10, 11, 0)},
	} {
		if got := LastWorkday(c.now, c.w); !got.Equal(c.want) {
			t.Errorf("LastWorkday(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}

func TestLastActiveWorkday(t *testing.T) {
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
		if got := LastActiveWorkday(monday, c.workdays, c.activity); !got.Equal(c.want) {
			t.Errorf("%s: LastActiveWorkday = %v, want %v", c.name, got, c.want)
		}
	}
}
