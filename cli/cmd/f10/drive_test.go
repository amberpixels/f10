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
// heads (empty for a task not started).
func (fx *driveFixture) task(id string, info taskInfo, branches string) {
	fx.infos[id] = info
	fx.f.script("git for-each-ref --format=%(refname) refs/heads/"+id+" refs/heads/"+id+"-*", branches)
	fx.f.script("git for-each-ref --format=%(refname) refs/remotes/origin/"+id+" refs/remotes/origin/"+id+"-*", "")
	fx.f.script("git config --get branch."+id+"."+cfgDrive, "")
}

// opens scripts start's create path for a new task under worktrunk.
func (fx *driveFixture) opens(id, ws, pane string) {
	agent := strings.ToLower(id)

	fx.f.script("wt switch --no-cd --yes --format json --create "+id, "{}")
	fx.f.script("herdr worktree open --path /code/repo."+id+" --label "+id,
		`{"result":{"workspace":{"workspace_id":"`+ws+`"},"root_pane":{"pane_id":"`+pane+`"},"already_open":false}}`)
	fx.f.script("herdr agent start "+agent+" --kind claude --pane "+pane, "{}")
}

// expects scripts the prompt for one skill and the record of it done.
func (fx *driveFixture) expects(id, skill string) {
	fx.f.script("herdr agent prompt "+strings.ToLower(id)+" "+skillPrompt(skill, id, nil, false), "{}")
	fx.f.script("git config branch."+id+"."+cfgDrive+" "+skill, "")
}

// writeRun records a run against GH-1's worktree, updated now.
func (fx *driveFixture) writeRun(t *testing.T, ship, leaf, note, ask string) {
	t.Helper()

	const id = "GH-1"

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

// The acceptance run: two tasks, neither started, through judge and ship.
// Both worktrees are created, GH-1 goes through both skills before GH-2 is
// opened, and the report has a row per task.
func TestDriveRunsTheListInOrder(t *testing.T) {
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

	// per skill: the look before the prompt, a poll mid-turn, a poll after
	fx.f.script(wsList,
		listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "done"),
		listing("GH-1", "done"), listing("GH-1", "working"), listing("GH-1", "done"),
		listing("GH-1", "done", "GH-2", "idle"), listing("GH-1", "done", "GH-2", "working"),
		listing("GH-1", "done", "GH-2", "idle"),
		listing("GH-1", "done", "GH-2", "idle"), listing("GH-1", "done", "GH-2", "working"),
		listing("GH-1", "done", "GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	out := fx.out.String()

	var order []string

	for _, c := range fx.f.calls {
		if strings.HasPrefix(c, "herdr agent prompt") || strings.HasPrefix(c, "wt switch") {
			order = append(order, c)
		}
	}

	want := []string{
		"--create GH-1",
		"gh-1 /f10:judge",
		"gh-1 /f10:ship",
		"--create GH-2",
		"gh-2 /f10:judge",
		"gh-2 /f10:ship",
	}
	if len(order) != len(want) {
		t.Fatalf("calls in order = %q", order)
	}

	for i, w := range want {
		if !strings.Contains(order[i], w) {
			t.Errorf("call %d = %q, want it to carry %q", i, order[i], w)
		}
	}

	for _, row := range []string{"GH-1:    done: judge ship", "GH-2:    done: judge ship"} {
		if !strings.Contains(out, row) {
			t.Errorf("report lacks %q:\n%s", row, out)
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
	fx.f.script("herdr agent prompt gh-1 "+skillPrompt("judge", "GH-1", nil, false), "{}")
	fx.f.script(wsList, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
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
	fx.f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.script("herdr agent prompt gh-1 1. proceed", "{}")
	fx.f.script("git config branch.GH-1."+cfgDrive+" judge", "")
	fx.f.script(wsList, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(fx.out.String(), "GH-1:    done: judge") || strings.Contains(fx.out.String(), "unused") {
		t.Errorf("output = %s", fx.out.String())
	}

	if fx.f.called("herdr agent prompt gh-1 /f10:judge") {
		t.Error("the skill was prompted again instead of answered")
	}
}

// A judge stop, blocked with no question, halts the drive with its reason.
// The task after it is not reached.
func TestDriveHaltsOnAJudgeStop(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.task("GH-2", taskInfo{body: "y"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt gh-1 "+skillPrompt("judge", "GH-1", nil, false), "{}")
	fx.f.script(wsList, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "stop: duplicate of GH-9", "") }

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d", err, exitHalted)
	}

	out := fx.out.String()
	for _, want := range []string{"GH-1:    halted at judge", "GH-2:    not reached", "GH-1 halted at judge: stop: duplicate of GH-9"} {
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
	fx.f.script("herdr agent prompt gh-1 "+skillPrompt("ship", "GH-1", nil, false), "{}")
	fx.f.script(wsList, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
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
	fx.task("GH-2", taskInfo{body: "y"}, heads("GH-2"))
	fx.f.script("git config --get branch.GH-2."+cfgDrive, "judge")
	fx.f.script(worktrees, porcelain("GH-2", "/code/repo.GH-2"))
	fx.f.script("herdr worktree open --path /code/repo.GH-2 --label GH-2",
		`{"result":{"workspace":{"workspace_id":"ws:GH-2"},"root_pane":{"pane_id":"pane:2"},"already_open":true}}`)
	fx.expects("GH-2", "ship")
	fx.f.script(wsList, listing("GH-2", "idle"), listing("GH-2", "working"), listing("GH-2", "idle"))

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

	if fx.f.called("herdr agent prompt gh-2 /f10:judge") ||
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

// The After: line is checked against the list before anything is created:
// a base later in the list, and a base outside it not yet finished.
func TestDriveRefusesDependenciesThatCannotHold(t *testing.T) {
	t.Run("base later in the list", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2", "GH-1")
		fx.task("GH-2", taskInfo{body: "x\nAfter: GH-1\n"}, "")
		fx.task("GH-1", taskInfo{body: "y"}, "")

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "comes later in the list") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.called("wt") || fx.f.called("herdr") {
			t.Error("something was created before the refusal")
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
	})

	t.Run("base outside the list, finished", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2")
		fx.task("GH-2", taskInfo{body: "After: GH-9"}, "")
		fx.task("GH-9", taskInfo{closed: true}, "")

		tasks, _, err := fx.in.resolveTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if err := fx.in.checkOrder(t.Context(), tasks); err != nil {
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
		answer  string
		every   time.Duration
		wantErr string
	}{
		{args: []string{"GH-1", "GH-2", "judge", "ship"}, tasks: "GH-1 GH-2", chain: "judge ship", every: defaultEvery},
		{args: []string{"GH-1", "--", "GH-4"}, tasks: "GH-1 GH-4", span: true, every: defaultEvery},
		{
			args:  []string{"--every", "5s", "GH-1", "plan", "→", "ship", "--answer=1. yes"},
			tasks: "GH-1", chain: "plan ship", answer: "1. yes", every: 5 * time.Second,
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
			a.answer != c.answer || a.every != c.every {
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
