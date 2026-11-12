package testkit

import (
	"testing"
)

const HandoffBody = "## Config\n- set `LOCALE_DEFAULT` before the deploy"

func WriteHandoffFile(t *testing.T, path, head string) {
	t.Helper()

	WriteFile(t, path, "---\ntask: GH-1\nhead: "+head+"\nwritten: 2026-11-04T10:15:00Z\n---\n\n"+HandoffBody+"\n")
}
