package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/layout"
)

// fakeDeliver records what deliver did, with the drive live or not.
type fakeDeliver struct {
	live     bool
	prompted []string
	resumed  []int
}

func (f *fakeDeliver) deliverer() deliverer {
	return deliverer{
		prompt: func(_ context.Context, a asks.Ask) error {
			f.prompted = append(f.prompted, a.Task+": "+a.Answer)

			return nil
		},
		resume: func(_ context.Context, _ io.Writer, a asks.Ask) error {
			f.resumed = append(f.resumed, a.Drive)

			return nil
		},
		live: func(asks.Ask) bool { return f.live },
	}
}

func raised(t *testing.T, dir string, a asks.Ask) asks.Ask {
	t.Helper()

	if a.Text == "" {
		a.Text = "1. Merge now? [yes | wait] 2. Which port?"
	}

	got, _, err := asks.Raise(dir, a, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestAnswerAskRoutes(t *testing.T) {
	cases := []struct {
		name     string
		drive    int
		live     bool
		prompted bool
		resumed  bool
	}{
		{name: "no drive: sent to the agent", prompted: true},
		{name: "stopped drive: resumed", drive: 4, resumed: true},
		{name: "running drive: left to it", drive: 4, live: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			a := raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Project: "p", Drive: c.drive})
			f := &fakeDeliver{live: c.live}

			var out bytes.Buffer
			if err := answerAsk(context.Background(), &out, dir, "ft-1", []string{"1=b", "2=8080"}, asks.ByAsks,
				f.deliverer()); err != nil {
				t.Fatal(err)
			}

			if (len(f.prompted) == 1) != c.prompted || (len(f.resumed) == 1) != c.resumed {
				t.Errorf("prompted %v, resumed %v\n%s", f.prompted, f.resumed, out.String())
			}

			got, isOpen, _ := asks.Get(dir, a.Key())
			if isOpen || got.Answer != "1. wait 2. 8080" || got.By != asks.ByAsks {
				t.Errorf("ask = %+v, open %v", got, isOpen)
			}

			if c.prompted && got.DeliveredAt.IsZero() {
				t.Error("an answer sent to the agent is not marked delivered")
			}

			// a second way in finds it answered
			err := answerAsk(context.Background(), &out, dir, "FT-1", nil, asks.BySkill, f.deliverer())
			if !strings.Contains(fmtErr(err), "already answered with f10 asks") {
				t.Errorf("second answer: %v", err)
			}
		})
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func TestAnswerAskRefuses(t *testing.T) {
	dir := t.TempDir()
	raised(t, dir, asks.Ask{Task: "FT-1", Root: "/a", Project: "alpha"})
	raised(t, dir, asks.Ask{Task: "FT-1", Root: "/b", Project: "beta"})
	f := &fakeDeliver{}

	err := answerAsk(context.Background(), io.Discard, dir, "FT-1", nil, asks.ByAsks, f.deliverer())
	if !strings.Contains(fmtErr(err), "alpha:FT-1 or beta:FT-1") {
		t.Errorf("ambiguous task: %v", err)
	}

	// question 2 has no default, so it must be answered
	err = answerAsk(context.Background(), io.Discard, dir, "alpha:FT-1", []string{"1=yes"}, asks.ByAsks, f.deliverer())
	if !strings.Contains(fmtErr(err), "question 2") {
		t.Errorf("missing free answer: %v", err)
	}

	err = answerAsk(context.Background(), io.Discard, dir, "alpha:FT-1", []string{"1=maybe", "2=80"}, asks.ByAsks,
		f.deliverer())
	if !strings.Contains(fmtErr(err), "takes a) yes, b) wait") {
		t.Errorf("bad option: %v", err)
	}

	if open, _ := asks.Open(dir); len(open) != 2 {
		t.Errorf("a refused answer closed an ask: %+v", open)
	}
}

func TestWalkAsks(t *testing.T) {
	dir := t.TempDir()
	raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Text: "1. Merge? [yes | wait]"})
	raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Text: "1. Port?"})
	f := &fakeDeliver{}

	// Enter takes FT-1's default; FT-2 is skipped
	in := bufio.NewReader(strings.NewReader("\ns\n"))
	if err := walkAsks(context.Background(), io.Discard, in, dir, f.deliverer()); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(f.prompted, []string{"FT-1: 1. yes"}) {
		t.Errorf("prompted %v", f.prompted)
	}

	if open, _ := asks.Open(dir); len(open) != 1 || open[0].Task != "FT-2" {
		t.Errorf("open = %+v", open)
	}
}

