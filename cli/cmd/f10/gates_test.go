package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

var gatePipeline = []string{"implement", "cleanup", "pr", "review (local)", "judge", "pr"}

// hookEvent is a hook's row in a checkout on a branch.
func hookEvent(at time.Time, session, kind string, p ledger.Payload) ledger.Event {
	e := sessionEvent(at, session, kind, p)
	e.Task, e.Branch, e.Checkout = "", "FT-1/thing", "/code/repo.FT-1"

	return e
}

// texts is each gate's step and text, for a test to compare.
func texts(gates []gate) map[string]string {
	out := map[string]string{}

	for _, g := range gates {
		key := g.Step
		if g.Judged != "" {
			key += " " + g.Judged
		}

		if _, seen := out[key]; seen {
			key += " again"
		}

		out[key] = g.text()
	}

	return out
}

func checkTexts(t *testing.T, gates []gate, want map[string]string) {
	t.Helper()

	got := texts(gates)

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestGatesDuringShip(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	events := []ledger.Event{
		hookEvent(at(0), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship", "args": "FT-1"}),
		shipClaim(at(1), "s1", "implement"),
		shipClaim(at(2), "s1", "cleanup"),
		shipClaim(at(3), "s1", "pr"),
		shipClaim(at(4), "s1", "review (local)"),
		// resolve called through the Skill tool inside ship: ship's, not manual
		hookEvent(at(5), "s1", ledger.RunStarted, ledger.Payload{"skill": "resolve", "args": "FT-1"}),
	}

	run := &state.Run{Session: "s1", Task: "FT-1", Ship: "running", Leaf: "review (local)", Pipeline: gatePipeline}

	gates := taskGates(context.Background(), gateInput{
		task: "FT-1", branch: "FT-1/thing", run: run, events: events, remoteFlow: true, now: at(6),
	})

	checkTexts(t, gates, map[string]string{
		"implement":    "done",
		"cleanup":      "done",
		"pr":           "done",
		"review":       "local: round 1 running (in ship) · remote: pending",
		"judge change": "change: pending",
		"pr again":     "pending",
	})

	if n := len(gates); n != 6 {
		t.Errorf("%d gates, want implement, cleanup, pr, review, judge, pr", n)
	}

	// the reviewer wrote round 1 after ship entered the review: resolve is on it
	gates = taskGates(context.Background(), gateInput{
		task: "FT-1", run: run, events: events, now: at(6),
		outcomes: outcomes{local: []reviewRound{{Round: "1", Found: 2, Ask: 2}}, localAt: at(4).Add(30 * time.Second)},
	})

	checkTexts(t, gates, map[string]string{"review": "local: resolve running (in ship)"})
}

// One review:local+ci entry is the one review gate with both halves, each
// moving from review to resolve once its own findings land.
func TestGatesBothHalvesInOneEntry(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	pipeline := []string{"implement", "pr", "review:local+ci", "pr"}
	events := []ledger.Event{
		hookEvent(at(0), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship", "args": "FT-1"}),
		shipClaim(at(1), "s1", "implement"),
		shipClaim(at(2), "s1", "pr"),
		shipClaim(at(3), "s1", "review:local+ci"),
	}
	run := &state.Run{Session: "s1", Task: "FT-1", Ship: "running", Leaf: "review:local+ci", Pipeline: pipeline}

	pending := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: pipeline, events: events[:3], now: at(3),
	})
	checkTexts(t, pending, map[string]string{"review": "local: pending · remote: pending"})

	if n := len(pending); n != 5 {
		t.Errorf("%d gates, want implement, pr, review, pr, judge off: %v", n, texts(pending))
	}

	running := taskGates(context.Background(), gateInput{task: "FT-1", run: run, events: events, now: at(4)})
	checkTexts(t, running, map[string]string{
		"review": "local: round 1 running (in ship) · remote: round 1 running (in ship)",
	})

	// the local reviewer wrote its findings; the CI review has not landed, so
	// nothing is resolving yet
	local := taskGates(context.Background(), gateInput{
		task: "FT-1", run: run, events: events, now: at(5),
		outcomes: outcomes{local: []reviewRound{{Round: "1", Found: 1, Ask: 1}}, localAt: at(4)},
	})
	checkTexts(t, local, map[string]string{
		"review": "local: round 1 in, waiting for the other half (in ship) · remote: round 1 running (in ship)",
	})

	// both halves landed: the one resolve settles them
	both := taskGates(context.Background(), gateInput{
		task: "FT-1", run: run, events: events, now: at(6),
		outcomes: outcomes{
			local: []reviewRound{{Round: "1", Found: 1, Ask: 1}}, localAt: at(4),
			remote: []reviewRound{{Round: "1", Found: 1, Ask: 1}}, remoteAt: at(5),
		},
	})
	checkTexts(t, both, map[string]string{
		"review": "local: resolve running (in ship) · remote: resolve running (in ship)",
	})

	done := &state.Run{Session: "s1", Task: "FT-1", Ship: "done", Pipeline: pipeline}
	settled := taskGates(context.Background(), gateInput{
		task: "FT-1", run: done, events: events, now: at(9),
		outcomes: outcomes{
			local: []reviewRound{{Round: "1", Found: 1, Fixed: 1}}, localAt: at(6),
			remote: []reviewRound{{Round: "1", Found: 2, Skipped: 2}}, remoteAt: at(6),
		},
	})
	checkTexts(t, settled, map[string]string{
		"review": "local: 1 found (1 fixed) · remote: 2 found (2 skipped)",
	})
}

func TestGatesStartedOutsideShip(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	done := &state.Run{Session: "s1", Task: "FT-1", Ship: "done", Pipeline: []string{"implement", "pr"}}
	base := []ledger.Event{
		hookEvent(at(0), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		shipClaim(at(1), "s1", "implement"),
		shipClaim(at(2), "s1", "pr"),
		sessionEvent(at(3), "s1", ledger.StateSet, ledger.Payload{"args": []any{"ship", "done"}, "ship": "done"}),
	}

	for _, tc := range []struct {
		name  string
		extra []ledger.Event
		want  map[string]string
	}{
		{
			"a review by hand in another session",
			[]ledger.Event{hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "review", "args": "this branch"})},
			map[string]string{"review": "local: round 1 running (manual)", "implement": "done", "pr": "done"},
		},
		{
			"a driven review in the ship's own session, after ship",
			[]ledger.Event{hookEvent(at(4), "s1", ledger.RunStarted, ledger.Payload{"skill": "review", "args": "FT-1 --driven"})},
			map[string]string{"review": "local: round 1 running (driven)"},
		},
		{
			"a remote review read in",
			[]ledger.Event{hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "review", "args": "FT-1 ci"})},
			map[string]string{"review": "local: pending · remote: round 1 running (manual)"},
		},
		{
			"a review in the ship's session whose turn ended",
			[]ledger.Event{
				hookEvent(at(4), "s1", ledger.RunStarted, ledger.Payload{"skill": "review"}),
				hookEvent(at(5), "s1", ledger.SessionStopped, ledger.Payload{}),
			},
			map[string]string{"review": "local: pending"},
		},
		{
			"a review whose session moved on to another skill",
			[]ledger.Event{
				hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "review"}),
				hookEvent(at(5), "s2", ledger.RunStarted, ledger.Payload{"skill": "explain"}),
			},
			map[string]string{"review": "local: pending"},
		},
		{
			"a review older than the state TTL, its stop never written",
			[]ledger.Event{hookEvent(at(4).Add(-48*time.Hour), "s2", ledger.RunStarted, ledger.Payload{"skill": "review"})},
			map[string]string{"review": "local: pending"},
		},
		{
			"a review typed in a session whose ship stopped blocked",
			[]ledger.Event{
				sessionEvent(at(4), "s3", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
				sessionEvent(at(5), "s3", ledger.StateSet, ledger.Payload{"args": []any{"ship", "blocked", "judge"}, "ship": "blocked"}),
				hookEvent(at(6), "s3", ledger.RunStarted, ledger.Payload{"skill": "review"}),
			},
			map[string]string{"review": "local: round 1 running (manual)"},
		},
		{
			"a review on another branch",
			[]ledger.Event{func() ledger.Event {
				e := hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "review"})
				e.Branch, e.Checkout = "FT-2/other", "/code/repo.FT-2"

				return e
			}()},
			map[string]string{"review": "local: pending"},
		},
		{
			"a judge by hand after ship",
			[]ledger.Event{hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "judge"})},
			map[string]string{"judge change": "change: running (manual)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gates := taskGates(context.Background(), gateInput{
				task: "FT-1", branch: "FT-1/thing", checkout: "/code/repo.FT-1", run: done,
				events: append(append([]ledger.Event{}, base...), tc.extra...),
				chain:  []string{"ship", "review", "judge"},
				now:    at(9),
			})

			checkTexts(t, gates, tc.want)
		})
	}
}

