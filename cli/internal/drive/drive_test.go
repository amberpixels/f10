package drive

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A driveFixture is the fakes, a tracker answering from a map, a clock
// that moves only when drive sleeps, and a hook per sleep so a test can
// change what Herdr and the run state say between two polls.
type driveFixture struct {
	f      *testkit.Fakes
	in     *DriveInput
	infos  map[string]hostops.TaskInfo
	clock  time.Time
	sleeps int
	onTick map[int]func()
	out    bytes.Buffer
}

func newDriveFixture(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := &driveFixture{
		f:      testkit.NewFakes(t),
		infos:  map[string]hostops.TaskInfo{},
		clock:  time.Unix(1_800_000_000, 0),
		onTick: map[int]func(){},
	}
	fx.f.Has["wt"] = true
	// no base branch has a commit or a head until a test says so
	fx.f.Absent = append(fx.f.Absent, "git rev-list --count ", "git rev-parse -q --verify refs/heads/",
		"git rev-parse -q --verify refs/remotes/origin/", "git reflog show ",
		// the state a reconcile reads: no merge or rebase in progress, a clean tree
		"git rev-parse -q --verify MERGE_HEAD", "git status --porcelain")
	// a catchup the drive sends records where it starts from: the default
	// branch is main, local only, and the branch merges with it cleanly
	// until a test says otherwise
	fx.f.Script(testkit.HeadRef, "origin/main")
	fx.f.Script("git rev-parse -q --verify refs/heads/main", "main0")
	fx.f.Script("git rev-parse -q --verify HEAD", "head0")
	fx.f.Script("git merge-tree --write-tree --no-messages main0 HEAD", "tree")

	// a test names the skills after the ids to run the machinery on any
	// skill; drive itself reads them off the flow
	testChainSkills := []string{"plan", "judge", "ship", "review", "resolve", "finish"}

	var chain, rest []string

	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(testChainSkills, args[i]):
			chain = append(chain, args[i])
		case args[i] == "--answer" || args[i] == "--model" || args[i] == "--agent" ||
			args[i] == "--every" || args[i] == "--flow":
			rest = append(rest, args[i], args[i+1])
			i++
		default:
			rest = append(rest, args[i])
		}
	}

	a, err := drivectl.ParseDriveArgs(rest)
	if err != nil {
		t.Fatal(err)
	}

	fx.in = &DriveInput{
		Main:     "/code/repo",
		Storage:  testkit.Storage,
		Args:     a,
		Argv:     args,
		Sequence: func(runplan.ShipFlow) []string { return chain },
		StateDir: t.TempDir(),
		TTL:      state.DefaultTTL,
		Tracker: stack.Tracker{
			Resolve: func(ctx context.Context, tok string) (ref.Ref, error) {
				return ref.Lookup{Prefix: "GH"}.Find(ctx, tok)
			},
			Info: func(_ context.Context, r ref.Ref) (hostops.TaskInfo, error) {
				info, ok := fx.infos[r.ID]
				if !ok {
					return hostops.TaskInfo{}, errors.New("no such issue")
				}

				return info, nil
			},
			Storage: storage.Root(t.TempDir()),
		},
		Now: func() time.Time { return fx.clock },
		Sleep: func(_ context.Context, d time.Duration) error {
			fx.sleeps++
			fx.clock = fx.clock.Add(d)

			if hook := fx.onTick[fx.sleeps]; hook != nil {
				hook()
			}

			return nil
		},
		Finish: func(context.Context, io.Writer, DriveFinish) error {
			t.Fatal("finish ran although the chain has none")

			return nil
		},
		Catchup: proseCatchup(),
	}

	fx.in.Out = &fx.out

	// what the engine reads after an agent's catchup turn: the base held,
	// no conflict marker left
	fx.f.Script("git merge-base --is-ancestor main0 HEAD", "")
	fx.f.Script("git diff --name-only --diff-filter=d -z main0...HEAD", "")

	return fx
}

// proseCatchup is the engine for a project whose verify is prose: git's
// integration scripted clean onto main at main0, verify left to the agent.
func proseCatchup() *stack.CatchupEngine {
	plan := stack.CatchupPlan{
		Branch: "GH-1",
		Base:   "main",
		SHA:    "main0",
		Found:  stack.FoundDefault,
		Method: stack.MethodRebase,
	}

	return &stack.CatchupEngine{
		AgentVerifies: "the Verify heading is prose, not commands",
		Integrate: func(context.Context, stack.CatchupInput, func(stack.CatchupPlan) []string) (stack.CatchupPlan, bool, error) {
			return plan, false, nil
		},
		Base: func(context.Context, stack.CatchupInput) (stack.CatchupPlan, error) { return plan, nil },
	}
}

// catchupTurn is the prompt the default engine sends after its clean
// integration: verify, left to the agent.
func catchupTurn(id string) string {
	return "herdr agent prompt " + testkit.AgentOf(id) + " " +
		Steered("/f10:catchup --driven", proseCatchup().CleanSteering("main"))
}

// oneAttempt halts a task on its first failed attempt, for a test whose
// subject is the reason a failure gives, not the attempt rule.
func (fx *driveFixture) oneAttempt(t *testing.T) {
	t.Helper()

	fx.in.Attempts = 1
}

func (fx *driveFixture) drive(t *testing.T) error {
	t.Helper()

	return Drive(t.Context(), &fx.out, fx.in)
}

// task scripts a task the tracker knows: its branch lookups answering
// heads (empty for a task not started), and nothing recorded on a branch
// named after it.
func (fx *driveFixture) task(id string, info hostops.TaskInfo, branches string) {
	fx.infos[id] = info
	fx.f.Script(testkit.RefsKey("refs/heads/", id), branches)
	fx.f.Script(testkit.RefsKey("refs/remotes/origin/", id), "")
}

// opens scripts start's create path for a new task under worktrunk.
func (fx *driveFixture) opens(id, ws, pane string) {
	fx.opensOn(id, ws, pane, "")
}

// opensOn is opens with the agent started on profile, as typed, "" for none.
func (fx *driveFixture) opensOn(id, ws, pane, profile string) {
	agent := testkit.AgentOf(id)

	var p agentctl.Profile

	started := "herdr agent start " + agent + " --kind claude --pane " + pane
	if profile != "" {
		parsed, err := agentctl.ParseProfile(profile)
		if err != nil {
			fx.f.T.Fatal(err)
		}

		p, _, _ = parsed.Resolve()
		started += " -- " + strings.Join(p.Args(), " ")
	}

	fx.f.Script("wt switch --no-cd --yes --format json --create "+id, "{}")
	fx.f.Script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
		`{"result":{"workspace":{"workspace_id":"`+ws+`"},"root_pane":{"pane_id":"`+pane+`"},"already_open":false}}`)
	fx.f.Script(started, "{}")
}

// expects scripts the prompt for one skill and the record of it done.
func (fx *driveFixture) expects(id, skill string) {
	prompt := SkillPrompt(
		skill,
		id,
		nil,
		runplan.ShipFlow{},
		slices.Contains(fx.in.SequenceOf(runplan.ShipFlow{}), "finish"),
	)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf(id)+" "+prompt, "{}")
}

