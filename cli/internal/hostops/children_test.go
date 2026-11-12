package hostops_test

import (
	"context"
	"errors"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A host without sub-issues answers 404, which is no parent link.
func TestChildrenGitHub404(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")

	f.Fail("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues",
		shell.Result{Code: 1, Stderr: "gh: Not Found (HTTP 404)"})

	_, err := stack.TrackerFor(tg).Children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
	if !errors.Is(err, hostops.ErrNoParentLink) {
		t.Errorf("err = %v, want errNoParentLink", err)
	}
}

// A driver declining the verb on a tracker no host reaches has no parent
// link; on a host tracker the host answers.
func TestChildrenDriverDeclines(t *testing.T) {
	testkit.NewFakes(t)

	tg := testkit.ChildTarget(t, "gh")
	testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())
	tg.Effective = &facts.Effective{
		IDPrefix:    "FT",
		TrackerKind: facts.TrackerNotion,
		Facts:       []facts.Fact{{Key: "tracker.kind", Value: "Notion", Origin: facts.DeclaredFrontmatter("main")}},
	}

	seam.Swap(t, &shell.Capture, func(context.Context, string, string, ...string) (shell.Result, error) {
		return shell.Result{Code: 3}, nil
	})

	if _, err := stack.RawChildrenFor(
		t.Context(),
		tg,
		ref.Ref{ID: "FT-71", Number: "71"},
	); !errors.Is(
		err,
		hostops.ErrNoParentLink,
	) {
		t.Errorf("err = %v, want errNoParentLink", err)
	}
}
