package facts_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// A command with no paths runs on every diff, the empty one included.
func TestUnscopedCommandAlwaysRuns(t *testing.T) {
	run, left := facts.ScopeVerify(
		[]facts.VerifyDecl{{Run: "just test"}, {Run: "just lint", Paths: []string{"cli/**"}}},
		nil,
	)

	if len(run) != 1 || run[0].Run != "just test" || len(left) != 1 {
		t.Errorf("run = %+v, left = %+v", run, left)
	}
}
