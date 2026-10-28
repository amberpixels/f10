package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The drive verb runs a list of tasks through a chain of skills and exits
// when every task is through, or one needs a human. The tasks run as their
// dependencies allow: each waits for its one base to finish, and every task
// whose base is finished runs at once, since each agent is its own session.
// It is Go rather than a Claude session because ordering, prompting and
// polling are deterministic, and a session polling an agent pays a round
// trip and a slice of context per check.
//
// The chain is derived from the flow, never declared: plan when the flow
// has a plan entry, ship, which runs every other entry, and finish when
// drive_finish is on. start and finish create and destroy the worktree and
// are this binary's own, so drive runs them in process; plan and ship go to
// the task's agent as prompts. After each prompt it polls Herdr's agent
// status for the turn's start and end, and the task's run state for the
// outcome.
//
// Rerunning the same command, or --resume with the record's number, is the
// resume. A finished task is skipped, a started one reads its position from
// its branch, and an ask is answered with --answer, one per asking task.
func driveCommand() *cli.Command {
	return &cli.Command{
		Name:  "drive",
		Usage: "run a list of tasks through a flow, each once its base has a commit, stopping when an agent asks",
		ArgsUsage: "<id>[:model]... | <id> -- <id>  [--flow NAME | --local] " +
			"[--model MODEL] [--answer [ID=]TEXT ...] [--every DURATION] [--foreground]\n" +
			"   f10 drive --resume N [--answer [ID=]TEXT ...] | list [--all] | cancel N",
		Description: "Each task runs the default flow, or the one --flow or --local names (f10 flow lists them): " +
			"plan when the flow has a plan entry and the task no plan file, then ship, " +
			"then finish when drive_finish is on. A rerun keeps the flow a task's branch records.\n" +
			"A task's base is its branch's recorded --after, else its body's After: line; " +
			"every task whose base branch has a commit runs at once, its finish waits for the base's, " +
			"and a halt stops only its dependents.\n" +
			"--model starts every agent on MODEL; <id>:<model> starts one task's agent on its own, which wins.\n" +
			"Each drive keeps a record under the storage root, picked up by the same command or --resume N, " +
			"and holds a lease on each task: a task another drive holds is skipped, and f10 finish refuses it. " +
			"list shows the drives; cancel N stops one from prompting further.\n" +
			"The drive runs in the per-user f10 daemon, started when none runs: this command prints its progress, " +
			"Ctrl+C stops only that, and rerunning it follows the drive again. --foreground runs it in this process.\n" +
			"Exit 0: every task went through its chain. 4: an agent asks; rerun with --answer. " +
			"5: a task halted (a judge stop, a failure, an agent that stopped without reporting, its base halted).",
		// a range is `GH-12 -- GH-15`, and flag parsing would swallow the --
		SkipFlagParsing: true,
		Action:          runDrive,
		ShellComplete:   completeFlow,
	}
}

// Exit codes beyond start's: an ask the skill relays, and any other halt.
const (
	exitAsk    = 4
	exitHalted = 5
	// exitDrained is a drive stopped for a daemon upgrade, its record kept
	// for the new daemon to resume; only the daemon and its clients see it
	exitDrained = daemon.ExitDrained
)

const (
	// defaultEvery is the fallback look: a run-state write wakes a drive
	// sooner (drivewait.go)
	defaultEvery = 30 * time.Second
	// promptSettle is how old a prompt mark must be before an idle agent
	// beside it reads as a turn that ended, not one Herdr has yet to show
	promptSettle = 5 * time.Second
	// startWindow is how long a prompted agent has to show it took the
	// prompt: Herdr reporting it working, or its run reporting.
	startWindow = 2 * time.Minute
	// stallGrace absorbs the Stop hook, which promotes a finished phase
	// asynchronously after the agent already reads idle.
	stallGrace = time.Minute
	// maxSpan caps a range, since every id in it costs a tracker call.
	maxSpan = 50
	// cfgDrive is the last chain skill drive saw a task finish, under
	// `branch.<name>.` beside f10-after: local, and deleted with the branch.
	cfgDrive = "f10-drive"
	// cfgChain is the chain drive runs the task through, space-joined.
	cfgChain = "f10-chain"
)

// oldChainSkills are the words a typed chain used, refused with a pointer
// at what replaced it.
var oldChainSkills = []string{"plan", "judge", "ship", "review", "resolve", "finish"}

// driveChain is what drive runs a task through on a flow.
func driveChain(fl shipFlow, finish bool) []string {
	var chain []string
	if fl.plans() {
		chain = append(chain, "plan")
	}

	chain = append(chain, "ship")
	if finish {
		chain = append(chain, "finish")
	}

	return chain
}

func runDrive(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	a, err := parseDriveArgs(argv)
	if err != nil {
		return err
	}

	if a.list || a.cancel > 0 {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		if a.list {
			return driveList(cmd.Writer, t.sharedRoot(), a.all)
		}

		if err := driveCancel(cmd.Writer, t.sharedRoot(), a.cancel, time.Now()); err != nil {
			return err
		}

		closeDriveAsks(asks.Dir(), t.sharedRoot(), a.cancel, time.Now())

		return nil
	}

	// the daemon may drain this drive before it opens anything: the signal
	// waits in the channel until the loop exists to take it
	drain, unwatch := watchDrain(a.fore)
	defer unwatch()

	if err := agents("").Available(); err != nil {
		return err
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	// the drive outlives this process unless asked to run in it
	if !a.fore && daemon.Supported {
		return driveThroughDaemon(ctx, cmd.Writer, t, argv, a)
	}

	// a resume runs the record's own command line, with the answers and
	// --every given now
	if a.resume > 0 {
		rec, err := drives.Read(t.sharedRoot(), a.resume)
		if err != nil {
			return err
		}

		given := a
		if a, err = parseDriveArgs(rec.Args); err != nil {
			return fmt.Errorf("drive %d: %w", rec.Number, err)
		}

		a.resume, a.answers, a.every, a.fore = given.resume, given.answers, given.every, given.fore
		argv = rec.Args
	}

	fl, err := resolveShipFlow(t.eff, a.flow)
	if err != nil {
		return err
	}

	chain := driveChain(fl, t.eff.DriveFinish)

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	drv := t.driver()
	h, hostErr := t.host()

	if slices.Contains(chain, "finish") && hostErr != nil {
		return fmt.Errorf("drive_finish is on, and finish merges through the host: %w", hostErr)
	}

	in := &driveInput{
		main:     main,
		root:     t.sharedRoot(),
		driver:   drv,
		tracker:  t.tracker(),
		args:     a,
		argv:     argv,
		chain:    chain,
		flow:     fl,
		resolve:  func(name string) (shipFlow, error) { return resolveShipFlow(t.eff, name) },
		plansDir: t.sharedPlansDir(),
		settings: claudeSettingsPath(),
		title:    func(r ref.Ref) func(context.Context) (string, error) { return titleLookup(t, r.Number) },
		started:  startedMove(t),
		record:   recordAt,
		finish: func(ctx context.Context, w io.Writer, task ref.Ref, pr string, progress func(string)) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getwd: %w", err)
			}

			return finish(ctx, w, finishInput{
				main:      main,
				driver:    drv,
				task:      task,
				host:      h.at(main),
				method:    t.eff.MergeMethod,
				cwd:       cwd,
				workspace: os.Getenv("HERDR_WORKSPACE_ID"),
				merge:     pr == finishMerge,
				local:     pr == finishLocal,
				progress:  progress,
				plansDir:  t.sharedPlansDir(),
				title:     titleLookup(t, task.Number),
				// a driven finish asks no second question, since a resume
				// carries only the latest answer: a bundle's riders stay
				// open, the reversible choice, and the report names them
				close: []string{},
			})
		},
		stateDir: state.Dir(),
		session:  state.Session(),
		ttl:      state.TTL(),
		now:      time.Now,
		asksDir:  asks.Dir(),
		notify:   askNotifier,
	}
	in.sleep = in.wake
	in.resume = in.resumeSelf

	if drain != nil {
		go func() {
			for range drain {
				in.draining.Store(true)
			}
		}()
	}

	return drive(ctx, cmd.Writer, in)
}

// driveArgs is the command line, parsed by hand so `--` survives.
type driveArgs struct {
	tasks   []string // as typed, any :model cut off; with span, the two ends of a range
	models  []string // each task's :model as typed, indexed like tasks, "" for none
	model   string   // --model as typed: every task's model unless its own names one
	span    bool
	flow    string   // --flow, or local for --local; "" for default
	answers []string // each --answer as typed: `GH-1=text` for one task, or bare text
	every   time.Duration
	resume  int  // --resume: the record to pick up
	fore    bool // --foreground: run the loop in this process, not the daemon
	list    bool // `drive list`
	all     bool // `drive list --all`
	cancel  int  // `drive cancel <n>`
}

// parseDriveArgs reads tasks, with --model, --answer, --every, --flow and
// --local anywhere. A chain skill typed after the ids is refused: the flow
// says what runs.
func parseDriveArgs(args []string) (driveArgs, error) {
	a := driveArgs{every: defaultEvery}

	if len(args) > 0 {
		switch strings.TrimSpace(args[0]) {
		case "list":
			return parseDriveList(args[1:])
		case "cancel":
			if len(args) != 2 {
				return driveArgs{}, errors.New("cancel takes one drive number: f10 drive cancel 3")
			}

			n, err := driveNumber(args[1])

			return driveArgs{cancel: n}, err
		}
	}

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])

		name, value, inline := strings.Cut(tok, "=")
		if name == "--model" || name == "--answer" || name == "--every" || name == "--flow" || name == "--resume" {
			if !inline {
				rest := args[i+1:]
				if len(rest) == 0 {
					return driveArgs{}, fmt.Errorf("%s needs a value", name)
				}

				value = rest[0]
				i++
			}

			if err := a.set(name, value); err != nil {
				return driveArgs{}, err
			}

			continue
		}

		skill := strings.ToLower(strings.Trim(tok, ","))

		switch {
		case tok == "":
			continue
		case tok == "--foreground":
			a.fore = true
		case tok == "--local":
			if a.flow != "" && a.flow != "local" {
				return driveArgs{}, fmt.Errorf("--local and --flow %s name two flows", a.flow)
			}

			a.flow = "local"
		case tok == "--":
			if a.span || len(a.tasks) != 1 {
				return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
			}

			a.span = true
		case isArrow(tok):
			continue
		case slices.Contains(oldChainSkills, skill):
			return driveArgs{}, fmt.Errorf("%s: drive takes no chain; it runs the project's flow "+
				"(f10 flow lists them, --flow picks one), and drive_finish says whether it ends with finish", tok)
		case strings.HasPrefix(tok, "-"):
			return driveArgs{}, fmt.Errorf(
				"unknown flag %s: drive takes --flow, --local, --model, --answer, --every, --resume and --foreground",
				tok,
			)
		default:
			task, m, err := splitModel(tok)
			if err != nil {
				return driveArgs{}, err
			}

			a.tasks, a.models = append(a.tasks, task), append(a.models, m)
		}
	}

	switch {
	case a.resume > 0 && (len(a.tasks) > 0 || a.flow != "" || a.model != ""):
		return driveArgs{}, fmt.Errorf("--resume %d runs the drive's own tasks, flow and models: "+
			"it takes only --answer and --every", a.resume)
	case a.resume > 0:
		return a, nil
	case len(a.tasks) == 0:
		return driveArgs{}, errors.New("drive needs the tasks: GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.span && len(a.tasks) != 2:
		return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	case a.span && slices.ContainsFunc(a.models, func(m string) bool { return m != "" }):
		return driveArgs{}, errors.New("a range takes the run-wide --model, not one on its ends")
	}

	return a, nil
}

