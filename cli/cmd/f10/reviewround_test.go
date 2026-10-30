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

func writeRound(t *testing.T, repo, name, body string) {
	t.Helper()

	dir := filepath.Join(repo, ".f10", "reviews", "DM-5")
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
	writeRound(t, repo, "1.md", "task: DM-5\nround: 1\nreviewer: local\nverdict: findings\n\n"+
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
	writeRound(t, repo, "1.md", strings.Replace(head, "%s", "1", 1)+
		"## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\nverdict: ask\n")
	writeRound(t, repo, "2.md", strings.Replace(head, "%s", "2", 1)+
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

// roundRepo is prRepo with a DM tracker and .f10 excluded the way a stealth
// project keeps it, checked out on DM-5/fix with one commit of its own.
func roundRepo(t *testing.T) string {
	t.Helper()
	isolateEnv(t)
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	dir := prRepo(t)
	path := filepath.Join(dir, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("---\ntracker:\n  id: DM-###\n---\n# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(".f10/\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "switch", "-q", "-c", "DM-5/fix")
	commitFile(t, dir, "fix.txt")

	return dir
}

// squashOntoMain lands the branch on origin's main as one squash commit,
// then checks the branch out again.
func squashOntoMain(t *testing.T, dir string) {
	t.Helper()

	git(t, dir, "switch", "-q", "main")
	git(t, dir, "merge", "-q", "--squash", "DM-5/fix")
	git(t, dir, "commit", "-q", "-m", "DM-5 squashed")
	git(t, dir, "push", "-q", "origin", "main")
	git(t, dir, "switch", "-q", "DM-5/fix")
}

// hostGH puts a fake gh first on PATH that reports PR #7 in state.
func hostGH(t *testing.T, state string) {
	t.Helper()

	bin := t.TempDir()
	gh := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"\"pr view\") echo '{\"number\":7,\"state\":\"" + state + "\"}' ;;\n" +
		"\"pr list\") echo '[]' ;;\nesac\n"

	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func wantDecline(t *testing.T, out string, err error, want string) {
	t.Helper()

	var code cli.ExitCoder
	if !errors.As(err, &code) || code.ExitCode() != exitDeclined {
		t.Fatalf("err = %v, want exit %d:\n%s", err, exitDeclined, out)
	}

	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

func twoRounds(t *testing.T, repo, extra string) {
	t.Helper()

	head := "task: DM-5\nround: %s\n" + extra + "reviewer: local\nverdict: findings\n\n"
	writeRound(t, repo, "1.md", strings.Replace(head, "%s", "1", 1)+
		"## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\nverdict: fix\n")
	writeRound(t, repo, "2.md", strings.Replace(head, "%s", "2", 1)+
		"## Prior round\n\n### 1.1 Missing check - fixed\ntested now\nwhere: a.go:1\n")
}

// A squash merge leaves the branch's commits off main, but its content is
// there, so there is nothing to review.
func TestReviewRoundDeclinesAMergedBranch(t *testing.T) {
	repo := roundRepo(t)
	squashOntoMain(t, repo)

	out, err := runRound(t, repo)
	wantDecline(t, out, err, "already merged, nothing to review")
}

// A review before any commit sees the working tree, not HEAD at main's tip.
func TestReviewRoundReviewsUncommittedWork(t *testing.T) {
	repo := roundRepo(t)
	git(t, repo, "reset", "-q", "--hard", "main")

	if err := os.WriteFile(filepath.Join(repo, "main.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := runRound(t, repo); err != nil || !strings.Contains(out, "round:   1") {
		t.Errorf("got %v:\n%s\nwant round 1", err, out)
	}
}

// Two rounds on a merged PR end their series: the next PR on the task starts
// at round 1, and the old rounds are archived, not deleted.
func TestReviewRoundStartsANewSeriesAfterAMergedPR(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	hostGH(t, "MERGED")
	twoRounds(t, repo, "pr: #7\n")

	out, err := runRound(t, repo)
	if err != nil || !strings.Contains(out, "round:   1") {
		t.Fatalf("got %v:\n%s\nwant round 1", err, out)
	}

	archived := filepath.Join(repo, ".f10", "reviews", "archive", "DM-5.1")
	for _, name := range []string{"1.md", "2.md"} {
		if _, err := os.Stat(filepath.Join(archived, name)); err != nil {
			t.Errorf("%s not archived: %v\n%s", name, err, out)
		}
	}

	if !strings.Contains(out, "archive:") {
		t.Errorf("output names no archive:\n%s", out)
	}
}

// A round with no pr: line belongs to the commits it reviewed: one HEAD
// cannot reach is a finished series.
func TestReviewRoundStartsANewSeriesPastAnUnreachableSha(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	twoRounds(t, repo, "reviewed: 0123456789abcdef0123456789abcdef01234567\n")

	if out, err := runRound(t, repo); err != nil || !strings.Contains(out, "round:   1") {
		t.Errorf("got %v:\n%s\nwant round 1", err, out)
	}
}

// A rebase keeps the branch's open PR, so the series and its limit stand.
func TestReviewRoundKeepsTheSeriesOfAnOpenPR(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	hostGH(t, "OPEN")
	twoRounds(t, repo, "reviewed: 0123456789abcdef0123456789abcdef01234567\npr: #7\n")

	out, err := runRound(t, repo)
	wantDecline(t, out, err, "round 2 is the last")
}

// A merge closes a series with no rounds too, since the archive counts the
// task's PRs; finishing the same PR again closes nothing more.
func TestCloseSeriesCountsEveryPR(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviews", "DM-5")
	first := seriesEnd{pr: "#7", head: "abc1234"}

	if got := changeOf(dir); got != "DM-5" {
		t.Fatalf("no series closed: %q", got)
	}

	for range 2 {
		if _, err := closeSeries(dir, first); err != nil {
			t.Fatal(err)
		}
	}

	if got := changeOf(dir); got != "DM-5.2" {
		t.Errorf("after one PR, twice finished: %q, want DM-5.2", got)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := closeSeries(dir, seriesEnd{pr: "!8", head: "def5678"})
	if err != nil || got != filepath.Join(root, "reviews", "archive", "DM-5.2") {
		t.Fatalf("a second PR's rounds: %q, %v", got, err)
	}

	if e := readSeriesEnd(got); e.pr != "!8" || e.head != "def5678" || e.at.IsZero() {
		t.Errorf("closed file reads %+v", e)
	}

	if _, err := closeSeries(dir, seriesEnd{pr: "#8"}); err != nil || changeOf(dir) != "DM-5.3" {
		t.Errorf("glab's !8 and gh's #8 are one PR: %q, %v", changeOf(dir), err)
	}
}

// Each finished series takes the next number; a task with no findings
// archives nothing.
func TestArchiveReviewsNumbersEachSeries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviews", "DM-5")

	if got, err := archiveReviews(dir, seriesEnd{}); got != "" || err != nil {
		t.Fatalf("no directory: got %q, %v", got, err)
	}

	for _, want := range []string{"DM-5.1", "DM-5.2"} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}

		got, err := archiveReviews(dir, seriesEnd{})
		if err != nil || got != filepath.Join(root, "reviews", "archive", want) {
			t.Errorf("got %q, %v; want archive/%s", got, err, want)
		}
	}
}

// The coherence lens reviews on a fresh branch cut from main: nothing past
// main yet is not merged, so it still gets round 1.
func TestReviewRoundGivesAFreshBranchRoundOne(t *testing.T) {
	repo := roundRepo(t)
	git(t, repo, "reset", "-q", "--hard", "main")

	if out, err := runRound(t, repo); err != nil || !strings.Contains(out, "round:   1") {
		t.Errorf("got %v:\n%s\nwant round 1", err, out)
	}
}
