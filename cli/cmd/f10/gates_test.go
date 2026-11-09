package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/state"
)

var gatePipeline = []string{"implement", "cleanup", "pr", "review:local", "judge", "pr"}

func hookEvent(at time.Time, session, kind string, p ledger.Payload) ledger.Event {
	e := sessionEvent(at, session, kind, p)
	e.Task, e.Branch, e.Checkout = "", "FT-1/thing", "/code/repo.FT-1"

	return e
}

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
		shipClaim(at(4), "s1", "review:local"),
		// resolve called through the Skill tool inside ship: ship's, not manual
		hookEvent(at(5), "s1", ledger.RunStarted, ledger.Payload{"skill": "resolve", "args": "FT-1"}),
	}

	run := &state.Run{Session: "s1", Task: "FT-1", Ship: "running", Leaf: "review:local", Pipeline: gatePipeline}

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

// A driven ship parked on its base's code shows the wait at the entry that
// found it empty, not a stop.
func TestGatesParkedOnTheBase(t *testing.T) {
	run := &state.Run{
		Session:  "s1",
		Task:     "FT-2",
		Ship:     "blocked",
		Leaf:     "implement",
		Wait:     "FT-1",
		Pipeline: gatePipeline,
	}

	gates := taskGates(context.Background(), gateInput{task: "FT-2", run: run, now: time.Unix(1_800_000_000, 0)})

	checkTexts(t, gates, map[string]string{"implement": "waiting on FT-1", "cleanup": "pending"})
}

// An optional CI review with no remote reviewer declared is dropped, so
// status shows the local review alone; with one declared, both.
func TestGatesOptionalCI(t *testing.T) {
	pipeline := []string{"implement", "pr", "review:local+ci?", "pr"}
	now := time.Unix(1_800_000_000, 0)

	alone := taskGates(context.Background(), gateInput{task: "FT-1", pipeline: flow.Of(pipeline), now: now})
	checkTexts(t, alone, map[string]string{"review": "local: pending"})

	both := taskGates(
		context.Background(),
		gateInput{task: "FT-1", pipeline: flow.Of(pipeline), remoteFlow: true, now: now},
	)
	checkTexts(t, both, map[string]string{"review": "local: pending · remote: pending"})

	// a flow whose only review is a dropped ci? keeps one review gate, off
	ciOnly := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: flow.Of([]string{"implement", "pr", "review:ci?"}), now: now,
	})

	var reviews []gate
	for _, g := range ciOnly {
		if g.Step == "review" {
			reviews = append(reviews, g)
		}
	}

	if len(reviews) != 1 || reviews[0].State != gateOff {
		t.Errorf("review gates = %+v", reviews)
	}
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
		task: "FT-1", pipeline: flow.Of(pipeline), events: events[:3], now: at(3),
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
		"review": "local: round 1 in, waiting for the other review (in ship) · remote: round 1 running (in ship)",
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

	// the flow declares review and judge after pr, which ship's claims never reached
	done := &state.Run{
		Session:  "s1",
		Task:     "FT-1",
		Ship:     "done",
		Pipeline: []string{"implement", "pr", "review", "judge"},
	}
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
			"a host engine review is the local half",
			[]ledger.Event{hookEvent(at(4), "s2", ledger.RunStarted, ledger.Payload{"skill": "review", "args": "FT-1 host"})},
			map[string]string{"review": "local: round 1 running (manual)"},
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
		Pipeline: []string{"implement", "review:local", "pr"},
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
		task: "FT-1",
		now:  time.Now(),
		run: &state.Run{
			Session:  "s1",
			Task:     "FT-1",
			Ship:     "done",
			Pipeline: []string{"judge", "implement", "review:local", "pr", "judge"},
		},
		finishes: true,
		outcomes: outcomes{
			planJudge:   &judgeOutcome{Verdict: "proceed", Reason: "sound"},
			changeJudge: &judgeOutcome{Verdict: "rethink", Reason: "symptom"},
		},
	})

	checkTexts(t, gates, map[string]string{
		"judge plan":   "plan: proceed - sound",
		"judge change": "change: rethink - symptom",
		"finish":       "pending",
	})

	if got := compactGates(gates); got != "󰊛 PJ proceed · 󰚔 L pending · R - · 󰊛 J rethink" {
		t.Errorf("compact = %q", got)
	}

	t.Setenv("F10_STATUS_GLYPHS", "PR MR rv jd")

	if got := compactGates(gates); got != "jd PJ proceed · rv L pending · R - · jd J rethink" {
		t.Errorf("with F10_STATUS_GLYPHS: compactGates = %q", got)
	}
}