func (a *driveArgs) set(name, value string) error {
	switch name {
	case "--resume":
		n, err := driveNumber(value)
		a.resume = n

		return err
	case "--model":
		if a.model != "" {
			return errors.New("--model is given twice: one run-wide model, and <id>:<model> for a task's own")
		}

		if a.model = strings.TrimSpace(value); a.model == "" {
			return errors.New("--model needs a value")
		}

		return nil
	case "--answer":
		if value = strings.TrimSpace(value); value != "" {
			a.answers = append(a.answers, value)
		}

		return nil
	case "--flow":
		value = strings.ToLower(strings.TrimSpace(value))

		switch {
		case value == "":
			return errors.New("--flow needs a flow name")
		case a.flow != "" && a.flow != value:
			return fmt.Errorf("--flow %s: the run already names the %s flow", value, a.flow)
		}

		a.flow = value

		return nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("--every %s: a positive duration, like 15s", value)
	}

	a.every = d

	return nil
}

// parseDriveList reads `drive list [--all]`.
func parseDriveList(args []string) (driveArgs, error) {
	a := driveArgs{list: true}

	for _, tok := range args {
		if tok != "--all" {
			return driveArgs{}, fmt.Errorf("drive list takes only --all, not %s", tok)
		}

		a.all = true
	}

	return a, nil
}

// driveNumber reads a drive's number.
func driveNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a drive number: f10 drive list shows them", s)
	}

	return n, nil
}

// splitModel cuts a task token's :model off. Only the last colon counts,
// and only when what follows has no slash, so a url's scheme and port stay
// part of the task.
func splitModel(tok string) (string, string, error) {
	i := strings.LastIndex(tok, ":")
	if i < 0 || strings.Contains(tok[i+1:], "/") {
		return tok, "", nil
	}

	if tok[i+1:] == "" {
		return "", "", fmt.Errorf("%s names no model after the colon: GH-12:opus", tok)
	}

	return tok[:i], tok[i+1:], nil
}

func isArrow(tok string) bool {
	return tok == "→" || tok == "->" || tok == ","
}

// driveInput is everything drive needs once the target is settled, so the
// flow can be tested without a checkout, a clock or a wait.
type driveInput struct {
	main     string
	root     string // the storage root every checkout shares, for the record and the leases; "" keeps none
	driver   *driver.Driver
	tracker  tracker
	args     driveArgs
	argv     []string // the command line as given, for the resume command
	chain    []string
	flow     shipFlow                            // the flow typed, or default; a task's branch may record another
	resolve  func(name string) (shipFlow, error) // a declared flow by name
	plansDir string                              // the plans dir every checkout shares
	settings string                              // claude's settings file, which names the model a bad --model falls back to
	title    func(ref.Ref) func(context.Context) (string, error)
	started  func(context.Context, ref.Ref) error // nil when tracker.status declares no started word
	// pr: "", or the answer merge or local; progress names each action finish takes
	finish   func(ctx context.Context, w io.Writer, task ref.Ref, pr string, progress func(string)) error
	stateDir string
	session  string // the session that typed the command, inherited through the daemon's environment
	ttl      time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	// record puts an event in the ledger, placed in dir; nil records nothing
	record func(ctx context.Context, dir string, e ledger.Event)

	answer  string            // a bare --answer, until the first task stopped on a question takes it
	answers map[string]string // --answer ID=text, by task id, until that task takes it
	models  map[string]string // each model as typed, resolved; "" for claude's default

	rec    *drives.Record           // this drive's record; nil with no root
	own    *drives.Lease            // the drive's own lease, held while it runs
	leases map[string]*drives.Lease // each task's, by key, held while the task is unended

	// asksDir is the inbox an ask goes to and answers come from; "" keeps
	// none. notify tells the user of a new ask; resume starts this drive
	// again for an answer that came after it stopped looking.
	asksDir string
	notify  func(context.Context, asks.Ask) error
	resume  func(ctx context.Context, n int) error
	// answeredIn is when a task's ask was answered where the agent heard
	// it: the drive follows that turn instead of sending the skill again
	answeredIn map[string]time.Time
	inboxNotes []string

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
	model    string // the agent's --model, resolved, or "" for claude's default
	branch   string // the task's local branch, or "" before it started
	after    string // the base the body's After: line names, as an id
	base     string // the base the drive waits for: the branch's record, else after
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
	end       *driveResult
	// freshAt is when this drive started the task's agent: run state last
	// updated before it was written by an agent that is gone, and is not read
	freshAt time.Time
	// lastCatchup is the catchup this drive last sent for the task, so a
	// conflict after it is judged by progress, not counted: the drive
	// catches up again while the target moves, and halts once it stops
	lastCatchup *catchupSent
	// waiting is set while finish waits for the base to be through: no turn
	// is in flight, so the poll only asks next again
	waiting bool
}

func (r *driveRun) active() bool { return r.opened && r.end == nil }

// catchupSent is what the branch looked like when a catchup went out: the
// sha it was being caught up to, its head, and the files that conflicted
// (joined, "" when none were read). A field left "" compares to nothing.
// base names the target in prose.
type catchupSent struct {
	target, head, files, base string
}

// halts says why a conflict after this catchup halts the task instead of
// sending another, or "" when the target moved and another catchup can make
// progress: the catchup left the branch where it was, the target is where
// it was, or the same files conflict again. base names the target in prose.
func (c *catchupSent) halts(target, head, files, base string) string {
	switch {
	case c == nil:
		return ""
	case head != "" && head == c.head:
		return "the catchup left the branch unchanged"
	case target != "" && target == c.target:
		return base + " has not moved since the catchup"
	case files != "" && files == c.files:
		return "the same files conflict with " + base + " again: " + files
	}

	return ""
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
// whole chain run.
func (r *driveRun) through() bool { return r.end != nil && r.end.code == 0 }

// drive runs the flow: resolve every task, read the graph, start every
// task whose base has a commit, then poll them all until each is through,
// or one asks, or nothing can move. Nothing is created or prompted before the
// graph passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) error {
	in.out, in.leases = w, map[string]*drives.Lease{}
	in.resolveModels(w)

	tasks, notes, err := in.resolveTasks(ctx)
	if err != nil {
		return err
	}

	order, graphNotes, err := in.graph(ctx, tasks)
	if err != nil {
		return err
	}

	notes = append(notes, graphNotes...)

	if err := in.openRecord(tasks); err != nil {
		return err
	}

	if err := in.recordLaunch(); err != nil {
		notes = append(notes, "the drive was not recorded as this session's launch: "+err.Error())
	}

	// runs after releaseAll: an answer claimed while this drive still held
	// its lease is seen here, or by its claimer once the lease is free
	defer in.resumeAnswered(context.WithoutCancel(ctx), tasks)
	defer in.releaseAll()

	if err := in.takeAnswers(tasks); err != nil {
		return err
	}

	in.takeInbox(tasks)

	if in.rec != nil {
		fmt.Fprintf(w, "drive %d\n", in.rec.Number)
		in.savePending(nil)

		// the daemon running this drive learns its number here
		if path := os.Getenv(daemon.NumberFileEnv); path != "" {
			//nolint:gosec // the daemon names the path, as this same user
			if err := os.WriteFile(path, []byte(strconv.Itoa(in.rec.Number)+"\n"), 0o600); err != nil {
				fmt.Fprintf(w, "  telling the daemon the drive's number: %v\n", err)
			}
		}
	}

	runs := make([]*driveRun, 0, len(order))
	byID := map[string]*driveRun{}

	for _, i := range order {
		r := &driveRun{task: tasks[i]}
		if r.task.finished {
			in.end(ctx, w, r, driveResult{row: "already finished"})
		}

		runs = append(runs, r)

		for _, id := range r.task.ref.Members() {
			if _, seen := byID[id]; !seen {
				byID[id] = r
			}
		}
	}

	// a base outside the list was refused unless finished
	for _, r := range runs {
		r.base = byID[r.task.base]
	}

	in.runs = runs

	// every task this drive may act on is its own before anything opens,
	// so a second drive cannot start one this one is waiting to run
	for _, r := range runs {
		if r.end != nil {
			continue
		}

		if why, ok := in.lease(r); !ok {
			in.end(ctx, w, r, driveResult{row: why, code: exitHalted, drives: true})
		}
	}

	if err := in.startRunnable(ctx, w, runs); err != nil {
		return err
	}

	// an interrupted drive leaves no finish record claiming it still runs
	defer func() {
		for _, r := range runs {
			if r.active() {
				in.clearFinish(context.WithoutCancel(ctx), r)
			}
		}
	}()

	for !stopping(runs) {
		if err := in.sleep(ctx, in.args.every); err != nil {
			return err
		}

		if in.draining.Load() {
			fmt.Fprintf(w, "drive %d drained for an upgrade; the new daemon resumes it\n", in.number())

			return cli.Exit("", exitDrained)
		}

		if in.cancelled() {
			for _, r := range runs {
				if r.end == nil {
					in.end(ctx, w, r, driveResult{row: "cancelled", code: exitHalted, drives: true})
				}
			}

			break
		}

		wss, live, err := in.lookAll(ctx)
		if err != nil {
			return err
		}

		for _, r := range runs {
			if !r.active() {
				continue
			}

			if err := in.poll(ctx, w, r, wss, live); err != nil {
				return fmt.Errorf("%s: %w", r.task.ref.ID, err)
			}
		}

		if err := in.markMoved(ctx, w, runs, wss); err != nil {
			return err
		}

		if err := in.markBehind(ctx, w, runs); err != nil {
			return err
		}

		if err := in.startRunnable(ctx, w, runs); err != nil {
			return err
		}
	}

	return in.report(w, runs, append(notes, in.inboxNotes...))
}

