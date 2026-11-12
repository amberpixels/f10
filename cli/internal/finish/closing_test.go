package finish

import (
	"testing"
)

// What KeepOpen rewrites, CloseAgain rewrites back, in every form pickup reads.
func TestKeepOpenRoundTrip(t *testing.T) {
	for _, body := range []string{
		"Closes #12",
		"fixes: https://github.com/o/r/issues/12",
		"Resolves https://gitlab.com/g/p/-/work_items/12 and more",
	} {
		kept := KeepOpen(body, "12")
		if kept == body || ClosingRE.MatchString(kept) {
			t.Errorf("keepOpen(%q) = %q, still closing", body, kept)
		}

		if again := CloseAgain(kept, "12"); !ClosingRE.MatchString(again) {
			t.Errorf("closeAgain(%q) = %q, not closing", kept, again)
		}
	}

	if !ClosingRE.MatchString(ClosingLine("7")) {
		t.Error("closingLine is not read as closing")
	}
}
