package gitx

import "testing"

func TestShortSHA(t *testing.T) {
	if got := ShortSHA("604bcbb6c505abb6"); got != "604bcbb" {
		t.Errorf("ShortSHA = %q", got)
	}

	if got := ShortSHA("abc"); got != "abc" {
		t.Errorf("ShortSHA short = %q", got)
	}
}

func TestSameCommit(t *testing.T) {
	full := "604bcbb6c505abb6eabf6d7eec618b567f7335f0"

	for _, c := range []struct {
		a, b string
		want bool
	}{
		{full, full, true},
		{"604bcbb", full, true},
		{full, "604BCBB6", true},
		{"604bcb", full, false},
		{"604bcbc", full, false},
	} {
		if got := SameCommit(c.a, c.b); got != c.want {
			t.Errorf("SameCommit(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
