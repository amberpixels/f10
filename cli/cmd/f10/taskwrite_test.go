package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
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
		if testkit.ExitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("%s: err = %v, want exit %d", name, err, ui.ExitDeclined)
		}
	}

	_, _, err := runAppStdin(t, strings.NewReader("hi\n"), "-C",
		createRepo(t, "echo 'notion: 504' >&2\nexit 1\n"), "task", "comment", "FT-12").streams()
	if err == nil || !strings.Contains(err.Error(), "notion: 504") ||
		!strings.Contains(err.Error(), tasksource.MayPosted) {
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
		"FT-12", "--status", "Done").streams(); testkit.ExitCode(
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
	if testkit.ExitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "the parent link was not") {
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
