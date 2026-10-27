package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Add the index":           "Add the index",
		"# FT-12: Add the index":  "Add the index",
		"ft-12: Add the index":    "Add the index",
		"FT-12 Add the index":     "Add the index",
		"FT-123: not this id":     "FT-123: not this id",
		"  Spaced: with a colon ": "Spaced: with a colon",
	} {
		if got := cleanTitle("FT-12", in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefLine(t *testing.T) {
	k := ledger.TaskRef{Title: "Fix [the] parser", URL: "https://x/12"}

	for _, tc := range []struct {
		token, id string
		k         ledger.TaskRef
		md        bool
		want      string
	}{
		{"12", "FT-12", k, false, "FT-12 (Fix [the] parser)"},
		{"12", "FT-12", k, true, `[FT-12: Fix \[the\] parser](https://x/12)`},
		{"12", "FT-12", ledger.TaskRef{Title: "No url"}, true, "FT-12"},
		{"12", "FT-12", ledger.TaskRef{}, false, "FT-12"},
		{"A+B", "", k, true, "A+B"},
	} {
		if got := refLine(tc.token, tc.id, tc.k, tc.md); got != tc.want {
			t.Errorf("refLine(%q, md=%v) = %q, want %q", tc.id, tc.md, got, tc.want)
		}
	}
}

// The After: line reads the same id bare or as the link create writes,
// whatever the title holds; anything else after the id is still refused.
func TestAfterInReadsALink(t *testing.T) {
	for body, want := range map[string]string{
		"x\n\nAfter: FT-109\n": "FT-109",
		"After: [FT-109: Base task](https://github.com/o/r/issues/109)\n":          "FT-109",
		"After: [FT-109](https://github.com/o/r/issues/109)\n":                     "FT-109",
		"**After:** [FT-109: a: b \\[c\\] d](https://github.com/o/r/issues/109)\n": "FT-109",
		"After: " + mdLink("FT-109", "Fix [x]: y", "https://u") + "\n":             "FT-109",
		"After: FT-109 and more\n":                                                 "",
		"After: [FT-109 x](https://u)":                                             "",
		"After: [FT-109: no link]":                                                 "",
	} {
		if got := afterIn(body); got != want {
			t.Errorf("afterIn(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestLinkAfter(t *testing.T) {
	body := "Text naming After: FT-1 in prose.\n  After: FT-2\nAfter: FT-109\n\nend\n"

	got := linkAfter(body, mdLink("FT-109", "Base", "https://u/109"))
	if want := "Text naming After: FT-1 in prose.\n  After: FT-2\nAfter: [FT-109: Base](https://u/109)\n\nend\n"; got != want {
		t.Errorf("linkAfter = %q, want %q", got, want)
	}

	if afterIn(got) != "FT-109" {
		t.Errorf("the linked body reads After: %q", afterIn(got))
	}
}

// A task read once answers from the ledger with no tracker call; a miss asks
// the tracker once and keeps the answer; --fresh asks again; one the tracker
// does not know is left out.
func TestTaskRefsLedgerThenTracker(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	projectOf = func(context.Context, string) string { return "github.com/o/r" }

	f.script("gh issue view 109 --json number,title,state,labels,createdAt,url,body,comments",
		`{"number":109,"title":"Base task","state":"OPEN","url":"https://github.com/o/r/issues/109","body":"b"}`)

	if _, err := readDoc(t.Context(), tg, ref.Ref{ID: "FT-109", Number: "109"}); err != nil {
		t.Fatal(err)
	}

	f.script("gh issue view 132 --json title,url", `{"title":"Second","url":"https://github.com/o/r/issues/132"}`)
	f.fail("gh issue view 7 --json title,url", shell.Result{Code: 1, Stderr: "no issue 7"})

	refs := []ref.Ref{{ID: "FT-109", Number: "109"}, {ID: "FT-132", Number: "132"}, {ID: "FT-7", Number: "7"}}

	var warn strings.Builder

	got := tg.taskRefs(t.Context(), refs, true, false, &warn)
	if got["FT-109"].Title != "Base task" || got["FT-132"].URL != "https://github.com/o/r/issues/132" {
		t.Errorf("refs = %+v", got)
	}

	if _, ok := got["FT-7"]; ok || !strings.Contains(warn.String(), "FT-7") {
		t.Errorf("unknown FT-7: refs %+v, warn %q", got, warn.String())
	}

	f.calls = nil

	got = tg.taskRefs(t.Context(), refs[:2], true, false, &warn)
	if len(f.calls) != 0 || got["FT-132"].Title != "Second" {
		t.Errorf("second lookup called %v, refs %+v", f.calls, got)
	}

	f.script("gh issue view 109 --json title,url", `{"title":"Renamed","url":"https://github.com/o/r/issues/109"}`)

	if got := tg.taskRefs(t.Context(), refs[:1], false, true, &warn); got["FT-109"].Title != "Renamed" {
		t.Errorf("fresh = %+v", got)
	}

	if k := ledger.LookupRefs(t.Context(), "github.com/o/r", []string{"FT-109"})["FT-109"]; k.Title != "Renamed" {
		t.Errorf("fresh did not rewrite the row: %+v", k)
	}
}

// Create links the body's After: line to the base task before the driver
// sees it, and still passes the base's number as --after.
func TestTaskCreateLinksTheAfterLine(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, `case "$1" in
read) echo "# FT-1041: Base [task]" ;;
url) echo https://tracker.example/1041 ;;
create) echo 1043 ;;
esac
`)

	out, _, err := runCreate(t, repo, "# Add the index\n\nbody\n\nAfter: FT-1041\n")
	if err != nil || out != "FT-1043\n" {
		t.Fatalf("create = %q, %v", out, err)
	}

	storage := filepath.Join(repo, ".f10")
	if got, want := readFile(t, filepath.Join(storage, "stdin")),
		"# Add the index\n\nbody\n\nAfter: [FT-1041: Base \\[task\\]](https://tracker.example/1041)"; strings.TrimSpace(got) != want {
		t.Errorf("driver stdin = %q, want %q", got, want)
	}

	if got := readFile(t, filepath.Join(storage, "args")); got != "create --after 1041\n" {
		t.Errorf("driver args = %q", got)
	}

	// the new task answers from the ledger without the tracker
	k := ledger.LookupRefs(t.Context(), projectOf(t.Context(), repo), []string{"FT-1043", "FT-1041"})
	if k["FT-1043"].Title != "Add the index" || k["FT-1041"].URL != "https://tracker.example/1041" {
		t.Errorf("ledger refs = %+v", k)
	}
}
