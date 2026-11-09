package main

import "testing"

// What keepOpen rewrites, closeAgain rewrites back, in every form pickup reads.
func TestKeepOpenRoundTrip(t *testing.T) {
	for _, body := range []string{
		"Closes #12",
		"fixes: https://github.com/o/r/issues/12",
		"Resolves https://gitlab.com/g/p/-/work_items/12 and more",
	} {
		kept := keepOpen(body, "12")
		if kept == body || closingRE.MatchString(kept) {
			t.Errorf("keepOpen(%q) = %q, still closing", body, kept)
		}

		if again := closeAgain(kept, "12"); !closingRE.MatchString(again) {
			t.Errorf("closeAgain(%q) = %q, not closing", kept, again)
		}
	}

	if !closingRE.MatchString(closingLine("7")) {
		t.Error("closingLine is not read as closing")
	}
}
