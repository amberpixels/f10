package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// objectKeys is the sorted top-level keys of one JSON object.
func objectKeys(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %v\n%s", err, raw)
	}

	return strings.Join(slices.Sorted(maps.Keys(m)), " ")
}

// `status <id> --json` keeps its keys: a run entry and the fallback, each
// with the shipment object beside the keys it always had.
func TestStatusTaskJSONShape(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix-retry", "")
	git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Chdir(repo)

	other := &state.Run{Session: "other", Task: "DM-7", Root: repo, Capture: "done", Plan: "running", Ship: "pending"}
	if err := other.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	var fb json.RawMessage
	if err := json.Unmarshal([]byte(runApp(t, "status", "--json", "DM-5").must(t)), &fb); err != nil {
		t.Fatal(err)
	}

	if got, want := objectKeys(t, fb), "others root shipment task"; got != want {
		t.Errorf("fallback keys = %q, want %q", got, want)
	}

	own := &state.Run{
		Session: "own", Task: "DM-5", Root: repo, Capture: "prior", Plan: "done", Ship: "running",
		Leaf: "implement", Ask: "1. Keep? [yes | no]",
	}
	if err := own.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	var runs []json.RawMessage
	if err := json.Unmarshal([]byte(runApp(t, "status", "--json", "DM-5").must(t)), &runs); err != nil {
		t.Fatal(err)
	}

	if len(runs) != 1 {
		t.Fatalf("runs = %d, want DM-5's own", len(runs))
	}

	want := "ask capture gates leaf plan root session ship shipment task updated"
	if got := objectKeys(t, runs[0]); got != want {
		t.Errorf("run keys = %q, want %q", got, want)
	}

	var entry struct {
		Shipment shipment `json:"shipment"`
	}
	if err := json.Unmarshal(runs[0], &entry); err != nil {
		t.Fatal(err)
	}

	if entry.Shipment.Task != "DM-5" || entry.Shipment.Branch != "DM-5/fix-retry" {
		t.Errorf("shipment = %+v, want DM-5 on its branch", entry.Shipment)
	}
}

// `task report --children` draws the parent, then its open subtasks nested
// by After:, each with its title and url.
func TestTaskReportChildrenTree(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")

	f.script("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues", `[
		{"number":72,"title":"base","state":"open","html_url":"https://x/72","body":"b","labels":[]},
		{"number":73,"title":"later","state":"open","html_url":"https://x/73","body":"x\nAfter: #72\n","labels":[]},
		{"number":74,"title":"done","state":"closed","html_url":"https://x/74","body":"","labels":[]}
	]`)
	scriptNoBranch(f, "FT-72", "FT-73")

	var out bytes.Buffer

	cmd := &cli.Command{Writer: &out, ErrWriter: &out}
	r := ref.Ref{ID: "FT-71", Number: "71"}

	if err := writeParentTree(t.Context(), cmd, tg, "71", r,
		ledger.TaskRef{ID: "FT-71", Title: "Rate limits", URL: "https://x/71"}); err != nil {
		t.Fatal(err)
	}

	want := "FT-71        Rate limits  ↗ https://x/71\n" +
		"└─ FT-72     base         ↗ https://x/72\n" +
		"   └─ FT-73  later        ↗ https://x/73\n"
	if out.String() != want {
		t.Errorf("tree =\n%s\nwant\n%s", out.String(), want)
	}
}

// A bare status whose session task's run asks a question leaves the turn to
// the model, as an asking launch does.
func TestHookStatusTextReportsTheSessionTasksAsk(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-3", state.Run{
		Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [refactor | stop]",
	})

	own := &state.Run{Session: "sess-1", Task: "DM-3", Root: fx.repo, Capture: "prior", Plan: "done", Ship: "pending"}
	if err := own.Save(fx.stateDir, time.Now()); err != nil {
		t.Fatal(err)
	}

	text, asks := hookStatusText(t.Context(), "sess-1")
	if !asks || !strings.Contains(text, "1. Refactor?") {
		t.Errorf("asks = %v, text:\n%s", asks, text)
	}
}

// `f10 state show` prints the session's run as the status run block does.
func TestStateShowPrintsTheRunBlock(t *testing.T) {
	repo := tracedRepo(t, "main", "")
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")
	t.Chdir(repo)

	r := &state.Run{
		Session: "sess-1", Task: "GH-1", URL: "https://gh/issues/1", Capture: "prior", Plan: "done",
		Ship: "blocked", Leaf: "judge", Note: "stop: x", Ask: "1. Keep? [yes | no]",
	}
	if err := r.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := runApp(t, "state", "show").must(t)

	saved, err := state.Load(state.Path(dir, "sess-1"))
	if err != nil {
		t.Fatal(err)
	}

	var want bytes.Buffer
	runRows(&want, saved, runView{showRoot: true, now: time.Now()})

	// the saved run's updated time differs by the save; compare the rest
	cut := func(s string) string { before, _, _ := strings.Cut(s, "updated"); return before }
	if cut(got) != cut(want.String()) {
		t.Errorf("state show =\n%s\nwant the run block\n%s", got, want.String())
	}
}