// resolveModels resolves every model the command line names, once each,
// and prints what it corrected or fell back on before anything opens.
func (in *driveInput) resolveModels(w io.Writer) {
	in.models = map[string]string{}

	for _, raw := range append([]string{in.args.model}, in.args.models...) {
		if _, done := in.models[raw]; done || raw == "" {
			continue
		}

		m, line := resolveModel(raw, in.settings)
		in.models[raw] = m

		if line != "" {
			fmt.Fprintln(w, line)
		}
	}
}

// modelFor is the resolved model of the task listed at i: its own, else
// the run-wide one. A range's tasks have no index of their own, i < 0.
func (in *driveInput) modelFor(i int) string {
	if i >= 0 && i < len(in.args.models) && in.args.models[i] != "" {
		return in.models[in.args.models[i]]
	}

	return in.models[in.args.model]
}

// stopping reports whether the drive ends: a task asks, which only a human
// can answer, or no task is active, so nothing can change.
func stopping(runs []*driveRun) bool {
	active := false

	for _, r := range runs {
		if r.end != nil && r.end.code == exitAsk {
			return true
		}

		active = active || r.active()
	}

	return !active
}

// startRunnable opens every pending task whose base released it and sends
// its first element. Runs are in graph order, so a base released earlier
// in the same pass releases its dependents in it.
func (in *driveInput) startRunnable(ctx context.Context, w io.Writer, runs []*driveRun) error {
	for _, r := range runs {
		if r.opened || r.end != nil {
			continue
		}

		if r.base != nil && !in.released(ctx, r.base) {
			continue
		}

		if err := in.open(ctx, w, r); err != nil {
			return fmt.Errorf("%s: %w", r.task.ref.ID, err)
		}

		if err := in.next(ctx, w, r); err != nil {
			return fmt.Errorf("%s: %w", r.task.ref.ID, err)
		}
	}

	return nil
}

// released reports whether a base lets its dependents start: through, or
// open with a commit of its own. Implement never commits, so a commit means
// a commit or pr step ran with verify green. Its own commits are what its
// branch has beyond the default branch, origin's copy of it, its own base's
// branch, and the commit it was created at, which keeps out a squashed
// grandparent's commits. Read from local refs, so a branch never pushed counts.
func (in *driveInput) released(ctx context.Context, b *driveRun) bool {
	if b.through() {
		return true
	}

	if !b.opened || b.end != nil {
		return false
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return false
	}

	args := []string{"rev-list", "--count", "refs/heads/" + b.o.branch, "--not", "refs/heads/" + def}

	// a ref git cannot read would drop out of the --not list and count the
	// base's own base as its commits, so it releases nobody this poll
	fork, err := gitx.Config(ctx, in.main, "branch."+b.o.branch+"."+cfgAfterBranch)
	if err != nil {
		return false
	}

	for _, r := range []string{"refs/remotes/origin/" + def, "refs/heads/" + fork} {
		if strings.HasSuffix(r, "/") {
			continue
		}

		sha, err := gitx.Ref(ctx, in.main, r)
		if err != nil {
			return false
		}

		if sha != "" {
			args = append(args, r)
		}
	}

	if created := createdAt(ctx, in.main, b.o.branch); created != "" {
		args = append(args, created)
	}

	n := gitx.Out(ctx, in.main, args...)

	return n != "" && n != "0"
}

// createdAt is the commit branch was created at, from its oldest reflog
// entry, or "" once the reflog no longer reaches back to the creation.
func createdAt(ctx context.Context, dir, branch string) string {
	lines := strings.Split(gitx.Out(ctx, dir, "reflog", "show", "--format=%H %gs", "refs/heads/"+branch), "\n")

	sha, subject, _ := strings.Cut(lines[len(lines)-1], " ")
	if !strings.HasPrefix(subject, "branch: Created from") {
		return ""
	}

	return sha
}

// markMoved marks each running dependent whose base branch has a head its
// own branch lacks, read while the base agent is not mid-turn, so its next
// boundary sends a catchup. A catchup in flight is left alone: it clears
// the mark when it ends, and the next poll marks again if the base moved
// meanwhile. A finished base's branch is gone, and f10-landed takes over.
func (in *driveInput) markMoved(ctx context.Context, w io.Writer, runs []*driveRun, wss []agentctl.Workspace) error {
	for _, r := range runs {
		b := r.base
		if !r.active() || r.skill == "catchup" || b == nil || !b.opened {
			continue
		}

		if b.end == nil {
			if ws := workspaceByID(wss, b.o.ws.ID); ws == nil || ws.Status == agentctl.StatusWorking {
				continue
			}
		}

		// a read git cannot answer skips the task for this poll: the next
		// poll reads again, and a wrong "moved" would send a catchup
		head, err := gitx.Ref(ctx, in.main, "refs/heads/"+b.o.branch)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", r.task.ref.ID, b.o.branch, err)

			continue
		}

		if head == "" {
			continue
		}

		if has, err := gitx.IsAncestor(ctx, in.main, head, "refs/heads/"+r.o.branch); has || err != nil {
			if err != nil {
				fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", r.task.ref.ID, r.o.branch, err)
			}

			continue
		}

		key := "branch." + r.o.branch + "." + cfgMoved

		marked, err := gitx.Config(ctx, in.main, key)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", r.task.ref.ID, key, err)

			continue
		}

		if marked == head {
			continue
		}

		if err := gitx.SetConfig(ctx, in.main, key, head); err != nil {
			return fmt.Errorf("%s: marking %s moved: %w", r.task.ref.ID, b.o.branch, err)
		}

		fmt.Fprintf(w, "%s: base %s moved, catching up before the next skill\n", r.task.ref.ID, b.o.branch)
	}

	return nil
}

// markBehind marks each running task whose branch is on origin and whose
// pull request's target moved past it, so its next boundary sends a catchup
// and the open PR never sits behind (a listed base still in flight is
// markMoved's). One fetch per look, only while a pushed branch is in flight;
// a fetch or read git cannot answer skips the look, and the next reads again.
func (in *driveInput) markBehind(ctx context.Context, w io.Writer, runs []*driveRun) error {
	var pushed []*driveRun

	for _, r := range runs {
		if !r.active() || r.skill == "catchup" || (r.base != nil && !r.base.through()) {
			continue
		}

		sha, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+r.o.branch)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped this look, reading origin/%s: %v\n", r.task.ref.ID, r.o.branch, err)

			continue
		}

		if sha != "" {
			pushed = append(pushed, r)
		}
	}

	if len(pushed) == 0 {
		return nil
	}

	if _, err := gitx.Exec(ctx, in.main, "fetch", "origin"); err != nil {
		fmt.Fprintf(w, "skipped this look, fetching origin: %v\n", err)

		return nil
	}

	for _, r := range pushed {
		if err := in.markBehindOne(ctx, w, r); err != nil {
			return err
		}
	}

	return nil
}

// markBehindOne marks one pushed branch its PR's target moved past, unless
// it is marked already or the target is in it.
func (in *driveInput) markBehindOne(ctx context.Context, w io.Writer, r *driveRun) error {
	id, branch := r.task.ref.ID, r.o.branch

	skip := func(what string, err error) error {
		fmt.Fprintf(w, "%s: skipped this look, reading %s: %v\n", id, what, err)

		return nil
	}

	target, err := in.prTarget(ctx, r)
	if err != nil {
		return skip("what "+branch+" targets", err)
	}

	// a base gone from everywhere is nobody's target until finish releases the dependent
	if target.sha == "" {
		return nil
	}

	key, err := in.mark(ctx, r)
	if err != nil {
		return skip("the marks on "+branch, err)
	}

	if key != "" {
		return nil
	}

	has, err := gitx.IsAncestor(ctx, in.main, target.sha, "refs/heads/"+branch)
	if err != nil {
		return skip(branch, err)
	}

	if has {
		return nil
	}

	if err := gitx.SetConfig(ctx, in.main, "branch."+branch+"."+target.key, target.value); err != nil {
		return fmt.Errorf("%s: marking %s behind %s: %w", id, branch, target.name, err)
	}

	fmt.Fprintf(w, "%s: %s moved past %s, catching up before the next skill\n", id, target.name, branch)

	return nil
}

// prTarget is what a task's pull request targets and the mark that sends
// a catchup for it, read as the catchup step's point 2 reads the base: the
// branch f10-after-branch records, here or on origin, else origin's
// default branch. sha is "" for a base found nowhere.
type prTarget struct {
	sha, name, key, value string
}

func (in *driveInput) prTarget(ctx context.Context, r *driveRun) (prTarget, error) {
	after, err := gitx.Config(ctx, in.main, "branch."+r.o.branch+"."+cfgAfterBranch)
	if err != nil {
		return prTarget{}, err
	}

	if after != "" {
		sha := ""

		for _, ref := range []string{"refs/heads/" + after, "refs/remotes/origin/" + after} {
			if sha, err = gitx.Ref(ctx, in.main, ref); err != nil || sha != "" {
				break
			}
		}

		return prTarget{sha: sha, name: after, key: cfgMoved, value: sha}, err
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return prTarget{}, err
	}

	sha, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+def)

	return prTarget{sha: sha, name: "origin/" + def, key: cfgLanded, value: def}, err
}

// takeAnswers sorts the --answer values: `ID=text` naming a listed task is
// that task's, anything else is the bare answer, of which there is one.
func (in *driveInput) takeAnswers(tasks []driveTask) error {
	in.answer, in.answers = "", map[string]string{}

	for _, a := range in.args.answers {
		if key, text, ok := strings.Cut(a, "="); ok {
			if i := slices.IndexFunc(tasks, func(t driveTask) bool {
				return strings.EqualFold(t.ref.ID, strings.TrimSpace(key))
			}); i >= 0 {
				in.answers[tasks[i].ref.ID] = strings.TrimSpace(text)

				continue
			}
		}

		if in.answer != "" {
			return errors.New("several --answer values name no task: key each one, --answer GH-1=\"1. …\"")
		}

		in.answer = a
	}

	return nil
}

