package launch_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestTaskBranch(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsAfter, testkit.Heads("GH-7/thing"))

		got, err := testkit.IndexOf(t).After(t.Context(), "GH-7")
		if err != nil || got != "GH-7/thing" {
			t.Errorf("after = %q, %v", got, err)
		}

		if f.Called("git for-each-ref --format=%(refname) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsAfter, "")
		f.Script(testkit.RefsAfterO, "refs/remotes/origin/GH-7")

		got, err := testkit.IndexOf(t).After(t.Context(), "GH-7")
		if err != nil || got != "origin/GH-7" {
			t.Errorf("after = %q, %v", got, err)
		}
	})

	t.Run("nothing anywhere fails", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsAfter, "")
		f.Script(testkit.RefsAfterO, "")

		if _, err := testkit.IndexOf(t).After(t.Context(), "GH-7"); err == nil {
			t.Error("after found a branch where none exists")
		}
	})

	t.Run("a branch carrying the id past a prefix is found before origin", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsAfter, "")
		f.Script(testkit.AllHeads, testkit.Heads("main", "feat/GH-7-base"))

		got, err := testkit.IndexOf(t).After(t.Context(), "GH-7")
		if err != nil || got != "feat/GH-7-base" {
			t.Errorf("after = %q, %v", got, err)
		}

		if f.Called("git for-each-ref --format=%(refname) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("several is an error naming them", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsAfter, testkit.Heads("GH-7", "GH-7-attempt2"))

		_, err := testkit.IndexOf(t).After(t.Context(), "GH-7")
		if err == nil || !strings.Contains(err.Error(), "GH-7-attempt2") {
			t.Errorf("err = %v, want the candidates named", err)
		}
	})
}

// The discuss prompt is plain text the user follows up on in the agent's
// pane, so it never carries the driven marker.
func TestDiscussPrompt(t *testing.T) {
	got := launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", nil, "", nil, "")
	want := "Run `f10 task read GH-1` and summarise what it asks in a few lines. Then stop and wait."

	if got != want {
		t.Errorf("prompt(discuss) = %q, want %q", got, want)
	}

	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: "/plans/GH-7.md", Planned: true}

	got = launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", dep, "", nil, "")
	if !strings.Contains(got, "For context: GH-1 depends on GH-7, whose plan is at /plans/GH-7.md.") {
		t.Errorf("prompt(discuss) with a dependency = %q, want the dependency named", got)
	}

	if strings.Contains(got, launch.DrivenMarker) {
		t.Errorf("prompt(discuss) = %q, want no driven marker", got)
	}

	got = launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", nil, "/plans/GH-1.md", nil, "")
	if !strings.Contains(got, "A plan for it is saved at /plans/GH-1.md; read it too.") {
		t.Errorf("prompt(discuss) with a plan = %q, want the plan named", got)
	}

	if strings.Contains(got, launch.DrivenMarker) {
		t.Errorf("prompt(discuss) with a plan = %q, want no driven marker", got)
	}
}
