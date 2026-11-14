package ledger

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/transcript"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// Kinds, by source.
const (
	RunStarted     = "run_started"
	TaskCaptured   = "task_captured"
	StepEntered    = "step_entered"
	Committed      = "committed"
	Pushed         = "pushed"
	PROpened       = "pr_opened"
	TaskFinished   = "task_finished"
	TaskHalted     = "task_halted" // drive's, not a hook's: a task it stopped
	SessionStopped = "session_stopped"
	TurnUsed       = "turn_used" // the tokens a turn spent
	WaitStarted    = "wait_started"
	WaitEnded      = "wait_ended"
	TaskSized      = "task_sized" // finish's: the branch's diffstat before it lands

	PlanSaved         = "plan_saved"
	PlanSuperseded    = "plan_superseded"
	ReviewRoundOpened = "review_round_opened"
	FindingVerdict    = "finding_verdict"
	JudgeVerdict      = "judge_verdict"

	StateSet      = "state_set"
	StateTask     = "state_task"
	StateNote     = "state_note"
	StateAsk      = "state_ask"
	StateWait     = "state_wait"
	StateStop     = "state_stop"
	StatePipeline = "state_pipeline"
	StateDone     = "state_done" // a phaseless step's report is out: the drive's end signal
	StateFinal    = "state_final"
)

// Observer records a hook's observations to the spool; only one that compares
// against prior rows (a stop, a findings or a verdict file) opens the ledger,
// so most hooks never touch SQLite.
type Observer struct {
	StateDir string
	Busy     time.Duration
	NoReplay bool // a synchronous hook holds the prompt: the spool waits for a later writer
	// TaskID is the tracker task id a created issue's url names.
	TaskID func(ctx context.Context, cwd, url string) string

	path   string
	l      *Ledger
	opened bool
	places map[string]Place
}

// Close closes the ledger if an observation opened it.
func (ob *Observer) Close() {
	if ob.l != nil {
		_ = ob.l.Close()
		ob.l = nil
	}
}

func (ob *Observer) Observe(ctx context.Context, o observe.Observation) error {
	if ob.path == "" {
		ob.path = Path()
	}

	if ob.path == "" {
		return nil
	}

	m := observe.MetaOf(o)

	events := ob.events(ctx, o, m)
	if len(events) == 0 {
		return nil
	}

	place := ob.place(ctx, m.Cwd)
	task := ""

	if m.Session != "" {
		if r, err := state.Load(state.Path(ob.StateDir, m.Session)); err == nil {
			task = r.Task
		}
	}

	for i := range events {
		e := &events[i]
		e.At, e.Session = m.At, m.Session
		e.Project, e.ProjectLocal, e.Checkout, e.Branch = place.Project, place.ProjectLocal, place.Checkout, place.Branch

		if e.Task == "" && !taskless(e) {
			e.Task = task
		}
	}

	fill(ob.path, events)

	switch {
	case gated(o):
		spoolAs(ob.path, events, true)
	case ob.l != nil:
		ob.l.Append(ctx, events...)
	default:
		spool(ob.path, events)
	}

	return nil
}

// open opens the ledger once, for an observation that reads prior rows.
// A failed open leaves ob.l nil: the reader then sees no prior rows.
func (ob *Observer) open(ctx context.Context) *Ledger {
	if !ob.opened {
		ob.opened = true

		if l, err := Open(ctx, ob.path, ob.Busy, !ob.NoReplay); err == nil {
			ob.l = l
		}
	}

	return ob.l
}

// gated reports an observation recorded only inside an f10 run: commits,
// pushes and created issues or PRs in an unrelated session stay out. The
// hook cannot tell without the database, so replay decides.
func gated(o observe.Observation) bool {
	switch o.(type) {
	case *observe.IssueCreated, *observe.PRCreated, *observe.Committed, *observe.Pushed:
		return true
	}

	return false
}

func (ob *Observer) place(ctx context.Context, cwd string) Place {
	if p, ok := ob.places[cwd]; ok {
		return p
	}

	if ob.places == nil {
		ob.places = map[string]Place{}
	}

	ob.places[cwd] = Where(ctx, cwd)

	return ob.places[cwd]
}