// mergeable scripts the check before each task's finish: origin's copy of
// its branch merges cleanly into origin/main.
func (fx *driveFixture) mergeable(ids ...string) {
	fx.f.Script(testkit.HeadRef, "origin/main")
	fx.f.Script("git fetch origin", "")

	for _, id := range ids {
		fx.f.Script("git rev-parse -q --verify origin/"+id, "pushed-"+id)
		fx.f.Script("git merge-tree --write-tree origin/main pushed-"+id, "")
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

	if err := os.WriteFile(filepath.Join(fx.in.StateDir, "s-"+id+".state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeShipStop records GH-1's ship stopped at the leaf in the default
// flow's pipeline, at place at, with the note and next it left; a stop
// naming the catchup the guard asks for is the guard's, and says so.
func (fx *driveFixture) writeShipStop(t *testing.T, status, leaf string, at int, next string) {
	t.Helper()

	stop := ""
	if next == catchupNext && (status == "failed" || status == "partial") {
		stop = "stop " + state.StopGuard + "\n"
	}

	body := fmt.Sprintf(
		"v 1\ntask GH-1\nroot /code/repo.GH-1\ncapture prior\nplan done\nship %s\nleaf %s\nat %d\n"+
			"pipeline judge → implement → cleanup → pr → review:local → judge → pr\n"+
			"note conflict with main\nnext %s\nask \n%supdated %d\n",
		status,
		leaf,
		at,
		next,
		stop,
		fx.clock.Unix(),
	)

	if err := os.WriteFile(filepath.Join(fx.in.StateDir, "s-GH-1.state"), []byte(body), 0o644); err != nil {
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

// FT-21's acceptance run, no dependency between the tasks: both agents are
// prompted before either finishes a skill, and the report has a row per task.
func TestDriveRunsIndependentTasksTogether(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "ship")
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "second"}, "")
	fx.f.Script(testkit.Worktrees,
		testkit.Porcelain(),
		testkit.Porcelain("GH-1", "/code/repo.GH-1"),
		testkit.Porcelain("GH-1", "/code/repo.GH-1"),
		testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.opens("GH-2", "ws:GH-2", "pane:2")

	for _, id := range []string{"GH-1", "GH-2"} {
		for _, skill := range []string{"judge", "ship"} {
			fx.expects(id, skill)
		}
	}

	// the looks before the two judge prompts, a tick mid-turn, a tick after
	// with the looks before the two ship prompts, then the same for ship
	fx.f.Script(testkit.WsList,
		unopened, listing("GH-1", "idle"), unopened, listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "working", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "idle", "GH-2", "idle"), listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "working", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"--create GH-1",
		testkit.AgentOf("GH-1") + " /f10:judge",
		"--create GH-2",
		testkit.AgentOf("GH-2") + " /f10:judge",
		testkit.AgentOf("GH-1") + " /f10:ship",
		testkit.AgentOf("GH-2") + " /f10:ship",
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
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	var moved []string

	fx.in.Started = func(_ context.Context, task ref.Ref) error {
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
		recorded     string // the profile GH-1's branch records
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
		{
			name: "profiles", args: []string{"GH-1:opus:max", "GH-2", "--agent", "sonnet:low", "judge"},
			model1: "opus:max", model2: "sonnet:low",
			out: "GH-1: branch GH-1 at /code/repo.GH-1, workspace ws:GH-1 on opus:max",
		},
		{
			name: "a rerun keeps the branch's record", args: []string{"GH-1", "GH-2", "judge"},
			model1: "claude:haiku:low", recorded: "claude:haiku:low", out: "workspace ws:GH-1 on haiku:low",
		},
		{
			name: "a flag wins over the record", args: []string{"GH-1", "GH-2", "--agent", "opus", "judge"},
			model1: "opus", model2: "opus", recorded: "claude:haiku:low", notWant: "haiku",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newDriveFixture(t, c.args...)
			fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
			fx.task("GH-2", hostops.TaskInfo{Body: "second"}, "")
			fx.f.Script(testkit.Worktrees,
				testkit.Porcelain(),
				testkit.Porcelain("GH-1", "/code/repo.GH-1"),
				testkit.Porcelain("GH-1", "/code/repo.GH-1"),
				testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			if c.recorded != "" {
				fx.f.Meta.Seed("GH-1", branchmeta.Agent, c.recorded)
			}

			fx.opensOn("GH-1", "ws:GH-1", "pane:1", c.model1)
			fx.opensOn("GH-2", "ws:GH-2", "pane:2", c.model2)
			fx.expects("GH-1", "judge")
			fx.expects("GH-2", "judge")
			fx.f.Script(testkit.WsList,
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

	for _, c := range fx.f.Calls {
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
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.Meta.Seed("GH-1", branchmeta.Chain, "judge") // an older drive's
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]") }

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitAsk {
		t.Fatalf("err = %v, want exit %d", err, ui.ExitAsk)
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

	if fx.f.Called("set branch.GH-1." + string(branchmeta.Drive)) {
		t.Error("an asked skill was recorded as done")
	}

	if fx.f.Called("set branch.GH-1." + string(branchmeta.Chain)) {
		t.Error("drive recorded a chain on the branch; it reads the skills off the flow")
	}

	if !fx.f.Meta.Unsetted("GH-1", branchmeta.Chain) {
		t.Error("drive left the chain an older drive stored on the branch")
	}
}

// The same command with --answer resumes the same task: the answer goes to
// the agent as plain text, and the skill finishes.
func TestDriveResumesWithTheAnswer(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge", "--answer", "1. proceed")
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.Script("herdr agent get pane:1", testkit.AgentFound)
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" 1. proceed", "{}")

	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(fx.out.String(), "GH-1:    done: judge") || strings.Contains(fx.out.String(), "unused") {
		t.Errorf("output = %s", fx.out.String())
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:judge") {
		t.Error("the skill was prompted again instead of answered")
	}
}

// merge-tree's conflicted stages as the drive reads them: a.go conflicting
// one way, a.go with the target's side changed, and b.go.
const (
	aOneWay  = "t\n100644 a1 1\ta.go\n100644 a2 2\ta.go\n100644 a3 3\ta.go\n"
	aChanged = "t\n100644 a1 1\ta.go\n100644 a2x 2\ta.go\n100644 a3 3\ta.go\n"
	bOneWay  = "t\n100644 b1 1\tb.go\n100644 b2 2\tb.go\n100644 b3 3\tb.go\n"
)

// catchupNext is the next line the catchup guard leaves on a driven ship.
const catchupNext = "run /f10:catchup --driven in the worktree, then rerun the drive"

// adoptsGH1 scripts the rerun's open of GH-1's worktree, its agent found.
func (fx *driveFixture) adoptsGH1() {
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.Script("herdr agent get pane:1", testkit.AgentFound)
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")
}

// A ship the catchup guard stopped resumes at that entry once a catchup
// settled the branch, since the stale stop is not a halt; a failed catchup
// resumes at the entry it guarded, and a resumed ship that stops again halts.
func TestDriveResumesAShipStopACatchupSettled(t *testing.T) {
	cases := map[string]struct {
		status, leaf string
		at           int
		prompt       string
		again        bool
	}{
		"guard stop":     {"partial", "pr", 7, "/f10:ship GH-1 resume at pr (entry 7) --driven", false},
		"failed catchup": {"failed", "catchup", 4, "/f10:ship GH-1 resume at pr (entry 4) --driven", false},
		"stops again":    {"partial", "pr", 7, "/f10:ship GH-1 resume at pr (entry 7) --driven", true},
		"single entry":   {"partial", "implement", 2, "/f10:ship GH-1 resume at implement --driven", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			if tc.again {
				fx.oneAttempt(t)
			}

			fx.adoptsGH1()
			fx.writeShipStop(t, tc.status, tc.leaf, tc.at, catchupNext)

			fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+tc.prompt, "{}")

			fx.f.Script(
				testkit.WsList,
				unopened,
				listing("GH-1", "idle"),
				listing("GH-1", "working"),
				listing("GH-1", "idle"),
			)
			fx.onTick[2] = func() {
				if tc.again {
					fx.writeShipStop(t, "partial", "pr", 7, catchupNext)

					return
				}

				fx.writeRun(t, "done", "", "", "")
			}

			err := fx.drive(t)

			if !fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + tc.prompt) {
				t.Errorf("ship was not prompted with %q: %q\n%s", tc.prompt, fx.f.Calls, fx.out.String())
			}

			if tc.again {
				if testkit.ExitCode(err) != ui.ExitHalted ||
					!strings.Contains(fx.out.String(), "GH-1 halted at ship: conflict with main") {
					t.Errorf("err = %v\n%s", err, fx.out.String())
				}

				return
			}

			if err != nil || !strings.Contains(fx.out.String(), "GH-1:    done: ship") {
				t.Errorf("err = %v\n%s", err, fx.out.String())
			}
		})
	}
}

// Every other stop still halts on a rerun, its note and next printed and
// nothing prompted: a branch catchup has not settled, a stop catchup does
// not fix, or an entry the guard does not run before.
func TestDriveHaltsAShipStopNoCatchupSettled(t *testing.T) {
	cases := map[string]struct {
		status, leaf string
		next         string
		git          [2]string // a command the branch answers otherwise
		landed       string    // the landed mark on the branch
	}{
		"merge in progress": {
			"partial",
			"pr",
			catchupNext,
			[2]string{"git rev-parse -q --verify MERGE_HEAD", "abc"},
			"",
		},
		"dirty tree": {"partial", "pr", catchupNext, [2]string{"git status --porcelain", " M a.go"}, ""},
		"red verify": {"failed", "implement", "fix the failing test, then rerun the drive", [2]string{}, ""},
		"catchup failed, merge left": {
			"failed",
			"catchup",
			catchupNext,
			[2]string{"git rev-parse -q --verify MERGE_HEAD", "abc"},
			"",
		},
		"review advises catchup": {"partial", "review:local", catchupNext, [2]string{}, ""},
		"judge stop, marked": {
			"blocked",
			"judge",
			"answer the judge, then rerun the drive",
			[2]string{},
			"main",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()

			if tc.git[0] != "" {
				fx.f.Script(tc.git[0], tc.git[1])
			}

			if tc.landed != "" {
				fx.f.Meta.Seed("GH-1", branchmeta.Landed, tc.landed)
			}

			fx.writeShipStop(t, tc.status, tc.leaf, 4, tc.next)
			fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

			err := fx.drive(t)
			if testkit.ExitCode(err) != ui.ExitHalted {
				t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, fx.out.String())
			}

			out := fx.out.String()
			if !strings.Contains(out, ": conflict with main") || !strings.Contains(out, "next: "+tc.next) {
				t.Errorf("output = %s", out)
			}

			if fx.f.Called("herdr agent prompt") {
				t.Errorf("prompted on an unsettled stop: %q", fx.f.Calls)
			}
		})
	}
}

// A ship whose catchup guard conflicted gets a catchup from the drive and
// resumes where it stopped; a guard conflict after it counts by its
// fingerprint, so main moving or other files conflicting catches up again,
// and the same conflict on the same target halts at the attempt limit.
func TestDriveSettlesAShipGuardConflict(t *testing.T) {
	catchup := catchupTurn("GH-1")
	resume := "herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"
	conflicts := "git merge-tree --write-tree --no-messages "

	// conflictsAgain is what a resumed ship whose guard conflicted once more
	// leaves: the stop, the mark, and git's view of main and the branch
	conflictsAgain := func(main, files string) func(fx *driveFixture, t *testing.T) {
		return func(fx *driveFixture, t *testing.T) {
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
			fx.f.Script("git rev-parse -q --verify refs/heads/main", main)
			fx.f.Script("git rev-parse -q --verify HEAD", "head1")
			fx.f.Fail(conflicts+main+" HEAD", shell.Result{Code: 1, Stdout: files})
		}
	}

	cases := map[string]struct {
		rerun bool
		// after is what the catchup's turn leaves: the run, and the mark
		after func(fx *driveFixture, t *testing.T)
		// again is what the resumed ship's turn leaves
		again func(fx *driveFixture, t *testing.T)
		// later is what the turns after that leave, one per turn
		later    []func(fx *driveFixture, t *testing.T)
		code     int
		want     []string
		catchups int
		// full runs at the default attempt limit, not one attempt
		full bool
	}{
		"settled": {
			after: restored,
			again: func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			want: []string{
				"the catchup guard conflicted, catching up",
				"the branch is settled: resume at pr",
				"GH-1:    done: ship",
			},
		},
		"settled, never stopped": {
			// a catchup that never stopped writes only its done signal
			after: func(fx *driveFixture, t *testing.T) {
				fx.f.Meta.Script("GH-1", branchmeta.Landed, "")
				fx.reports(t, fx.clock)
			},
			again: func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			want:  []string{"the branch is settled: resume at pr", "GH-1:    done: ship"},
		},
		"rerun": {
			rerun: true,
			after: restored,
			again: func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			want:  []string{"the branch is settled: resume at pr", "GH-1:    done: ship"},
		},
		"unsettled": {
			// reported, but the mark is still there
			after: func(fx *driveFixture, t *testing.T) {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
			},
			code: ui.ExitHalted,
			want: []string{"halted at catchup: the catchup did not end clean", "next: settle it with /f10:catchup in"},
		},
		"asks": {
			after: func(fx *driveFixture, t *testing.T) {
				fx.writeRun(t, "blocked", "catchup", "", "1. Keep both renames? [both | ours]")
			},
			code: ui.ExitAsk,
			want: []string{"GH-1 asks at catchup: 1. Keep both renames?"},
		},
		"conflicts again, main did not move": {
			after: restored,
			again: conflictsAgain("main0", aOneWay),
			code:  ui.ExitHalted,
			want: []string{
				"GH-1 halted at ship: GH-1 still conflicts with main after a catchup: a.go",
				"next: settle it with /f10:catchup in /code/repo.GH-1, then rerun the drive",
			},
			catchups: 1,
		},
		"conflicts again three times, main did not move": {
			// a clean catchup between the conflicts does not clear the count
			after: restored,
			again: conflictsAgain("main0", aOneWay),
			later: []func(fx *driveFixture, t *testing.T){
				restored, conflictsAgain("main0", aOneWay), restored, conflictsAgain("main0", aOneWay),
			},
			code: ui.ExitHalted,
			full: true,
			want: []string{
				"catchup failed 3 times in a row with nothing changed",
				"attempt 3 at",
				"next: settle it with /f10:catchup in /code/repo.GH-1, then rerun the drive",
			},
			catchups: 3,
		},
		"conflicts again on the same files the same way": {
			after: restored,
			again: conflictsAgain("main1", aOneWay),
			code:  ui.ExitHalted,
			want: []string{
				"GH-1 halted at ship: GH-1 still conflicts with main after a catchup: a.go",
			},
			catchups: 1,
		},
		"conflicts again on the same file, main changed it": {
			after: restored,
			again: conflictsAgain("main1", aChanged),
			later: []func(fx *driveFixture, t *testing.T){
				restored,
				func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			},
			want:     []string{"the branch is settled: resume at pr", "GH-1:    done: ship"},
			catchups: 2,
		},
		"conflicts again, main moved": {
			after: restored,
			again: conflictsAgain("main1", bOneWay),
			later: []func(fx *driveFixture, t *testing.T){
				restored,
				func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			},
			want:     []string{"the branch is settled: resume at pr", "GH-1:    done: ship"},
			catchups: 2,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			if tc.code == ui.ExitHalted && !tc.full {
				fx.oneAttempt(t)
			}

			fx.adoptsGH1()
			fx.f.Script(catchup, "{}")
			fx.f.Script(resume, "{}")

			// the first catchup is recorded against main0, a.go conflicting one way
			fx.f.Fail(conflicts+"main0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})

			// the open and the first send, then each turn working and at its
			// end; a send after a turn reads the tick's look
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.Script(testkit.WsList, unopened, idle, working, idle, working, idle, working, idle,
				working, idle, working, idle, working, idle, working, idle)
			tick := 2

			if tc.rerun {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
				fx.f.Script(testkit.WsList, unopened, idle, working, idle, working, idle)
				tick = 0
			} else {
				ship := SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{}, false)
				fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+ship, "{}")
				fx.onTick[2] = func() {
					fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
					fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
				}
			}

			fx.onTick[tick+2] = func() { tc.after(fx, t) }
			if tc.again != nil {
				fx.onTick[tick+4] = func() { tc.again(fx, t) }
			}

			for i, later := range tc.later {
				fx.onTick[tick+6+2*i] = func() { later(fx, t) }
			}

			err := fx.drive(t)
			out := fx.out.String()

			if (tc.code == 0 && err != nil) || (tc.code != 0 && testkit.ExitCode(err) != tc.code) {
				t.Fatalf("err = %v, want exit %d\n%s", err, tc.code, out)
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}

			if n, want := countCalls(fx.f.Calls, catchup), max(tc.catchups, 1); n != want {
				t.Errorf("catchup prompted %d times, want %d: %q", n, want, fx.f.Calls)
			}

			if tc.again == nil && fx.f.Called(resume) {
				t.Errorf("ship resumed on a catchup that did not end clean: %q", fx.f.Calls)
			}
		})
	}
}

// An ask ends the drive only once every active task is held on a base, or
// none is active; a held task with no ask anywhere keeps it polling.
func TestStoppingOnAnAsk(t *testing.T) {
	asked := func() *DriveRun {
		return &DriveRun{Phase: AtRunning, End: &DriveResult{Code: ui.ExitAsk}}
	}
	moving := func() *DriveRun { return &DriveRun{Phase: AtRunning} }
	parked := func() *DriveRun { return &DriveRun{Phase: AtParked} }
	waiting := func() *DriveRun { return &DriveRun{Phase: AtWaiting} }

	cases := map[string]struct {
		runs []*DriveRun
		want bool
	}{
		"asked, another moving":  {[]*DriveRun{asked(), moving()}, false},
		"asked, another parked":  {[]*DriveRun{asked(), parked()}, true},
		"asked, another waiting": {[]*DriveRun{asked(), waiting()}, true},
		"asked, nothing active":  {[]*DriveRun{asked()}, true},
		"no ask, one parked":     {[]*DriveRun{parked()}, false},
		"no ask, one moving":     {[]*DriveRun{moving()}, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Stopping(tc.runs); got != tc.want {
				t.Errorf("stopping = %v, want %v", got, tc.want)
			}
		})
	}
}

// An ask in one task does not end the drive while another can move: a ship
// the catchup guard stopped gets its catchup and its resume, under either
// mark, and the drive exits on the ask once the rest is through.
func TestDriveCatchesUpAGuardStopWhileAnotherTaskAsks(t *testing.T) {
	cases := map[string]struct{ key, target string }{
		"landed":     {string(branchmeta.Landed), "main"},
		"base moved": {string(branchmeta.Moved), "base1"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			catchup := catchupTurn("GH-1")
			resume := "herdr agent prompt " + testkit.AgentOf(
				"GH-1",
			) + " /f10:ship GH-1 resume at pr (entry 4) --driven"

			fx := newDriveFixture(t, "GH-1", "GH-2", "ship")
			fx.adoptsGH1()
			fx.task("GH-2", hostops.TaskInfo{Body: "second"}, testkit.Heads("GH-2"))
			fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			fx.f.Script(
				"herdr worktree open --cwd /code/repo --path /code/repo.GH-2 --label GH-2",
				`{"result":{"workspace":{"workspace_id":"ws:GH-2"},"root_pane":{"pane_id":"pane:2"},"already_open":true}}`,
			)
			fx.f.Script("herdr agent get pane:2", testkit.AgentFound)
			fx.f.Script(catchup, "{}")
			fx.f.Script(resume, "{}")

			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.Meta.Seed("GH-1", branchmeta.Key(tc.key), tc.target)

			if tc.key == string(branchmeta.Moved) {
				fx.f.Meta.Seed("GH-1", branchmeta.AfterBranch, "GH-0")
				fx.f.Script("git merge-tree --write-tree --no-messages base1 HEAD", "tree")
			}

			fx.writeRunFor(t, "GH-2", "blocked", "ship", "", "1. Keep the old flag? [yes | no]")

			idle := listing("GH-1", "idle", "GH-2", "idle")
			working := listing("GH-1", "working", "GH-2", "idle")
			fx.f.Script(testkit.WsList, unopened, idle, unopened, idle, working, idle, idle, working, idle)

			fx.onTick[2] = func() {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
				fx.f.Meta.Script("GH-1", branchmeta.Key(tc.key), "")
			}
			fx.onTick[4] = func() { fx.writeRun(t, "done", "", "", "") }

			err := fx.drive(t)
			out := fx.out.String()

			if testkit.ExitCode(err) != ui.ExitAsk {
				t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitAsk, out)
			}

			for _, call := range []string{catchup, resume} {
				if !fx.f.Called(call) {
					t.Errorf("%q was not prompted: %q\n%s", call, fx.f.Calls, out)
				}
			}

			for _, want := range []string{"GH-1:    done: ship", "GH-2:    asked at ship", "answer with:"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}

			// the ask is told when it is found, not held for the exit
			if ask, resumed := strings.Index(
				out,
				"GH-2 asks at ship",
			), strings.Index(
				out,
				"resume at pr",
			); ask < 0 || resumed < 0 ||
				ask > resumed {
				t.Errorf("the ask was not told before GH-1 resumed:\n%s", out)
			}
		})
	}
}

// restored is what a driven catchup that stopped, then ended clean, leaves:
// the mark cleared, the ship stop it found written back, and its done signal.
func restored(fx *driveFixture, t *testing.T) {
	t.Helper()
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.reports(t, fx.clock)
	fx.f.Meta.Script("GH-1", branchmeta.Landed, "")
}

// reports writes the catchup's done signal, dated at, into GH-1's run file,
// updated at the fixture clock: what the step's report leaves.
func (fx *driveFixture) reports(t *testing.T, at time.Time) {
	t.Helper()
	fx.reportsFor(t, "GH-1", at)
}

// reportsFor writes a task's catchup done signal into its run file, the
// rest of the run kept, or a fresh run when the agent wrote none before.
func (fx *driveFixture) reportsFor(t *testing.T, id string, at time.Time) {
	t.Helper()

	path := filepath.Join(fx.in.StateDir, "s-"+id+".state")
	kept := []string{"v 1", "task " + id, "root /code/repo." + id}

	if raw, err := os.ReadFile(path); err == nil {
		kept = nil

		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			key, _, _ := strings.Cut(line, " ")
			if key != "done" && key != "done_at" && key != "updated" {
				kept = append(kept, line)
			}
		}
	}

	kept = append(kept,
		"done catchup",
		"done_at "+strconv.FormatInt(at.Unix(), 10),
		"updated "+strconv.FormatInt(fx.clock.Unix(), 10),
	)

	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A catchup idle before its report is not done: only the signal its report
