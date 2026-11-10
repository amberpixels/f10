package statusview

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/timing"
)

// A Gate is one row of a task's flow: a plain step, its review or a judge.
type Gate struct {
	Step    string                `json:"step"`
	Label   string                `json:"label,omitempty"`  // the entry's display name, shown in place of Step
	State   string                `json:"state,omitempty"`  // plain steps and judges
	Via     string                `json:"via,omitempty"`    // a running gate: in ship, driven or manual
	Action  string                `json:"action,omitempty"` // drive's finish: what it is doing
	Judged  string                `json:"judged,omitempty"` // a judge: task, plan or change
	Outcome *archive.JudgeOutcome `json:"outcome,omitempty"`
	Local   *ReviewHalf           `json:"local,omitempty"`
	Remote  *ReviewHalf           `json:"remote,omitempty"`
}

// Shown is the gate's name as a person reads it: its label, else its step.
func (g Gate) Shown() string { return cmp.Or(g.Label, g.Step) }

func (g Gate) Text() string {
	switch {
	case g.Local != nil || g.Remote != nil:
		var parts []string

		if g.Local != nil {
			parts = append(parts, "local: "+g.Local.text())
		}

		if g.Remote != nil {
			parts = append(parts, "remote: "+g.Remote.text())
		}

		return strings.Join(parts, " · ")
	case g.Judged != "":
		return g.Judged + ": " + g.StateText()
	}

	return g.StateText()
}

func (g Gate) StateText() string {
	switch {
	case (g.State == GateRunning || g.State == gateQueued) && g.Via != "" && g.Action != "":
		return g.State + " (" + g.Via + "): " + g.Action
	case (g.State == GateRunning || g.State == gateQueued) && g.Via != "":
		return g.State + " (" + g.Via + ")"
	case g.State == gateWaiting && g.Action != "":
		return g.State + " " + g.Action
	case g.State == GateDone && g.Outcome != nil:
		text := g.Outcome.Verdict
		if done := g.Outcome.Done(); done != "" {
			text += " (" + done + ")"
		}

		if g.Outcome.Reason != "" {
			text += " - " + g.Outcome.Reason
		}

		return text
	}

	return g.State
}

func (g Gate) CompactState() string {
	switch g.State {
	case GateRunning:
		return "…"
	case GateDone:
		if g.Outcome != nil {
			return g.Outcome.Verdict
		}
	}

	return g.State
}

// style is the badge status whose palette the gate prints in.
func (g Gate) style() string {
	states := []string{g.State}
	if g.Local != nil || g.Remote != nil {
		states = nil

		for _, h := range []*ReviewHalf{g.Local, g.Remote} {
			if h != nil {
				states = append(states, h.State)
			}
		}
	}

	switch {
	case slices.Contains(states, GateRunning):
		return "running"
	case slices.ContainsFunc(states, state.Stopped):
		return "failed"
	case slices.Contains(states, gateUnknown):
		return "blocked"
	case slices.Contains(states, gateOpen):
		return "partial"
	case !slices.ContainsFunc(states, func(s string) bool { return s != GateDone && s != gateSettled }):
		return "done"
	}

	return state.Upcoming
}

type ReviewHalf struct {
	State  string        `json:"state"`
	Via    string        `json:"via,omitempty"`
	Action string        `json:"action,omitempty"` // running: review, or resolve settling it
	Detail string        `json:"detail,omitempty"` // unknown: why
	Rounds []ReviewRound `json:"rounds,omitempty"`
}

func (h *ReviewHalf) text() string {
	switch h.State {
	case GateRunning:
		what := fmt.Sprintf("round %d running", len(h.Rounds)+1)

		switch h.Action {
		case "resolve":
			what = "resolve running"
		case "await":
			// review:local+ci: this review's round is in, the other's is not
			what = fmt.Sprintf("round %d in, waiting for the other review", len(h.Rounds))
		}

		if h.Via != "" {
			what += " (" + h.Via + ")"
		}

		return what
	case gateUnknown:
		return "unknown (" + cmp.Or(h.Detail, "no answer") + ")"
	case gateOpen:
		if len(h.Rounds) == 0 {
			return "open (not handled)"
		}

		r := h.Rounds[len(h.Rounds)-1]

		return fmt.Sprintf("%d open of %d found", r.Ask, r.Found)
	case gateSettled:
		if len(h.Rounds) == 0 {
			return gateSettled
		}

		return reviewText(h.Rounds)
	}

	return h.State
}

