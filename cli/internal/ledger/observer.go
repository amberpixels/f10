package ledger

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
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
	WaitStarted    = "wait_started"
	WaitEnded      = "wait_ended"
	TaskSized      = "task_sized" // finish's: the branch's diffstat before it lands

	PlanSaved         = "plan_saved"
	PlanSuperseded    = "plan_superseded"
	ReviewRoundOpened = "review_round_opened"
	FindingVerdict    = "finding_verdict"
	JudgeVerdict      = "judge_verdict"

	StateSet   = "state_set"
	StateTask  = "state_task"
	StateNote  = "state_note"
	StateAsk   = "state_ask"
	StateFinal = "state_final"
)

// Observer records a hook's observations. It opens the ledger on the first
// one, so an event that yields nothing never touches the database; Close
// releases it.
type Observer struct {
	StateDir string
	Busy     time.Duration
	// NoReplay leaves the spool to a later writer: a synchronous hook holds
	// the prompt while it runs.
	NoReplay bool
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
	ob.open(ctx)

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

	if ob.l == nil {
		fill(ob.path, events)
		spool(ob.path, events)

		return nil
	}

	ob.l.Append(ctx, events...)

	return nil
}

func (ob *Observer) open(ctx context.Context) {
	if ob.opened {
		return
	}

	ob.opened = true
	ob.path = Path()

	if ob.path == "" {
		return
	}

	if l, err := Open(ctx, ob.path, ob.Busy, !ob.NoReplay); err == nil {
		ob.l = l
	}
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
	one := func(kind, source, task string) []Event {
		return []Event{{Kind: kind, Source: source, Task: task, Payload: payloadOf(o)}}
	}

	switch o := o.(type) {
	case *observe.SkillStarted:
		return one(RunStarted, Observed, "")
	case *observe.StepStarted:
		return one(StepEntered, Observed, "")
	case *observe.Finished:
		return one(TaskFinished, Observed, "")
	case *observe.SessionStopped:
		if ob.sameStop(ctx, m.Session, o) {
			return nil
		}

		return one(SessionStopped, Observed, o.Task)
	case *observe.IssueCreated:
		if !ob.inRun(ctx, m.Session) {
			return nil
		}

		e := one(TaskCaptured, Observed, "")
		if ob.TaskID != nil {
			e[0].Task = ob.TaskID(ctx, m.Cwd, o.URL)
			e[0].Payload.Val["id"] = e[0].Task
		}

		return e
	case *observe.PRCreated:
		if !ob.inRun(ctx, m.Session) {
			return nil
		}

		return one(PROpened, Observed, "")
	case *observe.Committed:
		if !ob.inRun(ctx, m.Session) {
			return nil
		}

		return one(Committed, Observed, "")
	case *observe.Pushed:
		if !ob.inRun(ctx, m.Session) {
			return nil
		}

		return one(Pushed, Observed, "")
	case *observe.WaitStarted:
		return one(WaitStarted, Observed, "")
	case *observe.WaitEnded:
		return one(WaitEnded, Observed, "")
	case *observe.PlanWritten:
		return one(PlanSaved, Artifact, o.Task)
	case *observe.PlanArchived:
		return one(PlanSuperseded, Artifact, o.Task)
	case *observe.FindingsWritten:
		return ob.findings(ctx, o)
	case *observe.VerdictWritten:
		return ob.verdict(ctx, o, m)
	}

	return nil
}

// inRun reports whether the session started an f10 skill, so commits and
// pushes in an unrelated session stay out. Unknown, with no database to
// ask, counts as yes: a spooled event can be ignored later, a dropped one
// cannot be recovered.
func (ob *Observer) inRun(ctx context.Context, session string) bool {
	if ob.l == nil {
		return true
	}

	if session == "" {
		return false
	}

	n, err := ob.l.Count(ctx, Filter{Session: session, Kind: RunStarted})

	return err != nil || n > 0
}

// sameStop reports whether the session's last stop row says the same: Stop
// fires at every turn end, and only a change is worth a row.
func (ob *Observer) sameStop(ctx context.Context, session string, o *observe.SessionStopped) bool {
	if ob.l == nil {
		return false
	}

	prior, err := ob.l.List(ctx, Filter{Session: session, Kind: SessionStopped})
	if err != nil || len(prior) == 0 {
		return false
	}

	last := prior[len(prior)-1].Payload.Val

	return last["phase"] == o.Phase && last["status"] == o.Status && last["leaf"] == nonEmpty(o.Leaf)
}

// nonEmpty is how an omitempty field reads back from a payload.
func nonEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}

