package task_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
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
	if got := taskcmd.WithImages(context.Background(), plain, dir, d); got != plain {
		t.Errorf("withImages(plain) = %q, want it unchanged", got)
	}

	doc := "# GH-1: shots\n\n![a](" + srv.URL + "/shot.png)\n\n---\n\n**bob**\n\n" + srv.URL + "/gone.png\n"

	got := taskcmd.WithImages(context.Background(), doc, dir, d)
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

	got := taskcmd.WithImages(context.Background(), doc, filepath.Join(blocker, "GH-1"), images.Downloader{})
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

	tg := &target.Target{Dir: t.TempDir(), Probes: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	auth := taskcmd.GithubAuth(context.Background(), tg)
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

// A driver's index wins; a driver declining it falls back to the host's issues.
func TestTaskListURL(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())

	f.Script("driver index", "https://tracker.example.com/board\n")

	if got, err := taskcmd.TaskListURL(t.Context(), tg); err != nil || got != "https://tracker.example.com/board" {
		t.Errorf("driver index = %q, %v", got, err)
	}

	f.Fail("driver index", shell.Result{Code: trackerdriver.ExitUnsupported})
	f.Script("gh repo view --json url", `{"url":"https://github.com/o/r"}`)

	if got, err := taskcmd.TaskListURL(t.Context(), tg); err != nil || got != "https://github.com/o/r/issues" {
		t.Errorf("fallback = %q, %v", got, err)
	}
}

// The printed list goes through settle and rank: a driver's rows come out
// most urgent first with the unknown value warned once on the error writer,
// and host issues come out ranked through the label map.
func TestWriteTaskListRanksAndWarns(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())

	f.Script("driver list", `[{"id":"FT-1","title":"a","priority":"Highest"},{"id":"FT-2","title":"b"},`+
		`{"id":"FT-3","title":"c","priority":"low"},{"id":"FT-4","title":"d","priority":"HIGH"},`+
		`{"id":"FT-5","title":"e","priority":"Highest"}]`)

	var out, warn bytes.Buffer
	if err := taskcmd.WriteTaskList(t.Context(), tg, &out, &warn); err != nil {
		t.Fatal(err)
	}

	var rows []hostops.Row
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
	f.Fail("driver list", shell.Result{Code: trackerdriver.ExitUnsupported})
	f.Script("gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":7,"title":"low","state":"OPEN","labels":[{"name":"P3"}]},`+
			`{"number":8,"title":"none","state":"OPEN","labels":[]},`+
			`{"number":9,"title":"high","state":"OPEN","labels":[{"name":"P1"}]}]`)
	tg.Effective.PriorityLabels = map[string]string{"p1": "high", "p3": "low"}

	out.Reset()
	warn.Reset()

	if err := taskcmd.WriteTaskList(t.Context(), tg, &out, &warn); err != nil {
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

	rows := hostops.SettlePriorities([]hostops.Row{
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
	for _, r := range taskcmd.RankRows(rows) {
		order.WriteString(r.ID)
	}

	if order.String() != "ecfabd" {
		t.Errorf("ranked order = %q, want ecfabd", order.String())
	}

	if len(taskcmd.RankRows(nil)) != 0 {
		t.Error("ranking nothing produced rows")
	}
}
