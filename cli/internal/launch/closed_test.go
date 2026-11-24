package launch_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
)

var closedGH1 = launch.Closed{
	ID:      "GH-1",
	Shipped: []string{"aa GH-2: Part one (#5)"},
	More:    2,
	Release: "v1.2.0",
	Typos:   []string{"GH-7 (Real task)"},
}

// A closed task's discussion says what shipped it and offers the typo, a
// follow-up PR and a follow-up task, before it waits.
func TestClosedPrompt(t *testing.T) {
	got := launch.Prompt(launch.ModeDiscuss, "GH-1", nil, "", nil, "", closedGH1)

	for _, want := range []string{
		"GH-1 is closed.",
		"shipped by: aa GH-2: Part one (#5); and 2 more.",
		"Released in v1.2.0.",
		"a typo for GH-7 (Real task)",
		"follow-up PR from this branch",
		"`/f10:capture`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}

	if !strings.HasSuffix(got, "Then stop and wait.") {
		t.Errorf("prompt does not end waiting:\n%s", got)
	}

	bare := launch.Prompt(launch.ModeDiscuss, "GH-1", nil, "", nil, "", launch.Closed{ID: "GH-1"})
	if !strings.Contains(bare, "look in its comments") || strings.Contains(bare, "a typo for") {
		t.Errorf("a closed task with no record:\n%s", bare)
	}
}

// The drive leaves a closed task out, so a discussion proposes a drive over
// the open ones only, and none when every task is closed.
func TestClosedDiscussionPrompt(t *testing.T) {
	got := launch.DiscussionPrompt(launch.Discussion{
		Tasks: []string{"GH-1", "GH-3"}, Closed: []launch.Closed{closedGH1},
	}, "")
	if !strings.Contains(got, "`/f10:start GH-3 --propose`") || !strings.Contains(got, "GH-1 is closed.") {
		t.Errorf("mixed discussion:\n%s", got)
	}

	all := launch.DiscussionPrompt(launch.Discussion{
		Tasks: []string{"GH-1"}, Parents: []string{"GH-1"}, Closed: []launch.Closed{closedGH1},
	}, "")
	if strings.Contains(all, "--propose") {
		t.Errorf("an all-closed discussion asks for a proposal:\n%s", all)
	}
}

func TestClosedNote(t *testing.T) {
	if got := closedGH1.Note(); got != "GH-1 is closed, shipped by 3 commits (v1.2.0); did you mean GH-7 (Real task)?" {
		t.Errorf("note = %q", got)
	}

	if got := (launch.Closed{ID: "GH-1"}).Note(); got != "GH-1 is closed" {
		t.Errorf("bare note = %q", got)
	}
}
