package main

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
	"github.com/amberpixels/f10/cli/internal/drives"
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
	// a catchup the drive sends records where it starts from: the default
	// branch is main, local only, and the branch merges with it cleanly
	// until a test says otherwise
	fx.f.script(headRef, "origin/main")
	fx.f.script("git rev-parse -q --verify refs/heads/main", "main0")
	fx.f.script("git rev-parse -q --verify HEAD", "head0")
	fx.f.script("git merge-tree --write-tree --no-messages main0 HEAD", "tree")

	// a test names the chain after the ids to run the machinery on any
	// skill; drive itself derives the chain from the flow
	var chain, rest []string

	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(oldChainSkills, args[i]):
			chain = append(chain, args[i])
		case args[i] == "--answer" || args[i] == "--model" || args[i] == "--agent" ||
			args[i] == "--every" || args[i] == "--flow":
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
		finish: func(context.Context, io.Writer, driveFinish) error {
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

// opensOn is opens with the agent started on profile, as typed, "" for none.
func (fx *driveFixture) opensOn(id, ws, pane, profile string) {
	agent := agentOf(id)

	var p agentctl.Profile

	start := "herdr agent start " + agent + " --kind claude --pane " + pane
	if profile != "" {
		parsed, err := agentctl.ParseProfile(profile)
		if err != nil {
			fx.f.t.Fatal(err)
		}

		p, _, _ = parsed.Resolve()
		start += " -- " + strings.Join(p.Args(), " ")
	}

	fx.f.script("wt switch --no-cd --yes --format json --create "+id, "{}")
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo."+id+" --label "+id,
		`{"result":{"workspace":{"workspace_id":"`+ws+`"},"root_pane":{"pane_id":"`+pane+`"},"already_open":false}}`)
	fx.f.script(start, "{}")

	if profile != "" {
		fx.f.script("git config branch."+id+"."+cfgAgent+" "+p.String(), "")
	}
}

// expects scripts the prompt for one skill and the record of it done.
func (fx *driveFixture) expects(id, skill string) {
	prompt := skillPrompt(skill, id, nil, shipFlow{}, slices.Contains(fx.in.chain, "finish"))
	fx.f.script("herdr agent prompt "+agentOf(id)+" "+prompt, "{}")
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

// writeShipStop records GH-1's ship stopped at the leaf in the default
// flow's pipeline, at place at, with the note and next it left.
func (fx *driveFixture) writeShipStop(t *testing.T, status, leaf string, at int, next string) {
	t.Helper()

	body := fmt.Sprintf(
		"v 1\ntask GH-1\nroot /code/repo.GH-1\ncapture prior\nplan done\nship %s\nleaf %s\nat %d\n"+
			"pipeline judge → implement → cleanup → pr → review:local → judge → pr\n"+
			"note conflict with main\nnext %s\nask \nupdated %d\n",
		status,
		leaf,
		at,
		next,
		fx.clock.Unix(),
	)

	if err := os.WriteFile(filepath.Join(fx.in.stateDir, "s-GH-1.state"), []byte(body), 0o644); err != nil {
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
		name          string
		args          []string
		model1        string
		model2        string
		recorded      string // the profile GH-1's branch records
		recordedModel string // the model GH-1's branch recorded before profiles
		out, notWant  string
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
		{
			name: "a model recorded before profiles", args: []string{"GH-1", "GH-2", "judge"},
			model1: "opus", recordedModel: "opus", out: "workspace ws:GH-1 on opus",
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
			if c.recorded != "" {
				fx.f.script("git config --get branch.GH-1."+cfgAgent, c.recorded)
			}

			if c.recordedModel != "" {
				fx.f.script("git config --get branch.GH-1."+cfgModel, c.recordedModel)
			}

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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}, false), "{}")
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
	fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
	fx.f.script("git status --porcelain", "")
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
			fx.adoptsGH1()
			fx.writeShipStop(t, tc.status, tc.leaf, tc.at, catchupNext)

			fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+tc.prompt, "{}")
			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")
			fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
			fx.onTick[2] = func() {
				if tc.again {
					fx.writeShipStop(t, "partial", "pr", 7, catchupNext)

					return
				}

				fx.writeRun(t, "done", "", "", "")
			}

			err := fx.drive(t)

			if !fx.f.called("herdr agent prompt " + agentOf("GH-1") + " " + tc.prompt) {
				t.Errorf("ship was not prompted with %q: %q\n%s", tc.prompt, fx.f.calls, fx.out.String())
			}

			if tc.again {
				if exitCode(err) != exitHalted ||
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
	}{
		"merge in progress": {"partial", "pr", catchupNext, [2]string{"git rev-parse -q --verify MERGE_HEAD", "abc"}},
		"dirty tree":        {"partial", "pr", catchupNext, [2]string{"git status --porcelain", " M a.go"}},
		"red verify":        {"failed", "implement", "fix the failing test, then rerun the drive", [2]string{}},
		"catchup failed, merge left": {
			"failed",
			"catchup",
			catchupNext,
			[2]string{"git rev-parse -q --verify MERGE_HEAD", "abc"},
		},
		"review advises catchup": {"partial", "review:local", catchupNext, [2]string{}},
		"judge stop, marked": {
			"blocked",
			"judge",
			"answer the judge, then rerun the drive",
			[2]string{"git config --get branch.GH-1." + cfgLanded, "main"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()

			if tc.git[0] != "" {
				fx.f.script(tc.git[0], tc.git[1])
			}

			fx.writeShipStop(t, tc.status, tc.leaf, 4, tc.next)
			fx.f.script(wsList, unopened, listing("GH-1", "idle"))

			err := fx.drive(t)
			if exitCode(err) != exitHalted {
				t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, fx.out.String())
			}

			out := fx.out.String()
			if !strings.Contains(out, ": conflict with main") || !strings.Contains(out, "next: "+tc.next) {
				t.Errorf("output = %s", out)
			}

			if fx.f.called("herdr agent prompt") {
				t.Errorf("prompted on an unsettled stop: %q", fx.f.calls)
			}
		})
	}
}

