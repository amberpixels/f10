package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

// A driveFixture is the fakes, a tracker answering from a map, a clock
// that moves only when drive sleeps, and a hook per sleep so a test can
// change what Herdr and the run state say between two polls.
type driveFixture struct {
	f      *fakes
	in     *driveInput
	infos  map[string]taskInfo
	clock  time.Time
	sleeps int
	onTick map[int]func()
	out    bytes.Buffer
}

func newDriveFixture(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := &driveFixture{
		f:      newFakes(t),
		infos:  map[string]taskInfo{},
		clock:  time.Unix(1_800_000_000, 0),
		onTick: map[int]func(){},
	}
	fx.f.has["wt"] = true
	// no base branch has a commit or a head until a test says so
	fx.f.absent = append(fx.f.absent, "git rev-list --count ", "git rev-parse -q --verify refs/heads/",
		"git rev-parse -q --verify refs/remotes/origin/", "git reflog show ")

	// a test names the chain after the ids, as drive once took it, to run
	// its machinery on any skill; drive itself derives the chain
	var chain, rest []string

	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(oldChainSkills, args[i]):
			chain = append(chain, args[i])
		case args[i] == "--answer" || args[i] == "--model" || args[i] == "--every" || args[i] == "--flow":
			rest = append(rest, args[i], args[i+1])
			i++
		default:
			rest = append(rest, args[i])
		}
	}

	a, err := parseDriveArgs(rest)
	if err != nil {
		t.Fatal(err)
	}

	fx.in = &driveInput{
		main: "/code/repo",
		tracker: tracker{
			resolve: func(ctx context.Context, tok string) (ref.Ref, error) {
				return ref.Lookup{Prefix: "GH"}.Find(ctx, tok)
			},
			info: func(_ context.Context, r ref.Ref) (taskInfo, error) {
				info, ok := fx.infos[r.ID]
				if !ok {
					return taskInfo{}, errors.New("no such issue")
				}

				return info, nil
			},
			archive: []string{t.TempDir()},
		},
		args:     a,
		argv:     args,
		chain:    chain,
		stateDir: t.TempDir(),
		ttl:      state.DefaultTTL,
		now:      func() time.Time { return fx.clock },
		sleep: func(_ context.Context, d time.Duration) error {
			fx.sleeps++
			fx.clock = fx.clock.Add(d)

			if hook := fx.onTick[fx.sleeps]; hook != nil {
				hook()
			}

			return nil
		},
		finish: func(context.Context, io.Writer, ref.Ref, string) error {
			t.Fatal("finish ran although the chain has none")

			return nil
		},
	}

	return fx
}

func (fx *driveFixture) drive(t *testing.T) error {
	t.Helper()

	return drive(t.Context(), &fx.out, fx.in)
}

// task scripts a task the tracker knows: its branch lookups answering
// heads (empty for a task not started), and nothing recorded on a branch
// named after it.
func (fx *driveFixture) task(id string, info taskInfo, branches string) {
	fx.infos[id] = info
	fx.f.script(refsKey("refs/heads/", id), branches)
	fx.f.script(refsKey("refs/remotes/origin/", id), "")

	for _, key := range []string{cfgDrive, cfgAfter, cfgAfterBranch, cfgLanded, cfgMoved} {
		fx.f.script("git config --get branch."+id+"."+key, "")
	}
}

// opens scripts start's create path for a new task under worktrunk.
func (fx *driveFixture) opens(id, ws, pane string) {
	fx.opensOn(id, ws, pane, "")
}

// opensOn is opens with the agent started on model, "" for none.
func (fx *driveFixture) opensOn(id, ws, pane, model string) {
	agent := agentOf(id)

	start := "herdr agent start " + agent + " --kind claude --pane " + pane
	if model != "" {
		start += " -- --model " + model
	}

	fx.f.script("wt switch --no-cd --yes --format json --create "+id, "{}")
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
		`{"result":{"workspace":{"workspace_id":"`+ws+`"},"root_pane":{"pane_id":"`+pane+`"},"already_open":false}}`)
	fx.f.script(start, "{}")

	if model != "" {
		fx.f.script("git config branch."+id+"."+cfgModel+" "+model, "")
	}
}

// expects scripts the prompt for one skill and the record of it done.
func (fx *driveFixture) expects(id, skill string) {
	fx.f.script("herdr agent prompt "+agentOf(id)+" "+skillPrompt(skill, id, nil, shipFlow{}), "{}")
	fx.f.script("git config branch."+id+"."+cfgDrive+" "+skill, "")
}

// mergeable scripts the check before each task's finish: origin's copy of
// its branch merges cleanly into origin/main.
func (fx *driveFixture) mergeable(ids ...string) {
	fx.f.script(headRef, "origin/main")
	fx.f.script("git fetch origin", "")

	for _, id := range ids {
		fx.f.script("git rev-parse -q --verify origin/"+id, "pushed-"+id)
		fx.f.script("git merge-tree --write-tree origin/main pushed-"+id, "")
	}
}

// writeRun records a run against GH-1's worktree, updated now.
func (fx *driveFixture) writeRun(t *testing.T, ship, leaf, note, ask string) {
	t.Helper()
	fx.writeRunFor(t, "GH-1", ship, leaf, note, ask)
}

