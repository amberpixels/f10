package launch_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
)

func TestDriven(t *testing.T) {
	cases := []struct{ text, want string }{
		{"/f10:ship GH-1", "/f10:ship GH-1 --driven"},
		{"/f10:plan GH-1 && /f10:ship GH-1 && defaults", "/f10:plan GH-1 && /f10:ship GH-1 && defaults --driven"},
		{"/f10:ship GH-1 --driven", "/f10:ship GH-1 --driven"},
		{"/f10:ship GH-1 --driven && go", "/f10:ship GH-1 --driven && go"},
		{"1. no 2. client", "1. no 2. client"},
		{"rebase first, then /f10:ship GH-1", "rebase first, then /f10:ship GH-1"},
	}

	for _, c := range cases {
		if got := launch.Driven(c.text); got != c.want {
			t.Errorf("driven(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