// A ship whose catchup guard conflicted gets a catchup from the drive and
// resumes where it stopped; a second guard conflict is judged by progress,
// so main moving with other files conflicting catches up again, else a halt.
func TestDriveSettlesAShipGuardConflict(t *testing.T) {
	landed := "git config --get branch.GH-1." + cfgLanded
	catchup := "herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven"
	resume := "herdr agent prompt " + agentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"
	conflicts := "git merge-tree --write-tree --no-messages "

	// conflictsAgain is what a resumed ship whose guard conflicted once more
	// leaves: the stop, the mark, and git's view of main and the branch
	conflictsAgain := func(main, files string) func(fx *driveFixture, t *testing.T) {
		return func(fx *driveFixture, t *testing.T) {
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.script(landed, "main")
			fx.f.script("git rev-parse -q --verify refs/heads/main", main)
			fx.f.script("git rev-parse -q --verify HEAD", "head1")
			fx.f.fail(conflicts+main+" HEAD", shell.Result{Code: 1, Stdout: files})
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
				fx.f.script("git config --get branch.GH-1."+cfgLanded, "")
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
			code: exitHalted,
			want: []string{"halted at catchup: the catchup did not end clean", "next: settle it with /f10:catchup in"},
		},
		"asks": {
			after: func(fx *driveFixture, t *testing.T) {
				fx.writeRun(t, "blocked", "catchup", "", "1. Keep both renames? [both | ours]")
			},
			code: exitAsk,
			want: []string{"GH-1 asks at catchup: 1. Keep both renames?"},
		},
		"conflicts again, main did not move": {
			after: restored,
			again: conflictsAgain("main0", aOneWay),
			code:  exitHalted,
			want: []string{
				"GH-1 halted at ship: GH-1 still conflicts with main after a catchup: " +
					"main has not moved since the catchup; conflicting: a.go",
				"next: settle it with /f10:catchup in /code/repo.GH-1, then rerun the drive",
			},
			catchups: 1,
		},
		"conflicts again on the same files the same way": {
			after: restored,
			again: conflictsAgain("main1", aOneWay),
			code:  exitHalted,
			want: []string{
				"GH-1 halted at ship: GH-1 still conflicts with main after a catchup: " +
					"the same files conflict with main the same way again: a.go",
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
			fx.adoptsGH1()
			fx.f.script(catchup, "{}")
			fx.f.script(resume, "{}")
			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")
			// the first catchup is recorded against main0, a.go conflicting one way
			fx.f.fail(conflicts+"main0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})

			// the ship's turn, then the catchup's, then the resumed ship's,
			// then a second catchup's and a second resume's
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.script(wsList, unopened, idle, working, idle, idle, working, idle, idle, working, idle,
				idle, working, idle, idle, working, idle)
			tick := 2

			if tc.rerun {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.f.script(landed, "main")
				fx.f.script(wsList, unopened, idle, working, idle, idle, working, idle)
				tick = 0
			} else {
				ship := skillPrompt("ship", "GH-1", nil, shipFlow{}, false)
				fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+ship, "{}")
				fx.onTick[2] = func() {
					fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
					fx.f.script(landed, "main")
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

			if (tc.code == 0 && err != nil) || (tc.code != 0 && exitCode(err) != tc.code) {
				t.Fatalf("err = %v, want exit %d\n%s", err, tc.code, out)
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}

			if n, want := countCalls(fx.f.calls, catchup), max(tc.catchups, 1); n != want {
				t.Errorf("catchup prompted %d times, want %d: %q", n, want, fx.f.calls)
			}

			if tc.again == nil && fx.f.called(resume) {
				t.Errorf("ship resumed on a catchup that did not end clean: %q", fx.f.calls)
			}
		})
	}
}

// An ask ends the drive only once every active task is held on a base, or
// none is active; a held task with no ask anywhere keeps it polling.
func TestStoppingOnAnAsk(t *testing.T) {
	asked := func() *driveRun { return &driveRun{opened: true, end: &driveResult{code: exitAsk}} }
	moving := func() *driveRun { return &driveRun{opened: true} }
	parked := func() *driveRun { return &driveRun{opened: true, parked: true} }
	waiting := func() *driveRun { return &driveRun{opened: true, waiting: true} }

	cases := map[string]struct {
		runs []*driveRun
		want bool
	}{
		"asked, another moving":  {[]*driveRun{asked(), moving()}, false},
		"asked, another parked":  {[]*driveRun{asked(), parked()}, true},
		"asked, another waiting": {[]*driveRun{asked(), waiting()}, true},
		"asked, nothing active":  {[]*driveRun{asked()}, true},
		"no ask, one parked":     {[]*driveRun{parked()}, false},
		"no ask, one moving":     {[]*driveRun{moving()}, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stopping(tc.runs); got != tc.want {
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
		"landed":     {cfgLanded, "main"},
		"base moved": {cfgMoved, "base1"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mark := "git config --get branch.GH-1." + tc.key
			catchup := "herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven"
			resume := "herdr agent prompt " + agentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"

			fx := newDriveFixture(t, "GH-1", "GH-2", "ship")
			fx.adoptsGH1()
			fx.task("GH-2", taskInfo{body: "second"}, heads("GH-2"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
			fx.f.script(
				"herdr worktree open --cwd /code/repo --path /code/repo.GH-2 --label GH-2",
				`{"result":{"workspace":{"workspace_id":"ws:GH-2"},"root_pane":{"pane_id":"pane:2"},"already_open":true}}`,
			)
			fx.f.script("herdr agent get pane:2", agentFound)
			fx.f.script(catchup, "{}")
			fx.f.script(resume, "{}")
			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.script(mark, tc.target)

			if tc.key == cfgMoved {
				fx.f.script("git config --get branch.GH-1."+cfgAfterBranch, "GH-0")
				fx.f.script("git merge-tree --write-tree --no-messages base1 HEAD", "tree")
			}

			fx.writeRunFor(t, "GH-2", "blocked", "ship", "", "1. Keep the old flag? [yes | no]")

			idle := listing("GH-1", "idle", "GH-2", "idle")
			working := listing("GH-1", "working", "GH-2", "idle")
			fx.f.script(wsList, unopened, idle, unopened, idle, working, idle, idle, working, idle)

			fx.onTick[2] = func() {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
				fx.f.script(mark, "")
			}
			fx.onTick[4] = func() { fx.writeRun(t, "done", "", "", "") }

			err := fx.drive(t)
			out := fx.out.String()

			if exitCode(err) != exitAsk {
				t.Fatalf("err = %v, want exit %d\n%s", err, exitAsk, out)
			}

			for _, call := range []string{catchup, resume} {
				if !fx.f.called(call) {
					t.Errorf("%q was not prompted: %q\n%s", call, fx.f.calls, out)
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
	fx.f.script("git config --get branch.GH-1."+cfgLanded, "")
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

	path := filepath.Join(fx.in.stateDir, "s-"+id+".state")
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
	landed := "git config --get branch.GH-1." + cfgLanded
	resume := "herdr agent prompt " + agentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"

	for name, stale := range map[string]bool{"no signal yet": false, "stale signal": true} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
			fx.f.script(landed, "main")
			fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")
			fx.f.script(resume, "{}")
			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

			if stale {
				fx.reports(t, fx.clock.Add(-time.Hour))
			}

			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			// the catchup's turn ends idle at the second look and stays so
			// until the report lands two looks later; then the look before
			// the resumed ship, its turn and its end
			fx.f.script(wsList, unopened, idle, working, idle, idle, idle, idle, working, idle)

			doneEarly := false
			fx.onTick[3] = func() { doneEarly = strings.Contains(fx.out.String(), "catchup: done") }
			fx.onTick[4] = func() {
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
				fx.f.script(landed, "")
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

			if !fx.f.called(resume) {
				t.Errorf("ship was not resumed after the catchup reported: %q", fx.f.calls)
			}
		})
	}
}

// A turn the drive attached to is read by what it reported, not by the skill
// the drive meant to send: a catchup that ended clean, before or after the
// attach, resumes ship at the stopped entry, and an attached ship stays a ship.
func TestDriveFollowsAnAttachedCatchup(t *testing.T) {
	resume := "herdr agent prompt " + agentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 7) --driven"
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
			fx.adoptsGH1()
			fx.f.script(resume, "{}")
			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

			if tc.unclean {
				fx.f.script("git status --porcelain", " M a.go")
			}

			// the turn's prompt came before this drive
			mark := fx.clock.Add(-time.Minute)
			if err := state.MarkPrompt(fx.in.stateDir, "s-GH-1", "/code/repo.GH-1", mark); err != nil {
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
			fx.f.script(wsList, unopened, working, working, idle, idle, working, idle)
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

			if tc.haltCode != (exitCode(err) == exitHalted) || !tc.haltCode && err != nil {
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

			if tc.resumed != fx.f.called(resume) {
				t.Errorf("ship resumed = %v, want %v: %q\n%s", !tc.resumed, tc.resumed, fx.f.calls, out)
			}
		})
	}
}

// A catchup's agent idle past the grace with no report halts the task as a
// ship turn would: stopped without reporting, the workspace named, the
// halt in the ledger.
func TestDriveHaltsOnACatchupThatStoppedWithoutReporting(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.script("git config --get branch.GH-1."+cfgLanded, "main")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	var recorded []ledger.Event

	fx.in.record = func(_ context.Context, _ string, e ledger.Event) { recorded = append(recorded, e) }

	err := fx.drive(t)
	out := fx.out.String()

	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, out)
	}

	if !strings.Contains(out, "GH-1 halted at catchup") || !strings.Contains(out, "stopped without reporting") ||
		strings.Contains(out, "did not end clean") {
		t.Errorf("output = %s", out)
	}

	if len(recorded) != 1 || recorded[0].Kind != ledger.TaskHalted || recorded[0].Payload.Val["skill"] != "catchup" {
		t.Errorf("recorded %+v, want one halt at catchup", recorded)
	}

	if fx.sleeps < int(stallGrace/defaultEvery) {
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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" go on", "{}")
	fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:ship") {
		t.Error("ship was prompted instead of answered")
	}
}

// A judge stop, blocked with no question, halts the task with its reason,
// and the task after it is halted with it. The record keeps GH-2's base.
func TestDriveHaltsOnAJudgeStop(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge")
	fx.in.root = t.TempDir()
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.task("GH-2", taskInfo{body: "After: GH-1"}, "")
	fx.f.script(headRef, "origin/main") // GH-1 has no commit, so GH-2 waits
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}, false), "{}")
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

	rec, err := drives.Read(fx.in.root, 1)
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
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("ship", "GH-1", nil, shipFlow{}, false), "{}")
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

	fx.in.finish = func(_ context.Context, w io.Writer, req driveFinish) error {
		finished = append(finished, req.task.ID)
		fmt.Fprintln(w, "task:  "+req.task.ID)

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
	addDir := " -- " + strings.Join(agentArgs(agentctl.Profile{}, fx.in.plansDir, "/code/repo.GH-1"), " ")
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
		{
			args: []string{
				"GH-1:claude:opus:high", "--agent", "sonnet:low", "https://github.com/o/r/issues/2:opus:max",
			},
			tasks: "GH-1 https://github.com/o/r/issues/2", model: "sonnet:low",
			models: "claude:opus:high|opus:max", every: defaultEvery,
		},
		{args: []string{"GH-1:"}, wantErr: "names no profile"},
		{args: []string{"GH-1:opus", "--", "GH-4"}, wantErr: "run-wide --agent"},
		{args: []string{"GH-1", "--agent", "opus", "--model", "fable"}, wantErr: "give one"},
		{args: []string{"GH-1", "--model", "high"}, wantErr: "names an effort"},
		{args: []string{"GH-1", "--model", "opus:high"}, wantErr: "a model alone"},
		{args: []string{"GH-1:opus:huge"}, wantErr: "not an effort"},
		{args: []string{"GH-1", "--agent", "opencode:opus:high"}, wantErr: "not an agent kind"},
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
		{args: []string{"GH-1", "--parallel", "0"}, wantErr: "a positive number"},
		{args: []string{"GH-1", "--parallel=many"}, wantErr: "a positive number"},
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
			strings.Join(a.answers, "|") != c.answers || a.every != c.every || a.agent != c.model ||
			(c.models != "" && strings.Join(a.profiles, "|") != c.models) {
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
		skillPrompt("plan", "GH-1", nil, shipFlow{}, false):              "/f10:plan GH-1 --driven",
		skillPrompt("ship", "GH-1", nil, shipFlow{}, false):              "/f10:ship GH-1 --driven",
		skillPrompt("ship", "GH-1", nil, shipFlow{name: "local"}, false): "/f10:ship local: GH-1 --driven",
		skillPrompt("plan", "GH-1", nil, shipFlow{name: "local"}, false): "/f10:plan GH-1 --driven",
		skillPrompt("judge", "GH-1", dep, shipFlow{}, false):             "/f10:judge GH-1 --driven",
	}

	for got, want := range cases {
		if got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}

	if p := skillPrompt("ship", "GH-1", dep, shipFlow{}, false); !strings.Contains(p, "GH-1 depends on GH-7") {
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

// wave is the fixture for five tasks: GH-12, GH-13 and GH-14 after GH-11,
// GH-15 after GH-13, with finish recorded among the calls.
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

	fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
		task := req.task
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

// A dependent started before its base finished is caught up before its next
// skill, which follows only once the mark is gone and the tree is clean; a
// catchup that leaves the mark halts the task.
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

			fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
				task := req.task
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
			// the catchup reports as its turn ends, clean or not
			fx.onTick[4] = func() { fx.reportsFor(t, "GH-2", fx.clock) }

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

// A pull request conflicting with the default branch at finish, as a sibling
// that merged first leaves it, is caught up and pushed with a lease before
// finish tries it again; a second conflict is judged by progress.
func TestDriveCatchesUpAConflictingPullBeforeFinish(t *testing.T) {
	conflicts := "git merge-tree --write-tree --no-messages "
	cases := map[string]struct {
		// after the catchup: HEAD, origin/main, and what HEAD conflicts on
		// (aOneWay, aChanged and bOneWay are merge-tree's conflicted stages)
		head, main, files string
		again             bool
		halt              string
	}{
		"settled": {head: "new", main: "m1"},
		"unchanged": {
			head: "head0", main: "m2", files: bOneWay,
			halt: "the catchup left the branch unchanged; conflicting: b.go",
		},
		"main did not move": {
			head: "new", main: "m1", files: aOneWay,
			halt: "origin/main has not moved since the catchup; conflicting: a.go",
		},
		"same files the same way": {
			head: "new", main: "m2", files: aOneWay,
			halt: "the same files conflict with origin/main the same way again: a.go",
		},
		"same file, main changed it": {head: "new", main: "m2", files: aChanged, again: true},
		"main moved, other files":    {head: "new", main: "m2", files: bOneWay, again: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.script("git fetch origin", "")
			// origin's copy of the branch before the catchup, then what the push left
			fx.f.script("git rev-parse -q --verify origin/GH-1", "old", tc.head)
			// read before and after the fetch when the catchup goes out, then
			// before and after the push's fetch, then what the push left
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1", "old", "old", "old", "old", tc.head)
			fx.f.fail("git merge-tree --write-tree origin/main old", shell.Result{Code: 1})
			fx.f.script("git config branch.GH-1."+cfgLanded+" main", "")
			// before the catchup, the switch that sends it, after it, before
			// finish; a second conflict marks and reads it once more
			landed := []string{"", "main", "", "", ""}
			if tc.again {
				landed = []string{"", "main", "", "", "main", "", ""}
			}

			fx.f.script("git config --get branch.GH-1."+cfgLanded, landed...)
			fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
			fx.f.script("git status --porcelain", "")
			fx.f.fail("git merge-base --is-ancestor old HEAD", shell.Result{Code: 1})
			fx.f.script("git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1", "")
			// origin holds the target, so the caught-up branch may be pushed
			fx.f.script("git merge-base --is-ancestor m1 refs/remotes/origin/main", "")
			// the record when the catchup goes out: origin/main at m1, ahead of
			// the local main, HEAD at head0, a.go conflicting one way
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "m1", tc.main)
			fx.f.fail("git merge-base --is-ancestor m1 main0", shell.Result{Code: 1})
			fx.f.fail("git merge-base --is-ancestor "+tc.main+" main0", shell.Result{Code: 1})
			fx.f.script("git rev-parse -q --verify HEAD", "head0", tc.head)
			fx.f.fail(conflicts+"m1 HEAD", shell.Result{Code: 1, Stdout: aOneWay})
			fx.f.fail(conflicts+tc.main+" HEAD", shell.Result{Code: 1, Stdout: tc.files})

			// origin's copy after the push: clean once settled, else still conflicting
			settled := tc.halt == "" && !tc.again
			if settled {
				fx.f.script("git merge-tree --write-tree origin/main "+tc.head, "")
			} else {
				fx.f.fail("git merge-tree --write-tree origin/main "+tc.head, shell.Result{Code: 1})
			}

			if tc.again {
				fx.f.answers["git merge-tree --write-tree origin/main new"] = []shell.Result{{Code: 1}, {}}
			}

			fx.f.fail("git merge-base --is-ancestor origin/main HEAD", shell.Result{Code: 1})
			fx.f.fail("git merge-tree --write-tree origin/main HEAD", shell.Result{Code: 1})

			fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
				task := req.task
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)

				return nil
			}

			// the open, the catchup's send, its turn and its end; a second
			// catchup: its send, its turn and its end
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.script(wsList, unopened, idle, working, idle, idle, working, idle)
			fx.onTick[2] = func() { fx.reports(t, fx.clock) }
			fx.onTick[4] = func() { fx.reports(t, fx.clock) }

			err := fx.drive(t)
			out := fx.out.String()
			catchups := countCalls(fx.f.calls, "herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven")
			push := "git push --force-with-lease=refs/heads/GH-1:old origin HEAD:refs/heads/GH-1"

			// the catchup's clean end reaches origin before finish tries the branch again
			if n := countCalls(fx.f.calls, push); n != 1 {
				t.Errorf("pushed %d times after the catchup, want once: %q", n, fx.f.calls)
			}

			if tc.halt != "" {
				if exitCode(err) != exitHalted ||
					!strings.Contains(out, "GH-1 still conflicts with origin/main after a catchup: "+tc.halt) ||
					!strings.Contains(out, "next: settle it with /f10:catchup in /code/repo.GH-1") {
					t.Fatalf("err = %v\n%s", err, out)
				}

				if fx.f.called("finish GH-1") || catchups != 1 {
					t.Errorf("a branch still conflicting was merged or caught up again: %q", fx.f.calls)
				}

				return
			}

			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			if want := map[bool]int{false: 1, true: 2}[tc.again]; catchups != want {
				t.Errorf("catchup prompted %d times, want %d: %q", catchups, want, fx.f.calls)
			}

			mark := "git config branch.GH-1." + cfgLanded + " main"
			catchup := "herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven"
			order := []string{mark, catchup, push, "finish GH-1"}

			if tc.again {
				order = []string{mark, catchup, push, mark, catchup, "finish GH-1"}
			}

			fx.assertOrder(t, order,
				"git config branch.GH-1."+cfgLanded+" ", "herdr agent prompt", "git push", "finish")

			for _, want := range []string{
				"conflicts with origin/main, catching up first",
				"GH-1 catchup: GH-1 caught up with origin/main, pushed with --force-with-lease",
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
	landed := "git config branch.GH-1." + cfgLanded + " main"
	moved := "git config branch.GH-1." + cfgMoved + " b2"
	ship := "herdr agent prompt " + agentOf("GH-1") + " " + skillPrompt("ship", "GH-1", nil, shipFlow{}, true)
	catchup := "herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven"
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
		fetches  int    // git fetch origin calls: the boundary before ship, the catchup's send, after it, the push, finish
		halt     string
		next     string // the halt's next line, "next: rerun the drive" when ""
	}{
		"origin/main moved":      {pushed: "p1", mark: landed, fetches: 5},
		"never pushed":           {fetches: 1},
		"already an ancestor":    {pushed: "p1", ancestor: true, fetches: 2},
		"already marked":         {pushed: "p1", marked: true, fetches: 5},
		"fetch fails":            {pushed: "p1", fetch: shell.Result{Code: 128, Stderr: "no route"}, fetches: 2},
		"an unlisted base moved": {pushed: "p1", after: "GH-9", mark: moved, fetches: 5},
		"an unlisted base moved, not on origin": {
			pushed: "p1", after: "GH-9", unpushed: true, mark: moved, fetches: 5,
		},
		"push rejected": {
			pushed: "p1", mark: landed, pushFail: true, fetches: 3,
			halt: "GH-1 halted at catchup: pushing GH-1, caught up with origin/main: ",
		},
		"origin moved during the catchup": {
			pushed: "p1", mark: landed, moved: true, fetches: 3,
			halt: "GH-1 halted at catchup: GH-1 caught up with origin/main, and origin/GH-1 moved meanwhile",
			next: "next: bring origin/GH-1 into /code/repo.GH-1, then /f10:catchup there",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship", "finish")
			fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.expects("GH-1", "ship")
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1", tc.pushed)
			fx.f.answers["git fetch origin"] = []shell.Result{tc.fetch, {}}

			originMain := "m2"
			if tc.pushed == "" {
				originMain = "" // finish merges a branch never pushed into the local default branch
				fx.f.script("git merge-tree --write-tree main0 refs/heads/GH-1", "")
			}

			fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", originMain)
			target := "m2"

			if tc.after != "" {
				target = "b2"
				fx.f.script("git config --get branch.GH-1."+cfgAfterBranch, tc.after)
				fx.f.script("git rev-parse -q --verify refs/heads/"+tc.after, "b2")

				if !tc.unpushed {
					fx.f.script("git rev-parse -q --verify refs/remotes/origin/"+tc.after, "b2")
				}
			}

			// behind at the boundary, caught up once the catchup ran
			ancestor := []shell.Result{{Code: 1}, {}}
			if tc.ancestor || tc.marked {
				ancestor = []shell.Result{{}}
			}

			fx.f.answers["git merge-base --is-ancestor "+target+" refs/heads/GH-1"] = ancestor
			// local main does not hold origin's, so origin/main is the target
			fx.f.fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
			// origin holds the target, unless the base was never pushed
			fx.f.script("git merge-base --is-ancestor m2 refs/remotes/origin/main", "")
			onOrigin := shell.Result{}
			if tc.unpushed {
				onOrigin = shell.Result{Code: 1}
			}

			fx.f.fail("git merge-base --is-ancestor b2 refs/remotes/origin/"+tc.after, onOrigin)
			fx.f.script(landed, "")
			fx.f.script(moved, "")

			// the mark read by the boundary's check, then by the boundary as it
			// sends the catchup (which reads a moved mark once more for its
			// target), then after the catchup and before finish
			switch {
			case tc.marked:
				fx.f.script("git config --get branch.GH-1."+cfgLanded, "main", "main", "")
			case tc.mark == landed:
				fx.f.script("git config --get branch.GH-1."+cfgLanded, "", "main", "")
			case tc.mark == moved:
				fx.f.script("git config --get branch.GH-1."+cfgMoved, "", "b2", "b2", "")
			}

			fx.f.script(catchup, "{}")
			fx.f.script("git merge-tree --write-tree --no-messages "+target+" HEAD", "tree")
			fx.f.script("git rev-parse -q --verify HEAD", "head0", "head1")
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
			fx.f.script("git status --porcelain", "")
			fx.f.fail("git merge-base --is-ancestor p1 HEAD", shell.Result{Code: 1})
			fx.f.script("git rev-parse -q --verify origin/GH-1", tc.pushed)
			fx.f.script("git merge-tree --write-tree origin/main p1", "")

			if tc.pushFail {
				fx.f.fail(push, shell.Result{Code: 1, Stderr: "rejected: stale info"})
			} else {
				fx.f.script(push, "")
			}

			fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
				task := req.task
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)

				return nil
			}

			// the open, whose boundary marks and sends the catchup; the
			// catchup's turn and its end, which sends ship; ship's turn and
			// its end, where finish runs
			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			fx.f.script(wsList, unopened, idle, working, idle, idle, working, idle)
			fx.onTick[2] = func() { // the catchup reports as its turn ends
				if tc.moved {
					fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p2")
				}

				fx.reports(t, fx.clock)
			}

			err := fx.drive(t)
			out := fx.out.String()

			if n := countCalls(fx.f.calls, "git fetch origin"); n != tc.fetches {
				t.Errorf("fetched %d times, want %d: %q", n, tc.fetches, fx.f.calls)
			}

			if tc.halt != "" {
				next := cmp.Or(tc.next, "next: rerun the drive")
				if exitCode(err) != exitHalted || !strings.Contains(out, tc.halt) ||
					!strings.Contains(out, next) || fx.f.called("finish GH-1") ||
					(tc.moved && fx.f.called("git push")) {
					t.Fatalf("err = %v\n%s", err, out)
				}

				return
			}

			if err != nil || !strings.Contains(out, "GH-1:    finished") {
				t.Fatalf("err = %v\n%s", err, out)
			}

			for _, m := range []string{landed, moved} {
				if n, want := countCalls(fx.f.calls, m), map[bool]int{false: 0, true: 1}[m == tc.mark]; n != want {
					t.Errorf("%q set %d times, want %d: %q", m, n, want, fx.f.calls)
				}
			}

			caught := tc.mark != "" || tc.marked
			if n, want := countCalls(fx.f.calls, catchup), map[bool]int{false: 0, true: 1}[caught]; n != want {
				t.Errorf("catchup prompted %d times, want %d: %q", n, want, fx.f.calls)
			}

			pushes := map[bool]int{false: 0, true: 1}[caught && !tc.unpushed]
			if n := countCalls(fx.f.calls, push); n != pushes {
				t.Errorf("pushed %d times, want %d: %q", n, pushes, fx.f.calls)
			}

			if tc.unpushed &&
				!strings.Contains(out, "caught up with its base, not pushed: its base is not on origin yet") {
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
			order := []string{catchup, push, ship, "git config branch.GH-1." + cfgDrive + " ship", "finish GH-1"}
			if tc.unpushed {
				order = slices.DeleteFunc(order, func(c string) bool { return c == push })
			}

			if tc.mark != "" {
				order = slices.Insert(order, 0, tc.mark)
			}

			fx.assertOrder(t, order, "herdr agent prompt", "git config branch.GH-1."+cfgDrive+" ", landed, moved,
				"git push", "finish")

			want := "origin/main moved past GH-1, catching up before the next skill"
			if tc.after != "" {
				want = tc.after + " moved past GH-1, catching up before the next skill"
			}

			if tc.marked {
				want = "GH-1 catchup: GH-1 caught up with origin/main, pushed with --force-with-lease (the catchup rewrote it)"
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
	fx.adoptsGH1()
	fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")
	ship := skillPrompt("ship", "GH-1", nil, shipFlow{}, false)
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+ship, "{}")
	fx.onTick[2] = func() {
		fx.writeShipStop(t, "failed", "implement", 2, "fix the two red tests, then rerun")
		fx.f.script("git config --get branch.GH-1."+cfgLanded, "main")
	}
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	err := fx.drive(t)
	out := fx.out.String()

	if exitCode(err) != exitHalted || !strings.Contains(out, "GH-1 halted at ship: conflict with main") {
		t.Fatalf("err = %v\n%s", err, out)
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:catchup") {
		t.Errorf("a catchup went out for a stop ship cannot resume from: %q", fx.f.calls)
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
// still saying running. The drive starts the agent in the leftover's shell,
// and the old run halts nothing, since its agent is gone.
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

	if want := "agent profile opus ignored: no agent was started"; !strings.Contains(fx.out.String(), want) {
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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}, false), "{}")
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
		finish: func(_ context.Context, _ io.Writer, req driveFinish) error {
			got = append(got, req.pr)
			if req.pr == "" {
				return foreignPullError{url: "https://gh/pr/9", author: "alice"}
			}

			return nil
		},
	}

	run := &driveRun{task: driveTask{ref: gh1}}

	res := in.runFinish(t.Context(), io.Discard, run)
	if res.code != exitAsk || len(res.lines) != 1 || !strings.Contains(res.lines[0], "[local | merge]") {
		t.Fatalf("runFinish on someone else's PR = %+v, want an ask", res)
	}

	in.answers["GH-1"] = "1. local"

	if res = in.runFinish(t.Context(), io.Discard, run); res.code != 0 {
		t.Fatalf("runFinish answered local = %+v, want finished", res)
	}

	// a bare answer that is no finish choice belongs to some agent's question
	in.answer = "1. yes"

	if res = in.runFinish(t.Context(), io.Discard, run); res.code != exitAsk || in.answer != "1. yes" {
		t.Errorf("a bare non-choice answer = %+v, left %q; want it left for an agent", res, in.answer)
	}

	in.answers["GH-1"] = "maybe"

	if res = in.runFinish(t.Context(), io.Discard, run); res.code != exitHalted {
		t.Errorf("a keyed answer that is no choice = %+v, want a halt", res)
	}

	if want := []string{"", finishLocal, ""}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("finish was passed %q, want %q", got, want)
	}
}