func (h *ReviewHalf) compact() string {
	if h == nil {
		return "-"
	}

	switch h.State {
	case GateRunning:
		return "…"
	case gateUnknown:
		return "?"
	case gateSettled:
		if len(h.Rounds) == 0 {
			return "ok"
		}

		r := h.Rounds[len(h.Rounds)-1]
		if r.Found == 0 {
			return "clean"
		}

		return fmt.Sprintf("%d/%d", r.Fixed, r.Found)
	case gateOpen:
		if len(h.Rounds) == 0 {
			return gateOpen
		}

		return fmt.Sprintf("%d open", h.Rounds[len(h.Rounds)-1].Ask)
	}

	return h.State
}

// Gate states. A plain step also takes a run's stop status (failed,
// blocked, partial) on the step it stopped at.
const (
	GateOff      = "not in this flow"
	GateUpcoming = state.Upcoming
	GateRunning  = "running"
	gateQueued   = "queued"
	gateWaiting  = "waiting" // a driven ship parked on its base's code
	GateDone     = "done"
	gateSkipped  = "skipped"
	gateOpen     = "open"
	gateSettled  = "settled"
	gateUnknown  = "unknown"
)

// How a running gate was started.
const (
	ViaShip   = "in ship"
	viaDriven = "driven"
	viaManual = "manual"
	ViaDrive  = "drive" // finish, which drive runs in its own process
	viaHost   = "on host"
)

// remoteTimeout bounds the host read for the remote review: past it the
// half is unknown and the rest of the status still prints.
var remoteTimeout = 5 * time.Second

type RemoteState struct {
	Found, Done, Handled, Settled bool
	Err                           error
}

type GateInput struct {
	Task, Branch, Checkout string

	Run      *state.Run // live, or the ledger's last claim; nil for none
	Events   []ledger.Event
	Driven   string                 // the last skill drive saw finish: plan, ship or finish
	Finishes bool                   // drive finishes the branch: its finish record is there
	Finish   branchmeta.DriveFinish // drive's record of running finish, zero for none or a drive gone
	Pipeline flow.Flow              // ship's entries of the branch's flow, when the run recorded none
	// RemoteFlow is a remote reviewer declared: it reviews every PR
	RemoteFlow bool

	Outcomes Outcomes
	Remote   func(ctx context.Context) RemoteState // nil: no host to ask
	Now      time.Time
}

func (in GateInput) ClaimedAt(session, step string) time.Time {
	var at time.Time

	for _, e := range in.Events {
		if e.Session != session || e.Kind != ledger.StateSet {
			continue
		}

		if phase, status, leaf := timing.Claim(e); phase == "ship" && status == "running" && leaf == step {
			at = e.At
		}
	}

	return at
}

// flow is the entries the task runs through: ship's, then finish when
// drive finishes the branch. plan and resolve are no gates: the plan phase
// row shows the one, a review gate folds in the other.
func (in GateInput) flow() flow.Flow {
	entries := in.Pipeline
	if in.Run != nil && len(in.Run.Pipeline) > 0 {
		entries = in.Run.Entries()
	}

	if in.Finishes {
		entries = append(slices.Clip(entries), flow.Entry{Step: "finish"})
	}

	return entries
}

// stepStates follows the claims ship's session made: steps before the one
// it is on are done, that one has the run's status, the rest are upcoming.
// finish is done once drive recorded it.
func (in GateInput) stepStates(entries flow.Flow) []string {
	states := make([]string, len(entries))
	for i := range states {
		states[i] = GateUpcoming
	}

	shipLen := len(entries)
	if in.Finishes {
		shipLen--

		if in.Driven == "finish" {
			states[shipLen] = GateDone
		}
	}

	r := in.Run
	if r == nil || shipLen == 0 || r.Ship == state.Upcoming || r.Ship == "" || r.Ship == "skipped" {
		return states
	}

	ship := entries[:shipLen].Strings()
	at, j := -1, 0

	for _, step := range ShipSteps(in.Events, r.Session, in.Now) {
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
			s = GateDone
		case r.Ship == "done":
			s = gateSkipped
		case k < at:
			s = GateDone
		case k == at && r.Wait != "" && state.Stopped(r.Ship):
			s = gateWaiting
		case k == at:
			s = r.Ship
		default:
			s = GateUpcoming
		}

		states[k] = s
	}

	return states
}

