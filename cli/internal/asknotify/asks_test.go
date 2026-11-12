package asknotify_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestNotifyArgv(t *testing.T) {
	a := asks.Ask{Task: "FT-1", URL: "https://x/1", Questions: asks.Parse("1. It's ready? [yes | no]")}
	none := func(string) (string, error) { return "", errors.New("absent") }
	found := func(string) (string, error) { return "/usr/bin/notify-send", nil }

	if got, _ := asknotify.NotifyArgv("off", a, "darwin", found); got != nil {
		t.Errorf("off = %q", got)
	}

	if got, _ := asknotify.NotifyArgv("", a, "darwin", none); len(got) != 3 || got[0] != "osascript" {
		t.Errorf("darwin = %q", got)
	}

	if got, _ := asknotify.NotifyArgv("", a, "linux", found); len(got) != 3 || got[0] != "notify-send" {
		t.Errorf("linux = %q", got)
	}

	if got, _ := asknotify.NotifyArgv("", a, "linux", none); got != nil {
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
		argv, env := asknotify.NotifyArgv(tmpl, a, "linux", nil)

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
	testkit.Git(t, repo, "init", "-q")

	root := layout.Locate(context.Background(), repo).CheckoutRoot
	a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: root})
	testkit.Raised(
		t,
		dir,
		asks.Ask{Task: "FT-2", Root: "/r", Worktree: root, Skill: "finish", Text: "1. x? [local | merge]"},
	)

	asknotify.PaneAnswered(context.Background(), t.TempDir(), "elsewhere")

	if open, _ := asks.Open(dir); len(open) != 2 {
		t.Fatalf("a prompt in another checkout answered: %+v", open)
	}

	asknotify.PaneAnswered(context.Background(), repo, "1. wait")

	got, isOpen, _ := asks.Get(dir, a.Key())
	if isOpen || got.By != asks.ByPane || got.Answer != "1. wait" || got.DeliveredAt.IsZero() || got.TakenAt.IsZero() {
		t.Errorf("ask = %+v, open %v", got, isOpen)
	}

	// finish's question is the drive's: a prompt in the pane leaves it open
	if open, _ := asks.Open(dir); len(open) != 1 || open[0].Task != "FT-2" {
		t.Errorf("open = %+v", open)
	}
}
