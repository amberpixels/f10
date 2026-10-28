package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

// Payloads under testdata/payloads keep the shape Claude Code sends, recorded
// with F10_STATE_DEBUG and renamed to a made-up project. The Bash ones carry
// real git output.
func payload(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "payloads", name+".json"))
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

var fixedNow = time.Unix(1_800_000_000, 0)

func testEnv(t *testing.T) *Env {
	t.Helper()
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	return &Env{
		StateDir: t.TempDir(),
		Home:     t.TempDir(),
		Now:      func() time.Time { return fixedNow },
	}
}

// normalized is a state file as the goldens hold it: the time masked, and the
// skill key, which the bash dispatcher that recorded them did not write,
// dropped.
func normalized(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "<none>"
	}

	if err != nil {
		t.Fatal(err)
	}

	var keep []string

	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "skill "):
		case strings.HasPrefix(line, "updated "):
			keep = append(keep, "updated *")
		default:
			keep = append(keep, line)
		}
	}

	return strings.Join(keep, "\n")
}

// Every scenario under testdata/scenarios is a starting state file, payloads
// in order, and the state file the bash dispatcher left for them, recorded
// before it was retired. The port must leave the same file.
func TestScenariosMatchTheBashDispatcher(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "scenarios", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}

	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			env := testEnv(t)
			path := state.Path(env.StateDir, "s1")

			if start, err := os.ReadFile(filepath.Join(dir, "start.state")); err == nil {
				if err := os.WriteFile(path, start, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			steps, err := os.ReadFile(filepath.Join(dir, "steps"))
			if err != nil {
				t.Fatal(err)
			}

			for name := range strings.FieldsSeq(string(steps)) {
				if out := Dispatch(context.Background(), payload(t, name), env); out != nil {
					t.Errorf("%s printed %s", name, out)
				}
			}

			if got, want := normalized(t, path), normalized(t, filepath.Join(dir, "want.state")); got != want {
				t.Errorf("state file\n got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestSeedRecordsTheSkill(t *testing.T) {
	env := testEnv(t)
	Dispatch(context.Background(), payload(t, "ue-ship-id"), env)

	r, err := state.Load(state.Path(env.StateDir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	if r.Skill != "ship" {
		t.Errorf("skill = %q, want ship", r.Skill)
	}
}

// debug runs before any task exists: it seeds capture, and an id-shaped
// token in the error text names no task.
func TestSeedDebugOpensCapture(t *testing.T) {
	env := testEnv(t)
	Dispatch(context.Background(), payload(t, "ue-debug-error"), env)

	r, err := state.Load(state.Path(env.StateDir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	if r.Skill != "debug" || r.Capture != "running" || r.Task != "" {
		t.Errorf("skill, capture, task = %q, %q, %q; want debug, running, empty", r.Skill, r.Capture, r.Task)
	}
}

type recorder struct{ got []observe.Observation }

func (r *recorder) Observe(_ context.Context, o observe.Observation) error {
	r.got = append(r.got, o)

	return nil
}

func TestObservationsFromRecordedPayloads(t *testing.T) {
	commit := &observe.Committed{Branch: "main", SHA: "b48b585", Subject: "FT-12: add a retry to the client"}

	for _, tc := range []struct {
		payload string
		want    []observe.Observation
	}{
		{"bash-commit-heredoc", []observe.Observation{commit}},
		{"bash-commit-push", []observe.Observation{
			commit,
			&observe.Pushed{Remote: "github.com:acme/app.git", Refs: []string{"main -> main"}},
		}},
		{"bash-push-ff", []observe.Observation{
			&observe.Pushed{Remote: "github.com:acme/app.git", Refs: []string{"main -> main"}},
		}},
		{"bash-push-uptodate", nil},
		{"bash-push-rejected", nil},
		{"bash-commit-dry-run", nil},
		{"bash-gh-issue", []observe.Observation{
			&observe.IssueCreated{Host: "gh", URL: "https://github.com/acme/app/issues/12"},
		}},
		{"bash-gh-issue-two", []observe.Observation{
			&observe.IssueCreated{Host: "gh", URL: "https://github.com/acme/app/issues/12"},
			&observe.IssueCreated{Host: "gh", URL: "https://github.com/acme/app/issues/13"},
		}},
		{"bash-gh-pr", []observe.Observation{
			&observe.PRCreated{Host: "gh", URL: "https://github.com/acme/app/pull/34"},
		}},
		{"bash-gh-pr-exists", nil},
		{"bash-glab-mr", []observe.Observation{
			&observe.PRCreated{Host: "glab", URL: "https://gitlab.com/acme/app/-/merge_requests/34"},
		}},
		{"bash-load-step", []observe.Observation{&observe.StepStarted{Steps: []string{"implement"}}}},
		{"bash-load-planning", []observe.Observation{&observe.StepStarted{Steps: []string{"fetch", "plan"}}}},
		{"bash-load-failed", nil},
		{"bash-finish", []observe.Observation{&observe.Finished{Args: []string{"FT-12", "--yes", "--merge"}}}},
		{"bash-other", nil},
		{"post-write-other", nil},
		{"write-findings", []observe.Observation{
			&observe.FindingsWritten{Path: "/work/acme/.f10/reviews/FT-12/1.md", Task: "FT-12", Round: "1"},
		}},
		{"write-plan-archive", nil},
		{"post-write-plan", []observe.Observation{
			&observe.PlanWritten{Path: "/work/acme/.f10/plans/FT-12.md", Task: "FT-12"},
		}},
	} {
		t.Run(tc.payload, func(t *testing.T) {
			env := testEnv(t)
			rec := &recorder{}
			env.Observers = []observe.Observer{rec}

			Dispatch(context.Background(), payload(t, tc.payload), env)

			for _, o := range rec.got {
				if m := observe.MetaOf(o); m.Session != "s1" || m.Cwd != "/work/acme" || !m.At.Equal(fixedNow) {
					t.Errorf("%s: meta not stamped: %+v", o.Kind(), m)
				}

				observe.Stamp(o, "", "", time.Time{})
			}

			if !reflect.DeepEqual(rec.got, tc.want) {
				t.Errorf("observations\n got: %s\nwant: %s", dump(rec.got), dump(tc.want))
			}
		})
	}
}

func dump(obs []observe.Observation) string {
	var parts []string

	for _, o := range obs {
		b, _ := json.Marshal(o)
		parts = append(parts, o.Kind()+string(b))
	}

	return strings.Join(parts, " ")
}

// The storage root comes from the project, so a plan or findings file in
// out-of-tree storage counts the same as one in-repo.
func TestOutOfTreeStorage(t *testing.T) {
	env := testEnv(t)
	rec := &recorder{}
	env.Observers = []observe.Observer{rec}
	env.StorageRoot = func(context.Context, string) string { return "/home/dev/.f10/acme" }

	Dispatch(context.Background(), payload(t, "write-plan-out-of-tree"), env)
	Dispatch(context.Background(), payload(t, "edit-findings-out-of-tree"), env)

	if got := dump(rec.got); !strings.Contains(got, `plan_written{"session":"s1"`) ||
		!strings.Contains(got, `"round":"2"`) {
		t.Errorf("observations: %s", got)
	}

	r, err := state.Load(state.Path(env.StateDir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	if r.Plan != "done" || r.Task != "FT-12" {
		t.Errorf("plan %s task %s, want done FT-12", r.Plan, r.Task)
	}
}

func startState(t *testing.T, env *Env, r *state.Run) {
	t.Helper()

	r.Session, r.Root = "s1", "/work/acme"
	if err := r.Save(env.StateDir, fixedNow); err != nil {
		t.Fatal(err)
	}
}

func loadS1(t *testing.T, env *Env) *state.Run {
	t.Helper()

	r, err := state.Load(state.Path(env.StateDir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func TestStepStartedFillsInTheShipLeaf(t *testing.T) {
	for _, tc := range []struct {
		name     string
		start    state.Run
		payload  string
		ship     string
		leaf     string
		keepsAsk bool
	}{
		{"ship running", state.Run{Skill: "ship", Capture: "prior", Plan: "done", Ship: "running", Leaf: "pr"}, "bash-load-step", "running", "implement", false},
		{"ship pending", state.Run{Skill: "ship", Capture: "prior", Plan: "done", Ship: "pending"}, "bash-load-step", "running", "implement", false},
		{"never over a stop state", state.Run{Skill: "ship", Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Ask: "1. go? [no | yes]"}, "bash-load-step", "blocked", "judge", true},
		{"never over done", state.Run{Skill: "ship", Capture: "prior", Plan: "done", Ship: "done"}, "bash-load-step", "done", "", false},
		{"not under plan", state.Run{Skill: "plan", Capture: "skipped", Plan: "running", Ship: "pending"}, "bash-load-step", "pending", "", false},
		{"several steps name none", state.Run{Skill: "ship", Capture: "prior", Plan: "done", Ship: "running", Leaf: "pr"}, "bash-load-planning", "running", "pr", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			startState(t, env, &tc.start)

			Dispatch(context.Background(), payload(t, tc.payload), env)

			r := loadS1(t, env)
			if r.Ship != tc.ship || r.Leaf != tc.leaf {
				t.Errorf("ship %s leaf %q, want %s %q", r.Ship, r.Leaf, tc.ship, tc.leaf)
			}

			if tc.keepsAsk && r.Ask == "" {
				t.Error("the driven run's question was cleared")
			}
		})
	}
}

func TestIssueCreatedNamesTheCapturedTask(t *testing.T) {
	idOf := func(_ context.Context, _, url string) string {
		return "FT-" + url[strings.LastIndex(url, "/")+1:]
	}

	for _, tc := range []struct {
		name    string
		start   state.Run
		payload string
		task    string
	}{
		{"capture running", state.Run{Skill: "capture", Capture: "running", Plan: "pending", Ship: "pending"}, "bash-gh-issue", "FT-12"},
		{"parent wins over subtask", state.Run{Skill: "capture", Capture: "running", Plan: "pending", Ship: "pending"}, "bash-gh-issue-two", "FT-12"},
		{"task already named", state.Run{Skill: "capture", Task: "FT-9", Capture: "running", Plan: "pending", Ship: "pending"}, "bash-gh-issue", "FT-9"},
		{"capture over", state.Run{Skill: "ship", Capture: "done", Plan: "done", Ship: "running"}, "bash-gh-issue", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			env.TaskID = idOf
			startState(t, env, &tc.start)

			Dispatch(context.Background(), payload(t, tc.payload), env)

			r := loadS1(t, env)
			if r.Task != tc.task {
				t.Errorf("task %q, want %q", r.Task, tc.task)
			}

			if r.Capture != tc.start.Capture {
				t.Errorf("capture %s, want it left at %s for capture to report", r.Capture, tc.start.Capture)
			}
		})
	}
}

func TestBareStatusIsAnsweredWithoutAModelTurn(t *testing.T) {
	env := testEnv(t)
	asked := ""
	env.StatusText = func(_ context.Context, session string) string {
		asked = session

		return "task     FT-12\nship     ◎ running: implement\n"
	}

	out := Dispatch(context.Background(), payload(t, "ue-status-bare"), env)

	var verdict struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Specific struct {
			Event    string `json:"hookEventName"`
			Suppress bool   `json:"suppressOriginalPrompt"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &verdict); err != nil {
		t.Fatalf("not a JSON verdict: %v\n%s", err, out)
	}

	if verdict.Decision != "block" || !verdict.Specific.Suppress || verdict.Specific.Event != "UserPromptExpansion" {
		t.Errorf("verdict %+v", verdict)
	}

	if asked != "s1" {
		t.Errorf("status asked for session %q", asked)
	}

	lines := strings.Split(verdict.Reason, "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "f10 · status") ||
		!strings.HasSuffix(lines[0], "no model turn") ||
		lines[1] != strings.Repeat("─", 44) ||
		lines[3] != "ship     ◎ running: implement" {
		t.Errorf("reason:\n%s", verdict.Reason)
	}

	if _, err := os.Stat(state.Path(env.StateDir, "s1")); !os.IsNotExist(err) {
		t.Error("a status answer seeded a run")
	}

	// with a task id, the question it may relay needs the model
	if out := Dispatch(context.Background(), payload(t, "ue-status-args"), env); out != nil {
		t.Errorf("status with an argument answered: %s", out)
	}
}

func TestStatusWithNothingToSay(t *testing.T) {
	env := testEnv(t)
	env.StatusText = func(context.Context, string) string { return "" }

	out := Dispatch(context.Background(), payload(t, "ue-status-bare"), env)
	if !strings.Contains(string(out), "no f10 run in this session") {
		t.Errorf("verdict: %s", out)
	}
}

func TestSessionStart(t *testing.T) {
	env := testEnv(t)
	env.PluginRoot = t.TempDir()

	// prune compares with real file times, so the clock is the real one
	now := time.Now()
	env.Now = func() time.Time { return now }

	renderer := filepath.Join(env.PluginRoot, "bin", "f10-state.sh")
	if err := os.MkdirAll(filepath.Dir(renderer), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(renderer, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(env.Home, ".claude", "f10", "statusline")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/old/plugin/bin/f10-state.sh", link); err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(env.StateDir, "dead.state")
	fresh := filepath.Join(env.StateDir, "live.state")
	other := filepath.Join(env.StateDir, "notes.txt")

	for _, f := range []string{old, fresh, other, filepath.Join(env.StateDir, "dead.conventions")} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	week := now.Add(-8 * 24 * time.Hour)
	for _, f := range []string{old, other, filepath.Join(env.StateDir, "dead.conventions")} {
		if err := os.Chtimes(f, week, week); err != nil {
			t.Fatal(err)
		}
	}

	Dispatch(context.Background(), payload(t, "ss-compact"), env)

	if _, err := os.Stat(state.StartedPath(env.StateDir, "s1")); !os.IsNotExist(err) {
		t.Error("compact marked a start: the loaded plugin did not change")
	}

	Dispatch(context.Background(), payload(t, "ss-startup"), env)

	if dest, err := os.Readlink(link); err != nil || dest != renderer {
		t.Errorf("statusline -> %q (%v), want %s", dest, err, renderer)
	}

	if _, err := os.Stat(state.StartedPath(env.StateDir, "s1")); err != nil {
		t.Errorf("startup left no start mark: %v", err)
	}

	for f, kept := range map[string]bool{
		old: false, filepath.Join(env.StateDir, "dead.conventions"): false, fresh: true, other: true,
	} {
		if _, err := os.Stat(f); (err == nil) != kept {
			t.Errorf("%s: kept = %v, want %v", filepath.Base(f), err == nil, kept)
		}
	}
}

func TestDebugLogAndGarbage(t *testing.T) {
	env := testEnv(t)
	env.Debug = true

	if out := Dispatch(context.Background(), []byte("not json"), env); out != nil {
		t.Errorf("garbage printed %s", out)
	}

	Dispatch(context.Background(), payload(t, "stop"), env)

	raw, err := os.ReadFile(filepath.Join(env.StateDir, "hooks.log"))
	if err != nil {
		t.Fatal(err)
	}

	if lines := strings.Count(string(raw), "\n"); lines < 2 || !strings.Contains(string(raw), `"Stop"`) {
		t.Errorf("hooks.log:\n%s", raw)
	}
}
