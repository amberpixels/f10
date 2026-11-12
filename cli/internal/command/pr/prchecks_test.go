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

// --brief is one line ship reads beside its badge.
func TestChecksLine(t *testing.T) {
	for want, hc := range map[string]forge.HeadChecks{
		"checks: red (lint, test)": {State: forge.Red, Failed: []forge.Check{{Name: "lint"}, {Name: "test"}}},
		"checks: pending (build)":  {State: forge.Pending, Running: []forge.Check{{Name: "build"}}},
		"checks: green":            {State: forge.Green},
		"checks: none":             {},
	} {
		if got := ChecksLine(hc); got != want {
			t.Errorf("ChecksLine = %q, want %q", got, want)
		}
	}
}

// --triage adds the failure to read, its job on the default branch, and
// what this red spell tried; a cancelled job reads as cancelled.
func TestTriageFacts(t *testing.T) {
	tr := &checkTriage{
		First:   &forge.Check{Name: "lint", Run: "77"},
		Default: forge.Green,
		Tried:   []string{"rerun at abc1234"},
	}

	got := factsText(TriageFacts(tr))
	want := "first=lint (run 77)\ndefault=green\ntried=rerun at abc1234\n"

	if got != want {
		t.Errorf("triage facts:\n%s\nwant:\n%s", got, want)
	}

	if got := factsText(
		TriageFacts(&checkTriage{First: &forge.Check{Name: "x"}}),
	); !strings.Contains(
		got,
		"default=none\ntried=nothing",
	) {
		t.Errorf("no history and nothing tried:\n%s", got)
	}

	cancelled := factsText(
		ChecksFacts("u", forge.HeadChecks{State: forge.Red, Failed: []forge.Check{{Name: "t", Cancelled: true}}}),
	)
	if !strings.Contains(cancelled, "cancelled t=") {
		t.Errorf("cancelled job:\n%s", cancelled)
	}
}