// A suffixed attempt finishes its own branch: the drive hands finish the
// run's suffix and branch, which the task id alone would not find.
func TestDriveFinishPassesTheRunsBranch(t *testing.T) {
	var got driveFinish

	in := &driveInput{
		main:    t.TempDir(),
		answers: map[string]string{},
		now:     time.Now,
		finish: func(_ context.Context, _ io.Writer, req driveFinish) error {
			got = req

			return nil
		},
	}

	run := &driveRun{task: driveTask{ref: gh1, suffix: "b"}, o: openedTask{branch: "GH-1-b"}}

	if res := in.runFinish(t.Context(), io.Discard, run); res.code != 0 {
		t.Fatalf("runFinish = %+v, want finished", res)
	}

	if got.task.ID != "GH-1" || got.suffix != "b" || got.branch != "GH-1-b" || got.progress == nil {
		t.Errorf("finish was passed %+v, want GH-1, suffix b, branch GH-1-b and a progress hook", got)
	}
}

// The drive's finish takes every setting the command does: under traces: on
// its squash commit names the task, and lore_gate holds as it does by hand.
func TestDriveFinishInputTakesTheSettings(t *testing.T) {
	repo := tracedRepo(t, "main", "on")
	project := "---\ntracker:\n  id: DM-###\ntraces: on\nmerge_method: squash\nlore_gate: block\n---\n"

	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	if err := os.WriteFile(path, []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	tg, err := targetAt(t.Context(), repo, false, "drive")
	if err != nil {
		t.Fatal(err)
	}

	bundle := ref.Ref{ID: "DM-2", Number: "2", Bundle: []string{"DM-1", "DM-2"}}
	req := driveFinish{task: bundle, suffix: "b", branch: "DM-1-b", pr: finishMerge}

	in, err := driveFinishInput(tg, repo, newHost("gh", repo), req)
	if err != nil {
		t.Fatal(err)
	}

	if !in.traces || in.method != "squash" || in.gate != "block" || in.loreStore == nil || in.title == nil {
		t.Errorf("settings = traces %v, method %q, gate %q, lore %v; want them all from project.md",
			in.traces, in.method, in.gate, in.loreStore)
	}

	if in.task.ID != "DM-1" || in.suffix != "b" || in.branch != "DM-1-b" || !in.merge || in.local {
		t.Errorf("run = %s %q %q merge %v local %v; want the lead DM-1, the run's suffix and branch, a merge",
			in.task.ID, in.suffix, in.branch, in.merge, in.local)
	}

	if in.close == nil || len(in.close) != 0 {
		t.Errorf("close = %v, want riders left open, never nil", in.close)
	}
}

// The watcher's finish record says who ran it; the drive's carries none.
func TestFinishedEvent(t *testing.T) {
	for _, by := range []string{"", "watch"} {
		e := finishedEvent("GH-1", by, time.Time{})
		got, set := e.Payload.Val["by"]

		if e.Kind != ledger.TaskFinished || e.Source != ledger.Observed || e.Task != "GH-1" ||
			set != (by != "") || (set && got != by) {
			t.Errorf("by %q: %+v", by, e)
		}
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
	fx.f.script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "1")
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

	fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
		task := req.task
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
	fx.f.script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "0")
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
	fx.f.script("git merge-tree --write-tree --no-messages b2 HEAD", "tree")
	fx.f.script("herdr agent prompt "+agentOf("GH-2")+" /f10:catchup --driven", "{}")
	fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
	fx.f.script("git status --porcelain", "")
	fx.onTick[3] = func() { fx.f.script(moved, "b2") }           // what the drive set
	fx.onTick[4] = func() { fx.f.script(moved, "") }             // the catchup cleared it
	fx.onTick[5] = func() { fx.reportsFor(t, "GH-2", fx.clock) } // and reports as its turn ends

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
	fx.f.script("git rev-list --count --no-merges refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main "+
		"refs/heads/GH-1 c1", "0")

	if fx.in.released(t.Context(), b) {
		t.Error("released with no commit of its own")
	}

	// GH-1 squash-merged and gone: the creation point still keeps its commits out
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "")
	fx.f.script(
		"git rev-list --count --no-merges refs/heads/GH-2 --not refs/heads/main refs/remotes/origin/main c1",
		"0",
		"1",
	)

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
	// again: the catchup ends clean, then conflicts with the same local main,
	// which origin's copy is behind, so the halt names it unmoved
	for _, mode := range []string{"clean", "unsettled", "again"} {
		t.Run(mode, func(t *testing.T) {
			clean := mode == "clean"
			fx := newDriveFixture(t, "GH-1", "finish")
			fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
			fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.f.script(headRef, "origin/main")
			fx.f.script("git fetch origin", "")
			fx.f.script("git rev-parse -q --verify origin/GH-1", "")
			fx.f.script("git rev-parse -q --verify refs/heads/main", "m0")
			fx.f.script(wsList, unopened, listing("GH-1", "idle"))

			fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
				task := req.task
				fx.f.calls = append(fx.f.calls, "finish "+task.ID)

				return nil
			}

			if !clean {
				fx.f.fail("git merge-tree --write-tree m0 refs/heads/GH-1", shell.Result{Code: 1})
				fx.f.fail("git merge-tree --write-tree --no-messages m0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})
				fx.f.script("git config branch.GH-1."+cfgLanded+" main", "")
				fx.f.script("git config --get branch.GH-1."+cfgLanded, "", "main")
				fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup --driven", "{}")

				if mode == "again" {
					// the catchup cleared the mark; origin's main is behind the local one
					fx.f.script("git config --get branch.GH-1."+cfgLanded, "", "main", "")
					fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "o0")
					fx.f.script("git merge-base --is-ancestor o0 m0", "")
					fx.f.script("git rev-parse -q --verify HEAD", "head0", "head1") // the catchup moved the branch
				}

				fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
				fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
				fx.f.script("git status --porcelain", "")
				fx.f.script(wsList,
					unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
				fx.onTick[2] = func() { fx.reports(t, fx.clock) } // the catchup reports as its turn ends
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

			// the catchup left the mark, or the branch conflicts with the same
			// main again: the task halts rather than merging
			want := "the catchup did not end clean"
			if mode == "again" {
				want = "GH-1 still conflicts with main after a catchup: " +
					"main has not moved since the catchup; conflicting: a.go"
			}

			if exitCode(err) != exitHalted || fx.f.called("finish GH-1") ||
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
	fx.in.plansDir = filepath.Join(root, "plans")

	var pairs []string

	for i, id := range []string{"GH-1", "GH-2"} {
		path := "/code/repo." + id
		pairs = append(pairs, id, path)
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i))
		addDir := " -- " + strings.Join(agentArgs(agentctl.Profile{}, fx.in.plansDir, path), " ")
		fx.f.script(fmt.Sprintf("herdr agent start %s --kind claude --pane pane:%d%s", agentOf(id), i, addDir), "{}")
		fx.expects(id, "ship")
	}

	fx.task("GH-1", taskInfo{body: "a question"}, "")
	fx.task("GH-2", taskInfo{body: "After: GH-1"}, "")
	fx.f.script(worktrees, porcelain(pairs...))

	// the ship turn writes the report
	writePlan(t, filepath.Join(root, "spikes", "GH-1.md"))

	fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
		task := req.task
		fx.f.calls = append(fx.f.calls, "finish "+task.ID)
		fx.infos[task.ID] = taskInfo{closed: true}

		return nil
	}

	return fx
}

