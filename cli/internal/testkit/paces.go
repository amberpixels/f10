package testkit

import (
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/runplan"
)

// PacedFlowIn resolves on a literal Effective, which Build would have given
// a pace set; nil is the built-ins.
func PacedFlowIn(eff *facts.Effective, set *facts.PaceSet, pace, name string) (runplan.ShipFlow, error) {
	if set == nil {
		set = facts.NewPaceSet()
		set.Remote = eff.Review.Remote()
	}

	eff.PaceSet = set

	return runplan.PacedFlow(eff, pace, name)
}
