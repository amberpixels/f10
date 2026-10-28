package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A task's images come after it as local paths, a failed one by its url and
// reason; a task with none prints as it was.
func TestWithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shot.png" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "images", "GH-1")
	d := images.Downloader{Client: srv.Client()}

	plain := "# GH-1: no images\n\nbody\n"
	if got := withImages(context.Background(), plain, dir, d); got != plain {
		t.Errorf("withImages(plain) = %q, want it unchanged", got)
	}

	doc := "# GH-1: shots\n\n![a](" + srv.URL + "/shot.png)\n\n---\n\n**bob**\n\n" + srv.URL + "/gone.png\n"

	got := withImages(context.Background(), doc, dir, d)
	want := strings.TrimRight(doc, "\n") + "\n\n" +
		"Images: " + filepath.Join(dir, "1.png") + " - read each before summarising\n" +
		"Images not downloaded: " + srv.URL + "/gone.png (HTTP 404)\n"
	if got != want {
		t.Errorf("withImages = %q, want %q", got, want)
	}
}

// A dir that cannot be written fails every image and still prints the task.
func TestWithImagesUnwritableDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "images")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	doc := "# GH-1\n\n![a](https://x.io/a.png)\n"

	got := withImages(context.Background(), doc, filepath.Join(blocker, "GH-1"), images.Downloader{})
	if !strings.HasPrefix(got, doc) || !strings.Contains(got, "Images not downloaded: https://x.io/a.png (") {
		t.Errorf("withImages = %q, want the doc and the image named as failed", got)
	}
}

// The gh token goes to GitHub's own hosts over https and nowhere else.
func TestGithubAuthScopesTheToken(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho tok\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin)

	tg := &target{dir: t.TempDir(), pb: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	auth := githubAuth(context.Background(), tg)
	if auth == nil {
		t.Fatal("githubAuth = nil on a gh host")
	}

	for raw, want := range map[string]string{
		"https://github.com/user-attachments/assets/u1":           "Bearer tok",
		"https://private-user-images.githubusercontent.com/1.png": "Bearer tok",
		"http://github.com/a.png":                                 "",
		"https://notgithub.com/a.png":                             "",
		"https://s3.amazonaws.com/a.png":                          "",
	} {
		r := httptest.NewRequest(http.MethodGet, raw, http.NoBody)
		auth(r)

		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("%s: Authorization = %q, want %q", raw, got, want)
		}
	}
}

// The list pages hang off the repo url each CLI reports, in its own spelling.
func TestListURLs(t *testing.T) {
	f := newFakes(t)

	f.script("gh repo view --json url", `{"url":"https://github.com/o/r"}`)
	f.script("glab repo view -F json", `{"web_url":"https://gitlab.example.com/g/r/"}`)

	for _, tc := range []struct {
		cli, issues, pulls string
	}{
		{"gh", "https://github.com/o/r/issues", "https://github.com/o/r/pulls"},
		{"glab", "https://gitlab.example.com/g/r/-/issues", "https://gitlab.example.com/g/r/-/merge_requests"},
	} {
		h := newHost(tc.cli, "")

		if got, err := h.issuesURL(t.Context()); err != nil || got != tc.issues {
			t.Errorf("%s issues = %q, %v; want %q", tc.cli, got, err, tc.issues)
		}

		if got, err := h.pullsURL(t.Context()); err != nil || got != tc.pulls {
			t.Errorf("%s pulls = %q, %v; want %q", tc.cli, got, err, tc.pulls)
		}
	}
}

// A driver's index wins; a driver declining it falls back to the host's issues.
func TestTaskListURL(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")
	writeDriver(t, tg.lay.StorageRoot)

	f.script("driver index", "https://tracker.example.com/board\n")

	if got, err := taskListURL(t.Context(), tg); err != nil || got != "https://tracker.example.com/board" {
		t.Errorf("driver index = %q, %v", got, err)
	}

	f.fail("driver index", shell.Result{Code: driver.ExitUnsupported})
	f.script("gh repo view --json url", `{"url":"https://github.com/o/r"}`)

	if got, err := taskListURL(t.Context(), tg); err != nil || got != "https://github.com/o/r/issues" {
		t.Errorf("fallback = %q, %v", got, err)
	}
}

