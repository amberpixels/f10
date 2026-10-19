package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/observe"
)

// dispatchRecorded runs one hook process: a fresh observer, closed after.
func dispatchRecorded(t *testing.T, env *Env, raw []byte) {
	t.Helper()

	rec := &ledger.Observer{
		StateDir: env.StateDir,
		Busy:     ledger.BusyAsync,
		TaskID:   func(context.Context, string, string) string { return "FT-12" },
	}
	defer rec.Close()

	env.Observers = []observe.Observer{rec}
	Dispatch(context.Background(), raw, env)
}

func toolPayload(t *testing.T, session, cwd, tool string, input, response map[string]any) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"session_id": session, "cwd": cwd, "hook_event_name": "PostToolUse",
		"tool_name": tool, "tool_input": input, "tool_response": response,
	})
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

const findingsOpen = `task: FT-12
round: 1
reviewed: b48b585
base: main 0000000
reviewer: local
verdict: findings

## Categories

- correctness and logic: 1
- tests and docs: none found

## Findings

### 1.1 Retry loops forever
where: client.go:10
category: correctness and logic
Fix: cap it.
`

// A capture, plan, ship and finish run leaves every observed and artifact
// kind, each with its source.
func TestARunLeavesEveryKindInTheLedger(t *testing.T) {
	db := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, db)

	env := testEnv(t)
	work := t.TempDir()
	plans := filepath.Join(work, ".f10", "plans")
	reviews := filepath.Join(work, ".f10", "reviews", "FT-12")

	for _, d := range []string{filepath.Join(plans, "archive"), reviews} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	bash := func(command, stdout string) []byte {
		return toolPayload(t, "s1", work, "Bash", map[string]any{"command": command}, map[string]any{"stdout": stdout})
	}
	write := func(path, content string) []byte {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		return toolPayload(t, "s1", work, "Write", map[string]any{"file_path": path}, map[string]any{})
	}

	findingsPath := filepath.Join(reviews, "1.md")
	resolved := strings.Replace(findingsOpen, "Fix: cap it.\n", "Fix: cap it.\nverdict: fix\nreason: capped at 3\n", 1)

	if err := os.WriteFile(filepath.Join(plans, "archive", "FT-12.1.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	steps := [][]byte{
		// a commit before any f10 skill started is out of scope
		toolPayload(t, "s9", "/work/other", "Bash", map[string]any{"command": "git commit -m x"},
			map[string]any{"stdout": "[main 1a2b3c4] x\n"}),
		payload(t, "ue-capture-desc"),
		payload(t, "bash-gh-issue"),
		payload(t, "stop"),
		payload(t, "ue-plan-id"),
		bash("mv "+filepath.Join(plans, "FT-12.md")+" "+filepath.Join(plans, "archive", "FT-12.1.md"), ""),
		write(filepath.Join(plans, "FT-12.md"), "# FT-12\n"),
		payload(t, "stop"),
		payload(t, "stop"), // the same stop again is no new row
		payload(t, "ue-ship-id"),
		payload(t, "bash-load-step"),
		payload(t, "bash-commit-push"),
		write(findingsPath, findingsOpen),
		write(findingsPath, resolved),
		write(findingsPath, resolved), // nothing changed: no new verdict
		payload(t, "bash-gh-pr"),
		payload(t, "stop"),
		payload(t, "ue-judge"),
		payload(t, "pre-skill-other"), // not an f10 skill
		payload(t, "bash-finish"),
	}

	for _, raw := range steps {
		dispatchRecorded(t, env, raw)
	}

	l, err := ledger.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	events, err := l.List(context.Background(), ledger.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, e := range events {
		got = append(got, e.Source+":"+e.Kind)

		if e.Session != "s1" {
			t.Errorf("%s recorded for session %q", e.Kind, e.Session)
		}
	}

	want := []string{
		"observed:run_started", "observed:task_captured", "observed:session_stopped",
		"observed:run_started", "artifact:plan_superseded", "artifact:plan_saved", "observed:session_stopped",
		"observed:run_started", "observed:step_entered", "observed:committed", "observed:pushed",
		"artifact:review_round_opened", "artifact:finding_verdict",
		"observed:pr_opened", "observed:session_stopped",
		"observed:run_started", "observed:task_finished",
	}
	if !slices.Equal(got, want) {
		t.Errorf("kinds\n got %v\nwant %v", got, want)
	}

	stops := []string{}

	for _, e := range events {
		switch e.Kind {
		case ledger.SessionStopped:
			stops = append(stops, str(e.Payload.Val["phase"])+" "+str(e.Payload.Val["status"]))
		case ledger.TaskCaptured:
			if e.Task != "FT-12" || e.Payload.Val["url"] != "https://github.com/acme/app/issues/12" {
				t.Errorf("captured %+v", e)
			}
		case ledger.ReviewRoundOpened:
			cats, _ := e.Payload.Val["categories"].(map[string]any)
			if e.Payload.Val["reviewed"] != "b48b585" || cats["correctness and logic"] != float64(1) {
				t.Errorf("round %+v", e.Payload.Val)
			}
		case ledger.FindingVerdict:
			if e.Payload.Val["finding"] != "1.1" || e.Payload.Val["verdict"] != "fix" ||
				e.Payload.Val["reason"] != "capped at 3" {
				t.Errorf("verdict %+v", e.Payload.Val)
			}
		case ledger.RunStarted:
			if e.Payload.Val["skill"] == "" {
				t.Errorf("run started with no skill: %+v", e.Payload.Val)
			}
		}

		if e.Project != work && e.Project != "/work/acme" || !e.ProjectLocal {
			t.Errorf("%s: project %q local=%v", e.Kind, e.Project, e.ProjectLocal)
		}
	}

	// each stop as the run stood when the turn ended, before the backstop;
	// the plan file written is plan done, observed rather than inferred
	if want := []string{"capture running", "plan done", "ship running"}; !slices.Equal(stops, want) {
		t.Errorf("stops %q, want %q", stops, want)
	}
}

func recorded(t *testing.T, db string, f ledger.Filter) []ledger.Event {
	t.Helper()

	l, err := ledger.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	events, err := l.List(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}

	return events
}

// The row keeps where the run stopped; the backstop's done is a flag on it,
// never its status.
func TestSessionStoppedRecordsTheStateBeforeTheBackstop(t *testing.T) {
	db := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, db)

	env := testEnv(t)
	start := "v 1\ntask FT-12\nroot /work/acme\nskill ship\ncapture prior\nplan prior\nship running\nleaf review\nfinal review\nupdated 1\n"

	if err := os.WriteFile(filepath.Join(env.StateDir, "s1.state"), []byte(start), 0o600); err != nil {
		t.Fatal(err)
	}

	dispatchRecorded(t, env, payload(t, "stop"))

	got := recorded(t, db, ledger.Filter{Kind: ledger.SessionStopped})
	if len(got) != 1 {
		t.Fatalf("got %d rows", len(got))
	}

	p := got[0].Payload.Val
	if p["phase"] != "ship" || p["status"] != "running" || p["leaf"] != "review" || p["backstop"] != true ||
		got[0].Task != "FT-12" {
		t.Errorf("stop row %+v", p)
	}

	// the backstop still promoted the badge
	raw, err := os.ReadFile(filepath.Join(env.StateDir, "s1.state"))
	if err != nil || !strings.Contains(string(raw), "ship done") {
		t.Errorf("state after the backstop:\n%s", raw)
	}
}

