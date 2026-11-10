// Package drive runs a list of tasks through their flow from one process: each
// in its own worktree and agent, dependents once their base has code, halting
// when an agent asks and nothing else can move; and the watcher's passes over
// tasks no drive owns.
package drive

import (
	"context"
	"io"
	"slices"
	"sync/atomic"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// DriveInput is one drive: what its command line and project settled, what
// it calls out to, and what the run changes as it goes.
type DriveInput struct {
	driveConfig
	driveDeps
	driveState
}

// Enter moves r to phase p and writes its standing to the record.
func (in *DriveInput) Enter(r *DriveRun, p runPhase) {
	if r.Phase == p {
		return
	}

	r.Phase = p
	in.saveTask(r, func(t *drives.Task) { t.Phase, t.Queued = p.recorded(), r.Queued() })
}

// Skills is what drive prompts r through, in order; a spike its pace does
// not finish gets finish once it closes.
func (in *DriveInput) Skills(r *DriveRun) []string {
	skills := in.SequenceOf(r.Flow)
	if r.Finishes && !slices.Contains(skills, "finish") {
		skills = append(slices.Clip(skills), "finish")
	}

	return skills
}

func (in *DriveInput) SequenceOf(fl runplan.ShipFlow) []string {
	if in.Sequence != nil {
		return in.Sequence(fl)
	}

	return DriveSkills(fl)
}

// driveConfig is what the command line, the project and the flow settle
// before the drive starts; nothing changes it after.
type driveConfig struct {
	Main string
	Root string // the storage root as a path for per-run state: the record, the leases, the asks; "" keeps none
	Args driverec.DriveArgs
	Argv []string // the command line as given, for the resume command
	// Sequence is the skills a task on fl goes through; nil is driveSkills,
	// read off the flow, and a test runs the machinery on any skill
	Sequence func(fl runplan.ShipFlow) []string
	Flow     runplan.ShipFlow                            // the flow typed, or default; a task's branch may record another
	Resolve  func(name string) (runplan.ShipFlow, error) // a declared flow by name, at the run's pace
	// Paced is a flow at another pace, for a branch that records one; nil
	// keeps the run's
	Paced    func(fl runplan.ShipFlow, pace string) (runplan.ShipFlow, error)
	Storage  storage.Root     // the same root, for task files: plans, findings, verdicts
	Settings string           // claude's settings file, which names the model a bad --model falls back to
	Layers   runplan.AgentMap // the user's and the project's agents map; nil for none
	StateDir string
	Session  string // the session that typed the command, inherited through the daemon's environment
	TTL      time.Duration
	// AsksDir is the inbox an ask goes to and answers come from; "" keeps none
	AsksDir string
	// DaemonRun is a drive a daemon runs that restarts it: an answer that
	// came after it stopped looking ends it ExitResume for that restart
	DaemonRun bool
	// DefaultParallel is the cap on tasks running a skill at once with none given
	DefaultParallel int
}

// driveDeps is what the drive calls out to, which a test replaces.
type driveDeps struct {
	Driver  *driver.Driver
	Tracker stack.Tracker
	Title   func(ref.Ref) func(context.Context) (string, error)
	Started func(context.Context, ref.Ref) error // nil when tracker.status declares no started word
	Finish  func(ctx context.Context, w io.Writer, req DriveFinish) error
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
	// Record puts an event in the ledger, placed in dir; nil records nothing
	Record func(ctx context.Context, dir string, e ledger.Event)
	// Pull reads a branch's PR on the host when the drive reconciles a
	// task; nil, with no host, reads none
	Pull func(ctx context.Context, branch string) (forge.PR, error)
	// Catchup is the engine the drive catches a branch up through
	Catchup *stack.CatchupEngine
	Notify  func(context.Context, asks.Ask) error // tells the user of a new ask
	Out     io.Writer
}

// driveState is what the run changes as it goes.
type driveState struct {
	Answers  answerBook                  // --answer and the inbox's answers, until a task takes each
	profiles map[string]agentctl.Profile // each profile as typed, resolved over the layers; "" is the map's default

	Rec    *drives.Record           // this drive's record; nil with no root
	own    *drives.Lease            // the drive's own lease, held while it runs
	leases map[string]*drives.Lease // each task's, by key, held while the task is unended

	links      *render.TaskLinks // prints this drive's task ids for people
	project    string            // the name the links print task ids under, read once they load
	inboxNotes []string
	PullLine   string // the last line retryPull printed, or a finish's deferred note
	RanFinish  bool   // a finish ran here, so this drive may pull main

	// Parallel caps the tasks running a skill at once, and twice it the
	// agents open; 0 caps nothing
	Parallel int
	Wss      []agentctl.Workspace // the last look's workspaces, for the cap's working check
	Tick     *TickView            // this tick's look; before the first, one with no workspaces

	Draining atomic.Bool // set by SIGUSR1: stop at the next tick, ending no task
	launched bool        // a session typed this drive: not a --resume, not an --answer relay
	Runs     []*DriveRun
}

// A DriveTask is one task of the list, as the validation pass found it.
type DriveTask struct {
	Ref      ref.Ref
	Suffix   string
	info     hostops.TaskInfo
	profile  agentctl.Profile // what the agent starts on, resolved
	flagged  bool             // the command line chose it, over the branch's record
	Branch   string           // the task's local branch, or "" before it started
	after    string           // the base the body's After: line names, as an id
	Base     string           // the base the drive waits for: the branch's record, else after
	finished bool
}

// A DriveResult is how one task ended: its row in the report, and when it
// stopped, the exit code and the lines beneath the report.
type DriveResult struct {
	Row   string
	Code  int
	Lines []string
	// Skill and reason name a halt, for the ledger
	Skill  string
	Reason string
	// fromRun is a halt the run itself reported, already in the ledger
	fromRun bool
	// Drives is an end this drive gave the task, owned elsewhere or
	// cancelled, which says nothing about the task itself
	Drives bool
	// ask is the question a ui.ExitAsk end waits on, for the inbox
	ask string
	// Attempts are the failures a halt on the attempt rule counted
	Attempts []attempt
}

// A DriveRun is one task on its way through the drive: pending until its
// base has a commit, then active with one skill in flight or waiting at
// finish for its base, then ended with its row.
type DriveRun struct {
	turn
	catchupState

	Task     DriveTask
	Base     *DriveRun // the listed base, or nil for none or one outside the list
	O        start.OpenedTask
	Phase    runPhase
	Flow     runplan.ShipFlow // the flow this task ships through: its branch's record, else the run's
	Finishes bool             // drive ends the task with finish: the pace says so, or a spike closes
	Pos      int              // the index in its skills of the skill in flight, or the next one
	End      *DriveResult
	// freshAt is when this drive opened the task to start its agent: run
	// state older than it was written by an agent that is gone, and is not read
	freshAt time.Time
	// parkedHead is the base's head when a ship parked itself, so only a
	// commit after it sends ship again, never the same empty base; waitHead
	// is the base head the last park read, so a base that committed before
	// the drive saw the stop is sent once, never looped
	parkedHead, WaitHead string
	// ParkedOrigin is set while a ship that stopped before pr waits for its
	// base's branch on origin, which the PR targets, not for its code
	ParkedOrigin bool
	// attempts are the failures in a row of the action in flight
	attempts []attempt
	// Job is the verify the drive runs after its own catchup, polled in
	// place of the agent while the phase is verifying
	Job *verifyJob
}

func (r *DriveRun) Opened() bool { return r.Phase != atPending && r.Phase != AtHeld }

// Queued: the cap holds the task, opened or not.
func (r *DriveRun) Queued() bool { return r.Phase == AtHeld || r.Phase == atQueued }

func (r *DriveRun) active() bool { return r.Opened() && r.End == nil }

// current is run unless its agent is gone, as for a run a cancelled start
// left behind in a checkout this drive gave a new agent.
func (r *DriveRun) current(run *state.Run) *state.Run {
	if run == nil || run.Updated.Before(r.freshAt) {
		return nil
	}

	return run
}

// through reports a task done with for its dependents: finished, or its
// every skill run.
func (r *DriveRun) through() bool { return r.End != nil && r.End.Code == 0 }

// A turn is the skill in flight and what the poll has seen of it; each send
// starts a new one.
type turn struct {
	Skill  string // one of the task's skills, or catchup
	SentAt time.Time
	begun  bool
	// attached is set when the drive found the turn already running, so the
	// skill it reports, not the one the drive meant to send, says how it ended
	attached  bool
	idleSince time.Time
	// Typed is the send's text when it went through the agent's input box,
	// "" when it went on the command line, and typedTo the agent that took
	// it; enterRetried is set once the poll checked that box
	Typed        string
	typedTo      string
	enterRetried bool
	// retry marks a send after a failed attempt: it prompts whatever the run
	// says, and holds across sends until a prompt goes out
	retry bool
}

// catchupState is what the drive's catchups keep across turns.
type catchupState struct {
	// LastCatchup is the catchup this drive last sent for the task, kept on
	// the drive record: the push after it leases against its origin tip, and
	// a conflict after it counts by its fingerprint
	LastCatchup *drives.Catchup
	// catchupPlan is the base the drive's own catchup integrated, and
	// catchupLeft what git showed unsettled after the agent's last turn
	catchupPlan *stack.CatchupPlan
	catchupLeft string
	// toldCatchup is set once the line saying why the agent verifies was printed
	toldCatchup bool
}

// runPhase is where an opened or held task stands between polls.
type runPhase int

// recorded is the phase as the drive record names it.
func (p runPhase) recorded() string {
	switch p {
	case atPending:
		return drives.PhasePending
	case AtHeld, atQueued:
		return drives.PhaseQueued
	case AtRunning:
		return drives.PhaseRunning
	case atVerifying:
		return drives.PhaseVerifying
	case atRetrying:
		return drives.PhaseRetrying
	case AtParked:
		return drives.PhaseParked
	case AtWaiting:
		return drives.PhaseWaiting
	}

	return drives.PhasePending
}

const (
	atPending   runPhase = iota // not opened: its base has no commit, or it was never reached
	AtHeld                      // not opened: the cap holds it
	AtRunning                   // a skill in flight, or about to be sent
	atQueued                    // opened, its next skill held by the cap
	atVerifying                 // the drive's own verify after a catchup runs, in job
	atRetrying                  // an attempt failed: the next poll sends the action again
	AtParked                    // a skill waits for its base's code
	AtWaiting                   // finish waits for its base to be through
)

// DriveSkills reads the skills off the flow: plan when the flow leads with
// it, ship, then finish when the pace finishes.
func DriveSkills(fl runplan.ShipFlow) []string {
	var skills []string
	if fl.Plans() {
		skills = append(skills, "plan")
	}

	skills = append(skills, "ship")
	if fl.Finishes() {
		skills = append(skills, "finish")
	}

	return skills
}