// report prints a row per task in graph order, then what stopped the
// drive, and exits with the code the rows add up to: an ask first, since
// answering it is what resumes the drive, then a halt.
func (in *driveInput) report(w io.Writer, runs []*driveRun, notes []string) error {
	var (
		rows   []fact
		lines  []string
		asking []string
		code   int
	)

	halted := map[string]bool{}

	for _, r := range runs {
		id := r.task.ref.ID

		switch {
		case r.end != nil:
			rows = append(rows, fact{label: id, value: r.end.row})
			lines = append(lines, r.end.lines...)

			if r.end.code == exitAsk {
				asking = append(asking, id)
			}

			halted[id] = r.end.code == exitHalted
			code = max(code, r.end.code)
		case r.active() && r.waiting:
			rows = append(rows, fact{label: id, value: "waiting for " + r.base.task.ref.ID})
		case r.active():
			rows = append(rows, fact{label: id, value: "running " + r.skill})
		case halted[r.task.base]:
			rows = append(rows, fact{label: id, value: "halted: base " + r.task.base + " halted"})
			halted[id] = true
			code = max(code, exitHalted)
		default:
			rows = append(rows, fact{label: id, value: "not reached"})
		}
	}

	if len(asking) > 0 {
		code = exitAsk
		lines = append(lines, "answer with: "+in.resumeLine()+answerFlags(asking))
	}

	if in.answer != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	for _, r := range runs {
		if _, left := in.answers[r.task.ref.ID]; left {
			notes = append(notes, "--answer for "+r.task.ref.ID+" went unused: it was not stopped on a question")
		}
	}

	// an answer reported unused is dropped, so a resume never sends it to a
	// question nobody saw; only one a stopped drive never reached is kept
	in.answer, in.answers = "", map[string]string{}
	in.savePending(nil)

	notes = append(notes, lines...)

	if len(rows) > 0 {
		fmt.Fprintln(w)
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if code != 0 {
		return cli.Exit("", code)
	}

	return nil
}

// answerFlags is the --answer part of the resume command: bare for one
// asking task, keyed by task when several ask.
func answerFlags(asking []string) string {
	if len(asking) == 1 {
		return ` --answer "1. … 2. …"`
	}

	var b strings.Builder
	for _, id := range asking {
		b.WriteString(` --answer "` + id + `=1. … 2. …"`)
	}

	return b.String()
}

// resolveTasks turns the arguments into tasks with what the tracker says
// about each: the body, closed or not, and whether it is finished. A range
// member the tracker does not know as a task is skipped with a note; a
// listed one is an error.
func (in *driveInput) resolveTasks(ctx context.Context) ([]driveTask, []string, error) {
	var (
		tasks []driveTask
		notes []string
	)

	// listed is the task's place on the command line, -1 for one a range spans
	add := func(r ref.Ref, suffix string, listed int) error {
		ranged := listed < 0

		if i := slices.IndexFunc(tasks, func(t driveTask) bool {
			return t.suffix == suffix && slices.ContainsFunc(r.Members(), func(id string) bool {
				return slices.Contains(t.ref.Members(), id)
			})
		}); i >= 0 {
			return fmt.Errorf("%s is listed twice", sharedMember(tasks[i].ref, r))
		}

		info, err := in.tracker.info(ctx, r)

		switch {
		case err != nil && ranged:
			notes = append(notes, fmt.Sprintf("%s skipped: %v", r.ID, err))

			return nil
		case err != nil:
			return fmt.Errorf("%s: %w", r.ID, err)
		case info.pull && ranged:
			notes = append(notes, r.ID+" skipped: a pull request, not a task")

			return nil
		case info.pull:
			return fmt.Errorf("%s is a pull request, not a task", r.ID)
		}

		task := driveTask{ref: r, suffix: suffix, info: info, model: in.modelFor(listed)}

		line, err := bundleAfter(ctx, in.tracker, r, info.body)
		if err != nil {
			return err
		}

		if line != "" {
			base, err := in.tracker.resolve(ctx, line)
			if err != nil {
				return fmt.Errorf("%s's body says After: %s: %w", r.ID, line, err)
			}

			task.after = base.Head().ID
		}

		branched, err := hasTaskBranch(ctx, in.main, r.ID)
		if err != nil {
			return err
		}

		if branched {
			var local []string
			if suffix == "" {
				if local, err = linkedBranches(ctx, in.main, r.ID); err != nil {
					return err
				}
			}

			if len(local) == 0 {
				if local, err = branchesFor(ctx, in.main, "refs/heads/", r.ID, suffix); err != nil {
					return err
				}
			}

			if len(local) == 1 {
				task.branch = local[0]
			}
		}

		task.finished = finishedTask(info, in.tracker.archive, r.ID, branched)

		if !ranged && !task.finished && !branched {
			for _, m := range r.MemberRefs() {
				if err := in.refuseParent(ctx, m); err != nil {
					return err
				}
			}
		}

		tasks = append(tasks, task)

		return nil
	}

	if in.args.span {
		from, to, err := in.span(ctx)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range spanRefs(from, to) {
			if err := add(r, "", -1); err != nil {
				return nil, nil, err
			}
		}

		return tasks, notes, nil
	}

	for i, tok := range in.args.tasks {
		id, suffix := ref.SplitSuffix(tok)

		r, err := in.tracker.resolve(ctx, id)
		if err != nil {
			return nil, nil, err
		}

		if suffix != "" && r.Bundle != nil {
			return nil, nil, fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
		}

		// any member drives the whole bundle, under its lead
		if err := add(r.Head(), suffix, i); err != nil {
			return nil, nil, err
		}
	}

	return tasks, notes, nil
}

// refuseParent stops a parent with unfinished subtasks before it is planned
// and shipped as a task of its own: the subtasks are what runs, and the
// drive skill is where they are chosen. A started task is never a parent
// here, so a resumed drive asks nothing more of the tracker.
func (in *driveInput) refuseParent(ctx context.Context, r ref.Ref) error {
	open, err := openSubtasks(ctx, in.tracker, r)
	if err != nil || len(open) == 0 {
		return err
	}

	return fmt.Errorf("%s is a parent of %s: list the subtasks to drive (f10 task children %s), "+
		"or run /f10:drive %s to choose", r.ID, strings.Join(open, ", "), r.ID, r.ID)
}

// span resolves a range's two ends.
func (in *driveInput) span(ctx context.Context) (ref.Ref, ref.Ref, error) {
	if slices.ContainsFunc(in.args.tasks, func(t string) bool { return strings.Contains(t, ref.Separator) }) {
		return ref.Ref{}, ref.Ref{}, errors.New("a range's ends are single ids: list a bundle on its own")
	}
	from, err := in.tracker.resolve(ctx, in.args.tasks[0])
	if err != nil {
		return ref.Ref{}, ref.Ref{}, err
	}

	to, err := in.tracker.resolve(ctx, in.args.tasks[1])
	if err != nil {
		return ref.Ref{}, ref.Ref{}, err
	}

	a, _ := strconv.Atoi(from.Number)
	b, _ := strconv.Atoi(to.Number)

	switch {
	case prefixOf(from) != prefixOf(to):
		return ref.Ref{}, ref.Ref{}, fmt.Errorf("a range stays in one id format: %s -- %s", from.ID, to.ID)
	case b < a:
		return ref.Ref{}, ref.Ref{}, fmt.Errorf("a range runs upwards: %s -- %s", from.ID, to.ID)
	case b-a >= maxSpan:
		return ref.Ref{}, ref.Ref{}, fmt.Errorf(
			"%s -- %s is %d ids; a range takes at most %d",
			from.ID,
			to.ID,
			b-a+1,
			maxSpan,
		)
	}

	return from, to, nil
}

// spanRefs is every id between two ends, inclusive, in the ends' format.
func spanRefs(from, to ref.Ref) []ref.Ref {
	a, _ := strconv.Atoi(from.Number)
	b, _ := strconv.Atoi(to.Number)
	prefix := prefixOf(from)

	refs := make([]ref.Ref, 0, b-a+1)

	for n := a; n <= b; n++ {
		num := strconv.Itoa(n)
		refs = append(refs, ref.Ref{ID: prefix + num, Number: num, Origin: ref.OriginExplicit})
	}

	return refs
}

// prefixOf is the part of an id before its number: "GH-" for GH-12.
func prefixOf(r ref.Ref) string {
	return strings.TrimSuffix(r.ID, r.Number)
}

// baseFinished reports whether a base outside the list is done with.
func (in *driveInput) baseFinished(ctx context.Context, id string) (bool, error) {
	base, err := in.tracker.resolve(ctx, id)
	if err != nil {
		return false, err
	}

	branched, err := hasTaskBranch(ctx, in.main, base.ID)
	if err != nil || branched {
		return false, err
	}

	info, err := in.tracker.info(ctx, base)
	if err != nil {
		return false, fmt.Errorf("%s: %w", base.ID, err)
	}

	return finishedTask(info, in.tracker.archive, base.ID, false), nil
}

// open opens the task's checkout and agent, adopting a plan main saved for
// it as start does, and reads where in the chain it stands.
func (in *driveInput) open(ctx context.Context, w io.Writer, r *driveRun) error {
	task := r.task

	after, note, err := bodyAfter(ctx, in.tracker, in.main, task.ref, task.info.body)
	if err != nil {
		return err
	}

	var title func(context.Context) (string, error)
	if in.title != nil {
		title = in.title(task.ref)
	}

	o, err := openTask(ctx, startInput{
		main:     in.main,
		driver:   in.driver,
		task:     task.ref,
		suffix:   task.suffix,
		after:    after,
		plansDir: in.plansDir,
		title:    title,
		model:    task.model,
		progress: w,
		flowKept: true,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s: branch %s at %s, workspace %s\n", task.ref.ID, o.branch, o.path, o.ws.ID)

	if o.fresh {
		o.notes = append(o.notes, markStarted(ctx, in.started, o.task)...)
	}

	fl, flowNote, err := in.keepFlow(ctx, o.branch)
	if err != nil {
		return err
	}

	if flowNote != "" {
		o.notes = append(o.notes, flowNote)
	}

	chain := in.chainFor(fl)

	// status reads finish off the chain
	if err := gitx.SetConfig(ctx, in.main, "branch."+o.branch+"."+cfgChain, strings.Join(chain, " ")); err != nil {
		return fmt.Errorf("recording the chain: %w", err)
	}

	if slices.Contains(chain, "finish") {
		in.finishStep(ctx, w, o.branch, ref.FinishQueued, "")
	}

	for _, n := range appendNote(o.notes, note) {
		fmt.Fprintf(w, "  %s\n", n)
	}

	pos, err := in.position(ctx, o, chain)
	if err != nil {
		return err
	}

	r.o, r.opened, r.pos, r.flow, r.chain = o, true, pos, fl, chain
	if o.fresh {
		r.freshAt = in.now().Truncate(time.Second) // the state file keeps whole seconds
	}

	return nil
}

// keepFlow is the flow a task runs: one its branch already records wins
// over the run's, since switching mid-run would rerun or skip entries ship
// already declared; a branch with none records the run's. A recorded flow
// project.md no longer declares gives way to the run's, with a note.
func (in *driveInput) keepFlow(ctx context.Context, branch string) (shipFlow, string, error) {
	key := "branch." + branch + "." + cfgFlow

	recorded, err := gitx.Config(ctx, in.main, key)
	if err != nil {
		return shipFlow{}, "", fmt.Errorf("reading the recorded flow: %w", err)
	}

	if recorded == in.flow.recorded() {
		return in.flow, "", nil
	}

	// an area picks the flow in ship, so a branch with none stays without
	if recorded == "" && in.flow.byArea {
		return in.flow, "", nil
	}

	// a recorded name is looked up as written: `default` is the default
	// flow, never the area's pick
	if recorded != "" && in.resolve != nil {
		if fl, err := in.resolve(recorded); err == nil {
			return fl, fmt.Sprintf("kept the %s flow its branch records; the run's %s does not apply to it",
				recorded, in.flow.label()), nil
		}
	}

	if !in.flow.byArea {
		if err := gitx.SetConfig(ctx, in.main, key, in.flow.recorded()); err != nil {
			return shipFlow{}, "", fmt.Errorf("recording the flow: %w", err)
		}
	}

	if recorded != "" {
		return in.flow, fmt.Sprintf("its branch recorded the %s flow, which project.md no longer declares; running %s",
			recorded, in.flow.label()), nil
	}

	return in.flow, "", nil
}

// chainFor is the chain a task runs on the flow it keeps: the run's own,
// or one derived from a flow its branch recorded, which may lead with plan
// where the run's does not, or the other way round.
func (in *driveInput) chainFor(fl shipFlow) []string {
	if fl == in.flow {
		return in.chain
	}

	return driveChain(fl, slices.Contains(in.chain, "finish"))
}

// position is how many chain skills the task is past: the last one drive
// recorded on the branch, else what its run state proves - plan and ship
// are the phases the badge tracks - and a plan the worktree reads counts as
// plan done, so driven ship takes it rather than planning again.
func (in *driveInput) position(ctx context.Context, o openedTask, chain []string) (int, error) {
	last, err := gitx.Config(ctx, in.main, "branch."+o.branch+"."+cfgDrive)
	if err != nil {
		return 0, fmt.Errorf("reading where the drive left %s: %w", o.branch, err)
	}

	if last != "" {
		if i := slices.Index(chain, last); i >= 0 {
			return i + 1, nil
		}
	}

	pos := 0

	if i := slices.Index(chain, "plan"); i >= 0 && o.plan != "" {
		pos = i + 1
	}

	run, err := in.run(o.path)
	if err != nil || run == nil {
		return pos, err
	}

	for _, phase := range []string{"plan", "ship"} {
		if s := run.Status(phase); s != "done" && s != "prior" {
			continue
		}

		if i := slices.Index(chain, phase); i >= 0 {
			pos = max(pos, i+1)
		}
	}

	return pos, nil
}

// next sends the task its next element: none left ends it, a base that
// halted halts it, and a branch marked landed or moved is caught up before
// any skill, finish included, runs on it. finish waits for the base to be
// through, then runs in this process once the branch merges cleanly; one
// that conflicts is marked landed and caught up first.
func (in *driveInput) next(ctx context.Context, w io.Writer, r *driveRun) error {
	wasWaiting := r.waiting
	r.waiting = false

	spike, err := in.spikeDone(ctx, r)
	if err != nil {
		in.end(ctx, w, r, halted(r.task.ref.ID, "finish", err.Error()))

		return nil
	}

	if spike && r.pos >= len(r.chain) {
		if err := in.closeAfterShip(ctx, r); err != nil {
			return err
		}
	}

	if r.pos >= len(r.chain) {
		in.end(ctx, w, r, driveResult{row: "done: " + strings.Join(r.chain, " ")})

		return nil
	}

	skill := r.chain[r.pos]

	if b := r.base; b != nil && b.end != nil && b.end.code == exitHalted {
		in.end(ctx, w, r, baseHalted(r, skill))

		return nil
	}

	if skill == "finish" && r.base != nil && !r.base.through() {
		if !wasWaiting {
			fmt.Fprintf(w, "%s finish: waiting for %s to finish\n", r.task.ref.ID, r.base.task.ref.ID)
			in.finishStep(ctx, w, r.o.branch, ref.FinishQueued, "waiting for "+r.base.task.ref.ID)
		}

		r.waiting = true

		return nil
	}

	// a spike merges nothing, so it needs no catchup and no clean merge
	if skill == "finish" && spike {
		in.end(ctx, w, r, in.runFinish(ctx, w, r))

		return nil
	}

	key, err := in.mark(ctx, r)
	if err == nil && skill == "finish" && key == "" {
		if res := in.readyToMerge(ctx, w, r); res != nil {
			in.end(ctx, w, r, *res)

			return nil
		}

		key, err = in.mark(ctx, r)
	}

	if err != nil {
		in.end(ctx, w, r, halted(r.task.ref.ID, skill, err.Error()))

		return nil
	}

	switch {
	case key != "":
		skill = "catchup"

		if err := in.recordCatchup(ctx, r, key); err != nil {
			in.end(ctx, w, r, halted(r.task.ref.ID, skill, err.Error()))

			return nil
		}
	case skill == "finish":
		in.end(ctx, w, r, in.runFinish(ctx, w, r))

		return nil
	}

	return in.send(ctx, w, r, skill)
}

// recordCatchup notes what the catchup about to go out starts from, for the
// progress check a later conflict runs. A read git cannot answer halts: a
// record made up would halt a healthy task, or loop on a doomed one.
func (in *driveInput) recordCatchup(ctx context.Context, r *driveRun, key string) error {
	target, base, err := in.catchupTarget(ctx, r, key)
	if err != nil {
		return err
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return fmt.Errorf("reading the head of %s: %w", r.o.branch, err)
	}

	files, err := gitx.Conflicts(ctx, r.o.path, target, "HEAD")
	if err != nil {
		return fmt.Errorf("reading what %s conflicts on: %w", r.o.branch, err)
	}

	r.lastCatchup = &catchupSent{target: target, head: head, files: strings.Join(files, ", "), base: base}

	return nil
}

// catchupTarget is the sha the catchup step will bring in for a mark, and
// its name for prose, resolved as that step's point 2 resolves the base:
// f10-landed is origin's default branch, else the local one; f10-base-moved
// holds the base branch's head itself.
func (in *driveInput) catchupTarget(ctx context.Context, r *driveRun, key string) (string, string, error) {
	if key == cfgMoved {
		target, err := gitx.Config(ctx, in.main, "branch."+r.o.branch+"."+key)
		if err != nil {
			return "", "", fmt.Errorf("reading the %s mark on %s: %w", key, r.o.branch, err)
		}

		return target, "its base", nil
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return "", "", err
	}

	for _, ref := range []string{"refs/remotes/origin/" + def, "refs/heads/" + def} {
		target, err := gitx.Ref(ctx, in.main, ref)
		if err != nil {
			return "", "", fmt.Errorf("reading %s: %w", ref, err)
		}

		if target != "" {
			return target, strings.TrimPrefix(strings.TrimPrefix(ref, "refs/remotes/"), "refs/heads/"), nil
		}
	}

	return "", "", fmt.Errorf("cannot read %s here", def)
}

// conflictAgain says why a conflict with target, after the catchup this
// drive sent, halts the task rather than sending another catchup, or "" to
// catch up again; the files that conflict now are named either way. The
// reads halt on a git that cannot answer, as every read before a push does.
func (in *driveInput) conflictAgain(ctx context.Context, r *driveRun, target, base string) (string, error) {
	if r.lastCatchup == nil {
		return "", nil
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading the head of %s: %w", r.o.branch, err)
	}

	paths, err := gitx.Conflicts(ctx, r.o.path, target, "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading what %s conflicts on: %w", r.o.branch, err)
	}

	files := strings.Join(paths, ", ")

	reason := r.lastCatchup.halts(target, head, files, base)
	if reason != "" && files != "" && !strings.Contains(reason, files) {
		reason += "; conflicting: " + files
	}

	return reason, nil
}

// catchupHalt is the halt a conflict the drive will not catch up again
// leaves: the reason, and how to settle it by hand.
func catchupHalt(r *driveRun, skill, base, reason string) driveResult {
	res := halted(r.task.ref.ID, skill, fmt.Sprintf("%s still conflicts with %s after a catchup: %s",
		r.o.branch, base, reason))
	res.lines = append(res.lines, "next: settle it with /f10:catchup in "+r.o.path+", then rerun the drive")

	return res
}

// spikeDone reports whether the task's ship ended as a spike that finish
// closes without a merge: past ship in its chain, a spike report written,
// and the branch never pushed, since a pushed one finishes through its PR.
func (in *driveInput) spikeDone(ctx context.Context, r *driveRun) (bool, error) {
	i := slices.Index(r.chain, "ship")
	if i < 0 || r.pos <= i || taskSpike(r.o.path, in.plansDir, r.task.ref.ID) == "" {
		return false, nil
	}

	pushed, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+r.o.branch)
	if err != nil {
		return false, fmt.Errorf("reading whether %s was pushed: %w", r.o.branch, err)
	}

	return pushed == "", nil
}

// closeAfterShip adds finish to a spike's chain that lacks it: a spike close
// merges nothing, which drive_finish gates, and a spike left open keeps its
// dependents on an empty base. status reads the recorded chain.
func (in *driveInput) closeAfterShip(ctx context.Context, r *driveRun) error {
	r.chain = append(slices.Clone(r.chain), "finish")

	if err := gitx.SetConfig(ctx, in.main, "branch."+r.o.branch+"."+cfgChain, strings.Join(r.chain, " ")); err != nil {
		return fmt.Errorf("recording the chain: %w", err)
	}

	return nil
}

// baseHalted ends a dependent whose base halted after releasing it, at the
// boundary before skill: its work stays on its branch.
func baseHalted(r *driveRun, skill string) driveResult {
	id, base := r.task.ref.ID, r.base.task.ref.ID
	reason := fmt.Sprintf("base %s halted, its work stays on %s", base, r.o.branch)

	return driveResult{
		row:    "halted: base " + base + " halted",
		code:   exitHalted,
		lines:  []string{fmt.Sprintf("%s halted before %s: %s", id, skill, reason)},
		skill:  skill,
		reason: reason,
	}
}

// readyToMerge brings the branch to where it merges cleanly, or says why
// it cannot. A pushed branch is merged by the host from origin's copy, so
// that is tried against origin's default branch: clean is ready; a local
// branch already caught up is pushed. A branch origin does not have is
// merged locally by finish, so it is tried against the local default
// branch. Anything else is marked landed, which sends it a catchup; after
// one, HEAD is what is tried, and a conflict that made no progress halts.
func (in *driveInput) readyToMerge(ctx context.Context, w io.Writer, r *driveRun) *driveResult {
	id, branch := r.task.ref.ID, r.o.branch

	def, err := localDefault(ctx, in.main)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	if _, err := gitx.Exec(ctx, r.o.path, "fetch", "origin"); err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	pushed, err := gitx.Ref(ctx, r.o.path, "origin/"+branch)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	base, head := "origin/"+def, pushed

	if pushed == "" {
		if base, err = landingBase(ctx, in.main, def); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}

		head = "refs/heads/" + branch
	}

	// every read before a push halts on a git that cannot answer: a wrong
	// "not an ancestor" would pick the lease and rewrite origin's branch
	clean, err := gitx.MergesCleanly(ctx, r.o.path, base, head)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	if clean {
		return nil
	}

	caughtUp := false
	if pushed != "" {
		if caughtUp, err = gitx.IsAncestor(ctx, r.o.path, base, "HEAD"); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}
	}

	// after a catchup, origin's copy is the tree before it: HEAD is what the
	// catchup left and what the push would send, so HEAD is what is tried
	if !caughtUp && pushed != "" && r.lastCatchup != nil {
		if caughtUp, err = gitx.MergesCleanly(ctx, r.o.path, base, "HEAD"); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}
	}

	if caughtUp {
		if err := in.pushCaughtUp(ctx, w, r, "finish", pushed, base); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}

		return nil
	}

	if r.lastCatchup != nil {
		target, err := gitx.Ref(ctx, r.o.path, base)
		if err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}

		reason, err := in.conflictAgain(ctx, r, target, base)

		switch {
		case err != nil:
			res := halted(id, "finish", err.Error())

			return &res
		case reason != "":
			res := catchupHalt(r, "finish", base, reason)

			return &res
		}
	}

	if err := gitx.SetConfig(ctx, in.main, "branch."+branch+"."+cfgLanded, def); err != nil {
		res := halted(id, "finish", fmt.Sprintf("marking %s landed on %s: %v", branch, def, err))

		return &res
	}

	fmt.Fprintf(w, "%s finish: %s conflicts with %s, catching up first\n", id, branch, base)
	in.finishStep(ctx, w, branch, ref.FinishQueued, "catching up with "+base)

	return nil
}