// The open-task list: a driver's list wins, its priority carried; a driver
// declining it falls back to the host's open issues, ids in the project's
// format and priorities read from labels through the declared map.
func TestTaskRows(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")
	writeDriver(t, tg.lay.StorageRoot)

	f.script(
		"driver list",
		`[{"id":"FT-9","title":"from the driver","status":"Todo","url":"https://t/9","priority":"high",`+
			`"body":"Some text.\n\nAfter: 12\n"}]`,
	)

	rows, err := taskRows(t.Context(), tg, issuePage)
	if err != nil || len(rows) != 1 || rows[0].ID != "FT-9" || rows[0].Priority != "high" {
		t.Errorf("driver list = %+v, %v", rows, err)
	}

	// the body is read for its After: line, a bare number in the project's
	// format, and never printed
	if tasks := boardTasks(rows, "FT"); len(tasks) != 1 || tasks[0].After != "FT-12" {
		t.Errorf("board tasks = %+v, want After FT-12", tasks)
	}

	var printed bytes.Buffer
	if err := writeRows(&printed, rows, "none"); err != nil || strings.Contains(printed.String(), "body") {
		t.Errorf("printed rows = %q, %v; want no body", printed.String(), err)
	}

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.script(
		"gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":7,"title":"host issue","state":"OPEN","url":"https://github.com/o/r/issues/7","labels":[{"name":"P3"}]},`+
			`{"number":8,"title":"unlabelled","state":"OPEN","url":"https://github.com/o/r/issues/8","labels":[]},`+
			`{"number":9,"title":"two labels","state":"OPEN","url":"https://github.com/o/r/issues/9","labels":[{"name":"p1"},{"name":"P3"}]}]`,
	)

	rows, err = taskRows(t.Context(), tg, issuePage)
	if err != nil || len(rows) != 3 || rows[0].ID != "FT-7" || rows[0].Status != "OPEN" || rows[0].Priority != "" {
		t.Errorf("fallback without a map = %+v, %v", rows, err)
	}

	tg.eff.PriorityLabels = map[string]string{"p1": "high", "p3": "low"}

	rows, err = taskRows(t.Context(), tg, issuePage)
	if err != nil {
		t.Fatal(err)
	}

	rows = rankRows(rows)

	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.ID+":"+r.Priority)
	}

	if want := "FT-9:high FT-7:low FT-8:"; strings.Join(got, " ") != want {
		t.Errorf("ranked rows = %q, want %q", strings.Join(got, " "), want)
	}
}

// The printed list goes through settle and rank: a driver's rows come out
// most urgent first with the unknown value warned once on the error writer,
// and host issues come out ranked through the label map.
func TestWriteTaskListRanksAndWarns(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")
	writeDriver(t, tg.lay.StorageRoot)

	f.script("driver list", `[{"id":"FT-1","title":"a","priority":"Highest"},{"id":"FT-2","title":"b"},`+
		`{"id":"FT-3","title":"c","priority":"low"},{"id":"FT-4","title":"d","priority":"HIGH"},`+
		`{"id":"FT-5","title":"e","priority":"Highest"}]`)

	var out, warn bytes.Buffer
	if err := writeTaskList(t.Context(), tg, &out, &warn); err != nil {
		t.Fatal(err)
	}

	var rows []row
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.ID+":"+r.Priority)
	}

	if want := "FT-4:high FT-3:low FT-1: FT-2: FT-5:"; strings.Join(got, " ") != want {
		t.Errorf("printed order = %q, want %q", strings.Join(got, " "), want)
	}

	if want := "f10: priority \"Highest\" on 2 row(s) is not one of urgent, high, medium, low; ignored\n"; warn.String() != want {
		t.Errorf("warning = %q, want %q", warn.String(), want)
	}

	// the host path, through the declared map
	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.script("gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":7,"title":"low","state":"OPEN","labels":[{"name":"P3"}]},`+
			`{"number":8,"title":"none","state":"OPEN","labels":[]},`+
			`{"number":9,"title":"high","state":"OPEN","labels":[{"name":"P1"}]}]`)
	tg.eff.PriorityLabels = map[string]string{"p1": "high", "p3": "low"}

	out.Reset()
	warn.Reset()

	if err := writeTaskList(t.Context(), tg, &out, &warn); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}

	if len(rows) != 3 || rows[0].ID != "FT-9" || rows[1].ID != "FT-7" || rows[2].ID != "FT-8" || warn.Len() != 0 {
		t.Errorf("host rows = %+v, warn %q", rows, warn.String())
	}
}