// writeLore writes entries as `f10 lore add` would leave them.
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

	if o := fileOutcomes(storage, "FT-1", time.Time{}); o.localRaised != 3 {
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

	if got := (gateSource{shared: shared}).storageFor(context.Background(), ""); got != shared {
		t.Errorf("no checkout: %q, want %q", got, shared)
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
	renderRun(&out, r, gates, false, "", time.Now(), 0)

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

	if err := renderRows(&out, rows, rowView{}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "review  󰚔 L … · R pending · 󰊛 J pending") {
		t.Errorf("rows lack the gate column:\n%s", out.String())
	}

	rows[0].full = true

	out.Reset()

	if err := renderRows(&out, rows, rowView{}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "implement  done\n") || strings.Contains(out.String(), "L … ·") {
		t.Errorf("a finished row lacks its gate lines:\n%s", out.String())
	}
}

// The judge fetch runs on a stale task leads the gates once it ran or the
// plan phase is on it, and shows nowhere before that.
func TestGatesTaskJudge(t *testing.T) {
	none := taskGates(context.Background(), gateInput{task: "FT-1", pipeline: flow.Of(gatePipeline), now: time.Now()})
	if _, ok := texts(none)["judge task"]; ok {
		t.Errorf("a task judge with nothing behind it: %v", texts(none))
	}

	// ship stopped at its own judge carries the same leaf; plan is done, so
	// it is no task judge
	atShip := taskGates(context.Background(), gateInput{
		task:     "FT-1",
		pipeline: flow.Of(gatePipeline),
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
			tc.in.task, tc.in.pipeline, tc.in.now = "FT-1", flow.Of(gatePipeline), time.Now()

			gates := taskGates(context.Background(), tc.in)
			if len(gates) == 0 || gates[0].Judged != "task" || gates[0].text() != tc.want {
				t.Errorf("first gate = %+v, want the task judge reading %q (all: %v)", gates, tc.want, texts(gates))
			}
		})
	}

	gates := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: flow.Of(gatePipeline), now: time.Now(),
		outcomes: outcomes{taskJudge: &judgeOutcome{Verdict: "proceed"}},
	})
	if got := compactGates(gates); got != "󰊛 TJ proceed · 󰚔 L pending · R - · 󰊛 J pending" {
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

func queued(action string) branchmeta.DriveFinish {
	return branchmeta.DriveFinish{State: branchmeta.FinishQueued, Action: action}
}

func running(action string) branchmeta.DriveFinish {
	return branchmeta.DriveFinish{State: branchmeta.FinishRunning, Action: action}
}

// Drive's record of running finish turns the finish row from pending to
// queued once ship is done, then running with the action finish is on; a
// base wait stays queued.
func TestGatesDriveFinish(t *testing.T) {
	shipped := &state.Run{Session: "s1", Task: "FT-1", Plan: "done", Ship: "done", Pipeline: gatePipeline}
	working := &state.Run{
		Session: "s1", Task: "FT-1", Plan: "done", Ship: "running", Leaf: "pr", Pipeline: gatePipeline,
	}

	for _, tc := range []struct {
		name   string
		run    *state.Run
		driven string
		finish branchmeta.DriveFinish
		want   string
	}{
		{"no record", shipped, "", branchmeta.DriveFinish{}, gatePending},
		{"ship not done", working, "", queued(""), gatePending},
		{"queued", shipped, "", queued(""), "queued (drive)"},
		{"queued once drive recorded ship", nil, "ship", queued(""), "queued (drive)"},
		{"waiting for a base", shipped, "", queued("waiting for FT-0"), "queued (drive): waiting for FT-0"},
		{"catching up", shipped, "", queued("catching up with main"), "queued (drive): catching up with main"},
		{"merging", shipped, "", running("merging #204"), "running (drive): merging #204"},
		{"finished", shipped, "finish", running("removing worktree"), gateDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gates := taskGates(context.Background(), gateInput{
				task: "FT-1", pipeline: flow.Of(gatePipeline), now: time.Now(),
				run: tc.run, finishes: true, driven: tc.driven, finish: tc.finish,
			})

			if got := texts(gates)["finish"]; got != tc.want {
				t.Errorf("finish = %q, want %q", got, tc.want)
			}
		})
	}
}