// pushCaughtUp pushes a caught-up branch over origin's copy at pushed:
// plain when the catchup merged, so origin's copy is an ancestor, with a
// lease against that sha when it rebased. skill names the step for the
// line it prints and the error it returns.
func (in *driveInput) pushCaughtUp(ctx context.Context, w io.Writer, r *driveRun, skill, pushed, base string) error {
	id, branch := r.task.ref.ID, r.o.branch

	fastForward, err := gitx.IsAncestor(ctx, r.o.path, pushed, "HEAD")
	if err != nil {
		return err
	}

	lease, how := "", "pushed"
	if !fastForward {
		lease, how = pushed, "pushed with --force-with-lease (the catchup rewrote it)"
	}

	if err := gitx.Push(ctx, r.o.path, branch, lease); err != nil {
		return fmt.Errorf("pushing %s, caught up with %s: %w", branch, base, err)
	}

	fmt.Fprintf(w, "%s %s: %s caught up with %s, %s\n", id, skill, branch, base, how)

	return nil
}

// pushCatchup pushes a branch origin has once a catchup ended clean, so its
// pull request carries the integration before the next skill; a branch never
// pushed is left to ship's pr step or to finish. Origin is fetched first: the
// lease must be origin's current sha, never a stale tracking ref.
func (in *driveInput) pushCatchup(ctx context.Context, w io.Writer, r *driveRun) *driveResult {
	id, branch := r.task.ref.ID, r.o.branch

	halt := func(err error) *driveResult {
		res := halted(id, "catchup", err.Error())
		res.lines = append(res.lines, "next: rerun the drive")

		return &res
	}

	pushed, err := gitx.Ref(ctx, r.o.path, "refs/remotes/origin/"+branch)
	if err != nil {
		return halt(err)
	}

	if pushed == "" {
		return nil
	}

	if _, err := gitx.Exec(ctx, r.o.path, "fetch", "origin"); err != nil {
		return halt(err)
	}

	if pushed, err = gitx.Ref(ctx, r.o.path, "refs/remotes/origin/"+branch); err != nil {
		return halt(err)
	}

	head, err := gitx.Ref(ctx, r.o.path, "HEAD")
	if err != nil {
		return halt(err)
	}

	if head == pushed {
		return nil
	}

	base := "its base"
	if r.lastCatchup != nil {
		base = r.lastCatchup.base
	}

	if err := in.pushCaughtUp(ctx, w, r, "catchup", pushed, base); err != nil {
		return halt(err)
	}

	return nil
}

