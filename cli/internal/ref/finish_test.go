package ref

import (
	"os"
	"testing"
)

func TestDriveFinishRoundTrip(t *testing.T) {
	for _, d := range []DriveFinish{{PID: 42}, {PID: 42, Action: "merging #204"}} {
		got, ok := ParseDriveFinish(d.Format())
		if !ok || got != d {
			t.Errorf("ParseDriveFinish(%q) = %+v, %v, want %+v", d.Format(), got, ok, d)
		}
	}

	for _, bad := range []string{"", "x", "0", "-3 merging", " "} {
		if _, ok := ParseDriveFinish(bad); ok {
			t.Errorf("ParseDriveFinish(%q) parsed", bad)
		}
	}
}

func TestLiveDriveFinish(t *testing.T) {
	own := DriveFinish{PID: os.Getpid(), Action: "removing worktree"}
	if got, ok := LiveDriveFinish(own.Format()); !ok || got != own {
		t.Errorf("LiveDriveFinish(own pid) = %+v, %v", got, ok)
	}

	defer func(f func(int) bool) { alive = f }(alive)

	alive = func(int) bool { return false }

	if _, ok := LiveDriveFinish(own.Format()); ok {
		t.Error("a record whose process is gone read live")
	}
}

func TestDriveFinishWaiting(t *testing.T) {
	if !(DriveFinish{PID: 1, Action: "waiting for GH-1"}).Waiting() {
		t.Error("a base wait is not waiting")
	}

	if (DriveFinish{PID: 1, Action: "merging #2"}).Waiting() {
		t.Error("a merge is waiting")
	}
}
