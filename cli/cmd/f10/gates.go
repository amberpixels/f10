package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/timing"
)

// A task's gates are every step its flow declares with its state, read from
// the ledger, lore or the findings and verdict files, and the host.

// Gate states. A plain step also takes a run's stop status (failed,
// blocked, partial) on the step it stopped at.
const (
	gateOff      = "not in this flow"
	gateUpcoming = state.Upcoming
	gateRunning  = "running"
	gateQueued   = "queued"
	gateWaiting  = "waiting" // a driven ship parked on its base's code
	gateDone     = "done"
	gateSkipped  = "skipped"
	gateOpen     = "open"
	gateSettled  = "settled"
	gateUnknown  = "unknown"
)

// How a running gate was started.
const (
	viaShip   = "in ship"
	viaDriven = "driven"
	viaManual = "manual"
	viaDrive  = "drive" // finish, which drive runs in its own process
	viaHost   = "on host"
)

// remoteTimeout bounds the host read for the remote review: past it the
// half is unknown and the rest of the status still prints.
var remoteTimeout = 5 * time.Second

// A gate is one row of a task's flow: a plain step, its review or a judge.
type gate struct {
	Step    string        `json:"step"`
	Label   string        `json:"label,omitempty"`  // the entry's display name, shown in place of Step
	State   string        `json:"state,omitempty"`  // plain steps and judges
	Via     string        `json:"via,omitempty"`    // a running gate: in ship, driven or manual
	Action  string        `json:"action,omitempty"` // drive's finish: what it is doing
	Judged  string        `json:"judged,omitempty"` // a judge: task, plan or change
	Outcome *judgeOutcome `json:"outcome,omitempty"`
	Local   *reviewHalf   `json:"local,omitempty"`
	Remote  *reviewHalf   `json:"remote,omitempty"`
}

type reviewHalf struct {
	State  string        `json:"state"`
	Via    string        `json:"via,omitempty"`
	Action string        `json:"action,omitempty"` // running: review, or resolve settling it
	Detail string        `json:"detail,omitempty"` // unknown: why
	Rounds []reviewRound `json:"rounds,omitempty"`
}

type remoteState struct {
	found, done, handled, settled bool
	err                           error
}

type gateInput struct {
	task, branch, checkout string

	run      *state.Run // live, or the ledger's last claim; nil for none
	events   []ledger.Event
	driven   string                 // the last skill drive saw finish: plan, ship or finish
	finishes bool                   // drive finishes the branch: its finish record is there
	finish   branchmeta.DriveFinish // drive's record of running finish, zero for none or a drive gone
	pipeline flow.Flow              // ship's entries of the branch's flow, when the run recorded none
	// remoteFlow is a remote reviewer declared: it reviews every PR
	remoteFlow bool

	outcomes outcomes
	remote   func(ctx context.Context) remoteState // nil: no host to ask
	now      time.Time
}

// taskGates builds the gates, nil when nothing declares a flow.
func taskGates(ctx context.Context, in gateInput) []gate {
	entries := in.flow()
	if len(entries) == 0 {
		return nil
	}

	steps := in.stepStates(entries)

	var (
		gates               []gate
		review              = -1
		judges              = map[string]int{}
		local, remote, isPR bool
	)

	for i, e := range entries {
		switch e.Step {
		case "review":
			// review:local+ci is one entry running both reviews
			by := in.reviewBy(entries, i)
			local, remote = local || by.Local, remote || by.CI

			if review < 0 {
				review = len(gates)
				gates = append(gates, gate{Step: "review"})
			}
		case "judge":
			judged := entries.JudgeKind(i)

			if _, ok := judges[judged]; !ok {
				judges[judged] = len(gates)
				gates = append(gates, gate{Step: "judge", Judged: judged})
			}
		case "finish":
			gates = append(gates, in.finishGate(steps[i]))
		default:
			if e.Step == "pr" {
				isPR = true
			}

			g := gate{Step: e.String(), Label: e.Label, State: steps[i]}
			if g.State == gateWaiting {
				g.Action = "on " + in.run.Wait
			}

			gates = append(gates, g)
		}
	}

	remote = remote || (in.remoteFlow && isPR)

	lh, rh := in.half(entries, steps, false, local), in.half(entries, steps, true, remote)
	if remote && rh.State != gateSettled && rh.State != gateRunning && in.remote != nil {
		in.hostHalf(ctx, rh)
	}

	switch {
	case lh == nil && rh == nil && review >= 0:
		// every review entry was a dropped ci?
		gates[review].State = gateOff
	case lh == nil && rh == nil:
		gates = append(gates, gate{Step: "review", State: gateOff})
	case review < 0:
		gates = append(gates, gate{Step: "review", Local: lh, Remote: rh})
	default:
		gates[review].Local, gates[review].Remote = lh, rh
	}

	for judged, i := range judges {
		in.judge(&gates[i], judged, entries, steps)
	}

	// the stale task's judge is in no flow: it leads once it ran or the run is on it
	if g, ok := in.taskJudge(); ok {
		gates = append([]gate{g}, gates...)
	}

	// a judge the flow does not declare shows once something ran it
	extra := false

	for _, judged := range []string{"plan", "change"} {
		if _, ok := judges[judged]; ok {
			continue
		}

		g := gate{Step: "judge", Judged: judged}
		in.judge(&g, judged, entries, steps)

		if g.State != gateUpcoming {
			gates, extra = append(gates, g), true
		}
	}

	if len(judges) == 0 && !extra {
		gates = append(gates, gate{Step: "judge", State: gateOff})
	}

	return gates
}

