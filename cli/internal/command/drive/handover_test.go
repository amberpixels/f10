package drive

import (
	"io"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A discussion's worktree: the daemon's own drive process, started there
// as `start --foreground` after the hand-off, runs the drive and never
// meets the hand-off again; a --foreground typed by hand is refused.
func TestHandOverInADiscussionWorktree(t *testing.T) {
	// verify may itself run under a daemon drive, which sets the variable
	t.Setenv(daemon.ResumeEnv, "")

	tg := testkit.ChildTarget(t, "")
	testkit.InitRepo(t, tg.Layout.CheckoutRoot, "GH-1/talk")
	testkit.Git(t, tg.Layout.CheckoutRoot, "commit", "-q", "--allow-empty", "-m", "first")

	if err := branchmeta.Set(t.Context(), tg.Layout.CheckoutRoot, "GH-1/talk", branchmeta.Discuss, "GH-1"); err != nil {
		t.Fatal(err)
	}

	argv := []string{"GH-1", "--foreground"}

	a, err := drivectl.ParseDriveArgs(argv)
	if err != nil {
		t.Fatal(err)
	}

	handed, err := handOver(t.Context(), io.Discard, tg, argv, a)
	if !handed || err == nil || !strings.Contains(err.Error(), "without --foreground") {
		t.Errorf("typed --foreground: handed %v, err %v; want the refusal", handed, err)
	}

	t.Setenv(daemon.ResumeEnv, "1")

	if handed, err := handOver(t.Context(), io.Discard, tg, argv, a); handed || err != nil {
		t.Errorf("the daemon's drive process: handed %v, err %v; want the drive run as usual", handed, err)
	}
}
