package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
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

	a, err := parseDriveArgs(args)
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
		chain:    a.chain,
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
		finish: func(context.Context, io.Writer, ref.Ref) error {
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
	fx.f.script("git for-each-ref --format=%(refname) refs/heads/"+id+" refs/heads/"+id+"-*", branches)
	fx.f.script("git for-each-ref --format=%(refname) refs/remotes/origin/"+id+" refs/remotes/origin/"+id+"-*", "")

	for _, key := range []string{cfgDrive, cfgAfter, cfgLanded} {
		fx.f.script("git config --get branch."+id+"."+key, "")
	}
}

// opens scripts start's create path for a new task under worktrunk.
func (fx *driveFixture) opens(id, ws, pane string) {
	agent := agentOf(id)

	fx.f.script("wt switch --no-cd --yes --format json --create "+id, "{}")
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
		`{"result":{"workspace":{"workspace_id":"`+ws+`"},"root_pane":{"pane_id":"`+pane+`"},"already_open":false}}`)
	fx.f.script("herdr agent start "+agent+" --kind claude --pane "+pane, "{}")
}

// expects scripts the prompt for one skill and the record of it done.
func (fx *driveFixture) expects(id, skill string) {
	fx.f.script("herdr agent prompt "+agentOf(id)+" "+skillPrompt(skill, id, nil, false), "{}")
	fx.f.script("git config branch."+id+"."+cfgDrive+" "+skill, "")
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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, false), "{}")
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

	if fx.f.called("git config branch.GH-1") {
		t.Error("an asked skill was recorded as done")
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
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, false), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("ship", "GH-1", nil, false), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[1] = func() { fx.writeRun(t, "running", "implement", "", "") }

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d", err, exitHalted)
	}

	if !strings.Contains(fx.out.String(), "stopped without reporting") || !strings.Contains(fx.out.String(), "GH-1") {
		t.Errorf("output = %s", fx.out.String())
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

	var finished []string

	fx.in.finish = func(_ context.Context, w io.Writer, task ref.Ref) error {
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

func TestParseDriveArgs(t *testing.T) {
	cases := []struct {
		args    []string
		tasks   string
		span    bool
		chain   string
		answers string
		every   time.Duration
		wantErr string
	}{
		{args: []string{"GH-1", "GH-2", "judge", "ship"}, tasks: "GH-1 GH-2", chain: "judge ship", every: defaultEvery},
		{args: []string{"GH-1", "--", "GH-4"}, tasks: "GH-1 GH-4", span: true, every: defaultEvery},
		{
			args:  []string{"--every", "5s", "GH-1", "plan", "→", "ship", "--answer=1. yes"},
			tasks: "GH-1", chain: "plan ship", answers: "1. yes", every: 5 * time.Second,
		},
		{
			args:  []string{"GH-1", "GH-2", "--answer", "GH-1=1. yes", "--answer=GH-2=1. no"},
			tasks: "GH-1 GH-2", answers: "GH-1=1. yes|GH-2=1. no", every: defaultEvery,
		},
		{args: []string{"GH-1", "judge", "GH-2"}, wantErr: "follows the chain"},
		{args: []string{"GH-1", "GH-2", "--", "GH-4"}, wantErr: "a range is one id"},
		{args: []string{"judge"}, wantErr: "needs the tasks"},
		{args: []string{"GH-1", "--every", "soon"}, wantErr: "positive duration"},
		{args: []string{"GH-1", "--answer"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "--force"}, wantErr: "unknown flag"},
		{args: []string{"GH-1", "finish", "ship"}, wantErr: "ends the chain"},
		{args: []string{"GH-1", "ship", "ship"}, wantErr: "twice"},
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

		if strings.Join(a.tasks, " ") != c.tasks || a.span != c.span || strings.Join(a.chain, " ") != c.chain ||
			strings.Join(a.answers, "|") != c.answers || a.every != c.every {
			t.Errorf("%q = %+v", c.args, a)
		}
	}
}

func TestParseChain(t *testing.T) {
	chain, err := parseChain(facts.DefaultDriveChain)
	if err != nil || strings.Join(chain, " ") != "plan judge ship review resolve finish" {
		t.Errorf("default = %q, %v", chain, err)
	}

	if chain, err := parseChain(
		"- `judge` -> ship, finish",
	); err != nil ||
		strings.Join(chain, " ") != "judge ship finish" {
		t.Errorf("declared = %q, %v", chain, err)
	}

	for _, bad := range []string{"", "start → ship", "ship → deploy"} {
		if _, err := parseChain(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestSkillPrompt(t *testing.T) {
	dep := &dependency{task: "GH-7", branch: "GH-7/base"}

	cases := map[string]string{
		skillPrompt("plan", "GH-1", nil, false):   "/f10:plan GH-1 && " + defaults + " --driven",
		skillPrompt("ship", "GH-1", nil, false):   "/f10:ship GH-1 && reuse the saved plan && " + defaults + " --driven",
		skillPrompt("review", "GH-1", nil, true):  "/f10:review GH-1 ci --driven",
		skillPrompt("review", "GH-1", nil, false): "/f10:review GH-1 --driven",
		skillPrompt("judge", "GH-1", dep, false):  "/f10:judge GH-1 --driven",
	}

	for got, want := range cases {
		if got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}

	if p := skillPrompt("ship", "GH-1", dep, false); !strings.Contains(p, "GH-1 depends on GH-7") {
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

// wave is the fixture for the issue's five tasks: GH-12, GH-13 and GH-14
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

	fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref) error {
		fx.f.calls = append(fx.f.calls, "finish "+task.ID)
		fx.infos[task.ID] = taskInfo{closed: true}

		return nil
	}

	return fx
}

// The issue's acceptance shape runs as three waves: GH-11 alone, then
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

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
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

			fx.in.finish = func(_ context.Context, _ io.Writer, task ref.Ref) error {
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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, false), "{}")
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