// The answers drive's own finish question takes, for a PR someone else
// opened: merge it, or clean up locally and leave it open.
const (
	finishMerge = "merge"
	finishLocal = "local"
)

// runFinish runs finish in this process. Its refusals halt the task with
// the reason, except a PR someone else opened, which asks; its report is
// indented under the progress lines.
func (in *driveInput) runFinish(ctx context.Context, w io.Writer, r *driveRun) driveResult {
	task, id := r.task, r.task.ref.ID

	pr, err := in.finishAnswer(id)
	if err != nil {
		return halted(id, "finish", err.Error())
	}

	fmt.Fprintf(w, "%s finish: running\n", id)

	var buf bytes.Buffer

	in.finishStep(ctx, w, r.o.branch, ref.FinishRunning, "starting")

	err = in.finish(ctx, &buf, task.ref, pr, func(action string) {
		in.finishStep(ctx, w, r.o.branch, ref.FinishRunning, action)
	})

	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		fmt.Fprintf(w, "  %s\n", sc.Text())
	}

	if fp := (foreignPullError{}); errors.As(err, &fp) {
		ask := fmt.Sprintf("1. %s was opened by %s: [%s | %s]", fp.url, fp.author, finishLocal, finishMerge)

		return driveResult{
			row:   "asked at finish",
			code:  exitAsk,
			lines: []string{id + " asks at finish: " + ask},
			ask:   ask,
		}
	}

	if err != nil {
		return halted(id, "finish", err.Error())
	}

	// finish runs in this process, where no hook sees it
	if in.record != nil {
		in.record(ctx, in.main, ledger.Event{
			At:      in.now(),
			Task:    id,
			Kind:    ledger.TaskFinished,
			Source:  ledger.Observed,
			Payload: ledger.NewPayload(ledger.Payload{"args": []any{id}}),
		})
	}

	return driveResult{row: "finished"}
}

// finishStep records on the branch where drive's finish of it stands and
// what it is doing, for status to read. A failed write is a line, never a
// halt: the record is only a view.
func (in *driveInput) finishStep(ctx context.Context, w io.Writer, branch, st, action string) {
	if branch == "" {
		return
	}

	rec := ref.DriveFinish{State: st, Action: action}
	if err := gitx.SetConfig(ctx, in.main, "branch."+branch+"."+ref.FinishKey, rec.Format()); err != nil {
		fmt.Fprintf(w, "  recording finish's progress on %s: %v\n", branch, err)
	}
}

// clearFinish drops the finish record of a task this drive opened with
// finish in its chain.
func (in *driveInput) clearFinish(ctx context.Context, r *driveRun) {
	if !r.opened || !slices.Contains(r.chain, "finish") {
		return
	}

	_ = gitx.UnsetConfig(ctx, in.main, "branch."+r.o.branch+"."+ref.FinishKey) // absent is fine
}

// finishAnswer is the answer to finish's question this drive was resumed
// with: one keyed to the task, which must be merge or local, else a bare
// one that is, so a bare answer meant for an agent's question stays there.
func (in *driveInput) finishAnswer(id string) (string, error) {
	if a, ok := in.answers[id]; ok {
		delete(in.answers, id)
		in.took(id, "finish", a)

		if c := finishChoice(a); c != "" {
			return c, nil
		}

		return "", fmt.Errorf("the answer to finish is %q: answer %s or %s", a, finishLocal, finishMerge)
	}

	if c := finishChoice(in.answer); c != "" {
		in.answer = ""
		in.took(id, "finish", c)

		return c, nil
	}

	return "", nil
}

// finishChoice reads `local`, `merge` or `1. local` as finish's answer, or "".
func finishChoice(answer string) string {
	a := strings.ToLower(strings.TrimSpace(answer))
	a = strings.TrimSpace(strings.TrimPrefix(a, "1."))

	if a == finishMerge || a == finishLocal {
		return a
	}

	return ""
}

