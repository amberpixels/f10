package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/start"
)

// A fresh start hands the agent its first prompt on its command line, ahead
// of the profile's flags, and never types it into the pane.
func TestStartLaunchesWithTheFirstPrompt(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)

	launched := "herdr agent start " + agentOf("GH-1") + " --kind claude --pane pane:7 -- " +
		launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", nil, "") + " --effort high"
	f.script(launched, "{}")

	in := start.StartInput{
		Main:           "/code/repo",
		Storage:        testStorage,
		Task:           gh1,
		Profile:        agentctl.Profile{Effort: "high"},
		ProfileFlagged: true,
	}
	if err := start.Start(t.Context(), &bytes.Buffer{}, in); err != nil {
		t.Fatal(err)
	}

	if !f.called(launched) {
		t.Errorf("the agent was not launched with its prompt: %q", f.calls)
	}

	if f.called("herdr agent prompt") || f.called("herdr agent read") {
		t.Errorf("the first prompt was typed: %q", f.calls)
	}
}

// A first prompt herdr refuses as an argument, one with a newline, is typed
// once the agent starts, and its input box is checked after.
func TestStartTypesAPromptWithANewline(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	text := launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", nil, "", nil, "first\nsecond")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+text, "{}")

	in := start.StartInput{
		Main:    "/code/repo",
		Storage: testStorage,
		Task:    gh1,
		Mode:    launch.ModeDiscuss,
		Note:    "first\nsecond",
	}
	if err := start.Start(t.Context(), &bytes.Buffer{}, in); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt") || !f.called("herdr agent read "+agentOf("GH-1")) {
		t.Errorf("the prompt was not typed and checked: %q", f.calls)
	}
}

// A drive's fresh task gets its first skill on the agent's command line,
// never typed into the pane.
func TestDriveLaunchesWithTheFirstSkill(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	want := "herdr agent start " + agentOf("GH-1") + " --kind claude --pane pane:1 -- " +
		drive.SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false)
	if len(fx.f.launches) != 1 || fx.f.launches[0] != want {
		t.Errorf("launches = %q, want %q", fx.f.launches, want)
	}
}

const (
	boxRule  = "────────────────────────────────────────"
	sentText = "/f10:ship GH-1 resume at pr (entry 4) --driven"
)

func screenWith(box string) string {
	return "⏺ earlier output\n❯ /f10:plan GH-1 --driven\n\n" + boxRule + "\n" + box + "\n" + boxRule + "\n  status line\n"
}

func TestHoldsPrompt(t *testing.T) {
	cases := map[string]struct {
		screen string
		want   bool
	}{
		"the prompt in the box":    {screenWith("❯ " + sentText), true},
		"the prompt wrapped":       {screenWith("❯ /f10:ship GH-1 resume\n  at pr (entry 4) --driven"), true},
		"an empty box":             {screenWith("❯ "), false},
		"the user's own text":      {screenWith("❯ what about the tests"), false},
		"the user's own paste":     {screenWith("❯ [Pasted text #1 +12 lines]"), false},
		"no box on screen":         {"❯ " + sentText + "\n", false},
		"the prompt above the box": {"❯ " + sentText + "\n" + boxRule + "\n❯ \n" + boxRule + "\n", false},
	}

	for name, c := range cases {
		if got := launch.HoldsPrompt(c.screen, sentText); got != c.want {
			t.Errorf("%s: holdsPrompt = %v, want %v", name, got, c.want)
		}
	}

	if !launch.HoldsPrompt(screenWith("❯ [Pasted text #1 +2 lines]"), "first\nsecond") {
		t.Error("a multi-line prompt pasted as a block reads as submitted")
	}
}

// A typed prompt still in the input box gets Enter once; one that went
// through gets none.
func TestPromptCheckedResendsEnterOnce(t *testing.T) {
	for name, c := range map[string]struct {
		screens []string
		enters  int
		note    bool
	}{
		"held":      {[]string{screenWith("❯ " + sentText)}, 1, true},
		"submitted": {[]string{screenWith("❯ ")}, 0, false},
	} {
		m := useAgents(t)
		m.started = []string{agentOf("GH-1")}
		m.screens = c.screens

		note, err := launch.PromptChecked(t.Context(), fwdWT, "GH-1", "", sentText)
		if err != nil {
			t.Fatal(err)
		}

		if len(m.enters) != c.enters || (note != "") != c.note {
			t.Errorf("%s: %d Enters, note %q", name, len(m.enters), note)
		}
	}
}

// The drive checks a typed send once, enterRetry after it, and never one
// that went on the agent's command line.
func TestDriveRetriesEnterOnceForATypedSend(t *testing.T) {
	m := useAgents(t)
	m.screens = []string{screenWith("❯ " + sentText)}

	prev := launch.EnterRetry
	launch.EnterRetry = 10 * time.Second

	t.Cleanup(func() { launch.EnterRetry = prev })

	now := time.Now()
	in := &drive.DriveInput{
		Now: func() time.Time { return now },
	}

	typed := &drive.DriveRun{
		Task:   drive.DriveTask{Ref: gh1},
		Skill:  "ship",
		SentAt: now.Add(-11 * time.Second),
		Typed:  sentText,
	}
	typed.O.Path = fwdWT

	launched := &drive.DriveRun{Task: drive.DriveTask{Ref: gh1}, Skill: "ship", SentAt: now.Add(-11 * time.Second)}
	recent := &drive.DriveRun{
		Task:   drive.DriveTask{Ref: gh1},
		Skill:  "ship",
		SentAt: now.Add(-time.Second),
		Typed:  sentText,
	}

	var out bytes.Buffer

	in.Out = &out

	for range 2 {
		in.RetryEnter(t.Context(), typed)
		in.RetryEnter(t.Context(), launched)
		in.RetryEnter(t.Context(), recent)
	}

	if len(m.enters) != 1 || !bytes.Contains(out.Bytes(), []byte("Enter resent")) {
		t.Errorf("%d Enters, output:\n%s", len(m.enters), out.String())
	}
}