func TestListAsksJSON(t *testing.T) {
	dir := t.TempDir()
	a := raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r"})
	raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r"})

	if _, err := asks.Claim(dir, a.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := listAsks(t.Context(), &out, dir, time.Now(), true); err != nil {
		t.Fatal(err)
	}

	var got askList

	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Open) != 1 || got.Open[0].Task != "FT-2" || len(got.Open[0].Questions) != 2 ||
		len(got.Answered) != 1 || got.Answered[0].By != asks.ByPane {
		t.Errorf("list = %+v", got)
	}
}

func TestNotifyArgv(t *testing.T) {
	a := asks.Ask{Task: "FT-1", URL: "https://x/1", Questions: asks.Parse("1. It's ready? [yes | no]")}
	none := func(string) (string, error) { return "", errors.New("absent") }
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }

	if got, _ := notifyArgv("off", a, "darwin", found); got != nil {
		t.Errorf("off = %q", got)
	}

	if got, _ := notifyArgv("", a, "darwin", none); len(got) != 3 || got[0] != "osascript" {
		t.Errorf("darwin = %q", got)
	}

	if got, _ := notifyArgv("", a, "linux", found); len(got) != 3 || got[0] != "notify-send" {
		t.Errorf("linux = %q", got)
	}

	if got, _ := notifyArgv("", a, "linux", none); got != nil {
		t.Errorf("linux without notify-send = %q", got)
	}
}

// An agent's question reaches the notify command as text, never as shell,
// whether the template quotes the placeholder or not.
func TestNotifyTemplateRunsNoQuestion(t *testing.T) {
	question := "Ship it? `touch pwned1` $(touch pwned2) 'quoted' \"double\""
	a := asks.Ask{Task: "FT-1", URL: "https://x/1", Questions: []asks.Question{{N: 1, Text: question}}}

	for _, tmpl := range []string{
		`printf '%s\n' "{task} asks: {question}" {url}`,
		`printf '%s\n' {question}`,
	} {
		dir := t.TempDir()
		argv, env := notifyArgv(tmpl, a, "linux", nil)

		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}

		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("%s ran the question: %v", tmpl, left)
		}

		if !strings.Contains(string(out), "`touch") || !strings.Contains(string(out), "$(touch") {
			t.Errorf("%s printed %q", tmpl, out)
		}
	}
}

// A prompt typed in the asking agent's checkout answers its ask; one typed
// in another checkout does not.
func TestPaneAnswered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("F10_ASKS_DIR", dir)

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	root := layout.Locate(context.Background(), repo).CheckoutRoot
	a := raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: root})
	raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Worktree: root, Skill: "finish", Text: "1. x? [local | merge]"})

	paneAnswered(context.Background(), t.TempDir(), "elsewhere")

	if open, _ := asks.Open(dir); len(open) != 2 {
		t.Fatalf("a prompt in another checkout answered: %+v", open)
	}

	paneAnswered(context.Background(), repo, "1. wait")

	got, isOpen, _ := asks.Get(dir, a.Key())
	if isOpen || got.By != asks.ByPane || got.Answer != "1. wait" || got.DeliveredAt.IsZero() || got.TakenAt.IsZero() {
		t.Errorf("ask = %+v, open %v", got, isOpen)
	}

	// finish's question is the drive's: a prompt in the pane leaves it open
	if open, _ := asks.Open(dir); len(open) != 1 || open[0].Task != "FT-2" {
		t.Errorf("open = %+v", open)
	}
}

// A session is told each open ask once, never the one its own agent asks.
func TestAsksContextOncePerSession(t *testing.T) {
	dir, stateDir := t.TempDir(), t.TempDir()
	raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: "/w1", Skill: "review"})
	raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Worktree: "/w2"})

	out := string(asksContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2"))
	if !strings.Contains(out, "FT-1 asks at review") || strings.Contains(out, "FT-2") ||
		!strings.Contains(out, `"hookEventName":"UserPromptSubmit"`) {
		t.Errorf("first prompt:\n%s", out)
	}

	if out := asksContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2"); out != nil {
		t.Errorf("told twice:\n%s", out)
	}

	if out := string(asksContext(dir, stateDir, "s2", "SessionStart", "")); !strings.Contains(out, "FT-2") {
		t.Errorf("another session was not told:\n%s", out)
	}
}