// A spike's chain ends with its close even where drive_finish leaves finish
// out: no catchup and no merge check before it (neither is scripted), the
// chain recorded with finish for status.
func TestDriveClosesASpikeAfterShip(t *testing.T) {
	for _, chain := range [][]string{{"ship"}, {"ship", "finish"}} {
		t.Run(strings.Join(chain, " "), func(t *testing.T) {
			fx := spikeDrive(t, append([]string{"GH-1"}, chain...)...)
			fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

			if err := fx.drive(t); err != nil {
				t.Fatalf("%v\n%s", err, fx.out.String())
			}

			if !fx.f.called("finish GH-1") {
				t.Errorf("the spike was not closed: %q", fx.f.calls)
			}

			if want := "GH-1:    finished"; !strings.Contains(fx.out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, fx.out.String())
			}

			if record := "git config branch.GH-1." + cfgChain + " ship finish"; !fx.f.called(record) {
				t.Errorf("%q did not run: %q", record, fx.f.calls)
			}
		})
	}
}

// A task after a spike starts once the spike is closed, as on a finished
// base, and runs its own chain without a close of its own.
func TestDriveStartsADependentOfAClosedSpike(t *testing.T) {
	fx := spikeDrive(t, "GH-1", "GH-2", "ship")
	// GH-2 opens on the default branch, its base finished
	fx.f.script(headRef, "origin/main")
	fx.f.script(wsList,
		unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"),
		unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		agentOf("GH-1") + " /f10:ship", "finish GH-1", agentOf("GH-2") + " /f10:ship",
	}, "herdr agent prompt", "finish")

	if fx.f.called("finish GH-2") {
		t.Errorf("a task that is not a spike was closed: %q", fx.f.calls)
	}

	if want := "GH-2:    done: ship"; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}
}

