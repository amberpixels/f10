package drivectl_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
)

// A --plan record written before the opened mark: a base that saved its plan
// and ended had opened, so its join no longer waits on it.
func TestJoinWaitsReadsAnEndedBaseAsOpened(t *testing.T) {
	tasks := []drivestore.Task{
		{ID: "A-1", Phase: drivestore.PhaseEnded, Outcome: &drivestore.Outcome{Row: "done: plan"}},
		{ID: "A-3", Join: []string{"A-1"}, Phase: drivestore.PhasePending},
	}

	if got := drivectl.JoinWaits(tasks[1], tasks, func(string) bool { return true }); len(got) != 0 {
		t.Errorf("waits = %v, want none: the base ended with its plan saved", got)
	}
}
