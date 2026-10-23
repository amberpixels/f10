package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

// tracedRepo is a checkout on the given branch, its project.md declaring
// DM task ids and the traces switch as asked ("" leaves it undeclared).
func tracedRepo(t *testing.T, branch, traces string) string {
	t.Helper()
	isolateEnv(t)
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	repo := mkRepo(t)
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "checkout", "-q", "-b", branch)

	block := "---\ntracker:\n  id: DM-###\n"
	if traces != "" {
		block += "traces: " + traces + "\n"
	}

	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(block+"---\n# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return repo
}

func runTrace(t *testing.T, repo string, args ...string) (string, error) {
	t.Helper()

	out, _, err := runTraceVerb(t, repo, "trailers", args...)

	return out, err
}

func runTraceVerb(t *testing.T, repo, verb string, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Writer = &out
	app.ErrWriter = &errOut
	err := app.Run(context.Background(), append([]string{"f10", "-C", repo, "trace", verb}, args...))

	return out.String(), errOut.String(), err
}

func TestTraceTrailers(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		traces string
		args   []string
		want   string
	}{
		{"off prints nothing", "DM-7/thing", "off", []string{"--step", "implement"}, ""},
		{"undeclared is off", "DM-7/thing", "", []string{"--step", "implement"}, ""},
		{"task from the branch", "DM-7/thing", "on", []string{"--step", "implement"}, "Task: DM-7\nStep: implement\n"},
		{
			"a bundle, one line each",
			"DM-7+DM-8/thing",
			"on",
			[]string{"--step", "implement"},
			"Task: DM-7\nTask: DM-8\nStep: implement\n",
		},
		{
			"explicit task wins",
			"DM-7/thing",
			"on",
			[]string{"--step", "judge", "--task", "DM-9"},
			"Task: DM-9\nStep: judge\n",
		},
		{
			"review counts", "DM-7/thing", "on",
			[]string{"--step", "resolve", "--round", "2", "--fixed", "1", "--skipped", "3"},
			"Task: DM-7\nStep: resolve\nReview: round=2 fixed=1 skipped=3\n",
		},
		{"no task on the branch", "main", "on", []string{"--step", "implement"}, "Step: implement\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := tracedRepo(t, c.branch, c.traces)

			got, err := runTrace(t, repo, c.args...)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("printed %q, want %q", got, c.want)
			}
		})
	}
}

// A session that recorded a task does not lend it to a branch naming none:
// the trailer is the branch's task or nothing.
func TestTraceTrailersIgnoreTheSessionTask(t *testing.T) {
	repo := tracedRepo(t, "main", "on")
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	run := &state.Run{Session: "sess-1", Task: "DM-5"}
	if err := run.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := runTrace(t, repo, "--step", "implement")
	if err != nil || got != "Step: implement\n" {
		t.Errorf("printed %q, %v; want Step: alone", got, err)
	}
}

func TestTraceTrailersRefuses(t *testing.T) {
	repo := tracedRepo(t, "DM-7/thing", "on")

	if _, err := runTrace(t, repo, "--step", "resolve", "--fixed", "1"); err == nil ||
		!strings.Contains(err.Error(), "go together") {
		t.Errorf("a lone count = %v", err)
	}

	if _, err := runTrace(t, repo, "--step", "Two Words"); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("a bad step = %v", err)
	}

	if _, err := runTrace(t, repo); err == nil {
		t.Error("no --step accepted")
	}
}

// A ghFake answers the host CLI while git stays real: the state verb reads
// the branch and its config from the checkout and the PR from gh.
type ghFake struct {
	calls  []string
	edited string // the body the last `gh pr edit --body-file` carried
}

const stateView = "gh pr view %s --json state,url,number,title,body,baseRefName,mergeStateStatus,mergeCommit,author"

// fakeGH scripts gh: the PR view for branch, the signed-in login, and an
// edit that records the body it was given.
func fakeGH(t *testing.T, branch, view string) *ghFake {
	t.Helper()

	f := &ghFake{}
	prev := shell.Capture

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		key := filepath.Base(name) + " " + strings.Join(args, " ")
		f.calls = append(f.calls, key)

		switch {
		case key == strings.ReplaceAll(stateView, "%s", branch):
			return shell.Result{Stdout: view}, nil
		case key == "gh api user":
			return shell.Result{Stdout: `{"login":"Me"}`}, nil
		case strings.HasPrefix(key, "gh pr edit "+branch+" --body-file "):
			data, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				t.Fatal(err)
			}

			f.edited = string(data)

			return shell.Result{}, nil
		}

		t.Fatalf("unscripted call: %q", key)

		return shell.Result{}, nil
	}

	t.Cleanup(func() { shell.Capture = prev })

	return f
}

