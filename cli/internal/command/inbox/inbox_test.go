package inbox_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/asks"
	inboxcmd "github.com/amberpixels/f10/cli/internal/command/inbox"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// fakeDeliver records what deliver did, with the drive live or not.
type fakeDeliver struct {
	live     bool
	prompted []string
	resumed  []int
}

func (f *fakeDeliver) deliverer() inboxcmd.Deliverer {
	return inboxcmd.Deliverer{
		Prompt: func(_ context.Context, _ io.Writer, a asks.Ask) error {
			f.prompted = append(f.prompted, a.Task+": "+a.Answer)

			return nil
		},
		Resume: func(_ context.Context, _ io.Writer, a asks.Ask) error {
			f.resumed = append(f.resumed, a.Drive)

			return nil
		},
		Live: func(asks.Ask) bool { return f.live },
	}
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
			a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Project: "p", Drive: c.drive})
			f := &fakeDeliver{live: c.live}

			var out bytes.Buffer
			if err := inboxcmd.AnswerAsk(
				context.Background(),
				&out,
				dir,
				"ft-1",
				[]string{"1=b", "2=8080"},
				asks.ByAsks,
				f.deliverer(),
			); err != nil {
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
			err := inboxcmd.AnswerAsk(context.Background(), &out, dir, "FT-1", nil, asks.BySkill, f.deliverer())
			if !strings.Contains(fmtErr(err), "already answered with f10 inbox") {
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
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/a", Project: "alpha"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/b", Project: "beta"})
	f := &fakeDeliver{}

	err := inboxcmd.AnswerAsk(context.Background(), io.Discard, dir, "FT-1", nil, asks.ByAsks, f.deliverer())
	if !strings.Contains(fmtErr(err), "alpha:FT-1 or beta:FT-1") {
		t.Errorf("ambiguous task: %v", err)
	}

	// question 2 has no default, so it must be answered
	err = inboxcmd.AnswerAsk(
		context.Background(),
		io.Discard,
		dir,
		"alpha:FT-1",
		[]string{"1=yes"},
		asks.ByAsks,
		f.deliverer(),
	)
	if !strings.Contains(fmtErr(err), "question 2") {
		t.Errorf("missing free answer: %v", err)
	}

	err = inboxcmd.AnswerAsk(
		context.Background(),
		io.Discard,
		dir,
		"alpha:FT-1",
		[]string{"1=maybe", "2=80"},
		asks.ByAsks,
		f.deliverer(),
	)
	if !strings.Contains(fmtErr(err), "takes a) yes, b) wait") {
		t.Errorf("bad option: %v", err)
	}

	if open, _ := asks.Open(dir); len(open) != 2 {
		t.Errorf("a refused answer closed an ask: %+v", open)
	}
}

func TestWalkAsks(t *testing.T) {
	dir := t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Text: "1. Merge? [yes | wait]"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Text: "1. Port?"})
	f := &fakeDeliver{}

	// Enter takes FT-1's default; FT-2 is skipped
	in := bufio.NewReader(strings.NewReader("\ns\n"))
	if err := inboxcmd.WalkAsks(context.Background(), io.Discard, in, dir, f.deliverer()); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(f.prompted, []string{"FT-1: 1. yes"}) {
		t.Errorf("prompted %v", f.prompted)
	}

	if open, _ := asks.Open(dir); len(open) != 1 || open[0].Task != "FT-2" {
		t.Errorf("open = %+v", open)
	}
}

// The walk sends the option picked, never another one whose text a letter
// or a number would name: options 2 and 1, picked by b, record 1.
func TestWalkAsksKeepsANumericOption(t *testing.T) {
	dir := t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Text: "1. Port? [2 | 1]"})
	f := &fakeDeliver{}

	in := bufio.NewReader(strings.NewReader("b\n"))
	if err := inboxcmd.WalkAsks(context.Background(), io.Discard, in, dir, f.deliverer()); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(f.prompted, []string{"FT-1: 1. 1"}) {
		t.Errorf("prompted %v, want option b, 1", f.prompted)
	}
}

// A list for one project leaves another project's asks out, open or
// answered. An empty project lists them all.
func TestListAsksKeepsToTheProject(t *testing.T) {
	dir := t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/a", Project: "a"})
	b := testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/b", Project: "b"})

	if _, err := asks.Claim(dir, b.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	read := func(project string) inboxcmd.AskList {
		var out bytes.Buffer
		if err := inboxcmd.ListAsks(t.Context(), &out, dir, project, time.Now(), true); err != nil {
			t.Fatal(err)
		}

		var got inboxcmd.AskList
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		return got
	}

	if got := read("/a"); len(got.Open) != 1 || got.Open[0].Task != "FT-1" || len(got.Answered) != 0 {
		t.Errorf("project a = %+v", got)
	}

	if got := read(""); len(got.Open) != 1 || len(got.Answered) != 1 {
		t.Errorf("every project = %+v", got)
	}
}

func TestListAsksJSON(t *testing.T) {
	dir := t.TempDir()
	a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r"})

	if _, err := asks.Claim(dir, a.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := inboxcmd.ListAsks(t.Context(), &out, dir, "", time.Now(), true); err != nil {
		t.Fatal(err)
	}

	var got inboxcmd.AskList

	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Open) != 1 || got.Open[0].Task != "FT-2" || len(got.Open[0].Questions) != 2 ||
		len(got.Answered) != 1 || got.Answered[0].By != asks.ByPane {
		t.Errorf("list = %+v", got)
	}
}

// A session is told each open ask once, never the one its own agent asks.
func TestInboxContextOncePerSession(t *testing.T) {
	dir, stateDir := t.TempDir(), t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: "/w1", Skill: "review"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Worktree: "/w2"})

	out := string(inboxcmd.InboxContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2", "/r"))
	if !strings.Contains(out, "ASK · FT-1 · at review") || strings.Contains(out, "FT-2") ||
		!strings.Contains(out, `"hookEventName":"UserPromptSubmit"`) {
		t.Errorf("first prompt:\n%s", out)
	}

	if out := inboxcmd.InboxContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2", "/r"); out != nil {
		t.Errorf("told twice:\n%s", out)
	}

	if out := string(
		inboxcmd.InboxContext(dir, stateDir, "s2", "SessionStart", "", ""),
	); !strings.Contains(
		out,
		"FT-2",
	) {
		t.Errorf("another session was not told:\n%s", out)
	}
}