func TestGatesOutcomes(t *testing.T) {
	ship := &state.Run{
		Session:  "s1",
		Task:     "FT-1",
		Ship:     "done",
		Pipeline: []string{"implement", "review (local)", "pr"},
	}

	for _, tc := range []struct {
		name string
		in   gateInput
		want map[string]string
	}{
		{
			"settled locally, no judge, no remote reviewer",
			gateInput{outcomes: outcomes{local: []reviewRound{{Round: "1", Found: 4, Fixed: 3, Skipped: 1}}}},
			map[string]string{"review": "local: 4 found (3 fixed, 1 skipped)", "judge": "not in this flow"},
		},
		{
			"open findings",
			gateInput{outcomes: outcomes{local: []reviewRound{{Round: "1", Found: 3, Fixed: 1, Ask: 2}}}},
			map[string]string{"review": "local: 2 open of 3 found"},
		},
		{
			"a remote review the host has not handled",
			gateInput{remoteFlow: true, remote: func(context.Context) remoteState {
				return remoteState{found: true, done: true}
			}},
			map[string]string{"review": "local: pending · remote: open (not handled)"},
		},
		{
			"a remote review settled on the host",
			gateInput{remoteFlow: true, remote: func(context.Context) remoteState {
				return remoteState{found: true, done: true, handled: true, settled: true}
			}},
			map[string]string{"review": "local: pending · remote: settled"},
		},
		{
			"a host past the timeout",
			gateInput{remoteFlow: true, remote: func(context.Context) remoteState {
				return remoteState{err: context.DeadlineExceeded}
			}},
			map[string]string{"review": "local: pending · remote: unknown (timed out)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.task, tc.in.run, tc.in.now = "FT-1", ship, time.Now()

			checkTexts(t, taskGates(context.Background(), tc.in), tc.want)
		})
	}

	// a judge the flow does not declare shows once it ran
	gates := taskGates(context.Background(), gateInput{
		task: "FT-1", run: ship, now: time.Now(),
		outcomes: outcomes{planJudge: &judgeOutcome{Verdict: "proceed"}},
	})

	checkTexts(t, gates, map[string]string{"judge plan": "plan: proceed"})

	if _, ok := texts(gates)["judge"]; ok {
		t.Errorf("a ran judge also reads not in this flow: %v", texts(gates))
	}

	// a plan judge before ship and a change judge after it
	gates = taskGates(context.Background(), gateInput{
		task: "FT-1", run: ship, now: time.Now(),
		chain: []string{"plan", "judge", "ship", "review", "resolve", "judge", "finish"},
		outcomes: outcomes{
			planJudge:   &judgeOutcome{Verdict: "proceed", Reason: "sound"},
			changeJudge: &judgeOutcome{Verdict: "rethink", Reason: "symptom"},
		},
		driven: "judge",
	})

	checkTexts(t, gates, map[string]string{
		"judge plan":   "plan: proceed - sound",
		"judge change": "change: rethink - symptom",
		"finish":       "pending",
	})

	if got := compactGates(gates); got != "PJ proceed · L pending · R - · J rethink" {
		t.Errorf("compact = %q", got)
	}
}

