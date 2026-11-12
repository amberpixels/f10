package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	hookcmd "github.com/amberpixels/f10/cli/internal/command/hook"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/testkit"
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
	repo := testkit.TracedRepo(t, "DM-5/fix-retry", "")
	testkit.Git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Chdir(repo)

	other := &state.Run{Session: "other", Task: "DM-7", Root: repo, Capture: "done", Plan: "running", Ship: "pending"}
	if err := other.Save(t.Context(), dir, time.Now()); err != nil {
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
	if err := own.Save(t.Context(), dir, time.Now()); err != nil {
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
		Shipment statusview.Shipment `json:"shipment"`
	}
	if err := json.Unmarshal(runs[0], &entry); err != nil {
		t.Fatal(err)
	}

	if entry.Shipment.Task != "DM-5" || entry.Shipment.Branch != "DM-5/fix-retry" {
		t.Errorf("shipment = %+v, want DM-5 on its branch", entry.Shipment)
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
	if err := own.Save(t.Context(), fx.stateDir, time.Now()); err != nil {
		t.Fatal(err)
	}

	text, asks := hookcmd.HookStatusText(t.Context(), "sess-1", "")
	if !asks || !strings.Contains(text, "1. Refactor?") {
		t.Errorf("asks = %v, text:\n%s", asks, text)
	}
}

// `f10 state show` prints the session's run as the status run block does.
func TestStateShowPrintsTheRunBlock(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "")
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")
	t.Chdir(repo)

	r := &state.Run{
		Session: "sess-1", Task: "GH-1", URL: "https://gh/issues/1", Capture: "prior", Plan: "done",
		Ship: "blocked", Leaf: "judge", Note: "stop: x", Ask: "1. Keep? [yes | no]",
	}
	if err := r.Save(t.Context(), dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := runApp(t, "state", "show").must(t)

	saved, err := state.Load(state.Path(dir, "sess-1"))
	if err != nil {
		t.Fatal(err)
	}

	var want bytes.Buffer
	statusview.RunRows(&want, saved, statusview.RunView{ShowRoot: true, Now: time.Now()})

	// the saved run's updated time differs by the save; compare the rest
	cut := func(s string) string { before, _, _ := strings.Cut(s, "updated"); return before }
	if cut(got) != cut(want.String()) {
		t.Errorf("state show =\n%s\nwant the run block\n%s", got, want.String())
	}
}

// A status for one task id answers from that task's checkout: its block with
// no turn for the model, unless the run asks; a token naming no task passes.
func TestHookStatusTextForATask(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-3", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-4", state.Run{
		Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [refactor | stop]",
	})

	text, pass := hookcmd.HookStatusText(t.Context(), "sess-1", "DM-3")
	if pass || !strings.HasPrefix(text, "task:") || !strings.Contains(text, "DM-3") {
		t.Errorf("DM-3: pass = %v, text:\n%s", pass, text)
	}

	if text, pass := hookcmd.HookStatusText(
		t.Context(),
		"sess-1",
		"DM-4",
	); !pass ||
		!strings.Contains(text, "1. Refactor?") {
		t.Errorf("DM-4: pass = %v, text:\n%s", pass, text)
	}

	if _, pass := hookcmd.HookStatusText(t.Context(), "sess-1", "whatever"); !pass {
		t.Error("a token naming no task was answered")
	}
}
