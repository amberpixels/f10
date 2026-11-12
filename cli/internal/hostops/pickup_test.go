package hostops_test

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestPickupOf(t *testing.T) {
	p := forge.PR{URL: testkit.PickGH.URL, Head: testkit.PickGH.Branch}
	if got := hostops.NewHost("gh", "").PickupOf("30", p); *got != *testkit.PickGH {
		t.Errorf("pickupOf on gh = %+v", got)
	}

	gl := hostops.NewHost("glab", "").PickupOf("7", forge.PR{URL: "https://gl/mr/7", Head: "b"})
	if gl.View != "f10 pr view --comments 7" || gl.Diff != "f10 pr diff 7" || gl.Branch != "b" {
		t.Errorf("pickupOf on glab = %+v", gl)
	}

	if got := hostops.NewHost("gh", "").PickupOf("30", mergedPull()); *got != *testkit.PickMerged {
		t.Errorf("pickupOf on a merged PR = %+v, want %+v", got, testkit.PickMerged)
	}

	closed := p
	closed.Status, closed.HeadSHA = forge.Closed, "def4567"
	closed.ClosedAt = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	got := hostops.NewHost("gh", "").PickupOf("30", closed)
	if got.SHA != "def4567" || got.HeadRef != "refs/pull/30/head" ||
		got.State != "closed without a merge on 2026-10-13" {
		t.Errorf("pickupOf on a closed PR = %+v", got)
	}

	// glab names nobody for a squash it did itself, and keeps its MR heads under its own ref
	glMerged := forge.PR{URL: "https://gl/mr/7", Head: "b", Status: forge.Merged, Landed: "aaa"}
	got = hostops.NewHost("glab", "").PickupOf("7", glMerged)
	if got.SHA != "aaa" || got.State != "merged" || got.HeadRef != "" {
		t.Errorf("pickupOf on a merged MR = %+v", got)
	}

	glClosed := forge.PR{URL: "https://gl/mr/7", Head: "b", Status: forge.Closed, HeadSHA: "bbb"}
	got = hostops.NewHost("glab", "").PickupOf("7", glClosed)
	if got.SHA != "bbb" || got.HeadRef != "refs/merge-requests/7/head" {
		t.Errorf("pickupOf on a closed MR = %+v", got)
	}
}

// mergedPull is pickGH's PR merged by alice, as gh answers for it.
func mergedPull() forge.PR {
	p := forge.PR{URL: testkit.PickGH.URL, Head: testkit.PickGH.Branch, Status: forge.Merged, HeadSHA: "def4567"}
	p.Landed = "abc1234abc"
	p.MergedBy = "alice"
	p.MergedAt = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)

	return p
}
