package launch_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Any member finds the bundle's branch through the recorded link.
func TestLinkedFindsAnyMember(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Meta.Seed("GH-1+GH-2/x", branchmeta.Task, "GH-1")
	f.Meta.Seed("GH-1+GH-2/x", branchmeta.Riders, "GH-2 GH-3")

	for _, id := range []string{"GH-1", "GH-3", "gh-2"} {
		if got := testkit.IndexOf(t).Linked(id); strings.Join(got, ",") != "GH-1+GH-2/x" {
			t.Errorf("linked(%s) = %v, want [GH-1+GH-2/x]", id, got)
		}
	}

	if got := testkit.IndexOf(t).Linked("GH-4"); got != nil {
		t.Errorf("linked(GH-4) = %v, want none", got)
	}
}
