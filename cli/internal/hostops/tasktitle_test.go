package hostops_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Every host read takes the prefix tracker.title wrote off the title, so
// the id shows once.
func TestHostReadsStripTheTitle(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script("gh issue view 12 --json number,title,state,labels,createdAt,url,body,assignees,comments",
		`{"number":12,"title":"FT-12: Drive halts","url":"https://github.com/o/r/issues/12"}`)
	f.Script(
		"gh issue view 12 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"FT-12: Drive halts","url":"https://github.com/o/r/issues/12"}`,
	)
	f.Script("gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":12,"title":"FT-12: Drive halts"},{"number":13,"title":"Bare one"}]`)

	h := testkit.TitledHost()

	doc, _, err := h.IssueDoc(t.Context(), "FT-12", "12", facts.StaleAfter{})
	if err != nil || !strings.HasPrefix(doc, "# FT-12: Drive halts\n") {
		t.Errorf("issueDoc = %q, %v", doc, err)
	}

	if title, err := h.IssueTitle(t.Context(), "12"); err != nil || title != "Drive halts" {
		t.Errorf("issueTitle = %q, %v", title, err)
	}

	rows, err := h.ListIssues(t.Context(), "", "FT", nil, hostops.IssuePage)
	if err != nil || len(rows) != 2 || rows[0].Title != "Drive halts" || rows[1].Title != "Bare one" {
		t.Errorf("listIssues = %+v, %v", rows, err)
	}

	// no template: the title stays as the host holds it
	if title, _ := hostops.NewHost("gh", "").IssueTitle(t.Context(), "12"); title != "FT-12: Drive halts" {
		t.Errorf("issueTitle without a template = %q", title)
	}
}

func TestDriverRowsStripTheTitle(t *testing.T) {
	rows := hostops.StripTitles(
		[]hostops.Row{{ID: "FT-3", Title: "FT-3 - Ship it"}, {ID: "FT-4", Title: "Plain"}},
		"{id}: {title}",
	)
	if rows[0].Title != "Ship it" || rows[1].Title != "Plain" {
		t.Errorf("stripTitles = %+v", rows)
	}
}

// An update puts the prefix back on a title renamed by hand, and leaves a
// prefixed title alone.
func TestHostUpdateRestoresThePrefix(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script("gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/issues/7","title":"Renamed by hand"}`)
	f.Script("gh issue edit 7 --title FT-7: Renamed by hand --add-label shipped", "")

	if err := testkit.TitledHost().
		Update(t.Context(), "7", hostops.Props{Labels: []string{"shipped"}}, nil); err != nil ||
		!f.Called("gh issue edit 7 --title FT-7: Renamed by hand --add-label shipped") {
		t.Errorf("update: %v, calls %v", err, f.Calls)
	}

	f.Script("gh issue view 8 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/issues/8","title":"FT-8: Fine"}`)
	f.Script("gh issue close 8", "")

	if err := testkit.TitledHost().Update(t.Context(), "8", hostops.Props{Status: "closed"}, nil); err != nil ||
		f.Called("gh issue edit 8") {
		t.Errorf("update of a prefixed title edited it: %v, calls %v", err, f.Calls)
	}
}

func TestOneIDHeading(t *testing.T) {
	r := ref.Ref{ID: "FT-12", Number: "12"}

	for doc, want := range map[string]string{
		"# FT-12: FT-12: Drive\n\nbody": "# FT-12: Drive\n\nbody",
		"# FT-12: Drive\n\nbody":        "# FT-12: Drive\n\nbody",
		"# Drive":                       "# Drive",
		"no heading":                    "no heading",
	} {
		if got := hostops.OneIDHeading(doc, r, "{id}: {title}"); got != want {
			t.Errorf("oneIDHeading(%q) = %q, want %q", doc, got, want)
		}
	}
}

// After a driver writes an issue on the host's tracker, its title goes
// through tracker.title; a failed retitle warns, since the write stands.
func TestRetitleAfter(t *testing.T) {
	f := testkit.NewFakes(t)

	tg := &target.Target{
		Dir:       t.TempDir(),
		Probes:    &probe.Probes{CLI: probe.Signal{Value: "gh"}},
		Effective: &facts.Effective{TrackerKind: facts.TrackerGitHub, IDPrefix: "FT", TitleTemplate: "{id}: {title}"},
	}
	r := ref.Ref{ID: "FT-12", Number: "12"}

	f.Script(
		"gh issue view 12 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"number":12,"title":"Drive","url":"https://github.com/o/r/issues/12"}`,
	)
	f.Script("gh issue edit 12 --title FT-12: Drive", "")

	var warn strings.Builder

	hostops.RetitleAfter(t.Context(), &warn, tg, r, "created")

	if !f.Called("gh issue edit 12 --title FT-12: Drive") || warn.Len() != 0 {
		t.Errorf("retitle: calls %v, warning %q", f.Calls, warn.String())
	}

	f.Fail("gh issue edit 12 --title FT-12: Drive", shell.Result{Code: 1, Stderr: "HTTP 502"})

	hostops.RetitleAfter(t.Context(), &warn, tg, r, "created")

	if !strings.Contains(warn.String(), "FT-12 was created") ||
		!strings.Contains(warn.String(), "f10 task retitle FT-12") {
		t.Errorf("failed retitle warned %q", warn.String())
	}
}
