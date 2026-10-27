package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/state"
)

// A task's gates are every step its flow declares, with the state each is
// in: ship's recorded entries, else the flow its branch records, merged
// with the chain drive runs it through, progress read from the ledger,
// outcomes from lore or the findings and verdict files, and the remote
// review from the host.

// Gate states. A plain step also takes a run's stop status (failed,
// blocked, partial) on the step it stopped at.
const (
	gateOff     = "not in this flow"
	gatePending = "pending"
	gateRunning = "running"
	gateDone    = "done"
	gateSkipped = "skipped"
	gateOpen    = "open"
	gateSettled = "settled"
	gateUnknown = "unknown"
)

// How a running gate was started.
const (
	viaShip   = "in ship"
	viaDriven = "driven"
	viaManual = "manual"
	viaHost   = "on host"
)

// remoteTimeout bounds the host read for the remote review: past it the
// half is unknown and the rest of the status still prints.
var remoteTimeout = 5 * time.Second

// A gate is one row of a task's flow: a plain step, its review (local and
// remote halves) or a judge.
type gate struct {
	Step    string        `json:"step"`
	State   string        `json:"state,omitempty"`  // plain steps and judges
	Via     string        `json:"via,omitempty"`    // a running gate: in ship, driven or manual
	Judged  string        `json:"judged,omitempty"` // a judge: plan or change
	Outcome *judgeOutcome `json:"outcome,omitempty"`
	Local   *reviewHalf   `json:"local,omitempty"`
	Remote  *reviewHalf   `json:"remote,omitempty"`
}

// A reviewHalf is the local or the remote review of a task.
type reviewHalf struct {
	State  string        `json:"state"`
	Via    string        `json:"via,omitempty"`
	Action string        `json:"action,omitempty"` // running: review, or resolve settling it
	Detail string        `json:"detail,omitempty"` // unknown: why
	Rounds []reviewRound `json:"rounds,omitempty"`
}

// remoteState is what the host says about the newest remote review.
type remoteState struct {
	found, done, handled, settled bool
	err                           error
}

// gateInput is everything a task's gates are read from.
type gateInput struct {
	task, branch, checkout string

	run      *state.Run // live, or the ledger's last claim; nil for none
	events   []ledger.Event
	chain    []string // drive's chain recorded on the branch
	driven   string   // the last chain skill drive saw finish
	pipeline []string // ship's entries of the branch's flow, when the run recorded none
	// remoteFlow is a remote reviewer declared: it reviews every PR
	remoteFlow bool

	outcomes outcomes
	remote   func(ctx context.Context) remoteState // nil: no host to ask
	now      time.Time
}

