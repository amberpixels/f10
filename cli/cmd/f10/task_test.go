package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	boardcmd "github.com/amberpixels/f10/cli/internal/command/board"
	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The open-task list: a driver's list wins, its priority carried; a driver
// declining it falls back to the host's open issues, ids in the project's
// format and priorities read from labels through the declared map.
func TestTaskRows(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())

	f.Script(
		"driver list",
		`[{"id":"FT-9","title":"from the driver","status":"Todo","url":"https://t/9","priority":"high",`+
			`"body":"Some text.\n\nAfter: 12\n"}]`,
	)

	rows, err := stack.TaskRows(t.Context(), tg, hostops.IssuePage)
	if err != nil || len(rows) != 1 || rows[0].ID != "FT-9" || rows[0].Priority != "high" {
		t.Errorf("driver list = %+v, %v", rows, err)
	}

	// the body is read for its After: line, a bare number in the project's
	// format, and never printed
	if tasks := boardcmd.BoardTasks(rows, "FT"); len(tasks) != 1 || tasks[0].After != "FT-12" {
		t.Errorf("board tasks = %+v, want After FT-12", tasks)
	}

	var printed bytes.Buffer
	if err := hostops.WriteRows(&printed, rows, "none"); err != nil || strings.Contains(printed.String(), "body") {
		t.Errorf("printed rows = %q, %v; want no body", printed.String(), err)
	}

	f.Fail("driver list", shell.Result{Code: trackerdriver.ExitUnsupported})
	f.Script(
		"gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":7,"title":"host issue","state":"OPEN","url":"https://github.com/o/r/issues/7","labels":[{"name":"P3"}]},`+
			`{"number":8,"title":"unlabelled","state":"OPEN","url":"https://github.com/o/r/issues/8","labels":[]},`+
			`{"number":9,"title":"two labels","state":"OPEN","url":"https://github.com/o/r/issues/9","labels":[{"name":"p1"},{"name":"P3"}]}]`,
	)

	rows, err = stack.TaskRows(t.Context(), tg, hostops.IssuePage)
	if err != nil || len(rows) != 3 || rows[0].ID != "FT-7" || rows[0].Status != "OPEN" || rows[0].Priority != "" {
		t.Errorf("fallback without a map = %+v, %v", rows, err)
	}

	tg.Effective.PriorityLabels = map[string]string{"p1": "high", "p3": "low"}

	rows, err = stack.TaskRows(t.Context(), tg, hostops.IssuePage)
	if err != nil {
		t.Fatal(err)
	}

	rows = taskcmd.RankRows(rows)

	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.ID+":"+r.Priority)
	}

	if want := "FT-9:high FT-7:low FT-8:"; strings.Join(got, " ") != want {
		t.Errorf("ranked rows = %q, want %q", strings.Join(got, " "), want)
	}
}

// createRepo is a checkout declaring FT-### ids, with the driver script at
// its storage root when one is given. The driver records its arguments and
// stdin beside itself, so a test reads what create handed over.
func createRepo(t *testing.T, script string) string {
	t.Helper()

	repo := t.TempDir()
	testkit.Git(t, repo, "init", "-q")

	dir := filepath.Join(repo, ".f10", "instructions")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	md := "---\ntracker:\n  kind: notion\n  id: FT-###\n---\n# demo\n"
	if err := os.WriteFile(filepath.Join(dir, "project.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if script != "" {
		// the read verbs leave args and stdin to the write a test checks
		body := "#!/bin/sh\nhere=$(dirname \"$0\")\ncase \"$1\" in read|url) ;; *) echo \"$@\" > \"$here/args\"; " +
			"cat > \"$here/stdin\" ;; esac\n" + script
		if err := os.WriteFile(filepath.Join(repo, ".f10", "driver"), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	return repo
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// The task goes to the driver on stdin with only the properties set, the
// parent and the body's After: base as numbers; the driver's number comes
// back as the project's id, and its warning reaches stderr.
func TestTaskCreateThroughTheDriver(t *testing.T) {
	repo := createRepo(t, "echo 'no labels on this tracker' >&2\necho 1043\n")
	doc := "# Add the index\n\nbody\n\nAfter: FT-1041\n"

	out, warn, err := runAppStdin(t, strings.NewReader(doc), "-C", repo, "task", "create", "--priority",
		"High", "--label", "draft", "--parent", "FT-1040").streams()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if out != "FT-1043\n" {
		t.Errorf("stdout = %q, want the id in the project's format", out)
	}

	if !strings.Contains(warn, "no labels on this tracker") {
		t.Errorf("stderr = %q, want the driver's warning", warn)
	}

	storage := filepath.Join(repo, ".f10")

	if got, want := readFile(
		t,
		filepath.Join(storage, "args"),
	), "create --priority high --label draft --parent 1040 --after 1041\n"; got != want {
		t.Errorf("driver args = %q, want %q", got, want)
	}

	if got := readFile(t, filepath.Join(storage, "stdin")); got != doc {
		t.Errorf("driver stdin = %q, want the task as given", got)
	}
}

// --set reaches the driver's create once per property, in the order given,
// as update passes it.
func TestTaskCreateSetsProperties(t *testing.T) {
	repo := createRepo(t, "echo 1043\n")

	_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", repo, "task", "create", "--set", "Kind=Bug",
		"--set", "Scope=CLI tools").streams()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got, want := readFile(t, filepath.Join(repo, ".f10", "args")),
		"create --set Kind=Bug --set Scope=CLI tools\n"; got != want {
		t.Errorf("driver args = %q, want %q", got, want)
	}
}

// Blank lines before the title are dropped, so the driver reads the title
// on the first line as the contract promises.
func TestTaskCreateTitleOnTheFirstLine(t *testing.T) {
	repo := createRepo(t, "echo 1\n")

	_, _, err := runAppStdin(t, strings.NewReader("\n\n# A\n\nbody\n"), "-C", repo, "task", "create").streams()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got := readFile(t, filepath.Join(repo, ".f10", "stdin")); got != "# A\n\nbody\n" {
		t.Errorf("driver stdin = %q, want the title first", got)
	}
}

// A prefixed id from the driver passes as it is.
func TestTaskCreatePrefixedID(t *testing.T) {
	out, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", createRepo(t, "echo FT-1043\n"), "task",
		"create").streams()
	if err != nil || out != "FT-1043\n" {
		t.Errorf("create = %q, %v; want FT-1043", out, err)
	}
}

// No driver, or one declining create, is exit 3: capture's cue to use the
// project's Create adapter. With no driver it comes before any check, so an
// input the driver would refuse still reaches the adapter.
func TestTaskCreateDeclined(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", createRepo(t, script), "task", "create").streams()
		if testkit.ExitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "Create adapter") {
			t.Errorf("%s: err = %v, want exit %d naming the Create adapter", name, err, ui.ExitDeclined)
		}
	}

	for name, args := range map[string][]string{"no title": nil, "bad parent": {"--parent", "not an id"}} {
		doc := "# A\n"
		if args == nil {
			doc = "no title\n"
		}

		_, _, err := runAppStdin(t, strings.NewReader(doc),
			append([]string{"-C", createRepo(t, ""), "task", "create"}, args...)...).streams()
		if testkit.ExitCode(err) != ui.ExitDeclined || !strings.Contains(err.Error(), "Create adapter") {
			t.Errorf("%s: err = %v, want exit %d naming the Create adapter", name, err, ui.ExitDeclined)
		}
	}
}

// A bad title or priority fails before the driver runs.
func TestTaskCreateRefusesBadInput(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		args []string
		want string
	}{
		{"no title\n", nil, "`# <title>`"},
		{"#\n", nil, "`# <title>`"},
		{"# A\n", []string{"--priority", "p1"}, `priority "p1"`},
		{"# A\n", []string{"--set", "nokey"}, `--set "nokey" is not key=value`},
	} {
		repo := createRepo(t, "echo 1\n")

		_, _, err := runAppStdin(t, strings.NewReader(tc.doc),
			append([]string{"-C", repo, "task", "create"}, tc.args...)...).streams()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q %v: err = %v, want %q", tc.doc, tc.args, err, tc.want)
		}

		if _, statErr := os.Stat(filepath.Join(repo, ".f10", "args")); statErr == nil {
			t.Errorf("%q %v: the driver ran", tc.doc, tc.args)
		}
	}
}

