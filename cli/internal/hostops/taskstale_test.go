package hostops_test

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestStaleAt(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		age        time.Duration
		staleAfter string
		want       bool
	}{
		{"under 10d", 10*24*time.Hour - time.Minute, "10d", false},
		{"over 10d", 10*24*time.Hour + time.Minute, "10d", true},
		{"under 3w", 20 * 24 * time.Hour, "3w", false},
		{"over 3w", 22 * 24 * time.Hour, "3w", true},
		{"undeclared", 1000 * 24 * time.Hour, "", false},
	} {
		if got := hostops.StaleAt(now.Add(-tc.age), testkit.StaleOf(tc.staleAfter), now); got != tc.want {
			t.Errorf("%s: staleAt = %v, want %v", tc.name, got, tc.want)
		}
	}

	if hostops.StaleAt(time.Time{}, testkit.StaleOf("10d"), now) {
		t.Error("no created date read as stale")
	}
}

func TestHostIssueCreated(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh issue view 14 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"createdAt":"2026-09-20T23:30:00-02:00","url":"https://github.com/o/r/issues/14"}`)
	f.Script("glab issue view 7 -F json", `{"iid":7,"created_at":"2026-10-01T08:00:00Z","web_url":"https://x"}`)
	f.Script(
		"gh issue view 4 --json number,title,state,labels,createdAt,url,body,assignees",
		`{"url":"https://github.com/o/r/pull/4"}`,
	)

	at, err := hostops.NewHost("gh", "/code/repo").IssueCreated(t.Context(), "14")
	if err != nil || at.UTC().Format(time.DateOnly) != "2026-09-21" {
		t.Errorf("gh created = %v, %v", at, err)
	}

	at, err = hostops.NewHost("glab", "/code/repo").IssueCreated(t.Context(), "7")
	if err != nil || at.UTC().Format(time.DateOnly) != "2026-10-01" {
		t.Errorf("glab created = %v, %v", at, err)
	}

	if _, err := hostops.NewHost("gh", "/code/repo").IssueCreated(t.Context(), "4"); err == nil {
		t.Error("a pull request answered for an issue")
	}
}