// taskGates builds the gates, nil when nothing declares a flow.
func taskGates(ctx context.Context, in gateInput) []gate {
	entries, firstShip, shipLen := in.flow()
	if len(entries) == 0 {
		return nil
	}

	steps := in.stepStates(entries, firstShip, shipLen)

	var (
		gates               []gate
		review              = -1
		judges              = map[string]int{}
		local, remote, isPR bool
	)

	for i, e := range entries {
		base, qual := facts.SplitEntry(e)

		switch base {
		case "review":
			// review:local+ci is one entry with both halves
			l, r := facts.ReviewHalves(qual)
			local, remote = local || l, remote || r

			if review < 0 {
				review = len(gates)
				gates = append(gates, gate{Step: "review"})
			}
		case "judge":
			// before implement it judges the plan, a judge the drive chain ran
			// before ship included
			judged := facts.JudgeKind(entries, i)

			if _, ok := judges[judged]; !ok {
				judges[judged] = len(gates)
				gates = append(gates, gate{Step: "judge", Judged: judged})
			}
		default:
			if base == "pr" {
				isPR = true
			}

			gates = append(gates, gate{Step: e, State: steps[i]})
		}
	}

	remote = remote || (in.remoteFlow && isPR)

	lh, rh := in.half(entries, steps, false, local), in.half(entries, steps, true, remote)
	if remote && rh.State != gateSettled && rh.State != gateRunning && in.remote != nil {
		in.hostHalf(ctx, rh)
	}

	switch {
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

	// a judge the flow does not declare shows once something ran it
	extra := false

	for _, judged := range []string{"plan", "change"} {
		if _, ok := judges[judged]; ok {
			continue
		}

		g := gate{Step: "judge", Judged: judged}
		in.judge(&g, judged, entries, steps)

		if g.State != gatePending {
			gates, extra = append(gates, g), true
		}
	}

	if len(judges) == 0 && !extra {
		gates = append(gates, gate{Step: "judge", State: gateOff})
	}

	return gates
}

// flow is the entries the task runs through, in order, the index of ship's
// first one (len when the chain runs no ship) and how many are ship's.
// plan and resolve are not gates: the plan phase row shows the one, a
// review gate folds in the other.
func (in gateInput) flow() ([]string, int, int) {
	pipeline := in.pipeline
	if in.run != nil && len(in.run.Pipeline) > 0 {
		pipeline = in.run.Pipeline
	}

	if len(in.chain) == 0 {
		return pipeline, 0, len(pipeline)
	}

	var entries []string

	firstShip, shipLen := -1, 0

	for _, c := range in.chain {
		switch c {
		case "plan", "resolve":
		case "ship":
			firstShip, shipLen = len(entries), len(pipeline)
			entries = append(entries, pipeline...)
		default:
			entries = append(entries, c)
		}
	}

	if firstShip < 0 {
		firstShip = len(entries)
	}

	return entries, firstShip, shipLen
}

// stepStates is each entry's state as a plain step. Ship's entries follow
// the claims its session made: every step before the one it is on is
// done, that one has the run's status, the rest are pending. The chain's
// own entries follow what drive recorded on the branch.
func (in gateInput) stepStates(entries []string, firstShip, shipLen int) []string {
	states := make([]string, len(entries))
	for i := range states {
		states[i] = gatePending
	}

	in.chainStates(entries, states, shipLen)

	r := in.run
	if r == nil || shipLen == 0 || r.Ship == "pending" || r.Ship == "" || r.Ship == "skipped" {
		return states
	}

	ship := entries[firstShip : firstShip+shipLen]
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
		case k == at:
			s = r.Ship
		default:
			s = gatePending
		}

		states[firstShip+k] = s
	}

	return states
}

// chainStates marks the chain's own entries done up to the last skill
// drive recorded.
func (in gateInput) chainStates(entries, states []string, shipLen int) {
	last := slices.Index(in.chain, in.driven)
	if last < 0 {
		return
	}

	i := 0

	for ci, c := range in.chain {
		switch c {
		case "plan", "resolve":
			continue
		case "ship":
			i += shipLen

			continue
		}

		if i < len(entries) && ci <= last {
			states[i] = gateDone
		}

		i++
	}
}

// half builds the local or the remote review, nil when the flow has none.
func (in gateInput) half(entries, steps []string, remote, inFlow bool) *reviewHalf {
	h := in.readHalf(entries, steps, remote)

	// a half the flow does not declare shows once something ran it
	if !inFlow && h.State == gatePending {
		return nil
	}

	return h
}