// writes, dated after the prompt, counts, never one an earlier catchup left.
func TestDriveWaitsOnACatchupThatHasNotReported(t *testing.T) {
	resume := "herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"

	for name, stale := range map[string]bool{"no signal yet": false, "stale signal": true} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
			fx.f.Script(catchupTurn("GH-1"), "{}")
			fx.f.Script(resume, "{}")

			if stale {
				fx.reports(t, fx.clock.Add(-time.Hour))
			}

			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			// the catchup's turn ends idle at the second look and stays so
			// until the report lands two looks later; then the look before
			// the resumed ship, its turn and its end
			fx.f.Script(testkit.WsList, unopened, idle, working, idle, idle, idle, idle, working, idle)

			doneEarly := false
			fx.onTick[3] = func() { doneEarly = strings.Contains(fx.out.String(), "catchup: done") }
			fx.onTick[4] = func() {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
				fx.f.Meta.Script("GH-1", branchmeta.Landed, "")
			}
			fx.onTick[6] = func() { fx.writeRun(t, "done", "", "", "") }

			err := fx.drive(t)
			out := fx.out.String()

			if doneEarly {
				t.Errorf("the catchup was read as done before it reported:\n%s", out)
			}

			if err != nil || !strings.Contains(out, "GH-1 catchup: done") ||
				!strings.Contains(out, "GH-1:    done: ship") {
				t.Errorf("err = %v\n%s", err, out)
			}

			if !fx.f.Called(resume) {
				t.Errorf("ship was not resumed after the catchup reported: %q", fx.f.Calls)
			}
		})
	}
}

// A turn the drive attached to is read by what it reported, not by the skill
// the drive meant to send: a catchup that ended clean, before or after the
// attach, resumes ship at the stopped entry, and an attached ship stays a ship.
func TestDriveFollowsAnAttachedCatchup(t *testing.T) {
	resume := "herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 7) --driven"
	working, idle := listing("GH-1", "working"), listing("GH-1", "idle")

	cases := map[string]struct {
		before   bool // the catchup reported before the drive attached
		stale    bool // a catchup reported before the attached turn's prompt
		ship     bool // the attached turn was a ship, which reports done
		unclean  bool // the catchup left a dirty tree
		want     []string
		resumed  bool
		haltCode bool
	}{
		"reports after the attach": {want: []string{"GH-1 catchup: done", "GH-1:    done: ship"}, resumed: true},
		"reported before the attach": {
			before: true, want: []string{"GH-1 catchup: done", "GH-1:    done: ship"}, resumed: true,
		},
		"not clean": {
			unclean: true, want: []string{"GH-1 halted at catchup: the catchup did not end clean"}, haltCode: true,
		},
		"a ship turn": {ship: true, want: []string{"GH-1 ship: done", "GH-1:    done: ship"}},
		"a ship turn after a catchup": {
			stale: true, ship: true, want: []string{"GH-1 ship: done", "GH-1:    done: ship"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			if tc.unclean {
				fx.oneAttempt(t)
			}

			fx.adoptsGH1()
			fx.f.Script(resume, "{}")

			if tc.unclean {
				fx.f.Script("git status --porcelain", " M a.go")
			}

			// the turn's prompt came before this drive
			mark := fx.clock.Add(-time.Minute)
			if err := state.MarkPrompt(fx.in.StateDir, "s-GH-1", "/code/repo.GH-1", mark); err != nil {
				t.Fatal(err)
			}

			fx.writeShipStop(t, "partial", "pr", 7, catchupNext)

			switch {
			case tc.before:
				fx.reports(t, fx.clock.Add(-30*time.Second))
			case tc.stale:
				fx.reports(t, mark.Add(-30*time.Second))
			}

			// the attach, the turn, its end; then the look before the resumed
			// ship, its turn and its end
			fx.f.Script(testkit.WsList, unopened, working, working, idle, idle, working, idle)
			fx.onTick[2] = func() {
				switch {
				case tc.ship:
					fx.writeRun(t, "done", "", "", "")

					if tc.stale {
						fx.reports(t, mark.Add(-30*time.Second))
					}
				case !tc.before:
					fx.writeShipStop(t, "partial", "pr", 7, catchupNext)
					fx.reports(t, fx.clock)
				}
			}
			fx.onTick[4] = func() { fx.writeRun(t, "done", "", "", "") }

			err := fx.drive(t)
			out := fx.out.String()

			if !strings.Contains(out, "GH-1 ship: attached to the turn in progress") {
				t.Errorf("the drive did not attach:\n%s", out)
			}

			if tc.haltCode != (testkit.ExitCode(err) == ui.ExitHalted) || !tc.haltCode && err != nil {
				t.Errorf("err = %v\n%s", err, out)
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}

			if tc.ship && strings.Contains(out, "the turn was a catchup") {
				t.Errorf("a ship turn was read as a catchup:\n%s", out)
			}

			if tc.resumed != fx.f.Called(resume) {
				t.Errorf("ship resumed = %v, want %v: %q\n%s", !tc.resumed, tc.resumed, fx.f.Calls, out)
			}
		})
	}
}

// A catchup's agent idle past the grace with no report and nothing settled
// is read from git: what is left is a failed attempt, and the last one halts
// the task at catchup, the halt in the ledger.
func TestDriveHaltsACatchupThatStallsWithNothingSettled(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.oneAttempt(t)
	fx.adoptsGH1()
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
	fx.f.Script(catchupTurn("GH-1"), "{}")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	var recorded []ledger.Event

	fx.in.Record = func(_ context.Context, _ string, e ledger.Event) { recorded = append(recorded, e) }

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, out)
	}

	if !strings.Contains(out, "GH-1 halted at catchup: the catchup did not end clean: the branch is still marked") {
		t.Errorf("output = %s", out)
	}

	if len(recorded) != 1 || recorded[0].Kind != ledger.TaskHalted || recorded[0].Payload.Val["skill"] != "catchup" {
		t.Errorf("recorded %+v, want one halt at catchup", recorded)
	}

	if fx.sleeps < int(StallGrace/drivectl.DefaultEvery) {
		t.Errorf("halted after %d polls, before the grace ran out", fx.sleeps)
	}
}

func countCalls(calls []string, call string) int {
	n := 0

	for _, c := range calls {
		if c == call {
			n++
		}
	}

	return n
}

// An answer given to a settled catchup stop is still sent as the answer.
func TestDriveAnswersASettledCatchupStop(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship", "--answer", "go on")
	fx.adoptsGH1()
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" go on", "{}")

	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ship") {
		t.Error("ship was prompted instead of answered")
	}
}

// A judge stop, blocked with no question, halts the task with its reason,
// and the task after it is halted with it. The record keeps GH-2's base.
func TestDriveHaltsOnAJudgeStop(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge")
	fx.in.Root = t.TempDir()
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "After: GH-1"}, "")
	fx.f.Script(testkit.HeadRef, "origin/main") // GH-1 has no commit, so GH-2 waits
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	// the run's own stop is in the ledger as its claims already
	fx.in.Record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d", err, ui.ExitHalted)
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

	rec, err := drivestore.Read(fx.in.Root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if got := rec.Task("GH-2").After; got != "GH-1" {
		t.Errorf("GH-2's recorded base = %q, want GH-1", got)
	}
}

// An agent idle while its ship phase still says running, past the grace,
// stopped without reporting: the drive halts naming the workspace.
func TestDriveHaltsOnAnAgentThatStoppedWithoutReporting(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[1] = func() { fx.writeRun(t, "running", "implement", "", "") }

	var recorded []ledger.Event

	fx.in.Record = func(_ context.Context, dir string, e ledger.Event) {
		if dir != "/code/repo.GH-1" {
			t.Errorf("halt placed in %s, want the task's worktree", dir)
		}

		recorded = append(recorded, e)
	}

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d", err, ui.ExitHalted)
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

	if fx.sleeps < int(StallGrace/drivectl.DefaultEvery) {
		t.Errorf("halted after %d polls, before the grace ran out", fx.sleeps)
	}
}

// A finished task is skipped without touching Herdr; a recorded position
// resumes after the skill it names; finish runs in process and ends the
// chain.
func TestDriveSkipsFinishedAndResumesFromTheBranch(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "ship", "finish")
	fx.task("GH-1", hostops.TaskInfo{Body: "x", Closed: true}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "y\nAfter: GH-1"}, testkit.Heads("GH-2"))
	fx.f.Meta.Seed("GH-2", branchmeta.Drive, "judge")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-2", "/code/repo.GH-2"))
	fx.f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-2 --label GH-2",
		`{"result":{"workspace":{"workspace_id":"ws:GH-2"},"root_pane":{"pane_id":"pane:2"},"already_open":true}}`)
	fx.f.Script("herdr agent get pane:2", testkit.AgentFound)
	fx.expects("GH-2", "ship")
	fx.f.Script(testkit.WsList, unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))
	fx.mergeable("GH-2")

	var finished []string

	fx.in.Finish = func(_ context.Context, w io.Writer, req DriveFinish) error {
		finished = append(finished, req.Task.ID)
		fmt.Fprintln(w, "task:  "+req.Task.ID)
		req.Handoff(strings.Split(testkit.HandoffBody, "\n"))

		return nil
	}

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	out := fx.out.String()
	// the handoff waits for the report, beneath its rows
	report := out[strings.LastIndex(out, "GH-1:    already finished"):]
	for _, want := range []string{"GH-2:    finished", "GH-2 handoff:\n  ## Config\n  - set"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}

	if strings.Count(out, "GH-2 handoff:") != 1 {
		t.Errorf("the handoff printed more than once:\n%s", out)
	}

	for _, want := range []string{"GH-1:    already finished", "GH-2:    finished", "  task:  GH-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if fx.f.Called("herdr agent prompt "+testkit.AgentOf("GH-2")+" /f10:judge") ||
		fx.f.Called("wt switch --no-cd --yes --format json --create GH-1") {
		t.Errorf("calls = %q", fx.f.Calls)
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
	fx.in.Storage = storage.Root(t.TempDir())
	testkit.WritePlan(t, fx.in.Storage.Plan("GH-1"))
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	// the shared storage root sits outside the worktree, so the agent may write there
	addDir := " -- " + strings.Join(start.AgentArgs(agentctl.Profile{}, fx.in.Storage, "/code/repo.GH-1"), " ")
	fx.f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:1"+addDir, "{}")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:plan") {
		t.Errorf("the saved plan was planned again: %q", fx.f.Calls)
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
			fx.f.Meta.Seed("GH-1", branchmeta.Drive, tc.recorded)

			pos, err := fx.in.Position(
				t.Context(),
				start.OpenedTask{Branch: "GH-1", Path: "/code/repo.GH-1", Plan: tc.plan},
				tc.chain,
			)
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
	fx.task("GH-3", hostops.TaskInfo{Body: "a"}, "")
	fx.infos["GH-4"] = hostops.TaskInfo{Pull: true}
	fx.task("GH-6", hostops.TaskInfo{Body: "c"}, "")

	tasks, notes, err := fx.in.ResolveTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(tasks) != 2 || tasks[0].Ref.ID != "GH-3" || tasks[1].Ref.ID != "GH-6" {
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
	fx.infos["GH-4"] = hostops.TaskInfo{Pull: true}

	if _, _, err := fx.in.ResolveTasks(t.Context()); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("err = %v", err)
	}
}

// The graph puts a base before its dependents whatever the list's order,
// and refuses what cannot run before anything is created: a base outside
// the list not yet finished, and a cycle.
func TestDriveGraph(t *testing.T) {
	graph := func(t *testing.T, fx *driveFixture) ([]string, error) {
		t.Helper()

		tasks, _, err := fx.in.ResolveTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		order, _, err := fx.in.Graph(t.Context(), tasks)

		var ids []string
		for _, i := range order {
			ids = append(ids, tasks[i].Ref.ID)
		}

		return ids, err
	}

	t.Run("base later in the list runs first", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2", "GH-1")
		fx.task("GH-2", hostops.TaskInfo{Body: "x\nAfter: GH-1\n"}, "")
		fx.task("GH-1", hostops.TaskInfo{Body: "y"}, "")

		ids, err := graph(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-1 GH-2" {
			t.Errorf("order = %v, %v", ids, err)
		}
	})

	t.Run("waves", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-15", "GH-14", "GH-13", "GH-12", "GH-11")
		fx.task("GH-11", hostops.TaskInfo{Body: "root"}, "")
		fx.task("GH-12", hostops.TaskInfo{Body: "After: GH-11"}, "")
		fx.task("GH-13", hostops.TaskInfo{Body: "After: GH-11"}, "")
		fx.task("GH-14", hostops.TaskInfo{Body: "After: GH-11"}, "")
		fx.task("GH-15", hostops.TaskInfo{Body: "After: GH-13"}, "")

		ids, err := graph(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-11 GH-14 GH-13 GH-15 GH-12" {
			t.Errorf("order = %v, %v", ids, err)
		}
	})

	t.Run("the branch's record beats the body", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3")
		fx.task("GH-1", hostops.TaskInfo{Body: "a"}, "")
		fx.task("GH-2", hostops.TaskInfo{Body: "b"}, "")
		fx.task("GH-3", hostops.TaskInfo{Body: "After: GH-1"}, testkit.Heads("GH-3"))
		fx.f.Meta.Seed("GH-3", branchmeta.After, "GH-2")

		tasks, _, err := fx.in.ResolveTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		_, notes, err := fx.in.Graph(t.Context(), tasks)
		if err != nil || tasks[2].Base != "GH-2" {
			t.Fatalf("base = %q, %v", tasks[2].Base, err)
		}

		if len(notes) != 1 || !strings.Contains(notes[0], "the branch wins") {
			t.Errorf("notes = %q", notes)
		}
	})

	t.Run("a cycle", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3")
		fx.task("GH-1", hostops.TaskInfo{Body: "After: GH-2"}, "")
		fx.task("GH-2", hostops.TaskInfo{Body: "After: GH-1"}, "")
		fx.task("GH-3", hostops.TaskInfo{Body: "free"}, "")

		_, err := graph(t, fx)
		if err == nil || !strings.Contains(err.Error(), "circle: GH-1 after GH-2, GH-2 after GH-1") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("base outside the list, not finished", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2")
		fx.task("GH-2", hostops.TaskInfo{Body: "After: GH-9"}, "")
		fx.task("GH-9", hostops.TaskInfo{Body: "open"}, "")

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "not in the list and not finished") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.Called("wt") || fx.f.Called("herdr") {
			t.Error("something was created before the refusal")
		}
	})

	t.Run("base outside the list, finished", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2")
		fx.task("GH-2", hostops.TaskInfo{Body: "After: GH-9"}, "")
		fx.task("GH-9", hostops.TaskInfo{Closed: true}, "")

		if _, err := graph(t, fx); err != nil {
			t.Errorf("a finished base was refused: %v", err)
		}
	})
}

