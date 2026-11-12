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

// Only the driver's exit 3 reaches the host: any other driver failure is
// the answer, and the host is never asked.
func TestFallbackOnlyOnUnsupported(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.DeclaredTracker(t, facts.TrackerGitHub)
	testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())

	f.Fail("driver list", shell.Result{Code: 1, Stderr: "tracker down"})

	if _, err := stack.TaskRows(
		t.Context(),
		tg,
		hostops.IssuePage,
	); err == nil ||
		!strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's failure", err)
	}

	if f.Called("gh issue list") {
		t.Error("a failing driver fell back to host issues")
	}

	f.Fail("driver search x", shell.Result{Code: trackerdriver.ExitUnsupported})
	f.Script("gh issue list --search x --json number,title,state,url,labels,body,assignees,createdAt --limit 30", `[]`)

	if _, err := taskcmd.SearchRows(t.Context(), tg, "x"); err != nil || !f.Called("gh issue list") {
		t.Errorf("err = %v; want the declined search on host issues", err)
	}
}
