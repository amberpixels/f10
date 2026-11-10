package facts

import (
	"strings"
	"testing"
)

func TestReviewValidate(t *testing.T) {
	t.Parallel()

	ok := Review{
		Reviewer: "claude[bot]", ReviewerKind: ReviewerBot, Arrives: ArrivesComment, Trigger: TriggerAutomatic,
		Done: DonePosted, Handled: HandledReaction, HandledText: "eyes",
	}

	if err := ok.Validate(); err != nil {
		t.Fatalf("a full contract: %v", err)
	}

	for _, tc := range []struct {
		name string
		edit func(*Review)
		want string
	}{
		{"none", func(r *Review) { *r = Review{} }, "declares no remote review"},
		{"missing", func(r *Review) { r.Arrives = "" }, "Arrives as line(s) could not be read"},
		{"thread off inline", func(r *Review) { r.Handled = HandledThread }, "does not arrive inline"},
		{"settled is handled", func(r *Review) { r.Settled, r.SettledText = HandledReaction, "eyes" }, "settled mark is the handled mark"},
	} {
		r := ok
		tc.edit(&r)

		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}

	r := ok
	r.Settled, r.SettledText = HandledReaction, "rocket"

	if err := r.Validate(); err != nil || r.SettledMark() != (Mark{HandledReaction, "rocket"}) {
		t.Errorf("a distinct settled reaction: %v, %+v", err, r.SettledMark())
	}
}
