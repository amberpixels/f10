package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/state"
)

// launchFixture is a main checkout whose session sess-1 launched tasks,
// each in a linked worktree on its own branch.
type launchFixture struct {
	repo, stateDir string
}

func newLaunchFixture(t *testing.T) launchFixture {
	t.Helper()

	repo := tracedRepo(t, "main", "")
	git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	fx := launchFixture{repo: repo, stateDir: t.TempDir()}
	t.Setenv("F10_STATE_DIR", fx.stateDir)
	t.Setenv("F10_SESSION_ID", "sess-1")
	t.Chdir(repo)

	return fx
}

// worktree opens id's branch in a sibling worktree, its agent's run
// reporting there.
func (fx launchFixture) worktree(t *testing.T, id string, run state.Run) {
	t.Helper()

	path := filepath.Join(filepath.Dir(fx.repo), "demo."+id)
	git(t, fx.repo, "worktree", "add", "-q", "-b", id+"/work", path)

	run.Session, run.Task, run.Root = "agent-"+id, id, canonPath(path)
	if err := run.Save(fx.stateDir, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func (fx launchFixture) launch(t *testing.T, kind, value string) {
	t.Helper()

	if err := state.AppendLaunch(fx.stateDir, "sess-1", state.Launch{Kind: kind, Value: value}); err != nil {
		t.Fatal(err)
	}
}

// drive writes a drive record over ids and launches it.
func (fx launchFixture) drive(t *testing.T, change func(*drives.Record), ids ...string) {
	t.Helper()

	tg, err := targetAt(t.Context(), fx.repo, false, "status")
	if err != nil {
		t.Fatal(err)
	}

	rec := &drives.Record{Args: ids, Session: "sess-1"}
	for _, id := range ids {
		rec.Tasks = append(rec.Tasks, drives.Task{ID: id})
	}

	if change != nil {
		change(rec)
	}

	if err := drives.Create(tg.sharedRoot(), rec); err != nil {
		t.Fatal(err)
	}

	fx.launch(t, state.LaunchDrive, strconv.Itoa(rec.Number))
}

// status runs a bare `f10 status` and returns stdout.
func (fx launchFixture) status(t *testing.T, args ...string) string {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &errOut

	if err := app.Run(t.Context(), append([]string{"f10", "status"}, args...)); err != nil {
		t.Fatalf("status: %v\n%s%s", err, out.String(), errOut.String())
	}

	return out.String()
}

func (fx launchFixture) rows(t *testing.T) []taskRow {
	t.Helper()

	out := fx.status(t, "--json")

	var rows []taskRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not a row list: %v\n%s", err, out)
	}

	return rows
}

func rowIDs(rows []taskRow) string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID + ":" + r.Stage
	}

	return strings.Join(ids, " ")
}

// Two starts and a drive of two more print one tree, launch order kept,
// an asking task carrying its question.
func TestBareStatusListsTheSessionLaunches(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-2", state.Run{Capture: "prior", Plan: "done", Ship: "pending"})
	fx.worktree(t, "DM-3", state.Run{
		Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [refactor | stop]",
	})

	fx.launch(t, state.LaunchStart, "DM-1")
	fx.launch(t, state.LaunchStart, "DM-2")
	fx.drive(t, nil, "DM-3", "DM-4")

	rows := fx.rows(t)
	if got, want := rowIDs(rows), "DM-1:implementing DM-2:planned DM-3:asking DM-4:not started"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}

	if rows[2].Ask == "" {
		t.Errorf("asking row has no ask: %+v", rows[2])
	}

	if plain := fx.status(
		t,
		"--plain",
	); !strings.HasPrefix(plain, launchesHead) ||
		!strings.Contains(plain, "Refactor?") {
		t.Errorf("plain tree:\n%s", plain)
	}
}

// A drive that is done or cancelled adds none of its tasks; a task both
// started and driven shows once.
func TestBareStatusDropsFinishedDrives(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})

	fx.launch(t, state.LaunchStart, "DM-1")
	fx.drive(t, func(r *drives.Record) {
		for i := range r.Tasks {
			r.Tasks[i].Outcome = &drives.Outcome{Row: "done", Code: 0}
		}
	}, "DM-5")

	now := time.Now()
	fx.drive(t, func(r *drives.Record) { r.Cancelled = &now }, "DM-6")
	fx.drive(t, nil, "DM-1")

	if got, want := rowIDs(fx.rows(t)), "DM-1:implementing"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
}

