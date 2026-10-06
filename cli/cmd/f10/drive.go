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

// The drive verb runs a list of tasks through a chain of skills, one task
// at a time, and exits when every task is through or one needs a human.
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
// by the same command with --answer.
func driveCommand() *cli.Command {
	return &cli.Command{
		Name:  "drive",
		Usage: "run a list of tasks through a chain of skills, one task at a time, stopping when an agent asks",
		ArgsUsage: "<id>... | <id> -- <id>  [plan|judge|ship|review|resolve|finish ...]  " +
			"[--answer TEXT] [--every DURATION]",
		Description: "The chain defaults to project.md's Drive chain, else " +
			"plan → judge → ship → review → resolve → finish.\n" +
			"Exit 0: every task went through its chain. 4: an agent asks; rerun with --answer. " +
			"5: a task halted (a judge stop, a failure, an agent that stopped without reporting).",
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
	tasks  []string // as typed; with span, the two ends of a range
	span   bool
	chain  []string
	answer string
	every  time.Duration
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
		a.answer = strings.TrimSpace(value)

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

	answer string // --answer, until a stopped task takes it
}

// A driveTask is one task of the list, as the validation pass found it.
type driveTask struct {
	ref      ref.Ref
	suffix   string
	info     taskInfo
	after    string // the base the body's After: line names, as an id
	finished bool
}

// A driveResult is how one task ended: its row in the report, and when it
// stopped the list, the exit code and the lines beneath the report.
type driveResult struct {
	row   string
	code  int
	lines []string
}

// drive runs the flow: resolve and validate every task, then each in
// order, then the report. Nothing is created or prompted before every task
// passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) error {
	in.answer = in.args.answer

	tasks, notes, err := in.resolveTasks(ctx)
	if err != nil {
		return err
	}

	if err := in.checkOrder(ctx, tasks); err != nil {
		return err
	}

	var (
		rows []fact
		stop *driveResult
	)

	for _, task := range tasks {
		switch {
		case stop != nil:
			rows = append(rows, fact{label: task.ref.ID, value: "not reached"})

			continue
		case task.finished:
			rows = append(rows, fact{label: task.ref.ID, value: "already finished"})

			continue
		}

		res, err := in.runTask(ctx, w, task)
		if err != nil {
			return fmt.Errorf("%s: %w", task.ref.ID, err)
		}

		rows = append(rows, fact{label: task.ref.ID, value: res.row})

		if res.code != 0 {
			stop = &res
		}
	}

	if in.answer != "" {
		notes = append(notes, "--answer went unused: no task was stopped on a question")
	}

	if stop != nil {
		notes = append(notes, stop.lines...)
	}

	if len(rows) > 0 {
		fmt.Fprintln(w)
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if stop != nil {
		return cli.Exit("", stop.code)
	}

	return nil
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

// checkOrder refuses a list whose dependencies cannot hold: a task whose
// base comes later in the list, or whose base is outside it and not
// finished. The order given is the order run.
func (in *driveInput) checkOrder(ctx context.Context, tasks []driveTask) error {
	index := map[string]int{}
	for i, t := range tasks {
		index[t.ref.ID] = i
	}

	for i, t := range tasks {
		if t.finished || t.after == "" {
			continue
		}

		if j, listed := index[t.after]; listed {
			if j >= i {
				return fmt.Errorf(
					"%s is After: %s, which comes later in the list: put %s first",
					t.ref.ID,
					t.after,
					t.after,
				)
			}

			continue
		}

		done, err := in.baseFinished(ctx, t.after)
		if err != nil {
			return err
		}

		if !done {
			return fmt.Errorf("%s is After: %s, which is not in the list and not finished: "+
				"add %s before %s, or finish it first", t.ref.ID, t.after, t.after, t.ref.ID)
		}
	}

	return nil
}

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

// runTask opens the task's checkout and agent, then runs the chain from
// where the task stands.
func (in *driveInput) runTask(ctx context.Context, w io.Writer, task driveTask) (driveResult, error) {
	id := task.ref.ID

	after, note, err := bodyAfter(ctx, in.tracker, in.main, task.ref, task.info.body)
	if err != nil {
		return driveResult{}, err
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
		return driveResult{}, err
	}

	fmt.Fprintf(w, "%s: branch %s at %s, workspace %s\n", id, o.branch, o.path, o.ws.ID)

	for _, n := range appendNote(o.notes, note) {
		fmt.Fprintf(w, "  %s\n", n)
	}

	pos, err := in.position(ctx, o)
	if err != nil {
		return driveResult{}, err
	}

	for _, skill := range in.chain[pos:] {
		if skill == "finish" {
			return in.runFinish(ctx, w, task), nil
		}

		res, err := in.agentStep(ctx, w, task, o, skill)
		if err != nil || res.code != 0 {
			return res, err
		}

		if err := gitx.SetConfig(ctx, in.main, "branch."+o.branch+"."+cfgDrive, skill); err != nil {
			return driveResult{}, fmt.Errorf("recording %s done: %w", skill, err)
		}
	}

	return driveResult{row: "done: " + strings.Join(in.chain, " ")}, nil
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
		return driveResult{
			row:   "halted at finish",
			code:  exitHalted,
			lines: []string{fmt.Sprintf("%s halted at finish: %v", task.ref.ID, err)},
		}
	}

	return driveResult{row: "finished"}
}

// agentStep runs one skill in the task's agent: attach to a turn already
// running, answer a question it stopped on, or prompt the skill; then wait
// for the turn to end and read how it ended.
func (in *driveInput) agentStep(
	ctx context.Context,
	w io.Writer,
	task driveTask,
	o openedTask,
	skill string,
) (driveResult, error) {
	id := task.ref.ID
	agent := agentName(id, task.suffix)

	ws, run, err := in.look(ctx, o.path)
	if err != nil {
		return driveResult{}, err
	}

	sentAt := in.now()
	attach := false

	switch {
	case ws.Agent == "working":
		attach = true

		fmt.Fprintf(w, "%s %s: attached to the turn in progress\n", id, skill)
	case run != nil && stoppedRun(run):
		if in.answer == "" {
			return in.verdict(task, skill, run), nil
		}

		if err := herdr.Prompt(ctx, o.path, agent, in.answer); err != nil {
			return driveResult{}, err
		}

		in.answer = ""

		fmt.Fprintf(w, "%s %s: answer sent\n", id, skill)
	default:
		fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: sentAt}
		if err := refuseStalled(fwd, ws, agent, o.path); err != nil {
			return halted(id, skill, err.Error()), nil
		}

		if err := herdr.Prompt(ctx, o.path, agent, skillPrompt(skill, id, o.dep, in.remote)); err != nil {
			return driveResult{}, err
		}

		fmt.Fprintf(w, "%s %s: sent\n", id, skill)
	}

	return in.await(ctx, w, task, o, skill, sentAt, attach)
}

