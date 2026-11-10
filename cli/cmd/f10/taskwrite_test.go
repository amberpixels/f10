package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The comment goes to the driver on stdin with the task's number, and the
// url it prints comes back.
func TestTaskCommentThroughTheDriver(t *testing.T) {
	repo := createRepo(t, "echo https://notion.so/c1\n")

	out, _, err := runAppStdin(t, strings.NewReader("\nShipped: https://reports.example/q3\n"), "-C", repo,
		"task", "comment", "FT-12").streams()
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
		_, _, err := runAppStdin(t, strings.NewReader("hi\n"), "-C", createRepo(t, script), "task", "comment",
			"FT-12").streams()
		if exitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("%s: err = %v, want exit %d", name, err, ui.ExitDeclined)
		}
	}

	_, _, err := runAppStdin(t, strings.NewReader("hi\n"), "-C",
		createRepo(t, "echo 'notion: 504' >&2\nexit 1\n"), "task", "comment", "FT-12").streams()
	if err == nil || !strings.Contains(err.Error(), "notion: 504") ||
		!strings.Contains(err.Error(), hostops.MayPosted) {
		t.Errorf("failed comment: err = %v, want the driver's stderr and the may-posted warning", err)
	}

	repo := createRepo(t, "echo ok\n")
	_, _, err = runAppStdin(t, strings.NewReader(" \n"), "-C", repo, "task", "comment", "FT-12").streams()
	if err == nil {
		t.Error("an empty comment was accepted")
	}

	if _, statErr := os.Stat(filepath.Join(repo, ".f10", "args")); statErr == nil {
		t.Error("the driver ran for an empty comment")
	}
}

// Update forwards only the properties set, priority canonical, in a fixed order.
func TestTaskUpdateThroughTheDriver(t *testing.T) {
	repo := createRepo(t, "echo 'no assignee field' >&2\n")

	_, warn, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "update", "FT-12", "--status",
		"Published", "--priority", "High", "--label", "q3", "--label", "web", "--assignee", "ann", "--set",
		"Report=https://r.example/q3").streams()
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

	if _, _, err := runAppStdin(t, strings.NewReader(""), "-C", createRepo(t, "exit 3\n"), "task", "update",
		"FT-12", "--status", "Done").streams(); exitCode(
		err,
	) != ui.ExitDeclined {
		t.Errorf("declined update: err = %v, want exit %d", err, ui.ExitDeclined)
	}
}

// The parent goes to the driver as a number, in an update of its own after
// the other properties, and "" is passed through to take the link off.
func TestTaskUpdateParentThroughTheDriver(t *testing.T) {
	log := "echo \"$@\" >> \"$here/log\"\n"

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"with a status": {
			[]string{"--status", "Done", "--parent", "FT-7"},
			"update 12 --status Done\nupdate 12 --parent 7\n",
		},
		"alone":  {[]string{"--parent", "FT-7"}, "update 12 --parent 7\n"},
		"unlink": {[]string{"--parent", ""}, "update 12 --parent \n"},
	} {
		repo := createRepo(t, log)

		_, _, err := runAppStdin(t, strings.NewReader(""),
			append([]string{"-C", repo, "task"}, append([]string{"update", "FT-12"}, tc.args...)...)...).streams()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got := readFile(t, filepath.Join(repo, ".f10", "log")); got != tc.want {
			t.Errorf("%s: driver calls = %q, want %q", name, got, tc.want)
		}
	}
}

// A driver declining the parent on a tracker that is not the host's is exit
// 3, after the status it took; a task named as its own parent is refused
// before the driver runs.
func TestTaskUpdateParentDeclinedAndRefused(t *testing.T) {
	repo := createRepo(t, "echo \"$@\" >> \"$here/log\"\ncase \"$*\" in *--parent*) exit 3 ;; esac\n")

	_, _, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "update", "FT-12", "--status",
		"Done", "--parent", "FT-7").streams()
	if exitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "the parent link was not") {
		t.Errorf("declined parent: err = %v, want exit %d naming the link", err, ui.ExitDeclined)
	}

	if got := readFile(t, filepath.Join(repo, ".f10", "log")); !strings.HasPrefix(got, "update 12 --status Done\n") {
		t.Errorf("driver calls = %q, want the status first", got)
	}

	repo = createRepo(t, "echo ok\n")
	_, _, err = runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "update", "FT-12", "--status",
		"Done", "--parent", "FT-12").streams()
	if err == nil {
		t.Error("a task was accepted as its own parent")
	}

	if _, statErr := os.Stat(filepath.Join(repo, ".f10", "args")); statErr == nil {
		t.Error("the driver ran for a self-parent")
	}
}

