package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

func TestProfilesFor(t *testing.T) {
	t.Parallel()

	s := Scenario{Name: "s", Agents: []string{"opus:high", "opus:medium"}}

	got, err := profilesFor(s, "")
	if err != nil || len(got) != 2 || got[0].String() != "claude:opus:high" || got[1].String() != "claude:opus:medium" {
		t.Errorf("listed profiles: %v %v", got, err)
	}

	got, err = profilesFor(s, "sonnet:low")
	if err != nil || len(got) != 1 || got[0].String() != "claude:sonnet:low" {
		t.Errorf("the flag alone: %v %v", got, err)
	}

	got, err = profilesFor(Scenario{Name: "s"}, "")
	if err != nil || len(got) != 1 || !got[0].IsDefault() {
		t.Errorf("none listed is one default run: %v %v", got, err)
	}

	if _, err := profilesFor(s, "xyz:low"); err == nil || !strings.Contains(err.Error(), "names no model") {
		t.Errorf("an unknown family is refused before the spend: %v", err)
	}

	if _, err := profilesFor(s, "opus:huge"); err == nil {
		t.Error("an unknown effort is refused")
	}
}

func TestReportNameAndArgs(t *testing.T) {
	t.Parallel()

	def, _, _ := agentctl.Profile{}.Resolve()
	high, _, _ := agentctl.Profile{Model: "opus", Effort: "high"}.Resolve()

	for _, c := range []struct {
		p      agentctl.Profile
		repeat int
		want   string
	}{
		{def, 0, "feature-small.json"},
		{high, 0, "feature-small.opus-high.json"},
		{def, 3, "feature-small.3.json"},
		{high, 2, "feature-small.opus-high.2.json"},
	} {
		if n := reportName("feature-small", c.p, c.repeat); n != c.want {
			t.Errorf("report %q, want %q", n, c.want)
		}
	}

	if d := resolveDir("/root", "evals/reports/base"); d != "/root/evals/reports/base" {
		t.Errorf("relative out %q", d)
	}

	if d := resolveDir("/root", "/tmp/base"); d != "/tmp/base" {
		t.Errorf("absolute out %q", d)
	}

	o := options{permission: "acceptEdits", budget: "5"}
	args := claudeArgs("/root", o, "/f10:plan T-1", high, "")
	want := []string{
		"-p",
		"--plugin-dir",
		"/root",
		"--output-format",
		"stream-json",
		"--verbose",
		"--permission-mode",
		"acceptEdits",
		"--max-budget-usd",
		"5",
		"--model",
		"opus",
		"--effort",
		"high",
		"/f10:plan T-1",
	}

	if !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}

	args = claudeArgs("/root", o, "p", def, "")
	if slices.Contains(args, "--model") || slices.Contains(args, "--effort") {
		t.Errorf("the default passes no flag: %q", args)
	}
}

func TestRunEnv(t *testing.T) {
	t.Parallel()

	env := runEnv([]string{"HOME=/h", "CLAUDECODE=1", "ANTHROPIC_API_KEY=sk-x", "PATH=/bin"}, "/tmp/x")

	for _, dropped := range []string{"CLAUDECODE=", "ANTHROPIC_API_KEY="} {
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, dropped) }) {
			t.Errorf("%s kept: %q", dropped, env)
		}
	}

	for _, want := range []string{"HOME=/h", "PATH=/bin", "F10_USER_DIR=/tmp/x/user", "F10_LEDGER=/tmp/x/f10.db"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q: %q", want, env)
		}
	}
}

// The seeded history and review rounds are what f10 history reads back.
func TestHistoryAndReviewsSeedTheRun(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "repo")

	if err := prepare(
		ctx,
		filepath.Join("..", "..", "..", "evals", "scenarios", "feature-small", "repo"),
		dir,
	); err != nil {
		t.Fatal(err)
	}

	commits := []HistoryCommit{
		{Message: "one\n\nTask: EV-11", File: "main.go", Append: "// one\n"},
		{Message: "two\n\nTask: EV-12", File: "main.go", Append: "// two\n"},
	}
	if err := applyHistory(ctx, dir, commits); err != nil {
		t.Fatal(err)
	}

	if log := gitOut(ctx, dir, "log", "--format=%(trailers:key=Task,valueonly)"); !strings.Contains(log, "EV-11") ||
		!strings.Contains(log, "EV-12") {
		t.Errorf("log carries no seeded tasks:\n%s", log)
	}

	path := filepath.Join(t.TempDir(), "f10.db")
	if err := seedReviews(
		ctx,
		dir,
		path,
		[]SeedReview{{Task: "EV-11", Rounds: 2, Categories: map[string]int32{"tests and docs": 1}}},
	); err != nil {
		t.Fatal(err)
	}

	events, err := ledger.ReadEvents(ctx, path, ledger.Filter{Task: "EV-11", Kind: ledger.ReviewRoundOpened})
	if err != nil || len(events) != 2 || events[0].Project != ledger.Where(ctx, dir).Project {
		t.Fatalf("seeded %d rows (%v), want two under the run's project", len(events), err)
	}
}

// A set that mixes with reports already in its dir would count stale runs,
// so a run refuses it before any spend.
func TestCheckOutRefusesAMixedSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := []Scenario{{Name: "review"}}

	if err := checkOut(filepath.Join(dir, "x"), options{repeat: 3}, s); err == nil {
		t.Error("-repeat 3 without -out accepted")
	}

	if err := checkOut(dir, options{out: dir, repeat: 3}, s); err != nil {
		t.Errorf("an empty -out refused: %v", err)
	}

	if err := writeReport(filepath.Join(dir, "review.2.json"), Report{Scenario: "review"}); err != nil {
		t.Fatal(err)
	}

	if err := writeReport(filepath.Join(dir, "review-host.json"), Report{Scenario: "review-host"}); err != nil {
		t.Fatal(err)
	}

	if err := checkOut(dir, options{out: dir, repeat: 3}, s); err == nil {
		t.Error("an -out holding reports of the scenario accepted")
	}

	if err := checkOut(dir, options{out: dir}, []Scenario{{Name: "feature"}}); err != nil {
		t.Errorf("another scenario's reports refused: %v", err)
	}

	if err := checkOut(dir, options{repeat: 1}, s); err != nil {
		t.Errorf("the release dir refused: %v", err)
	}
}
