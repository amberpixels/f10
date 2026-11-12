package task_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

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
		if got := taskcmd.RefLine(tc.token, tc.id, tc.k, tc.md); got != tc.want {
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
		"After: " + taskcmd.MdLink("FT-109", "Fix [x]: y", "https://u") + "\n":     "FT-109",
		"After: FT-109 and more\n":                                                 "",
		"After: [FT-109: t](https://u) and more\n":                                 "",
		"After: [FT-109 x](https://u)":                                             "",
		"After: [FT-109: no link]":                                                 "",
	} {
		if got := hostops.AfterIn(body); got != want {
			t.Errorf("afterIn(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestLinkAfter(t *testing.T) {
	body := "Text naming After: FT-1 in prose.\n  After: FT-2\nAfter: FT-109\n\nend\n"

	got := stack.LinkAfter(body, taskcmd.MdLink("FT-109", "Base", "https://u/109"))
	if want := "Text naming After: FT-1 in prose.\n  After: FT-2\nAfter: [FT-109: Base](https://u/109)\n\nend\n"; got != want {
		t.Errorf("linkAfter = %q, want %q", got, want)
	}

	if hostops.AfterIn(got) != "FT-109" {
		t.Errorf("the linked body reads After: %q", hostops.AfterIn(got))
	}
}

// A task read once answers from the ledger with no tracker call; a miss asks
// the tracker once and keeps the answer; --fresh asks again; one the tracker
// does not know is left out.
func TestTaskRefsLedgerThenTracker(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	seam.Swap(t, &target.ProjectOf, func(context.Context, string) string { return "github.com/o/r" })

	f.Script("gh issue view 109 --json number,title,state,labels,createdAt,url,body,assignees,comments",
		`{"number":109,"title":"Base task","state":"OPEN","url":"https://github.com/o/r/issues/109","body":"b"}`)

	if _, err := taskcmd.ReadDoc(t.Context(), tg, ref.Ref{ID: "FT-109", Number: "109"}); err != nil {
		t.Fatal(err)
	}

	f.Script(
		"gh issue view 132 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Second","url":"https://github.com/o/r/issues/132"}`,
	)
	f.Fail(
		"gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees",
		shell.Result{Code: 1, Stderr: "no issue 7"},
	)

	refs := []ref.Ref{{ID: "FT-109", Number: "109"}, {ID: "FT-132", Number: "132"}, {ID: "FT-7", Number: "7"}}

	var warn strings.Builder

	got := statusview.TaskRefsFor(tg, t.Context(), refs, true, false, &warn)
	if got["FT-109"].Title != "Base task" || got["FT-132"].URL != "https://github.com/o/r/issues/132" {
		t.Errorf("refs = %+v", got)
	}

	if _, ok := got["FT-7"]; ok || !strings.Contains(warn.String(), "FT-7") {
		t.Errorf("unknown FT-7: refs %+v, warn %q", got, warn.String())
	}

	f.Calls = nil

	got = statusview.TaskRefsFor(tg, t.Context(), refs[:2], true, false, &warn)
	if len(f.Calls) != 0 || got["FT-132"].Title != "Second" {
		t.Errorf("second lookup called %v, refs %+v", f.Calls, got)
	}

	f.Script(
		"gh issue view 109 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Renamed","url":"https://github.com/o/r/issues/109"}`,
	)

	if got := statusview.TaskRefsFor(tg, t.Context(), refs[:1], false, true, &warn); got["FT-109"].Title != "Renamed" {
		t.Errorf("fresh = %+v", got)
	}

	if k := ledger.LookupRefs(t.Context(), "github.com/o/r", []string{"FT-109"})["FT-109"]; k.Title != "Renamed" {
		t.Errorf("fresh did not rewrite the row: %+v", k)
	}
}
