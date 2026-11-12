package drive

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestDriveParsesABundle(t *testing.T) {
	a, err := drivectl.ParseDriveArgs(strings.Fields("GH-1+GH-2:opus GH-3"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(a.Tasks, " ") != "GH-1+GH-2 GH-3" || a.Profiles[0] != "opus" {
		t.Errorf("tasks %v profiles %v, want the bundle whole with its profile", a.Tasks, a.Profiles)
	}

	in := &DriveInput{
		Args: drivectl.DriveArgs{Tasks: []string{"GH-1+GH-2", "GH-5"}, Span: true},
	}
	if _, _, err := in.Span(t.Context()); err == nil {
		t.Error("a bundle as a range end was taken")
	}
}

// A task after a rider waits for the bundle the rider ships in.
func TestDriveIndexesEveryMember(t *testing.T) {
	tasks := []DriveTask{{Ref: testkit.Bundle12}, {Ref: ref.Ref{ID: "GH-3"}, Base: "GH-2"}}

	index := ListIndex(tasks)
	if index["GH-2"] != 0 || index["GH-1"] != 0 || index["GH-3"] != 1 {
		t.Errorf("index = %v, want both members at the bundle's place", index)
	}

	order, err := TopoOrder(tasks, index)
	if err != nil || len(order) != 2 || order[0] != 0 {
		t.Errorf("order = %v, %v; want the bundle first", order, err)
	}
}

func TestSkillPromptForABundle(t *testing.T) {
	for skill, want := range map[string]string{
		"plan":   "/f10:plan GH-1+GH-2 --driven",
		"ship":   "/f10:ship GH-1+GH-2 --driven",
		"review": "/f10:review GH-1 --driven",
		"finish": "/f10:finish GH-1 --driven",
	} {
		if got := SkillPrompt(skill, "GH-1+GH-2", nil, runplan.ShipFlow{}, false); got != want {
			t.Errorf("skillPrompt(%s) = %q, want %q", skill, got, want)
		}
	}
}
