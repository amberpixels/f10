package hostops_test

import (
	"errors"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

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
