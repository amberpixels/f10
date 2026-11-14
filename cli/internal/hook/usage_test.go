package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// usageSession is a session in a temp dir: its transcript, an event
// builder that names it, and an env whose clock moves a second per hook.
type usageSession struct {
	t    *testing.T
	env  *Env
	db   string
	path string
	work string
}

func newUsageSession(t *testing.T) *usageSession {
	t.Helper()

	db := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, db)

	env := testEnv(t)
	now := fixedNow
	env.Now = func() time.Time { now = now.Add(time.Second); return now }
	env.Profile = func(context.Context, string) agentctl.Profile {
		return agentctl.Profile{Kind: "claude", Model: "opus", Effort: "high"}
	}

	dir := t.TempDir()
	s := &usageSession{t: t, env: env, db: db, path: filepath.Join(dir, "s1.jsonl"), work: t.TempDir()}
	s.append(`{"type":"user","uuid":"u0","message":{"content":"before the run"}}`,
		assistant("a0", "m0", "claude-sonnet-5-5", 999, false)) // spent before any skill: never counted

	return s
}

func assistant(uuid, id, model string, out int, side bool) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"isSidechain":%v,"message":{"id":%q,"model":%q,"usage":`+
			`{"input_tokens":1,"output_tokens":%d,"cache_creation_input_tokens":10,"cache_read_input_tokens":100}}}`,
		uuid, side, id, model, out)
}

func (s *usageSession) append(lines ...string) {
	s.t.Helper()

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()

	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			s.t.Fatal(err)
		}
	}
}

func (s *usageSession) event(name string, extra map[string]any) {
	s.t.Helper()

	m := map[string]any{"session_id": "s1", "cwd": s.work, "transcript_path": s.path, "hook_event_name": name}
	maps.Copy(m, extra)

	raw, err := json.Marshal(m)
	if err != nil {
		s.t.Fatal(err)
	}

	dispatchRecorded(s.t, s.env, raw)
}

// ship starts a ship run, its phase set as the skill's first badge call
// would, mid-pipeline so the Stop backstop leaves it running.
func (s *usageSession) ship() {
	s.t.Helper()
	s.event("UserPromptExpansion", map[string]any{"command_name": "f10:ship", "command_args": "FT-12"})
	s.run("running")
}

func (s *usageSession) run(status string) {
	s.t.Helper()
	startState(s.t, s.env, &state.Run{
		Task: "FT-12", Skill: "ship", Capture: "prior", Plan: "prior", Ship: status, Leaf: "implement", Final: "pr",
	})
}

func (s *usageSession) turns() []ledger.TurnUsedPayload {
	s.t.Helper()

	var out []ledger.TurnUsedPayload

	for _, e := range recorded(s.t, s.db, ledger.Filter{Kind: ledger.TurnUsed}) {
		p, err := ledger.Decode[ledger.TurnUsedPayload](e)
		if err != nil || e.Task != "FT-12" {
			s.t.Errorf("turn row %+v: %v", e, err)
		}

		out = append(out, p)
	}

	return out
}

// Each turn of a run leaves a turn_used row of what it spent, subagents
// included, summing to the transcript's own totals; run_started and a
// changed model carry the run context.
func TestEachTurnRecordsItsUsageAndTheRunContext(t *testing.T) {
	s := newUsageSession(t)
	_ = os.WriteFile(state.ModelPath(s.env.StateDir, "s1"), []byte("claude-fable-5-2\n"), 0o600)

	// a subagent that ran before the skill is not the run's cost
	sub := filepath.Join(filepath.Dir(s.path), "s1", "subagents")
	_ = os.MkdirAll(sub, 0o750)
	_ = os.WriteFile(
		filepath.Join(sub, "agent-0.jsonl"),
		[]byte(assistant("x0", "mx0", "claude-haiku-5-5", 900, true)+"\n"),
		0o600,
	)

	s.ship()
	s.append(
		assistant("a1", "m1", "claude-sonnet-5-5", 100, false),
		assistant("a1b", "m1", "claude-sonnet-5-5", 100, false),
	)
	s.event("Stop", nil)

	_ = os.WriteFile(
		filepath.Join(sub, "agent-1.jsonl"),
		[]byte(assistant("x1", "mx", "claude-haiku-5-5", 40, true)+"\n"),
		0o600,
	)
	s.append(assistant("a2", "m2", "claude-sonnet-5-5", 20, false), assistant("a3", "m3", "claude-sonnet-5-5", 7, true))
	s.event("Stop", nil)

	s.append(assistant("a4", "m4", "claude-opus-5-5", 3, false))
	s.event("Stop", nil)

	starts := recorded(t, s.db, ledger.Filter{Kind: ledger.RunStarted})
	if len(starts) != 1 {
		t.Fatalf("%d run_started rows", len(starts))
	}

	rs, _ := ledger.Decode[ledger.RunStartedPayload](starts[0])
	if rs.Model != "claude-sonnet-5-5" || rs.Agent != "claude" || rs.F10Version == "" ||
		rs.Profile != (ledger.AgentProfile{Kind: "claude", Model: "opus", Effort: "high"}) {
		t.Errorf("run_started context %+v", rs.RunContext)
	}

	var (
		sum, subs ledger.Tokens
		throughs  []string
	)

	turns := s.turns()
	for _, p := range turns {
		sum.Output += p.Usage.Output
		sum.Input += p.Usage.Input
		subs.Output += p.Usage.Subagents.Output
		throughs = append(throughs, p.Usage.Through)
	}

	if len(turns) != 3 || sum.Output != 123 || sum.Input != 3 || subs.Output != 47 {
		t.Errorf("%d turn rows summing %+v, subagents %+v", len(turns), sum, subs)
	}

	// a1b repeats a1's message, so a1 is the last entry the first turn counted
	if want := []string{"a1", "a2", "a4"}; fmt.Sprint(throughs) != fmt.Sprint(want) {
		t.Errorf("through %v, want %v", throughs, want)
	}

	// the second turn ran on the same model and status: no stop row; the
	// switch to opus is one, the only one carrying a context
	var models []string // status/model

	for _, e := range recorded(t, s.db, ledger.Filter{Kind: ledger.SessionStopped}) {
		p, _ := ledger.Decode[ledger.SessionStoppedPayload](e)
		models = append(models, p.Status+"/"+p.Model)
	}

	if want := []string{"running/", "running/claude-opus-5-5"}; fmt.Sprint(models) != fmt.Sprint(want) {
		t.Errorf("stop rows' models %q, want %q", models, want)
	}
}

// The turn that ends a run counts; later turns count for no task until a
// skill starts the next run, which counts from its own start.
func TestUsageStopsWhenTheRunIsDone(t *testing.T) {
	s := newUsageSession(t)
	s.ship()

	s.append(assistant("a1", "m1", "m", 1, false))
	s.run("done")
	s.event("Stop", nil)

	s.append(assistant("a2", "m2", "m", 2, false)) // chat after the run
	s.event("Stop", nil)

	s.append(assistant("a3", "m3", "m", 4, false)) // still after it, before the next skill
	s.ship()
	s.append(assistant("a4", "m4", "m", 8, false))
	s.event("Stop", nil)

	var outs []int
	for _, p := range s.turns() {
		outs = append(outs, p.Usage.Output)
	}

	if want := []int{1, 8}; fmt.Sprint(outs) != fmt.Sprint(want) {
		t.Errorf("turn outputs %v, want %v", outs, want)
	}
}

// The work a Stop adds for usage and context, on a 20 MB transcript after
// its first turn, stays under the hook's 50ms budget.
func TestStopUsageWorkIsFastOnALargeTranscript(t *testing.T) {
	s := newUsageSession(t)

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	pad := strings.Repeat("y", 4000)
	for i := range 5000 {
		_, _ = fmt.Fprintf(f, `{"type":"user","uuid":"u%d","message":{"content":%q}}`+"\n", i, pad)
	}

	_ = f.Close()

	startUsage(s.env.StateDir, "s1", s.path)
	s.append(assistant("a1", "m1", "m", 1, false))

	start := time.Now()
	u := turnUsage(s.env.StateDir, "s1", s.path, false)
	_ = runContext(t.Context(), s.env, "s1", s.work, s.path, "")

	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("usage work took %v", took)
	}

	if u == nil || u.Output != 1 {
		t.Errorf("usage %+v", u)
	}
}

// A first turn's run_started reads the SessionStart model when the
// transcript has no assistant line yet.
func TestRunStartedFallsBackToTheSessionStartModel(t *testing.T) {
	s := newUsageSession(t)
	_ = os.WriteFile(s.path, []byte(`{"type":"user","uuid":"u0"}`+"\n"), 0o600)
	_ = os.WriteFile(state.ModelPath(s.env.StateDir, "s1"), []byte("claude-fable-5-2\n"), 0o600)

	s.ship()

	rs, _ := ledger.Decode[ledger.RunStartedPayload](recorded(t, s.db, ledger.Filter{Kind: ledger.RunStarted})[0])
	if rs.Model != "claude-fable-5-2" {
		t.Errorf("model %q", rs.Model)
	}
}

// A transcript that is gone or unreadable still records the stop, with no
// usage row.
func TestStopsWithoutAReadableTranscript(t *testing.T) {
	for name, spoil := range map[string]func(path string){
		"missing": func(path string) { _ = os.Remove(path) },
		"corrupt": func(path string) { _ = os.WriteFile(path, []byte("\x00garbage\n{\"type\":\"assistant\"\n"), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			s := newUsageSession(t)
			s.ship()
			spoil(s.path)
			s.event("Stop", nil)

			if n := len(recorded(t, s.db, ledger.Filter{Kind: ledger.SessionStopped})); n != 1 {
				t.Errorf("%d stop rows", n)
			}

			if n := len(recorded(t, s.db, ledger.Filter{Kind: ledger.TurnUsed})); n != 0 {
				t.Errorf("%d usage rows", n)
			}
		})
	}
}

// A session with no f10 run never reads its transcript.
func TestASessionWithNoRunRecordsNoUsage(t *testing.T) {
	s := newUsageSession(t)
	s.event("Stop", nil)

	if _, err := os.Stat(state.UsagePath(s.env.StateDir, "s1")); !os.IsNotExist(err) {
		t.Errorf("a usage position for a session with no run: %v", err)
	}

	if _, err := os.Stat(s.db); err == nil {
		if n := len(recorded(t, s.db, ledger.Filter{})); n != 0 {
			t.Errorf("%d rows", n)
		}
	}
}