// A session in one project is never told another project's asks. One
// outside any project is told every project's, each named.
func TestInboxContextKeepsToTheProject(t *testing.T) {
	dir := t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/a", Project: "a", Worktree: "/a1"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/b", Project: "b", Worktree: "/b1"})

	out := string(inboxcmd.InboxContext(dir, t.TempDir(), "s1", "UserPromptSubmit", "/a0", "/a"))
	if !strings.Contains(out, "FT-1") || strings.Contains(out, "FT-2") {
		t.Errorf("in project a:\n%s", out)
	}

	out = string(inboxcmd.InboxContext(dir, t.TempDir(), "s1", "UserPromptSubmit", "", ""))
	if !strings.Contains(out, "ASK · a:FT-1") || !strings.Contains(out, "ASK · b:FT-2") {
		t.Errorf("outside any project:\n%s", out)
	}
}

// SessionStart records the session's project for the status line, and
// drops the record outside any project.
func TestWriteProject(t *testing.T) {
	stateDir := t.TempDir()
	path := state.ProjectPath(stateDir, "s1")

	inboxcmd.WriteProject(stateDir, "s1", "/a")

	if raw, _ := os.ReadFile(path); strings.TrimSpace(string(raw)) != asks.ProjectHash("/a") {
		t.Errorf("project file = %q, want %s", raw, asks.ProjectHash("/a"))
	}

	inboxcmd.WriteProject(stateDir, "s1", "")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("project file outside a project: %v", err)
	}
}

// A driven agent's hook tells it nothing on any event, including the
// prompt that carries its own answer. The same hook undriven tells the ask.
func TestInboxHookSkipsADrivenAgent(t *testing.T) {
	dir, stateDir := t.TempDir(), t.TempDir()
	t.Setenv("F10_INBOX_DIR", dir)
	t.Setenv("F10_STATE_DIR", stateDir)
	// a live worktree, so the session start's prune keeps the ask
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: t.TempDir()})

	hook := func(event, cwd string) string {
		var out bytes.Buffer

		cmd := inboxcmd.Command()
		cmd.Writer = &out
		cmd.Reader = strings.NewReader(`{"hook_event_name":"` + event + `","session_id":"s1","cwd":"` + cwd + `"}`)

		if err := cmd.Run(t.Context(), []string{"inbox", "hook"}); err != nil {
			t.Fatal(err)
		}

		return out.String()
	}

	t.Setenv(launch.DrivenEnv, "1")

	repo := testkit.ScopeRepo(t, t.TempDir(), "app")

	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if out := hook(event, repo); out != "" {
			t.Errorf("%s told a driven agent:\n%s", event, out)
		}
	}

	// its status line still counts its own project's asks alone
	if _, err := os.Stat(state.ProjectPath(stateDir, "s1")); err != nil {
		t.Errorf("a driven session start wrote no project record: %v", err)
	}

	t.Setenv(launch.DrivenEnv, "")

	if out := hook("UserPromptSubmit", "/"); !strings.Contains(out, "FT-1") {
		t.Errorf("an undriven session was not told:\n%s", out)
	}
}

// The old asks noun runs the same verbs, hidden, so an installed plugin's
// f10 asks hook keeps working until the plugin updates.
func TestAsksAliasMatchesInbox(t *testing.T) {
	inbox, alias := inboxcmd.Command(), inboxcmd.AsksAlias()
	if inbox.Name != "inbox" || inbox.Hidden {
		t.Fatalf("inbox = %q, hidden %v", inbox.Name, inbox.Hidden)
	}

	if alias.Name != "asks" || !alias.Hidden {
		t.Fatalf("alias = %q, hidden %v", alias.Name, alias.Hidden)
	}

	verbs := func(cmds []*cli.Command) []string {
		out := make([]string, 0, len(cmds))
		for _, c := range cmds {
			out = append(out, c.Name)
		}

		return out
	}

	if got, want := verbs(
		alias.Commands,
	), verbs(
		inbox.Commands,
	); !slices.Equal(got, want) ||
		!slices.Contains(got, "hook") {
		t.Errorf("alias verbs %v, inbox verbs %v", got, want)
	}
}
