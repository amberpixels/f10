package task_test

import (
	"strings"
	"testing"
	"time"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Only a proceed on the task itself, written within stale_after, clears a
// stale task; the newest one is named, whichever root holds it.
func TestStaleness(t *testing.T) {
	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour)
	day := 24 * time.Hour

	if got := taskcmd.Staleness(old, testkit.StaleOf(""), now, nil, "FT-1"); got != taskcmd.StaleFresh {
		t.Errorf("undeclared = %q", got)
	}

	if got := taskcmd.Staleness(time.Time{}, testkit.StaleOf("3w"), now, nil, "FT-1"); got != taskcmd.StaleUndated {
		t.Errorf("no date = %q", got)
	}

	if got := taskcmd.Staleness(now.Add(-day), testkit.StaleOf("3w"), now, nil, "FT-1"); got != taskcmd.StaleFresh {
		t.Errorf("young = %q", got)
	}

	root, main := t.TempDir(), t.TempDir()

	testkit.WriteVerdict(t, root, "FT-1", "1", "task FT-1", "rethink", now.Add(-day))
	testkit.WriteVerdict(t, root, "FT-1", "2", "plan FT-1", "proceed", now.Add(-day))
	testkit.WriteVerdict(t, root, "FT-1", "3", "task FT-1", "proceed", now.Add(-30*day))

	if got := taskcmd.Staleness(old, testkit.StaleOf("3w"), now, []string{root}, "FT-1"); got != taskcmd.StaleNeeds {
		t.Errorf("no recent task proceed = %q", got)
	}

	older := testkit.WriteVerdict(t, root, "FT-1", "4", "task FT-1", "proceed", now.Add(-5*day))
	newer := testkit.WriteVerdict(t, main, "FT-1", "1", "task FT-1", "proceed", now.Add(-2*day))

	if got := taskcmd.Staleness(
		old,
		testkit.StaleOf("3w"),
		now,
		[]string{root},
		"FT-1",
	); got != taskcmd.StaleJudged+" "+older {
		t.Errorf("recent proceed = %q, want %s", got, older)
	}

	if got := taskcmd.Staleness(
		old,
		testkit.StaleOf("3w"),
		now,
		[]string{root, main},
		"FT-1",
	); got != taskcmd.StaleJudged+" "+newer {
		t.Errorf("newest across roots = %q, want %s", got, newer)
	}
}

// A bare call asks the host's issues for the date, a driver beside them
// included; a failing host is the plain error, never a refetch hint.
func TestTaskStaleCreatedAt(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh issue view 14 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"createdAt":"2026-09-20T23:30:00Z","url":"https://github.com/o/r/issues/14"}`)
	f.Fail(
		"gh issue view 15 --json number,title,state,labels,createdAt,url,body,assignees",
		shell.Result{Code: 1, Stderr: "rate limited"},
	)

	tg := testkit.ChildTarget(t, "gh")

	at, err := taskcmd.CreatedAtFor(t.Context(), tg, ref.Ref{ID: "FT-14", Number: "14"}, "")
	if err != nil || at.Format(time.DateOnly) != "2026-09-20" {
		t.Errorf("host date = %v, %v", at, err)
	}

	_, err = taskcmd.CreatedAtFor(t.Context(), tg, ref.Ref{ID: "FT-15", Number: "15"}, "")
	if err == nil || !strings.Contains(err.Error(), "rate limited") || strings.Contains(err.Error(), "fetch adapter") {
		t.Errorf("failing host = %v, want gh's error alone", err)
	}

	at, err = taskcmd.CreatedAtFor(t.Context(), tg, ref.Ref{ID: "FT-16", Number: "16"}, "2020-01-02")
	if err != nil || at.Format(time.DateOnly) != "2020-01-02" {
		t.Errorf("--created = %v, %v; want no host call", at, err)
	}

	if _, err := taskcmd.CreatedAtFor(t.Context(), tg, ref.Ref{ID: "FT-16", Number: "16"}, "2020-13-01"); err == nil ||
		!strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("bad --created = %v", err)
	}

	tg.Effective.TrackerKind = facts.TrackerJira
	tg.Effective.Facts = append(tg.Effective.Facts, facts.Fact{Key: "tracker.kind", Origin: facts.DeclaredIn("main")})

	if at, err := taskcmd.CreatedAtFor(
		t.Context(),
		tg,
		ref.Ref{ID: "FT-14", Number: "14"},
		"",
	); err != nil ||
		!at.IsZero() {
		t.Errorf("non-host tracker = %v, %v; want undated", at, err)
	}
}
