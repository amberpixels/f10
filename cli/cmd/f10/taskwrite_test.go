package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
)

func runWrite(t *testing.T, repo, stdin string, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Reader = strings.NewReader(stdin)
	app.Writer, app.ErrWriter = &out, &errOut
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {} // keep the exit code, not the exit

	err := app.Run(t.Context(), append([]string{"f10", "-C", repo, "task"}, args...))

	return out.String(), errOut.String(), err
}

// The comment goes to the driver on stdin with the task's number, and the
// url it prints comes back.
func TestTaskCommentThroughTheDriver(t *testing.T) {
	repo := createRepo(t, "echo https://notion.so/c1\n")

	out, _, err := runWrite(t, repo, "\nShipped: https://reports.example/q3\n", "comment", "FT-12")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}

	if out != "https://notion.so/c1\n" {
		t.Errorf("stdout = %q, want the comment's url", out)
	}

	storage := filepath.Join(repo, ".f10")
	if got := readFile(t, filepath.Join(storage, "args")); got != "comment 12\n" {
		t.Errorf("driver args = %q, want the number", got)
	}

	if got := readFile(t, filepath.Join(storage, "stdin")); got != "Shipped: https://reports.example/q3" {
		t.Errorf("driver stdin = %q, want the comment trimmed", got)
	}
}

// On a tracker that is not the host's, no driver or a declining one is
// exit 3, and a failed comment says it may have posted.
func TestTaskCommentDeclinedAndFailed(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		_, _, err := runWrite(t, createRepo(t, script), "hi\n", "comment", "FT-12")
		if exitCode(err) != exitNoWrite || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("%s: err = %v, want exit %d", name, err, exitNoWrite)
		}
	}

	_, _, err := runWrite(t, createRepo(t, "echo 'notion: 504' >&2\nexit 1\n"), "hi\n", "comment", "FT-12")
	if err == nil || !strings.Contains(err.Error(), "notion: 504") || !strings.Contains(err.Error(), mayPosted) {
		t.Errorf("failed comment: err = %v, want the driver's stderr and the may-posted warning", err)
	}

	repo := createRepo(t, "echo ok\n")
	if _, _, err := runWrite(t, repo, " \n", "comment", "FT-12"); err == nil {
		t.Error("an empty comment was accepted")
	}

	if _, statErr := os.Stat(filepath.Join(repo, ".f10", "args")); statErr == nil {
		t.Error("the driver ran for an empty comment")
	}
}

// Update forwards only the properties set, priority canonical, in a fixed order.
func TestTaskUpdateThroughTheDriver(t *testing.T) {
	repo := createRepo(t, "echo 'no assignee field' >&2\n")

	_, warn, err := runWrite(t, repo, "", "update", "FT-12",
		"--status", "Published", "--priority", "High", "--label", "q3", "--label", "web",
		"--assignee", "ann", "--set", "Report=https://r.example/q3")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	want := "update 12 --status Published --priority high --label q3 --label web --assignee ann " +
		"--set Report=https://r.example/q3\n"
	if got := readFile(t, filepath.Join(repo, ".f10", "args")); got != want {
		t.Errorf("driver args = %q, want %q", got, want)
	}

	if !strings.Contains(warn, "no assignee field") {
		t.Errorf("stderr = %q, want the driver's warning", warn)
	}

	if _, _, err := runWrite(
		t,
		createRepo(t, "exit 3\n"),
		"",
		"update",
		"FT-12",
		"--status",
		"Done",
	); exitCode(
		err,
	) != exitNoWrite {
		t.Errorf("declined update: err = %v, want exit %d", err, exitNoWrite)
	}
}

// Update refuses nothing to set, a bad priority and a --set without a key
// before the driver runs.
func TestTaskUpdateRefusesBadInput(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--priority", "p1"},
		{"--set", "novalue"},
		{"--set", "=x"},
	} {
		repo := createRepo(t, "echo ok\n")

		if _, _, err := runWrite(t, repo, "", append([]string{"update", "FT-12"}, args...)...); err == nil {
			t.Errorf("%v: accepted", args)
		}

		if _, statErr := os.Stat(filepath.Join(repo, ".f10", "args")); statErr == nil {
			t.Errorf("%v: the driver ran", args)
		}
	}
}

// Host issues take a comment through each CLI's own verb, and refuse a
// pull request's number.
func TestHostComment(t *testing.T) {
	f := newFakes(t)

	f.script("gh issue view 7 --json url", `{"url":"https://github.com/o/r/issues/7"}`)
	f.script("gh issue comment 7 --body hi", "https://github.com/o/r/issues/7#issuecomment-1\n")
	f.script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7"}`)
	f.script("glab issue note 7 --message hi", "")
	f.script("gh issue view 8 --json url", `{"url":"https://github.com/o/r/pull/8"}`)

	if got, err := newHost("gh", "").comment(t.Context(), "7", "hi"); err != nil ||
		got != "https://github.com/o/r/issues/7#issuecomment-1" {
		t.Errorf("gh comment = %q, %v", got, err)
	}

	if got, err := newHost("glab", "").comment(t.Context(), "7", "hi"); err != nil || got != "" {
		t.Errorf("glab comment = %q, %v; want no url", got, err)
	}

	if _, err := newHost("gh", "").comment(t.Context(), "8", "hi"); err == nil || f.called("gh issue comment 8") {
		t.Errorf("comment on a pull request: err = %v", err)
	}
}