// A listed parent with unfinished subtasks is refused before anything is
// created; a task with none, a started one and a range member pass.
func TestDriveRefusesAParent(t *testing.T) {
	withChildren := func(fx *driveFixture, kids map[string][]stack.ChildRow) {
		fx.in.Tracker.Children = func(_ context.Context, r ref.Ref) ([]stack.ChildRow, error) {
			k, ok := kids[r.ID]
			if !ok {
				return nil, hostops.ErrNoParentLink
			}

			return k, nil
		}
	}

	t.Run("a parent with open subtasks", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", hostops.TaskInfo{Body: "the parent"}, "")
		withChildren(
			fx,
			map[string][]stack.ChildRow{"GH-1": {{ID: "GH-2"}, {ID: "GH-3", Finished: true}, {ID: "GH-4"}}},
		)

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "GH-1 is a parent of GH-2, GH-4") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.Called("wt") || fx.f.Called("herdr") {
			t.Error("something was created before the refusal")
		}
	})

	for name, kids := range map[string][]stack.ChildRow{
		"every subtask finished": {{ID: "GH-2", Finished: true}},
		"no subtasks":            {},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1")
			fx.task("GH-1", hostops.TaskInfo{Body: "free"}, "")
			withChildren(fx, map[string][]stack.ChildRow{"GH-1": kids})

			if _, _, err := fx.in.ResolveTasks(t.Context()); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}

	t.Run("no parent link", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", hostops.TaskInfo{Body: "free"}, "")
		withChildren(fx, nil)

		if _, _, err := fx.in.ResolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})

	t.Run("a started task is not asked", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		fx.task("GH-1", hostops.TaskInfo{Body: "free"}, "refs/heads/GH-1/work")
		fx.in.Tracker.Children = func(context.Context, ref.Ref) ([]stack.ChildRow, error) {
			t.Error("children asked for a started task")

			return nil, nil
		}

		if _, _, err := fx.in.ResolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})

	t.Run("a range member is not asked", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "--", "GH-2")
		fx.task("GH-1", hostops.TaskInfo{Body: "free"}, "")
		fx.task("GH-2", hostops.TaskInfo{Body: "free"}, "")
		fx.in.Tracker.Children = func(context.Context, ref.Ref) ([]stack.ChildRow, error) {
			t.Error("children asked for a range member")

			return nil, nil
		}

		if _, _, err := fx.in.ResolveTasks(t.Context()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})
}

// A flow with no pace finish word finishes; only finish off stops at ship.
func TestDriveChain(t *testing.T) {
	for _, c := range []struct {
		fl   runplan.ShipFlow
		want string
	}{
		{runplan.ShipFlow{}, "plan ship finish"},
		{runplan.ShipFlow{Finish: "on"}, "plan ship finish"},
		{runplan.ShipFlow{Finish: "off"}, "plan ship"},
		{runplan.ShipFlow{Name: "direct", Planless: true}, "ship finish"},
		{runplan.ShipFlow{Name: "direct", Planless: true, Finish: "off"}, "ship"},
	} {
		if got := strings.Join(DriveSkills(c.fl), " "); got != c.want {
			t.Errorf("%+v = %q, want %q", c.fl, got, c.want)
		}
	}
}

// Medium and a user pace with no finish dial end in finish; slow does not.
func TestDriveSkillsFinishByDefault(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}
	set := facts.NewPaceSet()
	set.Over(
		map[string]facts.Pace{"mine": {Steps: map[string]facts.Dials{"judge": {Off: true}}}},
		func(string, string) facts.Origin { return facts.DeclaredIn("test") },
	)

	for pace, want := range map[string]string{
		"medium": "plan ship finish",
		"mine":   "plan ship finish",
		"slow":   "plan ship",
	} {
		fl, err := testkit.PacedFlowIn(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(DriveSkills(fl), " "); got != want {
			t.Errorf("%s: skills %q, want %q", pace, got, want)
		}
	}
}

func TestSkillPrompt(t *testing.T) {
	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base"}

	cases := map[string]string{
		SkillPrompt("plan", "GH-1", nil, runplan.ShipFlow{}, false):              "/f10:plan GH-1 --driven",
		SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{}, false):              "/f10:ship GH-1 --driven",
		SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{Name: "local"}, false): "/f10:ship local: GH-1 --driven",
		SkillPrompt("plan", "GH-1", nil, runplan.ShipFlow{Name: "local"}, false): "/f10:plan GH-1 --driven",
		SkillPrompt("judge", "GH-1", dep, runplan.ShipFlow{}, false):             "/f10:judge GH-1 --driven",
	}

	for got, want := range cases {
		if got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}

	if p := SkillPrompt(
		"ship",
		"GH-1",
		dep,
		runplan.ShipFlow{},
		false,
	); !strings.Contains(
		p,
		"GH-1 depends on GH-7",
	) {
		t.Errorf("a dependent ship carries no contract: %q", p)
	}
}

func TestResumeCommand(t *testing.T) {
	got := ResumeCommand([]string{"GH-1", "--", "GH-3", "judge", "--answer", "1. old", "--every=5s"})
	if want := "f10 drive GH-1 -- GH-3 judge --every=5s"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The body's base becomes start's --after when it has a branch, the
// default branch when it is finished, and a refusal otherwise.
func TestBodyAfter(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	tr := fx.in.Tracker

	fx.task("GH-7", hostops.TaskInfo{Body: "base"}, testkit.Heads("GH-7/base"))

	after, note, err := stack.BodyAfter(t.Context(), tr, "/code/repo", testkit.Gh1, "After: GH-7")
	if err != nil || after != "GH-7" || !strings.Contains(note, "from the task body") {
		t.Errorf("branched base = %q, %q, %v", after, note, err)
	}

	fx.task("GH-8", hostops.TaskInfo{Closed: true}, "")

	after, note, err = stack.BodyAfter(t.Context(), tr, "/code/repo", testkit.Gh1, "After: GH-8")
	if err != nil || after != "" || !strings.Contains(note, "finished") {
		t.Errorf("finished base = %q, %q, %v", after, note, err)
	}

	fx.task("GH-9", hostops.TaskInfo{Body: "open"}, "")

	if _, _, err := stack.BodyAfter(t.Context(), tr, "/code/repo", testkit.Gh1, "After: GH-9"); err == nil ||
		!strings.Contains(err.Error(), "start GH-9 first") {
		t.Errorf("unstarted base: err = %v", err)
	}

	if after, note, err := stack.BodyAfter(
		t.Context(),
		tr,
		"/code/repo",
		testkit.Gh1,
		"no line",
	); after != "" || note != "" ||
		err != nil {
		t.Errorf("no line = %q, %q, %v", after, note, err)
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

// wave is the fixture for five tasks: GH-12, GH-13 and GH-14 after GH-11,
// GH-15 after GH-13, with finish recorded among the calls.
func wave(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, args...)
	fx.task("GH-11", hostops.TaskInfo{Body: "root"}, "")
	fx.task("GH-12", hostops.TaskInfo{Body: "After: GH-11"}, "")
	fx.task("GH-13", hostops.TaskInfo{Body: "After: GH-11"}, "")
	fx.task("GH-14", hostops.TaskInfo{Body: "After: GH-11"}, "")
	fx.task("GH-15", hostops.TaskInfo{Body: "After: GH-13"}, "")

	var pairs []string

	for i, id := range []string{"GH-11", "GH-12", "GH-13", "GH-14", "GH-15"} {
		pairs = append(pairs, id, "/code/repo."+id)
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i))

		for _, skill := range []string{"judge", "ship"} {
			fx.expects(id, skill)
		}
	}

	fx.f.Script(testkit.Worktrees, testkit.Porcelain(pairs...))
	fx.mergeable("GH-11", "GH-12", "GH-13", "GH-14", "GH-15")

	fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		task := req.Task
		fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)
		fx.infos[task.ID] = hostops.TaskInfo{Closed: true}

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

	fx.f.Script(testkit.WsList,
		unopened, all("idle", ids...), // GH-11's open, then the look before its judge
		all("working", ids...), all("idle", ids...), // GH-11's turn, then it finishes
		unopened, all("idle", ids...), unopened, all("idle", ids...), // the second wave's opens and looks
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...), // the second wave's turns
		unopened, all("idle", ids...), // GH-15's open, then the look before its judge
		all("working", ids...), all("idle", ids...))

	// finish runs in-process, where no hook records it: drive does
	var finished []string

	fx.in.Record = func(_ context.Context, _ string, e ledger.Event) {
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
		testkit.AgentOf("GH-11") + " /f10:judge",
		"finish GH-11",
		testkit.AgentOf(
			"GH-12",
		) + " /f10:judge",
		testkit.AgentOf("GH-13") + " /f10:judge",
		testkit.AgentOf("GH-14") + " /f10:judge",
		"finish GH-12",
		"finish GH-13",
		"finish GH-14",
		testkit.AgentOf("GH-15") + " /f10:judge",
		"finish GH-15",
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
	fx.infos["GH-11"] = hostops.TaskInfo{Closed: true}

	fx.f.Script(testkit.WsList,
		unopened, all("idle", ids...), unopened, all("idle", ids...), // the three opens and looks
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...), // the judges' turns
		all("idle", ids...), all("idle", ids...), // before GH-12's and GH-14's ships
		all("working", ids...), all("idle", ids...))
	fx.onTick[2] = func() { fx.writeRunFor(t, "GH-13", "blocked", "judge", "stop: duplicate of GH-9", "") }

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, fx.out.String())
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

	if fx.f.Called("herdr agent prompt "+testkit.AgentOf("GH-13")+" /f10:ship") ||
		fx.f.Called("herdr agent prompt "+testkit.AgentOf("GH-15")) {
		t.Errorf("a halted task or its dependent was prompted: %q", fx.f.Calls)
	}
}

// A dependent started before its base finished is caught up before its next
// skill, which follows only once the mark is gone and the tree is clean; a
// catchup that leaves the mark halts the task.
func TestDriveCatchesADependentUpAfterFinish(t *testing.T) {
	for _, clean := range []bool{true, false} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "finish")
			fx.oneAttempt(t)
			fx.task("GH-1", hostops.TaskInfo{Body: "base"}, "")
			fx.task("GH-2", hostops.TaskInfo{Body: "dependent"}, testkit.Heads("GH-2"))
			fx.f.Meta.Seed("GH-2", branchmeta.After, "GH-1")
			fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.opens("GH-2", "ws:GH-2", "pane:2")
			fx.expects("GH-1", "judge")
			fx.expects("GH-2", "judge")
			fx.f.Script(catchupTurn("GH-2"), "{}")
			fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.Script("git status --porcelain", "")
			fx.mergeable("GH-1", "GH-2")

			fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
				task := req.Task
				fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)
				fx.infos[task.ID] = hostops.TaskInfo{Closed: true}

				if task.ID == "GH-1" {
					after := ""
					if !clean {
						after = "GH-1"
					}
					// the mark finish leaves, then what the catchup left
					fx.f.Meta.Script("GH-2", branchmeta.Landed, "GH-1", after)
				}

				return nil
			}

			ids := []string{"GH-1", "GH-2"}
			fx.f.Script(testkit.WsList,
				unopened, all("idle", ids...),
				all("working", ids...), all("idle", ids...), // GH-1's judge, then finish
				unopened, all("idle", ids...), // GH-2's open, then the look before its catchup
				all("working", ids...), all("idle", ids...), // the catchup
				all("idle", ids...), // before GH-2's judge
				all("working", ids...), all("idle", ids...))
			// the catchup reports as its turn ends, clean or not
			fx.onTick[4] = func() { fx.reportsFor(t, "GH-2", fx.clock) }

			err := fx.drive(t)

			if !clean {
				if testkit.ExitCode(err) != ui.ExitHalted ||
					!strings.Contains(fx.out.String(), "GH-2:    halted at catchup") ||
					!strings.Contains(fx.out.String(), "still marked "+string(branchmeta.Landed)) {
					t.Fatalf("err = %v\n%s", err, fx.out.String())
				}

				if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-2") + " /f10:judge") {
					t.Error("judge ran on a branch the catchup left unsettled")
				}

				return
			}

			if err != nil {
				t.Fatalf("%v\n%s", err, fx.out.String())
			}

			fx.assertOrder(t, []string{
				testkit.AgentOf(
					"GH-1",
				) + " /f10:judge",
				"finish GH-1",
				testkit.AgentOf("GH-2") + " /f10:catchup ",
				testkit.AgentOf("GH-2") + " /f10:judge",
				"finish GH-2",
			}, "herdr agent prompt", "finish")

			if fx.f.Called("set branch.GH-2." + string(branchmeta.Drive) + " catchup") {
				t.Error("the catchup was recorded as a chain position")
			}
		})
	}
}