// A branch with no finish record has no finish row: drive does not finish
// it, or no drive opened it.
func TestGatesNoFinishRecord(t *testing.T) {
	shipped := &state.Run{Session: "s1", Task: "FT-1", Plan: "done", Ship: "done", Pipeline: gatePipeline}
	gates := taskGates(context.Background(), gateInput{task: "FT-1", run: shipped, driven: "ship", now: time.Now()})

	if got, ok := texts(gates)["finish"]; ok {
		t.Errorf("finish = %q, want no row", got)
	}
}

// A flow with no review or judge leaves both out of the compact row, while
// the full listing still names them as not in this flow.
func TestCompactGatesOmitsStepsNotInTheFlow(t *testing.T) {
	gates := taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: flow.Of([]string{"implement", "pr"}), now: time.Now(),
	})

	if got := compactGates(gates); got != "" {
		t.Errorf("compact = %q, want none", got)
	}

	checkTexts(t, gates, map[string]string{"review": gateOff, "judge": gateOff})
}

// driveFlows reads drives neither cancelled nor done, the newest first, by
// each task's lead id, and knows which driver still runs.
func TestDriveFlows(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	for _, rec := range []*drives.Record{
		{Flow: "old", Tasks: []drives.Task{{ID: "FT-1"}, {ID: "FT-4"}}},
		{Flow: "gone", Cancelled: &now, Tasks: []drives.Task{{ID: "FT-5"}}},
		{Flow: "done", Tasks: []drives.Task{{ID: "FT-6", Outcome: &drives.Outcome{Row: "ok"}}}},
		{Flow: "prose", Tasks: []drives.Task{{ID: "FT-1_v2"}, {ID: "FT-2+FT-3"}}},
		{Tasks: []drives.Task{{ID: "FT-4"}}},
	} {
		if err := drives.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	lease, err := drives.Acquire(root, drives.DriveKey(4), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	got := driveFlows(root)
	want := map[string]driveFlow{
		"FT-1": {name: "prose", running: true},
		"FT-2": {name: "prose", running: true},
		"FT-4": {name: "default"},
	}

	if len(got) != len(want) {
		t.Errorf("flows = %v, want %v", got, want)
	}

	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %+v, want %+v", id, got[id], w)
		}
	}
}

// A follow-up PR shows only its own series: the first PR's lore verdict and
// its verdict file stay out of its gates, while a finished task with no
// branch still shows the series it ended with.
func TestStatusReadsTheCurrentSeries(t *testing.T) {
	storage := t.TempDir()
	writeFile(t, filepath.Join(storage, "verdicts", "FT-1", "1.md"),
		"task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: first PR\n")

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(storage, "verdicts", "FT-1", "1.md"), old, old); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	store := &lore.Store{Root: root}
	writeLore(t, root, lore.Entry{
		Kind: lore.KindVerdict, Change: "FT-1", Human: "h@x", At: old,
		Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Judged: "change", Body: "first PR",
	})

	if _, err := closeSeries(filepath.Join(storage, "reviews", "FT-1"), seriesEnd{pr: "#7"}); err != nil {
		t.Fatal(err)
	}

	if o := seriesOutcomes(store, storage, "FT-1", true); o.changeJudge != nil || o.planJudge != nil {
		t.Errorf("the follow-up shows the first PR's verdicts: %+v", o)
	}

	if o := seriesOutcomes(store, storage, "FT-1", false); o.changeJudge == nil || o.changeJudge.Reason != "first PR" {
		t.Errorf("the finished task lost its last series: %+v", o)
	}
}

// The judge row says what the run did with the verdict, read from the
// verdict file's decision line; a file without one renders as before.
func TestJudgeDecisionText(t *testing.T) {
	for _, tc := range []struct {
		decision string
		changes  int
		want     string
	}{
		{"", 0, "proceed with changes - why"},
		{"continue", 0, "proceed with changes - why"},
		{"built", 2, "proceed with changes (2 built) - why"},
		{"built", 0, "proceed with changes (built) - why"},
		{"refactor as agreed", 0, "proceed with changes (refactor built) - why"},
		{"folded", 0, "proceed with changes (folded into plan) - why"},
		{"block", 0, "proceed with changes (blocked) - why"},
		{"stop", 0, "proceed with changes (blocked) - why"},
		{"asked", 0, "proceed with changes (waiting on an answer) - why"},
		{"overridden", 0, "proceed with changes (overridden by the user) - why"},
		{"something new", 0, "proceed with changes (something new) - why"},
	} {
		g := gate{Step: "judge", Judged: "change", State: gateDone, Outcome: &judgeOutcome{
			Verdict: "proceed with changes", Reason: "why", Decision: tc.decision, Changes: tc.changes,
		}}

		if got := g.stateText(); got != tc.want {
			t.Errorf("decision %q: got %q, want %q", tc.decision, got, tc.want)
		}

		if got := g.compactState(); got != "proceed with changes" {
			t.Errorf("decision %q: compact %q, want the bare verdict", tc.decision, got)
		}
	}
}

