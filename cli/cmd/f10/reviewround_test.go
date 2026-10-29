package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/findings"
)

func runRound(t *testing.T, repo string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {} // keep the exit code, not the exit

	t.Chdir(repo)

	err := app.Run(t.Context(), append([]string{"f10", "review", "round"}, args...))

	return out.String(), err
}

func writeRound(t *testing.T, repo, key, name, body string) {
	t.Helper()

	dir := filepath.Join(repo, ".f10", "reviews", key)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRoundNamesTheDirectory(t *testing.T) {
	for _, tc := range []struct{ name, branch, arg, key string }{
		{"the branch's task", "DM-5/fix", "", "DM-5"},
		{"an explicit task", "main", "DM-7", "DM-7"},
		{"no task: the branch, dashed", "spike/try-it", "", "spike-try-it"},
		{"a bundle's rider: the lead", "DM-5/fix", "DM-6", "DM-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := tracedRepo(t, tc.branch, "")
			git(t, repo, "config", "branch.DM-5/fix.f10-tasks", "DM-6")

			var args []string
			if tc.arg != "" {
				args = append(args, tc.arg)
			}

			out, err := runRound(t, repo, append(args, "--json")...)
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}

			var st findings.Status
			if err := json.Unmarshal([]byte(out), &st); err != nil {
				t.Fatal(err)
			}

			if filepath.Base(st.Dir) != tc.key || st.Round != 1 || filepath.Base(st.Path) != "1.md" {
				t.Errorf("got %+v, want round 1 under %s", st, tc.key)
			}
		})
	}
}

// An unsettled round one declines with exit 3, naming the finding.
func TestReviewRoundDeclines(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	writeRound(t, repo, "DM-5", "1.md", "task: DM-5\nround: 1\nreviewer: local\nverdict: findings\n\n"+
		"## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\n")

	out, err := runRound(t, repo)

	var code cli.ExitCoder
	if !errors.As(err, &code) || code.ExitCode() != exitDeclined {
		t.Fatalf("err = %v, want exit %d", err, exitDeclined)
	}

	for _, want := range []string{"latest:", "1.md", "decline:", "1.1 Missing check - no verdict"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "round:") {
		t.Errorf("a decline prints no next round:\n%s", out)
	}
}

// Round two is the last: it declines whether or not anything still waits.
func TestReviewRoundDeclinesAfterRoundTwo(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	head := "task: DM-5\nround: %s\nreviewer: local\nverdict: findings\n\n"
	writeRound(t, repo, "DM-5", "1.md", strings.Replace(head, "%s", "1", 1)+
		"## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\nverdict: ask\n")
	writeRound(t, repo, "DM-5", "2.md", strings.Replace(head, "%s", "2", 1)+
		"## Prior round\n\n### 1.1 Missing check - still open\nno test yet\nwhere: a.go:1\n")

	out, err := runRound(t, repo)

	var code cli.ExitCoder
	if !errors.As(err, &code) || code.ExitCode() != exitDeclined {
		t.Fatalf("err = %v, want exit %d", err, exitDeclined)
	}

	for _, want := range []string{"2.md", "round 2 is the last; these wait", "1.1 Missing check - still open"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