// findings records a findings file: the round opening once per reviewed
// sha, then each finding whose verdict differs from the last one recorded.
// Review writes the file and resolve edits it, so the diff against the
// ledger decides, not the tool that wrote it.
func (ob *Observer) findings(ctx context.Context, o *observe.FindingsWritten) []Event {
	raw, err := os.ReadFile(o.Path)
	if err != nil {
		return nil
	}

	f, err := findings.Parse(string(raw))
	if err != nil {
		return []Event{{Kind: ReviewRoundOpened, Source: Artifact, Task: o.Task, Payload: col(Payload{
			"path": o.Path, "round": o.Round, "parse_error": err.Error(),
		})}}
	}

	var prior []Event

	if ob.l != nil {
		prior, _ = ob.l.List(ctx, Filter{Task: o.Task})
	}

	var events []Event

	opened := false
	last := map[string]string{} // round/finding -> verdict and status last recorded

	for _, e := range prior {
		switch e.Kind {
		case ReviewRoundOpened:
			if e.Payload.Val["round"] == f.Round && e.Payload.Val["reviewed"] == f.Reviewed {
				opened = true
			}
		case FindingVerdict:
			round, _ := e.Payload.Val["round"].(string)
			id, _ := e.Payload.Val["finding"].(string)
			v, _ := e.Payload.Val["verdict"].(string)
			st, _ := e.Payload.Val["status"].(string)
			last[round+"/"+id] = v + "|" + st
		}
	}

	if !opened {
		cats := map[string]any{}
		for c, n := range f.Categories {
			cats[c] = n
		}

		events = append(events, Event{Kind: ReviewRoundOpened, Source: Artifact, Task: o.Task, Payload: col(Payload{
			"path": o.Path, "round": f.Round, "reviewed": f.Reviewed, "reviewer": f.Reviewer,
			"verdict": f.Verdict, "categories": cats,
		})})
	}

	// keyed by round: a prior-round block reuses its round-one id, and its
	// status is news even when its verdict is not
	for _, fd := range f.Findings {
		if fd.Verdict == "" && fd.Status == "" || last[f.Round+"/"+fd.ID] == fd.Verdict+"|"+fd.Status {
			continue
		}

		p := Payload{"round": f.Round, "finding": fd.ID, "title": fd.Title}
		for key, v := range map[string]string{
			"verdict": fd.Verdict, "category": fd.Category, "reason": fd.Reason, "status": fd.Status,
		} {
			if v != "" {
				p[key] = v
			}
		}

		events = append(events, Event{Kind: FindingVerdict, Source: Artifact, Task: o.Task, Payload: col(p)})
	}

	return events
}

// verdict records a judge's verdict file once per verdict and decision:
// a rewrite in the same session that changes neither adds nothing, a
// decision filled in after a driven ask adds a row. Another session at the
// same path is a new run: numbering restarts in a recreated checkout.
func (ob *Observer) verdict(ctx context.Context, o *observe.VerdictWritten, m observe.Meta) []Event {
	raw, err := os.ReadFile(o.Path)
	if err != nil {
		return nil
	}

	p := Payload{"path": o.Path, "key": o.Key, "n": o.N}

	f, err := verdict.Parse(string(raw))
	if err != nil {
		p["parse_error"] = err.Error()

		task := ""
		if f != nil {
			task = f.Task

			if f.Idea() {
				p["idea"] = true
			}
		}

		return []Event{{Kind: JudgeVerdict, Source: Artifact, Task: task, Payload: col(p)}}
	}

	// the database holds every project's rows: scope the lookup to this one
	if ob.l != nil {
		prior, _ := ob.l.List(ctx, Filter{Project: ob.place(ctx, m.Cwd).Project, Kind: JudgeVerdict})
		for _, e := range slices.Backward(prior) {
			if v := e.Payload.Val; v["path"] == o.Path {
				if e.Session == m.Session && v["verdict"] == f.Verdict && v["decision"] == nonEmpty(f.Decision) {
					return nil
				}

				break
			}
		}
	}

	for key, v := range map[string]string{
		"subject": f.Subject, "verdict": f.Verdict, "reason": f.Reason, "run": f.Run, "decision": f.Decision,
	} {
		if v != "" {
			p[key] = v
		}
	}

	if f.Blind {
		p["blind"] = true
	}

	if f.Idea() {
		p["idea"] = true
	}

	return []Event{{Kind: JudgeVerdict, Source: Artifact, Task: f.Task, Payload: col(p)}}
}

// taskless reports a row that belongs to no task even in a session that has
// one: a verdict on an idea judged mid-task is not that task's verdict.
func taskless(e *Event) bool {
	return e.Kind == JudgeVerdict && e.Payload.Val["idea"] == true
}

// payloadOf is an observation's own fields, Meta left out: the row's
// columns carry it.
func payloadOf(o observe.Observation) payloadCol {
	p := Payload{}

	if raw, err := json.Marshal(o); err == nil {
		_ = json.Unmarshal(raw, &p)
	}

	delete(p, "session")
	delete(p, "cwd")
	delete(p, "at")

	return col(p)
}
