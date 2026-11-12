package tasksource_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// On GitHub a re-link moves the issue from the parent it has, an unlink
// removes it from the parent the issue names, and an issue with no parent
// is left alone; GitLab takes no issue parent, which update answers with
// exit 3.
func TestHostRelink(t *testing.T) {
	f := testkit.NewFakes(t)
	gh := hostops.NewHost("gh", "")

	f.Script("gh api repos/{owner}/{repo}/issues/12",
		`{"id":555,"parent_issue_url":"https://api.github.com/repos/o/r/issues/3"}`)
	f.Script("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true",
		`{}`)
	f.Script("gh api -X DELETE repos/o/r/issues/3/sub_issue -F sub_issue_id=555", `{}`)

	if err := gh.Relink(t.Context(), "7", "12"); err != nil ||
		!f.Called("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues") {
		t.Errorf("relink to 7: %v", err)
	}

	if err := gh.Relink(t.Context(), "", "12"); err != nil ||
		!f.Called("gh api -X DELETE repos/o/r/issues/3/sub_issue -F sub_issue_id=555") {
		t.Errorf("unlink: %v", err)
	}

	f.Script("gh api repos/{owner}/{repo}/issues/13", `{"id":556}`)

	before := len(f.Calls)

	if err := gh.Relink(t.Context(), "", "13"); err != nil || len(f.Calls) != before+1 {
		t.Errorf("unlink with no parent: %v", err)
	}

	err := tasksource.TasksFor(testkit.ChildTarget(t, "glab")).Relink(t.Context(), &bytes.Buffer{},
		ref.Ref{ID: "FT-12", Number: "12"}, "7", tasksource.UnwrittenNothing)
	if testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("glab relink: err = %v, want exit %d", err, ui.ExitDeclined)
	}
}

// With no driver, a host tracker creates an issue: the labels and the
// priority's label in the create, the number read from the printed url,
// then the parent link by the child's database id.
func TestHostCreate(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	tg.Effective.PriorityLabels = map[string]string{"p1": "high"}

	f.Script("gh issue create --title Add it --body the body --label draft --label p1",
		"Creating issue in o/r\n\nhttps://github.com/o/r/issues/12\n")
	f.Script("gh api repos/{owner}/{repo}/issues/12", `{"id":555,"number":12}`)
	f.Script("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true", `{}`)

	var warn bytes.Buffer

	r, url, err := tasksource.TasksFor(tg).Create(t.Context(), &warn, tasksource.NewTask{
		Doc:    "# Add it\n\nthe body\n",
		Props:  hostops.Props{Priority: "high", Labels: []string{"draft"}},
		Parent: ref.Ref{ID: "FT-7", Number: "7"},
	})
	if err != nil || r.ID != "FT-12" || url != "https://github.com/o/r/issues/12" || warn.Len() > 0 {
		t.Errorf("hostCreate = %+v, %q, %v, %q", r, url, err, warn.String())
	}

	if !f.Called("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true") {
		t.Error("the subtask was not linked")
	}
}

// The issue exists once created, so a failed parent link is a warning with
// the id still returned, never an error that invites a second create.
func TestHostCreateLinkFailureWarns(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")

	f.Script("gh issue create --title A --body ", "https://github.com/o/r/issues/12\n")
	f.Fail("gh api repos/{owner}/{repo}/issues/12", shell.Result{Code: 1, Stderr: "HTTP 502"})

	var warn bytes.Buffer

	r, _, err := tasksource.TasksFor(
		tg,
	).Create(t.Context(), &warn, tasksource.NewTask{Doc: "# A\n", Parent: ref.Ref{ID: "FT-7", Number: "7"}})
	if err != nil || r.ID != "FT-12" || !strings.Contains(warn.String(), "not linked under FT-7") {
		t.Errorf("hostCreate = %+v, %v, %q; want the id and a warning", r, err, warn.String())
	}
}

// A closed status, an unmapped priority or a --set property is refused
// before anything is created; a create printing no issue url may still have
// made one.
func TestHostCreateRefusals(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")

	for _, p := range []hostops.Props{{Status: "closed"}, {Priority: "urgent"}, {Set: []string{"Kind=Bug"}}} {
		if _, _, err := tasksource.TasksFor(tg).
			Create(t.Context(), &bytes.Buffer{}, tasksource.NewTask{Doc: "# A\n", Props: p}); err == nil {
			t.Errorf("%+v: no error", p)
		}
	}

	if f.Called("gh issue create") {
		t.Error("an issue was created for refused input")
	}

	f.Script("gh issue create --title A --body ", "done\n")

	if _, _, err := tasksource.TasksFor(tg).
		Create(t.Context(), &bytes.Buffer{}, tasksource.NewTask{Doc: "# A\n"}); err == nil ||
		!strings.Contains(err.Error(), tasksource.MayExist) {
		t.Errorf("no url printed: err = %v, want the may-exist warning", err)
	}
}
