package pr_test

import (
	"testing"

	prcmd "github.com/amberpixels/f10/cli/internal/command/pr"
)

func TestPatchFiles(t *testing.T) {
	patch := "diff --git a/x.go b/x.go\nindex 1..2\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/old.md b/new.md\nrename from old.md\n"

	if got := prcmd.PatchFiles(patch); got != "x.go\nnew.md" {
		t.Errorf("patchFiles = %q", got)
	}
}