// A pull request conflicting with the default branch at finish, as a sibling
// that merged first leaves it, is caught up and pushed with a lease before
// finish tries it again; a second conflict counts by its fingerprint, so the
// same conflict with an unmoved main halts and a moved one catches up again.
func TestDriveCatchesUpAConflictingPullBeforeFinish(t *testing.T) {
	conflicts := "git merge-tree --write-tree --no-messages "
	cases := map[string]struct {
		// after the catchup: HEAD, origin/main, and what HEAD conflicts on
		// (aOneWay, aChanged and bOneWay are merge-tree's conflicted stages)
		head, main, files string
		again             bool
		halt              string
	}{
		"settled":                    {head: "new", main: "m1"},
		"main did not move":          {head: "new", main: "m1", files: aOneWay, halt: "a.go"},
		"same file, main changed it": {head: "new", main: "m2", files: aChanged, again: true},
		"main moved, other files":    {head: "new", main: "m2", files: bOneWay, again: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			if tc.halt != "" {
				fx.oneAttempt(t)
			}

			fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
			fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.Script("git fetch origin", "")
			// origin's copy of the branch before the catchup, then what the push left
			fx.f.Script("git rev-parse -q --verify origin/GH-1", "old", tc.head)
			// read before and after the fetch when the catchup goes out, then
			// before and after the push's fetch, then what the push left
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "old", "old", "old", "old", tc.head)
			fx.f.Fail("git merge-tree --write-tree origin/main old", shell.Result{Code: 1})

			// before the catchup, the switch that sends it, after it, before
			// finish; a second conflict marks and reads it once more, the
			// agent-verified settle reading it twice to see verify went green
			landed := []string{"", "main", "", "", ""}
			if tc.again {
				landed = []string{"", "main", "", "", "", "", "main", "", "", ""}
			}

			fx.f.Meta.Script("GH-1", branchmeta.Landed, landed...)
			fx.f.Script(catchupTurn("GH-1"), "{}")
			fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.Script("git status --porcelain", "")
			fx.f.Fail("git merge-base --is-ancestor old HEAD", shell.Result{Code: 1})
			// the branch holds origin's copy when the catchup goes out
			fx.f.Script("git merge-base --is-ancestor old refs/heads/GH-1", "")
			fx.f.Script("git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1", "")
			// origin holds the target, so the caught-up branch may be pushed
			fx.f.Script("git merge-base --is-ancestor m1 refs/remotes/origin/main", "")
			// the record when the catchup goes out: origin/main at m1, ahead of
			// the local main, HEAD at head0, a.go conflicting one way
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "m1", tc.main)
			fx.f.Fail("git merge-base --is-ancestor m1 main0", shell.Result{Code: 1})
			fx.f.Fail("git merge-base --is-ancestor "+tc.main+" main0", shell.Result{Code: 1})
			fx.f.Script("git rev-parse -q --verify HEAD", "head0", tc.head)
			fx.f.Fail(conflicts+"m1 HEAD", shell.Result{Code: 1, Stdout: aOneWay})
			fx.f.Fail(conflicts+tc.main+" HEAD", shell.Result{Code: 1, Stdout: tc.files})

			// origin's copy after the push: clean once settled, else still conflicting
			settled := tc.halt == "" && !tc.again
			if settled {
				fx.f.Script("git merge-tree --write-tree origin/main "+tc.head, "")
			} else {
				fx.f.Fail("git merge-tree --write-tree origin/main "+tc.head, shell.Result{Code: 1})
			}

			if tc.again {
				fx.f.Answers["git merge-tree --write-tree origin/main new"] = []shell.Result{{Code: 1}, {}}
				fx.f.Script("git merge-base --is-ancestor new refs/heads/GH-1", "")
			}

			fx.f.Fail("git merge-base --is-ancestor origin/main HEAD", shell.Result{Code: 1})
			fx.f.Fail("git merge-tree --write-tree origin/main HEAD", shell.Result{Code: 1})

			fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
				task := req.Task
				fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)

				return nil
			}

			// the open, the catchup's send, its turn and its end; a second
			// catchup: its send, its turn and its end
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.Script(testkit.WsList, unopened, idle, working, idle, idle, working, idle)
			fx.onTick[2] = func() { fx.reports(t, fx.clock) }
			fx.onTick[4] = func() { fx.reports(t, fx.clock) }

			err := fx.drive(t)
			out := fx.out.String()
			catchups := countCalls(fx.f.Calls, catchupTurn("GH-1"))
			push := "git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1"

			// the catchup's clean end reaches origin before finish tries the branch again
			if n := countCalls(fx.f.Calls, push); n != 1 {
				t.Errorf("pushed %d times after the catchup, want once: %q", n, fx.f.Calls)
			}

			if tc.halt != "" {
				if testkit.ExitCode(err) != ui.ExitHalted ||
					!strings.Contains(out, "GH-1 still conflicts with main after a catchup: "+tc.halt) ||
					!strings.Contains(out, "next: settle it with /f10:catchup in /code/repo.GH-1") {
					t.Fatalf("err = %v\n%s", err, out)
				}

				if fx.f.Called("finish GH-1") || catchups != 1 {
					t.Errorf("a branch still conflicting was merged or caught up again: %q", fx.f.Calls)
				}

				return
			}

			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			if want := map[bool]int{false: 1, true: 2}[tc.again]; catchups != want {
				t.Errorf("catchup prompted %d times, want %d: %q", catchups, want, fx.f.Calls)
			}

			mark := "set branch.GH-1." + string(branchmeta.Landed) + " main"
			catchup := catchupTurn("GH-1")
			order := []string{mark, catchup, push, "finish GH-1"}

			if tc.again {
				order = []string{mark, catchup, push, mark, catchup, "finish GH-1"}
			}

			fx.assertOrder(t, order,
				"set branch.GH-1."+string(branchmeta.Landed)+" ", "herdr agent prompt", "git push", "finish")

			for _, want := range []string{
				"conflicts with origin/main, catching up first",
				"GH-1 catchup: GH-1 caught up with main, pushed with --force-with-lease",
				"GH-1:    finished",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// A pushed branch is kept up to date with its pull request's target at the
// boundary before each skill, never mid-turn, and pushed before the skill
// runs unless origin lacks what the catchup brought in.
func TestDriveCatchesUpAPushedBranchWhenItsTargetMoves(t *testing.T) {
	landed := "set branch.GH-1." + string(branchmeta.Landed) + " main"
	moved := "set branch.GH-1." + string(branchmeta.Moved) + " b2"
	ship := "herdr agent prompt " + testkit.AgentOf(
		"GH-1",
	) + " " + SkillPrompt(
		"ship",
		"GH-1",
		nil,
		runplan.ShipFlow{},
		true,
	)
	catchup := catchupTurn("GH-1")
	push := "git push --force-with-lease=refs/heads/GH-1:p1 origin HEAD:refs/heads/GH-1"

	cases := map[string]struct {
		pushed   string // origin's copy of the branch, "" for one never pushed
		after    string // a base branch recorded on the branch, outside the drive
		unpushed bool   // that base's head is not on origin yet
		ancestor bool   // the target is in the branch already
		marked   bool   // the branch is marked already
		fetch    shell.Result
		pushFail bool
		moved    bool   // someone pushed to the branch while the catchup ran
		mark     string // the mark the boundary sets
		fetches  int    // git fetch origin calls: the tick's one before ship (the boundary and the catchup's send), after it, the push, finish
		halt     string
		next     string // the halt's next line, "next: rerun the drive" when ""
	}{
		"origin/main moved":      {pushed: "p1", mark: landed, fetches: 4},
		"never pushed":           {fetches: 1},
		"already an ancestor":    {pushed: "p1", ancestor: true, fetches: 2},
		"already marked":         {pushed: "p1", marked: true, fetches: 4},
		"fetch fails":            {pushed: "p1", fetch: shell.Result{Code: 128, Stderr: "no route"}, fetches: 2},
		"an unlisted base moved": {pushed: "p1", after: "GH-9", mark: moved, fetches: 4},
		"an unlisted base moved, not on origin": {
			pushed: "p1", after: "GH-9", unpushed: true, mark: moved, fetches: 4,
		},
		"push rejected": {
			pushed: "p1", mark: landed, pushFail: true, fetches: 2,
			halt: "GH-1 halted at catchup: pushing GH-1, caught up with main: ",
		},
		"origin moved during the catchup": {
			pushed: "p1", mark: landed, moved: true, fetches: 2,
			halt: "GH-1 halted at catchup: GH-1 caught up with main, and origin/GH-1 moved meanwhile",
			next: "next: bring origin/GH-1 into /code/repo.GH-1, then /f10:catchup there",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship", "finish")
			// the real integration on the scripted base, so its fetch counts
			// with the tick's
			fx.in.Catchup.Integrate = stack.IntegrateWith(fx.in.Catchup.Base)
			fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
			fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.expects("GH-1", "ship")
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", tc.pushed)
			fx.f.Answers["git fetch origin"] = []shell.Result{tc.fetch, {}}

			originMain := "m2"
			if tc.pushed == "" {
				originMain = "" // finish merges a branch never pushed into the local default branch
				fx.f.Script("git merge-tree --write-tree main0 refs/heads/GH-1", "")
			}

			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", originMain)
			aim := "m2"

			if tc.after != "" {
				aim = "b2"
				fx.f.Meta.Seed("GH-1", branchmeta.AfterBranch, tc.after)
				fx.f.Script("git rev-parse -q --verify refs/heads/"+tc.after, "b2")

				if !tc.unpushed {
					fx.f.Script("git rev-parse -q --verify refs/remotes/origin/"+tc.after, "b2")
				}
			}

			// behind at the boundary, caught up once the catchup ran
			ancestor := []shell.Result{{Code: 1}, {}}
			if tc.ancestor || tc.marked {
				ancestor = []shell.Result{{}}
			}

			fx.f.Answers["git merge-base --is-ancestor "+aim+" refs/heads/GH-1"] = ancestor
			// local main does not hold origin's, so origin/main is the target
			fx.f.Fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
			// origin holds the target, unless the base was never pushed
			fx.f.Script("git merge-base --is-ancestor m2 refs/remotes/origin/main", "")
			onOrigin := shell.Result{}
			if tc.unpushed {
				onOrigin = shell.Result{Code: 1}
			}

			fx.f.Fail("git merge-base --is-ancestor b2 refs/remotes/origin/"+tc.after, onOrigin)
			fx.f.Meta.Script("GH-1", branchmeta.Landed, "")
			fx.f.Script(moved, "")

			// the mark read by the boundary's check, then by the boundary as it
			// sends the catchup (which reads a moved mark once more for its
			// target), then after the catchup and before finish
			switch {
			case tc.marked:
				fx.f.Meta.Script("GH-1", branchmeta.Landed, "main", "main", "")
			case tc.mark == landed:
				fx.f.Meta.Script("GH-1", branchmeta.Landed, "", "main", "")
			case tc.mark == moved:
				fx.f.Meta.Script("GH-1", branchmeta.Moved, "", "b2", "b2", "")
			}

			fx.f.Script(catchup, "{}")
			fx.f.Script("git rebase main0", "") // the scripted base's method
			fx.f.Script("git merge-tree --write-tree --no-messages "+aim+" HEAD", "tree")
			fx.f.Script("git rev-parse -q --verify HEAD", "head0", "head1")
			fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.Script("git status --porcelain", "")
			fx.f.Fail("git merge-base --is-ancestor p1 HEAD", shell.Result{Code: 1})
			fx.f.Script("git merge-base --is-ancestor p1 refs/heads/GH-1", "") // not behind origin
			fx.f.Script("git rev-parse -q --verify origin/GH-1", tc.pushed)
			fx.f.Script("git merge-tree --write-tree origin/main p1", "")

			if tc.pushFail {
				fx.f.Fail(push, shell.Result{Code: 1, Stderr: "rejected: stale info"})
			} else {
				fx.f.Script(push, "")
			}

			fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
				task := req.Task
				fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)

				return nil
			}

			// the open, whose boundary marks and sends the catchup; the
			// catchup's turn and its end, which sends ship; ship's turn and
			// its end, where finish runs
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.Script(testkit.WsList, unopened, idle, working, idle, idle, working, idle)
			fx.onTick[2] = func() { // the catchup reports as its turn ends
				if tc.moved {
					fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p2")
					fx.f.Fail("git merge-base --is-ancestor p2 HEAD", shell.Result{Code: 1})
				}

				fx.reports(t, fx.clock)
			}

			err := fx.drive(t)
			out := fx.out.String()

			if n := countCalls(fx.f.Calls, "git fetch origin"); n != tc.fetches {
				t.Errorf("fetched %d times, want %d: %q", n, tc.fetches, fx.f.Calls)
			}

			if tc.halt != "" {
				next := cmp.Or(tc.next, "next: rerun the drive")
				if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, tc.halt) ||
					!strings.Contains(out, next) || fx.f.Called("finish GH-1") ||
					(tc.moved && fx.f.Called("git push")) {
					t.Fatalf("err = %v\n%s", err, out)
				}

				return
			}

			if err != nil || !strings.Contains(out, "GH-1:    finished") {
				t.Fatalf("err = %v\n%s", err, out)
			}

			for _, m := range []string{landed, moved} {
				if n, want := countCalls(fx.f.Calls, m), map[bool]int{false: 0, true: 1}[m == tc.mark]; n != want {
					t.Errorf("%q set %d times, want %d: %q", m, n, want, fx.f.Calls)
				}
			}

			caught := tc.mark != "" || tc.marked
			if n, want := countCalls(fx.f.Calls, catchup), map[bool]int{false: 0, true: 1}[caught]; n != want {
				t.Errorf("catchup prompted %d times, want %d: %q", n, want, fx.f.Calls)
			}

			pushes := map[bool]int{false: 0, true: 1}[caught && !tc.unpushed]
			if n := countCalls(fx.f.Calls, push); n != pushes {
				t.Errorf("pushed %d times, want %d: %q", n, pushes, fx.f.Calls)
			}

			if tc.unpushed &&
				!strings.Contains(out, "caught up with main, not pushed: main is not on origin yet") {
				t.Errorf("a base origin lacks was pushed, or the skip went unsaid:\n%s", out)
			}

			if !caught {
				if !tc.ancestor && tc.pushed != "" && tc.fetch.Code == 0 {
					t.Errorf("no catchup although origin/main moved past the branch:\n%s", out)
				}

				if tc.fetch.Code != 0 && !strings.Contains(out, "skipped the target check, fetching origin: ") {
					t.Errorf("a failed fetch was not reported:\n%s", out)
				}

				return
			}

			// the mark is set at the boundary before ship and acted on there,
			// so ship runs on the caught-up branch
			order := []string{
				catchup,
				push,
				ship,
				"set branch.GH-1." + string(branchmeta.Drive) + " ship",
				"finish GH-1",
			}
			if tc.unpushed {
				order = slices.DeleteFunc(order, func(c string) bool { return c == push })
			}

			if tc.mark != "" {
				order = slices.Insert(order, 0, tc.mark)
			}

			fx.assertOrder(
				t,
				order,
				"herdr agent prompt",
				"set branch.GH-1."+string(branchmeta.Drive)+" ",
				landed,
				moved,
				"git push",
				"finish",
			)

			want := "origin/main moved past GH-1, catching up before the next skill"
			if tc.after != "" {
				want = tc.after + " moved past GH-1, catching up before the next skill"
			}

			if tc.marked {
				want = "GH-1 catchup: GH-1 caught up with main, pushed with --force-with-lease (the catchup rewrote it)"
			}

			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		})
	}
}

// A ship that failed at a guarded entry for its own reason, while the look
// marked its branch behind the default branch, is not the guard's stop: no
// catchup goes out, since ship could not resume from it, and the stop halts.
func TestDriveHaltsAShipThatFailedUnderAWatcherMark(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.oneAttempt(t)
	fx.adoptsGH1()

	ship := SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{}, false)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+ship, "{}")
	fx.onTick[2] = func() {
		fx.writeShipStop(t, "failed", "implement", 2, "fix the two red tests, then rerun")
		fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
	}
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, "GH-1 halted at ship: conflict with main") {
		t.Fatalf("err = %v\n%s", err, out)
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:catchup") {
		t.Errorf("a catchup went out for a stop ship cannot resume from: %q", fx.f.Calls)
	}
}

