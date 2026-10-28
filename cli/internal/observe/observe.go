// Package observe turns what a hook sees - a Bash command and its output, a
// file written - into typed facts about a run: an issue or PR opened, a
// commit, a push, a step loaded, a plan or findings file saved.
//
// It only recognises; it never acts. Observers do: the badge reads a few of
// these to settle what the model would otherwise have to report, and the
// ledger persists all of them.
package observe

import (
	"context"
	"time"
)

// Meta is what every observation carries: the session it happened in, the
// directory it ran from, and when the hook saw it.
type Meta struct {
	Session string    `json:"session"`
	Cwd     string    `json:"cwd,omitempty"`
	At      time.Time `json:"at"`
}

func (m *Meta) meta() *Meta { return m }

// An Observation is one fact. The set is closed: only this package's types
// satisfy it, so an observer's type switch can be exhaustive.
type Observation interface {
	Kind() string
	meta() *Meta
}

// Stamp fills an observation's Meta.
func Stamp(o Observation, session, cwd string, at time.Time) {
	m := o.meta()
	m.Session, m.Cwd, m.At = session, cwd, at
}

// MetaOf reads an observation's Meta.
func MetaOf(o Observation) Meta { return *o.meta() }

// An Observer receives observations, one at a time, in the order a hook saw
// them. An error is the observer's own; the hook carries on.
type Observer interface {
	Observe(ctx context.Context, o Observation) error
}

// IssueCreated is a tracker issue opened with `gh issue create` or
// `glab issue create`, seen by the url it printed.
type IssueCreated struct {
	Meta

	Host string `json:"host"` // gh or glab
	URL  string `json:"url"`
}

// PRCreated is a PR or MR opened with `gh pr create` or `glab mr create`.
type PRCreated struct {
	Meta

	Host string `json:"host"`
	URL  string `json:"url"`
}

// Committed is a `git commit` that made a commit, seen by its
// `[branch sha] subject` line.
type Committed struct {
	Meta

	Branch  string `json:"branch"`
	SHA     string `json:"sha"` // abbreviated, as git printed it
	Subject string `json:"subject"`
}

// Pushed is a `git push` that moved at least one ref.
type Pushed struct {
	Meta

	Remote string   `json:"remote,omitempty"` // the `To <remote>` line
	Refs   []string `json:"refs"`             // each `src -> dst` as printed
}

// StepStarted is `f10-load-project.sh` loading steps: ship loads each
// pipeline step right before it runs, so one step named is that step
// starting. No steps is a load for a pipeline with no step to name.
type StepStarted struct {
	Meta

	Steps []string `json:"steps"`
}

// Finished is an `f10 finish` call, with its arguments.
type Finished struct {
	Meta

	Args []string `json:"args"`
}

// PlanWritten is a plan file saved under the storage root's plans/.
type PlanWritten struct {
	Meta

	Path string `json:"path"`
	Task string `json:"task"`
}

// FindingsWritten is a review's findings file saved under the storage root's
// reviews/<task>/.
type FindingsWritten struct {
	Meta

	Path  string `json:"path"`
	Task  string `json:"task"`
	Round string `json:"round"`
}

// VerdictWritten is a judge's verdict file saved under the storage root's
// verdicts/<key>/, key the task id or, for an idea, a slug.
type VerdictWritten struct {
	Meta

	Path string `json:"path"`
	Key  string `json:"key"`
	N    string `json:"n"`
}

func (*IssueCreated) Kind() string    { return "issue_created" }
func (*PRCreated) Kind() string       { return "pr_created" }
func (*Committed) Kind() string       { return "committed" }
func (*Pushed) Kind() string          { return "pushed" }
func (*StepStarted) Kind() string     { return "step_started" }
func (*Finished) Kind() string        { return "finished" }
func (*PlanWritten) Kind() string     { return "plan_written" }
func (*FindingsWritten) Kind() string { return "findings_written" }
func (*VerdictWritten) Kind() string  { return "verdict_written" }

// SkillStarted is an f10 skill starting: typed, which Claude Code expands,
// or called by the model through the Skill tool.
type SkillStarted struct {
	Meta

	Skill string `json:"skill"`
	Args  string `json:"args,omitempty"`
}

// SessionStopped is a turn ending with a run in the session, at the phase,
// status and leaf its state held before the Stop backstop ran. Backstop says
// the backstop then promoted it to done.
type SessionStopped struct {
	Meta

	Task     string `json:"task,omitempty"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Leaf     string `json:"leaf,omitempty"`
	Backstop bool   `json:"backstop,omitempty"`
}

// PlanArchived is a plan moved into the storage root's plans/archive/, the
// plan step's way of superseding it.
type PlanArchived struct {
	Meta

	From string `json:"from"`
	To   string `json:"to"`
	Task string `json:"task"`
}

func (*SkillStarted) Kind() string   { return "skill_started" }
func (*SessionStopped) Kind() string { return "session_stopped" }
func (*PlanArchived) Kind() string   { return "plan_archived" }

// Wait reasons: a turn ended and the user has not answered, an
// AskUserQuestion open, a permission dialog open.
const (
	WaitTurn       = "turn"
	WaitQuestion   = "question"
	WaitPermission = "permission"
)

// WaitStarted is a run starting to wait on the user. Tool names the tool a
// question or permission dialog belongs to.
type WaitStarted struct {
	Meta

	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
}

// WaitEnded is that wait over, By the hook event that ended it.
type WaitEnded struct {
	Meta

	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
	By     string `json:"by"`
}

func (*WaitStarted) Kind() string { return "wait_started" }
func (*WaitEnded) Kind() string   { return "wait_ended" }
