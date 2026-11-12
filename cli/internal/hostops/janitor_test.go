package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The delete escapes the branch: GitHub keeps its slashes as segments,
// GitLab takes the whole name as one.
func TestDeleteRemoteBranchEscapesTheName(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/a%23b", "")
	f.Script("glab api -X DELETE projects/:fullpath/repository/branches/GH-1%2Fa%23b", "")

	for _, name := range []string{"gh", "glab"} {
		if err := hostops.NewHost(name, "/code/repo").DeleteBranch(t.Context(), "GH-1/a#b"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
