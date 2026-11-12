package daemon_test

import (
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/daemon"
)

func TestDriveEnv(t *testing.T) {
	got := daemon.DriveEnv([]string{
		"PATH=/bin", "HOME=/h", "GH_TOKEN=x", "HERDR_ENV=1", "AWS_PROFILE=p", "SECRET=s", "LC_ALL=C", "ZIG=z",
	}, []string{"ZIG"})

	want := []string{"PATH=/bin", "HOME=/h", "GH_TOKEN=x", "HERDR_ENV=1", "LC_ALL=C", "ZIG=z"}
	if !slices.Equal(got, want) {
		t.Errorf("driveEnv = %v, want %v", got, want)
	}
}
