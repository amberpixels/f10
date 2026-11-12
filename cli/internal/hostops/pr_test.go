package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestPullCommentsGitHub(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh pr view 4 --json comments", `{"comments":[`+
		`{"author":{"login":"ann"},"body":"looks good","createdAt":"2026-10-01T00:00:00Z"}]}`)

	got, err := hostops.NewHost("gh", t.TempDir()).PullComments(t.Context(), 4)
	if err != nil || len(got) != 1 || got[0].Author != "ann" || got[0].Body != "looks good" {
		t.Errorf("PullComments = %+v, %v", got, err)
	}
}