func openPR(body string) string {
	b, _ := json.Marshal(body)

	return `{"state":"OPEN","url":"https://gh/pr/9","number":9,"body":` + string(b) + `,"author":{"login":"me"}}`
}

func TestTraceState(t *testing.T) {
	const branch = "DM-7/thing"

	withBlock := "Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"review\",\"round\":1,\"open\":2} -->\n"

	cases := []struct {
		name   string
		branch string
		after  string
		view   string
		args   []string
		want   string // the edited body; "" means no edit
	}{
		{
			"appended with the task and its base", branch, "DM-6", openPR("Adds the thing.\n"),
			[]string{"--step", "pr"},
			"Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"pr\",\"after\":\"DM-6\"} -->\n",
		},
		{
			"counts carry when only the step is passed", branch, "", openPR(withBlock),
			[]string{"--step", "judge"},
			"Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"judge\",\"round\":1,\"open\":2} -->\n",
		},
		{
			"an explicit zero clears", branch, "", openPR(withBlock),
			[]string{"--step", "resolve", "--open", "0", "--asks", "0"},
			"Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"resolve\",\"round\":1} -->\n",
		},
		{
			"a bundle writes its riders", "DM-7+DM-8/thing", "", openPR("Adds both.\n"),
			[]string{"--step", "pr"},
			"Adds both.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"riders\":[\"DM-8\"],\"step\":\"pr\"} -->\n",
		},
		{
			"a branch naming no task writes none", "main", "", openPR("A planless change.\n"),
			[]string{"--step", "pr"},
			"A planless change.\n\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\n",
		},
		{
			"an unchanged body is not edited", branch, "", openPR(withBlock),
			[]string{"--step", "review", "--round", "1", "--open", "2"},
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := tracedRepo(t, c.branch, "on")
			if c.after != "" {
				git(t, repo, "config", "branch."+c.branch+".f10-after", c.after)
			}

			gh := fakeGH(t, c.branch, c.view)

			if _, _, err := runTraceVerb(t, repo, "state", c.args...); err != nil {
				t.Fatal(err)
			}

			if gh.edited != c.want {
				t.Errorf("edited body %q, want %q", gh.edited, c.want)
			}
		})
	}
}

func TestTraceStateOffMakesNoHostCall(t *testing.T) {
	repo := tracedRepo(t, "DM-7/thing", "off")
	gh := fakeGH(t, "DM-7/thing", openPR("Adds the thing.\n"))

	if _, _, err := runTraceVerb(t, repo, "state", "--step", "pr"); err != nil {
		t.Fatal(err)
	}

	if len(gh.calls) != 0 {
		t.Errorf("called the host: %v", gh.calls)
	}
}

func TestTraceStateDeclines(t *testing.T) {
	const branch = "DM-7/thing"

	t.Run("a merged PR is refused", func(t *testing.T) {
		repo := tracedRepo(t, branch, "on")
		fakeGH(t, branch, `{"state":"MERGED","url":"https://gh/pr/9","body":"x","author":{"login":"me"}}`)

		_, _, err := runTraceVerb(t, repo, "state", "--step", "pr")
		if err == nil || !strings.Contains(err.Error(), "is merged") {
			t.Errorf("err = %v, want the PR's state", err)
		}
	})

	t.Run("another author's PR is left alone", func(t *testing.T) {
		repo := tracedRepo(t, branch, "on")
		gh := fakeGH(t, branch, `{"state":"OPEN","url":"https://gh/pr/9","body":"x","author":{"login":"someone"}}`)

		_, errOut, err := runTraceVerb(t, repo, "state", "--step", "pr")
		if err != nil || gh.edited != "" || !strings.Contains(errOut, "opened by someone") {
			t.Errorf("err %v, edited %q, stderr %q", err, gh.edited, errOut)
		}
	})
}

// --body-file puts the block into the draft, its counts carried from the
// PR's current block, and leaves the PR itself unedited.
func TestTraceStateBodyFile(t *testing.T) {
	const branch = "DM-7/thing"

	repo := tracedRepo(t, branch, "on")
	host := "Old description.\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"judge\",\"round\":1,\"asks\":1} -->\n"
	gh := fakeGH(t, branch, openPR(host))

	draft := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(draft, []byte("New description.\n\nCloses #7\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runTraceVerb(t, repo, "state", "--step", "pr", "--body-file", draft); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(draft)
	want := "New description.\n\nCloses #7\n\n<!-- state {\"v\":1,\"task\":\"DM-7\",\"step\":\"pr\",\"round\":1,\"asks\":1} -->\n"

	if string(got) != want {
		t.Errorf("draft %q, want %q", got, want)
	}

	if gh.edited != "" {
		t.Errorf("the PR was edited: %q", gh.edited)
	}
}
