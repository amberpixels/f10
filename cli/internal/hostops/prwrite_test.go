package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The url comes from reading the new request back, not from create's output.
func TestCreatePullReadsTheURLBack(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh pr create --head feat --base base --title T --body B --label x --draft", "noise\n")
	f.Script(
		"gh pr view feat --json number,title,state,url,body,author,baseRefName,headRefName,headRefOid,isCrossRepository,mergeStateStatus,mergeCommit,mergedAt,mergedBy,closedAt",
		`{"state":"OPEN","url":"https://gh/o/r/pull/9","number":9}`,
	)

	got, err := hostops.NewHost("gh", t.TempDir()).OpenPull(t.Context(),
		forge.NewPull{Head: "feat", Base: "base", Title: "T", Body: "B", Labels: []string{"x"}, Draft: true})
	if err != nil || got != "https://gh/o/r/pull/9" {
		t.Errorf("createPull = %q, %v", got, err)
	}
}
