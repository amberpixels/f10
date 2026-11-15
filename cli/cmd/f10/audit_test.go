package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// a/ had three tasks of three review rounds and two halts, b/ more churn
// at one round each: the pain map lists a/ alone, and the task map maps
// a trailer-less commit by its subject.
func TestAuditMapListsTheCostlyArea(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	t.Chdir(t.TempDir())

	commit := func(file, task string, lines int, msg string) {
		dir := filepath.Join(repo, filepath.Dir(file))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(repo, file), []byte(strings.Repeat(task+"\n", lines)), 0o600); err != nil {
			t.Fatal(err)
		}

		testkit.Git(t, repo, "add", file)
		testkit.Git(t, repo, "commit", "-q", "-m", msg)
	}

	for _, task := range []string{"DM-1", "DM-2", "DM-3"} {
		commit("a/x.go", task, 2, "work\n\nTask: "+task)
	}

	for i, task := range []string{"DM-4", "DM-5", "DM-6", "DM-7"} {
		commit("b/y.go", task, 50+i, "work\n\nTask: "+task)
	}

	commit("b/y.go", "DM-8", 80, "DM-8 no trailer")

	ctx := t.Context()
	project := ledger.Where(ctx, repo).Project
	at := time.Now().Add(-time.Hour)

	var events []ledger.Event

	add := func(task, kind string, p any) {
		e := testkit.StatEvent(at, "s-"+task, task, kind, p)
		e.ID, e.Project = fmt.Sprintf("e%03d", len(events)), project
		events = append(events, e)
	}

	for _, task := range []string{"DM-1", "DM-2", "DM-3"} {
		for r := 1; r <= 3; r++ {
			add(task, ledger.ReviewRoundOpened, &ledger.ReviewRoundOpenedPayload{Round: strconv.Itoa(r)})
		}
	}

	add("DM-1", ledger.TaskHalted, &ledger.TaskHaltedPayload{Skill: "ship", Reason: "catchup conflict"})
	add("DM-2", ledger.TaskHalted, &ledger.TaskHaltedPayload{Skill: "ship", Reason: "catchup conflict"})

	for _, task := range []string{"DM-4", "DM-5", "DM-6", "DM-7", "DM-8"} {
		add(task, ledger.ReviewRoundOpened, &ledger.ReviewRoundOpenedPayload{Round: "1"})
	}

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, events...)
	_ = l.Close()

	out, err := runApp(t, "-C", repo, "audit", "map", "all").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "a: 3.0 review rounds vs 1.0") || strings.Contains(out, "  b: ") {
		t.Errorf("pain map is not a alone:\n%s", out)
	}

	raw, err := runApp(t, "-C", repo, "audit", "map", "all", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var m struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
		Pain struct {
			Areas []struct {
				Dir string `json:"dir"`
			} `json:"areas"`
		} `json:"pain"`
	}

	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}

	if len(m.Pain.Areas) != 1 || m.Pain.Areas[0].Dir != "a" {
		t.Errorf("--json pain areas = %+v, want a alone", m.Pain.Areas)
	}

	if len(m.Tasks) != 8 || m.Tasks[7].ID != "DM-8" {
		t.Errorf("--json tasks = %+v, want DM-1 to DM-8", m.Tasks)
	}
}

// A project's old regression row is not ledger history for this range.
func TestAuditMapWithNoLedger(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	ctx := t.Context()
	e := testkit.StatEvent(time.Now().Add(-time.Hour), "s-old", "XX-1", ledger.RegressionFound,
		&ledger.RegressionFoundPayload{OriginTask: "XX-0", Repro: "full"})
	e.ID, e.Project = "e1", ledger.Where(ctx, repo).Project

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, e)
	_ = l.Close()

	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	testkit.Git(t, repo, "add", "a.go")
	testkit.Git(t, repo, "commit", "-q", "-m", "work\n\nTask: XX-5")

	out, err := runApp(t, "-C", repo, "audit", "map", "all").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "XX-5") || !strings.Contains(out, "pain map\n  No ledger history for this range.") {
		t.Errorf("no ledger:\n%s", out)
	}
}

// A commit off the default branch still maps by its branch, through the
// tail the bounded rev-list finds.
func TestAuditMapOffDefaultCommit(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	base := strings.TrimSpace(testkit.Git(t, repo, "rev-parse", "HEAD"))
	testkit.Git(t, repo, "update-ref", "refs/remotes/origin/main", base)
	testkit.Git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	testkit.Git(t, repo, "checkout", "-q", "-b", "DM-30/side")

	if err := os.WriteFile(filepath.Join(repo, "c.go"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	testkit.Git(t, repo, "add", "c.go")
	testkit.Git(t, repo, "commit", "-q", "-m", "no id here")

	out, err := runApp(t, "-C", repo, "audit", "map", base+"..HEAD").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "DM-30") {
		t.Errorf("the side commit did not map to its branch's task:\n%s", out)
	}
}