func (in GateInput) finishGate(st string) Gate {
	g := Gate{Step: "finish", State: st}
	if st != GateUpcoming || in.Finish.State == "" || !in.shipDone() {
		return g
	}

	g.Via, g.Action = ViaDrive, in.Finish.Action

	g.State = gateQueued
	if in.Finish.Running() {
		g.State = GateRunning
	}

	return g
}

func (in GateInput) shipDone() bool {
	if in.Run != nil && in.Run.Ship == "done" {
		return true
	}

	return in.Driven == "ship" || in.Driven == "finish"
}

// reviewBy is who the review entry at i runs, an optional CI review
// settled as the flow resolution settles it.
func (in GateInput) reviewBy(entries flow.Flow, i int) facts.By {
	by, _ := facts.ReviewBy(entries[i].Arg)
	by, _ = by.Settle(in.RemoteFlow, entries.PublishedBefore(i))

	return by
}

func (in GateInput) half(entries flow.Flow, steps []string, remote, inFlow bool) *ReviewHalf {
	h := in.readHalf(entries, steps, remote)

	// a half the flow does not declare shows once something ran it
	if !inFlow && h.State == GateUpcoming {
		return nil
	}

	return h
}

func (in GateInput) readHalf(entries flow.Flow, steps []string, remote bool) *ReviewHalf {
	rounds, last := in.Outcomes.Local, in.Outcomes.LocalAt
	if remote {
		rounds, last = in.Outcomes.Remote, in.Outcomes.RemoteAt
	}

	h := &ReviewHalf{State: GateUpcoming, Rounds: rounds}

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
		case s == GateRunning:
			h.State, h.Via, h.Action = GateRunning, ViaShip, "review"

			// a round written since ship entered the review is being resolved;
			// with both halves in the entry, only once both have landed
			if in.Run != nil {
				since := in.ClaimedAt(in.Run.Session, e.String())

				own, other := last.After(since), true
				if l && r {
					other = in.Outcomes.LocalAt.After(since) && in.Outcomes.RemoteAt.After(since)
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
			h.State, h.Via, h.Action = GateRunning, m.via, skill

			return h
		}
	}

	return h
}

func (in GateInput) hostHalf(ctx context.Context, h *ReviewHalf) {
	rs := in.Remote(ctx)

	switch {
	case errors.Is(rs.Err, context.DeadlineExceeded):
		h.State, h.Detail = gateUnknown, "timed out"
	case rs.Err != nil:
		h.State, h.Detail = gateUnknown, firstErrLine(rs.Err)
	case !rs.Found || h.State == GateRunning:
	case !rs.Done:
		h.State, h.Via = GateRunning, viaHost
	case rs.Settled:
		h.State = gateSettled
	case !rs.Handled:
		h.State = gateOpen
	case h.State == GateUpcoming:
		h.State = gateSettled
	}
}

func (in GateInput) judge(g *Gate, judged string, entries flow.Flow, steps []string) {
	out, last := in.Outcomes.ChangeJudge, in.Outcomes.changeAt
	if judged == "plan" {
		out, last = in.Outcomes.PlanJudge, in.Outcomes.planAt
	}

	g.State = GateUpcoming
	if out != nil {
		g.State, g.Outcome = GateDone, out
	}

	for i, e := range entries {
		if e.Step != "judge" {
			continue
		}

		switch s := steps[i]; {
		case s == GateRunning && judged == "change":
			g.State, g.Via = GateRunning, ViaShip

			return
		case state.Stopped(s) && judged == "change":
			g.State = s

			return
		}
	}

	// a judge run outside ship judges the plan until ship starts
	shipStarted := in.Run != nil && in.Run.Ship != state.Upcoming && in.Run.Ship != ""
	if (judged == "plan") == shipStarted {
		return
	}

	if m := in.started("judge", false, last); m != nil {
		g.State, g.Via, g.Outcome = GateRunning, m.via, nil
	}
}

