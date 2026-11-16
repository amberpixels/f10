package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The confirm question can cover the proposal printed before it, so the
// question must carry the proposal itself as its preview.
func TestDriveConfirmCarriesProposal(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", "drive", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	// The point runs from its numbered line through its indented lines, one sentence each.
	var point5 strings.Builder

	in := false
	for l := range strings.SplitSeq(string(data), "\n") {
		switch {
		case strings.HasPrefix(l, "5. **Confirm once**"):
			in = true
		case in && !strings.HasPrefix(l, "   "):
			in = false
		}

		if in {
			point5.WriteString(l + "\n")
		}
	}

	if !strings.Contains(point5.String(), "each option carrying the proposal as its `preview`") {
		t.Errorf("Plan point 5 asks `Drive this command?` without the proposal as its preview:\n%s", point5.String())
	}
}