// flow is the entries the task runs through: ship's, then finish when
// drive finishes the branch. plan and resolve are no gates: the plan phase
// row shows the one, a review gate folds in the other.
func (in gateInput) flow() flow.Flow {
	entries := in.pipeline
	if in.run != nil && len(in.run.Pipeline) > 0 {
		entries = in.run.Entries()
	}

	if in.finishes {
		entries = append(slices.Clip(entries), flow.Entry{Step: "finish"})
	}

	return entries
}

// stepStates follows the claims ship's session made: steps before the one
// it is on are done, that one has the run's status, the rest are upcoming.
// finish is done once drive recorded it.
func (in gateInput) stepStates(entries flow.Flow) []string {
	states := make([]string, len(entries))
	for i := range states {
		states[i] = gateUpcoming
	}

	shipLen := len(entries)
	if in.finishes {
		shipLen--

		if in.driven == "finish" {
			states[shipLen] = gateDone
		}
	}

	r := in.run
	if r == nil || shipLen == 0 || r.Ship == state.Upcoming || r.Ship == "" || r.Ship == "skipped" {
		return states
	}

	ship := entries[:shipLen].Strings()
	at, j := -1, 0

	for _, step := range shipSteps(in.events, r.Session, in.now) {
		if k := slices.Index(ship[j:], step); k >= 0 {
			at, j = j+k, j+k+1
		}
	}

	if at < 0 && r.Leaf != "" {
		at = slices.Index(ship, r.Leaf)
	}

	for k := range ship {
		var s string

		switch {
		case r.Ship == "done" && (at < 0 || k <= at):
			s = gateDone
		case r.Ship == "done":
			s = gateSkipped
		case k < at:
			s = gateDone
		case k == at && r.Wait != "" && state.Stopped(r.Ship):
			s = gateWaiting
		case k == at:
			s = r.Ship
		default:
			s = gateUpcoming
		}

		states[k] = s
	}

	return states
}

func (in gateInput) finishGate(st string) gate {
	g := gate{Step: "finish", State: st}
	if st != gateUpcoming || in.finish.State == "" || !in.shipDone() {
		return g
	}

	g.Via, g.Action = viaDrive, in.finish.Action

	g.State = gateQueued
	if in.finish.Running() {
		g.State = gateRunning
	}

	return g
}

func (in gateInput) shipDone() bool {
	if in.run != nil && in.run.Ship == "done" {
		return true
	}

	return in.driven == "ship" || in.driven == "finish"
}

// reviewBy is who the review entry at i runs, an optional CI review
// settled as the flow resolution settles it.
func (in gateInput) reviewBy(entries flow.Flow, i int) facts.By {
	by, _ := facts.ReviewBy(entries[i].Arg)
	by, _ = by.Settle(in.remoteFlow, entries.PublishedBefore(i))

	return by
}

func (in gateInput) half(entries flow.Flow, steps []string, remote, inFlow bool) *reviewHalf {
	h := in.readHalf(entries, steps, remote)

	// a half the flow does not declare shows once something ran it
	if !inFlow && h.State == gateUpcoming {
		return nil
	}

	return h
}

func (in gateInput) readHalf(entries flow.Flow, steps []string, remote bool) *reviewHalf {
	rounds, last := in.outcomes.local, in.outcomes.localAt
	if remote {
		rounds, last = in.outcomes.remote, in.outcomes.remoteAt
	}

	h := &reviewHalf{State: gateUpcoming, Rounds: rounds}

	if len(rounds) > 0 {
		h.State = gateSettled
		if rounds[len(rounds)-1].Ask > 0 {
			h.State = gateOpen
		}
	}

	// ship on a review entry running this review, alone or beside the other
	for i, e := range entries {
		by := in.reviewBy(entries, i)
		l, r := by.Local, by.CI

		if e.Step != "review" || (remote && !r) || (!remote && !l) {
			continue
		}

		switch s := steps[i]; {
		case s == gateRunning:
			h.State, h.Via, h.Action = gateRunning, viaShip, "review"

			// a round written since ship entered the review is being resolved;
			// with both halves in the entry, only once both have landed
			if in.run != nil {
				since := in.claimedAt(in.run.Session, e.String())

				own, other := last.After(since), true
				if l && r {
					other = in.outcomes.localAt.After(since) && in.outcomes.remoteAt.After(since)
				}

				switch {
				case own && other:
					h.Action = "resolve"
				case own:
					h.Action = "await"
				}
			}

			return h
		case state.Stopped(s):
			h.State = s

			return h
		}
	}

	for _, skill := range []string{"resolve", "review"} {
		if m := in.started(skill, remote, last); m != nil {
			h.State, h.Via, h.Action = gateRunning, m.via, skill

			return h
		}
	}

	return h
}

