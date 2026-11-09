package main

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// driveInput is everything drive needs once the target is settled, so the
// flow can be tested without a checkout, a clock or a wait.
type driveInput struct {
	main    string
	root    string // the storage root as a path for per-run state: the record, the leases, the asks; "" keeps none
	driver  *driver.Driver
	tracker tracker
	args    driveArgs
	argv    []string // the command line as given, for the resume command
	chain   []string
	flow    shipFlow                            // the flow typed, or default; a task's branch may record another
	resolve func(name string) (shipFlow, error) // a declared flow by name, at the run's pace
	// paced is a flow at another pace, for a branch that records one; nil
	// keeps the run's
	paced    func(fl shipFlow, pace string) (shipFlow, error)
	storage  storage.Root // the same root, for task files: plans, findings, verdicts
	settings string       // claude's settings file, which names the model a bad --model falls back to
	layers   agentMap     // the user's and the project's agents map; nil for none
	title    func(ref.Ref) func(context.Context) (string, error)
	started  func(context.Context, ref.Ref) error // nil when tracker.status declares no started word
	finish   func(ctx context.Context, w io.Writer, req driveFinish) error
	stateDir string
	session  string // the session that typed the command, inherited through the daemon's environment
	ttl      time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	// record puts an event in the ledger, placed in dir; nil records nothing
	record func(ctx context.Context, dir string, e ledger.Event)
	// pull reads a branch's PR on the host when the drive reconciles a
	// task; nil, with no host, reads none
	pull func(ctx context.Context, branch string) (pull, error)
	// catchup is the catchup the drive runs itself; nil sends the whole
	// catchup to the agent, for catchupWhy
	catchup    *binaryCatchup
	catchupWhy string

	answer   string                      // a bare --answer, until the first task stopped on a question takes it
	answers  map[string]string           // --answer ID=text, by task id, until that task takes it
	profiles map[string]agentctl.Profile // each profile as typed, resolved over the layers; "" is the map's default

	rec    *drives.Record           // this drive's record; nil with no root
	own    *drives.Lease            // the drive's own lease, held while it runs
	leases map[string]*drives.Lease // each task's, by key, held while the task is unended

	// asksDir is the inbox an ask goes to and answers come from; "" keeps
	// none. notify tells the user of a new ask; resume starts this drive
	// again for an answer that came after it stopped looking.
	asksDir string
	notify  func(context.Context, asks.Ask) error
	resume  func(ctx context.Context, n int) error

	// links prints this drive's task ids for people, under project
	links   *taskLinks
	project string

	// answeredIn is when a task's ask was answered where the agent heard
	// it: the drive follows that turn instead of sending the skill again
	answeredIn map[string]time.Time
	inboxNotes []string
	pullLine   string // the last line retryPull printed, or a finish's deferred note
	ranFinish  bool   // a finish ran here, so this drive may pull main

	// parallel caps the tasks running a skill at once, and twice it the
	// agents open; 0 caps nothing. defaultParallel is the cap with none given.
	parallel        int
	defaultParallel int
	wss             []agentctl.Workspace // the last look's workspaces, for the cap's working check

	draining atomic.Bool // set by SIGUSR1: stop at the next tick, ending no task
	launched bool        // a session typed this drive: not a --resume, not an --answer relay
	runs     []*driveRun
	out      io.Writer
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
	// ask is the question an exitAsk end waits on, for the inbox
	ask string
	// attempts are the failures a halt on the attempt rule counted
	attempts []attempt
}

// A driveRun is one task on its way through the drive: pending until its
// base has a commit, then active with one chain element in flight or
// waiting at finish for its base, then ended with its row.
type driveRun struct {
	task      driveTask
	base      *driveRun // the listed base, or nil for none or one outside the list
	o         openedTask
	opened    bool
	flow      shipFlow // the flow this task ships through: its branch's record, else the run's
	chain     []string // what the task runs through on that flow
	pos       int      // the chain element in flight, or the next one
	skill     string   // what is in flight: a chain skill, or catchup
	sentAt    time.Time
	begun     bool
	idleSince time.Time
	// attached is set when the drive found the turn already running, so the
	// skill it reports, not the one the drive meant to send, says how it ended
	attached bool
	end      *driveResult
	// freshAt is when this drive opened the task to start its agent: run
	// state older than it was written by an agent that is gone, and is not read
	freshAt time.Time
	// lastCatchup is the catchup this drive last sent for the task, so a
	// conflict after it is judged by progress, not counted: the drive
	// catches up again while the target moves, and halts once it stops
	lastCatchup *catchupSent
	// waiting is set while finish waits for the base to be through: no turn
	// is in flight, so the poll only asks next again
	waiting bool
	// parked is set while a chain skill waits for its base's code, the same
	// way. parkedHead is the base's head when a ship parked itself, so only
	// a commit after it sends ship again, never the same empty base.
	parked     bool
	parkedHead string
	// waitHead is the base head the last park read, so a base that
	// committed before the drive saw the stop is sent once, never looped
	waitHead string
	// queued is set while the drive's cap holds the task: unopened, or
	// open with its next skill unsent
	queued bool
	// typed is the last send's text when it went through the agent's input
	// box, "" when it went on the command line, and typedTo the agent that
	// took it; enterRetried is set once the poll checked that box
	typed        string
	typedTo      string
	enterRetried bool
	// attempts are the failures in a row of the action in flight; resend
	// sends it again at the next poll, and retry marks that send, so it
	// prompts whatever the run says
	attempts []attempt
	resend   bool
	retry    bool
	// verifying is the verify the drive runs after its own catchup, polled
	// in place of the agent; toldCatchup is set once the line saying why the
	// agent catches up was printed
	verifying   *verifyJob
	toldCatchup bool
	// catchupPlan is the base the drive's own catchup integrated, and
	// catchupLeft what git showed unsettled after the agent's last turn
	catchupPlan *catchupPlan
	catchupLeft string
}

func (r *driveRun) active() bool { return r.opened && r.end == nil }

// current is run unless its agent is gone, as for a run a cancelled start
// left behind in a checkout this drive gave a new agent.
func (r *driveRun) current(run *state.Run) *state.Run {
	if run == nil || run.Updated.Before(r.freshAt) {
		return nil
	}

	return run
}

// through reports a task done with for its dependents: finished, or its
// whole chain run.
func (r *driveRun) through() bool { return r.end != nil && r.end.code == 0 }