// await polls until the turn ends. A turn has begun once Herdr shows the
// agent working or its run reports after the prompt: Herdr's `done` lasts
// from the previous turn until someone looks, so it proves nothing. Once
// the agent is idle again, the run says how the turn ended.
func (in *driveInput) await(
	ctx context.Context,
	w io.Writer,
	task driveTask,
	o openedTask,
	skill string,
	sentAt time.Time,
	begun bool,
) (driveResult, error) {
	id := task.ref.ID
	since := sentAt.Truncate(time.Second) // the state file keeps whole seconds

	var idleSince time.Time

	for {
		if err := in.sleep(ctx, in.args.every); err != nil {
			return driveResult{}, err
		}

		ws, run, err := in.look(ctx, o.path)
		if err != nil {
			return driveResult{}, err
		}

		now := in.now()
		recent := run != nil && !run.Updated.Before(since)

		switch ws.Agent {
		case "working":
			begun, idleSince = true, time.Time{}

			continue
		case "idle", "done":
		default:
			continue // Herdr has not placed the agent yet
		}

		if !begun && !recent {
			if now.Sub(sentAt) > startWindow {
				return halted(id, skill, fmt.Sprintf("the agent in workspace %s did not take the prompt within %s",
					cmp.Or(ws.Label, ws.ID), startWindow)), nil
			}

			continue
		}

		begun = true

		if recent && stoppedRun(run) {
			return in.verdict(task, skill, run), nil
		}

		// plan and ship own a phase; one still running with the agent idle
		// is a turn that ended without reporting, once the Stop hook had time
		if (skill == "plan" || skill == "ship") && run != nil && run.Status(skill) == "running" {
			if idleSince.IsZero() {
				idleSince = now
			}

			if now.Sub(idleSince) < stallGrace {
				continue
			}

			return halted(id, skill, fmt.Sprintf("agent %s in workspace %s is idle while its run says %s %s: "+
				"it stopped without reporting, answer it in that pane", agentName(id, task.suffix),
				cmp.Or(ws.Label, ws.ID), skill, run.PhaseText(skill))), nil
		}

		fmt.Fprintf(w, "%s %s: done\n", id, skill)

		return driveResult{}, nil
	}
}

// verdict reads a stopped run: an ask goes to the user, anything else halts.
func (in *driveInput) verdict(task driveTask, skill string, run *state.Run) driveResult {
	id := task.ref.ID

	if run.Ask != "" {
		return driveResult{
			row:  "asked at " + skill,
			code: exitAsk,
			lines: []string{
				fmt.Sprintf("%s asks at %s: %s", id, skill, run.Ask),
				"answer with: " + resumeCommand(in.argv) + ` --answer "1. … 2. …"`,
			},
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
// dependency's contract when the task has a base.
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
