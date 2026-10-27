package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// titledHost is a gh host whose project declares tracker.title.
func titledHost() host {
	h := newHost("gh", "")
	h.titles, h.prefix = "{id}: {title}", "FT"

	return h
}

// Every host read takes the prefix tracker.title wrote off the title, so
// the id shows once.
func TestHostReadsStripTheTitle(t *testing.T) {
	f := newFakes(t)

	f.script("gh issue view 12 --json number,title,state,labels,createdAt,url,body,comments",
		`{"number":12,"title":"FT-12: Drive halts","url":"https://github.com/o/r/issues/12"}`)
	f.script(
		"gh issue view 12 --json title,url",
		`{"title":"FT-12: Drive halts","url":"https://github.com/o/r/issues/12"}`,
	)
	f.script("gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":12,"title":"FT-12: Drive halts"},{"number":13,"title":"Bare one"}]`)

	h := titledHost()

	doc, _, err := h.issueDoc(t.Context(), "FT-12", "12", "")
	if err != nil || !strings.HasPrefix(doc, "# FT-12: Drive halts\n") {
		t.Errorf("issueDoc = %q, %v", doc, err)
	}

	if title, err := h.issueTitle(t.Context(), "12"); err != nil || title != "Drive halts" {
		t.Errorf("issueTitle = %q, %v", title, err)
	}

	rows, err := h.listIssues(t.Context(), "", "FT", nil, issuePage)
	if err != nil || len(rows) != 2 || rows[0].Title != "Drive halts" || rows[1].Title != "Bare one" {
		t.Errorf("listIssues = %+v, %v", rows, err)
	}

	// no template: the title stays as the host holds it
	if title, _ := newHost("gh", "").issueTitle(t.Context(), "12"); title != "FT-12: Drive halts" {
		t.Errorf("issueTitle without a template = %q", title)
	}
}

func TestDriverRowsStripTheTitle(t *testing.T) {
	rows := stripTitles([]row{{ID: "FT-3", Title: "FT-3 - Ship it"}, {ID: "FT-4", Title: "Plain"}}, "{id}: {title}")
	if rows[0].Title != "Ship it" || rows[1].Title != "Plain" {
		t.Errorf("stripTitles = %+v", rows)
	}
}

// An update puts the prefix back on a title renamed by hand, and leaves a
// prefixed title alone.
func TestHostUpdateRestoresThePrefix(t *testing.T) {
	f := newFakes(t)

	f.script("gh issue view 7 --json labels,assignees,title,url",
		`{"url":"https://github.com/o/r/issues/7","title":"Renamed by hand"}`)
	f.script("gh issue edit 7 --title FT-7: Renamed by hand --add-label shipped", "")

	if err := titledHost().update(t.Context(), "7", props{labels: []string{"shipped"}}, nil); err != nil ||
		!f.called("gh issue edit 7 --title FT-7: Renamed by hand --add-label shipped") {
		t.Errorf("update: %v, calls %v", err, f.calls)
	}

	f.script("gh issue view 8 --json labels,assignees,title,url",
		`{"url":"https://github.com/o/r/issues/8","title":"FT-8: Fine"}`)
	f.script("gh issue close 8", "")

	if err := titledHost().update(t.Context(), "8", props{status: "closed"}, nil); err != nil ||
		f.called("gh issue edit 8") {
		t.Errorf("update of a prefixed title edited it: %v, calls %v", err, f.calls)
	}
}

// --all lists open and closed issues and changes only the bare titles, so
// a second run over the result changes nothing.
func TestTitleChangesAll(t *testing.T) {
	f := newFakes(t)

	list := "gh issue list --state all --json number,title --limit 100000"
	f.script(list, `[{"number":1,"title":"FT-1: Done"},{"number":2,"title":"Bare"},{"number":3,"title":"ft-3 Lower"}]`)

	changes, err := titledHost().titleChanges(t.Context(), "")
	if err != nil || len(changes) != 2 {
		t.Fatalf("titleChanges = %+v, %v", changes, err)
	}

	if changes[0].to != "FT-2: Bare" || changes[1].to != "FT-3: Lower" {
		t.Errorf("changes = %+v", changes)
	}

	f.script(
		list,
		`[{"number":1,"title":"FT-1: Done"},{"number":2,"title":"FT-2: Bare"},{"number":3,"title":"FT-3: Lower"}]`,
	)

	if again, err := titledHost().titleChanges(t.Context(), ""); err != nil || len(again) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
}

// retitle needs a template, and the host's issues to write it on.
func TestTaskRetitleRefuses(t *testing.T) {
	_, errOut, err := runWrite(t, createRepo(t, ""), "", "retitle", "FT-12")
	if err != nil || !strings.Contains(errOut, "declares no tracker.title") {
		t.Errorf("no template: err = %v, stderr %q", err, errOut)
	}

	repo := createRepo(t, "")
	md := "---\ntracker:\n  kind: notion\n  id: FT-###\n  title: \"{id}: {title}\"\n---\n# demo\n"

	if err := os.WriteFile(filepath.Join(repo, ".f10", "instructions", "project.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runWrite(t, repo, "", "retitle", "--all"); exitCode(err) != exitNoWrite {
		t.Errorf("notion tracker: err = %v, want exit %d", err, exitNoWrite)
	}
}

func TestOneIDHeading(t *testing.T) {
	r := ref.Ref{ID: "FT-12", Number: "12"}

	for doc, want := range map[string]string{
		"# FT-12: FT-12: Drive\n\nbody": "# FT-12: Drive\n\nbody",
		"# FT-12: Drive\n\nbody":        "# FT-12: Drive\n\nbody",
		"# Drive":                       "# Drive",
		"no heading":                    "no heading",
	} {
		if got := oneIDHeading(doc, r, "{id}: {title}"); got != want {
			t.Errorf("oneIDHeading(%q) = %q, want %q", doc, got, want)
		}
	}
}

// After a driver writes an issue on the host's tracker, its title goes
// through tracker.title; a failed retitle warns, since the write stands.
func TestRetitleAfter(t *testing.T) {
	f := newFakes(t)

	tg := &target{
		dir: t.TempDir(),
		pb:  &probe.Probes{CLI: probe.Signal{Value: "gh"}},
		eff: &facts.Effective{TrackerKind: "GitHub Issues", IDPrefix: "FT", TitleTemplate: "{id}: {title}"},
	}
	r := ref.Ref{ID: "FT-12", Number: "12"}

	f.script(
		"gh issue view 12 --json number,title,url",
		`{"number":12,"title":"Drive","url":"https://github.com/o/r/issues/12"}`,
	)
	f.script("gh issue edit 12 --title FT-12: Drive", "")

	var warn strings.Builder

	retitleAfter(t.Context(), &warn, tg, r, "created")

	if !f.called("gh issue edit 12 --title FT-12: Drive") || warn.Len() != 0 {
		t.Errorf("retitle: calls %v, warning %q", f.calls, warn.String())
	}

	f.fail("gh issue edit 12 --title FT-12: Drive", shell.Result{Code: 1, Stderr: "HTTP 502"})

	retitleAfter(t.Context(), &warn, tg, r, "created")

	if !strings.Contains(warn.String(), "FT-12 was created") ||
		!strings.Contains(warn.String(), "f10 task retitle FT-12") {
		t.Errorf("failed retitle warned %q", warn.String())
	}
}