// writeRunFor records a run against a task's worktree, updated now.
func (fx *driveFixture) writeRunFor(t *testing.T, id, ship, leaf, note, ask string) {
	t.Helper()

	body := fmt.Sprintf(
		"v 1\ntask %s\nroot /code/repo.%s\ncapture prior\nplan done\nship %s\nleaf %s\nnote %s\nask %s\nupdated %d\n",
		id,
		id,
		ship,
		leaf,
		note,
		ask,
		fx.clock.Unix(),
	)

	if err := os.WriteFile(filepath.Join(fx.in.stateDir, "s-"+id+".state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unopened is the listing open reads for a checkout Herdr does not show yet.
var unopened = listing()

// listing is Herdr's workspace list: main's, then one per task with its
// agent status.
func listing(agents ...string) string {
	var b strings.Builder

	b.WriteString(
		`{"result":{"workspaces":[{"workspace_id":"ws:1","label":"repo","worktree":{"checkout_path":"/code/repo"}}`,
	)

	for i := 0; i+1 < len(agents); i += 2 {
		id := agents[i]
		b.WriteString(`,{"workspace_id":"ws:` + id + `","label":"` + id + `","agent_status":"` + agents[i+1] +
			`","worktree":{"checkout_path":"/code/repo.` + id + `"}}`)
	}

	b.WriteString("]}}")

	return b.String()
}

// FT-21's acceptance run, with no dependency between the two tasks: both
// worktrees are created and both agents prompted before either finishes a
// skill, each moves to ship as its judge ends, and the report has a row
// per task.
func TestDriveRunsIndependentTasksTogether(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "ship")
	fx.task("GH-1", taskInfo{body: "first"}, "")
	fx.task("GH-2", taskInfo{body: "second"}, "")
	fx.f.script(worktrees,
		porcelain(),
		porcelain("GH-1", "/code/repo.GH-1"),
		porcelain("GH-1", "/code/repo.GH-1"),
		porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.opens("GH-2", "ws:GH-2", "pane:2")

	for _, id := range []string{"GH-1", "GH-2"} {
		for _, skill := range []string{"judge", "ship"} {
			fx.expects(id, skill)
		}
	}

	// the looks before the two judge prompts, a tick mid-turn, a tick after
	// with the looks before the two ship prompts, then the same for ship
	fx.f.script(wsList,
		unopened, listing("GH-1", "idle"), unopened, listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "working", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "idle", "GH-2", "idle"), listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "working", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"--create GH-1",
		agentOf("GH-1") + " /f10:judge",
		"--create GH-2",
		agentOf("GH-2") + " /f10:judge",
		agentOf("GH-1") + " /f10:ship",
		agentOf("GH-2") + " /f10:ship",
	}
	fx.assertOrder(t, want, "herdr agent prompt", "wt switch")

	for _, row := range []string{"GH-1:    done: judge ship", "GH-2:    done: judge ship"} {
		if !strings.Contains(fx.out.String(), row) {
			t.Errorf("report lacks %q:\n%s", row, fx.out.String())
		}
	}
}

// A task drive opens fresh moves to its started status, as start's does.
func TestDriveMarksStarted(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", taskInfo{body: "first"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	var moved []string

	fx.in.started = func(_ context.Context, task ref.Ref) error {
		moved = append(moved, task.ID)

		return nil
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if len(moved) != 1 || moved[0] != "GH-1" {
		t.Errorf("moved %v, want GH-1", moved)
	}
}

// Each agent starts on its task's own model, else the run-wide one; a
// name that is no model starts claude's default, said once at the top.
func TestDriveStartsAgentsOnTheirModels(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		model1       string
		model2       string
		out, notWant string
	}{
		{
			name: "own and run-wide", args: []string{"GH-1:sonnet", "GH-2", "--model", "opus", "judge"},
			model1: "sonnet", model2: "opus", notWant: "!!!",
		},
		{
			name: "corrected", args: []string{"GH-1:fabel", "GH-2", "judge"},
			model1: "fable", out: "--model fabel read as fable",
		},
		{
			name: "no model",
			args: []string{"GH-1", "GH-2", "--model", "xyz", "judge"},
			out:  "!!! --model xyz is not a model name f10 recognizes: the agent starts on claude's default model instead",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newDriveFixture(t, c.args...)
			fx.task("GH-1", taskInfo{body: "first"}, "")
			fx.task("GH-2", taskInfo{body: "second"}, "")
			fx.f.script(worktrees,
				porcelain(),
				porcelain("GH-1", "/code/repo.GH-1"),
				porcelain("GH-1", "/code/repo.GH-1"),
				porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			fx.opensOn("GH-1", "ws:GH-1", "pane:1", c.model1)
			fx.opensOn("GH-2", "ws:GH-2", "pane:2", c.model2)
			fx.expects("GH-1", "judge")
			fx.expects("GH-2", "judge")
			fx.f.script(wsList,
				unopened, listing("GH-1", "idle"), unopened, listing("GH-1", "idle", "GH-2", "idle"),
				listing("GH-1", "working", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"))

			if err := fx.drive(t); err != nil {
				t.Fatalf("%v\n%s", err, fx.out.String())
			}

			out := fx.out.String()
			if c.out != "" && strings.Count(out, c.out) != 1 {
				t.Errorf("output carries %q %d times, want once:\n%s", c.out, strings.Count(out, c.out), out)
			}

			if c.notWant != "" && strings.Contains(out, c.notWant) {
				t.Errorf("output carries %q:\n%s", c.notWant, out)
			}
		})
	}
}

// assertOrder checks that the calls carrying any of the prefixes, in the
// order made, each carry the wanted text.
func (fx *driveFixture) assertOrder(t *testing.T, want []string, prefixes ...string) {
	t.Helper()

	var order []string

	for _, c := range fx.f.calls {
		for _, p := range prefixes {
			if strings.HasPrefix(c, p) {
				order = append(order, c)

				break
			}
		}
	}

	if len(order) != len(want) {
		t.Fatalf("calls in order = %q\nwant %q", order, want)
	}

	for i, w := range want {
		if !strings.Contains(order[i], w) {
			t.Errorf("call %d = %q, want it to carry %q", i, order[i], w)
		}
	}
}

// An agent that stops on a question ends the drive with exit 4, the
// question, and the command that answers it.
func TestDriveExitsOnAnAsk(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]") }

	err := fx.drive(t)
	if exitCode(err) != exitAsk {
		t.Fatalf("err = %v, want exit %d", err, exitAsk)
	}

	out := fx.out.String()
	for _, want := range []string{
		"GH-1:    asked at judge",
		"GH-1 asks at judge: 1. Rethink: [proceed | stop]",
		`answer with: f10 drive GH-1 judge --answer "1. … 2. …"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if fx.f.called("git config branch.GH-1." + cfgDrive) {
		t.Error("an asked skill was recorded as done")
	}

	if !fx.f.called("git config branch.GH-1." + cfgChain + " judge") {
		t.Error("the chain was not recorded on the branch")
	}
}

// The same command with --answer resumes the same task: the answer goes to
// the agent as plain text, and the skill finishes.
func TestDriveResumesWithTheAnswer(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge", "--answer", "1. proceed")
	fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. proceed", "{}")
	fx.f.script("git config branch.GH-1."+cfgDrive+" judge", "")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(fx.out.String(), "GH-1:    done: judge") || strings.Contains(fx.out.String(), "unused") {
		t.Errorf("output = %s", fx.out.String())
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:judge") {
		t.Error("the skill was prompted again instead of answered")
	}
}

// A judge stop, blocked with no question, halts the task with its reason,
// and the task after it is halted with it.
func TestDriveHaltsOnAJudgeStop(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.task("GH-2", taskInfo{body: "After: GH-1"}, "")
	fx.f.script(headRef, "origin/main") // GH-1 has no commit, so GH-2 waits
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	// the run's own stop is in the ledger as its claims already
	fx.in.record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d", err, exitHalted)
	}

	out := fx.out.String()
	for _, want := range []string{
		"GH-1:    halted at judge",
		"GH-2:    halted: base GH-1 halted",
		"GH-1 halted at judge: stop: duplicate of GH-9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// An agent idle while its ship phase still says running, past the grace,
// stopped without reporting: the drive halts naming the workspace.
func TestDriveHaltsOnAnAgentThatStoppedWithoutReporting(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("ship", "GH-1", nil, shipFlow{}), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[1] = func() { fx.writeRun(t, "running", "implement", "", "") }

	var recorded []ledger.Event

	fx.in.record = func(_ context.Context, dir string, e ledger.Event) {
		if dir != "/code/repo.GH-1" {
			t.Errorf("halt placed in %s, want the task's worktree", dir)
		}

		recorded = append(recorded, e)
	}

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d", err, exitHalted)
	}

	if !strings.Contains(fx.out.String(), "stopped without reporting") || !strings.Contains(fx.out.String(), "GH-1") {
		t.Errorf("output = %s", fx.out.String())
	}

	// the halt outlives the drive: status reads it from the ledger
	if len(recorded) != 1 {
		t.Fatalf("recorded %d events, want one halt", len(recorded))
	}

	e := recorded[0]
	if reason, _ := e.Payload.Val["reason"].(string); e.Kind != ledger.TaskHalted || e.Task != "GH-1" ||
		e.Payload.Val["skill"] != "ship" || !strings.Contains(reason, "stopped without reporting") {
		t.Errorf("halt event %+v", e)
	}

	if fx.sleeps < int(stallGrace/defaultEvery) {
		t.Errorf("halted after %d polls, before the grace ran out", fx.sleeps)
	}
}

// A finished task is skipped without touching Herdr; a recorded position
// resumes after the skill it names; finish runs in process and ends the
// chain.
func TestDriveSkipsFinishedAndResumesFromTheBranch(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "ship", "finish")
	fx.task("GH-1", taskInfo{body: "x", closed: true}, "")
	fx.task("GH-2", taskInfo{body: "y\nAfter: GH-1"}, heads("GH-2"))
	fx.f.script("git config --get branch.GH-2."+cfgDrive, "judge")
	fx.f.script(worktrees, porcelain("GH-2", "/code/repo.GH-2"))
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-2 --label GH-2",
		`{"result":{"workspace":{"workspace_id":"ws:GH-2"},"root_pane":{"pane_id":"pane:2"},"already_open":true}}`)
	fx.f.script("herdr agent get pane:2", agentFound)
	fx.expects("GH-2", "ship")
	fx.f.script(wsList, unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))
	fx.mergeable("GH-2")

	var finished []string

	fx.in.finish = func(_ context.Context, w io.Writer, task ref.Ref, _ string) error {
		finished = append(finished, task.ID)
		fmt.Fprintln(w, "task:  "+task.ID)

		return nil
	}

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	out := fx.out.String()
	for _, want := range []string{"GH-1:    already finished", "GH-2:    finished", "  task:  GH-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if fx.f.called("herdr agent prompt "+agentOf("GH-2")+" /f10:judge") ||
		fx.f.called("wt switch --no-cd --yes --format json --create GH-1") {
		t.Errorf("calls = %q", fx.f.calls)
	}

	if len(finished) != 1 || finished[0] != "GH-2" {
		t.Errorf("finished = %v", finished)
	}
}

// A plan saved for the task before drive opened it counts as plan done: the
// chain resumes at judge, and no plan prompt is sent. Out-of-tree storage
// keeps it in place; start's tests cover the move from main.
func TestDriveTakesASavedPlanAsPlanned(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "plan", "judge")
	fx.in.plansDir = t.TempDir()
	writePlan(t, filepath.Join(fx.in.plansDir, "GH-1.md"))
	fx.task("GH-1", taskInfo{body: "first"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	// the shared storage root sits outside the worktree, so the agent may write there
	addDir := " -- --add-dir " + filepath.Dir(fx.in.plansDir)
	fx.f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:1"+addDir, "{}")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:plan") {
		t.Errorf("the saved plan was planned again: %q", fx.f.calls)
	}

	if want := "GH-1:    done: plan judge"; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}
}

// position counts a plan the worktree reads only where the chain has plan,
// and a skill drive recorded on the branch still wins.
func TestDrivePositionCountsTheWorktreesPlan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chain    []string
		plan     string
		recorded string
		want     int
	}{
		{name: "plan in the chain", chain: []string{"plan", "judge", "ship"}, plan: "/p/GH-1.md", want: 1},
		{name: "no plan saved", chain: []string{"plan", "judge", "ship"}, want: 0},
		{name: "no plan in the chain", chain: []string{"judge", "ship"}, plan: "/p/GH-1.md", want: 0},
		{name: "recorded skill wins", chain: []string{"plan", "judge", "ship"}, plan: "/p/GH-1.md", recorded: "judge", want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1")
			fx.f.script("git config --get branch.GH-1."+cfgDrive, tc.recorded)

			pos, err := fx.in.position(t.Context(), openedTask{branch: "GH-1", path: "/code/repo.GH-1", plan: tc.plan},
				tc.chain)
			if err != nil || pos != tc.want {
				t.Errorf("position = %d, %v; want %d", pos, err, tc.want)
			}
		})
	}
}

// A range takes every id between its ends; a pull request in it, or a
// number the tracker does not know, is skipped with a note.
func TestDriveRangeSkipsWhatIsNotATask(t *testing.T) {
	fx := newDriveFixture(t, "GH-3", "--", "GH-6")
	fx.task("GH-3", taskInfo{body: "a"}, "")
	fx.infos["GH-4"] = taskInfo{pull: true}
	fx.task("GH-6", taskInfo{body: "c"}, "")

	tasks, notes, err := fx.in.resolveTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(tasks) != 2 || tasks[0].ref.ID != "GH-3" || tasks[1].ref.ID != "GH-6" {
		t.Errorf("tasks = %+v", tasks)
	}

	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "GH-4 skipped: a pull request") || !strings.Contains(joined, "GH-5 skipped") {
		t.Errorf("notes = %q", notes)
	}
}

// A listed id that is a pull request is an error, not a skip.
func TestDriveRefusesAListedPullRequest(t *testing.T) {
	fx := newDriveFixture(t, "GH-4")
	fx.infos["GH-4"] = taskInfo{pull: true}

	if _, _, err := fx.in.resolveTasks(t.Context()); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("err = %v", err)
	}
}

// The graph puts a base before its dependents whatever the list's order,
// and refuses what cannot run before anything is created: a base outside
// the list not yet finished, and a cycle.
func TestDriveGraph(t *testing.T) {
	graph := func(t *testing.T, fx *driveFixture) ([]string, error) {
		t.Helper()

		tasks, _, err := fx.in.resolveTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		order, _, err := fx.in.graph(t.Context(), tasks)

		var ids []string
		for _, i := range order {
			ids = append(ids, tasks[i].ref.ID)
		}

		return ids, err
	}

	t.Run("base later in the list runs first", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2", "GH-1")
		fx.task("GH-2", taskInfo{body: "x\nAfter: GH-1\n"}, "")
		fx.task("GH-1", taskInfo{body: "y"}, "")

		ids, err := graph(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-1 GH-2" {
			t.Errorf("order = %v, %v", ids, err)
		}
	})

	t.Run("waves", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-15", "GH-14", "GH-13", "GH-12", "GH-11")
		fx.task("GH-11", taskInfo{body: "root"}, "")
		fx.task("GH-12", taskInfo{body: "After: GH-11"}, "")
		fx.task("GH-13", taskInfo{body: "After: GH-11"}, "")
		fx.task("GH-14", taskInfo{body: "After: GH-11"}, "")
		fx.task("GH-15", taskInfo{body: "After: GH-13"}, "")

		ids, err := graph(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-11 GH-14 GH-13 GH-15 GH-12" {
			t.Errorf("order = %v, %v", ids, err)
		}
	})

	t.Run("the branch's record beats the body", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3")
		fx.task("GH-1", taskInfo{body: "a"}, "")
		fx.task("GH-2", taskInfo{body: "b"}, "")
		fx.task("GH-3", taskInfo{body: "After: GH-1"}, heads("GH-3"))
		fx.f.script("git config --get branch.GH-3."+cfgAfter, "GH-2")

		tasks, _, err := fx.in.resolveTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		_, notes, err := fx.in.graph(t.Context(), tasks)
		if err != nil || tasks[2].base != "GH-2" {
			t.Fatalf("base = %q, %v", tasks[2].base, err)
		}

		if len(notes) != 1 || !strings.Contains(notes[0], "the branch wins") {
			t.Errorf("notes = %q", notes)
		}
	})

	t.Run("a cycle", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3")
		fx.task("GH-1", taskInfo{body: "After: GH-2"}, "")
		fx.task("GH-2", taskInfo{body: "After: GH-1"}, "")
		fx.task("GH-3", taskInfo{body: "free"}, "")

		_, err := graph(t, fx)
		if err == nil || !strings.Contains(err.Error(), "circle: GH-1 after GH-2, GH-2 after GH-1") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("base outside the list, not finished", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2")
		fx.task("GH-2", taskInfo{body: "After: GH-9"}, "")
		fx.task("GH-9", taskInfo{body: "open"}, "")

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "not in the list and not finished") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.called("wt") || fx.f.called("herdr") {
			t.Error("something was created before the refusal")
		}
	})

	t.Run("base outside the list, finished", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2")
		fx.task("GH-2", taskInfo{body: "After: GH-9"}, "")
		fx.task("GH-9", taskInfo{closed: true}, "")

		if _, err := graph(t, fx); err != nil {
			t.Errorf("a finished base was refused: %v", err)
		}
	})
}

// A listed parent with unfinished subtasks is refused before anything is
// created; a task with none, a started one and a range member pass.
func TestDriveRefusesAParent(t *testing.T) {
	withChildren := func(fx *driveFixture, kids map[string][]childRow) {
		fx.in.tracker.children = func(_ context.Context, r ref.Ref) ([]childRow, error) {
			k, ok := kids[r.ID]
			if !ok {
				return nil, errNoParentLink
			}

			return k, nil
		}
	}

	t.Run("a parent with open subtasks", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", taskInfo{body: "the parent"}, "")
		withChildren(fx, map[string][]childRow{"GH-1": {{ID: "GH-2"}, {ID: "GH-3", Finished: true}, {ID: "GH-4"}}})

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "GH-1 is a parent of GH-2, GH-4") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.called("wt") || fx.f.called("herdr") {
			t.Error("something was created before the refusal")
		}
	})

	for name, kids := range map[string][]childRow{
		"every subtask finished": {{ID: "GH-2", Finished: true}},
		"no subtasks":            {},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1")
			fx.task("GH-1", taskInfo{body: "free"}, "")
			withChildren(fx, map[string][]childRow{"GH-1": kids})

			if _, _, err := fx.in.resolveTasks(t.Context()); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}

	t.Run("no parent link", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", taskInfo{body: "free"}, "")
		withChildren(fx, nil)

		if _, _, err := fx.in.resolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})

	t.Run("a started task is not asked", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", taskInfo{body: "free"}, "refs/heads/GH-1/work")
		fx.in.tracker.children = func(context.Context, ref.Ref) ([]childRow, error) {
			t.Error("children asked for a started task")

			return nil, nil
		}

		if _, _, err := fx.in.resolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})

	t.Run("a range member is not asked", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "--", "GH-2")
		fx.task("GH-1", taskInfo{body: "free"}, "")
		fx.task("GH-2", taskInfo{body: "free"}, "")
		fx.in.tracker.children = func(context.Context, ref.Ref) ([]childRow, error) {
			t.Error("children asked for a range member")

			return nil, nil
		}

		if _, _, err := fx.in.resolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})
}

func TestParseDriveArgs(t *testing.T) {
	cases := []struct {
		args    []string
		tasks   string
		span    bool
		flow    string
		answers string
		every   time.Duration
		model   string
		models  string // each task's :model, | between, checked when set
		wantErr string
	}{
		{args: []string{"GH-1", "GH-2", "--flow", "Direct"}, tasks: "GH-1 GH-2", flow: "direct", every: defaultEvery},
		{args: []string{"GH-1", "--local"}, tasks: "GH-1", flow: "local", every: defaultEvery},
		{args: []string{"GH-1", "--local", "--flow=local"}, tasks: "GH-1", flow: "local", every: defaultEvery},
		{args: []string{"GH-1", "--", "GH-4"}, tasks: "GH-1 GH-4", span: true, every: defaultEvery},
		{
			args:  []string{"--every", "5s", "GH-1", "--answer=1. yes"},
			tasks: "GH-1", answers: "1. yes", every: 5 * time.Second,
		},
		{
			args:  []string{"GH-1", "GH-2", "--answer", "GH-1=1. yes", "--answer=GH-2=1. no"},
			tasks: "GH-1 GH-2", answers: "GH-1=1. yes|GH-2=1. no", every: defaultEvery,
		},
		{
			args:  []string{"GH-1:sonnet", "GH-2", "--model", "opus"},
			tasks: "GH-1 GH-2", model: "opus", models: "sonnet|", every: defaultEvery,
		},
		{
			args:  []string{"--model=fable", "GH-1-retry:opus"},
			tasks: "GH-1-retry", model: "fable", models: "opus", every: defaultEvery,
		},
		{
			args:   []string{"https://host:8080/o/r/issues/1", "https://github.com/o/r/issues/2:opus"},
			tasks:  "https://host:8080/o/r/issues/1 https://github.com/o/r/issues/2",
			models: "|opus", every: defaultEvery,
		},
		{args: []string{"GH-1", "--", "GH-4"}, tasks: "GH-1 GH-4", span: true, models: "|", every: defaultEvery},
		{args: []string{"GH-1:"}, wantErr: "names no model"},
		{args: []string{"GH-1:opus", "--", "GH-4"}, wantErr: "run-wide --model"},
		{args: []string{"GH-1", "--model", "opus", "--model", "fable"}, wantErr: "given twice"},
		{args: []string{"GH-1", "--model"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "judge", "GH-2"}, wantErr: "drive takes no chain"},
		{args: []string{"GH-1", "ship"}, wantErr: "f10 flow lists them"},
		{args: []string{"GH-1", "--local", "--flow", "direct"}, wantErr: "already names the local flow"},
		{args: []string{"GH-1", "--flow"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "GH-2", "--", "GH-4"}, wantErr: "a range is one id"},
		{args: []string{"--model", "opus"}, wantErr: "needs the tasks"},
		{args: []string{"GH-1", "--every", "soon"}, wantErr: "positive duration"},
		{args: []string{"GH-1", "--answer"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "--force"}, wantErr: "unknown flag"},
	}

	for _, c := range cases {
		a, err := parseDriveArgs(c.args)

		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: err = %v, want %q", c.args, err, c.wantErr)
			}

			continue
		}

		if err != nil {
			t.Errorf("%q: %v", c.args, err)

			continue
		}

		if strings.Join(a.tasks, " ") != c.tasks || a.span != c.span || a.flow != c.flow ||
			strings.Join(a.answers, "|") != c.answers || a.every != c.every || a.model != c.model ||
			(c.models != "" && strings.Join(a.models, "|") != c.models) {
			t.Errorf("%q = %+v", c.args, a)
		}
	}
}

func TestDriveChain(t *testing.T) {
	for _, c := range []struct {
		fl     shipFlow
		finish bool
		want   string
	}{
		{shipFlow{}, false, "plan ship"},
		{shipFlow{}, true, "plan ship finish"},
		{shipFlow{name: "direct", planless: true}, true, "ship finish"},
		{shipFlow{name: "direct", planless: true}, false, "ship"},
	} {
		if got := strings.Join(driveChain(c.fl, c.finish), " "); got != c.want {
			t.Errorf("%+v finish %v = %q, want %q", c.fl, c.finish, got, c.want)
		}
	}
}

func TestSkillPrompt(t *testing.T) {
	dep := &dependency{task: "GH-7", branch: "GH-7/base"}

	cases := map[string]string{
		skillPrompt("plan", "GH-1", nil, shipFlow{}):              "/f10:plan GH-1 --driven",
		skillPrompt("ship", "GH-1", nil, shipFlow{}):              "/f10:ship GH-1 --driven",
		skillPrompt("ship", "GH-1", nil, shipFlow{name: "local"}): "/f10:ship local: GH-1 --driven",
		skillPrompt("plan", "GH-1", nil, shipFlow{name: "local"}): "/f10:plan GH-1 --driven",
		skillPrompt("judge", "GH-1", dep, shipFlow{}):             "/f10:judge GH-1 --driven",
	}

	for got, want := range cases {
		if got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}

	if p := skillPrompt("ship", "GH-1", dep, shipFlow{}); !strings.Contains(p, "GH-1 depends on GH-7") {
		t.Errorf("a dependent ship carries no contract: %q", p)
	}
}

func TestResumeCommand(t *testing.T) {
	got := resumeCommand([]string{"GH-1", "--", "GH-3", "judge", "--answer", "1. old", "--every=5s"})
	if want := "f10 drive GH-1 -- GH-3 judge --every=5s"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAfterIn(t *testing.T) {
	cases := map[string]string{
		"Body.\n\nAfter: GH-12\n":   "GH-12",
		"After:#12":                 "#12",
		"After: GH-12\r\n":          "GH-12",
		"Run after: GH-12":          "",
		"  After: GH-12":            "",
		"After: GH-12 and GH-13\n":  "",
		"no line at all, After: x ": "",
		"**After:** GH-12":          "GH-12",
		"`After:` GH-12":            "GH-12",
		"After: **GH-12**":          "GH-12",
		"After: `GH-12`":            "GH-12",
		"- After: GH-12":            "",
	}

	for body, want := range cases {
		if got := afterIn(body); got != want {
			t.Errorf("afterIn(%q) = %q, want %q", body, got, want)
		}
	}
}

// The body's base becomes start's --after when it has a branch, the
// default branch when it is finished, and a refusal otherwise.
func TestBodyAfter(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	tr := fx.in.tracker

	fx.task("GH-7", taskInfo{body: "base"}, heads("GH-7/base"))

	after, note, err := bodyAfter(t.Context(), tr, "/code/repo", gh1, "After: GH-7")
	if err != nil || after != "GH-7" || !strings.Contains(note, "from the task body") {
		t.Errorf("branched base = %q, %q, %v", after, note, err)
	}

	fx.task("GH-8", taskInfo{closed: true}, "")

	after, note, err = bodyAfter(t.Context(), tr, "/code/repo", gh1, "After: GH-8")
	if err != nil || after != "" || !strings.Contains(note, "finished") {
		t.Errorf("finished base = %q, %q, %v", after, note, err)
	}

	fx.task("GH-9", taskInfo{body: "open"}, "")

	if _, _, err := bodyAfter(t.Context(), tr, "/code/repo", gh1, "After: GH-9"); err == nil ||
		!strings.Contains(err.Error(), "start GH-9 first") {
		t.Errorf("unstarted base: err = %v", err)
	}

	if after, note, err := bodyAfter(
		t.Context(),
		tr,
		"/code/repo",
		gh1,
		"no line",
	); after != "" || note != "" ||
		err != nil {
		t.Errorf("no line = %q, %q, %v", after, note, err)
	}
}

// A plan archived by finish marks a task finished even when the tracker
// left it open.
func TestFinishedTaskByArchive(t *testing.T) {
	dir := t.TempDir()

	if finishedTask(taskInfo{}, []string{dir}, "GH-1", false) {
		t.Error("finished with nothing archived")
	}

	if err := os.WriteFile(filepath.Join(dir, "GH-1.2.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if !finishedTask(taskInfo{}, []string{dir}, "GH-1", false) {
		t.Error("an archived plan does not count")
	}

	if finishedTask(taskInfo{closed: true}, nil, "GH-1", true) {
		t.Error("a task with a branch counted as finished")
	}
}

// all is a Herdr listing with every given task's agent in one status.
func all(status string, ids ...string) string {
	pairs := make([]string, 0, 2*len(ids))
	for _, id := range ids {
		pairs = append(pairs, id, status)
	}

	return listing(pairs...)
}

// wave is the fixture for five tasks: GH-12, GH-13 and GH-14
// after GH-11, GH-15 after GH-13, every worktree already listed so each
// open reuses one, and finish recorded among the calls and closing the
// task the way the tracker would.
func wave(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, args...)
	fx.task("GH-11", taskInfo{body: "root"}, "")
	fx.task("GH-12", taskInfo{body: "After: GH-11"}, "")
	fx.task("GH-13", taskInfo{body: "After: GH-11"}, "")
	fx.task("GH-14", taskInfo{body: "After: GH-11"}, "")
	fx.task("GH-15", taskInfo{body: "After: GH-13"}, "")

	var pairs []string

	for i, id := range []string{"GH-11", "GH-12", "GH-13", "GH-14", "GH-15"} {
		pairs = append(pairs, id, "/code/repo."+id)
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i))

		for _, skill := range []string{"judge", "ship"} {
			fx.expects(id, skill)
		}
	}

	fx.f.script(worktrees, porcelain(pairs...))
	fx.mergeable("GH-11", "GH-12", "GH-13", "GH-14", "GH-15")

	fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref, _ string) error {
		fx.f.calls = append(fx.f.calls, "finish "+task.ID)
		fx.infos[task.ID] = taskInfo{closed: true}

		return nil
	}

	return fx
}

// The five-task graph runs as three waves: GH-11 alone, then
// GH-12, GH-13 and GH-14 prompted together once GH-11 finished, then GH-15
// once its base GH-13 finished.
func TestDriveRunsTheGraphInWaves(t *testing.T) {
	ids := []string{"GH-11", "GH-12", "GH-13", "GH-14", "GH-15"}
	fx := wave(t, "GH-11", "GH-12", "GH-13", "GH-14", "GH-15", "judge", "finish")

	fx.f.script(wsList,
		unopened, all("idle", ids...), // GH-11's open, then the look before its judge
		all("working", ids...), all("idle", ids...), // GH-11's turn, then it finishes
		unopened, all("idle", ids...), unopened, all("idle", ids...), // the second wave's opens and looks
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...), // the second wave's turns
		unopened, all("idle", ids...), // GH-15's open, then the look before its judge
		all("working", ids...), all("idle", ids...))

	// finish runs in-process, where no hook records it: drive does
	var finished []string

	fx.in.record = func(_ context.Context, _ string, e ledger.Event) {
		if e.Kind == ledger.TaskFinished {
			finished = append(finished, e.Task)
		}
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if len(finished) != len(ids) {
		t.Errorf("recorded finishes %v, want one per task", finished)
	}

	fx.assertOrder(t, []string{
		agentOf("GH-11") + " /f10:judge", "finish GH-11",
		agentOf("GH-12") + " /f10:judge", agentOf("GH-13") + " /f10:judge", agentOf("GH-14") + " /f10:judge",
		"finish GH-12", "finish GH-13", "finish GH-14",
		agentOf("GH-15") + " /f10:judge", "finish GH-15",
	}, "herdr agent prompt", "finish")

	for _, id := range ids {
		if !strings.Contains(fx.out.String(), id+":    finished") {
			t.Errorf("%s is not finished:\n%s", id, fx.out.String())
		}
	}
}

// A judge stop on GH-13 halts it and GH-15 after it, with the reason, while
// GH-12 and GH-14 go on to ship.
func TestDriveScopesAStopToItsDependents(t *testing.T) {
	ids := []string{"GH-12", "GH-13", "GH-14", "GH-15"}
	fx := wave(t, "GH-11", "GH-12", "GH-13", "GH-14", "GH-15", "judge", "ship")
	fx.infos["GH-11"] = taskInfo{closed: true}

	fx.f.script(wsList,
		unopened, all("idle", ids...), unopened, all("idle", ids...), // the three opens and looks
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...), // the judges' turns
		all("idle", ids...), all("idle", ids...), // before GH-12's and GH-14's ships
		all("working", ids...), all("idle", ids...))
	fx.onTick[2] = func() { fx.writeRunFor(t, "GH-13", "blocked", "judge", "stop: duplicate of GH-9", "") }

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, fx.out.String())
	}

	out := fx.out.String()
	for _, want := range []string{
		"GH-11:    already finished",
		"GH-12:    done: judge ship",
		"GH-13:    halted at judge",
		"GH-14:    done: judge ship",
		"GH-15:    halted: base GH-13 halted",
		"GH-13 halted at judge: stop: duplicate of GH-9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if fx.f.called("herdr agent prompt "+agentOf("GH-13")+" /f10:ship") ||
		fx.f.called("herdr agent prompt "+agentOf("GH-15")) {
		t.Errorf("a halted task or its dependent was prompted: %q", fx.f.calls)
	}
}

// A dependent started before its base finished is caught up with the
// default branch before its next skill: finish marks it landed, the drive
// sends /f10:catchup, and only once the mark is gone and the tree is clean
// does judge follow. A catchup that leaves the mark halts the task.
func TestDriveCatchesADependentUpAfterFinish(t *testing.T) {
	for _, clean := range []bool{true, false} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "finish")
			fx.task("GH-1", taskInfo{body: "base"}, "")
			fx.task("GH-2", taskInfo{body: "dependent"}, heads("GH-2"))
			fx.f.script("git config --get branch.GH-2."+cfgAfter, "GH-1")
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.opens("GH-2", "ws:GH-2", "pane:2")
			fx.expects("GH-1", "judge")
			fx.expects("GH-2", "judge")
			fx.f.script("herdr agent prompt "+agentOf("GH-2")+" /f10:catchup --driven", "{}")
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
			fx.f.script("git status --porcelain", "")
			fx.mergeable("GH-1", "GH-2")

			fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref, _ string) error {
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)
				fx.infos[task.ID] = taskInfo{closed: true}

				if task.ID == "GH-1" {
					after := ""
					if !clean {
						after = "GH-1"
					}
					// the mark finish leaves, then what the catchup left
					fx.f.script("git config --get branch.GH-2."+cfgLanded, "GH-1", after)
				}

				return nil
			}

			ids := []string{"GH-1", "GH-2"}
			fx.f.script(wsList,
				unopened, all("idle", ids...),
				all("working", ids...), all("idle", ids...), // GH-1's judge, then finish
				unopened, all("idle", ids...), // GH-2's open, then the look before its catchup
				all("working", ids...), all("idle", ids...), // the catchup
				all("idle", ids...), // before GH-2's judge
				all("working", ids...), all("idle", ids...))

			err := fx.drive(t)

			if !clean {
				if exitCode(err) != exitHalted || !strings.Contains(fx.out.String(), "GH-2:    halted at catchup") ||
					!strings.Contains(fx.out.String(), "still marked "+cfgLanded) {
					t.Fatalf("err = %v\n%s", err, fx.out.String())
				}

				if fx.f.called("herdr agent prompt " + agentOf("GH-2") + " /f10:judge") {
					t.Error("judge ran on a branch the catchup left unsettled")
				}

				return
			}

			if err != nil {
				t.Fatalf("%v\n%s", err, fx.out.String())
			}

			fx.assertOrder(t, []string{
				agentOf(
					"GH-1",
				) + " /f10:judge",
				"finish GH-1",
				agentOf("GH-2") + " /f10:catchup --driven",
				agentOf("GH-2") + " /f10:judge",
				"finish GH-2",
			}, "herdr agent prompt", "finish")

			if fx.f.called("git config branch.GH-2." + cfgDrive + " catchup") {
				t.Error("the catchup was recorded as a chain position")
			}
		})
	}
}

// A pull request that conflicts with the default branch at finish, as a
// sibling that merged first leaves it, is caught up before the merge: the
// branch is marked landed, the agent gets /f10:catchup, and the caught-up
// branch is pushed (with a lease, since the catchup rebased it) before
// finish merges it. A branch still conflicting after that catchup halts.
func TestDriveCatchesUpAConflictingPullBeforeFinish(t *testing.T) {
	for _, settled := range []bool{true, false} {
		t.Run(fmt.Sprintf("settled=%v", settled), func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.script(headRef, "origin/main")
			fx.f.script("git fetch origin", "")
			fx.f.script("git rev-parse -q --verify origin/GH-1", "old")
			fx.f.fail("git merge-tree --write-tree origin/main old", shell.Result{Code: 1})
			fx.f.script("git config branch.GH-1."+cfgLanded+" main", "")
			// before the catchup, the switch that sends it, after it, before finish's push
			fx.f.script("git config --get branch.GH-1."+cfgLanded, "", "main", "", "", "")
			fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
			fx.f.script("git status --porcelain", "")
			fx.f.fail("git merge-base --is-ancestor old HEAD", shell.Result{Code: 1})
			fx.f.script("git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1", "")

			after := shell.Result{}
			if !settled {
				after = shell.Result{Code: 1}
			}

			fx.f.answers["git merge-base --is-ancestor origin/main HEAD"] = []shell.Result{{Code: 1}, after}

			fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref, _ string) error {
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)

				return nil
			}

			fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

			err := fx.drive(t)
			out := fx.out.String()

			if !settled {
				if exitCode(err) != exitHalted ||
					!strings.Contains(out, "still conflicts with origin/main after a catchup") {
					t.Fatalf("err = %v\n%s", err, out)
				}

				if fx.f.called("finish GH-1") || fx.f.called("git push") {
					t.Errorf("a branch still conflicting was pushed or merged: %q", fx.f.calls)
				}

				return
			}

			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			fx.assertOrder(t, []string{
				"git config branch.GH-1." + cfgLanded + " main",
				"herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven",
				"git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1",
				"finish GH-1",
			}, "git config branch.GH-1."+cfgLanded+" ", "herdr agent prompt", "git push", "finish")

			for _, want := range []string{"conflicts with origin/main, catching up first", "--force-with-lease", "GH-1:    finished"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// Two tasks asking in the same tick both reach the report, with a resume
// command keyed per task; the resume sends each answer to its own agent.
func TestDriveCollectsEveryAskAndTakesKeyedAnswers(t *testing.T) {
	ids := []string{"GH-1", "GH-2"}

	setup := func(t *testing.T, args ...string) *driveFixture {
		t.Helper()

		fx := newDriveFixture(t, args...)
		fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))

		return fx
	}

	t.Run("ask", func(t *testing.T) {
		fx := setup(t, "GH-1", "GH-2", "judge")
		fx.task("GH-1", taskInfo{body: "a"}, "")
		fx.task("GH-2", taskInfo{body: "b"}, "")
		fx.opens("GH-1", "ws:GH-1", "pane:1")
		fx.opens("GH-2", "ws:GH-2", "pane:2")
		fx.expects("GH-1", "judge")
		fx.expects("GH-2", "judge")
		fx.f.script(wsList,
			unopened, all("idle", ids...), unopened, all("idle", ids...), all("working", ids...), all("idle", ids...))
		fx.onTick[2] = func() {
			fx.writeRunFor(t, "GH-1", "blocked", "judge", "", "1. Rethink: [proceed | stop]")
			fx.writeRunFor(t, "GH-2", "blocked", "judge", "", "1. Split it? [yes | no]")
		}

		err := fx.drive(t)
		if exitCode(err) != exitAsk {
			t.Fatalf("err = %v, want exit %d", err, exitAsk)
		}

		for _, want := range []string{
			"GH-1 asks at judge: 1. Rethink: [proceed | stop]",
			"GH-2 asks at judge: 1. Split it? [yes | no]",
			`answer with: f10 drive GH-1 GH-2 judge --answer "GH-1=1. … 2. …" --answer "GH-2=1. … 2. …"`,
		} {
			if !strings.Contains(fx.out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, fx.out.String())
			}
		}
	})

	t.Run("resume", func(t *testing.T) {
		fx := setup(t, "GH-1", "GH-2", "judge", "--answer", "GH-1=1. proceed", "--answer", "gh-2=1. no")
		fx.task("GH-1", taskInfo{body: "a"}, heads("GH-1"))
		fx.task("GH-2", taskInfo{body: "b"}, heads("GH-2"))

		for _, id := range ids {
			fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
				`{"result":{"workspace":{"workspace_id":"ws:`+id+`"},"root_pane":{"pane_id":"p"},"already_open":true}}`)
			fx.f.script("herdr agent get p", agentFound)
			fx.f.script("git config branch."+id+"."+cfgDrive+" judge", "")
		}

		fx.writeRunFor(t, "GH-1", "blocked", "judge", "", "1. Rethink: [proceed | stop]")
		fx.writeRunFor(t, "GH-2", "blocked", "judge", "", "1. Split it? [yes | no]")
		fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. proceed", "{}")
		fx.f.script("herdr agent prompt "+agentOf("GH-2")+" 1. no", "{}")
		fx.f.script(wsList,
			unopened, all("idle", ids...), unopened, all("idle", ids...), all("working", ids...), all("idle", ids...))
		fx.onTick[2] = func() {
			fx.writeRunFor(t, "GH-1", "done", "", "", "")
			fx.writeRunFor(t, "GH-2", "done", "", "", "")
		}

		if err := fx.drive(t); err != nil {
			t.Fatalf("%v\n%s", err, fx.out.String())
		}

		if strings.Contains(fx.out.String(), "unused") {
			t.Errorf("an answer went unused:\n%s", fx.out.String())
		}

		fx.assertOrder(t, []string{agentOf("GH-1") + " 1. proceed", agentOf("GH-2") + " 1. no"}, "herdr agent prompt")
	})

	t.Run("two bare answers", func(t *testing.T) {
		fx := setup(t, "GH-1", "--answer", "yes", "--answer", "no")
		fx.task("GH-1", taskInfo{body: "a"}, "")

		if err := fx.drive(t); err == nil || !strings.Contains(err.Error(), "key each one") {
			t.Errorf("err = %v", err)
		}
	})
}

// shownTwice is Herdr's list with the checkout of GH-1 in two workspaces: a
// leftover first, then the one with the agent.
func shownTwice(leftover, agent string) string {
	return `{"result":{"workspaces":[` +
		`{"workspace_id":"ws:old","label":"GH-1","agent_status":"` + leftover +
		`","worktree":{"checkout_path":"/code/repo.GH-1"}},` +
		`{"workspace_id":"ws:GH-1","label":"GH-1","agent_status":"` + agent +
		`","worktree":{"checkout_path":"/code/repo.GH-1"}}]}}`
}

// staleRun records a run against GH-1's worktree a minute before now, as a
// cancelled start's agent leaves it.
func (fx *driveFixture) staleRun(t *testing.T, ship, leaf string) {
	t.Helper()

	now := fx.clock
	fx.clock = now.Add(-time.Minute)
	fx.writeRun(t, ship, leaf, "", "")
	fx.clock = now
}

// existing scripts a started GH-1: its branch and worktree, and nothing
// recorded on the branch.
func (fx *driveFixture) existing() {
	fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
}

const agentMissing = `{"error":{"code":"agent_not_found","message":"agent target pane:1 not found"}}`

// FT-28's case: an earlier start left a workspace with no agent and a run
// that still says running. The drive opens no second workspace, starts the
// agent in the leftover's idle shell, and prompts it: the old run's agent
// is gone, so its state halts nothing.
func TestDriveAdoptsAWorkspaceAnEarlierStartLeft(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.existing()
	fx.staleRun(t, "running", "implement")
	fx.f.script("herdr pane list --workspace ws:GH-1", `{"result":{"panes":[{"pane_id":"pane:1"}]}}`)
	fx.f.fail("herdr agent get pane:1", shell.Result{Code: 1, Stderr: agentMissing})
	fx.f.script("herdr pane process-info --pane pane:1",
		`{"result":{"process_info":{"foreground_process_group_id":42,"shell_pid":42}}}`)
	fx.f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:1", "{}")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList,
		listing("GH-1", "unknown"), listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr worktree open") {
		t.Error("a second workspace was opened on a checkout Herdr already shows")
	}

	for _, want := range []string{
		"GH-1: branch GH-1 at /code/repo.GH-1, workspace ws:GH-1",
		"GH-1 judge: sent",
		"GH-1:    done: judge",
	} {
		if !strings.Contains(fx.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, fx.out.String())
		}
	}
}

// Two workspaces on one checkout: the one with the agent is adopted, the
// other is named, and the turn is read from the adopted one although the
// leftover comes first in Herdr's list.
func TestDriveAdoptsTheWorkspaceWithTheAgent(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.existing()
	fx.f.script("herdr pane list --workspace ws:old", `{"result":{"panes":[{"pane_id":"pane:9"}]}}`)
	fx.f.script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:2"},{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, shownTwice("unknown", "idle"), shownTwice("idle", "idle"),
		shownTwice("idle", "working"), shownTwice("idle", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	for _, want := range []string{
		"workspace ws:GH-1",
		"workspace GH-1 also shows this checkout and was left open: close it with herdr workspace close ws:old",
		"GH-1:    done: judge",
	} {
		if !strings.Contains(fx.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, fx.out.String())
		}
	}

	if fx.f.called("herdr agent start") || fx.f.called("herdr workspace close") {
		t.Errorf("calls = %q", fx.f.calls)
	}
}

// A model for a workspace whose agent is already running is said to be
// ignored, never applied by starting a second agent.
func TestDriveSaysAModelWasIgnored(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "--model", "opus", "judge")
	fx.existing()
	fx.f.script("herdr pane list --workspace ws:old", `{"result":{"panes":[{"pane_id":"pane:9"}]}}`)
	fx.f.script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:2"},{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, shownTwice("unknown", "idle"), shownTwice("idle", "idle"),
		shownTwice("idle", "working"), shownTwice("idle", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if want := "--model opus ignored: no agent was started"; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}

	if fx.f.called("herdr agent start") {
		t.Errorf("calls = %q", fx.f.calls)
	}
}

// An agent that was already there, idle under a run that says running, may
// sit on a dialog: the task halts, and the halt is on screen before the
// report, naming the way out of a cancelled run.
func TestDrivePrintsAHaltWhenItHappens(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.existing()
	fx.staleRun(t, "running", "implement")
	fx.f.script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.f.script(wsList, listing("GH-1", "idle"))

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d", err, exitHalted)
	}

	out := fx.out.String()
	line, row := strings.Index(out, "GH-1 halted at judge: agent"), strings.Index(out, "GH-1:    halted at judge")

	if line < 0 || row < 0 || line > row {
		t.Errorf("the halt is not printed before the report:\n%s", out)
	}

	if !strings.Contains(out, "quit that agent and rerun") {
		t.Errorf("the halt names no way out of a cancelled run:\n%s", out)
	}
}

// A workspace whose agent Herdr never places halts the task once the start
// window is out, instead of being polled until the drive ends.
func TestDriveBoundsTheWaitForAnAgent(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "unknown"))

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "workspace GH-1 (ws:GH-1) shows no agent") {
		t.Errorf("output = %s", fx.out.String())
	}

	if limit := int(startWindow/defaultEvery) + 2; fx.sleeps > limit {
		t.Errorf("polled %d times, past the start window's %d", fx.sleeps, limit)
	}
}

// Someone else's PR at finish is drive's own question: the task stops
// asking, and the resumed drive passes the answer to finish.
func TestDriveFinishAsksForSomeoneElsesPR(t *testing.T) {
	var got []string

	in := &driveInput{
		answers: map[string]string{},
		finish: func(_ context.Context, _ io.Writer, _ ref.Ref, pr string) error {
			got = append(got, pr)
			if pr == "" {
				return foreignPullError{url: "https://gh/pr/9", author: "alice"}
			}

			return nil
		},
	}

	task := driveTask{ref: gh1}

	res := in.runFinish(t.Context(), io.Discard, task)
	if res.code != exitAsk || len(res.lines) != 1 || !strings.Contains(res.lines[0], "[local | merge]") {
		t.Fatalf("runFinish on someone else's PR = %+v, want an ask", res)
	}

	in.answers["GH-1"] = "1. local"

	if res = in.runFinish(t.Context(), io.Discard, task); res.code != 0 {
		t.Fatalf("runFinish answered local = %+v, want finished", res)
	}

	// a bare answer that is no finish choice belongs to some agent's question
	in.answer = "1. yes"

	if res = in.runFinish(t.Context(), io.Discard, task); res.code != exitAsk || in.answer != "1. yes" {
		t.Errorf("a bare non-choice answer = %+v, left %q; want it left for an agent", res, in.answer)
	}

	in.answers["GH-1"] = "maybe"

	if res = in.runFinish(t.Context(), io.Discard, task); res.code != exitHalted {
		t.Errorf("a keyed answer that is no choice = %+v, want a halt", res)
	}

	if want := []string{"", finishLocal, ""}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("finish was passed %q, want %q", got, want)
	}
}

