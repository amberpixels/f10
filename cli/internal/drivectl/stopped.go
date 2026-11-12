package drivectl

import (
	"github.com/amberpixels/f10/cli/internal/state"
)

func StoppedRun(r *state.Run) bool {
	_, stopped := r.StoppedPhase()

	return stopped
}
