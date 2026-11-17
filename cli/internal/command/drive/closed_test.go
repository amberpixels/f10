package drive

import (
	"slices"
	"testing"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/drivectl"
)

// A flow start of closed tasks becomes a discussion of the same ids with its
// base and agent kept, and names the flow's flags it drops.
func TestClosedStartAsDiscussion(t *testing.T) {
	a, err := drivectl.ParseDriveArgs([]string{"GH-1", "--plan", "--pace", "fast", "--after", "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	req := discussionRequest(a)
	if req.GetMode() != f10v1.StartMode_START_MODE_DISCUSS || req.GetToken() != "GH-1" ||
		len(req.GetTasks()) != 0 || req.GetAfter() != "GH-7" ||
		req.GetFlow() != "" || req.GetPace() != "" {
		t.Errorf("request = %v", req)
	}

	if got := droppedDriveFlags(a); !slices.Equal(got, []string{"--plan", "--pace fast"}) {
		t.Errorf("dropped = %q", got)
	}
}
