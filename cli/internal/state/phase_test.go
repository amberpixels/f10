package state

import "testing"

func TestRunFurthestAndStoppedPhase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		run      Run
		furthest string
		stopped  string
	}{
		{"untouched", Run{Capture: "pending", Plan: "pending", Ship: "pending"}, "capture", ""},
		{"unset reads as untouched", Run{}, "capture", ""},
		{"planning", Run{Capture: "prior", Plan: "running", Ship: "pending"}, "plan", ""},
		{"plan blocked", Run{Capture: "prior", Plan: "blocked", Ship: "pending"}, "plan", "plan"},
		{"ship partial", Run{Capture: "prior", Plan: "done", Ship: "partial"}, "ship", "ship"},
		{"skipped counts as left", Run{Capture: "prior", Plan: "skipped", Ship: "pending"}, "plan", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.run.Furthest(); got != tc.furthest {
				t.Errorf("Furthest = %q, want %q", got, tc.furthest)
			}

			got, ok := tc.run.StoppedPhase()
			if got != tc.stopped || ok != (tc.stopped != "") {
				t.Errorf("StoppedPhase = %q, %v; want %q", got, ok, tc.stopped)
			}
		})
	}
}
