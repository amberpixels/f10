package textx

import (
	"testing"
	"time"
)

func TestCut(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"a longer title", 8, "a longe…"},
		{"表示幅の広い", 7, "表示幅…"},
	} {
		if got := Cut(c.in, c.n); got != c.want {
			t.Errorf("Cut(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestOrDash(t *testing.T) {
	t.Parallel()

	if OrDash("") != "-" || OrDash("x") != "x" {
		t.Errorf("OrDash: %q %q", OrDash(""), OrDash("x"))
	}
}

func TestAge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "0s"},
		{45 * time.Second, "45s"},
		{12 * time.Minute, "12m"},
		{5 * time.Hour, "5h"},
		{75 * time.Hour, "3d"},
	} {
		if got := Age(now, now.Add(-c.d)); got != c.want {
			t.Errorf("Age(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