// writeLore puts entries straight into a change directory, as `f10 lore
// add` would leave them.
func writeLore(t *testing.T, root string, entries ...lore.Entry) {
	t.Helper()

	for _, e := range entries {
		e.ID = ulid.MustNew(ulid.Timestamp(e.At), ulid.DefaultEntropy()).String()

		raw, err := lore.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}

		writeFile(t, filepath.Join(root, e.Change, e.FileName()), string(raw))
	}
}

func TestOutcomesPreferLore(t *testing.T) {
	storage := t.TempDir()
	writeFile(t, filepath.Join(storage, "reviews", "FT-1", "1.md"),
		"task: FT-1\nround: 1\nreviewer: local\nverdict: findings\n\n## Findings\n\n### 1.1 A\ncategory: x\n")
	writeFile(t, filepath.Join(storage, "verdicts", "FT-1", "1.md"), "task: FT-1\nverdict: stop\nreason: old\n")

	root := t.TempDir()
	store := &lore.Store{Root: root}

	// no lore entries: the files answer
	if o := readOutcomes(
		store,
		storage,
		"FT-1",
	); len(o.local) != 1 || o.changeJudge == nil ||
		o.changeJudge.Verdict != "stop" {
		t.Fatalf("files fallback = %+v", o)
	}

	// lore written before the files is behind them: the files answer
	stale := t.TempDir()
	writeLore(t, stale, lore.Entry{
		Kind: lore.KindFinding, Change: "FT-1", Human: "h@x", At: time.Now().Add(-time.Hour),
		Severity: "minor", Status: "fixed", Resolution: "abcdef1", Source: "local", Body: "old",
	})

	if o := readOutcomes(&lore.Store{Root: stale}, storage, "FT-1"); len(o.local) != 1 || o.local[0].Ask != 1 {
		t.Errorf("stale lore won over the newer file: %+v", o.local)
	}

	when := time.Now().UTC().Truncate(time.Second)
	finding := func(minute int, source, status string) lore.Entry {
		e := lore.Entry{
			Kind: lore.KindFinding, Change: "FT-1", Human: "h@x", At: when.Add(time.Duration(minute) * time.Minute),
			Severity: "minor", Status: status, Source: source, Body: "a finding",
		}
		if status != "open" {
			e.Resolution = "abcdef1"
		}

		return e
	}

	writeLore(t, root,
		finding(1, "local", "fixed"),
		finding(2, "local", "wontfix"),
		finding(3, "remote", "open"),
		lore.Entry{
			Kind: lore.KindVerdict, Change: "FT-1", Human: "h@x", At: when.Add(4 * time.Minute),
			Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Judged: "change", Body: "fine\n\nlonger",
		},
	)

	o := readOutcomes(store, storage, "FT-1")

	if want := (reviewRound{Round: "1", Found: 2, Fixed: 1, Skipped: 1}); len(o.local) != 1 || o.local[0] != want {
		t.Errorf("local from lore = %+v, want %+v", o.local, want)
	}

	if len(o.remote) != 1 || o.remote[0].Ask != 1 {
		t.Errorf("remote from lore = %+v", o.remote)
	}

	if o.changeJudge == nil || o.changeJudge.Verdict != "proceed" || o.changeJudge.Reason != "fine" {
		t.Errorf("change judge from lore = %+v", o.changeJudge)
	}
}