// taskJudge is the judge fetch runs on a task older than stale_after, shown
// only once it ran or the plan phase is on it.
func (in GateInput) taskJudge() (Gate, bool) {
	g := Gate{Step: "judge", Judged: "task"}

	switch r := in.Run; {
	case r != nil && r.Leaf == "judge" && (r.Plan == GateRunning || state.Stopped(r.Plan)):
		g.State = r.Plan
	case in.Outcomes.TaskJudge != nil:
		g.State, g.Outcome = GateDone, in.Outcomes.TaskJudge
	default:
		return Gate{}, false
	}

	return g, true
}

// started is the newest call of skill on the task's branch or checkout after
// since, not a ship run's, whose session has not stopped or moved on and is
// younger than the state TTL, since Stop writes no stop for a session that
// ran no phase; `ci` makes a review the remote half's.
func (in GateInput) started(skill string, remote bool, since time.Time) *startMark {
	var (
		mark *startMark
		// a session's ship run is on until a claim takes ship off running;
		// ship calls only resolve through the Skill tool, never review or judge
		inShip = map[string]bool{}
	)

	for _, e := range in.Events {
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

	if mark != nil && in.Now.Sub(mark.at) > state.TTL() {
		return nil
	}

	return mark
}

// onTask: the hook writes run_started before any fetch, with no task, so the
// branch and the checkout tie a row to the task.
func (in GateInput) onTask(e ledger.Event) bool {
	return (in.Branch != "" && e.Branch == in.Branch) ||
		(in.Checkout != "" && e.Checkout != "" && gitx.Resolved(e.Checkout) == gitx.Resolved(in.Checkout))
}

// TaskGates builds the gates, nil when nothing declares a flow.
func TaskGates(ctx context.Context, in GateInput) []Gate {
	entries := in.flow()
	if len(entries) == 0 {
		return nil
	}

	steps := in.stepStates(entries)

	var (
		gates               []Gate
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
				gates = append(gates, Gate{Step: "review"})
			}
		case "judge":
			judged := entries.JudgeKind(i)

			if _, ok := judges[judged]; !ok {
				judges[judged] = len(gates)
				gates = append(gates, Gate{Step: "judge", Judged: judged})
			}
		case "finish":
			gates = append(gates, in.finishGate(steps[i]))
		default:
			if e.Step == "pr" {
				isPR = true
			}

			g := Gate{Step: e.String(), Label: e.Label, State: steps[i]}
			if g.State == gateWaiting {
				g.Action = "on " + in.Run.Wait
			}

			gates = append(gates, g)
		}
	}

	remote = remote || (in.RemoteFlow && isPR)

	lh, rh := in.half(entries, steps, false, local), in.half(entries, steps, true, remote)
	if remote && rh.State != gateSettled && rh.State != GateRunning && in.Remote != nil {
		in.hostHalf(ctx, rh)
	}

	switch {
	case lh == nil && rh == nil && review >= 0:
		// every review entry was a dropped ci?
		gates[review].State = GateOff
	case lh == nil && rh == nil:
		gates = append(gates, Gate{Step: "review", State: GateOff})
	case review < 0:
		gates = append(gates, Gate{Step: "review", Local: lh, Remote: rh})
	default:
		gates[review].Local, gates[review].Remote = lh, rh
	}

	for judged, i := range judges {
		in.judge(&gates[i], judged, entries, steps)
	}

	// the stale task's judge is in no flow: it leads once it ran or the run is on it
	if g, ok := in.taskJudge(); ok {
		gates = append([]Gate{g}, gates...)
	}

	// a judge the flow does not declare shows once something ran it
	extra := false

	for _, judged := range []string{"plan", "change"} {
		if _, ok := judges[judged]; ok {
			continue
		}

		g := Gate{Step: "judge", Judged: judged}
		in.judge(&g, judged, entries, steps)

		if g.State != GateUpcoming {
			gates, extra = append(gates, g), true
		}
	}

	if len(judges) == 0 && !extra {
		gates = append(gates, Gate{Step: "judge", State: GateOff})
	}

	return gates
}

type startMark struct {
	at      time.Time
	session string
	via     string
}
