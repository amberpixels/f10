package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		h := host{name: tc.cli}

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
		`[{"id":"FT-9","title":"from the driver","status":"Todo","url":"https://t/9","priority":"high"}]`,
	)

	rows, err := taskRows(t.Context(), tg)
	if err != nil || len(rows) != 1 || rows[0].ID != "FT-9" || rows[0].Priority != "high" {
		t.Errorf("driver list = %+v, %v", rows, err)
	}

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.script(
		"gh issue list --json number,title,state,url,labels --limit 30",
		`[{"number":7,"title":"host issue","state":"OPEN","url":"https://github.com/o/r/issues/7","labels":[{"name":"P3"}]},`+
			`{"number":8,"title":"unlabelled","state":"OPEN","url":"https://github.com/o/r/issues/8","labels":[]},`+
			`{"number":9,"title":"two labels","state":"OPEN","url":"https://github.com/o/r/issues/9","labels":[{"name":"p1"},{"name":"P3"}]}]`,
	)

	rows, err = taskRows(t.Context(), tg)
	if err != nil || len(rows) != 3 || rows[0].ID != "FT-7" || rows[0].Status != "OPEN" || rows[0].Priority != "" {
		t.Errorf("fallback without a map = %+v, %v", rows, err)
	}

	tg.eff.PriorityLabels = map[string]string{"p1": "high", "p3": "low"}

	rows, err = taskRows(t.Context(), tg)
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
		rows, err := host{name: tc.cli}.pullRows(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != tc.id || rows[0].Status != pullOpen || rows[0].URL != tc.url {
			t.Errorf("%s rows = %+v, %v", tc.cli, rows, err)
		}
	}
}
