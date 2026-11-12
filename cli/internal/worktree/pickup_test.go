package worktree_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/worktree"
)

func TestPullToken(t *testing.T) {
	cases := []struct {
		token, number string
		probe         bool
	}{
		{token: "https://github.com/o/r/pull/12", number: "12"},
		{token: "https://github.com/o/r/pull/12/files", number: "12"},
		{token: "https://gitlab.com/g/p/-/merge_requests/7", number: "7"},
		{token: "!7", number: "7"},
		{token: "#12", number: "12", probe: true},
		{token: "https://github.com/o/r/issues/12"},
		{token: "GH-12"},
		{token: "12"},
		{token: "#"},
		{token: "#12x"},
	}

	for _, c := range cases {
		number, probe := worktree.PullToken(c.token)
		if number != c.number || probe != c.probe {
			t.Errorf("pullToken(%q) = %q, %v; want %q, %v", c.token, number, probe, c.number, c.probe)
		}
	}
}