// A spike-reported task whose branch is on origin finishes through its PR,
// so drive takes no shortcut for it.
func TestDriveSpikeShortcutNeedsABranchNeverPushed(t *testing.T) {
	fx := spikeDrive(t, "GH-1", "ship")
	r := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-1"}}, chain: []string{"ship"}, pos: 1}
	r.o.path, r.o.branch = "/code/repo.GH-1", "GH-1"

	if done, _ := fx.in.spikeDone(t.Context(), r); !done {
		t.Error("a spike never pushed took no shortcut")
	}

	fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1", "abc")

	if done, _ := fx.in.spikeDone(t.Context(), r); done {
		t.Error("a pushed spike took the shortcut")
	}
}

// Drive records its finish of a task on the branch for status to read:
// queued once the task opens, then each action finish names.
func TestDriveRecordsFinishProgress(t *testing.T) {
	ids := []string{"GH-11", "GH-12", "GH-13", "GH-14", "GH-15"}
	fx := wave(t, "GH-11", "GH-12", "GH-13", "GH-14", "GH-15", "judge", "finish")

	fx.f.script(wsList,
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...),
		unopened, all("idle", ids...), unopened, all("idle", ids...),
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...),
		unopened, all("idle", ids...),
		all("working", ids...), all("idle", ids...))

	fx.in.finish = func(_ context.Context, _ io.Writer, req driveFinish) error {
		task := req.task
		req.progress("merging #9")
		fx.infos[task.ID] = taskInfo{closed: true}

		return nil
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	var got []string

	for _, c := range fx.f.calls {
		if strings.Contains(c, "GH-11."+ref.FinishKey) {
			got = append(got, c)
		}
	}

	key := "git config branch.GH-11." + ref.FinishKey
	want := []string{key + " queued", key + " running: starting", key + " running: merging #9"}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("finish record writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A chain with no finish writes no finish record.
func TestDriveWithoutFinishWritesNoRecord(t *testing.T) {
	fx := wave(t, "GH-11", "judge")

	fx.f.script(wsList, unopened, all("idle", "GH-11"), all("working", "GH-11"), all("idle", "GH-11"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if !fx.f.called("git config branch.GH-11." + cfgChain) {
		t.Fatal("drive opened no task")
	}

	if fx.f.called("git config branch.GH-11." + ref.FinishKey) {
		t.Error("a chain without finish wrote a finish record")
	}

	for _, c := range fx.f.calls {
		if strings.Contains(c, ref.FinishKey) {
			t.Errorf("unexpected finish record call %q", c)
		}
	}
}

// A task that ends any way but finished drops its finish record; a
// finished one's went with its branch.
func TestDriveEndClearsFinishRecord(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	r := &driveRun{opened: true, chain: []string{"ship", "finish"}, o: openedTask{branch: "GH-1"}}

	fx.in.end(t.Context(), &fx.out, r, driveResult{row: "asked at finish", code: exitAsk})

	unset := "git config --unset branch.GH-1." + ref.FinishKey
	if !fx.f.called(unset) {
		t.Errorf("an ask left the finish record; calls: %v", fx.f.calls)
	}

	fx.f.calls = nil
	fx.in.end(t.Context(), &fx.out, r, driveResult{row: "finished"})

	if fx.f.called(unset) {
		t.Error("a finished task's record was unset again")
	}
}

// Resume steering sits after the subject, before the chained clauses, and
// names the place only for an entry the flow names twice.
func TestResumeAtSteering(t *testing.T) {
	pipeline := []string{"judge", "implement", "cleanup", "pr", "review:local", "judge", "pr"}
	stop := func(leaf string, at int) *state.Run {
		return &state.Run{Ship: "partial", Leaf: leaf, At: at, Pipeline: pipeline, Next: "/f10:catchup --driven"}
	}

	for run, want := range map[*state.Run]string{
		stop("pr", 4):        "resume at pr (entry 4)",
		stop("implement", 2): "resume at implement",
		stop("catchup", 4):   "resume at pr (entry 4)",
		stop("catchup", 0):   "",
		stop("pr", 0):        "resume at pr",
		stop("cleanup", 3):   "",
		{Ship: "partial", Leaf: "pr", At: 4, Pipeline: pipeline, Next: "fix it"}: "",
	} {
		if got := resumeAt("ship", run); got != want {
			t.Errorf("resumeAt(%s, %q) = %q, want %q", run.Leaf, run.Next, got, want)
		}
	}

	if got := resumeAt("plan", stop("pr", 4)); got != "" {
		t.Errorf("a plan resumes at %q", got)
	}

	p := steered(skillPrompt("ship", "GH-1", nil, shipFlow{}, true), "resume at pr")
	if want := "/f10:ship GH-1 resume at pr && " + finishClause + " --driven"; p != want {
		t.Errorf("steered = %q, want %q", p, want)
	}
}

// A ship drive finishes after names it, so the report says nothing is
// owed in that pane; no other prompt carries it.
func TestSkillPromptNamesDriveFinish(t *testing.T) {
	if p := skillPrompt("ship", "GH-1", nil, shipFlow{}, true); p != "/f10:ship GH-1 && "+finishClause+" --driven" {
		t.Errorf("ship with finish = %q", p)
	}

	for _, skill := range []string{"plan", "judge", "catchup"} {
		if p := skillPrompt(skill, "GH-1", nil, shipFlow{}, true); strings.Contains(p, finishClause) {
			t.Errorf("%s prompt names finish: %q", skill, p)
		}
	}
}

// An interrupted drive clears the finish record of every task still in
// flight, so status never reads a drive that is gone as queued.
func TestDriveInterruptClearsFinishRecord(t *testing.T) {
	fx := wave(t, "GH-11", "judge", "finish")
	fx.f.script(wsList, unopened, all("idle", "GH-11"), all("working", "GH-11"))

	ticks := 0
	fx.in.sleep = func(context.Context, time.Duration) error {
		ticks++
		if ticks > 1 {
			return context.Canceled
		}

		return nil
	}

	if err := fx.drive(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("drive = %v, want the interrupt\n%s", err, fx.out.String())
	}

	if unset := "git config --unset branch.GH-11." + ref.FinishKey; !fx.f.called(unset) {
		t.Errorf("%q did not run: %q", unset, fx.f.calls)
	}
}

// writeWait records GH-2's ship parked on GH-1, its branch without code: a
// driven ship's stop at implement with no ask.
func (fx *driveFixture) writeWait(t *testing.T) {
	t.Helper()

	body := fmt.Sprintf(
		"v 1\ntask GH-2\nroot /code/repo.GH-2\ncapture prior\nplan done\nship blocked\nleaf implement\n"+
			"pipeline judge → implement → cleanup → pr\nnote waiting on GH-1: its branch has no code yet\n"+
			"wait GH-1\nupdated %d\n",
		fx.clock.Unix(),
	)

	if err := os.WriteFile(filepath.Join(fx.in.stateDir, "s-GH-2.state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A ship that stops on a base without code parks in the drive with no ask,
// the base runs on, and ship goes again at implement once the base commits.
// The base here lost its only commit (a reset) between release and the park.
func TestDriveParksAShipOnItsBasesCode(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	// GH-2's open and first send, the park, a poll still empty, then code
	fx.f.script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "1", "1", "0", "0", "1")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.script("herdr agent prompt "+agentOf("GH-2")+" "+
		steered(skillPrompt("ship", "GH-2", nil, shipFlow{}, false), "resume at implement"), "{}")
	// the head the park read, then the commit that releases it
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "a1", "a2")
	fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
	fx.f.script("git status --porcelain", "")
	fx.in.asksDir = t.TempDir()

	fx.f.script(wsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2 parks
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 still without code
		listing("GH-1", "working", "GH-2", "idle"), // GH-1 committed
		listing("GH-1", "working", "GH-2", "idle"), // the look before the resend
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
		agentOf("GH-1") + " /f10:ship",
		agentOf("GH-2") + " /f10:ship",
		agentOf("GH-2") + " /f10:ship GH-2 resume at implement",
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

	if entries, _ := os.ReadDir(fx.in.asksDir); len(entries) > 0 {
		t.Errorf("a parked ship raised an ask: %v", entries)
	}
}

// A base that committed between the ship's look and the drive's read of its
// stop gets ship sent again at once, not parked until a second commit; the
// same head stopping it twice parks it, so the two never loop.
func TestDriveResendsAShipWhoseBaseCommittedFirst(t *testing.T) {
	fx := chained(t, "GH-1", "GH-2", "ship")
	fx.expects("GH-1", "ship")
	fx.expects("GH-2", "ship")
	fx.f.script("herdr agent prompt "+agentOf("GH-2")+" "+
		steered(skillPrompt("ship", "GH-2", nil, shipFlow{}, false), "resume at implement"), "{}")
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "a1")
	fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
	fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
	fx.f.script("git status --porcelain", "")

	fx.f.script(wsList,
		unopened, all("idle", "GH-1"),
		unopened, all("idle", "GH-1", "GH-2"),
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // GH-2's stop
		listing("GH-1", "working", "GH-2", "idle"), // the look before the resend
		all("working", "GH-1", "GH-2"),
		listing("GH-1", "working", "GH-2", "idle"), // the same head stops it again
		all("idle", "GH-1", "GH-2"))
	fx.onTick[2] = func() { fx.writeWait(t) }
	fx.onTick[4] = func() { fx.writeWait(t) }
	// GH-1 halts, which ends the parked GH-2 at its next boundary
	fx.onTick[5] = func() { fx.writeRunFor(t, "GH-1", "blocked", "judge", "stop: duplicate of GH-9", "") }

	if err := fx.drive(t); exitCode(err) != exitHalted {
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
	b := &driveRun{opened: true, o: openedTask{branch: "GH-1"}}
	r := &driveRun{opened: true, parked: true, base: b, o: openedTask{branch: "GH-2"}}

	if err := fx.in.markMoved(t.Context(), io.Discard, []*driveRun{b, r}, nil); err != nil {
		t.Fatal(err)
	}

	if fx.f.called("git rev-parse -q --verify refs/heads/GH-1") {
		t.Errorf("a parked task was checked for a moved base: %q", fx.f.calls)
	}

	if !heldStop(&state.Run{Ship: "blocked", Leaf: "implement", Wait: "GH-1"}) {
		t.Error("a catchup does not hold a parked ship's stop")
	}
}

// A ship parked on a base this drive does not run halts with its note:
// nothing here will ever give that base code.
func TestDriveHaltsAShipWaitingOnABaseOutsideIt(t *testing.T) {
	fx := newDriveFixture(t, "GH-2", "ship")
	fx.task("GH-2", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-2", "/code/repo.GH-2"))
	fx.opens("GH-2", "ws:GH-2", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-2")+" "+skillPrompt("ship", "GH-2", nil, shipFlow{}, false), "{}")
	fx.f.script(wsList, unopened, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))
	fx.onTick[2] = func() { fx.writeWait(t) }

	if err := fx.drive(t); exitCode(err) != exitHalted {
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
// out and refuses once origin moved since. With no record held (a resumed
// drive, or a skill sent after the catchup) only a fast-forward goes out.
func TestDrivePushesACaughtUpBranchOnlyOverWhatTheCatchupSaw(t *testing.T) {
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
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			ancestor := shell.Result{Code: 1}
			if tc.fastForward {
				ancestor = shell.Result{}
			}

			fx.f.fail("git merge-base --is-ancestor "+tc.pushed+" HEAD", ancestor)

			if tc.push != "" {
				fx.f.script(tc.push, "")
			}

			r := &driveRun{
				o:           openedTask{branch: "GH-1", path: "/code/repo.GH-1"},
				lastCatchup: &catchupSent{base: "origin/main", pushed: "p1", held: tc.held},
			}

			err := fx.in.pushCaughtUp(context.Background(), io.Discard, r, "finish", tc.pushed, "origin/main")

			if tc.push != "" {
				if err != nil || !fx.f.called(tc.push) {
					t.Fatalf("err = %v, want %q: %q", err, tc.push, fx.f.calls)
				}
			} else {
				if !errors.As(err, new(originMovedError)) || fx.f.called("git push") {
					t.Fatalf("err = %v, want refused with no push: %q", err, fx.f.calls)
				}

				res := pushHalted("GH-1", "finish", err)
				if !slices.ContainsFunc(res.lines, func(l string) bool { return strings.HasPrefix(l, tc.next) }) {
					t.Errorf("halt lines = %q, want one starting %q", res.lines, tc.next)
				}
			}

			if r.lastCatchup.held {
				t.Error("the record still holds after a push used it")
			}
		})
	}
}