// Lore answers round two only when it holds every finding both rounds
// raised; a reopened prior finding is not a new one.
func TestOutcomesRoundTwo(t *testing.T) {
	storage := t.TempDir()
	reviews := filepath.Join(storage, "reviews", "FT-1")
	writeFile(t, filepath.Join(reviews, "1.md"), "task: FT-1\nround: 1\nreviewer: local\nverdict: findings\n\n"+
		"## Findings\n\n### 1.1 A\ncategory: x\nverdict: fix\n\n### 1.2 B\ncategory: x\nverdict: fix\n")
	writeFile(t, filepath.Join(reviews, "2.md"), "task: FT-1\nround: 2\nreviewer: local\nverdict: findings\n\n"+
		"## Prior round\n\n### 1.1 A - fixed\n\n### 1.2 B - still open\n\n## Findings\n\n### 2.1 C\ncategory: x\n")

	if o := fileOutcomes(storage, "FT-1"); o.localRaised != 3 {
		t.Fatalf("raised = %d, want 3: 1.1, 1.2 and 2.1, the reopened 1.2 once", o.localRaised)
	}

	when := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	entry := func(status string) lore.Entry {
		e := lore.Entry{
			Kind: lore.KindFinding, Change: "FT-1", Human: "h@x", At: when,
			Severity: "minor", Status: status, Source: "local", Body: "f",
		}
		if status != "open" {
			e.Resolution = "abcdef1"
		}

		return e
	}

	// round two's new finding never reached lore: the files answer
	short := t.TempDir()
	writeLore(t, short, entry("fixed"), entry("open"))

	if o := readOutcomes(&lore.Store{Root: short}, storage, "FT-1"); len(o.local) != 2 {
		t.Errorf("lore missing a finding answered: %+v", o.local)
	}

	whole := t.TempDir()
	writeLore(t, whole, entry("fixed"), entry("open"), entry("open"))

	o := readOutcomes(&lore.Store{Root: whole}, storage, "FT-1")
	if want := (reviewRound{Round: "2", Found: 3, Fixed: 1, Ask: 2}); len(o.local) != 1 || o.local[0] != want {
		t.Errorf("lore with every finding = %+v, want %+v", o.local, want)
	}
}

