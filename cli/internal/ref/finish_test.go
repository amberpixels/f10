package ref

import "testing"

func TestDriveFinishRoundTrip(t *testing.T) {
	for _, d := range []DriveFinish{
		{State: FinishQueued},
		{State: FinishQueued, Action: "waiting for GH-1"},
		{State: FinishRunning, Action: "merging #204"},
	} {
		got, ok := ParseDriveFinish(d.Format())
		if !ok || got != d {
			t.Errorf("ParseDriveFinish(%q) = %+v, %v, want %+v", d.Format(), got, ok, d)
		}
	}

	if got := (DriveFinish{State: FinishRunning, Action: "merging #204"}).Format(); got != "running: merging #204" {
		t.Errorf("Format = %q", got)
	}

	for _, bad := range []string{"", "x", "42", "42 merging #9", "merging #9", " "} {
		if _, ok := ParseDriveFinish(bad); ok {
			t.Errorf("ParseDriveFinish(%q) parsed", bad)
		}
	}
}

// A value an older drive wrote, its pid first, still parses.
func TestParseDriveFinishSkipsAnOldPid(t *testing.T) {
	got, ok := ParseDriveFinish("4242 running merging #204")
	if want := (DriveFinish{State: FinishRunning, Action: "merging #204"}); !ok || got != want {
		t.Errorf("ParseDriveFinish = %+v, %v, want %+v", got, ok, want)
	}
}
