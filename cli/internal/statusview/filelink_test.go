package statusview

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The plan row sits in the facts block, which links it once and never wraps.
func TestStatusFallbackLinksPlan(t *testing.T) {
	fb := TaskFallback(
		TaskStatusInput{ID: "GH-1", Root: "/tmp"},
		nil,
		Shipment{Task: "GH-1", Plan: "/tmp/.f10/plans/GH-1.md"},
	)

	testkit.WithTerminal(t, true)

	var out bytes.Buffer
	fb.Render(&out, 30, true)

	spans := testkit.LinkSpan.FindAllStringSubmatch(out.String(), -1)
	if len(spans) != 1 || spans[0][1] != "file:///tmp/.f10/plans/GH-1.md" {
		t.Fatalf("want one link, on the plan row:\n%q", out.String())
	}

	out.Reset()
	fb.Render(&out, 0, false)

	if strings.Contains(out.String(), "\x1b]8;;") {
		t.Errorf("unlinked render carries a link:\n%q", out.String())
	}
}