// On GitHub a re-link moves the issue from the parent it has, an unlink
// removes it from the parent the issue names, and an issue with no parent
// is left alone; GitLab takes no issue parent, which update answers with
// exit 3.
func TestHostRelink(t *testing.T) {
	f := newFakes(t)
	gh := hostops.NewHost("gh", "")

	f.script("gh api repos/{owner}/{repo}/issues/12",
		`{"id":555,"parent_issue_url":"https://api.github.com/repos/o/r/issues/3"}`)
	f.script("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true",
		`{}`)
	f.script("gh api -X DELETE repos/o/r/issues/3/sub_issue -F sub_issue_id=555", `{}`)

	if err := gh.Relink(t.Context(), "7", "12"); err != nil ||
		!f.called("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues") {
		t.Errorf("relink to 7: %v", err)
	}

	if err := gh.Relink(t.Context(), "", "12"); err != nil ||
		!f.called("gh api -X DELETE repos/o/r/issues/3/sub_issue -F sub_issue_id=555") {
		t.Errorf("unlink: %v", err)
	}

	f.script("gh api repos/{owner}/{repo}/issues/13", `{"id":556}`)

	before := len(f.calls)

	if err := gh.Relink(t.Context(), "", "13"); err != nil || len(f.calls) != before+1 {
		t.Errorf("unlink with no parent: %v", err)
	}

	err := hostops.TasksFor(childTarget(t, "glab")).Relink(t.Context(), &bytes.Buffer{},
		ref.Ref{ID: "FT-12", Number: "12"}, "7", hostops.UnwrittenNothing)
	if exitCode(err) != ui.ExitDeclined {
		t.Errorf("glab relink: err = %v, want exit %d", err, ui.ExitDeclined)
	}
}