// chained is the fixture for GH-2 after GH-1, GH-2's branch already
// recording it, with GH-1's branch one commit past main so GH-2 opens as
// soon as GH-1 has.
func chained(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, args...)
	fx.task("GH-1", taskInfo{body: "base"}, "")
	fx.task("GH-2", taskInfo{body: "dependent"}, heads("GH-2"))
	fx.f.script("git config --get branch.GH-2."+cfgAfter, "GH-1")
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.f.script(headRef, "origin/main")
	fx.f.script("git rev-list --count refs/heads/GH-1 --not refs/heads/main", "1")
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.opens("GH-2", "ws:GH-2", "pane:2")

	return fx
}

// A dependent opens once its base's branch has a commit, while the base is
// still in judge, and its finish waits until the base finished.
func TestDriveStartsADependentAtItsBasesFirstCommit(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "judge", "finish")
	fx.expects("GH-1", "judge")
	fx.expects("GH-2", "judge")
	fx.mergeable("GH-1", "GH-2")

	fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref, _ string) error {
		fx.f.calls = append(fx.f.calls, "finish "+task.ID)

		return nil
	}

	fx.f.script(wsList,
		unopened, all("idle", "GH-1"), // GH-1's open and the look before its judge
		unopened, all("idle", "GH-1", "GH-2"), // GH-2's, in the same pass
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's judge ends first
		all("idle", "GH-1", "GH-2"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		agentOf("GH-1") + " /f10:judge",
		agentOf("GH-2") + " /f10:judge",
		"finish GH-1",
		"finish GH-2",
	}, "herdr agent prompt", "finish")

	out := fx.out.String()
	for _, want := range []string{"GH-2 finish: waiting for GH-1 to finish", "GH-1:    finished", "GH-2:    finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A base whose branch has no commit holds its dependent, and a base that
// halts before one leaves it unopened.
func TestDriveHoldsADependentUntilItsBaseCommits(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "judge")
	fx.f.script("git rev-list --count refs/heads/GH-1 --not refs/heads/main", "0")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, all("idle", "GH-1"), all("working", "GH-1"), all("idle", "GH-1"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); exitCode(err) != exitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-2")) {
		t.Errorf("GH-2 was prompted before GH-1 had a commit: %q", fx.f.calls)
	}

	if !strings.Contains(fx.out.String(), "GH-2:    halted: base GH-1 halted") {
		t.Errorf("GH-2's row is wrong:\n%s", fx.out.String())
	}
}