// A failed create, or one that prints no id, carries the driver's stderr and
// the warning that the task may exist.
func TestTaskCreateFailureMayExist(t *testing.T) {
	for script, want := range map[string]string{
		"echo 'notion: 504 gateway timeout' >&2\nexit 1\n": "notion: 504 gateway timeout",
		"echo created it\n": "not one task id",
	} {
		_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", createRepo(t, script), "task", "create").streams()
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), hostops.MayExist) {
			t.Errorf("%q: err = %v, want %q and the may-exist warning", script, err, want)
		}
	}
}

// A driver that exits 4 rejected the create before writing, so its error
// carries the driver's stderr and no may-exist warning.
func TestTaskCreateNotWrittenDropsMayExist(t *testing.T) {
	script := "echo '--label \"bug\" is not an option of \"Tags\"' >&2\nexit 4\n"

	_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", createRepo(t, script), "task", "create",
		"--label", "bug").streams()
	if err == nil || !strings.Contains(err.Error(), `is not an option of "Tags"`) ||
		strings.Contains(err.Error(), hostops.MayExist) {
		t.Errorf("err = %v, want the driver's stderr and no may-exist warning", err)
	}
}

// failingReader fails a test that reads it.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read")

	return 0, io.EOF
}

// With no driver, create exits 3 without reading stdin, so a run with
// nothing piped in never waits on a terminal.
func TestTaskCreateNoDriverLeavesStdin(t *testing.T) {
	err := runAppStdin(t, failingReader{t}, "-C", createRepo(t, ""), "task", "create").err
	if testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("err = %v, want exit %d", err, ui.ExitDeclined)
	}
}