// The session's own run naming a launched task (planned here, then
// started) still prints the whole tree; one naming another task, such as
// the parent capture split, adds no row: only launches are rows.
func TestBareStatusLaunchesOutrankTheSessionTask(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-2", state.Run{Capture: "prior", Plan: "done", Ship: "pending"})

	own := &state.Run{Session: "sess-1", Task: "DM-1", Root: fx.repo, Capture: "done", Plan: "done", Ship: "pending"}
	if err := own.Save(fx.stateDir, time.Now()); err != nil {
		t.Fatal(err)
	}

	fx.launch(t, state.LaunchStart, "DM-1")
	fx.launch(t, state.LaunchStart, "DM-2")

	if got, want := rowIDs(fx.rows(t)), "DM-1:implementing DM-2:planned"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}

	own.Task = "DM-9"
	if err := own.Save(fx.stateDir, time.Now()); err != nil {
		t.Fatal(err)
	}

	if got, want := rowIDs(fx.rows(t)), "DM-1:implementing DM-2:planned"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
}

// A launch nests under its base as a parent's child does: a driven task
// not started yet waits on the base its drive recorded, a started one
// hangs under the base its branch records.
func TestBareStatusNestsLaunchesUnderTheirBase(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-3", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-5", state.Run{Capture: "prior", Plan: "done", Ship: "pending"})
	git(t, fx.repo, "config", "branch.DM-5/work."+cfgAfter, "DM-3")

	fx.drive(t, func(r *drives.Record) { r.Tasks[1].After = "DM-3" }, "DM-3", "DM-4")
	fx.launch(t, state.LaunchStart, "DM-5")

	rows := fx.rows(t)
	if got, want := rowIDs(rows), "DM-3:implementing DM-4:waiting DM-5:planned"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}

	if rows[1].Step != "after DM-3" || rows[1].Depth != 1 || rows[2].Depth != 1 {
		t.Errorf("not nested under DM-3: %+v", rows)
	}
}

// A started task's branch names its base over what its drive recorded,
// as drive itself reads it.
func TestBareStatusBranchBaseBeatsTheDrives(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-3", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-7", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.worktree(t, "DM-4", state.Run{Capture: "prior", Plan: "done", Ship: "pending"})
	git(t, fx.repo, "config", "branch.DM-4/work."+cfgAfter, "DM-3")

	fx.drive(t, func(r *drives.Record) { r.Tasks[2].After = "DM-7" }, "DM-3", "DM-7", "DM-4")

	rows := fx.rows(t)
	if got, want := rowIDs(rows), "DM-3:implementing DM-4:planned DM-7:implementing"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}

	if rows[1].After != "DM-3" || rows[1].Depth != 1 {
		t.Errorf("DM-4 is not under its branch's base DM-3: %+v", rows[1])
	}
}

// A base that finished holds nothing back: its dependent reads by its own
// run, at the top level.
func TestBareStatusIgnoresAFinishedBase(t *testing.T) {
	fx := newLaunchFixture(t)

	archive := filepath.Join(fx.repo, ".f10", "plans", "archive")
	if err := os.MkdirAll(archive, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(archive, "DM-5.1.md"), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fx.drive(t, func(r *drives.Record) { r.Tasks[0].After = "DM-5" }, "DM-6")

	rows := fx.rows(t)
	if got, want := rowIDs(rows), "DM-6:not started"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}

	if rows[0].After != "" || rows[0].Depth != 0 {
		t.Errorf("a finished base still holds DM-6: %+v", rows[0])
	}
}

// Dropping a row moves what hung under it up to its level.
func TestDropRowsLiftsTheDroppedRowsChildren(t *testing.T) {
	rows := []taskRow{
		{ID: "A", Depth: 0, Stage: stageDone},
		{ID: "B", Depth: 1},
		{ID: "C", Depth: 2, Stage: stageDone},
		{ID: "D", Depth: 3},
		{ID: "E", Depth: 1},
		{ID: "F", Depth: 0},
	}

	got := dropRows(rows, func(r taskRow) bool { return r.Stage == stageDone })

	var b strings.Builder
	for _, r := range got {
		fmt.Fprintf(&b, "%s%d ", r.ID, r.Depth)
	}

	if want := "B0 D1 E0 F0 "; b.String() != want {
		t.Errorf("rows = %q, want %q", b.String(), want)
	}
}