func TestStorageFor(t *testing.T) {
	shared := t.TempDir()
	checkout := t.TempDir()
	writeFile(t, filepath.Join(checkout, ".f10", "verdicts", "FT-1", "1.md"), "task: FT-1\nverdict: proceed\n")

	run := &state.Run{Root: checkout}

	if got := (gateSource{shared: shared}).storageFor(context.Background(), "", run); got != shared {
		t.Errorf("out-of-tree: %q, want %q", got, shared)
	}

	if got := (gateSource{}).storageFor(context.Background(), "", run); got != filepath.Join(checkout, ".f10") {
		t.Errorf("in-repo, checkout on disk: %q", got)
	}

	if got := (gateSource{}).storageFor(
		context.Background(),
		"",
		&state.Run{Root: filepath.Join(checkout, "gone")},
	); got != "" {
		t.Errorf("in-repo, checkout gone: %q", got)
	}
}

func TestRenderGates(t *testing.T) {
	r := &state.Run{Task: "FT-1", Capture: "prior", Plan: "done", Ship: "running", Leaf: "review (local)"}
	gates := []gate{
		{Step: "implement", State: gateDone},
		{Step: "review", Local: &reviewHalf{State: gateRunning, Via: viaShip}, Remote: &reviewHalf{State: gatePending}},
		{Step: "judge", Judged: "change", State: gatePending},
	}

	var out bytes.Buffer
	renderRun(&out, r, gates, false, time.Now(), 0)

	for _, want := range []string{
		"ship            running: review (local)\n",
		"  implement     done\n",
		"  review        local: round 1 running (in ship) · remote: pending\n",
		"  judge         change: pending\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	raw, err := json.Marshal([]runJSON{{Run: r, Gates: gates}, {Run: r}})
	if err != nil {
		t.Fatal(err)
	}

	var back []map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}

	if g, _ := back[0]["gates"].([]any); len(g) != 3 {
		t.Errorf("gates JSON = %s", raw)
	}

	if _, ok := back[1]["gates"]; ok {
		t.Errorf("a run with no gates carries some: %s", raw)
	}

	// a parent's rows carry the compact column, a finished task alone every line
	rows := []taskRow{{ID: "FT-1", Title: "Thing", Stage: stageReviewing, Step: "review", Gates: gates}}

	out.Reset()
	renderRows(&out, rows, 0)

	if !strings.Contains(out.String(), "review  L … · R pending · J pending") {
		t.Errorf("rows lack the gate column:\n%s", out.String())
	}

	out.Reset()
	markdownRows(&out, rows)

	if !strings.Contains(out.String(), "| task | stage | title | step | gates | PR |") ||
		!strings.Contains(out.String(), "| review | L … · R pending · J pending |") {
		t.Errorf("markdown lacks the gate column:\n%s", out.String())
	}

	rows[0].full = true

	out.Reset()
	markdownRows(&out, rows)

	if !strings.Contains(out.String(), "| | | implement: done | | |") {
		t.Errorf("a finished row lacks its gate lines:\n%s", out.String())
	}
}