// events maps one observation to its rows; none when the observation is
// out of scope or already recorded.
func (ob *Observer) events(ctx context.Context, o observe.Observation, m observe.Meta) []Event {
	one := func(kind, source, task string, payload any) []Event {
		e, err := NewEvent(kind, payload)
		if err != nil {
			return nil
		}

		e.Source, e.Task = source, task

		return []Event{e}
	}

	switch o := o.(type) {
	case *observe.SkillStarted:
		return one(
			RunStarted,
			Observed,
			"",
			RunStartedPayload{Skill: o.Skill, Args: o.Args, RunContext: contextOf(o.RunContext)},
		)
	case *observe.StepStarted:
		return one(StepEntered, Observed, "", StepEnteredPayload{Steps: o.Steps})
	case *observe.Finished:
		return one(TaskFinished, Observed, "", TaskFinishedPayload{Args: o.Args})
	case *observe.SessionStopped:
		sameStop, sameContext := ob.sameStop(ctx, m.Session, o)
		if sameStop && sameContext {
			return nil
		}

		p := stopPayload(o)
		if !sameContext {
			p.RunContext = contextOf(o.RunContext)
		}

		return one(SessionStopped, Observed, o.Task, p)
	case *observe.TurnUsed:
		if o.Usage.Zero() {
			return nil
		}

		return one(TurnUsed, Observed, o.Task, TurnUsedPayload{Usage: usageOf(o.Usage)})
	case *observe.IssueCreated:
		if m.Session == "" {
			return nil
		}

		id := o.ID
		if id == "" && ob.TaskID != nil {
			id = ob.TaskID(ctx, m.Cwd, o.URL)
		}

		return one(TaskCaptured, Observed, id, TaskCapturedPayload{Host: o.Host, URL: o.URL, ID: id})
	case *observe.PRCreated:
		if m.Session == "" {
			return nil
		}

		return one(PROpened, Observed, "", PROpenedPayload{Host: o.Host, URL: o.URL})
	case *observe.Committed:
		if m.Session == "" {
			return nil
		}

		return one(Committed, Observed, "", CommittedPayload{Branch: o.Branch, SHA: o.SHA, Subject: o.Subject})
	case *observe.Pushed:
		if m.Session == "" {
			return nil
		}

		return one(Pushed, Observed, "", PushedPayload{Remote: o.Remote, Refs: o.Refs})
	case *observe.WaitStarted:
		return one(WaitStarted, Observed, "", WaitStartedPayload{Reason: o.Reason, Tool: o.Tool})
	case *observe.WaitEnded:
		return one(WaitEnded, Observed, "", WaitEndedPayload{Reason: o.Reason, Tool: o.Tool, By: o.By})
	case *observe.PlanWritten:
		return one(PlanSaved, Artifact, o.Task, PlanSavedPayload{Path: o.Path, Task: o.Task})
	case *observe.PlanArchived:
		return one(PlanSuperseded, Artifact, o.Task, PlanSupersededPayload{From: o.From, To: o.To, Task: o.Task})
	case *observe.FindingsWritten:
		return ob.findings(ctx, o)
	case *observe.VerdictWritten:
		return ob.verdict(ctx, o, m)
	}

	return nil
}

func stopPayload(o *observe.SessionStopped) SessionStoppedPayload {
	return SessionStoppedPayload{Task: o.Task, Phase: o.Phase, Status: o.Status, Leaf: o.Leaf, Backstop: o.Backstop}
}

// artifact is a row an artifact file wrote, nil when payload does not match
// the kind.
func artifact(kind, task string, payload any) []Event {
	e, err := NewEvent(kind, payload)
	if err != nil {
		return nil
	}

	e.Source, e.Task = Artifact, task

	return []Event{e}
}

// sameStop reports whether the session's last stop row says the same, and
// whether the run context matches the session's last row that carried one:
// Stop fires at every turn end, and only a change is worth a row.
func (ob *Observer) sameStop(ctx context.Context, session string, o *observe.SessionStopped) (bool, bool) {
	l := ob.open(ctx)
	if l == nil {
		return false, false
	}

	// one read of the session's stops and starts: the context may sit on either
	rows, err := l.List(ctx, Filter{Session: session, Kinds: []string{SessionStopped, RunStarted}})
	if err != nil {
		return false, false
	}

	var stops, starts []Event

	for _, e := range rows {
		if e.Kind == SessionStopped {
			stops = append(stops, e)
		} else {
			starts = append(starts, e)
		}
	}

	same := false
	if len(stops) > 0 {
		last, _ := Decode[SessionStoppedPayload](stops[len(stops)-1])
		same = last.Phase == o.Phase && last.Status == o.Status && last.Leaf == o.Leaf
	}

	return same, lastContext(stops, starts) == contextOf(o.RunContext)
}

// lastContext is the run context of the latest row of either list that
// carries one, by time and then id.
func lastContext(stops, starts []Event) RunContext {
	var (
		found  RunContext
		latest *Event
	)

	for _, e := range slices.Concat(stops, starts) {
		var c RunContext

		if e.Kind == RunStarted {
			p, _ := Decode[RunStartedPayload](e)
			c = p.RunContext
		} else {
			p, _ := Decode[SessionStoppedPayload](e)
			c = p.RunContext
		}

		if c == (RunContext{}) {
			continue
		}

		if latest == nil || e.At.After(latest.At) || e.At.Equal(latest.At) && e.ID > latest.ID {
			found, latest = c, &e
		}
	}

	return found
}

func contextOf(c observe.RunContext) RunContext {
	return RunContext{
		F10Version: c.F10Version,
		Model:      c.Model,
		Agent:      c.Agent,
		Profile:    AgentProfile{Kind: c.ProfileKind, Model: c.ProfileModel, Effort: c.ProfileEffort},
	}
}