// No launch, or none still live, prints what a bare status printed before.
func TestBareStatusWithNoLiveLaunch(t *testing.T) {
	fx := newLaunchFixture(t)

	if got := fx.status(t); got != "no f10 run in this session\n" {
		t.Errorf("no launches: %q", got)
	}

	now := time.Now()
	fx.drive(t, func(r *drives.Record) { r.Cancelled = &now }, "DM-6")

	if got := fx.status(t); got != "no f10 run in this session\n" {
		t.Errorf("a cancelled drive only: %q", got)
	}
}

// Inside a task's worktree, the branch's task wins over the launches.
func TestBareStatusInATaskWorktreeIgnoresLaunches(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.launch(t, state.LaunchStart, "DM-2")
	t.Chdir(filepath.Join(filepath.Dir(fx.repo), "demo.DM-1"))

	runs := runStatusJSON(t)
	if len(runs) != 1 || runs[0].Task != "DM-1" {
		t.Errorf("bare status in DM-1's worktree = %+v, want DM-1's run", runs)
	}
}

// A start whose branch is gone, or a driven task whose plan finish
// archived, is done with and drops out.
func TestBareStatusDropsFinishedStarts(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})

	archive := filepath.Join(fx.repo, ".f10", "plans", "archive")
	if err := os.MkdirAll(archive, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(archive, "DM-5.1.md"), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fx.launch(t, state.LaunchStart, "DM-1")
	fx.launch(t, state.LaunchStart, "DM-2") // its branch was removed
	fx.drive(t, nil, "DM-5", "DM-6")

	if got, want := rowIDs(fx.rows(t)), "DM-1:implementing DM-6:not started"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
}

// The hook leaves a bare status to the model only when its tree holds a
// question to relay.
func TestHookStatusTextReportsAsks(t *testing.T) {
	fx := newLaunchFixture(t)

	fx.worktree(t, "DM-1", state.Run{Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"})
	fx.launch(t, state.LaunchStart, "DM-1")

	text, asks := hookStatusText(t.Context(), "sess-1")
	if asks || !strings.HasPrefix(text, launchesHead) {
		t.Errorf("no ask: asks = %v, text:\n%s", asks, text)
	}

	// the hook's answer is chat text, where a link escape prints as noise
	if strings.Contains(text, "\x1b]8;") {
		t.Errorf("the hook's tree carries OSC 8 links:\n%q", text)
	}

	fx.worktree(t, "DM-3", state.Run{
		Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [refactor | stop]",
	})
	fx.launch(t, state.LaunchStart, "DM-3")

	if _, asks := hookStatusText(t.Context(), "sess-1"); !asks {
		t.Error("an asking launch did not report an ask")
	}
}

// A task the drive has not opened yet shows the drive's flow, not the
// default one; a bundle's row finds its drive by the lead.
func TestBareStatusUnopenedTaskTakesTheDrivesFlow(t *testing.T) {
	fx := newLaunchFixture(t)

	path := filepath.Join(fx.repo, ".f10", "instructions", "project.md")
	md := "---\ntracker:\n  id: DM-###\nflows:\n  default: plan judge implement pr review:local judge pr\n" +
		"  prose: implement pr\n---\n# demo\n"

	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	fx.drive(t, func(r *drives.Record) { r.Flow = "prose" }, "DM-7", "DM-8+DM-9")

	rows := fx.rows(t)
	if len(rows) != 2 {
		t.Fatalf("rows = %s, want DM-7 and DM-8", rowIDs(rows))
	}

	for _, row := range rows {
		for _, g := range row.Gates {
			if g.Step == "judge" && g.State != gateOff || g.Local != nil {
				t.Errorf("%s: gate %+v is the default flow's, not prose's", row.ID, g)
			}
		}

		if gc := row.gateColumn(); gc != "" {
			t.Errorf("%s: gate column = %q, want none", row.ID, gc)
		}
	}
}
