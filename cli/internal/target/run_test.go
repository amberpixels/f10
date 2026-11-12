package target_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/target"
)

// A problem in the user's file names that file, so only the project's own
// errors take the project.md label, once per problem: a multi-line YAML
// error's continuation lines stay with their file.
func TestLabeledErrors(t *testing.T) {
	got := target.LabeledErrors([]facts.Problem{
		{
			Error:   true,
			File:    "/home/me/.f10/paces.md",
			Message: "/home/me/.f10/paces.md: yaml: line 3:\n  field x not found",
		},
		{Error: true, Message: "agents.x: no flow has an x entry"},
	})
	want := "/home/me/.f10/paces.md: yaml: line 3:\n  field x not found\nproject.md: agents.x: no flow has an x entry"

	if got != want {
		t.Errorf("labeled = %q, want %q", got, want)
	}
}
