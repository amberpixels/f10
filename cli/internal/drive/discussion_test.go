package drive

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A worktree handed over from a discussion gets its first skill only once
// the discussion's turn ends: attaching to that turn would read it as the
// skill's, and its end as a stop with no report. The mark goes at rest.
func TestFirstSkillWaitsForTheDiscussionTurn(t *testing.T) {
	main := filepath.Join(t.TempDir(), "repo")
	testkit.InitRepo(t, main, "main")
	testkit.Git(t, main, "commit", "-q", "--allow-empty", "-m", "first")
	testkit.Git(t, main, "branch", "GH-2/ship")

	if err := branchmeta.Set(t.Context(), main, "GH-2/ship", branchmeta.Discuss, "GH-1"); err != nil {
		t.Fatal(err)
	}

	in := &DriveInput{Out: io.Discard, Main: main}
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-2"}}}
	openRun(r)
	r.O.Branch = "GH-2/ship"

	in.view().Wss = []agentctl.Workspace{{ID: r.O.Ws.ID, Path: r.O.Path, Status: agentctl.StatusWorking}}

	waiting, err := in.discussionTurn(t.Context(), r)
	if err != nil || !waiting {
		t.Fatalf("working agent: waiting = %v, %v; want the first skill held", waiting, err)
	}

	in.view().Wss[0].Status = agentctl.StatusIdle

	if waiting, err = in.discussionTurn(t.Context(), r); err != nil || waiting {
		t.Fatalf("agent at rest: waiting = %v, %v; want the skill sent", waiting, err)
	}

	if v := branchmeta.Out(t.Context(), main, "GH-2/ship", branchmeta.Discuss); v != "" {
		t.Errorf("discussion mark = %q, want it gone once the agent rested", v)
	}
}
