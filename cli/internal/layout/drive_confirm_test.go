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

	var point5 string
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(l, "5. **Confirm once**") {
			point5 = l
		}
	}

	if !strings.Contains(point5, "each option carrying the proposal as its `preview`") {
		t.Errorf("Plan point 5 asks `Drive this command?` without the proposal as its preview:\n%s", point5)
	}
}
