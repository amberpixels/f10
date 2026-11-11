package main

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/finish"
)

// What KeepOpen rewrites, CloseAgain rewrites back, in every form pickup reads.
func TestKeepOpenRoundTrip(t *testing.T) {
	for _, body := range []string{
		"Closes #12",
		"fixes: https://github.com/o/r/issues/12",
		"Resolves https://gitlab.com/g/p/-/work_items/12 and more",
	} {
		kept := finish.KeepOpen(body, "12")
		if kept == body || finish.ClosingRE.MatchString(kept) {
			t.Errorf("keepOpen(%q) = %q, still closing", body, kept)
		}

		if again := finish.CloseAgain(kept, "12"); !finish.ClosingRE.MatchString(again) {
			t.Errorf("closeAgain(%q) = %q, not closing", kept, again)
		}
	}

	if !finish.ClosingRE.MatchString(finish.ClosingLine("7")) {
		t.Error("closingLine is not read as closing")
	}
}
