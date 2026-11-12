package asks_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	askscmd "github.com/amberpixels/f10/cli/internal/command/asks"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// fakeDeliver records what deliver did, with the drive live or not.
type fakeDeliver struct {
	live     bool
	prompted []string
	resumed  []int
}

func (f *fakeDeliver) deliverer() askscmd.Deliverer {
	return askscmd.Deliverer{
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
			if err := askscmd.AnswerAsk(context.Background(), &out, dir, "ft-1", []string{"1=b", "2=8080"}, asks.ByAsks,
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
			err := askscmd.AnswerAsk(context.Background(), &out, dir, "FT-1", nil, asks.BySkill, f.deliverer())
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
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/a", Project: "alpha"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/b", Project: "beta"})
	f := &fakeDeliver{}

	err := askscmd.AnswerAsk(context.Background(), io.Discard, dir, "FT-1", nil, asks.ByAsks, f.deliverer())
	if !strings.Contains(fmtErr(err), "alpha:FT-1 or beta:FT-1") {
		t.Errorf("ambiguous task: %v", err)
	}

	// question 2 has no default, so it must be answered
	err = askscmd.AnswerAsk(
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

	err = askscmd.AnswerAsk(
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
	if err := askscmd.WalkAsks(context.Background(), io.Discard, in, dir, f.deliverer()); err != nil {
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
	a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r"})

	if _, err := asks.Claim(dir, a.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := askscmd.ListAsks(t.Context(), &out, dir, time.Now(), true); err != nil {
		t.Fatal(err)
	}

	var got askscmd.AskList

	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Open) != 1 || got.Open[0].Task != "FT-2" || len(got.Open[0].Questions) != 2 ||
		len(got.Answered) != 1 || got.Answered[0].By != asks.ByPane {
		t.Errorf("list = %+v", got)
	}
}

// A session is told each open ask once, never the one its own agent asks.
func TestAsksContextOncePerSession(t *testing.T) {
	dir, stateDir := t.TempDir(), t.TempDir()
	testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Worktree: "/w1", Skill: "review"})
	testkit.Raised(t, dir, asks.Ask{Task: "FT-2", Root: "/r", Worktree: "/w2"})

	out := string(askscmd.AsksContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2"))
	if !strings.Contains(out, "FT-1 asks at review") || strings.Contains(out, "FT-2") ||
		!strings.Contains(out, `"hookEventName":"UserPromptSubmit"`) {
		t.Errorf("first prompt:\n%s", out)
	}

	if out := askscmd.AsksContext(dir, stateDir, "s1", "UserPromptSubmit", "/w2"); out != nil {
		t.Errorf("told twice:\n%s", out)
	}

	if out := string(askscmd.AsksContext(dir, stateDir, "s2", "SessionStart", "")); !strings.Contains(out, "FT-2") {
		t.Errorf("another session was not told:\n%s", out)
	}
}