func (in gateInput) readHalf(entries, steps []string, remote bool) *reviewHalf {
	rounds, last := in.outcomes.local, in.outcomes.localAt
	if remote {
		rounds, last = in.outcomes.remote, in.outcomes.remoteAt
	}

	h := &reviewHalf{State: gatePending, Rounds: rounds}

	if len(rounds) > 0 {
		h.State = gateSettled
		if rounds[len(rounds)-1].Ask > 0 {
			h.State = gateOpen
		}
	}

	// ship on a review entry running this half, alone or beside the other
	for i, e := range entries {
		base, qual := facts.SplitEntry(e)
		l, r := facts.ReviewHalves(qual)

		if base != "review" || (remote && !r) || (!remote && !l) {
			continue
		}

		switch s := steps[i]; {
		case s == gateRunning:
			h.State, h.Via, h.Action = gateRunning, viaShip, "review"

			// a round written since ship entered the review is being resolved;
			// with both halves in the entry, only once both have landed
			if in.run != nil {
				since := in.claimedAt(in.run.Session, e)

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

// hostHalf folds the host's word on the remote review into the half.
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
	case h.State == gatePending:
		h.State = gateSettled
	}
}

// judge fills a judge gate: running in ship, or started outside it, else
// its newest verdict.
func (in gateInput) judge(g *gate, judged string, entries, steps []string) {
	out, last := in.outcomes.changeJudge, in.outcomes.changeAt
	if judged == "plan" {
		out, last = in.outcomes.planJudge, in.outcomes.planAt
	}

	g.State = gatePending
	if out != nil {
		g.State, g.Outcome = gateDone, out
	}

	for i, e := range entries {
		if base, _ := facts.SplitEntry(e); base != "judge" {
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
	shipStarted := in.run != nil && in.run.Ship != "pending" && in.run.Ship != ""
	if (judged == "plan") == shipStarted {
		return
	}

	if m := in.started("judge", false, last); m != nil {
		g.State, g.Via, g.Outcome = gateRunning, m.via, nil
	}
}

// claimedAt is when the session last claimed ship running step.
func (in gateInput) claimedAt(session, step string) time.Time {
	var at time.Time

	for _, e := range in.events {
		if e.Session != session || e.Kind != ledger.StateSet {
			continue
		}

		args, _ := e.Payload.Val["args"].([]any)
		if len(args) >= 3 && args[0] == "ship" && args[1] == "running" && args[2] == step {
			at = e.At
		}
	}

	return at
}

// startMark is a skill call outside ship, tied to the task.
type startMark struct {
	at      time.Time
	session string
	via     string
}

// started is the newest call of skill on the task's branch or checkout
// after since, not made by a ship run, and still running: its session
// neither stopped nor started another skill since, and it is younger than
// the state TTL, since the Stop hook writes no stop for a session that ran
// no phase. A review's `ci` argument makes it the remote half's. The hook
// writes run_started before any fetch, with no task, so the branch and the
// checkout tie it to the task.
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
			s := payloadString(e.Payload.Val, "skill")

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

			args := strings.Fields(payloadString(e.Payload.Val, "args"))
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
			switch payloadString(e.Payload.Val, "ship") {
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

// onTask is a hook's row on the task's branch or in its checkout.
func (in gateInput) onTask(e ledger.Event) bool {
	return (in.branch != "" && e.Branch == in.branch) ||
		(in.checkout != "" && e.Checkout != "" && canonPath(e.Checkout) == canonPath(in.checkout))
}

// outcomes is what the reviews and judges of a task left, with the time
// of the newest of each, so a later call reads as running.
type outcomes struct {
	local, remote          []reviewRound
	planJudge, changeJudge *judgeOutcome

	localAt, remoteAt, planAt, changeAt time.Time

	// every finding the files raised, each counted in its first round only
	localRaised, remoteRaised int
}

// readOutcomes reads lore first and the findings and verdict files for
// whatever lore holds nothing of: runs from before the steps wrote lore.
func readOutcomes(store *lore.Store, storageRoot, task string) outcomes {
	var o outcomes

	if storageRoot != "" {
		o = fileOutcomes(storageRoot, task)
	}

	if store == nil {
		return o
	}

	entries, err := store.Effective(task)
	if err != nil {
		return o
	}

	l := loreOutcomes(entries)

	if current(l.local, o.localRaised, l.localAt, o.localAt) {
		o.local = roundsFromLore(l.local, o.local)
	}

	if current(l.remote, o.remoteRaised, l.remoteAt, o.remoteAt) {
		o.remote = roundsFromLore(l.remote, o.remote)
	}

	o.localAt, o.remoteAt = latest(o.localAt, l.localAt), latest(o.remoteAt, l.remoteAt)

	if l.planJudge != nil && !l.planAt.Add(time.Second).Before(o.planAt) {
		o.planJudge = l.planJudge
	}

	if l.changeJudge != nil && !l.changeAt.Add(time.Second).Before(o.changeAt) {
		o.changeJudge = l.changeJudge
	}

	o.planAt, o.changeAt = latest(o.planAt, l.planAt), latest(o.changeAt, l.changeAt)

	return o
}

// current is lore's counts answering for a half: as new as the newest
// findings file (lore keeps whole seconds) and holding every finding the
// files raised, so a lore write that failed leaves the files the answer.
// No file at all leaves lore alone.
func current(fromLore []reviewRound, raised int, loreAt, fileAt time.Time) bool {
	switch {
	case fromLore == nil:
		return false
	case fileAt.IsZero():
		return true
	}

	return !loreAt.Add(time.Second).Before(fileAt) && fromLore[0].Found >= raised
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

// roundsFromLore is lore's counts as the latest round, numbered after the
// files when they hold rounds: lore keeps findings, not rounds.
func roundsFromLore(counts, files []reviewRound) []reviewRound {
	r := counts[0]
	r.Round = "1"

	if len(files) > 0 {
		r.Round = files[len(files)-1].Round
	}

	return []reviewRound{r}
}

// loreOutcomes counts the effective findings by source and keeps the
// newest verdict of each kind.
func loreOutcomes(entries []lore.Entry) outcomes {
	var (
		o              outcomes
		local, remote  *reviewRound
		planAt, change time.Time
	)

	for _, e := range entries {
		switch e.Kind {
		case lore.KindFinding:
			r, at := &local, &o.localAt
			if e.Source == "remote" {
				r, at = &remote, &o.remoteAt
			}

			if *r == nil {
				*r = &reviewRound{}
			}

			(*r).Found++

			switch e.Status {
			case "fixed":
				(*r).Fixed++
			case "open":
				(*r).Ask++
			default:
				(*r).Skipped++
			}

			if e.At.After(*at) {
				*at = e.At
			}
		case lore.KindVerdict:
			j := &judgeOutcome{Verdict: e.Outcome, Reason: firstLine(strings.TrimSpace(e.Body))}

			if e.Judged == "plan" {
				if !e.At.Before(planAt) {
					o.planJudge, planAt = j, e.At
				}
			} else if !e.At.Before(change) {
				o.changeJudge, change = j, e.At
			}
		case lore.KindIntent, lore.KindDecision, lore.KindLesson:
		}
	}

	if local != nil {
		o.local = []reviewRound{*local}
	}

	if remote != nil {
		o.remote = []reviewRound{*remote}
	}

	o.planAt, o.changeAt = planAt, change

	return o
}

func firstErrLine(err error) string {
	return firstLine(err.Error())
}

// gateLines is the gates as label and value, the step names indented
// under ship.
func gateLines(gates []gate) [][2]string {
	out := make([][2]string, len(gates))

	for i, g := range gates {
		out[i] = [2]string{"  " + g.Step, g.text()}
	}

	return out
}

// style is the badge status whose palette the gate prints in: an open
// review reads as partial, an unknown one as blocked.
func (g gate) style() string {
	states := []string{g.State}
	if g.Local != nil || g.Remote != nil {
		states = nil

		for _, h := range []*reviewHalf{g.Local, g.Remote} {
			if h != nil {
				states = append(states, h.State)
			}
		}
	}

	switch {
	case slices.Contains(states, gateRunning):
		return "running"
	case slices.ContainsFunc(states, state.Stopped):
		return "failed"
	case slices.Contains(states, gateUnknown):
		return "blocked"
	case slices.Contains(states, gateOpen):
		return "partial"
	case !slices.ContainsFunc(states, func(s string) bool { return s != gateDone && s != gateSettled }):
		return "done"
	}

	return "pending"
}

// text is the gate's state as a reader takes it in.
func (g gate) text() string {
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
		return g.Judged + ": " + g.stateText()
	}

	return g.stateText()
}

func (g gate) stateText() string {
	switch {
	case g.State == gateRunning && g.Via != "":
		return "running (" + g.Via + ")"
	case g.State == gateDone && g.Outcome != nil:
		text := g.Outcome.Verdict
		if g.Outcome.Reason != "" {
			text += " - " + g.Outcome.Reason
		}

		return text
	}

	return g.State
}

func (h *reviewHalf) text() string {
	switch h.State {
	case gateRunning:
		what := fmt.Sprintf("round %d running", len(h.Rounds)+1)

		switch h.Action {
		case "resolve":
			what = "resolve running"
		case "await":
			// review:local+ci: this half's round is in, the other's is not
			what = fmt.Sprintf("round %d in, waiting for the other half", len(h.Rounds))
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

// compactGates is the parent rows' gate column: `L 3/4 · R open · J
// proceed`, `-` for a gate not in the flow, `…` running, `?` unknown.
func compactGates(gates []gate) string {
	var parts []string

	for _, g := range gates {
		switch {
		case g.Step == "review" && g.State == gateOff:
			parts = append(parts, "L - · R -")
		case g.Step == "review":
			parts = append(parts, "L "+g.Local.compact(), "R "+g.Remote.compact())
		case g.Step == "judge":
			j := "J "
			if g.Judged == "plan" {
				j = "PJ "
			}

			parts = append(parts, j+g.compactState())
		}
	}

	return strings.Join(parts, " · ")
}

func (g gate) compactState() string {
	switch g.State {
	case gateOff:
		return "-"
	case gateRunning:
		return "…"
	case gateDone:
		if g.Outcome != nil {
			return g.Outcome.Verdict
		}
	}

	return g.State
}

func (h *reviewHalf) compact() string {
	if h == nil {
		return "-"
	}

	switch h.State {
	case gateRunning:
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

// gateSource binds what every task's gates read alike: the ledger, the
// project's facts, the lore store and the host.
type gateSource struct {
	events []ledger.Event
	main   string // where the branch config lives
	flows  map[string][]string
	review facts.Review
	store  *lore.Store
	host   *host
	shared string // the out-of-tree storage root, "" in-repo
}

// storageFor is where a task's findings and verdicts are: its checkout's
// storage root, else where its last run left them.
func (src gateSource) storageFor(ctx context.Context, root string, run *state.Run) string {
	if root != "" {
		return layout.Locate(ctx, root).StorageRoot
	}

	if run == nil {
		return src.shared
	}

	return finishedStorage(src.shared, run.Root)
}

// gatesOf reads one task's gates. root is the checkout holding branch, ""
// when none does; storage is where its findings and verdicts are.
func (src gateSource) gatesOf(ctx context.Context, id, branch, root, storage string, run *state.Run) []gate {
	in := gateInput{
		task:       id,
		branch:     branch,
		checkout:   root,
		run:        run,
		events:     src.events,
		remoteFlow: src.review.Remote(),
		outcomes:   readOutcomes(src.store, storage, id),
		now:        time.Now(),
	}

	flow := "default"

	if branch != "" && src.main != "" {
		in.chain = strings.Fields(gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+cfgChain))
		in.driven = gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+cfgDrive)

		if recorded := gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+cfgFlow); recorded != "" {
			flow = recorded
		}
	}

	if entries, ok := src.flows[flow]; ok {
		in.pipeline = facts.ShipEntries(entries)
	} else {
		in.pipeline = facts.ShipEntries(src.flows["default"])
	}

	if src.host != nil && branch != "" && src.review.Remote() {
		h := *src.host
		if root != "" {
			h = h.at(root)
		}

		in.remote = func(ctx context.Context) remoteState {
			return readRemote(ctx, h, src.review, branch)
		}
	}

	return taskGates(ctx, in)
}

// readRemote asks the host about the newest remote review on the branch's
// request, within remoteTimeout. No request yet is nothing found.
func readRemote(ctx context.Context, h host, f facts.Review, branch string) remoteState {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()

	p, err := h.pull(ctx, branch)

	switch {
	case ctx.Err() != nil:
		return remoteState{err: ctx.Err()}
	case noSuchPull(err):
		return remoteState{}
	case err != nil:
		return remoteState{err: err}
	case p.url() == "":
		return remoteState{}
	}

	rv, err := newReviewer(ctx, h, f, path.Base(p.url()))
	if err == nil {
		var r remoteReview

		r, err = rv.latest(ctx)
		if err == nil {
			return remoteState{found: true, done: r.Done, handled: r.Handled, settled: r.Settled}
		}
	}

	switch {
	case ctx.Err() != nil:
		return remoteState{err: ctx.Err()}
	case errors.Is(err, errNoReview):
		return remoteState{}
	}

	return remoteState{err: err}
}
