package drivectl_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

func TestDriveTakesPace(t *testing.T) {
	a, err := drivectl.ParseDriveArgs([]string{"GH-1", "--pace", "Fast"})
	if err != nil || a.Pace != "fast" {
		t.Errorf("--pace = %+v, %v", a, err)
	}

	if _, err := drivectl.ParseDriveArgs([]string{"GH-1", "--pace", "fast", "--pace=slow"}); err == nil {
		t.Error("two paces were taken")
	}
}