// Two tasks asking in the same tick both reach the report, with a resume
// command keyed per task; the resume sends each answer to its own agent.
func TestDriveCollectsEveryAskAndTakesKeyedAnswers(t *testing.T) {
	ids := []string{"GH-1", "GH-2"}

	setup := func(t *testing.T, args ...string) *driveFixture {
		t.Helper()

		fx := newDriveFixture(t, args...)
		fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))

		return fx
	}

	t.Run("ask", func(t *testing.T) {
		fx := setup(t, "GH-1", "GH-2", "judge")
		fx.task("GH-1", hostops.TaskInfo{Body: "a"}, "")
		fx.task("GH-2", hostops.TaskInfo{Body: "b"}, "")
		fx.opens("GH-1", "ws:GH-1", "pane:1")
		fx.opens("GH-2", "ws:GH-2", "pane:2")
		fx.expects("GH-1", "judge")
		fx.expects("GH-2", "judge")
		fx.f.Script(testkit.WsList,
			unopened, all("idle", ids...), unopened, all("idle", ids...), all("working", ids...), all("idle", ids...))
		fx.onTick[2] = func() {
			fx.writeRunFor(t, "GH-1", "blocked", "judge", "", "1. Rethink: [proceed | stop]")
			fx.writeRunFor(t, "GH-2", "blocked", "judge", "", "1. Split it? [yes | no]")
		}

		err := fx.drive(t)
		if testkit.ExitCode(err) != ui.ExitAsk {
			t.Fatalf("err = %v, want exit %d", err, ui.ExitAsk)
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
		fx.task("GH-1", hostops.TaskInfo{Body: "a"}, testkit.Heads("GH-1"))
		fx.task("GH-2", hostops.TaskInfo{Body: "b"}, testkit.Heads("GH-2"))

		for _, id := range ids {
			fx.f.Script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
				`{"result":{"workspace":{"workspace_id":"ws:`+id+`"},"root_pane":{"pane_id":"p"},"already_open":true}}`)
			fx.f.Script("herdr agent get p", testkit.AgentFound)
		}

		fx.writeRunFor(t, "GH-1", "blocked", "judge", "", "1. Rethink: [proceed | stop]")
		fx.writeRunFor(t, "GH-2", "blocked", "judge", "", "1. Split it? [yes | no]")
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" 1. proceed", "{}")
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-2")+" 1. no", "{}")
		fx.f.Script(testkit.WsList,
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

		fx.assertOrder(
			t,
			[]string{testkit.AgentOf("GH-1") + " 1. proceed", testkit.AgentOf("GH-2") + " 1. no"},
			"herdr agent prompt",
		)
	})

	t.Run("two bare answers", func(t *testing.T) {
		fx := setup(t, "GH-1", "--answer", "yes", "--answer", "no")
		fx.task("GH-1", hostops.TaskInfo{Body: "a"}, "")

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
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
}

const agentMissing = `{"error":{"code":"agent_not_found","message":"agent target pane:1 not found"}}`

// FT-28's case: an earlier start left a workspace with no agent and a run
// still saying running. The drive starts the agent in the leftover's shell,
// and the old run halts nothing, since its agent is gone.
func TestDriveAdoptsAWorkspaceAnEarlierStartLeft(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.existing()
	fx.staleRun(t, "running", "implement")
	fx.f.Script("herdr pane list --workspace ws:GH-1", `{"result":{"panes":[{"pane_id":"pane:1"}]}}`)
	fx.f.Fail("herdr agent get pane:1", shell.Result{Code: 1, Stderr: agentMissing})
	fx.f.Script("herdr pane process-info --pane pane:1",
		`{"result":{"process_info":{"foreground_process_group_id":42,"shell_pid":42}}}`)
	fx.f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:1", "{}")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList,
		listing("GH-1", "unknown"), listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.Called("herdr worktree open") {
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
	fx.f.Script("herdr pane list --workspace ws:old", `{"result":{"panes":[{"pane_id":"pane:9"}]}}`)
	fx.f.Script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:2"},{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.Script("herdr agent get pane:1", testkit.AgentFound)
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, shownTwice("unknown", "idle"), shownTwice("idle", "idle"),
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

	if fx.f.Called("herdr agent start") || fx.f.Called("herdr workspace close") {
		t.Errorf("calls = %q", fx.f.Calls)
	}
}

// A model for a workspace whose agent is already running is said to be
// ignored, never applied by starting a second agent.
func TestDriveSaysAModelWasIgnored(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "--model", "opus", "judge")
	fx.existing()
	fx.f.Script("herdr pane list --workspace ws:old", `{"result":{"panes":[{"pane_id":"pane:9"}]}}`)
	fx.f.Script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:2"},{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.Script("herdr agent get pane:1", testkit.AgentFound)
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, shownTwice("unknown", "idle"), shownTwice("idle", "idle"),
		shownTwice("idle", "working"), shownTwice("idle", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if want := "agent profile opus ignored: no agent was started"; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}

	if fx.f.Called("herdr agent start") {
		t.Errorf("calls = %q", fx.f.Calls)
	}
}

// An agent that was already there, idle under a run that says running, may
// sit on a dialog: the task halts, and the halt is on screen before the
// report, naming the way out of a cancelled run.
func TestDrivePrintsAHaltWhenItHappens(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.existing()
	fx.staleRun(t, "running", "implement")
	fx.f.Script("herdr pane list --workspace ws:GH-1",
		`{"result":{"panes":[{"pane_id":"pane:1","agent":"claude"}]}}`)
	fx.f.Script("herdr agent get pane:1", testkit.AgentFound)
	fx.f.Script(testkit.WsList, listing("GH-1", "idle"))

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d", err, ui.ExitHalted)
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
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "unknown"))

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "workspace GH-1 (ws:GH-1) shows no agent") {
		t.Errorf("output = %s", fx.out.String())
	}

	if limit := int(StartWindow/drivectl.DefaultEvery) + 2; fx.sleeps > limit {
		t.Errorf("polled %d times, past the start window's %d", fx.sleeps, limit)
	}
}

// Someone else's PR at finish is drive's own question: the task stops
// asking, and the resumed drive passes the answer to finish.
func TestDriveFinishAsksForSomeoneElsesPR(t *testing.T) {
	var got []string

	in := &DriveInput{
		Finish: func(_ context.Context, _ io.Writer, req DriveFinish) error {
			got = append(got, req.Pr)
			if req.Pr == "" {
				return finish.ForeignPullError{URL: "https://gh/pr/9", Author: "alice"}
			}

			return nil
		},
		Answers: NewAnswerBook(),
	}
	in.Out = io.Discard

	run := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}

	res := in.RunFinish(t.Context(), run)
	if res.Code != ui.ExitAsk || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "[local | merge]") {
		t.Fatalf("runFinish on someone else's PR = %+v, want an ask", res)
	}

	in.Answers.Give(Answer{Task: "GH-1", Text: "1. local"})

	if res = in.RunFinish(t.Context(), run); res.Code != 0 {
		t.Fatalf("runFinish answered local = %+v, want finished", res)
	}

	// a bare answer that is no finish choice belongs to some agent's question
	in.Answers.Give(Answer{Text: "1. yes"})

	if res = in.RunFinish(t.Context(), run); res.Code != ui.ExitAsk || in.Answers.Bare != "1. yes" {
		t.Errorf("a bare non-choice answer = %+v, left %q; want it left for an agent", res, in.Answers.Bare)
	}

	in.Answers.Give(Answer{Task: "GH-1", Text: "maybe"})

	if res = in.RunFinish(t.Context(), run); res.Code != ui.ExitHalted {
		t.Errorf("a keyed answer that is no choice = %+v, want a halt", res)
	}

	if want := []string{"", FinishLeaveOpen, ""}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("finish was passed %q, want %q", got, want)
	}
}

// A suffixed attempt finishes its own branch: the drive hands finish the
// run's suffix and branch, which the task id alone would not find.
func TestDriveFinishPassesTheRunsBranch(t *testing.T) {
	var got DriveFinish

	in := &DriveInput{
		Main: t.TempDir(),
		Now:  time.Now,
		Finish: func(_ context.Context, _ io.Writer, req DriveFinish) error {
			got = req

			return nil
		},
		Answers: NewAnswerBook(),
	}
	in.Out = io.Discard

	run := &DriveRun{Task: DriveTask{Ref: testkit.Gh1, Suffix: "b"}, O: start.OpenedTask{Branch: "GH-1-b"}}

	if res := in.RunFinish(t.Context(), run); res.Code != 0 {
		t.Fatalf("runFinish = %+v, want finished", res)
	}

	if got.Task.ID != "GH-1" || got.Suffix != "b" || got.Branch != "GH-1-b" || got.Progress == nil {
		t.Errorf("finish was passed %+v, want GH-1, suffix b, branch GH-1-b and a progress hook", got)
	}
}

// The drive's finish takes every setting the command does: under traces: on
// its squash commit names the task, and lore_gate holds as it does by hand.
func TestDriveFinishInputTakesTheSettings(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "on")
	project := "---\ntracker:\n  id: DM-###\ntraces: on\nmerge_method: squash\nlore_gate: block\n---\n"

	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	if err := os.WriteFile(path, []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	tg, err := target.At(t.Context(), repo, false, "drive")
	if err != nil {
		t.Fatal(err)
	}

	bundle := ref.Ref{ID: "DM-2", Number: "2", Bundle: []string{"DM-1", "DM-2"}}
	req := DriveFinish{Task: bundle, Suffix: "b", Branch: "DM-1-b", Pr: FinishMerge}

	in, err := DriveFinishInput(tg, repo, hostops.NewHost("gh", repo), req)
	if err != nil {
		t.Fatal(err)
	}

	if !in.Traces || in.Method != "squash" || in.Gate != "block" || in.LoreStore == nil || in.Title == nil {
		t.Errorf("settings = traces %v, method %q, gate %q, lore %v; want them all from project.md",
			in.Traces, in.Method, in.Gate, in.LoreStore)
	}

	if in.Task.ID != "DM-1" || in.Suffix != "b" || in.Branch != "DM-1-b" || !in.Flags.Merge || in.Flags.LeaveOpen {
		t.Errorf("run = %s %q %q merge %v local %v; want the lead DM-1, the run's suffix and branch, a merge",
			in.Task.ID, in.Suffix, in.Branch, in.Flags.Merge, in.Flags.LeaveOpen)
	}

	if in.Close != nil || !in.FollowBody {
		t.Errorf("close = %v, follow body %v; want the PR body to decide the riders", in.Close, in.FollowBody)
	}

	w := WatcherFinishInput(tg, repo, hostops.NewHost("gh", repo), bundle, "DM-1-b")
	if w.Close != nil || !w.FollowBody || !w.Flags.MergedOnly || w.Branch != "DM-1-b" || w.Cwd != repo {
		t.Errorf("watcher = close %v, follow body %v, merged only %v, branch %q, cwd %q; "+
			"want the body to decide, merged requests only, on DM-1-b from main",
			w.Close, w.FollowBody, w.Flags.MergedOnly, w.Branch, w.Cwd)
	}
}

// chained is the fixture for GH-2 after GH-1, GH-2's branch already
// recording it, with GH-1's branch one commit past main so GH-2 opens as
// soon as GH-1 has.
func chained(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, args...)
	fx.task("GH-1", hostops.TaskInfo{Body: "base"}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "dependent"}, testkit.Heads("GH-2"))
	fx.f.Meta.Seed("GH-2", branchmeta.After, "GH-1")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.f.Script(testkit.HeadRef, "origin/main")
	fx.f.Script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "1")
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

	fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		task := req.Task
		fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)

		return nil
	}

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"), // GH-1's open and the look before its judge
		unopened, all("idle", "GH-1", "GH-2"), // GH-2's, in the same pass
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's judge ends first
		all("idle", "GH-1", "GH-2"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		testkit.AgentOf("GH-1") + " /f10:judge",
		testkit.AgentOf("GH-2") + " /f10:judge",
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
	fx.f.Script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "0")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, unopened, all("idle", "GH-1"), all("working", "GH-1"), all("idle", "GH-1"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-2")) {
		t.Errorf("GH-2 was prompted before GH-1 had a commit: %q", fx.f.Calls)
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

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "idle", "GH-2", "working"), // GH-1 stops on its judge
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); testkit.ExitCode(err) != ui.ExitHalted {
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

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-2") + " /f10:ship") {
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

	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "b2")
	fx.f.Answers["git merge-base --is-ancestor b2 refs/heads/GH-2"] = []shell.Result{{Code: 1}, {}}

	fx.f.Script("git merge-tree --write-tree --no-messages b2 HEAD", "tree")
	fx.f.Script(catchupTurn("GH-2"), "{}")
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")
	fx.onTick[4] = func() { fx.f.Meta.Script("GH-2", branchmeta.Moved, "") } // the catchup cleared it
	fx.onTick[5] = func() { fx.reportsFor(t, "GH-2", fx.clock) }             // and reports as its turn ends

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "idle", "GH-2", "working"), // GH-1's judge ends: it is idle, and its branch moved
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's judge ends
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // the catchup ends
		listing("GH-1", "idle", "GH-2", "working"),
		all("idle", "GH-1", "GH-2"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		"herdr agent prompt " + testkit.AgentOf("GH-2") + " /f10:judge",
		"set branch.GH-2." + string(branchmeta.Moved) + " b2",
		catchupTurn("GH-2"),
		"herdr agent prompt " + testkit.AgentOf("GH-2") + " /f10:ship",
	}, "herdr agent prompt "+testkit.AgentOf("GH-2"), "set branch.GH-2."+string(branchmeta.Moved))

	if n := strings.Count(strings.Join(fx.f.Calls, "\n"), "set branch.GH-2."+string(branchmeta.Moved)+" "); n != 1 {
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
	fx.oneAttempt(t)
	fx.f.Script(testkit.HeadRef, "origin/main")
	b := &DriveRun{Phase: AtRunning, O: start.OpenedTask{Branch: "GH-2"}}

	fx.f.Meta.Seed("GH-2", branchmeta.AfterBranch, "GH-1")
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "a1")
	fx.f.Script("git reflog show --format=%H %gs refs/heads/GH-2",
		"c3 commit: second\nc2 commit: first\nc1 branch: Created from GH-1")
	fx.f.Script("git rev-list --count --no-merges refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main "+
		"refs/heads/GH-1 c1", "0")

	if fx.in.Released(t.Context(), b) {
		t.Error("released with no commit of its own")
	}

	// GH-1 squash-merged and gone: the creation point still keeps its commits out
	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "")
	fx.f.Script(
		"git rev-list --count --no-merges refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main c1",
		"0",
		"1",
	)

	if fx.in.Released(t.Context(), b) {
		t.Error("released by its squashed base's commits")
	}

	if !fx.in.Released(t.Context(), b) {
		t.Error("not released once it has a commit of its own")
	}

	b.End = &DriveResult{Code: ui.ExitHalted}

	if fx.in.Released(t.Context(), b) {
		t.Error("a halted base released its dependent")
	}
}

// A branch origin does not have is tried against the local default branch
// before finish, which merges it here: clean goes straight to finish, a
// conflict is marked landed and caught up first.
func TestDriveFinishesABranchNeverPushed(t *testing.T) {
	// again: the catchup ends clean, then conflicts with the same local main,
	// which origin's copy is behind, so the halt names the same conflict
	for _, mode := range []string{"clean", "unsettled", "again"} {
		t.Run(mode, func(t *testing.T) {
			clean := mode == "clean"
			fx := newDriveFixture(t, "GH-1", "finish")
			if !clean {
				fx.oneAttempt(t)
			}

			fx.task("GH-1", hostops.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
			fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.Script(testkit.HeadRef, "origin/main")
			fx.f.Script("git fetch origin", "")
			fx.f.Script("git rev-parse -q --verify origin/GH-1", "")
			fx.f.Script("git rev-parse -q --verify refs/heads/main", "m0")
			fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

			fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
				task := req.Task
				fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)

				return nil
			}

			if !clean {
				fx.f.Fail("git merge-tree --write-tree m0 refs/heads/GH-1", shell.Result{Code: 1})
				fx.f.Fail("git merge-tree --write-tree --no-messages m0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})

				fx.f.Meta.Script("GH-1", branchmeta.Landed, "", "main")
				fx.f.Script(catchupTurn("GH-1"), "{}")

				if mode == "again" {
					// the catchup cleared the mark; origin's main is behind the local one
					fx.f.Meta.Script("GH-1", branchmeta.Landed, "", "main", "")
					fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "o0")
					fx.f.Script("git merge-base --is-ancestor o0 m0", "")
					fx.f.Script("git rev-parse -q --verify HEAD", "head0", "head1") // the catchup moved the branch
				}

				fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
				fx.f.Script("git status --porcelain", "")
				fx.f.Script(testkit.WsList,
					unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
				fx.onTick[2] = func() { fx.reports(t, fx.clock) } // the catchup reports as its turn ends
			} else {
				fx.f.Script("git merge-tree --write-tree m0 refs/heads/GH-1", "tree")
			}

			err := fx.drive(t)
			out := fx.out.String()

			if clean {
				if err != nil || !fx.f.Called("finish GH-1") || fx.f.Called("git push") {
					t.Fatalf("err = %v, calls %q\n%s", err, fx.f.Calls, out)
				}

				return
			}

			// the catchup left the mark, or the branch conflicts with the same
			// main again: the task halts rather than merging
			want := "the catchup did not end clean"
			if mode == "again" {
				want = "GH-1 still conflicts with main after a catchup: a.go"
			}

			if testkit.ExitCode(err) != ui.ExitHalted || fx.f.Called("finish GH-1") ||
				!strings.Contains(out, "conflicts with m0, catching up first") || !strings.Contains(out, want) {
				t.Fatalf("err = %v\n%s", err, out)
			}
		})
	}
}

