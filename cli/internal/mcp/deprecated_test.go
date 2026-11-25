package mcp

import (
	"testing"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// A tool's result carries no deprecated field: a model reads what it is
// shown, and an old name beside its replacement says the same thing twice.
func TestDropDeprecatedClearsOldNames(t *testing.T) {
	t.Parallel()

	start := &f10v1.StartResponse{Drive: 3, Batch: 3, Log: "/l"} //nolint:staticcheck // the old name, cleared below
	dropDeprecated(start.ProtoReflect())

	if start.GetDrive() != 0 || start.GetBatch() != 3 || start.GetLog() != "/l" { //nolint:staticcheck // the old name
		t.Errorf("start reply = %v, want the batch alone", start)
	}

	status := &f10v1.GetDaemonStatusResponse{
		Drives: []*f10v1.SupervisedDrive{{Drive: 1}}, //nolint:staticcheck // the old list, cleared below
	}
	dropDeprecated(status.ProtoReflect())

	if len(status.GetDrives()) != 0 { //nolint:staticcheck // the old list
		t.Errorf("daemon status still lists the batches under the old name: %v", status)
	}
}
