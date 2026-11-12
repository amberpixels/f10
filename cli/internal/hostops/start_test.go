package hostops_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The title comes from the host's own issue, through the one field gh is
// asked for and the whole issue glab answers with.
func TestHostIssueTitle(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(
		"gh issue view 1 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Add dark mode","url":"https://github.com/o/r/issues/1"}`,
	)
	f.Script("glab issue view 1 -F json", `{"iid":1,"title":"Dunkelmodus","web_url":"https://x"}`)
	f.Fail(
		"gh issue view 2 --json number,title,state,labels,createdAt,url,body,assignees",
		shell.Result{Code: 1, Stderr: "no issue 2"},
	)
	f.Script("gh issue view 3 --json number,title,state,labels,createdAt,url,body,assignees", `{"title":""}`)
	f.Script(
		"gh issue view 4 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Merged PR","url":"https://github.com/o/r/pull/4"}`,
	)

	for name, want := range map[string]string{"gh": "Add dark mode", "glab": "Dunkelmodus"} {
		got, err := hostops.NewHost(name, "/code/repo").IssueTitle(t.Context(), "1")
		if err != nil || got != want {
			t.Errorf("%s title = %q, %v; want %q", name, got, err, want)
		}
	}

	gh := hostops.NewHost("gh", "/code/repo")

	if _, err := gh.IssueTitle(t.Context(), "2"); err == nil || !strings.Contains(err.Error(), "no issue 2") {
		t.Errorf("err = %v, want gh's stderr", err)
	}

	if _, err := gh.IssueTitle(t.Context(), "3"); err == nil || !strings.Contains(err.Error(), "no title") {
		t.Errorf("err = %v, want no title", err)
	}

	// gh answers `issue view` for a pull request too: its title would name a
	// branch after an unrelated PR
	if _, err := gh.IssueTitle(t.Context(), "4"); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("err = %v, want a pull request refused", err)
	}
}

// task read and task open refuse a pull request the same way.
func TestHostRefusesPullRequests(t *testing.T) {
	f := testkit.NewFakes(t)
	pr := `{"number":35,"title":"Merged PR","url":"https://github.com/o/r/pull/35"}`
	f.Script("gh issue view 35 --json number,title,state,labels,createdAt,url,body,assignees,comments", pr)
	f.Script("gh issue view 35 --json number,title,state,labels,createdAt,url,body,assignees", pr)
	f.Script("glab issue view 7 -F json", `{"iid":7,"web_url":"https://gitlab.com/g/p/-/merge_requests/7"}`)

	gh := hostops.NewHost("gh", "/code/repo")

	if _, _, err := gh.IssueDoc(
		t.Context(),
		"FT-35",
		"35",
		facts.StaleAfter{},
	); err == nil ||
		!strings.Contains(err.Error(), "#35 is a pull request") {
		t.Errorf("issueDoc = %v", err)
	}

	if _, err := gh.IssueURL(t.Context(), "35"); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("issueURL = %v", err)
	}

	if _, err := hostops.NewHost("glab", "/code/repo").IssueURL(t.Context(), "7"); err == nil {
		t.Error("glab merge request taken for an issue")
	}
}

// task read puts the labels and the created date, in UTC, between state
// and url, whichever shape the host gives them in.
func TestHostIssueDocLabels(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh issue view 12 --json number,title,state,labels,createdAt,url,body,assignees,comments",
		`{"number":12,"title":"Crash","state":"OPEN","labels":[{"name":"bug"},{"name":"draft"}],`+
			`"url":"https://github.com/o/r/issues/12","body":"boom"}`)
	f.Script("gh issue view 13 --json number,title,state,labels,createdAt,url,body,assignees,comments",
		`{"number":13,"title":"Plain","state":"OPEN","labels":[],"url":"https://github.com/o/r/issues/13"}`)
	f.Script("gh issue view 14 --json number,title,state,labels,createdAt,url,body,assignees,comments",
		`{"number":14,"title":"Old","state":"OPEN","labels":[{"name":"bug"}],"createdAt":"2026-09-20T23:30:00-02:00",`+
			`"url":"https://github.com/o/r/issues/14"}`)
	f.Script("glab issue view 7 -F json",
		`{"iid":7,"title":"Crash","state":"opened","labels":["bug"],"created_at":"2026-10-01T08:00:00Z",`+
			`"web_url":"https://gitlab.com/g/p/-/issues/7"}`)

	tests := []struct {
		host   string
		number string
		stale  string
		want   string
	}{
		{"gh", "12", "", "OPEN · bug, draft · https://github.com/o/r/issues/12\n"},
		{"gh", "13", "3w", "OPEN · https://github.com/o/r/issues/13\n"},
		{"gh", "14", "", "OPEN · bug · created 2026-09-21 · https://github.com/o/r/issues/14\n"},
		{
			"gh",
			"14",
			"3w",
			"OPEN · bug · created 2026-09-21 (stale: older than 3w) · https://github.com/o/r/issues/14\n",
		},
		{"glab", "7", "", "opened · bug · created 2026-10-01 · https://gitlab.com/g/p/-/issues/7\n"},
		{"glab", "7", "520w", "opened · bug · created 2026-10-01 · https://gitlab.com/g/p/-/issues/7\n"},
	}

	for _, tt := range tests {
		doc, _, err := hostops.NewHost(
			tt.host,
			"/code/repo",
		).IssueDoc(t.Context(), "FT-"+tt.number, tt.number, testkit.StaleOf(tt.stale))
		if err != nil || !strings.Contains(doc, tt.want) {
			t.Errorf("%s issueDoc(%s) = %q, %v; want a line %q", tt.host, tt.number, doc, err, tt.want)
		}
	}
}
