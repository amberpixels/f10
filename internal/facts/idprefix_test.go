package facts

import (
	"testing"

	"github.com/amberpixels/f10/internal/probe"
)

func TestIDPrefix(t *testing.T) {
	gh := &probe.Probes{CLI: probe.Signal{Value: "gh"}}
	glab := &probe.Probes{CLI: probe.Signal{Value: "glab"}}
	none := &probe.Probes{CLI: probe.Signal{Value: "none"}}

	cases := []struct {
		name     string
		declared string
		pb       *probe.Probes
		want     string
	}{
		{
			// how a project.md actually writes it: the placeholders sit
			// inside backticks and bold, so nothing word-like follows them
			name:     "placeholder format in markup",
			declared: "Notion. Task id format: **`WS-####`** (uppercase - plans go to `.f10/plans/WS-####.md`).",
			pb:       gh,
			want:     "WS",
		},
		{name: "bare placeholder format", declared: "Task id format: GH-###", pb: gh, want: "GH"},
		{name: "literal sample", declared: "Jira. Ids look like ABC-1234.", pb: gh, want: "ABC"},
		{
			// a placeholder anywhere beats a sample-shaped token that was
			// never an id at all
			name:     "placeholder wins over an incidental dash-number",
			declared: "Bodies are UTF-8. Task id format: **`WS-####`**.",
			pb:       gh,
			want:     "WS",
		},
		{name: "undeclared falls back to the host", declared: "GitHub Issues (via gh)", pb: gh, want: "GH"},
		{name: "undeclared on gitlab", declared: "GitLab work items (via glab)", pb: glab, want: "GL"},
		{name: "no format and no host says nothing", declared: "some tracker", pb: none, want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idPrefix(c.declared, c.pb); got != c.want {
				t.Errorf("idPrefix(%q) = %q, want %q", c.declared, got, c.want)
			}
		})
	}
}
