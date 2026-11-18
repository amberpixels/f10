package statusview

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A drive's queued task keeps its row though an earlier PR archived its
// plan, and its dependent still waits on it; the drive's own record, not
// the archive, drops a task it finished.
func TestLaunchKidsKeepAQueuedTaskWithAnArchivedPlan(t *testing.T) {
	f := testkit.NewFakes(t)
	stateDir := t.TempDir()
	t.Setenv("F10_STATE_DIR", stateDir)
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	tg := testkit.ChildTarget(t, "gh")
	root := tg.Layout.StorageRoot

	testkit.WritePlan(t, filepath.Join(root.Archive(), "FT-4.md"))
	testkit.WritePlan(t, filepath.Join(root.Archive(), "FT-6.md"))
	testkit.ScriptNoBranch(f, "FT-4", "FT-5", "FT-6")

	for _, n := range []string{"4", "5"} {
		f.Script("gh issue view "+n+" --json number,title,state,labels,createdAt,url,body,assignees",
			`{"number":`+n+`,"title":"t`+n+`","state":"OPEN","url":"https://x/`+n+`","body":"","labels":[]}`)
	}

	done := drivestore.Task{ID: "FT-6"}
	drivectl.SetFinished(&done)

	rec := &drivestore.Record{
		Number:  1,
		Created: time.Now(),
		Args:    []string{"FT-4", "FT-5", "FT-6"},
		Tasks: []drivestore.Task{
			{ID: "FT-4", Queued: true, Phase: drivestore.PhasePending},
			{ID: "FT-5", After: "FT-4", Phase: drivestore.PhasePending},
			done,
		},
	}
	if err := drivestore.Create(root.Path(), rec); err != nil {
		t.Fatal(err)
	}

	if err := state.AppendLaunch(stateDir, "s1", state.Launch{Kind: state.LaunchDrive, Value: "1"}); err != nil {
		t.Fatal(err)
	}

	rows := launchKids(t.Context(), io.Discard, tg, "s1")

	got := map[string]string{}
	for _, r := range rows {
		got[r.ID] = r.After
	}

	if _, ok := got["FT-4"]; !ok {
		t.Errorf("rows = %+v, want the queued FT-4 kept", rows)
	}

	if got["FT-5"] != "FT-4" {
		t.Errorf("FT-5 waits on %q, want FT-4", got["FT-5"])
	}

	if _, ok := got["FT-6"]; ok {
		t.Errorf("rows = %+v, want FT-6 dropped: its drive finished it", rows)
	}
}