// A host update moves the priority label, adds labels and the assignee,
// then closes; the level's other labels come off.
func TestHostUpdate(t *testing.T) {
	f := newFakes(t)

	view := `{"url":"https://github.com/o/r/issues/7","labels":[{"name":"P2"},{"name":"bug"}],` +
		`"assignees":[{"login":"bob"},{"login":"Ann"}]}`
	f.script("gh issue view 7 --json labels,assignees,url", view)
	f.script(
		"gh issue edit 7 --add-label shipped --add-label p1 --remove-label P2 --add-assignee ann --remove-assignee bob",
		"",
	)
	f.script("gh issue close 7", "")
	f.script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7","labels":["P2"]}`)
	f.script("glab issue update 7 --label p1 --unlabel P2", "")
	f.script("glab issue reopen 7", "")

	priorities := map[string]string{"p1": "high", "p2": "medium"}

	err := newHost("gh", "").update(t.Context(), "7",
		props{status: "Closed", priority: "high", labels: []string{"shipped"}, assignee: "ann"}, priorities)
	if err != nil || !f.called("gh issue close 7") {
		t.Errorf("gh update: %v", err)
	}

	if err := newHost(
		"glab",
		"",
	).update(t.Context(), "7", props{status: "open", priority: "high"}, priorities); err != nil ||
		!f.called("glab issue reopen 7") {
		t.Errorf("glab update: %v", err)
	}

	// @me keeps the signed-in login and drops the others
	f.script("gh api user", `{"login":"Ann"}`)
	f.script("gh issue edit 7 --add-assignee @me --remove-assignee bob", "")

	if err := newHost("gh", "").update(t.Context(), "7", props{assignee: "@me"}, priorities); err != nil ||
		!f.called("gh issue edit 7 --add-assignee @me --remove-assignee bob") {
		t.Errorf("gh update @me: %v", err)
	}

	for _, p := range []props{
		{status: "In progress"},
		{set: []string{"Sprint=7"}},
		{priority: "urgent"},
	} {
		if err := newHost("gh", "").update(t.Context(), "7", p, priorities); err == nil {
			t.Errorf("%+v: accepted", p)
		}
	}
}

// With no driver, a host tracker creates an issue: the labels and the
// priority's label in the create, the number read from the printed url,
// then the parent link by the child's database id.
func TestHostCreate(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")
	tg.eff.PriorityLabels = map[string]string{"p1": "high"}

	f.script("gh issue create --title Add it --body the body --label draft --label p1",
		"Creating issue in o/r\n\nhttps://github.com/o/r/issues/12\n")
	f.script("gh api repos/{owner}/{repo}/issues/12", `{"id":555,"number":12}`)
	f.script("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555", `{}`)

	var warn bytes.Buffer

	r, url, err := hostCreate(t.Context(), &warn, tg, "# Add it\n\nthe body\n",
		props{priority: "high", labels: []string{"draft"}}, "FT-7")
	if err != nil || r.ID != "FT-12" || url != "https://github.com/o/r/issues/12" || warn.Len() > 0 {
		t.Errorf("hostCreate = %+v, %q, %v, %q", r, url, err, warn.String())
	}

	if !f.called("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555") {
		t.Error("the subtask was not linked")
	}
}

// The issue exists once created, so a failed parent link is a warning with
// the id still returned, never an error that invites a second create.
func TestHostCreateLinkFailureWarns(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")

	f.script("gh issue create --title A --body ", "https://github.com/o/r/issues/12\n")
	f.fail("gh api repos/{owner}/{repo}/issues/12", shell.Result{Code: 1, Stderr: "HTTP 502"})

	var warn bytes.Buffer

	r, _, err := hostCreate(t.Context(), &warn, tg, "# A\n", props{}, "FT-7")
	if err != nil || r.ID != "FT-12" || !strings.Contains(warn.String(), "not linked under FT-7") {
		t.Errorf("hostCreate = %+v, %v, %q; want the id and a warning", r, err, warn.String())
	}
}

// A closed status or an unmapped priority is refused before anything is
// created; a create printing no issue url may still have made one.
func TestHostCreateRefusals(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")

	for _, p := range []props{{status: "closed"}, {priority: "urgent"}} {
		if _, _, err := hostCreate(t.Context(), &bytes.Buffer{}, tg, "# A\n", p, ""); err == nil {
			t.Errorf("%+v: no error", p)
		}
	}

	if f.called("gh issue create") {
		t.Error("an issue was created for refused input")
	}

	f.script("gh issue create --title A --body ", "done\n")

	if _, _, err := hostCreate(t.Context(), &bytes.Buffer{}, tg, "# A\n", props{}, ""); err == nil ||
		!strings.Contains(err.Error(), mayExist) {
		t.Errorf("no url printed: err = %v, want the may-exist warning", err)
	}
}

// GitLab takes no issue under an issue, so the link is refused without a
// call and create warns.
func TestLinkParentGitLab(t *testing.T) {
	f := newFakes(t)

	if err := newHost("glab", "").linkParent(t.Context(), "7", "12"); !errors.Is(err, errGitLabNoIssueParent) {
		t.Errorf("linkParent = %v", err)
	}

	if f.called("glab api") {
		t.Error("glab was called")
	}
}
