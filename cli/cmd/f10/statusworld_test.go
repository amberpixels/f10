package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// One parent's children, each in a different place, read end to end as
// `f10 status FT-1 --json` prints them: the guard on the status stage
// vocabulary while its facts move. F10_UPDATE=1 rewrites the golden.
func TestStatusParentWorld(t *testing.T) {
	f := newFakes(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := func(m int) time.Time { return now.Add(time.Duration(m-60) * time.Minute) }

	stateDir := t.TempDir()
	t.Setenv("F10_STATE_DIR", stateDir)
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	tg := childTarget(t, "gh")
	root := tg.Layout.StorageRoot.Path()

	f.script("gh api --paginate repos/{owner}/{repo}/issues/1/sub_issues", `[
		{"number":2,"title":"closed by the tracker","state":"closed","html_url":"https://x/2","body":"","labels":[]},
		{"number":3,"title":"in review","state":"open","html_url":"https://x/3","body":"","labels":[]},
		{"number":4,"title":"merged, branch gone","state":"open","html_url":"https://x/4","body":"","labels":[]},
		{"number":5,"title":"asking","state":"open","html_url":"https://x/5","body":"","labels":[]},
		{"number":6,"title":"driven, after review","state":"open","html_url":"https://x/6","body":"After: #3","labels":[]},
		{"number":7,"title":"after a closed base","state":"open","html_url":"https://x/7","body":"After: #2","labels":[]},
		{"number":8,"title":"halted by drive","state":"open","html_url":"https://x/8","body":"","labels":[]},
		{"number":9,"title":"after review, no drive","state":"open","html_url":"https://x/9","body":"After: #3","labels":[]},
		{"number":10,"title":"planned","state":"open","html_url":"https://x/10","body":"","labels":[]}
	]`)

	// FT-3 is checked out in its own worktree, with a live run in review
	scriptNoBranch(f, "FT-1", "FT-2", "FT-4", "FT-5", "FT-6", "FT-7", "FT-8", "FT-9", "FT-10")
	f.script(refsKey("refs/heads/", "FT-3"), heads("FT-3/review"))
	f.script(refsKey("refs/remotes/origin/", "FT-3"), "")
	f.script(worktrees, porcelain("FT-3/review", "/code/repo.FT-3"))

	writeRun(t, stateDir, &state.Run{
		Session: "s3", Task: "FT-3", Root: "/code/repo.FT-3", Capture: "prior", Plan: "done", Ship: "running",
		Leaf: "review", Pipeline: []string{"implement", "pr", "review"}, At: 3, Final: "review", Updated: at(50),
	})

	// one search reads every child's requests; FT-4's names no id, so the
	// url the ledger kept reads it, and a hit on a longer id is not FT-1's
	f.script(searchKey("FT-2", "FT-7", "FT-3", "FT-6", "FT-9", "FT-4", "FT-5", "FT-8", "FT-10"), `{"data":{
		"h2":{"nodes":[{"number":30,"title":"Review","url":"https://github.com/a/b/pull/30","state":"OPEN","headRefName":"FT-3/review"}]},
		"t5":{"nodes":[{"number":40,"title":"hotfix","url":"https://github.com/a/b/pull/40","state":"MERGED","headRefName":"hotfix"}]},
		"t8":{"nodes":[{"number":41,"title":"FT-100: other","url":"https://github.com/a/b/pull/41","state":"OPEN","headRefName":"FT-100/x"}]}
	}}`)
	f.script("gh pr view https://github.com/a/b/pull/40 --json "+prFieldsArg(),
		`{"number":40,"url":"https://github.com/a/b/pull/40","state":"MERGED","headRefName":"hotfix"}`)

	// the ledger's project is the main worktree's path: the fakes give no remote
	in := func(events ...ledger.Event) []ledger.Event {
		for i := range events {
			events[i].Project = "/code/repo"
		}

		return events
	}

	ledger.Record(in(
		event(
			at(1),
			"FT-4",
			ledger.StateSet,
			ledger.Payload{"capture": "prior", "plan": "done", "ship": "running", "leaf": "pr"},
		),
		event(at(2), "FT-4", ledger.PROpened, ledger.Payload{"url": "https://github.com/a/b/pull/40"}),
		event(at(3), "FT-5", ledger.StateAsk, ledger.Payload{
			"capture": "prior", "plan": "done", "ship": "blocked", "leaf": "judge", "ask": "1. Proceed? [yes | no]",
		}),
		event(at(4), "FT-8", ledger.StateSet, ledger.Payload{
			"capture": "prior", "plan": "done", "ship": "running", "leaf": "implement",
		}),
		event(at(5), "FT-8", ledger.TaskHalted, ledger.Payload{"skill": "ship", "reason": "agent idle"}),
		event(at(6), "FT-10", ledger.StateSet, ledger.Payload{"capture": "prior", "plan": "done", "ship": "upcoming"}),
	)...)

	// a running drive lists FT-6, so it waits on its base
	if err := drives.Create(root, &drives.Record{Tasks: []drives.Task{{ID: "FT-6", After: "FT-3"}}}); err != nil {
		t.Fatal(err)
	}

	lease, err := drives.Acquire(root, drives.DriveKey(1), 1)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(lease.Release)

	f.absent = append(
		f.absent,
		"git branch --show-current",
		"git remote",
		"git rev-parse ",
		"git config ",
		"git log ",
		"git symbolic-ref ",
		"git merge-base ",
	)

	rd, err := statusview.NewStatusReader(tg, now)
	if err != nil {
		t.Fatal(err)
	}

	var out, warn bytes.Buffer

	if _, err := rd.Task(
		t.Context(),
		statusview.StatusView{Out: &out, Warn: &warn, JSON: true},
		"FT-1",
		false,
	); err != nil {
		t.Fatal(err)
	}

	if warn.Len() > 0 {
		t.Errorf("warned: %s", warn.String())
	}

	var rows []statusview.TaskRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("%v:\n%s", err, out.String())
	}

	got, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')
	golden := filepath.Join("testdata", "status", "world.status.json")

	if os.Getenv("F10_UPDATE") != "" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (F10_UPDATE=1 writes it)", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("status differs from %s:\n%s", golden, got)
	}
}

func writeRun(t *testing.T, dir string, r *state.Run) {
	t.Helper()

	if err := r.Save(t.Context(), dir, r.Updated); err != nil {
		t.Fatal(err)
	}
}

// prFieldsArg is the --json list forge reads one request with.
func prFieldsArg() string {
	return "number,title,state,url,body,author,baseRefName,headRefName,headRefOid," +
		"isCrossRepository,mergeStateStatus,mergeCommit,mergedAt,mergedBy,closedAt"
}

// searchKey is the one GraphQL search forge sends for the requests of ids.
func searchKey(ids ...string) string {
	const nodes = "{ nodes { ... on PullRequest { number title url state headRefName createdAt mergedAt closedAt } } }"

	var decl, fields, vars []string

	for i, id := range ids {
		for _, q := range []struct{ alias, search string }{{"t", id}, {"h", "head:" + id}} {
			v := q.alias + strconv.Itoa(i)
			decl = append(decl, "$"+v+": String!")
			fields = append(fields, v+": search(query: $"+v+", type: ISSUE, first: 50) "+nodes)
			vars = append(vars, "-F "+v+"=repo:{owner}/{repo} is:pr "+q.search)
		}
	}

	return "gh api graphql -f query=query(" + strings.Join(decl, ", ") + ") { " + strings.Join(fields, " ") + " } " +
		strings.Join(vars, " ")
}