// Lore carries no decision, so a lore verdict keeps the file's decision
// when the two agree on the verdict.
func TestOutcomesKeepTheFileDecision(t *testing.T) {
	storage := t.TempDir()
	writeFile(
		t,
		filepath.Join(storage, "verdicts", "FT-1", "1.md"),
		"task: FT-1\nsubject: branch x\nverdict: proceed with changes\nreason: file\ndecision: built\n\n## Changes\n\n1. A.\n2. B.\n",
	)

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(storage, "verdicts", "FT-1", "1.md"), past, past); err != nil {
		t.Fatal(err)
	}

	verdictEntry := func(outcome string) lore.Entry {
		return lore.Entry{
			Kind: lore.KindVerdict, Change: "FT-1", Human: "h@x", At: time.Now().UTC().Truncate(time.Second),
			Outcome: outcome, Gate: "judge", Commit: "abcdef1", Judged: "change", Body: "from lore",
		}
	}

	same := t.TempDir()
	writeLore(t, same, verdictEntry("proceed with changes"))

	j := readOutcomes(&lore.Store{Root: same}, storage, "FT-1").changeJudge
	if j == nil || j.Reason != "from lore" || j.Decision != "built" || j.Changes != 2 {
		t.Errorf("lore verdict lost the file's decision: %+v", j)
	}

	other := t.TempDir()
	writeLore(t, other, verdictEntry("proceed"))

	j = readOutcomes(&lore.Store{Root: other}, storage, "FT-1").changeJudge
	if j == nil || j.Verdict != "proceed" || j.Decision != "" {
		t.Errorf("a different lore verdict took the file's decision: %+v", j)
	}
}

// A labelled entry shows by its label, in the gates and their JSON, while
// the claims still match it as the bare step it runs.
func TestGatesShowEntryLabels(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	events := []ledger.Event{
		hookEvent(at(0), "s1", ledger.RunStarted, ledger.Payload{"skill": "ship", "args": "FT-1"}),
		shipClaim(at(1), "s1", "implement"),
		shipClaim(at(2), "s1", "pr"),
		shipClaim(at(3), "s1", "review:local"),
		shipClaim(at(4), "s1", "pr[pr-polish]"),
	}

	run := &state.Run{
		Session: "s1", Task: "FT-1", Ship: "running", Leaf: "pr", At: 4,
		Pipeline: []string{"implement", "pr", "review:local", "pr"}, Labels: []string{"", "", "", "pr-polish"},
	}

	gates := taskGates(context.Background(), gateInput{task: "FT-1", run: run, events: events, now: at(5)})

	var polish *gate

	for i, g := range gates {
		if g.Label == "pr-polish" {
			polish = &gates[i]
		}
	}

	if polish == nil || polish.Step != "pr" || polish.State != "running" || polish.shown() != "pr-polish" {
		t.Fatalf("gates = %+v, want the second pr running and shown as pr-polish", gates)
	}

	if b, _ := json.Marshal(polish); !strings.Contains(string(b), `"step":"pr","label":"pr-polish"`) {
		t.Errorf("json = %s", b)
	}

	if rows := gateLines(gates); !slices.ContainsFunc(rows, func(r [2]string) bool { return r[0] == "  pr-polish" }) {
		t.Errorf("rows = %v, want a pr-polish row", rows)
	}

	// with no run yet, the branch's flow gives the labels
	gates = taskGates(context.Background(), gateInput{
		task: "FT-1", pipeline: run.Entries(), now: at(5),
	})
	if i := slices.IndexFunc(gates, func(g gate) bool { return g.shown() == "pr-polish" }); i < 0 ||
		gates[i].State != gatePending {
		t.Errorf("gates = %+v, want pr-polish pending", gates)
	}
}

// A labelled review claim still marks when ship entered the review, so a
// round written before it does not read as this review's.
func TestClaimedAtCutsTheLabel(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	in := gateInput{events: []ledger.Event{shipClaim(at, "s1", "review:local[review-final]")}}

	if got := in.claimedAt("s1", "review:local"); !got.Equal(at) {
		t.Errorf("claimedAt = %v, want %v", got, at)
	}
}