// A base that halts after its dependent started halts the dependent at its
// next boundary, and the dependent's next skill is never sent.
func TestDriveHaltsADependentWhenItsBaseHaltsAfterRelease(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "judge", "ship")
	fx.expects("GH-1", "judge")
	fx.expects("GH-2", "judge")

	fx.f.script(wsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "idle", "GH-2", "working"), // GH-1 stops on its judge
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); exitCode(err) != exitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	out := fx.out.String()
	for _, want := range []string{
		"GH-1:    halted at judge",
		"GH-2:    halted: base GH-1 halted",
		"GH-2 halted before ship: base GH-1 halted, its work stays on GH-2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-2") + " /f10:ship") {
		t.Error("GH-2's ship was sent after its base halted")
	}
}

// A base branch that moves while the base agent is idle marks the
// dependent, which catches up before its next skill; once its branch holds
// the base's head nothing is marked again.
func TestDriveCatchesADependentUpWhenItsBaseMoves(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "judge", "ship")

	for _, id := range []string{"GH-1", "GH-2"} {
		fx.expects(id, "judge")
		fx.expects(id, "ship")
	}

	moved := "git config --get branch.GH-2." + cfgMoved
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "b2")
	fx.f.answers["git merge-base --is-ancestor b2 refs/heads/GH-2"] = []shell.Result{{Code: 1}, {}}
	fx.f.script("git config branch.GH-2."+cfgMoved+" b2", "")
	fx.f.script("herdr agent prompt "+agentOf("GH-2")+" /f10:catchup --driven", "{}")
	fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
	fx.f.script("git status --porcelain", "")
	fx.onTick[3] = func() { fx.f.script(moved, "b2") } // what the drive set
	fx.onTick[4] = func() { fx.f.script(moved, "") }   // the catchup cleared it

	fx.f.script(wsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "idle", "GH-2", "working"), // GH-1's judge ends: it is idle, and its branch moved
		listing("GH-1", "idle", "GH-2", "working"), // the look before GH-1's ship
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's judge ends
		listing("GH-1", "working", "GH-2", "idle"), // the look before its catchup
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // the catchup ends
		listing("GH-1", "working", "GH-2", "idle"), // the look before GH-2's ship
		listing("GH-1", "idle", "GH-2", "working"),
		all("idle", "GH-1", "GH-2"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		"herdr agent prompt " + agentOf("GH-2") + " /f10:judge",
		"git config branch.GH-2." + cfgMoved + " b2",
		"herdr agent prompt " + agentOf("GH-2") + " /f10:catchup --driven",
		"herdr agent prompt " + agentOf("GH-2") + " /f10:ship",
	}, "herdr agent prompt "+agentOf("GH-2"), "git config branch.GH-2."+cfgMoved)

	if n := strings.Count(strings.Join(fx.f.calls, "\n"), "git config branch.GH-2."+cfgMoved+" "); n != 1 {
		t.Errorf("marked %d times, want once", n)
	}

	if !strings.Contains(fx.out.String(), "GH-2:    done: judge ship") {
		t.Errorf("GH-2 did not finish its chain:\n%s", fx.out.String())
	}
}

