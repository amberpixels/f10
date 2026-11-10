package main

import (
	"context"
	"io"
	"slices"
	"sync/atomic"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// driveInput is one drive: what its command line and project settled, what
// it calls out to, and what the run changes as it goes.
type driveInput struct {
	driveConfig
	driveDeps
	driveState
}

// driveConfig is what the command line, the project and the flow settle
// before the drive starts; nothing changes it after.
type driveConfig struct {
	main string
	root string // the storage root as a path for per-run state: the record, the leases, the asks; "" keeps none
	args driveArgs
	argv []string // the command line as given, for the resume command
	// sequence is the skills a task on fl goes through; nil is driveSkills,
	// read off the flow, and a test runs the machinery on any skill
	sequence func(fl shipFlow) []string
	flow     shipFlow                            // the flow typed, or default; a task's branch may record another
	resolve  func(name string) (shipFlow, error) // a declared flow by name, at the run's pace
	// paced is a flow at another pace, for a branch that records one; nil
	// keeps the run's
	paced    func(fl shipFlow, pace string) (shipFlow, error)
	storage  storage.Root // the same root, for task files: plans, findings, verdicts
	settings string       // claude's settings file, which names the model a bad --model falls back to
	layers   agentMap     // the user's and the project's agents map; nil for none
	stateDir string
	session  string // the session that typed the command, inherited through the daemon's environment
	ttl      time.Duration
	// asksDir is the inbox an ask goes to and answers come from; "" keeps none
	asksDir string
	// daemonRun is a drive a daemon runs that restarts it: an answer that
	// came after it stopped looking ends it ExitResume for that restart
	daemonRun bool
	// defaultParallel is the cap on tasks running a skill at once with none given
	defaultParallel int
}

// driveDeps is what the drive calls out to, which a test replaces.
type driveDeps struct {
	driver  *driver.Driver
	tracker tracker
	title   func(ref.Ref) func(context.Context) (string, error)
	started func(context.Context, ref.Ref) error // nil when tracker.status declares no started word
	finish  func(ctx context.Context, w io.Writer, req driveFinish) error
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	// record puts an event in the ledger, placed in dir; nil records nothing
	record func(ctx context.Context, dir string, e ledger.Event)
	// pull reads a branch's PR on the host when the drive reconciles a
	// task; nil, with no host, reads none
	pull func(ctx context.Context, branch string) (forge.PR, error)
	// catchup is the engine the drive catches a branch up through
	catchup *catchupEngine
	notify  func(context.Context, asks.Ask) error // tells the user of a new ask
	out     io.Writer
}

// driveState is what the run changes as it goes.
type driveState struct {
	answers  answerBook                  // --answer and the inbox's answers, until a task takes each
	profiles map[string]agentctl.Profile // each profile as typed, resolved over the layers; "" is the map's default

	rec    *drives.Record           // this drive's record; nil with no root
	own    *drives.Lease            // the drive's own lease, held while it runs
	leases map[string]*drives.Lease // each task's, by key, held while the task is unended

	links      *taskLinks // prints this drive's task ids for people
	project    string     // the name the links print task ids under, read once they load
	inboxNotes []string
	pullLine   string // the last line retryPull printed, or a finish's deferred note
	ranFinish  bool   // a finish ran here, so this drive may pull main

	// parallel caps the tasks running a skill at once, and twice it the
	// agents open; 0 caps nothing
	parallel int
	wss      []agentctl.Workspace // the last look's workspaces, for the cap's working check
	tick     *tickView            // this tick's look; before the first, one with no workspaces

	draining atomic.Bool // set by SIGUSR1: stop at the next tick, ending no task
	launched bool        // a session typed this drive: not a --resume, not an --answer relay
	runs     []*driveRun
}

// A driveTask is one task of the list, as the validation pass found it.
type driveTask struct {
	ref      ref.Ref
	suffix   string
	info     taskInfo
	profile  agentctl.Profile // what the agent starts on, resolved
	flagged  bool             // the command line chose it, over the branch's record
	branch   string           // the task's local branch, or "" before it started
	after    string           // the base the body's After: line names, as an id
	base     string           // the base the drive waits for: the branch's record, else after
	finished bool
}

// A driveResult is how one task ended: its row in the report, and when it
// stopped, the exit code and the lines beneath the report.
type driveResult struct {
	row   string
	code  int
	lines []string
	// skill and reason name a halt, for the ledger
	skill  string
	reason string
	// fromRun is a halt the run itself reported, already in the ledger
	fromRun bool
	// drives is an end this drive gave the task, owned elsewhere or
	// cancelled, which says nothing about the task itself
	drives bool
	// ask is the question a ui.ExitAsk end waits on, for the inbox
	ask string
	// attempts are the failures a halt on the attempt rule counted
	attempts []attempt
}

// A driveRun is one task on its way through the drive: pending until its
// base has a commit, then active with one skill in flight or waiting at
// finish for its base, then ended with its row.
type driveRun struct {
	turn
	catchupState

	task     driveTask
	base     *driveRun // the listed base, or nil for none or one outside the list
	o        openedTask
	phase    runPhase
	flow     shipFlow // the flow this task ships through: its branch's record, else the run's
	finishes bool     // drive ends the task with finish: the pace says so, or a spike closes
	pos      int      // the index in its skills of the skill in flight, or the next one
	end      *driveResult
	// freshAt is when this drive opened the task to start its agent: run
	// state older than it was written by an agent that is gone, and is not read
	freshAt time.Time
	// parkedHead is the base's head when a ship parked itself, so only a
	// commit after it sends ship again, never the same empty base; waitHead
	// is the base head the last park read, so a base that committed before
	// the drive saw the stop is sent once, never looped
	parkedHead, waitHead string
	// parkedOrigin is set while a ship that stopped before pr waits for its
	// base's branch on origin, which the PR targets, not for its code
	parkedOrigin bool
	// attempts are the failures in a row of the action in flight
	attempts []attempt
	// job is the verify the drive runs after its own catchup, polled in
	// place of the agent while the phase is verifying
	job *verifyJob
}

// A turn is the skill in flight and what the poll has seen of it; each send
// starts a new one.
type turn struct {
	skill  string // one of the task's skills, or catchup
	sentAt time.Time
	begun  bool
	// attached is set when the drive found the turn already running, so the
	// skill it reports, not the one the drive meant to send, says how it ended
	attached  bool
	idleSince time.Time
	// typed is the send's text when it went through the agent's input box,
	// "" when it went on the command line, and typedTo the agent that took
	// it; enterRetried is set once the poll checked that box
	typed        string
	typedTo      string
	enterRetried bool
	// retry marks a send after a failed attempt: it prompts whatever the run
	// says, and holds across sends until a prompt goes out
	retry bool
}

// catchupState is what the drive's catchups keep across turns.
type catchupState struct {
	// lastCatchup is the catchup this drive last sent for the task, kept on
	// the drive record: the push after it leases against its origin tip, and
	// a conflict after it counts by its fingerprint
	lastCatchup *drives.Catchup
	// catchupPlan is the base the drive's own catchup integrated, and
	// catchupLeft what git showed unsettled after the agent's last turn
	catchupPlan *catchupPlan
	catchupLeft string
	// toldCatchup is set once the line saying why the agent verifies was printed
	toldCatchup bool
}

// runPhase is where an opened or held task stands between polls.
type runPhase int

const (
	atPending   runPhase = iota // not opened: its base has no commit, or it was never reached
	atHeld                      // not opened: the cap holds it
	atRunning                   // a skill in flight, or about to be sent
	atQueued                    // opened, its next skill held by the cap
	atVerifying                 // the drive's own verify after a catchup runs, in job
	atRetrying                  // an attempt failed: the next poll sends the action again
	atParked                    // a skill waits for its base's code
	atWaiting                   // finish waits for its base to be through
)

// recorded is the phase as the drive record names it.
func (p runPhase) recorded() string {
	switch p {
	case atPending:
		return drives.PhasePending
	case atHeld, atQueued:
		return drives.PhaseQueued
	case atRunning:
		return drives.PhaseRunning
	case atVerifying:
		return drives.PhaseVerifying
	case atRetrying:
		return drives.PhaseRetrying
	case atParked:
		return drives.PhaseParked
	case atWaiting:
		return drives.PhaseWaiting
	}

	return drives.PhasePending
}

func (r *driveRun) opened() bool { return r.phase != atPending && r.phase != atHeld }

func (r *driveRun) active() bool { return r.opened() && r.end == nil }

// queued: the cap holds the task, opened or not.
func (r *driveRun) queued() bool { return r.phase == atHeld || r.phase == atQueued }

// enter moves r to phase p and writes its standing to the record.
func (in *driveInput) enter(r *driveRun, p runPhase) {
	if r.phase == p {
		return
	}

	r.phase = p
	in.saveTask(r, func(t *drives.Task) { t.Phase, t.Queued = p.recorded(), r.queued() })
}

// skills is what drive prompts r through, in order; a spike its pace does
// not finish gets finish once it closes.
func (in *driveInput) skills(r *driveRun) []string {
	skills := in.sequenceOf(r.flow)
	if r.finishes && !slices.Contains(skills, "finish") {
		skills = append(slices.Clip(skills), "finish")
	}

	return skills
}

func (in *driveInput) sequenceOf(fl shipFlow) []string {
	if in.sequence != nil {
		return in.sequence(fl)
	}

	return driveSkills(fl)
}

// driveSkills reads the skills off the flow: plan when the flow leads with
// it, ship, then finish when the pace finishes.
func driveSkills(fl shipFlow) []string {
	var skills []string
	if fl.plans() {
		skills = append(skills, "plan")
	}

	skills = append(skills, "ship")
	if fl.finishes() {
		skills = append(skills, "finish")
	}

	return skills
}

// current is run unless its agent is gone, as for a run a cancelled start
// left behind in a checkout this drive gave a new agent.
func (r *driveRun) current(run *state.Run) *state.Run {
	if run == nil || run.Updated.Before(r.freshAt) {
		return nil
	}

	return run
}

// through reports a task done with for its dependents: finished, or its
// every skill run.
func (r *driveRun) through() bool { return r.end != nil && r.end.code == 0 }
