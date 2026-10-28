package ref

import (
	"os"
	"os/exec"
	"testing"
)

func TestDriveFinishRoundTrip(t *testing.T) {
	for _, d := range []DriveFinish{
		{PID: 42, State: FinishQueued},
		{PID: 42, State: FinishQueued, Action: "waiting for GH-1"},
		{PID: 42, State: FinishRunning, Action: "merging #204"},
	} {
		got, ok := ParseDriveFinish(d.Format())
		if !ok || got != d {
			t.Errorf("ParseDriveFinish(%q) = %+v, %v, want %+v", d.Format(), got, ok, d)
		}
	}

	for _, bad := range []string{"", "x", "0 queued", "-3 running merging", " ", "42", "42 merging #9"} {
		if _, ok := ParseDriveFinish(bad); ok {
			t.Errorf("ParseDriveFinish(%q) parsed", bad)
		}
	}
}

func TestLiveDriveFinish(t *testing.T) {
	own := DriveFinish{PID: os.Getpid(), State: FinishRunning, Action: "removing worktree"}
	if got, ok := LiveDriveFinish(own.Format()); !ok || got != own {
		t.Errorf("LiveDriveFinish(own pid) = %+v, %v", got, ok)
	}

	defer func(f func(int) bool) { alive = f }(alive)

	alive = func(int) bool { return false }

	if _, ok := LiveDriveFinish(own.Format()); ok {
		t.Error("a record whose process is gone read live")
	}
}

// A reaped process reads dead through the real probe.
func TestProcessAliveOnAReapedPid(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip(err)
	}

	if processAlive(cmd.Process.Pid) {
		t.Errorf("reaped pid %d reads alive", cmd.Process.Pid)
	}
}
