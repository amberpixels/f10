package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		if got := staleAt(now.Add(-tc.age), tc.staleAfter, now); got != tc.want {
			t.Errorf("%s: staleAt = %v, want %v", tc.name, got, tc.want)
		}
	}

	if staleAt(time.Time{}, "10d", now) {
		t.Error("no created date read as stale")
	}
}

// writeVerdict puts a verdict file under root, its mtime age before now.
func writeVerdict(t *testing.T, root, task, n, subject, verdict string, at time.Time) string {
	t.Helper()

	path := filepath.Join(root, "verdicts", task, n+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	body := "task: " + task + "\nsubject: " + subject + "\nverdict: " + verdict + "\nreason: r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}

	return path
}

// Only a proceed on the task itself, written within stale_after, clears a
// stale task; the newest one is named, whichever root holds it.
func TestStaleness(t *testing.T) {
	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour)
	day := 24 * time.Hour

	if got := staleness(old, "", now, nil, "FT-1"); got != staleFresh {
		t.Errorf("undeclared = %q", got)
	}

	if got := staleness(time.Time{}, "3w", now, nil, "FT-1"); got != staleUndated {
		t.Errorf("no date = %q", got)
	}

	if got := staleness(now.Add(-day), "3w", now, nil, "FT-1"); got != staleFresh {
		t.Errorf("young = %q", got)
	}

	root, main := t.TempDir(), t.TempDir()

	writeVerdict(t, root, "FT-1", "1", "task FT-1", "rethink", now.Add(-day))
	writeVerdict(t, root, "FT-1", "2", "plan FT-1", "proceed", now.Add(-day))
	writeVerdict(t, root, "FT-1", "3", "task FT-1", "proceed", now.Add(-30*day))

	if got := staleness(old, "3w", now, []string{root}, "FT-1"); got != staleNeeds {
		t.Errorf("no recent task proceed = %q", got)
	}

	older := writeVerdict(t, root, "FT-1", "4", "task FT-1", "proceed", now.Add(-5*day))
	newer := writeVerdict(t, main, "FT-1", "1", "task FT-1", "proceed", now.Add(-2*day))

	if got := staleness(old, "3w", now, []string{root}, "FT-1"); got != staleJudged+" "+older {
		t.Errorf("recent proceed = %q, want %s", got, older)
	}

	if got := staleness(old, "3w", now, []string{root, main}, "FT-1"); got != staleJudged+" "+newer {
		t.Errorf("newest across roots = %q, want %s", got, newer)
	}
}

func TestHostIssueCreated(t *testing.T) {
	f := newFakes(t)
	f.script("gh issue view 14 --json createdAt,url",
		`{"createdAt":"2026-09-20T23:30:00-02:00","url":"https://github.com/o/r/issues/14"}`)
	f.script("glab issue view 7 -F json", `{"iid":7,"created_at":"2026-10-01T08:00:00Z","web_url":"https://x"}`)
	f.script("gh issue view 4 --json createdAt,url", `{"url":"https://github.com/o/r/pull/4"}`)

	at, err := newHost("gh", "/code/repo").issueCreated(t.Context(), "14")
	if err != nil || at.UTC().Format(time.DateOnly) != "2026-09-21" {
		t.Errorf("gh created = %v, %v", at, err)
	}

	at, err = newHost("glab", "/code/repo").issueCreated(t.Context(), "7")
	if err != nil || at.UTC().Format(time.DateOnly) != "2026-10-01" {
		t.Errorf("glab created = %v, %v", at, err)
	}

	if _, err := newHost("gh", "/code/repo").issueCreated(t.Context(), "4"); err == nil {
		t.Error("a pull request answered for an issue")
	}
}

// task stale takes the branch's task and stale_after from project.md, and
// --created stands in for a tracker the host does not answer.
func TestTaskStaleCommand(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	t.Chdir(repo)

	run := func(staleAfter string, args ...string) string {
		t.Helper()

		md := "---\ntracker:\n  id: DM-###\n"
		if staleAfter != "" {
			md += "stale_after: " + staleAfter + "\n"
		}

		path := filepath.Join(repo, ".f10", "instructions", "project.md")
		if err := os.WriteFile(path, []byte(md+"---\n# demo\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer

		app := newApp()
		app.Writer, app.ErrWriter = &out, &out

		if err := app.Run(t.Context(), append([]string{"f10", "task", "stale"}, args...)); err != nil {
			t.Fatalf("task stale %v: %v\n%s", args, err, out.String())
		}

		return strings.TrimSpace(out.String())
	}

	if got := run(""); got != staleFresh {
		t.Errorf("undeclared = %q", got)
	}

	if got := run("3w", "--created", time.Now().UTC().Format(time.DateOnly)); got != staleFresh {
		t.Errorf("created today = %q", got)
	}

	if got := run("3w", "--created", "2020-01-01"); got != staleNeeds {
		t.Errorf("old = %q", got)
	}

	writeVerdict(t, filepath.Join(repo, ".f10"), "DM-5", "1", "task DM-5", "proceed", time.Now())

	if got := run("3w", "--created", "2020-01-01"); !strings.HasPrefix(got, staleJudged+" ") ||
		!strings.HasSuffix(got, filepath.Join("verdicts", "DM-5", "1.md")) {
		t.Errorf("judged = %q", got)
	}
}
