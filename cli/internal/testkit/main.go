package testkit

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testenv"
)

// Storage is the storage root of a fixture that names none. The fakes drop
// the agent grants it adds, so a test that does not care about storage
// scripts the agent start without them.
var Storage storage.Root

// Main runs the tests in testenv's environment, with the drive watcher a
// no-op so a start or a drive run hands nothing to a real daemon.
func Main(m *testing.M) {
	cleanup := testenv.Setup()
	Storage = storage.Root(filepath.Join(testenv.Root, "storage"))
	drivectl.WatchProject = func(context.Context, io.Writer, *target.Target) {}

	code := m.Run()

	cleanup()
	os.Exit(code)
}