// A base's own commits are what its branch has beyond main, origin/main
// (which local main may lag), its own base's branch and the commit it was
// created at, so a squash-merged grandparent's commits never release it.
func TestDriveReleasedCountsOnlyTheBasesOwnCommits(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	fx.f.script(headRef, "origin/main")
	b := &driveRun{opened: true, o: openedTask{branch: "GH-2"}}

	fx.f.script("git config --get branch.GH-2."+cfgAfterBranch, "GH-1")
	fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "a1")
	fx.f.script("git reflog show --format=%H %gs refs/heads/GH-2",
		"c3 commit: second\nc2 commit: first\nc1 branch: Created from GH-1")
	fx.f.script("git rev-list --count refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main "+
		"refs/heads/GH-1 c1", "0")

	if fx.in.released(t.Context(), b) {
		t.Error("released with no commit of its own")
	}

	// GH-1 squash-merged and gone: the creation point still keeps its commits out
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "")
	fx.f.script("git rev-list --count refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main c1", "0", "1")

	if fx.in.released(t.Context(), b) {
		t.Error("released by its squashed base's commits")
	}

	if !fx.in.released(t.Context(), b) {
		t.Error("not released once it has a commit of its own")
	}

	b.end = &driveResult{code: exitHalted}

	if fx.in.released(t.Context(), b) {
		t.Error("a halted base released its dependent")
	}
}