// On a GitHub tracker, with no driver or past one declining the parent,
// update links the issue as a sub-issue by its database id, moving it from
// a parent it has; a driver failing after the status says the status landed.
func TestTaskUpdateParentOnHostIssues(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		repo, calls := hostRepo(t, script)
		gh := "#!/bin/sh\necho \"$@\" >> " + calls + "\n" +
			"case \"$*\" in \"api repos/{owner}/{repo}/issues/12\") echo '{\"id\":555}' ;; api*) echo '{}' ;; esac\n"

		if err := os.WriteFile(filepath.Join(filepath.Dir(calls), "gh"), []byte(gh), 0o700); err != nil {
			t.Fatal(err)
		}

		_, _, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "update", "FT-12", "--parent",
			"FT-7").streams()
		if err != nil {
			t.Fatalf("%s: update: %v", name, err)
		}

		want := "api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true"
		if got := readFile(t, calls); !strings.Contains(got, want) {
			t.Errorf("%s: gh calls = %q, want %q", name, got, want)
		}
	}

	repo := createRepo(t,
		"case \"$*\" in *--parent*) echo 'flag provided but not defined: -parent' >&2; exit 1 ;; esac\n")

	_, _, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "update", "FT-12", "--status",
		"Done", "--parent", "FT-7").streams()
	if err == nil || !strings.Contains(err.Error(), "-parent") ||
		!strings.Contains(err.Error(), "other properties were written") {
		t.Errorf("failed parent: err = %v, want the driver's error and that the status landed", err)
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

		_, _, err := runAppStdin(t, strings.NewReader(""),
			append([]string{"-C", repo, "task"}, append([]string{"update", "FT-12"}, args...)...)...).streams()
		if err == nil {
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

	f.script(
		"gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/issues/7"}`,
	)
	f.script("gh issue comment 7 --body hi", "https://github.com/o/r/issues/7#issuecomment-1\n")
	f.script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7"}`)
	f.script("glab issue note 7 --message hi", "")
	f.script(
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
		f.called("gh issue comment 8") {
		t.Errorf("comment on a pull request: err = %v", err)
	}
}

// A host update moves the priority label, adds labels and the assignee,
// then closes; the level's other labels come off.
func TestHostUpdate(t *testing.T) {
	f := newFakes(t)

	view := `{"url":"https://github.com/o/r/issues/7","labels":[{"name":"P2"},{"name":"bug"}],` +
		`"assignees":[{"login":"bob"},{"login":"Ann"}]}`
	f.script("gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees", view)
	f.script(
		"gh issue edit 7 --add-label shipped --add-label p1 --remove-label P2 --add-assignee ann --remove-assignee bob",
		"",
	)
	f.script("gh issue close 7", "")
	f.script("glab issue view 7 -F json", `{"web_url":"https://gitlab.example.com/g/r/-/issues/7","labels":["P2"]}`)
	f.script("glab issue update 7 --label p1 --unlabel P2", "")
	f.script("glab issue reopen 7", "")

	priorities := map[string]string{"p1": "high", "p2": "medium"}

	err := hostops.NewHost("gh", "").Update(t.Context(), "7",
		hostops.Props{Status: "Closed", Priority: "high", Labels: []string{"shipped"}, Assignee: "ann"}, priorities)
	if err != nil || !f.called("gh issue close 7") {
		t.Errorf("gh update: %v", err)
	}

	if err := hostops.NewHost(
		"glab",
		"",
	).Update(t.Context(), "7", hostops.Props{Status: "open", Priority: "high"}, priorities); err != nil ||
		!f.called("glab issue reopen 7") {
		t.Errorf("glab update: %v", err)
	}

	// @me keeps the signed-in login and drops the others
	f.script("gh api user", `{"login":"Ann"}`)
	f.script("gh issue edit 7 --add-assignee @me --remove-assignee bob", "")

	if err := hostops.NewHost("gh", "").
		Update(t.Context(), "7", hostops.Props{Assignee: "@me"}, priorities); err != nil ||
		!f.called("gh issue edit 7 --add-assignee @me --remove-assignee bob") {
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
	f := newFakes(t)
	tg := childTarget(t, "gh")
	tg.Effective.PriorityLabels = map[string]string{"p1": "high"}

	f.script("gh issue create --title Add it --body the body --label draft --label p1",
		"Creating issue in o/r\n\nhttps://github.com/o/r/issues/12\n")
	f.script("gh api repos/{owner}/{repo}/issues/12", `{"id":555,"number":12}`)
	f.script("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true", `{}`)

	var warn bytes.Buffer

	r, url, err := hostops.TasksFor(tg).Create(t.Context(), &warn, hostops.NewTask{
		Doc:    "# Add it\n\nthe body\n",
		Props:  hostops.Props{Priority: "high", Labels: []string{"draft"}},
		Parent: ref.Ref{ID: "FT-7", Number: "7"},
	})
	if err != nil || r.ID != "FT-12" || url != "https://github.com/o/r/issues/12" || warn.Len() > 0 {
		t.Errorf("hostCreate = %+v, %q, %v, %q", r, url, err, warn.String())
	}

	if !f.called("gh api -X POST repos/{owner}/{repo}/issues/7/sub_issues -F sub_issue_id=555 -F replace_parent=true") {
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
	f := newFakes(t)
	tg := childTarget(t, "gh")

	for _, p := range []hostops.Props{{Status: "closed"}, {Priority: "urgent"}, {Set: []string{"Kind=Bug"}}} {
		if _, _, err := hostops.TasksFor(tg).
			Create(t.Context(), &bytes.Buffer{}, hostops.NewTask{Doc: "# A\n", Props: p}); err == nil {
			t.Errorf("%+v: no error", p)
		}
	}

	if f.called("gh issue create") {
		t.Error("an issue was created for refused input")
	}

	f.script("gh issue create --title A --body ", "done\n")

	if _, _, err := hostops.TasksFor(tg).
		Create(t.Context(), &bytes.Buffer{}, hostops.NewTask{Doc: "# A\n"}); err == nil ||
		!strings.Contains(err.Error(), hostops.MayExist) {
		t.Errorf("no url printed: err = %v, want the may-exist warning", err)
	}
}

// GitLab takes no issue under an issue, so the link is refused without a
// call and create warns.
func TestLinkParentGitLab(t *testing.T) {
	f := newFakes(t)

	if err := hostops.NewHost("glab", "").LinkParent(t.Context(), "7", "12"); !errors.Is(err, forge.ErrNoIssueParent) {
		t.Errorf("linkParent = %v", err)
	}

	if f.called("glab api") {
		t.Error("glab was called")
	}
}
