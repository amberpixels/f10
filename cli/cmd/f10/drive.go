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
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
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
// The chain is skills, not pipeline steps: start and finish create and
// destroy the worktree and are this binary's own, so drive runs them in
// process; plan, judge, ship, review and resolve go to the task's agent
// as prompts. After each prompt it polls Herdr's agent status for the
// turn's start and end, and the task's run state for the outcome.
//
// Rerunning the same command is the resume. A finished task is skipped, a
// started one reads its position from its branch, and an ask is answered
// by the same command with --answer, one per asking task.
func driveCommand() *cli.Command {
	return &cli.Command{
		Name:  "drive",
		Usage: "run a list of tasks through a chain of skills, each once its base is finished, stopping when an agent asks",
		ArgsUsage: "<id>... | <id> -- <id>  [plan|judge|ship|review|resolve|finish ...]  " +
			"[--answer [ID=]TEXT ...] [--every DURATION]",
		Description: "The chain defaults to project.md's Drive chain, else " +
			"plan → judge → ship → review → resolve → finish.\n" +
			"A task's base is its branch's recorded --after, else its body's After: line; " +
			"every task whose base is finished runs at once, and a halt stops only its dependents.\n" +
			"Exit 0: every task went through its chain. 4: an agent asks; rerun with --answer. " +
			"5: a task halted (a judge stop, a failure, an agent that stopped without reporting, its base halted).",
		// a range is `GH-12 -- GH-15`, and flag parsing would swallow the --
		SkipFlagParsing: true,
		Action:          runDrive,
	}
}

// Exit codes beyond start's: an ask the skill relays, and any other halt.
const (
	exitAsk    = 4
	exitHalted = 5
)

const (
	defaultEvery = 15 * time.Second
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
)

// chainSkills is the chain's vocabulary. start is implicit: a task needs a
// worktree before anything else runs.
var chainSkills = []string{"plan", "judge", "ship", "review", "resolve", "finish"}

func runDrive(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	a, err := parseDriveArgs(argv)
	if err != nil {
		return err
	}

	if err := herdr.Available(); err != nil {
		return err
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	chain := a.chain
	if len(chain) == 0 {
		if chain, err = parseChain(t.fact("Drive chain")); err != nil {
			return fmt.Errorf("project.md Drive chain: %w", err)
		}
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	drv := driver.Find(t.lay.StorageRoot, t.dir)
	h, hostErr := t.host()

	if slices.Contains(chain, "finish") && hostErr != nil {
		return fmt.Errorf("the chain ends in finish, which merges through the host: %w", hostErr)
	}

	return drive(ctx, cmd.Writer, &driveInput{
		main:     main,
		driver:   drv,
		tracker:  t.tracker(),
		args:     a,
		argv:     argv,
		chain:    chain,
		remote:   t.eff.Review.Remote(),
		plansDir: t.sharedPlansDir(),
		title:    func(r ref.Ref) func(context.Context) (string, error) { return titleLookup(t, r.Number) },
		finish: func(ctx context.Context, w io.Writer, task ref.Ref) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getwd: %w", err)
			}

			return finish(ctx, w, finishInput{
				main:      main,
				driver:    drv,
				task:      task,
				host:      h.at(main),
				hosting:   t.fact("Hosting & PR"),
				cwd:       cwd,
				workspace: os.Getenv("HERDR_WORKSPACE_ID"),
				plansDir:  t.sharedPlansDir(),
			})
		},
		stateDir: state.Dir(),
		ttl:      state.TTL(),
		now:      time.Now,
		sleep:    sleepCtx,
	})
}

// driveArgs is the command line, parsed by hand so `--` survives.
type driveArgs struct {
	tasks   []string // as typed; with span, the two ends of a range
	span    bool
	chain   []string
	answers []string // each --answer as typed: `GH-1=text` for one task, or bare text
	every   time.Duration
}

