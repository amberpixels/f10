package hostops_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The list pages hang off the repo url each CLI reports, in its own spelling.
func TestListURLs(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script("gh repo view --json url", `{"url":"https://github.com/o/r"}`)
	f.Script("glab repo view -F json", `{"web_url":"https://gitlab.example.com/g/r/"}`)

	for _, tc := range []struct {
		cli, issues, pulls string
	}{
		{"gh", "https://github.com/o/r/issues", "https://github.com/o/r/pulls"},
		{"glab", "https://gitlab.example.com/g/r/-/issues", "https://gitlab.example.com/g/r/-/merge_requests"},
	} {
		h := hostops.NewHost(tc.cli, "")

		if got, err := h.IssuesURL(t.Context()); err != nil || got != tc.issues {
			t.Errorf("%s issues = %q, %v; want %q", tc.cli, got, err, tc.issues)
		}

		if got, err := h.PullsURL(t.Context()); err != nil || got != tc.pulls {
			t.Errorf("%s pulls = %q, %v; want %q", tc.cli, got, err, tc.pulls)
		}
	}
}

// Into a pipe the rows are JSON, the priority present only where a row has
// one, so a project with nothing declared sees no priority key.
func TestWriteRowsJSONPriority(t *testing.T) {
	var out bytes.Buffer
	if err := hostops.WriteRows(
		&out,
		[]hostops.Row{{ID: "FT-1", Title: "a", Priority: "high"}, {ID: "FT-2", Title: "b"}},
		"none",
	); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), `"priority": "high"`) || strings.Count(out.String(), "priority") != 1 {
		t.Errorf("rows = %s", out.String())
	}
}

// Open requests list as rows, numbered the way each host writes them.
func TestPullRows(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script("gh pr list --json number,title,state,url --limit 30",
		`[{"number":113,"title":"bundle","state":"OPEN","url":"https://github.com/o/r/pull/113"}]`)
	f.Script("glab mr list -F json --per-page 30",
		`[{"iid":4,"title":"fix","state":"opened","web_url":"https://gitlab.example.com/g/r/-/merge_requests/4"}]`)

	for _, tc := range []struct{ cli, id, url string }{
		{"gh", "#113", "https://github.com/o/r/pull/113"},
		{"glab", "!4", "https://gitlab.example.com/g/r/-/merge_requests/4"},
	} {
		rows, err := hostops.NewHost(tc.cli, "").PullRows(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != tc.id || rows[0].Status != forge.Open ||
			rows[0].URL != tc.url {
			t.Errorf("%s rows = %+v, %v", tc.cli, rows, err)
		}
	}
}