// A project migrated from pipelines and the default drive chain shows a
// driven task the same gates, in the same order, as before flows.
func TestGatesSameAfterMigration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "project.md"),
		"---\npipelines:\n  default: implement → cleanup → pr → review (local) → judge → pr\n---\n")

	eff := facts.Build(&layout.Layout{Project: "demo", Layers: []layout.Layer{{Label: "main", Dir: dir}}},
		&probe.Probes{})
	flow := eff.Flows["default"]

	if got := strings.Join(flow, " "); got != "plan judge implement cleanup pr review:local judge pr" {
		t.Fatalf("migrated default = %q", got)
	}

	order := func(gates []gate) string {
		var steps []string
		for _, g := range gates {
			steps = append(steps, strings.TrimSpace(g.Step+" "+g.Judged)+"="+g.text())
		}

		return strings.Join(steps, ", ")
	}

	before := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: gatePipeline, now: time.Now(),
		chain: []string{"plan", "judge", "ship", "review", "resolve", "finish"},
	})
	after := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: facts.ShipEntries(flow), now: time.Now(),
		chain: driveChain(shipFlow{}, eff.DriveFinish),
	})

	if order(before) != order(after) {
		t.Errorf("gates drifted across the migration:\nbefore %s\nafter  %s", order(before), order(after))
	}
}

// The judge fetch runs on a stale task leads the gates once it ran or the
// plan phase is on it, and shows nowhere before that.
func TestGatesTaskJudge(t *testing.T) {
	none := taskGates(context.Background(), gateInput{task: "FT-1", pipeline: gatePipeline, now: time.Now()})
	if _, ok := texts(none)["judge task"]; ok {
		t.Errorf("a task judge with nothing behind it: %v", texts(none))
	}

	// ship stopped at its own judge carries the same leaf; plan is done, so
	// it is no task judge
	atShip := taskGates(context.Background(), gateInput{
		task:     "FT-1",
		pipeline: gatePipeline,
		now:      time.Now(),
		run: &state.Run{
			Session:  "s1",
			Task:     "FT-1",
			Plan:     "done",
			Ship:     "blocked",
			Leaf:     "judge",
			Pipeline: gatePipeline,
		},
	})
	if _, ok := texts(atShip)["judge task"]; ok {
		t.Errorf("ship's judge read as the task's: %v", texts(atShip))
	}

	for _, tc := range []struct {
		name string
		in   gateInput
		want string
	}{
		{
			"verdict",
			gateInput{outcomes: outcomes{taskJudge: &judgeOutcome{Verdict: "stop", Reason: "done in FT-9"}}},
			"task: stop - done in FT-9",
		},
		{"running", gateInput{run: &state.Run{Plan: "running", Ship: "pending", Leaf: "judge"}}, "task: running"},
		{"blocked", gateInput{
			run:      &state.Run{Plan: "blocked", Ship: "pending", Leaf: "judge"},
			outcomes: outcomes{taskJudge: &judgeOutcome{Verdict: "rethink"}},
		}, "task: blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.task, tc.in.pipeline, tc.in.now = "FT-1", gatePipeline, time.Now()

			gates := taskGates(context.Background(), tc.in)
			if len(gates) == 0 || gates[0].Judged != "task" || gates[0].text() != tc.want {
				t.Errorf("first gate = %+v, want the task judge reading %q (all: %v)", gates, tc.want, texts(gates))
			}
		})
	}

	gates := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: gatePipeline, now: time.Now(),
		outcomes: outcomes{taskJudge: &judgeOutcome{Verdict: "proceed"}},
	})
	if got := compactGates(gates); got != "TJ proceed · L pending · R - · J pending" {
		t.Errorf("compact = %q", got)
	}
}

