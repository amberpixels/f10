package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
)

func TestAfterIn(t *testing.T) {
	cases := map[string]string{
		"Body.\n\nAfter: GH-12\n":   "GH-12",
		"After:#12":                 "#12",
		"After: GH-12\r\n":          "GH-12",
		"Run after: GH-12":          "",
		"  After: GH-12":            "",
		"After: GH-12 and GH-13\n":  "",
		"no line at all, After: x ": "",
		"**After:** GH-12":          "GH-12",
		"`After:` GH-12":            "GH-12",
		"After: **GH-12**":          "GH-12",
		"After: `GH-12`":            "GH-12",
		"- After: GH-12":            "",
	}

	for body, want := range cases {
		if got := hostops.AfterIn(body); got != want {
			t.Errorf("afterIn(%q) = %q, want %q", body, got, want)
		}
	}
}
