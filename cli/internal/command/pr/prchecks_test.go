package pr

import (
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func factsText(rows []render.Fact) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.Label + "=" + r.Value + "\n")
	}

	return b.String()
}

// Each failed job is a row of its own with its log url, the counted set
// beside the state; no checks says none.
func TestChecksFacts(t *testing.T) {
	red := forge.HeadChecks{State: forge.Red, SHA: "h3ad", Required: true, Failed: []forge.Check{
		{Name: "lint", URL: "https://ci/lint"},
	}, Running: []forge.Check{{Name: "test"}}}

	got := factsText(ChecksFacts("https://gh/pr/9", red))
	want := "pr=https://gh/pr/9\nhead=h3ad\nchecks=red (required)\nfailed lint=https://ci/lint\nrunning=test\n"

	if got != want {
		t.Errorf("facts:\n%s\nwant:\n%s", got, want)
	}

	none := factsText(ChecksFacts("https://gh/pr/9", forge.HeadChecks{SHA: "h3ad"}))
	if !strings.Contains(none, "checks=none") {
		t.Errorf("no checks:\n%s", none)
	}
}

// Red exits 1 and pending 8, as gh pr checks does; green and none exit 0.
func TestChecksExit(t *testing.T) {
	for state, want := range map[string]int{forge.Red: 1, forge.Pending: ui.ExitPending, forge.Green: 0, "": 0} {
		code := 0

		var ec cli.ExitCoder
		if err := checksExit(state); errors.As(err, &ec) {
			code = ec.ExitCode()
		}

		if code != want {
			t.Errorf("checksExit(%q) = %d, want %d", state, code, want)
		}
	}
}
