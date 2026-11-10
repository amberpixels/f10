package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
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

	launch := "herdr agent start " + agentOf("GH-1") + " --kind claude --pane pane:7 -- " +
		prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, "") + " --effort high"
	f.script(launch, "{}")

	in := startInput{
		main:           "/code/repo",
		storage:        testStorage,
		task:           gh1,
		profile:        agentctl.Profile{Effort: "high"},
		profileFlagged: true,
	}
	if err := start(t.Context(), &bytes.Buffer{}, in); err != nil {
		t.Fatal(err)
	}

	if !f.called(launch) {
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

	text := prompt(modeDiscuss, shipFlow{}, "GH-1", nil, "", nil, "first\nsecond")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+text, "{}")

	in := startInput{main: "/code/repo", storage: testStorage, task: gh1, mode: modeDiscuss, note: "first\nsecond"}
	if err := start(t.Context(), &bytes.Buffer{}, in); err != nil {
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
	fx.task("GH-1", taskInfo{body: "first"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	want := "herdr agent start " + agentOf("GH-1") + " --kind claude --pane pane:1 -- " +
		skillPrompt("judge", "GH-1", nil, shipFlow{}, false)
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
		if got := holdsPrompt(c.screen, sentText); got != c.want {
			t.Errorf("%s: holdsPrompt = %v, want %v", name, got, c.want)
		}
	}

	if !holdsPrompt(screenWith("❯ [Pasted text #1 +2 lines]"), "first\nsecond") {
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

		note, err := promptChecked(t.Context(), fwdWT, "GH-1", "", sentText)
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

	prev := enterRetry
	enterRetry = 10 * time.Second

	t.Cleanup(func() { enterRetry = prev })

	now := time.Now()
	in := &driveInput{
		driveDeps: driveDeps{
			now: func() time.Time { return now },
		},
	}

	typed := &driveRun{task: driveTask{ref: gh1}, skill: "ship", sentAt: now.Add(-11 * time.Second), typed: sentText}
	typed.o.path = fwdWT

	launched := &driveRun{task: driveTask{ref: gh1}, skill: "ship", sentAt: now.Add(-11 * time.Second)}
	recent := &driveRun{task: driveTask{ref: gh1}, skill: "ship", sentAt: now.Add(-time.Second), typed: sentText}

	var out bytes.Buffer

	for range 2 {
		in.retryEnter(t.Context(), &out, typed)
		in.retryEnter(t.Context(), &out, launched)
		in.retryEnter(t.Context(), &out, recent)
	}

	if len(m.enters) != 1 || !bytes.Contains(out.Bytes(), []byte("Enter resent")) {
		t.Errorf("%d Enters, output:\n%s", len(m.enters), out.String())
	}
}