// A round-two file reuses round-one ids under its prior section: each status
// is recorded against round two, a block with a status and no verdict too.
func TestRoundTwoRecordsPriorStatuses(t *testing.T) {
	db := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, db)

	env := testEnv(t)
	work := t.TempDir()
	reviews := filepath.Join(work, ".f10", "reviews", "FT-12")

	if err := os.MkdirAll(reviews, 0o750); err != nil {
		t.Fatal(err)
	}

	write := func(name, content string) {
		path := filepath.Join(reviews, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		dispatchRecorded(
			t,
			env,
			toolPayload(t, "s1", work, "Write", map[string]any{"file_path": path}, map[string]any{}),
		)
	}

	write("1.md", `task: FT-12
round: 1
reviewed: aaaaaaa
reviewer: local
verdict: findings

## Findings

### 1.1 Retry loops forever
where: client.go:10
category: correctness and logic
verdict: skip +reply
reason: bounded by the caller

### 1.2 No timeout
where: client.go:20
category: correctness and logic
verdict: fix
`)
	write("2.md", `task: FT-12
round: 2
reviewed: bbbbbbb
reviewer: local
verdict: clean

## Prior round

### 1.1 Retry loops forever - explanation accepted
The caller bounds it.
where: client.go:10
verdict: skip +reply
reason: bounded by the caller

### 1.2 No timeout - fixed
where: client.go:20

## Categories

- correctness and logic: none found
`)

	var got []string

	for _, e := range recorded(t, db, ledger.Filter{Kind: ledger.FindingVerdict}) {
		p := e.Payload.Val
		got = append(
			got,
			strings.Join([]string{str(p["round"]), str(p["finding"]), str(p["verdict"]), str(p["status"])}, " "),
		)
	}

	want := []string{"1 1.1 skip +reply ", "1 1.2 fix ", "2 1.1 skip +reply explanation accepted", "2 1.2  fixed"}
	if !slices.Equal(got, want) {
		t.Errorf("verdict rows\n got %q\nwant %q", got, want)
	}
}

func str(v any) string {
	s, _ := v.(string)

	return s
}