func (in gateInput) hostHalf(ctx context.Context, h *reviewHalf) {
	rs := in.remote(ctx)

	switch {
	case errors.Is(rs.err, context.DeadlineExceeded):
		h.State, h.Detail = gateUnknown, "timed out"
	case rs.err != nil:
		h.State, h.Detail = gateUnknown, firstErrLine(rs.err)
	case !rs.found || h.State == gateRunning:
	case !rs.done:
		h.State, h.Via = gateRunning, viaHost
	case rs.settled:
		h.State = gateSettled
	case !rs.handled:
		h.State = gateOpen
	case h.State == gateUpcoming:
		h.State = gateSettled
	}
}

func (in gateInput) judge(g *gate, judged string, entries flow.Flow, steps []string) {
	out, last := in.outcomes.changeJudge, in.outcomes.changeAt
	if judged == "plan" {
		out, last = in.outcomes.planJudge, in.outcomes.planAt
	}

	g.State = gateUpcoming
	if out != nil {
		g.State, g.Outcome = gateDone, out
	}

	for i, e := range entries {
		if e.Step != "judge" {
			continue
		}

		switch s := steps[i]; {
		case s == gateRunning && judged == "change":
			g.State, g.Via = gateRunning, viaShip

			return
		case state.Stopped(s) && judged == "change":
			g.State = s

			return
		}
	}

	// a judge run outside ship judges the plan until ship starts
	shipStarted := in.run != nil && in.run.Ship != state.Upcoming && in.run.Ship != ""
	if (judged == "plan") == shipStarted {
		return
	}

	if m := in.started("judge", false, last); m != nil {
		g.State, g.Via, g.Outcome = gateRunning, m.via, nil
	}
}

// taskJudge is the judge fetch runs on a task older than stale_after, shown
// only once it ran or the plan phase is on it.
func (in gateInput) taskJudge() (gate, bool) {
	g := gate{Step: "judge", Judged: "task"}

	switch r := in.run; {
	case r != nil && r.Leaf == "judge" && (r.Plan == gateRunning || state.Stopped(r.Plan)):
		g.State = r.Plan
	case in.outcomes.taskJudge != nil:
		g.State, g.Outcome = gateDone, in.outcomes.taskJudge
	default:
		return gate{}, false
	}

	return g, true
}

func (in gateInput) claimedAt(session, step string) time.Time {
	var at time.Time

	for _, e := range in.events {
		if e.Session != session || e.Kind != ledger.StateSet {
			continue
		}

		if phase, status, leaf := timing.Claim(e); phase == "ship" && status == "running" && leaf == step {
			at = e.At
		}
	}

	return at
}

type startMark struct {
	at      time.Time
	session string
	via     string
}

// started is the newest call of skill on the task's branch or checkout after
// since, not a ship run's, whose session has not stopped or moved on and is
// younger than the state TTL, since Stop writes no stop for a session that
// ran no phase; `ci` makes a review the remote half's.
func (in gateInput) started(skill string, remote bool, since time.Time) *startMark {
	var (
		mark *startMark
		// a session's ship run is on until a claim takes ship off running;
		// ship calls only resolve through the Skill tool, never review or judge
		inShip = map[string]bool{}
	)

	for _, e := range in.events {
		switch e.Kind {
		case ledger.RunStarted:
			s := e.Skill()

			if mark != nil && e.Session == mark.session && e.At.After(mark.at) {
				mark = nil
			}

			if slices.Contains(state.Phases, s) {
				inShip[e.Session] = s == "ship"

				continue
			}

			if s != skill || (s == "resolve" && inShip[e.Session]) || !in.onTask(e) || !e.At.After(since) {
				continue
			}

			args := strings.Fields(e.Payload.Val.String("args"))
			if slices.Contains(args, "ci") != remote {
				continue
			}

			via := viaManual
			if slices.Contains(args, "--driven") {
				via = viaDriven
			}

			mark = &startMark{at: e.At, session: e.Session, via: via}
		case ledger.StateSet:
			// blocked is a driven ship waiting to resume, not its end
			switch e.Payload.Val.String("ship") {
			case "done", "failed", "partial":
				inShip[e.Session] = false
			}
		case ledger.SessionStopped:
			if mark != nil && e.Session == mark.session && e.At.After(mark.at) {
				mark = nil
			}
		}
	}

	if mark != nil && in.now.Sub(mark.at) > state.TTL() {
		return nil
	}

	return mark
}

// onTask: the hook writes run_started before any fetch, with no task, so the
// branch and the checkout tie a row to the task.
func (in gateInput) onTask(e ledger.Event) bool {
	return (in.branch != "" && e.Branch == in.branch) ||
		(in.checkout != "" && e.Checkout != "" && gitx.Resolved(e.Checkout) == gitx.Resolved(in.checkout))
}