// A verdict file on the task itself, or a lore entry judged task, is the
// task judge's, never the change judge's.
func TestOutcomesTaskJudge(t *testing.T) {
	storage := t.TempDir()
	writeFile(t, filepath.Join(storage, "verdicts", "FT-1", "1.md"),
		"task: FT-1\nsubject: task FT-1\nverdict: stop\nreason: solved\nrun: before plan\n")

	o := readOutcomes(nil, storage, "FT-1")
	if o.taskJudge == nil || o.taskJudge.Verdict != "stop" || o.changeJudge != nil {
		t.Errorf("file = %+v", o)
	}

	root := t.TempDir()
	writeLore(t, root, lore.Entry{
		Kind: lore.KindVerdict, Change: "FT-2", Human: "h@x", At: time.Now().UTC().Truncate(time.Second),
		Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Judged: "task", Body: "still real",
	})

	o = readOutcomes(&lore.Store{Root: root}, "", "FT-2")
	if o.taskJudge == nil || o.taskJudge.Verdict != "proceed" || o.changeJudge != nil {
		t.Errorf("lore = %+v", o)
	}

	// a lore verdict newer than the file answers; an older one does not
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Now().Add(time.Hour), "proceed"},
		{time.Now().Add(-time.Hour), "stop"},
	} {
		both := t.TempDir()
		writeLore(t, both, lore.Entry{
			Kind: lore.KindVerdict, Change: "FT-1", Human: "h@x", At: tc.at.UTC().Truncate(time.Second),
			Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Judged: "task", Body: "still real",
		})

		if o := readOutcomes(&lore.Store{Root: both}, storage, "FT-1"); o.taskJudge == nil ||
			o.taskJudge.Verdict != tc.want {
			t.Errorf("lore at %v over the file = %+v, want %s", tc.at, o.taskJudge, tc.want)
		}
	}
}

// Drive's record of running finish turns the finish row from pending to
// queued once ship is done, then running with the action finish is on; a
// base wait stays queued.
func TestGatesDriveFinish(t *testing.T) {
	chain := []string{"plan", "ship", "finish"}
	shipped := &state.Run{Session: "s1", Task: "FT-1", Plan: "done", Ship: "done", Pipeline: gatePipeline}
	working := &state.Run{
		Session: "s1", Task: "FT-1", Plan: "done", Ship: "running", Leaf: "pr", Pipeline: gatePipeline,
	}

	for _, tc := range []struct {
		name   string
		run    *state.Run
		driven string
		finish ref.DriveFinish
		want   string
	}{
		{"no record", shipped, "", ref.DriveFinish{}, gatePending},
		{"ship not done", working, "", ref.DriveFinish{PID: 7}, gatePending},
		{"queued", shipped, "", ref.DriveFinish{PID: 7}, "queued (drive)"},
		{"queued by the recorded chain", nil, "ship", ref.DriveFinish{PID: 7}, "queued (drive)"},
		{"waiting for a base", shipped, "", ref.DriveFinish{PID: 7, Action: "waiting for FT-0"}, "queued (drive): waiting for FT-0"},
		{"merging", shipped, "", ref.DriveFinish{PID: 7, Action: "merging #204"}, "running (drive): merging #204"},
		{"finished", shipped, "finish", ref.DriveFinish{PID: 7, Action: "removing worktree"}, gateDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gates := taskGates(context.Background(), gateInput{
				task: "FT-1", pipeline: gatePipeline, now: time.Now(),
				run: tc.run, chain: chain, driven: tc.driven, finish: tc.finish,
			})

			if got := texts(gates)["finish"]; got != tc.want {
				t.Errorf("finish = %q, want %q", got, tc.want)
			}
		})
	}
}