// A driver's priority outside the vocabulary is dropped with one warning per
// value, not per row; a known one is kept in its canonical form. Ranking is
// stable, unranked rows last.
func TestSettleAndRankPriorities(t *testing.T) {
	var warn bytes.Buffer

	rows := settlePriorities([]row{
		{ID: "a"},
		{ID: "b", Priority: "Highest"},
		{ID: "c", Priority: " LOW "},
		{ID: "d", Priority: "Highest"},
		{ID: "e", Priority: "urgent"},
		{ID: "f", Priority: "low"},
	}, &warn)

	if want := "f10: priority \"Highest\" on 2 row(s) is not one of urgent, high, medium, low; ignored\n"; warn.String() != want {
		t.Errorf("warning = %q, want %q", warn.String(), want)
	}

	if rows[1].Priority != "" || rows[2].Priority != "low" || rows[3].Priority != "" {
		t.Errorf("settled = %+v", rows)
	}

	var order strings.Builder
	for _, r := range rankRows(rows) {
		order.WriteString(r.ID)
	}

	if order.String() != "ecfabd" {
		t.Errorf("ranked order = %q, want ecfabd", order.String())
	}

	if len(rankRows(nil)) != 0 {
		t.Error("ranking nothing produced rows")
	}
}

// Into a pipe the rows are JSON, the priority present only where a row has
// one, so a project with nothing declared sees today's bytes.
func TestWriteRowsJSONPriority(t *testing.T) {
	var out bytes.Buffer
	if err := writeRows(
		&out,
		[]row{{ID: "FT-1", Title: "a", Priority: "high"}, {ID: "FT-2", Title: "b"}},
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
	f := newFakes(t)

	f.script("gh pr list --json number,title,state,url --limit 30",
		`[{"number":113,"title":"bundle","state":"OPEN","url":"https://github.com/o/r/pull/113"}]`)
	f.script("glab mr list -F json --per-page 30",
		`[{"iid":4,"title":"fix","state":"opened","web_url":"https://gitlab.example.com/g/r/-/merge_requests/4"}]`)

	for _, tc := range []struct{ cli, id, url string }{
		{"gh", "#113", "https://github.com/o/r/pull/113"},
		{"glab", "!4", "https://gitlab.example.com/g/r/-/merge_requests/4"},
	} {
		rows, err := newHost(tc.cli, "").pullRows(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != tc.id || rows[0].Status != pullOpen || rows[0].URL != tc.url {
			t.Errorf("%s rows = %+v, %v", tc.cli, rows, err)
		}
	}
}

// createRepo is a checkout declaring FT-### ids, with the driver script at
// its storage root when one is given. The driver records its arguments and
// stdin beside itself, so a test reads what create handed over.
func createRepo(t *testing.T, script string) string {
	t.Helper()

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

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

func runCreate(t *testing.T, repo, doc string, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Reader = strings.NewReader(doc)
	app.Writer, app.ErrWriter = &out, &errOut
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {} // keep the exit code, not the exit

	err := app.Run(t.Context(), append([]string{"f10", "-C", repo, "task", "create"}, args...))

	return out.String(), errOut.String(), err
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

	out, warn, err := runCreate(t, repo, doc, "--priority", "High", "--label", "draft", "--parent", "FT-1040")
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

// Blank lines before the title are dropped, so the driver reads the title
// on the first line as the contract promises.
func TestTaskCreateTitleOnTheFirstLine(t *testing.T) {
	repo := createRepo(t, "echo 1\n")

	if _, _, err := runCreate(t, repo, "\n\n# A\n\nbody\n"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if got := readFile(t, filepath.Join(repo, ".f10", "stdin")); got != "# A\n\nbody\n" {
		t.Errorf("driver stdin = %q, want the title first", got)
	}
}

// A prefixed id from the driver passes as it is.
func TestTaskCreatePrefixedID(t *testing.T) {
	out, _, err := runCreate(t, createRepo(t, "echo FT-1043\n"), "# A\n")
	if err != nil || out != "FT-1043\n" {
		t.Errorf("create = %q, %v; want FT-1043", out, err)
	}
}

// No driver, or one declining create, is exit 3: capture's cue to use the
// project's Create adapter. With no driver it comes before any check, so an
// input the driver would refuse still reaches the adapter.
func TestTaskCreateDeclined(t *testing.T) {
	for name, script := range map[string]string{"no driver": "", "declined": "exit 3\n"} {
		_, _, err := runCreate(t, createRepo(t, script), "# A\n")
		if exitCode(err) != exitNoCreate || !strings.Contains(err.Error(), "Create adapter") {
			t.Errorf("%s: err = %v, want exit %d naming the Create adapter", name, err, exitNoCreate)
		}
	}

	for name, args := range map[string][]string{"no title": nil, "bad parent": {"--parent", "not an id"}} {
		doc := "# A\n"
		if args == nil {
			doc = "no title\n"
		}

		_, _, err := runCreate(t, createRepo(t, ""), doc, args...)
		if exitCode(err) != exitNoCreate || !strings.Contains(err.Error(), "Create adapter") {
			t.Errorf("%s: err = %v, want exit %d naming the Create adapter", name, err, exitNoCreate)
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
	} {
		repo := createRepo(t, "echo 1\n")

		_, _, err := runCreate(t, repo, tc.doc, tc.args...)
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
		_, _, err := runCreate(t, createRepo(t, script), "# A\n")
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), mayExist) {
			t.Errorf("%q: err = %v, want %q and the may-exist warning", script, err, want)
		}
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
	app := newApp()
	app.Reader = failingReader{t}
	app.Writer, app.ErrWriter = &bytes.Buffer{}, &bytes.Buffer{}
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {} // keep the exit code, not the exit

	err := app.Run(t.Context(), []string{"f10", "-C", createRepo(t, ""), "task", "create"})
	if exitCode(err) != exitNoCreate {
		t.Errorf("err = %v, want exit %d", err, exitNoCreate)
	}
}

// task current names the branch's task, and fails when nothing names one.
func TestTaskCurrent(t *testing.T) {
	for _, tc := range []struct{ branch, want string }{{"DM-5/fix", "DM-5"}, {"main", ""}} {
		t.Run(tc.branch, func(t *testing.T) {
			t.Chdir(tracedRepo(t, tc.branch, ""))

			var out bytes.Buffer

			app := newApp()
			app.Writer, app.ErrWriter = &out, &out

			err := app.Run(t.Context(), []string{"f10", "task", "current"})

			switch {
			case tc.want == "" && err == nil:
				t.Errorf("printed %q, want an error", out.String())
			case tc.want != "" && (err != nil || strings.TrimSpace(out.String()) != tc.want):
				t.Errorf("got %q, %v; want %s", out.String(), err, tc.want)
			}
		})
	}
}