//nolint:lostfield // the model goes on the stop row's context, not the usage
func usageOf(u transcript.Usage) Usage {
	out := Usage{Tokens: tokensOf(u.Tokens), Subagents: tokensOf(u.Subagents), Through: u.Through}

	if len(u.ByModel) > 1 {
		out.ByModel = make(map[string]Tokens, len(u.ByModel))
		for m, t := range u.ByModel {
			out.ByModel[m] = tokensOf(t)
		}
	}

	return out
}

func tokensOf(t transcript.Tokens) Tokens {
	return Tokens{Input: t.Input, Output: t.Output, CacheWrite: t.CacheWrite, CacheRead: t.CacheRead}
}

// findings records the round opening once per reviewed sha, then each finding
// whose verdict differs from the last recorded. Review writes the file and
// resolve edits it, so the diff against the ledger decides, not the writer.
func (ob *Observer) findings(ctx context.Context, o *observe.FindingsWritten) []Event {
	raw, err := os.ReadFile(o.Path)
	if err != nil {
		return nil
	}

	f, err := findings.Parse(string(raw))
	if err != nil {
		return artifact(
			ReviewRoundOpened,
			o.Task,
			ReviewRoundOpenedPayload{Path: o.Path, Round: o.Round, ParseError: err.Error()},
		)
	}

	var opens, verdicts []Event

	if l := ob.open(ctx); l != nil {
		// every opening of the round, not the last: any one at this sha counts
		opens, _ = l.List(ctx, Filter{Task: o.Task, Kind: ReviewRoundOpened, Round: f.Round})
		verdicts, _ = l.latestVerdicts(ctx, o.Task)
	}

	var events []Event

	opened := slices.ContainsFunc(opens, func(e Event) bool {
		p, _ := Decode[ReviewRoundOpenedPayload](e)

		return p.Round == f.Round && p.Reviewed == f.Reviewed
	})
	last := map[string]string{} // verdict and status last recorded, by round/finding

	for _, e := range verdicts {
		p, _ := Decode[FindingVerdictPayload](e)
		last[p.Round+"/"+p.Finding] = p.Verdict + "|" + p.Status
	}

	if !opened {
		events = append(events, artifact(ReviewRoundOpened, o.Task, ReviewRoundOpenedPayload{
			Path: o.Path, Round: f.Round, Reviewed: f.Reviewed, Reviewer: f.Reviewer,
			Verdict: f.Verdict, Categories: f.Categories,
		})...)
	}

	// keyed by round: a prior-round block reuses its round-one id, and its
	// status is news even when its verdict is not
	for _, fd := range f.Findings {
		if fd.Verdict == "" && fd.Status == "" || last[f.Round+"/"+fd.ID] == fd.Verdict+"|"+fd.Status {
			continue
		}

		events = append(events, artifact(FindingVerdict, o.Task, FindingVerdictPayload{
			Round: f.Round, Finding: fd.ID, Title: fd.Title,
			Verdict: fd.Verdict, Category: fd.Category, Reason: fd.Reason, Status: fd.Status,
		})...)
	}

	return events
}

// verdict records a verdict file once per verdict and decision, so a
// decision filled in after a driven ask adds a row and a plain rewrite none;
// another session at the same path is a recreated checkout's new run.
func (ob *Observer) verdict(ctx context.Context, o *observe.VerdictWritten, m observe.Meta) []Event {
	raw, err := os.ReadFile(o.Path)
	if err != nil {
		return nil
	}

	p := JudgeVerdictPayload{Path: o.Path, Key: o.Key, N: o.N}

	f, err := verdict.Parse(string(raw))
	if err != nil {
		p.ParseError = err.Error()

		task := ""
		if f != nil {
			task, p.Idea = f.Task, f.Idea()
		}

		return artifact(JudgeVerdict, task, p)
	}

	// the database holds every project's rows: scope the lookup to this one
	if l := ob.open(ctx); l != nil {
		e, ok, _ := l.Last(ctx, Filter{Project: ob.place(ctx, m.Cwd).Project, Kind: JudgeVerdict, Path: o.Path})
		if v, _ := Decode[JudgeVerdictPayload](e); ok && e.Session == m.Session && v.Verdict == f.Verdict &&
			v.Decision == f.Decision {
			return nil
		}
	}

	p.Subject, p.Verdict, p.Reason, p.Run, p.Decision = f.Subject, f.Verdict, f.Reason, f.Run, f.Decision
	p.Blind, p.Idea = f.Blind, f.Idea()

	return artifact(JudgeVerdict, f.Task, p)
}

// taskless reports a row that belongs to no task even in a session that has
// one: a verdict on an idea judged mid-task is not that task's verdict.
func taskless(e *Event) bool {
	if e.Kind != JudgeVerdict {
		return false
	}

	p, _ := Decode[JudgeVerdictPayload](*e)

	return p.Idea
}
