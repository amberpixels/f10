package runplan_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestResolveShipFlow(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{
		"default": flow.Of([]string{"plan", "implement", "review:local", "pr"}),
		"local":   flow.Of([]string{"plan", "implement", "commit"}),
		"direct":  flow.Of([]string{"implement", "push"}),
	}}

	for name, want := range map[string]runplan.ShipFlow{
		"":       {},
		"Local":  {Name: "local"},
		"direct": {Name: "direct", Planless: true},
	} {
		if got, err := testkit.PacedFlowIn(eff, nil, "", name); err != nil || got != want {
			t.Errorf("pacedFlow(%q) = %+v, %v; want %+v", name, got, err, want)
		}
	}

	// an unknown name fails with the list f10 flow prints
	_, err := testkit.PacedFlowIn(eff, nil, "", "fast")
	if err == nil || !strings.Contains(err.Error(), "declares no such flow") ||
		!strings.Contains(err.Error(), "implement push") || !strings.Contains(err.Error(), "--flow direct") {
		t.Errorf("unknown flow = %v", err)
	}
}