// task current names the branch's task, else the session's, and fails
// when neither names one.
func TestTaskCurrent(t *testing.T) {
	for _, tc := range []struct{ name, branch, session, want string }{
		{"branch over session", "DM-5/fix", "DM-9", "DM-5"},
		{"session on main", "main", "DM-9", "DM-9"},
		{"nothing", "main", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(testkit.TracedRepo(t, tc.branch, ""))

			if tc.session != "" {
				testkit.SessionTask(t, tc.session)
			}

			r := runApp(t, "task", "current")
			out, err := r.combined, r.err

			switch {
			case tc.want == "" && (err == nil || !strings.Contains(err.Error(), "no current task")):
				t.Errorf("printed %q, %v; want no current task", out, err)
			case tc.want != "" && (err != nil || strings.TrimSpace(out) != tc.want):
				t.Errorf("got %q, %v; want %s", out, err, tc.want)
			}
		})
	}
}

// hostRepo is createRepo on a GitHub tracker with a github.com origin, and a
// fake gh first on PATH that logs its calls and prints an issue url for a
// create; script, when given, is the driver.
func hostRepo(t *testing.T, script string) (string, string) {
	t.Helper()

	repo := createRepo(t, script)
	md := "---\ntracker:\n  kind: github\n  id: FT-###\n---\n# demo\n"

	if err := os.WriteFile(filepath.Join(repo, ".f10", "instructions", "project.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin",
		"git@github.com:o/r.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}

	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	gh := "#!/bin/sh\necho \"$@\" >> " + calls + "\n" +
		"case \"$1 $2\" in \"issue create\") echo https://github.com/o/r/issues/12 ;; esac\n"

	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return repo, calls
}

// A GitHub tracker creates the issue itself, with no driver or past one
// declining create, and prints the new id.
func TestTaskCreateOnHostIssues(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		repo, calls := hostRepo(t, script)

		out, _, err := runAppStdin(t, strings.NewReader("# Add it\n\nbody\n"), "-C", repo, "task", "create",
			"--label", "draft").streams()
		if err != nil || out != "FT-12\n" {
			t.Errorf("%s: create = %q, %v; want FT-12", name, out, err)
		}

		if got := readFile(t, calls); !strings.Contains(got, "issue create --title Add it --body body --label draft") {
			t.Errorf("%s: gh calls = %q", name, got)
		}
	}
}

// Host issues have no named properties, so --set fails before an issue is
// created, with no driver or past one declining create.
func TestTaskCreateOnHostIssuesRefusesSet(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		repo, calls := hostRepo(t, script)

		_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", repo, "task", "create", "--set",
			"Kind=Bug").streams()
		if err == nil || !strings.Contains(err.Error(), "nothing was created") ||
			strings.Contains(err.Error(), hostops.MayExist) {
			t.Errorf("%s: err = %v, want a refusal saying nothing was created", name, err)
		}

		if got, _ := os.ReadFile(calls); strings.Contains(string(got), "issue create") {
			t.Errorf("%s: an issue was created: %q", name, got)
		}
	}
}

// With no host CLI to reach the issues, create answers exit 3 before stdin,
// so capture falls back to its adapter.
func TestTaskCreateNoHostCLI(t *testing.T) {
	repo, _ := hostRepo(t, "")
	if out, err := exec.Command("git", "-C", repo, "remote", "remove", "origin").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}

	_, _, err := runAppStdin(t, strings.NewReader("# A\n"), "-C", repo, "task", "create").streams()
	if testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("err = %v, want exit %d", err, ui.ExitDeclined)
	}
}
