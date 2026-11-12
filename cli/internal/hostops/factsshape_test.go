package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Only the host's own issues fall back to the host; a declared Notion or
// Jira tracker reads through its driver alone.
func TestHostIssuesByKind(t *testing.T) {
	for kind, want := range map[facts.TrackerKind]bool{
		facts.TrackerGitHub: true,
		facts.TrackerGitLab: true,
		facts.TrackerNotion: false,
		facts.TrackerJira:   false,
	} {
		if got := testkit.DeclaredTracker(t, kind).HostIssues(); got != want {
			t.Errorf("declared %s: hostIssues = %v, want %v", kind, got, want)
		}
	}

	// a kind only the CLI implies is no declaration, so the host answers
	detected := testkit.ChildTarget(t, "gh")
	detected.Effective.TrackerKind = facts.TrackerJira
	detected.Effective.Facts = nil

	if !detected.HostIssues() || detected.TrackerDeclared() {
		t.Error("an undeclared kind should fall back to host issues")
	}

	none := testkit.ChildTarget(t, "gh")
	none.Effective.TrackerKind = ""

	if !none.HostIssues() {
		t.Error("no kind should fall back to host issues")
	}
}
