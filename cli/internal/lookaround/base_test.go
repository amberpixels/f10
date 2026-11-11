package lookaround

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/trace"
)

// A request read without its state block still gives the block's base,
// parsed from its body, so status and the board settle a base alike.
func TestBaseOfReadsTheBlockFromABody(t *testing.T) {
	t.Parallel()

	body, err := trace.PutState("the description", trace.State{V: 1, Task: "GH-2", Step: "pr", After: "GH-1"})
	if err != nil {
		t.Fatal(err)
	}

	pr := &PR{}
	pr.Body = body

	for _, tc := range []struct {
		branch     string
		pr         *PR
		base, from string
	}{
		{"GH-9", pr, "GH-9", FromLocal},
		{"", pr, "GH-1", FromTrace},
		{"", nil, "GH-7", FromGuessed},
	} {
		if base, from := BaseOf(tc.branch, tc.pr, "GH-7"); base != tc.base || from != tc.from {
			t.Errorf("BaseOf(%q) = %s from %s, want %s from %s", tc.branch, base, from, tc.base, tc.from)
		}
	}
}