// A branch origin does not have is tried against the local default branch
// before finish, which merges it here: clean goes straight to finish, a
// conflict is marked landed and caught up first.
func TestDriveFinishesABranchNeverPushed(t *testing.T) {
	for _, clean := range []bool{true, false} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.script(headRef, "origin/main")
			fx.f.script("git fetch origin", "")
			fx.f.script("git rev-parse -q --verify origin/GH-1", "")
			fx.f.script("git rev-parse -q --verify refs/heads/main", "m0")
			fx.f.script(wsList, unopened, listing("GH-1", "idle"))

			fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref, _ string) error {
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)

				return nil
			}

			if !clean {
				fx.f.fail("git merge-tree --write-tree m0 refs/heads/GH-1", shell.Result{Code: 1})
				fx.f.script("git config branch.GH-1."+cfgLanded+" main", "")
				fx.f.script("git config --get branch.GH-1."+cfgLanded, "", "main")
				fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")
				fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
				fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
				fx.f.script("git status --porcelain", "")
				fx.f.script(wsList,
					unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
			} else {
				fx.f.script("git merge-tree --write-tree m0 refs/heads/GH-1", "tree")
			}

			err := fx.drive(t)
			out := fx.out.String()

			if clean {
				if err != nil || !fx.f.called("finish GH-1") || fx.f.called("git push") {
					t.Fatalf("err = %v, calls %q\n%s", err, fx.f.calls, out)
				}

				return
			}

			// the catchup left the mark, so the task halts rather than merging
			if exitCode(err) != exitHalted || fx.f.called("finish GH-1") ||
				!strings.Contains(out, "conflicts with m0, catching up first") {
				t.Fatalf("err = %v\n%s", err, out)
			}
		})
	}
}