// spikeDrive is the fixture for GH-1 shipped as a spike, its report already
// in the spikes dir, and GH-2, when listed, after it.
func spikeDrive(t *testing.T, args ...string) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, args...)
	root := t.TempDir()
	fx.in.Storage = storage.Root(root)

	var pairs []string

	for i, id := range []string{"GH-1", "GH-2"} {
		path := "/code/repo." + id
		pairs = append(pairs, id, path)
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i))
		addDir := " -- " + strings.Join(start.AgentArgs(agentctl.Profile{}, fx.in.Storage, path), " ")
		fx.f.Script(
			fmt.Sprintf("herdr agent start %s --kind claude --pane pane:%d%s", testkit.AgentOf(id), i, addDir),
			"{}",
		)
		fx.expects(id, "ship")
	}

	fx.task("GH-1", hostops.TaskInfo{Body: "a question"}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "After: GH-1"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(pairs...))

	// the ship turn writes the report
	testkit.WritePlan(t, filepath.Join(root, "spikes", "GH-1.md"))

	fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		task := req.Task
		fx.f.Calls = append(fx.f.Calls, "finish "+task.ID)
		fx.infos[task.ID] = hostops.TaskInfo{Closed: true}

		return nil
	}

	return fx
}

// A spike ends with its close even where the pace leaves finish out: no
// catchup and no merge check before it (neither is scripted), and a finish
// record for status.
func TestDriveClosesASpikeAfterShip(t *testing.T) {
	for _, chain := range [][]string{{"ship"}, {"ship", "finish"}} {
		t.Run(strings.Join(chain, " "), func(t *testing.T) {
			fx := spikeDrive(t, append([]string{"GH-1"}, chain...)...)
			fx.f.Script(
				testkit.WsList,
				unopened,
				listing("GH-1", "idle"),
				listing("GH-1", "working"),
				listing("GH-1", "idle"),
			)

			if err := fx.drive(t); err != nil {
				t.Fatalf("%v\n%s", err, fx.out.String())
			}

			if !fx.f.Called("finish GH-1") {
				t.Errorf("the spike was not closed: %q", fx.f.Calls)
			}

			if want := "GH-1:    finished"; !strings.Contains(fx.out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, fx.out.String())
			}

			if record := "set branch.GH-1." + string(branchmeta.Finish) + " queued"; !fx.f.Called(record) {
				t.Errorf("%q did not run: %q", record, fx.f.Calls)
			}
		})
	}
}

// A task after a spike starts once the spike is closed, as on a finished
// base, and runs its own chain without a close of its own.
func TestDriveStartsADependentOfAClosedSpike(t *testing.T) {
	fx := spikeDrive(t, "GH-1", "GH-2", "ship")
	// GH-2 opens on the default branch, its base finished
	fx.f.Script(testkit.HeadRef, "origin/main")
	fx.f.Script(testkit.WsList,
		unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"),
		unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		testkit.AgentOf("GH-1") + " /f10:ship", "finish GH-1", testkit.AgentOf("GH-2") + " /f10:ship",
	}, "herdr agent prompt", "finish")

	if fx.f.Called("finish GH-2") {
		t.Errorf("a task that is not a spike was closed: %q", fx.f.Calls)
	}

	if want := "GH-2:    done: ship"; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}
}

// A spike-reported task whose branch is on origin finishes through its PR,
// so drive takes no shortcut for it.
func TestDriveSpikeShortcutNeedsABranchNeverPushed(t *testing.T) {
	fx := spikeDrive(t, "GH-1", "ship")
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}, Pos: 1}
	r.O.Path, r.O.Branch = "/code/repo.GH-1", "GH-1"

	if done, _ := fx.in.SpikeDone(t.Context(), r); !done {
		t.Error("a spike never pushed took no shortcut")
	}

	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "abc")

	if done, _ := fx.in.SpikeDone(t.Context(), r); done {
		t.Error("a pushed spike took the shortcut")
	}
}

// Drive records its finish of a task on the branch for status to read:
// queued once the task opens, then each action finish names.
func TestDriveRecordsFinishProgress(t *testing.T) {
	ids := []string{"GH-11", "GH-12", "GH-13", "GH-14", "GH-15"}
	fx := wave(t, "GH-11", "GH-12", "GH-13", "GH-14", "GH-15", "judge", "finish")

	fx.f.Script(testkit.WsList,
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...),
		unopened, all("idle", ids...), unopened, all("idle", ids...),
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...),
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...))

	fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		task := req.Task
		req.Progress("merging #9")
		fx.infos[task.ID] = hostops.TaskInfo{Closed: true}

		return nil
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	var got []string

	for _, c := range fx.f.Calls {
		if strings.Contains(c, "GH-11."+string(branchmeta.Finish)) {
			got = append(got, c)
		}
	}

	key := "set branch.GH-11." + string(branchmeta.Finish)
	want := []string{key + " queued", key + " running: starting", key + " running: merging #9"}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("finish record writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A drive with no finish writes no finish record, and clears one an earlier
// drive left.
func TestDriveWithoutFinishWritesNoRecord(t *testing.T) {
	fx := wave(t, "GH-11", "judge")
	fx.f.Meta.Seed("GH-11", branchmeta.Finish, branchmeta.FinishQueued) // an earlier drive's

	fx.f.Script(testkit.WsList, unopened, all("idle", "GH-11"), all("working", "GH-11"), all("idle", "GH-11"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if !fx.f.Meta.Unsetted("GH-11", branchmeta.Finish) {
		t.Fatal("drive opened no task, or left a finish record standing")
	}

	if fx.f.Called("set branch.GH-11." + string(branchmeta.Finish)) {
		t.Error("a drive without finish wrote a finish record")
	}
}

// A task that ends any way but finished keeps its finish record, set back
// to queued so status still shows finish without claiming it runs; a
// finished one's went with its branch.
func TestDriveEndParksFinishRecord(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	r := &DriveRun{Phase: AtRunning, Finishes: true, O: start.OpenedTask{Branch: "GH-1"}}

	fx.in.End(t.Context(), r, DriveResult{Row: "asked at finish", Code: ui.ExitAsk})

	if !fx.f.Meta.Wrote("GH-1", branchmeta.Finish, branchmeta.FinishQueued) {
		t.Errorf("an ask did not park the finish record; calls: %v", fx.f.Calls)
	}

	if fx.f.Meta.Unsetted("GH-1", branchmeta.Finish) {
		t.Error("an ask dropped the finish record, which status reads")
	}

	fx.f.Calls = nil
	fx.in.End(t.Context(), r, DriveResult{Row: "finished"})

	if fx.f.Called("set branch.GH-1." + string(branchmeta.Finish)) {
		t.Error("a finished task's record was written again")
	}
}

// Resume steering sits after the subject, before the chained clauses, and
// names the place only for an entry the flow names twice.
func TestResumeAtSteering(t *testing.T) {
	pipeline := []string{"judge", "implement", "cleanup", "pr", "review:local", "judge", "pr"}
	// the guard's kind decides; next is worded freely, so none names catchup
	stop := func(leaf string, at int) *state.Run {
		return &state.Run{
			Ship:     "partial",
			Leaf:     leaf,
			At:       at,
			Pipeline: pipeline,
			Stop:     state.StopGuard,
			Next:     "settle it, then rerun",
		}
	}

	for run, want := range map[*state.Run]string{
		stop("pr", 4):        "resume at pr (entry 4)",
		stop("implement", 2): "resume at implement",
		stop("catchup", 4):   "resume at pr (entry 4)",
		stop("catchup", 0):   "",
		stop("pr", 0):        "resume at pr",
		stop("cleanup", 3):   "",
		{Ship: "partial", Leaf: "pr", At: 4, Pipeline: pipeline, Next: "fix it"}:                                "",
		{Ship: "partial", Leaf: "pr", At: 4, Pipeline: pipeline, Next: "run /f10:catchup --driven, then rerun"}: "",
		{Ship: "failed", Leaf: "pr", At: 4, Pipeline: pipeline, Wait: "GH-9"}:                                   "resume at pr (entry 4)",
	} {
		if got := ResumeAt("ship", run); got != want {
			t.Errorf("resumeAt(%s, %q) = %q, want %q", run.Leaf, run.Next, got, want)
		}
	}

	if got := ResumeAt("plan", stop("pr", 4)); got != "" {
		t.Errorf("a plan resumes at %q", got)
	}

	p := Steered(SkillPrompt("ship", "GH-1", nil, runplan.ShipFlow{}, true), "resume at pr")
	if want := "/f10:ship GH-1 resume at pr && " + FinishClause + " --driven"; p != want {
		t.Errorf("steered = %q, want %q", p, want)
	}
}

// A ship drive finishes after names it, so the report says nothing is
// owed in that pane; no other prompt carries it.
func TestSkillPromptNamesDriveFinish(t *testing.T) {
	if p := SkillPrompt(
		"ship",
		"GH-1",
		nil,
		runplan.ShipFlow{},
		true,
	); p != "/f10:ship GH-1 && "+FinishClause+" --driven" {
		t.Errorf("ship with finish = %q", p)
	}

	for _, skill := range []string{"plan", "judge", "catchup"} {
		if p := SkillPrompt(
			skill,
			"GH-1",
			nil,
			runplan.ShipFlow{},
			true,
		); strings.Contains(
			p,
			FinishClause,
		) {
			t.Errorf("%s prompt names finish: %q", skill, p)
		}
	}
}

// An interrupted drive parks the finish record of every task still in
// flight at queued, so status still shows finish and never reads a drive
// that is gone as running it.
func TestDriveInterruptParksFinishRecord(t *testing.T) {
	fx := wave(t, "GH-11", "judge", "finish")
	fx.f.Script(testkit.WsList, unopened, all("idle", "GH-11"), all("working", "GH-11"))

	ticks := 0
	fx.in.Sleep = func(context.Context, time.Duration) error {
		ticks++
		if ticks > 1 {
			return context.Canceled
		}

		return nil
	}

	if err := fx.drive(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("drive = %v, want the interrupt\n%s", err, fx.out.String())
	}

	key := "branch.GH-11." + string(branchmeta.Finish)
	if last := fx.f.Calls[len(fx.f.Calls)-1]; last != "set "+key+" queued" {
		t.Errorf("the last call = %q, want the record parked at queued", last)
	}

	if fx.f.Meta.Unsetted("GH-11", branchmeta.Finish) {
		t.Errorf("%s was unset: status would lose the finish row", key)
	}
}

// writeWait records GH-2's ship parked on GH-1, its branch without code: a
// driven ship's stop at implement with no ask.
func (fx *driveFixture) writeWait(t *testing.T) {
	t.Helper()
	fx.writeWaitAt(t, "implement", "its branch has no code yet")
}

// writeWaitAt records GH-2's ship parked on GH-1 at leaf, why saying what
// the base lacks.
func (fx *driveFixture) writeWaitAt(t *testing.T, leaf, why string) {
	t.Helper()

	body := fmt.Sprintf(
		"v 1\ntask GH-2\nroot /code/repo.GH-2\ncapture prior\nplan done\nship blocked\nleaf %s\n"+
			"pipeline judge → implement → cleanup → pr\nnote waiting on GH-1: %s\n"+
			"wait GH-1\nupdated %d\n",
		leaf, why, fx.clock.Unix(),
	)

	if err := os.WriteFile(filepath.Join(fx.in.StateDir, "s-GH-2.state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A ship that stops on a base without code parks in the drive with no ask,
// the base runs on, and ship goes again at implement once the base commits.
// The base here lost its only commit (a reset) between release and the park.
func TestDriveParksAShipOnItsBasesCode(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	// GH-2's open and first send, the park, a poll still empty, then code
	fx.f.Script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "1", "1", "0", "0", "1")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-2")+" "+
		Steered(SkillPrompt("ship", "GH-2", nil, runplan.ShipFlow{}, false), "resume at implement"), "{}")
	// the head the park read, then the commit that releases it
	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "a1", "a2")
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")
	fx.in.AsksDir = t.TempDir()

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2 parks
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 still without code
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 committed
		all("working", "GH-1", "GH-2"),
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeWait(t) }
	fx.onTick[5] = func() {
		fx.writeRunFor(t, "GH-1", "done", "", "", "")
		fx.writeRunFor(t, "GH-2", "done", "", "", "")
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		testkit.AgentOf("GH-1") + " /f10:ship",
		testkit.AgentOf("GH-2") + " /f10:ship",
		testkit.AgentOf("GH-2") + " /f10:ship GH-2 resume at implement",
	}, "herdr agent prompt")

	out := fx.out.String()
	for _, want := range []string{
		"GH-2 ship: waiting on GH-1 for code",
		"GH-2 ship: its base has code now: resume at implement",
		"GH-1:    done: ship",
		"GH-2:    done: ship",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if entries, _ := os.ReadDir(fx.in.AsksDir); len(entries) > 0 {
		t.Errorf("a parked ship raised an ask: %v", entries)
	}
}

// A ship that stops before pr on a base origin lacks parks with no attempt
// and no ask, though the base has code, and goes again at pr once the base's
// branch is on origin.
func TestDriveParksAShipOnItsBaseUntilOriginHasIt(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-2")+" "+
		Steered(SkillPrompt("ship", "GH-2", nil, runplan.ShipFlow{}, false), "resume at pr"), "{}")
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "")
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")
	fx.in.AsksDir = t.TempDir()

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2 parks
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 still not on origin
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 pushed
		all("working", "GH-1", "GH-2"),
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeWaitAt(t, "pr", "its branch is not on origin yet") }
	fx.onTick[4] = func() { fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "o1") }
	fx.onTick[5] = func() {
		fx.writeRunFor(t, "GH-1", "done", "", "", "")
		fx.writeRunFor(t, "GH-2", "done", "", "", "")
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		testkit.AgentOf("GH-1") + " /f10:ship",
		testkit.AgentOf("GH-2") + " /f10:ship",
		testkit.AgentOf("GH-2") + " /f10:ship GH-2 resume at pr",
	}, "herdr agent prompt")

	out := fx.out.String()
	for _, want := range []string{
		"GH-2 ship: waiting on GH-1 for origin",
		"GH-2 ship: its base is on origin now: resume at pr",
		"GH-2:    done: ship",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if strings.Count(out, "resume at pr") != 1 || strings.Contains(out, "trying again") {
		t.Errorf("the origin wait resent early or counted an attempt:\n%s", out)
	}

	if entries, _ := os.ReadDir(fx.in.AsksDir); len(entries) > 0 {
		t.Errorf("a parked ship raised an ask: %v", entries)
	}
}

