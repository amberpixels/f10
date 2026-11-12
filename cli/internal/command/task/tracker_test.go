package task_test

import (
	"strings"
	"testing"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// A driver declining a verb on a non-host tracker is named with that verb,
// never reported as missing.
func TestDeclinedVerbOnNonHostTracker(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.DeclaredTracker(t, facts.TrackerNotion)
	path := testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())

	f.Fail("driver list", shell.Result{Code: trackerdriver.ExitUnsupported})
	f.Fail("driver search index", shell.Result{Code: trackerdriver.ExitUnsupported})

	_, listErr := stack.TaskRows(t.Context(), tg, hostops.IssuePage)
	_, searchErr := taskcmd.SearchRows(t.Context(), tg, "index")

	for verb, err := range map[string]error{"list": listErr, "search": searchErr} {
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "`"+verb+"`") ||
			strings.Contains(err.Error(), "none was found") {
			t.Errorf("%s declined = %v, want the driver path and the verb, not a missing driver", verb, err)
		}
	}

	if f.Called("gh issue list") {
		t.Error("a Notion tracker fell back to host issues")
	}
}
