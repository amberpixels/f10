package hostops_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
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

	err := hostops.TasksFor(testkit.ChildTarget(t, "glab")).Relink(t.Context(), &bytes.Buffer{},
		ref.Ref{ID: "FT-12", Number: "12"}, "7", hostops.UnwrittenNothing)
	if testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("glab relink: err = %v, want exit %d", err, ui.ExitDeclined)
	}
}

// Host issues take a comment through each CLI's own verb, and refuse a
// pull request's number.
func TestHostComment(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script(
		"gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/issues/7"}`,
	)
	f.Script("gh issue comment 7 --body hi", "https://github.com/o/r/issues/7#issuecomment-1\n")
	f.Script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7"}`)
	f.Script("glab issue note 7 --message hi", "")
	f.Script(
		"gh issue view 8 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/pull/8"}`,
	)

	if got, err := hostops.NewHost("gh", "").CommentIssue(t.Context(), "7", "hi"); err != nil ||
		got != "https://github.com/o/r/issues/7#issuecomment-1" {
		t.Errorf("gh comment = %q, %v", got, err)
	}

	if got, err := hostops.NewHost("glab", "").CommentIssue(t.Context(), "7", "hi"); err != nil || got != "" {
		t.Errorf("glab comment = %q, %v; want no url", got, err)
	}

	if _, err := hostops.NewHost("gh", "").
		CommentIssue(t.Context(), "8", "hi"); err == nil ||
		f.Called("gh issue comment 8") {
		t.Errorf("comment on a pull request: err = %v", err)
	}
}

// A host update moves the priority label, adds labels and the assignee,
// then closes; the level's other labels come off.
func TestHostUpdate(t *testing.T) {
	f := testkit.NewFakes(t)

	view := `{"url":"https://github.com/o/r/issues/7","labels":[{"name":"P2"},{"name":"bug"}],` +
		`"assignees":[{"login":"bob"},{"login":"Ann"}]}`
	f.Script("gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees", view)
	f.Script(
		"gh issue edit 7 --add-label shipped --add-label p1 --remove-label P2 --add-assignee ann --remove-assignee bob",
		"",
	)
	f.Script("gh issue close 7", "")
	f.Script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7","labels":["P2"]}`)
	f.Script("glab issue update 7 --label p1 --unlabel P2", "")
	f.Script("glab issue reopen 7", "")

	priorities := map[string]string{"p1": "high", "p2": "medium"}

	err := hostops.NewHost("gh", "").Update(t.Context(), "7",
		hostops.Props{Status: "Closed", Priority: "high", Labels: []string{"shipped"}, Assignee: "ann"}, priorities)
	if err != nil || !f.Called("gh issue close 7") {
		t.Errorf("gh update: %v", err)
	}

	if err := hostops.NewHost(
		"glab",
		"",
	).Update(t.Context(), "7", hostops.Props{Status: "open", Priority: "high"}, priorities); err != nil ||
		!f.Called("glab issue reopen 7") {
		t.Errorf("glab update: %v", err)
	}

	// @me keeps the signed-in login and drops the others
	f.Script("gh api user", `{"login":"Ann"}`)
	f.Script("gh issue edit 7 --add-assignee @me --remove-assignee bob", "")

	if err := hostops.NewHost("gh", "").
		Update(t.Context(), "7", hostops.Props{Assignee: "@me"}, priorities); err != nil ||
		!f.Called("gh issue edit 7 --add-assignee @me --remove-assignee bob") {
		t.Errorf("gh update @me: %v", err)
	}

	for _, p := range []hostops.Props{
		{Status: "In progress"},
		{Set: []string{"Sprint=7"}},
		{Priority: "urgent"},
	} {
		if err := hostops.NewHost("gh", "").Update(t.Context(), "7", p, priorities); err == nil {
			t.Errorf("%+v: accepted", p)
		}
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

	r, url, err := hostops.TasksFor(tg).Create(t.Context(), &warn, hostops.NewTask{
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

	r, _, err := hostops.TasksFor(
		tg,
	).Create(t.Context(), &warn, hostops.NewTask{Doc: "# A\n", Parent: ref.Ref{ID: "FT-7", Number: "7"}})
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
		if _, _, err := hostops.TasksFor(tg).
			Create(t.Context(), &bytes.Buffer{}, hostops.NewTask{Doc: "# A\n", Props: p}); err == nil {
			t.Errorf("%+v: no error", p)
		}
	}

	if f.Called("gh issue create") {
		t.Error("an issue was created for refused input")
	}

	f.Script("gh issue create --title A --body ", "done\n")

	if _, _, err := hostops.TasksFor(tg).
		Create(t.Context(), &bytes.Buffer{}, hostops.NewTask{Doc: "# A\n"}); err == nil ||
		!strings.Contains(err.Error(), hostops.MayExist) {
		t.Errorf("no url printed: err = %v, want the may-exist warning", err)
	}
}

// GitLab takes no issue under an issue, so the link is refused without a
// call and create warns.
func TestLinkParentGitLab(t *testing.T) {
	f := testkit.NewFakes(t)

	if err := hostops.NewHost("glab", "").LinkParent(t.Context(), "7", "12"); !errors.Is(err, forge.ErrNoIssueParent) {
		t.Errorf("linkParent = %v", err)
	}

	if f.Called("glab api") {
		t.Error("glab was called")
	}
}