// A base that finishes while its dependent waits for origin releases it:
// ship goes again at pr, which then targets the default branch.
func TestDriveResendsAnOriginWaitOnceItsBaseFinished(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	b := &DriveRun{
		Phase: AtRunning,
		O:     start.OpenedTask{Branch: "GH-1"},
		Task:  DriveTask{Ref: ref.Ref{ID: "GH-1"}},
	}
	r := &DriveRun{
		Phase: AtRunning,
		Base:  b,
		O:     start.OpenedTask{Branch: "GH-2"},
		Task:  DriveTask{Ref: ref.Ref{ID: "GH-2"}},
	}
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "")

	run := &state.Run{Ship: "blocked", Leaf: "pr", Wait: "GH-1"}
	if !OriginWait(run) || OriginWait(&state.Run{Ship: "blocked", Leaf: "implement", Wait: "GH-1"}) {
		t.Fatal("originWait reads the wrong leaf")
	}

	r.ParkedOrigin = true
	if fx.in.BaseReady(t.Context(), r) {
		t.Fatal("a base origin lacks read as ready")
	}

	b.End = &DriveResult{Row: "done: ship"}
	if !fx.in.BaseReady(t.Context(), r) {
		t.Error("a finished base did not release the origin wait")
	}
}

// A ship that stops before pr while origin already holds the base (the push
// landed between its look and the drive's read) is sent again at once.
func TestDriveResendsAnOriginWaitTheBasePushedFirst(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-2")+" "+
		Steered(SkillPrompt("ship", "GH-2", nil, runplan.ShipFlow{}, false), "resume at pr"), "{}")
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's stop
		all("working", "GH-1", "GH-2"),
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() {
		fx.writeWaitAt(t, "pr", "its branch is not on origin yet")
		fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "o1")
	}
	fx.onTick[4] = func() {
		fx.writeRunFor(t, "GH-1", "done", "", "", "")
		fx.writeRunFor(t, "GH-2", "done", "", "", "")
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	out := fx.out.String()
	if strings.Count(out, "resume at pr") != 1 || strings.Contains(out, "waiting on GH-1 for origin") {
		t.Errorf("ship was not sent again at once:\n%s", out)
	}
}

// The origin wait's resends are bounded: the same origin sha stopping pr
// again, or a base done in this drive stopping it twice, halts the task.
func TestDriveHaltsAnOriginWaitThatStopsAgain(t *testing.T) {
	cases := map[string]struct {
		waitHead, origin, reason string
		through                  bool
	}{
		"same origin sha": {
			waitHead: "origin o1", origin: "o1",
			reason: "its pr step found GH-1 not on origin though origin/GH-1 is at o1",
		},
		"base done, unpushed": {
			waitHead: "through", through: true,
			reason: "GH-1 ended in this drive, yet its pr step still found GH-1 not on origin",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-2", "ship")
			b := &DriveRun{
				Phase: AtRunning,
				O:     start.OpenedTask{Branch: "GH-1"},
				Task:  DriveTask{Ref: ref.Ref{ID: "GH-1"}},
			}
			if tc.through {
				b.End = &DriveResult{Row: "done: ship"}
			}

			r := &DriveRun{
				Phase: AtRunning, Base: b, Skill: "ship", WaitHead: tc.waitHead,
				O:    start.OpenedTask{Branch: "GH-2", Path: "/code/repo.GH-2"},
				Task: DriveTask{Ref: ref.Ref{ID: "GH-2"}},
			}
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", tc.origin)

			if err := fx.in.OnTask(t.Context(), r, fx.in.ParkForOrigin(t.Context(), r)); err != nil {
				t.Fatal(err)
			}

			if r.End == nil || r.End.Code != ui.ExitHalted || r.End.Reason != tc.reason {
				t.Errorf("end = %+v, want a halt: %s", r.End, tc.reason)
			}
		})
	}
}

// A drive resumed onto a ship already stopped for origin holds it: the stop
// alone does not read as a base ready to resend to.
func TestDriveHoldsAResumedOriginWait(t *testing.T) {
	fx := newDriveFixture(t, "GH-2", "ship")
	b := &DriveRun{
		Phase: AtRunning,
		O:     start.OpenedTask{Branch: "GH-1"},
		Task:  DriveTask{Ref: ref.Ref{ID: "GH-1"}},
	}
	r := &DriveRun{
		Phase: AtRunning, Base: b,
		O:    start.OpenedTask{Branch: "GH-2", Path: "/code/repo.GH-2"},
		Task: DriveTask{Ref: ref.Ref{ID: "GH-2"}},
	}
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "")
	fx.writeWaitAt(t, "pr", "its branch is not on origin yet")

	var out bytes.Buffer

	fx.in.Out = &out
	if err := fx.in.Next(t.Context(), r); err != nil {
		t.Fatal(err)
	}

	if r.Phase != AtParked || !r.ParkedOrigin ||
		!strings.Contains(out.String(), "GH-2 ship: waiting on GH-1 for origin") {
		t.Errorf("phase = %v, origin = %v, output = %s", r.Phase, r.ParkedOrigin, out.String())
	}
}

// A base that committed between the ship's look and the drive's read of its
// stop gets ship sent again at once, not parked until a second commit; the
// same head stopping it twice parks it, so the two never loop.
func TestDriveResendsAShipWhoseBaseCommittedFirst(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-2")+" "+
		Steered(SkillPrompt("ship", "GH-2", nil, runplan.ShipFlow{}, false), "resume at implement"), "{}")
	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "a1")
	fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.Script("git status --porcelain", "")

	fx.f.Script(testkit.WsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's stop
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // the same head stops it again
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeWait(t) }
	fx.onTick[4] = func() { fx.writeWait(t) }
	// GH-1 halts, which ends the parked GH-2 at its next boundary
	fx.onTick[5] = func() { fx.writeRunFor(t, "GH-1", "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	out := fx.out.String()
	if !strings.Contains(out, "GH-2 ship: its base has code now: resume at implement") {
		t.Errorf("ship was not sent again at once:\n%s", out)
	}

	if strings.Count(out, "resume at implement") != 1 || !strings.Contains(out, "GH-2 ship: waiting on GH-1 for code") {
		t.Errorf("the same head should park the second stop, not resend:\n%s", out)
	}
}

// A parked task gets no moved mark: the catchup it would send meets a ship
// stop it cannot resume, and ship's own guard brings the base in anyway.
func TestDriveMarksNoParkedTask(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	b := &DriveRun{Phase: AtRunning, O: start.OpenedTask{Branch: "GH-1"}}
	r := &DriveRun{Phase: AtParked, Base: b, O: start.OpenedTask{Branch: "GH-2"}}

	if err := fx.in.MarkMoved(t.Context(), []*DriveRun{b, r}, nil); err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("git rev-parse -q --verify refs/heads/GH-1") {
		t.Errorf("a parked task was checked for a moved base: %q", fx.f.Calls)
	}

	if !HeldStop(&state.Run{Ship: "blocked", Leaf: "implement", Wait: "GH-1"}) {
		t.Error("a catchup does not hold a parked ship's stop")
	}
}

// A ship parked on a base this drive does not run halts with its note:
// nothing here will ever give that base code.
func TestDriveHaltsAShipWaitingOnABaseOutsideIt(t *testing.T) {
	fx := newDriveFixture(t, "GH-2", "ship")
	fx.task("GH-2", hostops.TaskInfo{Body: "x"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-2", "/code/repo.GH-2"))
	fx.opens("GH-2", "ws:GH-2", "pane:1")
	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-2")+" "+SkillPrompt("ship", "GH-2", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.Script(testkit.WsList, unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))
	fx.onTick[2] = func() { fx.writeWait(t) }

	if err := fx.drive(t); testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if out := fx.out.String(); !strings.Contains(
		out,
		"GH-2 halted at ship: waiting on GH-1: its branch has no code yet",
	) {
		t.Errorf("output = %s", out)
	}
}

// The push after a catchup leases against origin's copy when the catchup went
// out and refuses once origin moved since. With no record held (a rerun, or
// a skill sent after the catchup) only a fast-forward goes out. Only a push
// that went out spends the lease.
func TestPushCatchupOnlyOverWhatTheCatchupSaw(t *testing.T) {
	cases := map[string]struct {
		held        bool   // no skill went out since the catchup
		pushed      string // origin's copy of the branch at the push
		fastForward bool   // HEAD holds origin's copy: the catchup merged
		push        string // the push expected, "" for none
		next        string // the halt's next line when refused
	}{
		"origin held": {
			held: true, pushed: "p1",
			push: "git push --force-with-lease=refs/heads/GH-1:p1 origin HEAD:refs/heads/GH-1",
		},
		"origin moved": {
			held: true, pushed: "p2",
			next: "next: bring origin/GH-1 into /code/repo.GH-1, then /f10:catchup there, then rerun the drive",
		},
		"no record, rebased": {
			pushed: "p2",
			next:   "next: check origin/GH-1 holds nothing /code/repo.GH-1 lacks",
		},
		"no record, merged": {
			pushed: "p2", fastForward: true, push: "git push origin HEAD:refs/heads/GH-1",
		},
		// a branch that already holds what origin gained drops nothing
		"origin moved, merged": {
			held: true, pushed: "p2", fastForward: true, push: "git push origin HEAD:refs/heads/GH-1",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			ancestor := shell.Result{Code: 1}
			if tc.fastForward {
				ancestor = shell.Result{}
			}

			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", tc.pushed)
			fx.f.Script("git fetch origin", "")
			fx.f.Fail("git merge-base --is-ancestor "+tc.pushed+" HEAD", ancestor)

			if tc.push != "" {
				fx.f.Script(tc.push, "")
			}

			rec := &drivestore.Catchup{Base: "origin/main", Origin: "p1", Spent: !tc.held}

			_, err := stack.PushCatchup(context.Background(), "/code/repo.GH-1", "GH-1", rec)

			if tc.push != "" {
				if err != nil || !fx.f.Called(tc.push) {
					t.Fatalf("err = %v, want %q: %q", err, tc.push, fx.f.Calls)
				}

				if !rec.Spent {
					t.Error("the record still holds after a push used it")
				}

				return
			}

			if !errors.As(err, new(stack.OriginMovedError)) || fx.f.Called("git push") {
				t.Fatalf("err = %v, want refused with no push: %q", err, fx.f.Calls)
			}

			// a refusal spends the lease, so a rerun never replays it
			if !rec.Spent {
				t.Error("a refused push left its lease live")
			}

			res := PushHalted("GH-1", "finish", err)
			if !slices.ContainsFunc(res.Lines, func(l string) bool { return strings.HasPrefix(l, tc.next) }) {
				t.Errorf("halt lines = %q, want one starting %q", res.Lines, tc.next)
			}
		})
	}
}

// A finish that deferred its pull is finished all the same, the drive's
// report carries the note, and the next tick does not say it again.
func TestDriveFinishWithADeferredPullIsFinished(t *testing.T) {
	const note = "main not pulled: the local main has commits origin/main lacks"

	in := &DriveInput{
		Root: t.TempDir(),
		Now:  time.Now,
		Finish: func(_ context.Context, _ io.Writer, req DriveFinish) error {
			req.Deferred(note)

			return nil
		},
		Answers: NewAnswerBook(),
	}
	in.Out = io.Discard

	if in.PullLine != "" {
		t.Fatal("pullLine set before any finish")
	}

	defer func() {
		if in.PullLine != note || !in.RanFinish {
			t.Errorf("after the finish pullLine = %q, ranFinish = %v; want the note, so a tick says it once",
				in.PullLine, in.RanFinish)
		}
	}()

	res := in.RunFinish(t.Context(), &DriveRun{Task: DriveTask{Ref: testkit.Gh1}})
	if res.Row != drivectl.RowFinished || res.Code != 0 || len(res.Lines) != 1 ||
		!strings.Contains(res.Lines[0], "GH-1 finished; main not pulled") {
		t.Errorf("runFinish = %+v, want finished with the note", res)
	}
}

// The drive retries a deferred pull each tick, saying a cause once, and
// says when it lands.
func TestDriveRetriesADeferredPull(t *testing.T) {
	f := testkit.NewFakes(t)
	main, root := t.TempDir(), t.TempDir()

	if err := os.WriteFile(filepath.Join(root, finish.PendingPullFile), []byte("main m3rge\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.Script(testkit.CurBr, "main")
	f.Fail("git pull --ff-only", shell.Result{Code: 1, Stderr: "fatal: Not possible to fast-forward, aborting."})

	in := &DriveInput{
		Main: main,
		Root: root,
	}
	var out bytes.Buffer

	in.Out = &out

	// a drive that ran no finish never pulls main
	in.RetryPull(t.Context())

	if f.Called("git pull") {
		t.Fatal("a drive that ran no finish pulled main")
	}

	in.RanFinish = true
	in.RetryPull(t.Context())
	in.RetryPull(t.Context())

	if n := strings.Count(out.String(), "main not pulled"); n != 1 {
		t.Errorf("a diverged main was said %d times:\n%s", n, out.String())
	}

	f.Script("git pull --ff-only", "")
	f.Script("git merge-base --is-ancestor m3rge main", "")
	out.Reset()

	in.RetryPull(t.Context())

	if !strings.Contains(out.String(), "pulled main up to m3rge") {
		t.Errorf("retry after the pull lands:\n%s", out.String())
	}

	if def, _ := finish.ReadPendingPull(root); def != "" {
		t.Error("the pending pull outlived the pull")
	}
}

// The drive's resume line names a labelled entry by its label, while the
// steering it sends keeps the bare entry and its place, which ship matches.
func TestResumeShownUsesTheLabel(t *testing.T) {
	run := &state.Run{
		Ship: "partial", Leaf: "pr", At: 4, Stop: state.StopGuard,
		Pipeline: []string{"implement", "pr", "review:local", "pr"}, Labels: []string{"", "", "", "pr-polish"},
	}

	at := ResumeAt("ship", run)
	if at != "resume at pr (entry 4)" {
		t.Fatalf("resumeAt = %q", at)
	}

	if got := ResumeShown(run, at); got != "resume at pr-polish" {
		t.Errorf("resumeShown = %q, want resume at pr-polish", got)
	}

	run.Labels = nil
	if got := ResumeShown(run, at); got != at {
		t.Errorf("unlabelled resumeShown = %q, want %q", got, at)
	}
}

// A run that ends while its catchup's verify runs stops the verify, so a
// cancelled drive leaves no test suite running behind it.
func TestDriveEndStopsARunningVerify(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	r := &DriveRun{Phase: AtRunning, O: start.OpenedTask{Branch: "GH-1"}}

	log := filepath.Join(t.TempDir(), "verify.log")
	r.Job = StartVerifyJob(t.Context(), func(ctx context.Context) (stack.VerifyRun, error) {
		return stack.RunVerify(ctx, t.TempDir(), []string{"sleep 30"}, log)
	})

	fx.in.End(t.Context(), r, DriveResult{Row: "cancelled", Code: ui.ExitHalted, Drives: true})

	select {
	case <-r.Job.Done:
		if !errors.Is(r.Job.Err, context.Canceled) {
			t.Errorf("verify ended with %v, want it cancelled", r.Job.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the verify still runs after its run ended")
	}
}

// Every way out of the drive loop, a drain included, waits out the kill of a
// verify still running, since the process exits right after.
func TestLeaveRunsHaltsAVerifyBeforeReturning(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}}

	log := filepath.Join(t.TempDir(), "verify.log")
	r.Job = StartVerifyJob(t.Context(), func(ctx context.Context) (stack.VerifyRun, error) {
		return stack.RunVerify(ctx, t.TempDir(), []string{"sleep 30"}, log)
	})

	fx.in.LeaveRuns(t.Context(), []*DriveRun{r})

	if !r.Job.Finished() {
		t.Fatal("leaveRuns returned with the verify still running")
	}
}