// parseDriveArgs reads tasks, then skills, with --answer and --every
// anywhere. A token in the chain vocabulary is a skill; every other one is
// a task, and a task after a skill is refused, since a list and a chain
// interleaved is a typo more often than a plan.
func parseDriveArgs(args []string) (driveArgs, error) {
	a := driveArgs{every: defaultEvery}

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])

		name, value, inline := strings.Cut(tok, "=")
		if name == "--answer" || name == "--every" {
			if !inline {
				if i+1 >= len(args) {
					return driveArgs{}, fmt.Errorf("%s needs a value", name)
				}

				i++
				value = args[i]
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
		case tok == "--":
			if a.span || len(a.tasks) != 1 || len(a.chain) > 0 {
				return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
			}

			a.span = true
		case isArrow(tok):
			continue
		case slices.Contains(chainSkills, skill):
			a.chain = append(a.chain, skill)
		case strings.HasPrefix(tok, "-"):
			return driveArgs{}, fmt.Errorf("unknown flag %s: drive takes --answer and --every", tok)
		case len(a.chain) > 0:
			return driveArgs{}, fmt.Errorf("%s follows the chain: the tasks come first, then the skills", tok)
		default:
			a.tasks = append(a.tasks, tok)
		}
	}

	switch {
	case len(a.tasks) == 0:
		return driveArgs{}, errors.New("drive needs the tasks: GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.span && len(a.tasks) != 2:
		return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	}

	if err := checkChain(a.chain); err != nil {
		return driveArgs{}, err
	}

	return a, nil
}

func (a *driveArgs) set(name, value string) error {
	if name == "--answer" {
		if value = strings.TrimSpace(value); value != "" {
			a.answers = append(a.answers, value)
		}

		return nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("--every %s: a positive duration, like 15s", value)
	}

	a.every = d

	return nil
}

func isArrow(tok string) bool {
	return tok == "→" || tok == "->" || tok == ","
}

// parseChain reads a declared chain: the skills in order, separated by
// arrows, commas or spaces, a list bullet allowed in front.
func parseChain(text string) ([]string, error) {
	r := strings.NewReplacer("→", " ", "->", " ", ",", " ")

	var chain []string

	for tok := range strings.FieldsSeq(r.Replace(text)) {
		tok = strings.ToLower(strings.Trim(tok, "`*"))
		if tok == "" || tok == "-" {
			continue
		}

		chain = append(chain, tok)
	}

	if len(chain) == 0 {
		return nil, errors.New("no skills in it")
	}

	return chain, checkChain(chain)
}

// checkChain refuses a skill outside the vocabulary, and one named twice:
// a task's position is the last skill it finished, which a repeat makes
// ambiguous.
func checkChain(chain []string) error {
	for i, s := range chain {
		if !slices.Contains(chainSkills, s) {
			return fmt.Errorf("%s is not a chain skill: the chain takes %s (start is implicit)",
				s, strings.Join(chainSkills, ", "))
		}

		if slices.Contains(chain[:i], s) {
			return fmt.Errorf("%s appears twice in the chain", s)
		}
	}

	if i := slices.Index(chain, "finish"); i >= 0 && i != len(chain)-1 {
		return errors.New("finish removes the worktree, so it ends the chain")
	}

	return nil
}

// driveInput is everything drive needs once the target is settled, so the
// flow can be tested without a checkout, a clock or a wait.
type driveInput struct {
	main     string
	driver   *driver.Driver
	tracker  tracker
	args     driveArgs
	argv     []string // the command line as given, for the resume command
	chain    []string
	remote   bool   // project.md declares a remote review: review reads it rather than reviewing locally
	plansDir string // the shared plans dir, or "" for in-repo storage
	title    func(ref.Ref) func(context.Context) (string, error)
	finish   func(ctx context.Context, w io.Writer, task ref.Ref) error
	stateDir string
	ttl      time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	answer  string            // a bare --answer, until the first task stopped on a question takes it
	answers map[string]string // --answer ID=text, by task id, until that task takes it
}

// A driveTask is one task of the list, as the validation pass found it.
type driveTask struct {
	ref      ref.Ref
	suffix   string
	info     taskInfo
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
}

// A driveRun is one task on its way through the drive: pending until its
// base is through, then active with one chain element in flight, then
// ended with its row.
type driveRun struct {
	task      driveTask
	o         openedTask
	opened    bool
	pos       int    // the chain element in flight, or the next one
	skill     string // what is in flight: a chain skill, or catchup
	sentAt    time.Time
	begun     bool
	idleSince time.Time
	end       *driveResult
}

func (r *driveRun) active() bool { return r.opened && r.end == nil }

// through reports a task done with for its dependents: finished, or its
// whole chain run.
func (r *driveRun) through() bool { return r.end != nil && r.end.code == 0 }

