package task_test

import (
	"context"
	"io"
	"os"
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
		{"12", "FT-12", ledger.TaskRef{Title: "Done", URL: "https://x/12", Closed: true}, false, "FT-12 (Done) [closed]"},
		{"12", "FT-12", ledger.TaskRef{Title: "Done", URL: "https://x/12", Closed: true}, true, "[FT-12: Done](https://x/12) [closed]"},
		{"12", "FT-12", ledger.TaskRef{Closed: true}, false, "FT-12"},
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

// finishedRefTarget is a target whose ledger holds FT-109's title and a
// finish typed as `109`, the way `f10 finish 109` records it.
func finishedRefTarget(t *testing.T) (*testkit.Fakes, *target.Target) {
	t.Helper()

	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	seam.Swap(t, &target.ProjectOf, func(context.Context, string) string { return "github.com/o/r" })

	ledger.RememberRefs(t.Context(), "github.com/o/r",
		ledger.TaskRef{ID: "FT-109", Title: "Base task", URL: "https://github.com/o/r/issues/109"})

	e := ledger.MustEvent(ledger.TaskFinished, ledger.TaskFinishedPayload{Args: []string{"109"}})
	e.Project = "github.com/o/r"
	ledger.Record(e)

	return f, tg
}

// A finish this machine recorded marks the task closed from the ledger
// alone; a task the tracker reads as closed is marked from that read.
func TestTaskRefsMarkClosed(t *testing.T) {
	f, tg := finishedRefTarget(t)

	got := statusview.TaskRefsFor(tg, t.Context(), []ref.Ref{{ID: "FT-109", Number: "109"}}, true, false, nil)
	if !got["FT-109"].Closed || len(f.Calls) != 0 {
		t.Errorf("finished FT-109 = %+v, tracker calls %v", got["FT-109"], f.Calls)
	}

	f.Script(
		"gh issue view 7 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Gone","state":"CLOSED","url":"https://github.com/o/r/issues/7"}`,
	)
	f.Script(
		"gh issue view 8 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Live","state":"OPEN","url":"https://github.com/o/r/issues/8"}`,
	)

	refs := []ref.Ref{{ID: "FT-7", Number: "7"}, {ID: "FT-8", Number: "8"}}

	got = statusview.TaskRefsFor(tg, t.Context(), refs, true, false, nil)
	if !got["FT-7"].Closed || got["FT-8"].Closed {
		t.Errorf("tracker states: FT-7 %+v, FT-8 %+v", got["FT-7"], got["FT-8"])
	}

	if k := ledger.LookupRefs(t.Context(), "github.com/o/r", []string{"FT-7"})["FT-7"]; k.Title != "Gone" || k.Closed {
		t.Errorf("stored ref = %+v, want the title without a state", k)
	}
}

// A re-planned task is pending work again, whatever its finish says.
func TestTaskRefsReplannedIsOpen(t *testing.T) {
	_, tg := finishedRefTarget(t)

	plans := filepath.Dir(tg.Layout.StorageRoot.Plan("FT-109"))
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(plans, "FT-109.md"), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := statusview.TaskRefsFor(tg, t.Context(), []ref.Ref{{ID: "FT-109", Number: "109"}}, true, false, nil)
	if got["FT-109"].Closed {
		t.Errorf("re-planned FT-109 = %+v, want open", got["FT-109"])
	}
}

// A comment on a closed task is refused before anything is posted. The
// tracker's state wins over a finish, which a reopen never clears; the
// finish decides only when the tracker does not answer.
func TestRefuseClosedComment(t *testing.T) {
	f, tg := finishedRefTarget(t)
	view := func(n string) string {
		return "gh issue view " + n + " --json number,title,state,labels,createdAt,url,body,assignees"
	}

	f.Script(view("109"), `{"number":109,"title":"Base task","state":"CLOSED"}`)

	err := taskcmd.RefuseClosed(t.Context(), tg, ref.Ref{ID: "FT-109", Number: "109"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "FT-109 is closed") || !strings.Contains(err.Error(), "--closed") {
		t.Errorf("refuse closed = %v", err)
	}

	f.Script(view("109"), `{"number":109,"title":"Base task","state":"OPEN"}`)

	if err := taskcmd.RefuseClosed(t.Context(), tg, ref.Ref{ID: "FT-109", Number: "109"}, io.Discard); err != nil {
		t.Errorf("reopened FT-109 refused: %v", err)
	}

	f.Fail(view("109"), shell.Result{Code: 1, Stderr: "offline"})

	if err := taskcmd.RefuseClosed(t.Context(), tg, ref.Ref{ID: "FT-109", Number: "109"}, io.Discard); err == nil {
		t.Error("an unanswered tracker let a finished task through")
	}

	f.Fail(view("8"), shell.Result{Code: 1, Stderr: "offline"})

	if err := taskcmd.RefuseClosed(t.Context(), tg, ref.Ref{ID: "FT-8", Number: "8"}, io.Discard); err != nil {
		t.Errorf("an unanswered tracker refused FT-8, which no finish names: %v", err)
	}
}

// A tracker read in this call beats the finish: a task reopened after it
// prints open under --fresh.
func TestTaskRefsFreshReadBeatsFinish(t *testing.T) {
	f, tg := finishedRefTarget(t)

	f.Script(
		"gh issue view 109 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"title":"Base task","state":"OPEN","url":"https://github.com/o/r/issues/109"}`,
	)

	got := statusview.TaskRefsFor(tg, t.Context(), []ref.Ref{{ID: "FT-109", Number: "109"}}, true, true, nil)
	if got["FT-109"].Closed {
		t.Errorf("reopened FT-109 under --fresh = %+v, want open", got["FT-109"])
	}
}

// A finish row naming no task marks nothing: resolved, its empty token would
// name this checkout's own task.
func TestTaskRefsSkipEmptyFinish(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	seam.Swap(t, &target.ProjectOf, func(context.Context, string) string { return "github.com/o/r" })

	ledger.RememberRefs(t.Context(), "github.com/o/r",
		ledger.TaskRef{ID: "FT-5", Title: "Here", URL: "https://github.com/o/r/issues/5"})

	e := ledger.MustEvent(ledger.TaskFinished, ledger.TaskFinishedPayload{})
	e.Project = "github.com/o/r"
	ledger.Record(e)

	got := statusview.TaskRefsFor(tg, t.Context(), []ref.Ref{{ID: "FT-5", Number: "5"}}, true, false, nil)
	if got["FT-5"].Closed || len(f.Calls) != 0 {
		t.Errorf("FT-5 = %+v, calls %v", got["FT-5"], f.Calls)
	}
}
