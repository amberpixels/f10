package worktree_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

func TestSiblingPathAndAgentName(t *testing.T) {
	if got, want := worktree.SiblingPath("/code/repo", "GH-1/slug"), "/code/repo.GH-1-slug"; got != want {
		t.Errorf("siblingPath = %q, want %q", got, want)
	}

	if got, want := agentctl.BareAgentName("GH-1", ""), "gh-1"; got != want {
		t.Errorf("bareAgentName = %q, want %q", got, want)
	}

	if got, want := agentctl.BareAgentName("GH-1", "Attempt2"), "gh-1-attempt2"; got != want {
		t.Errorf("bareAgentName = %q, want %q", got, want)
	}

	// the same task in two checkouts gets two names, each in herdr's shape
	valid := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	one, other := agentctl.AgentName("GH-1", "", "/code/repo.GH-1"), agentctl.AgentName("GH-1", "", "/code/other.GH-1")

	if one == other || !strings.HasPrefix(one, "gh-1-") || !valid.MatchString(one) {
		t.Errorf("agentName = %q and %q, want two distinct gh-1-<hash> names", one, other)
	}

	if long := agentctl.AgentName("ABCDEFGH-123456", "a-very-long-attempt-suffix", "/p"); !valid.MatchString(long) {
		t.Errorf("agentName = %q (%d chars), want at most 32", long, len(long))
	}
}