// send runs one element in the task's agent: attach to a turn already
// running, answer a question it stopped on, or prompt the element. The
// poll reads how it ends.
func (in *driveInput) send(ctx context.Context, w io.Writer, r *driveRun, skill string) error {
	id := r.task.ref.ID

	ws, run, err := in.look(ctx, r)
	if err != nil {
		return err
	}

	run = r.current(run)
	r.skill, r.sentAt, r.begun, r.idleSince = skill, in.now(), false, time.Time{}

	in.saveTask(r, func(t *drives.Task) { t.Skill, t.Outcome = skill, nil })

	answeredAt, answered := in.answeredIn[id]
	delete(in.answeredIn, id)

	switch {
	case ws.Status == agentctl.StatusWorking:
		r.begun = true

		fmt.Fprintf(w, "%s %s: attached to the turn in progress\n", id, skill)
	case answered:
		// the agent heard the answer where it was given: its turn began
		// then, and the prompt mark the hook wrote just before the claim
		// may fall in the second before
		r.sentAt = answeredAt.Add(-time.Second).Truncate(time.Second)

		fmt.Fprintf(w, "%s %s: answered outside this drive, following the turn\n", id, skill)
	case run != nil && skill == "catchup" && guardStop(run):
		// the ship stop is the one this catchup settles
		return in.prompt(ctx, w, r, ws, skill, "")
	case run != nil && stoppedRun(run):
		answer := in.takeAnswer(id)
		if answer == "" {
			// a catchup clears a mark but restores the ship stop it noted, so
			// git, not the note, says whether that stop still holds
			if at := resumeAt(skill, run); at != "" && in.unsettled(ctx, r) == "" {
				fmt.Fprintf(w, "%s %s: stopped for a catchup, the branch is settled: %s\n", id, skill, at)

				return in.prompt(ctx, w, r, ws, skill, at)
			}

			in.end(ctx, w, r, in.verdict(r.task, skill, run))

			return nil
		}

		// closed before the prompt, so the pane hook does not read the
		// drive's own answer as one typed there
		in.claimSent(r, answer)

		if err := promptAgent(ctx, r.o.path, id, r.task.suffix, answer); err != nil {
			return err
		}

		in.took(id, skill, answer)
		fmt.Fprintf(w, "%s %s: answer sent\n", id, skill)
	case run != nil && phaseDone(run, skill):
		// the agent got there before a drive that stopped could see it
		fmt.Fprintf(w, "%s %s: already done\n", id, skill)

		if err := gitx.SetConfig(ctx, in.main, "branch."+r.o.branch+"."+cfgDrive, skill); err != nil {
			return fmt.Errorf("recording %s done: %w", skill, err)
		}

		r.pos++

		return in.next(ctx, w, r)
	default:
		return in.prompt(ctx, w, r, ws, skill, "")
	}

	return nil
}

// prompt sends the skill to an agent at rest, steering beside the task,
// unless the agent idles on a run still running.
func (in *driveInput) prompt(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	ws *agentctl.Workspace,
	skill, steering string,
) error {
	id := r.task.ref.ID

	fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: r.sentAt, since: r.freshAt}
	if err := refuseStalled(fwd, ws, agentctl.AgentName(id, r.task.suffix, r.o.path), r.o.path); err != nil {
		in.end(ctx, w, r, halted(id, skill, err.Error()))

		return nil
	}

	p := steered(skillPrompt(skill, r.task.ref.Label(), r.o.dep, r.flow, slices.Contains(r.chain, "finish")), steering)
	if err := promptAgent(ctx, r.o.path, id, r.task.suffix, p); err != nil {
		return err
	}

	fmt.Fprintf(w, "%s %s: sent\n", id, skill)

	return nil
}

// resumeAt is the steering that resumes a ship the catchup guard stopped,
// `resume at pr`, naming its place when the flow names the entry twice;
// "" for any other stop. A failed catchup is no flow entry but keeps the
// place of the entry it guarded, which is where ship resumes.
func resumeAt(skill string, run *state.Run) string {
	if skill != "ship" || run.Ask != "" || !state.Stopped(run.Ship) {
		return ""
	}

	entry := run.Leaf

	switch {
	case entry == "catchup" && run.At > 0 && run.At <= len(run.Pipeline):
		entry = run.Pipeline[run.At-1]
	case !strings.Contains(run.Next, "/f10:catchup"):
		return ""
	}

	step, _, _ := strings.Cut(entry, ":")
	if !slices.Contains(guardedSteps, step) || !slices.Contains(run.Pipeline, entry) {
		return ""
	}

	s := "resume at " + entry
	twice := slices.Contains(run.Pipeline[slices.Index(run.Pipeline, entry)+1:], entry)
	if run.At > 0 && twice {
		s += fmt.Sprintf(" (entry %d)", run.At)
	}

	return s
}

// guardedSteps are the entries ship's catchup guard runs before.
var guardedSteps = []string{"implement", "pr", "judge"}

// steered puts steering after the skill's subject, before any chained
// clause and the driven marker, so the skill reads it as its own.
func steered(prompt, steering string) string {
	if steering == "" {
		return prompt
	}

	if i := strings.Index(prompt, " && "); i >= 0 {
		return prompt[:i] + " " + steering + prompt[i:]
	}

	if base, ok := strings.CutSuffix(prompt, " "+drivenMarker); ok {
		return base + " " + steering + " " + drivenMarker
	}

	return prompt + " " + steering
}

// phaseRunning reports run's plan or ship phase still running: the two
// phases a turn owns, so the turn has not reported its end.
func phaseRunning(run *state.Run, phase string) bool {
	return (phase == "plan" || phase == "ship") && run != nil && run.Status(phase) == "running"
}

// stalled reports an agent idle for at least grace while its run says
// phase is running: a turn that ended without reporting, once the Stop
// hook had time. drive and the watcher both read a stall this way.
func stalled(status agentctl.Status, run *state.Run, phase string, idle, grace time.Duration) bool {
	return (status == agentctl.StatusIdle || status == agentctl.StatusDone) && phaseRunning(run, phase) && idle >= grace
}

// phaseDone reports a run past skill: plan and ship own a phase, which
// reads done, or prior for a plan an earlier run wrote.
func phaseDone(run *state.Run, skill string) bool {
	if skill != "plan" && skill != "ship" {
		return false
	}

	s := run.Status(skill)

	return s == "done" || s == "prior"
}

// takeAnswer is the answer for a task stopped on a question: its own keyed
// one, else the bare one. Either is taken once.
func (in *driveInput) takeAnswer(id string) string {
	if a, ok := in.answers[id]; ok {
		delete(in.answers, id)

		return a
	}

	a := in.answer
	in.answer = ""

	return a
}

// poll reads one task's turn from this tick's look. A turn has begun once
// Herdr shows the agent working or its run reports after the prompt:
// Herdr's `done` lasts from the previous turn until someone looks, so it
// proves nothing. Once the agent is idle again, the run says how the turn
// ended.
func (in *driveInput) poll(
	ctx context.Context,
	w io.Writer,
	r *driveRun,
	wss []agentctl.Workspace,
	live []*state.Run,
) error {
	id, skill := r.task.ref.ID, r.skill

	if r.waiting {
		return in.next(ctx, w, r)
	}

	ws := workspaceByID(wss, r.o.ws.ID)
	if ws == nil {
		return in.workspaceGone(r)
	}

	var run *state.Run
	if runs := inRoot(live, r.o.path); len(runs) > 0 {
		run = r.current(runs[0])
	}

	now := in.now()
	since := r.sentAt.Truncate(time.Second) // the state file keeps whole seconds
	recent := run != nil && !run.Updated.Before(since)

	switch ws.Status {
	case agentctl.StatusWorking:
		r.begun, r.idleSince = true, time.Time{}

		return nil
	case agentctl.StatusIdle, agentctl.StatusDone:
	default:
		// Herdr shows no agent yet: the start window bounds this wait too
		if !r.begun && now.Sub(r.sentAt) > startWindow {
			in.end(ctx, w, r, halted(id, skill, fmt.Sprintf("workspace %s shows no agent %s after the prompt: "+
				"check its pane, then rerun this drive", workspaceName(ws), startWindow)))
		}

		return nil
	}

	// a prompt mark proves a turn the looks missed: one that began and ended
	// between two of them
	if !r.begun && !recent {
		if p, ok := state.LastPrompt(in.stateDir, r.o.path); ok && !p.At.Before(since) {
			if now.Sub(p.At) < promptSettle {
				return nil
			}

			r.begun = true
		}
	}

	if !r.begun && !recent {
		if now.Sub(r.sentAt) > startWindow {
			in.end(
				ctx,
				w,
				r,
				halted(id, skill, fmt.Sprintf("the agent in workspace %s did not take the prompt within %s",
					workspaceName(ws), startWindow)),
			)
		}

		return nil
	}

	r.begun = true

	if recent && stoppedRun(run) {
		switch {
		case skill == "catchup" && guardStop(run):
			// a catchup that stopped and then ended clean restores the stop it
			// found, so git says how it ended
		case skill == "ship" && guardStop(run) && resumeAt(skill, run) != "" && in.marked(ctx, r):
			// the stop the guard leaves names the catchup ship resumes from; a
			// mark on a ship that failed elsewhere is the watcher's, not the guard's
			if res := in.guardAgain(ctx, r); res != nil {
				in.end(ctx, w, r, *res)

				return nil
			}

			fmt.Fprintf(w, "%s ship: the catchup guard conflicted, catching up\n", id)

			return in.next(ctx, w, r)
		default:
			in.end(ctx, w, r, in.verdict(r.task, skill, run))

			return nil
		}
	}

	if phaseRunning(run, skill) {
		if r.idleSince.IsZero() {
			r.idleSince = now
		}

		if stalled(ws.Status, run, skill, now.Sub(r.idleSince), stallGrace) {
			in.end(
				ctx,
				w,
				r,
				halted(id, skill, fmt.Sprintf("agent %s in workspace %s is idle while its run says %s %s: "+
					"it stopped without reporting, answer it in that pane", agentctl.AgentName(id, r.task.suffix, r.o.path),
					workspaceName(ws), skill, run.PhaseText(skill))),
			)
		}

		return nil
	}

	fmt.Fprintf(w, "%s %s: done\n", id, skill)

	if skill == "catchup" {
		if reason := in.unsettled(ctx, r); reason != "" {
			res := halted(id, skill, "the catchup did not end clean: "+reason)
			res.lines = append(res.lines, "next: settle it with /f10:catchup in "+r.o.path+", then rerun the drive")
			in.end(ctx, w, r, res)

			return nil
		}

		if res := in.pushCatchup(ctx, w, r); res != nil {
			in.end(ctx, w, r, *res)

			return nil
		}

		return in.next(ctx, w, r)
	}

	if err := gitx.SetConfig(ctx, in.main, "branch."+r.o.branch+"."+cfgDrive, skill); err != nil {
		return fmt.Errorf("recording %s done: %w", skill, err)
	}

	r.pos++

	return in.next(ctx, w, r)
}