// drive runs the flow: resolve every task, read the graph, start every
// task whose base is through, then poll them all until each is through, or
// one asks, or nothing can move. Nothing is created or prompted before the
// graph passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) error {
	tasks, notes, err := in.resolveTasks(ctx)
	if err != nil {
		return err
	}

	order, graphNotes, err := in.graph(ctx, tasks)
	if err != nil {
		return err
	}

	notes = append(notes, graphNotes...)

	if err := in.takeAnswers(tasks); err != nil {
		return err
	}

	runs := make([]*driveRun, 0, len(order))
	byID := map[string]*driveRun{}

	for _, i := range order {
		r := &driveRun{task: tasks[i]}
		if r.task.finished {
			r.end = &driveResult{row: "already finished"}
		}

		runs = append(runs, r)

		if _, seen := byID[r.task.ref.ID]; !seen {
			byID[r.task.ref.ID] = r
		}
	}

	if err := in.startRunnable(ctx, w, runs, byID); err != nil {
		return err
	}

	for !stopping(runs) {
		if err := in.sleep(ctx, in.args.every); err != nil {
			return err
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

		if err := in.startRunnable(ctx, w, runs, byID); err != nil {
			return err
		}
	}

	return in.report(w, runs, notes)
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

// startRunnable opens every pending task whose base is through and sends
// its first element. Runs are in graph order, so a base finished earlier in
// the same pass releases its dependents in it.
func (in *driveInput) startRunnable(
	ctx context.Context,
	w io.Writer,
	runs []*driveRun,
	byID map[string]*driveRun,
) error {
	for _, r := range runs {
		if r.opened || r.end != nil {
			continue
		}

		// a base outside the list was refused unless finished
		if b := byID[r.task.base]; b != nil && !b.through() {
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
		lines = append(lines, "answer with: "+resumeCommand(in.argv)+answerFlags(asking))
	}

	if in.answer != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	for _, r := range runs {
		if _, left := in.answers[r.task.ref.ID]; left {
			notes = append(notes, "--answer for "+r.task.ref.ID+" went unused: it was not stopped on a question")
		}
	}

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

	add := func(r ref.Ref, suffix string, ranged bool) error {
		if slices.ContainsFunc(tasks, func(t driveTask) bool { return t.ref.ID == r.ID && t.suffix == suffix }) {
			return fmt.Errorf("%s is listed twice", r.ID)
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

		task := driveTask{ref: r, suffix: suffix, info: info}

		if line := afterIn(info.body); line != "" {
			base, err := in.tracker.resolve(ctx, line)
			if err != nil {
				return fmt.Errorf("%s's body says After: %s: %w", r.ID, line, err)
			}

			task.after = base.ID
		}

		branched, err := hasTaskBranch(ctx, in.main, r.ID)
		if err != nil {
			return err
		}

		if branched {
			local, err := branchesFor(ctx, in.main, "refs/heads/", r.ID, suffix)
			if err != nil {
				return err
			}

			if len(local) == 1 {
				task.branch = local[0]
			}
		}

		task.finished = finishedTask(info, in.tracker.archive, r.ID, branched)
		tasks = append(tasks, task)

		return nil
	}

	if in.args.span {
		from, to, err := in.span(ctx)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range spanRefs(from, to) {
			if err := add(r, "", true); err != nil {
				return nil, nil, err
			}
		}

		return tasks, notes, nil
	}

	for _, tok := range in.args.tasks {
		id, suffix := ref.SplitSuffix(tok)

		r, err := in.tracker.resolve(ctx, id)
		if err != nil {
			return nil, nil, err
		}

		if err := add(r, suffix, false); err != nil {
			return nil, nil, err
		}
	}

	return tasks, notes, nil
}

// span resolves a range's two ends.
func (in *driveInput) span(ctx context.Context) (ref.Ref, ref.Ref, error) {
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

// open opens the task's checkout and agent, and reads where in the chain it
// stands.
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
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s: branch %s at %s, workspace %s\n", task.ref.ID, o.branch, o.path, o.ws.ID)

	for _, n := range appendNote(o.notes, note) {
		fmt.Fprintf(w, "  %s\n", n)
	}

	pos, err := in.position(ctx, o)
	if err != nil {
		return err
	}

	r.o, r.opened, r.pos = o, true, pos

	return nil
}

// position is how many chain skills the task is past: the last one drive
// recorded on the branch, else what its run state proves - plan and ship
// are the phases the badge tracks.
func (in *driveInput) position(ctx context.Context, o openedTask) (int, error) {
	if last := gitx.Out(ctx, in.main, "config", "--get", "branch."+o.branch+"."+cfgDrive); last != "" {
		if i := slices.Index(in.chain, last); i >= 0 {
			return i + 1, nil
		}
	}

	run, err := in.run(o.path)
	if err != nil || run == nil {
		return 0, err
	}

	pos := 0

	for _, phase := range []string{"plan", "ship"} {
		if s := run.Status(phase); s != "done" && s != "prior" {
			continue
		}

		if i := slices.Index(in.chain, phase); i >= 0 {
			pos = max(pos, i+1)
		}
	}

	return pos, nil
}

// next sends the task its next element: none left ends it, finish runs in
// this process, and a branch its finished base marked landed is caught up
// with the default branch before any skill runs on it.
func (in *driveInput) next(ctx context.Context, w io.Writer, r *driveRun) error {
	if r.pos >= len(in.chain) {
		r.end = &driveResult{row: "done: " + strings.Join(in.chain, " ")}

		return nil
	}

	skill := in.chain[r.pos]

	switch {
	case skill == "finish":
		res := in.runFinish(ctx, w, r.task)
		r.end = &res

		return nil
	case in.landed(ctx, r):
		skill = "catchup"
	}

	return in.send(ctx, w, r, skill)
}

// runFinish runs finish in this process. Its refusals halt the task with
// the reason; its report is indented under the progress lines.
func (in *driveInput) runFinish(ctx context.Context, w io.Writer, task driveTask) driveResult {
	fmt.Fprintf(w, "%s finish: running\n", task.ref.ID)

	var buf bytes.Buffer

	err := in.finish(ctx, &buf, task.ref)

	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		fmt.Fprintf(w, "  %s\n", sc.Text())
	}

	if err != nil {
		return halted(task.ref.ID, "finish", err.Error())
	}

	return driveResult{row: "finished"}
}

// send runs one element in the task's agent: attach to a turn already
// running, answer a question it stopped on, or prompt the element. The
// poll reads how it ends.
func (in *driveInput) send(ctx context.Context, w io.Writer, r *driveRun, skill string) error {
	id := r.task.ref.ID
	agent := agentName(id, r.task.suffix)

	ws, run, err := in.look(ctx, r.o.path)
	if err != nil {
		return err
	}

	r.skill, r.sentAt, r.begun, r.idleSince = skill, in.now(), false, time.Time{}

	switch {
	case ws.Agent == "working":
		r.begun = true

		fmt.Fprintf(w, "%s %s: attached to the turn in progress\n", id, skill)
	case run != nil && stoppedRun(run):
		answer := in.takeAnswer(id)
		if answer == "" {
			res := in.verdict(r.task, skill, run)
			r.end = &res

			return nil
		}

		if err := herdr.Prompt(ctx, r.o.path, agent, answer); err != nil {
			return err
		}

		fmt.Fprintf(w, "%s %s: answer sent\n", id, skill)
	default:
		fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: r.sentAt}
		if err := refuseStalled(fwd, ws, agent, r.o.path); err != nil {
			res := halted(id, skill, err.Error())
			r.end = &res

			return nil
		}

		if err := herdr.Prompt(ctx, r.o.path, agent, skillPrompt(skill, id, r.o.dep, in.remote)); err != nil {
			return err
		}

		fmt.Fprintf(w, "%s %s: sent\n", id, skill)
	}

	return nil
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
func (in *driveInput) poll(ctx context.Context, w io.Writer, r *driveRun, wss []herdr.Listed, live []*state.Run) error {
	id, skill := r.task.ref.ID, r.skill

	ws := workspaceAt(wss, r.o.path)
	if ws == nil {
		return fmt.Errorf("no Herdr workspace shows %s any more", r.o.path)
	}

	var run *state.Run
	if runs := inRoot(live, r.o.path); len(runs) > 0 {
		run = runs[0]
	}

	now := in.now()
	since := r.sentAt.Truncate(time.Second) // the state file keeps whole seconds
	recent := run != nil && !run.Updated.Before(since)

	switch ws.Agent {
	case "working":
		r.begun, r.idleSince = true, time.Time{}

		return nil
	case "idle", "done":
	default:
		return nil // Herdr has not placed the agent yet
	}

	if !r.begun && !recent {
		if now.Sub(r.sentAt) > startWindow {
			res := halted(id, skill, fmt.Sprintf("the agent in workspace %s did not take the prompt within %s",
				cmp.Or(ws.Label, ws.ID), startWindow))
			r.end = &res
		}

		return nil
	}

	r.begun = true

	if recent && stoppedRun(run) {
		res := in.verdict(r.task, skill, run)
		r.end = &res

		return nil
	}

	// plan and ship own a phase; one still running with the agent idle is a
	// turn that ended without reporting, once the Stop hook had time
	if (skill == "plan" || skill == "ship") && run != nil && run.Status(skill) == "running" {
		if r.idleSince.IsZero() {
			r.idleSince = now
		}

		if now.Sub(r.idleSince) >= stallGrace {
			res := halted(id, skill, fmt.Sprintf("agent %s in workspace %s is idle while its run says %s %s: "+
				"it stopped without reporting, answer it in that pane", agentName(id, r.task.suffix),
				cmp.Or(ws.Label, ws.ID), skill, run.PhaseText(skill)))
			r.end = &res
		}

		return nil
	}

	fmt.Fprintf(w, "%s %s: done\n", id, skill)

	if skill == "catchup" {
		if reason := in.unsettled(ctx, r); reason != "" {
			res := halted(id, skill, "the catchup did not end clean: "+reason)
			r.end = &res

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

// landed reports whether finish marked the task's branch: its base's code
// is in the default branch and not yet in this one.
func (in *driveInput) landed(ctx context.Context, r *driveRun) bool {
	return gitx.Out(ctx, in.main, "config", "--get", "branch."+r.o.branch+"."+cfgLanded) != ""
}

// unsettled says why a catchup that ended without a stop did not bring the
// branch up to date, or "" when it did. Catchup owns no phase, so its
// success is read from git: the landed mark gone, which catchup clears only
// once verify is green, no merge or rebase left open, and a clean tree.
func (in *driveInput) unsettled(ctx context.Context, r *driveRun) string {
	switch {
	case in.landed(ctx, r):
		return "the branch is still marked " + cfgLanded
	case gitx.Out(ctx, r.o.path, "rev-parse", "-q", "--verify", "MERGE_HEAD") != "":
		return "a merge is still in progress in " + r.o.path
	case gitx.Out(ctx, r.o.path, "rev-parse", "-q", "--verify", "REBASE_HEAD") != "":
		return "a rebase is still in progress in " + r.o.path
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
	if run.Next != "" {
		res.lines = append(res.lines, "next: "+run.Next)
	}

	return res
}

func halted(id, skill, reason string) driveResult {
	return driveResult{
		row:   "halted at " + skill,
		code:  exitHalted,
		lines: []string{fmt.Sprintf("%s halted at %s: %s", id, skill, reason)},
	}
}

// stoppedRun reports whether any phase halted: blocked, failed or partial.
func stoppedRun(r *state.Run) bool {
	return slices.ContainsFunc(state.Phases, func(p string) bool { return state.Stopped(r.Status(p)) })
}

// look is the workspace showing the checkout and the newest run reported
// from it. A workspace gone mid-run is an error: the agent went with it.
func (in *driveInput) look(ctx context.Context, path string) (*herdr.Listed, *state.Run, error) {
	wss, err := herdr.Workspaces(ctx, in.main)
	if err != nil {
		return nil, nil, err
	}

	ws := workspaceAt(wss, path)
	if ws == nil {
		return nil, nil, fmt.Errorf("no Herdr workspace shows %s any more", path)
	}

	run, err := in.run(path)

	return ws, run, err
}

// lookAll is one tick's view: every workspace Herdr shows and every live
// run, read once for all the tasks in flight.
func (in *driveInput) lookAll(ctx context.Context) ([]herdr.Listed, []*state.Run, error) {
	wss, err := herdr.Workspaces(ctx, in.main)
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

// skillPrompt is what the agent is told for one skill, marked driven. Plan
// and ship take every gap on its default, as start's prompt does, and ship
// reuses the plan the chain wrote rather than asking; both carry the
// dependency's contract when the task has a base. catchup is not a chain
// skill: drive sends it to a branch its finished base marked landed.
func skillPrompt(skill, id string, dep *dependency, remote bool) string {
	var p string

	switch skill {
	case "plan":
		p = fmt.Sprintf("/f10:plan %s && %s", id, defaults)
	case "ship":
		p = fmt.Sprintf("/f10:ship %s && reuse the saved plan && %s", id, defaults)
	case "review":
		p = "/f10:review " + id
		if remote {
			p += " ci"
		}
	case "catchup":
		p = "/f10:catchup" // it catches up the branch its session stands on, and takes no id
	default:
		p = "/f10:" + skill + " " + id
	}

	if dep != nil && (skill == "plan" || skill == "ship") {
		p += " && " + dependencyClause(id, dep)
	}

	return driven(p)
}

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
