package start

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// handoverRepo is a main checkout with a discussion's branch GH-1/talk
// checked out in a worktree beside it, its agent and workspace live.
func handoverRepo(t *testing.T) (string, string, *testkit.MemAgents) {
	t.Helper()

	main := filepath.Join(t.TempDir(), "repo")
	testkit.InitRepo(t, main, "main")
	testkit.Git(t, main, "commit", "-q", "--allow-empty", "-m", "first")

	path := filepath.Join(filepath.Dir(main), "repo.GH-1")
	testkit.Git(t, main, "worktree", "add", "-q", "-b", "GH-1/talk", path)

	if err := branchmeta.Set(t.Context(), main, "GH-1/talk", branchmeta.Discuss, "GH-1 GH-2 GH-3"); err != nil {
		t.Fatal(err)
	}

	if err := branchmeta.Set(t.Context(), main, "GH-1/talk", branchmeta.Pace, "fast"); err != nil {
		t.Fatal(err)
	}

	m := testkit.UseListingAgents(t)
	agent := agentctl.AgentName("GH-1", "", path)
	m.Spaces = []agentctl.Workspace{{ID: "ws1", Label: "GH-1/talk", Path: path}}
	m.Started = []string{agent}
	m.Homes = map[string]string{agent: "ws1"}

	return main, path, m
}

// The group's lead takes the branch, the agent and the workspace by its own
// names, the branch's facts moving with it and its riders recorded.
func TestHandOverRenamesForTheLead(t *testing.T) {
	main, path, m := handoverRepo(t)

	h := HandOver{
		Main: main, Path: path, Branch: "GH-1/talk", Name: "GH-2/ship",
		Task: ref.Ref{ID: "GH-2", Bundle: []string{"GH-2", "GH-3"}},
	}

	if _, err := h.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	rec, err := branchmeta.Load(t.Context(), main, "GH-2/ship")
	if err != nil {
		t.Fatal(err)
	}

	if rec.Task != "GH-2" || !slices.Equal(rec.Riders, []string{"GH-3"}) || rec.Pace != "fast" ||
		len(rec.Discuss) == 0 {
		t.Errorf("record = %+v, want lead GH-2, rider GH-3, pace and the discussion kept", rec)
	}

	if got := strings.TrimSpace(testkit.Git(t, path, "branch", "--show-current")); got != "GH-2/ship" {
		t.Errorf("worktree branch = %q, want GH-2/ship", got)
	}

	if want := agentctl.AgentName("GH-2", "", path); !slices.Equal(m.Started, []string{want}) {
		t.Errorf("agents = %q, want %q", m.Started, want)
	}

	if m.Spaces[0].Label != "GH-2/ship" {
		t.Errorf("workspace label = %q, want GH-2/ship", m.Spaces[0].Label)
	}
}

// The task the discussion opened for keeps its branch and agent, alone.
func TestHandOverToTheSameTaskRenamesNothing(t *testing.T) {
	main, path, m := handoverRepo(t)
	before := slices.Clone(m.Started)

	h := HandOver{Main: main, Path: path, Branch: "GH-1/talk", Name: "GH-1/talk", Task: ref.Ref{ID: "GH-1"}}

	if _, err := h.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(m.Started, before) || m.Spaces[0].Label != "GH-1/talk" {
		t.Errorf("agents %q, workspace %q: want both unchanged", m.Started, m.Spaces[0].Label)
	}
}

// A branch the rename cannot take puts the agent and the workspace back.
func TestHandOverUndoesTheRenamesOnAGitFailure(t *testing.T) {
	main, path, m := handoverRepo(t)
	testkit.Git(t, main, "branch", "GH-2/ship")

	before := slices.Clone(m.Started)

	h := HandOver{Main: main, Path: path, Branch: "GH-1/talk", Name: "GH-2/ship", Task: ref.Ref{ID: "GH-2"}}

	if _, err := h.Run(t.Context()); err == nil {
		t.Fatal("want the rename onto an existing branch refused")
	}

	if !slices.Equal(m.Started, before) || m.Spaces[0].Label != "GH-1/talk" {
		t.Errorf("agents %q, workspace %q: want both put back", m.Started, m.Spaces[0].Label)
	}
}
