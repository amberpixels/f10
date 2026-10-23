package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tracedRepo is a checkout on the given branch, its project.md declaring
// DM task ids and the traces switch as asked ("" leaves it undeclared).
func tracedRepo(t *testing.T, branch, traces string) string {
	t.Helper()
	isolateEnv(t)
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	repo := mkRepo(t)
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "checkout", "-q", "-b", branch)

	block := "---\ntracker:\n  id: DM-###\n"
	if traces != "" {
		block += "traces: " + traces + "\n"
	}

	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(block+"---\n# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return repo
}

func runTrace(t *testing.T, repo string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out
	app.ErrWriter = &bytes.Buffer{}
	err := app.Run(context.Background(), append([]string{"f10", "-C", repo, "trace", "trailers"}, args...))

	return out.String(), err
}

func TestTraceTrailers(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		traces string
		args   []string
		want   string
	}{
		{"off prints nothing", "DM-7/thing", "off", []string{"--step", "implement"}, ""},
		{"undeclared is off", "DM-7/thing", "", []string{"--step", "implement"}, ""},
		{"task from the branch", "DM-7/thing", "on", []string{"--step", "implement"}, "Task: DM-7\nStep: implement\n"},
		{
			"a bundle, one line each",
			"DM-7+DM-8/thing",
			"on",
			[]string{"--step", "implement"},
			"Task: DM-7\nTask: DM-8\nStep: implement\n",
		},
		{
			"explicit task wins",
			"DM-7/thing",
			"on",
			[]string{"--step", "judge", "--task", "DM-9"},
			"Task: DM-9\nStep: judge\n",
		},
		{
			"review counts", "DM-7/thing", "on",
			[]string{"--step", "resolve", "--round", "2", "--fixed", "1", "--skipped", "3"},
			"Task: DM-7\nStep: resolve\nReview: round=2 fixed=1 skipped=3\n",
		},
		{"no task on the branch", "main", "on", []string{"--step", "implement"}, "Step: implement\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := tracedRepo(t, c.branch, c.traces)

			got, err := runTrace(t, repo, c.args...)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("printed %q, want %q", got, c.want)
			}
		})
	}
}

func TestTraceTrailersRefuses(t *testing.T) {
	repo := tracedRepo(t, "DM-7/thing", "on")

	if _, err := runTrace(t, repo, "--step", "resolve", "--fixed", "1"); err == nil ||
		!strings.Contains(err.Error(), "go together") {
		t.Errorf("a lone count = %v", err)
	}

	if _, err := runTrace(t, repo, "--step", "Two Words"); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("a bad step = %v", err)
	}

	if _, err := runTrace(t, repo); err == nil {
		t.Error("no --step accepted")
	}
}