// guardAgain is the halt a ship whose catchup guard conflicted again gets
// when the catchup this drive already sent made no progress, or nil to
// catch up again (a first conflict, or a target that moved). A mark git
// cannot read is left to next, which halts on it.
func (in *driveInput) guardAgain(ctx context.Context, r *driveRun) *driveResult {
	if r.lastCatchup == nil {
		return nil
	}

	key, err := in.mark(ctx, r)
	if err != nil || key == "" {
		return nil
	}

	target, base, err := in.catchupTarget(ctx, r, key)
	if err != nil {
		res := halted(r.task.ref.ID, "ship", err.Error())

		return &res
	}

	reason, err := in.conflictAgain(ctx, r, target, base)

	switch {
	case err != nil:
		res := halted(r.task.ref.ID, "ship", err.Error())

		return &res
	case reason != "":
		res := catchupHalt(r, "ship", base, reason)

		return &res
	}

	return nil
}

// marked reports whether the task's branch is marked for a catchup.
// A mark git cannot read counts as marked: the catchup it sends ends in
// unsettled, which names the read and halts.
func (in *driveInput) marked(ctx context.Context, r *driveRun) bool {
	key, err := in.mark(ctx, r)

	return key != "" || err != nil
}

// mark is the key the branch is marked with, or "": f10-landed when its
// base's code is in the default branch and not yet in this one, else
// f10-base-moved when its base branch moved past it.
func (in *driveInput) mark(ctx context.Context, r *driveRun) (string, error) {
	for _, key := range []string{cfgLanded, cfgMoved} {
		v, err := gitx.Config(ctx, in.main, "branch."+r.o.branch+"."+key)
		if err != nil {
			return "", fmt.Errorf("reading the %s mark on %s: %w", key, r.o.branch, err)
		}

		if v != "" {
			return key, nil
		}
	}

	return "", nil
}

// unsettled says why a catchup that ended without a stop did not bring the
// branch up to date, or "" when it did. Catchup owns no phase, so its
// success is read from git: the mark gone, which catchup clears only once
// verify is green, no merge or rebase left open, and a clean tree.
func (in *driveInput) unsettled(ctx context.Context, r *driveRun) string {
	key, err := in.mark(ctx, r)

	switch {
	case err != nil:
		return err.Error()
	case key != "":
		return "the branch is still marked " + key
	}

	for _, head := range []struct{ ref, what string }{{"MERGE_HEAD", "merge"}, {"REBASE_HEAD", "rebase"}} {
		sha, err := gitx.Ref(ctx, r.o.path, head.ref)

		switch {
		case err != nil:
			return fmt.Sprintf("could not tell whether a %s is in progress in %s: %v", head.what, r.o.path, err)
		case sha != "":
			return fmt.Sprintf("a %s is still in progress in %s", head.what, r.o.path)
		}
	}

	dirty, err := gitx.Status(ctx, r.o.path)

	switch {
	case err != nil:
		return err.Error()
	case len(dirty) > 0:
		return fmt.Sprintf("%d uncommitted paths in %s", len(dirty), r.o.path)
	}

	return ""
}

// verdict reads a stopped run: an ask goes to the user, anything else halts.
func (*driveInput) verdict(task driveTask, skill string, run *state.Run) driveResult {
	id := task.ref.ID

	if run.Ask != "" {
		return driveResult{
			row:   "asked at " + skill,
			code:  exitAsk,
			lines: []string{fmt.Sprintf("%s asks at %s: %s", id, skill, run.Ask)},
			ask:   run.Ask,
		}
	}

	reason := run.Note
	if reason == "" {
		for _, p := range state.Phases {
			if state.Stopped(run.Status(p)) {
				reason = "the run says " + p + " " + run.PhaseText(p)

				break
			}
		}
	}

	res := halted(id, skill, reason)
	res.fromRun = true

	if run.Next != "" {
		res.lines = append(res.lines, "next: "+run.Next)
	}

	return res
}

// end ends the task with res and prints it now: a halt or an ask found
// mid-drive is on screen when it happens, not only in the final report. A
// halt also goes to the ledger, where status reads it after the drive exits.
func (in *driveInput) end(ctx context.Context, w io.Writer, r *driveRun, res driveResult) {
	r.end = &res

	// a finished task's branch took the record with it
	if res.row != "finished" {
		in.clearFinish(ctx, r)
	}

	in.saveTask(r, func(t *drives.Task) {
		t.Skill, t.Outcome = "", &drives.Outcome{Row: res.row, Code: res.code}
	})
	in.unlease(r, in.runs)

	if res.code == exitAsk && res.ask != "" {
		in.raise(ctx, r, strings.TrimPrefix(res.row, "asked at "), res.ask)
	}

	if res.code == exitHalted && !res.fromRun && !res.drives {
		in.recordHalt(ctx, r, res)
	}

	if len(res.lines) == 0 {
		fmt.Fprintf(w, "%s: %s\n", r.task.ref.ID, res.row)

		return
	}

	for _, l := range res.lines {
		fmt.Fprintln(w, l)
	}
}

func halted(id, skill, reason string) driveResult {
	return driveResult{
		row:    "halted at " + skill,
		code:   exitHalted,
		lines:  []string{fmt.Sprintf("%s halted at %s: %s", id, skill, reason)},
		skill:  skill,
		reason: reason,
	}
}

// recordHalt puts a halt drive found itself, an idle agent or a refused
// finish, in the ledger against the task, placed in its worktree when it
// has one.
func (in *driveInput) recordHalt(ctx context.Context, r *driveRun, res driveResult) {
	if in.record == nil {
		return
	}

	in.record(ctx, cmp.Or(r.o.path, in.main), ledger.Event{
		At:      in.now(),
		Task:    r.task.ref.ID,
		Kind:    ledger.TaskHalted,
		Source:  ledger.Observed,
		Payload: ledger.NewPayload(ledger.Payload{"skill": res.skill, "reason": res.reason}),
	})
}

// recordAt records e in the ledger, placed where dir is.
func recordAt(ctx context.Context, dir string, e ledger.Event) {
	place := ledger.Where(ctx, dir)
	e.Project, e.ProjectLocal, e.Checkout, e.Branch = place.Project, place.ProjectLocal, place.Checkout, place.Branch

	ledger.Record(e)
}

// guardStop reports a ship stop shaped like the catchup guard's: failed
// or partial with no question, at an entry the guard runs before. A
// judge's stop is blocked, and a driven catchup stops at leaf catchup.
func guardStop(run *state.Run) bool {
	step, _, _ := strings.Cut(run.Leaf, ":")

	return run.Ask == "" && (run.Ship == "failed" || run.Ship == "partial") && slices.Contains(guardedSteps, step)
}

// stoppedRun reports whether any phase halted: blocked, failed or partial.
func stoppedRun(r *state.Run) bool {
	return slices.ContainsFunc(state.Phases, func(p string) bool { return state.Stopped(r.Status(p)) })
}

// look is the task's workspace, by the id open settled on, since a
// checkout can show in more than one, and the newest run reported from the
// checkout. A workspace gone mid-run is an error: the agent went with it.
func (in *driveInput) look(ctx context.Context, r *driveRun) (*agentctl.Workspace, *state.Run, error) {
	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	ws := workspaceByID(wss, r.o.ws.ID)
	if ws == nil {
		return nil, nil, in.workspaceGone(r)
	}

	run, err := in.run(r.o.path)

	return ws, run, err
}

func (in *driveInput) workspaceGone(r *driveRun) error {
	return fmt.Errorf("%s no longer shows workspace %s, which showed %s", agents(in.main).Name(), r.o.ws.ID, r.o.path)
}

// workspaceName is a workspace as a halt names it: its label and its id.
func workspaceName(ws *agentctl.Workspace) string {
	if ws.Label == "" || ws.Label == ws.ID {
		return ws.ID
	}

	return ws.Label + " (" + ws.ID + ")"
}

// lookAll is one tick's view: every workspace Herdr shows and every live
// run, read once for all the tasks in flight.
func (in *driveInput) lookAll(ctx context.Context) ([]agentctl.Workspace, []*state.Run, error) {
	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, nil, err
	}

	return wss, live, nil
}

// run is the newest live run recorded against the checkout, or nil.
func (in *driveInput) run(path string) (*state.Run, error) {
	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, err
	}

	if runs := inRoot(live, path); len(runs) > 0 {
		return runs[0], nil
	}

	return nil, nil
}

// skillPrompt is what the agent is told for one skill, marked driven. Driven
// mode takes every gap on its default and reuses a saved plan, so plan is
// the bare skill and ship names only its flow, unless default; both name
// the dependency when the task has a base. catchup is not a chain skill:
// drive sends it to a branch its finished base marked landed. id is a
// bundle's label, `ABC-1+ABC-2`: plan and ship take it whole, every other
// skill its lead, from which it finds the branch. finish says drive
// finishes the task after ship, which ship's report names so nobody waits
// in its pane.
func skillPrompt(skill, id string, dep *dependency, fl shipFlow, finish bool) string {
	var p string

	if skill != "plan" && skill != "ship" {
		id, _, _ = strings.Cut(id, ref.Separator)
	}

	switch skill {
	case "ship":
		p = "/f10:ship " + fl.word() + id
	case "catchup":
		p = "/f10:catchup" // it catches up the branch its session stands on, and takes no id
	default:
		p = "/f10:" + skill + " " + id
	}

	if dep != nil && (skill == "plan" || skill == "ship") {
		p += " && " + dependencyClause(id, dep)
	}

	if finish && skill == "ship" {
		p += " && " + finishClause
	}

	return driven(p)
}

// finishClause tells a driven ship that drive finishes the task after it.
const finishClause = "drive finishes it after ship and removes this worktree"

// resumeCommand is the command line as given, minus any --answer, quoted
// where a token needs it.
func resumeCommand(argv []string) string {
	parts := []string{"f10", "drive"}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		switch {
		case tok == "--answer":
			i++

			continue
		case strings.HasPrefix(tok, "--answer="):
			continue
		case strings.ContainsAny(tok, " \t\"'"):
			tok = strconv.Quote(tok)
		}

		parts = append(parts, tok)
	}

	return strings.Join(parts, " ")
}

// sleepCtx waits d, or less when ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// sharedMember is the first id two listed entries both name.
func sharedMember(a, b ref.Ref) string {
	for _, id := range b.Members() {
		if slices.Contains(a.Members(), id) {
			return id
		}
	}

	return b.ID
}
