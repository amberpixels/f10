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
		ArgsUsage: "<id>[:model]... | <id> -- <id>  [plan|judge|ship|review|resolve|finish ...]  " +
			"[--model MODEL] [--answer [ID=]TEXT ...] [--every DURATION]",
		Description: "The chain defaults to project.md's Drive chain, else " +
			"plan → judge → ship → review → resolve → finish.\n" +
			"A task's base is its branch's recorded --after, else its body's After: line; " +
			"every task whose base is finished runs at once, and a halt stops only its dependents.\n" +
			"--model starts every agent on MODEL; <id>:<model> starts one task's agent on its own, which wins.\n" +
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
		if chain, err = parseChain(t.eff.DriveChain); err != nil {
			return fmt.Errorf("project.md Drive chain: %w", err)
		}
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	drv := t.driver()
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
		settings: claudeSettingsPath(),
		title:    func(r ref.Ref) func(context.Context) (string, error) { return titleLookup(t, r.Number) },
		record:   recordAt,
		finish: func(ctx context.Context, w io.Writer, task ref.Ref, pr string) error {
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
				plansDir:  t.sharedPlansDir(),
				// a driven finish asks no second question, since a resume
				// carries only the latest answer: a bundle's riders stay
				// open, the reversible choice, and the report names them
				close: []string{},
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
	tasks   []string // as typed, any :model cut off; with span, the two ends of a range
	models  []string // each task's :model as typed, indexed like tasks, "" for none
	model   string   // --model as typed: every task's model unless its own names one
	span    bool
	chain   []string
	answers []string // each --answer as typed: `GH-1=text` for one task, or bare text
	every   time.Duration
}

// parseDriveArgs reads tasks, then skills, with --model, --answer and
// --every anywhere. A token in the chain vocabulary is a skill; every other one is
// a task, and a task after a skill is refused, since a list and a chain
// interleaved is a typo more often than a plan.
func parseDriveArgs(args []string) (driveArgs, error) {
	a := driveArgs{every: defaultEvery}

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])

		name, value, inline := strings.Cut(tok, "=")
		if name == "--model" || name == "--answer" || name == "--every" {
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
			return driveArgs{}, fmt.Errorf("unknown flag %s: drive takes --model, --answer and --every", tok)
		case len(a.chain) > 0:
			return driveArgs{}, fmt.Errorf("%s follows the chain: the tasks come first, then the skills", tok)
		default:
			task, m, err := splitModel(tok)
			if err != nil {
				return driveArgs{}, err
			}

			a.tasks, a.models = append(a.tasks, task), append(a.models, m)
		}
	}

	switch {
	case len(a.tasks) == 0:
		return driveArgs{}, errors.New("drive needs the tasks: GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.span && len(a.tasks) != 2:
		return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	case a.span && slices.ContainsFunc(a.models, func(m string) bool { return m != "" }):
		return driveArgs{}, errors.New("a range takes the run-wide --model, not one on its ends")
	}

	if err := checkChain(a.chain); err != nil {
		return driveArgs{}, err
	}

	return a, nil
}

func (a *driveArgs) set(name, value string) error {
	switch name {
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
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("--every %s: a positive duration, like 15s", value)
	}

	a.every = d

	return nil
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

// parseChain checks a declared chain: the skills in order, in any case.
func parseChain(declared []string) ([]string, error) {
	chain := make([]string, 0, len(declared))
	for _, s := range declared {
		chain = append(chain, strings.ToLower(s))
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
	settings string // claude's settings file, which names the model a bad --model falls back to
	title    func(ref.Ref) func(context.Context) (string, error)
	finish   func(ctx context.Context, w io.Writer, task ref.Ref, pr string) error // pr: "", or the answer merge or local
	stateDir string
	ttl      time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	// record puts an event in the ledger, placed in dir; nil records nothing
	record func(ctx context.Context, dir string, e ledger.Event)

	answer  string            // a bare --answer, until the first task stopped on a question takes it
	answers map[string]string // --answer ID=text, by task id, until that task takes it
	models  map[string]string // each model as typed, resolved; "" for claude's default
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
	// freshAt is when this drive started the task's agent: run state last
	// updated before it was written by an agent that is gone, and is not read
	freshAt time.Time
	// mergeCatchup is set once this drive sent a catchup because the pull
	// request conflicted at finish: a second conflict halts rather than loops
	mergeCatchup bool
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

// drive runs the flow: resolve every task, read the graph, start every
// task whose base is through, then poll them all until each is through, or
// one asks, or nothing can move. Nothing is created or prompted before the
// graph passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) error {
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

	if err := in.takeAnswers(tasks); err != nil {
		return err
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
				local = linkedBranches(ctx, in.main, r.ID)
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
	if o.fresh {
		r.freshAt = in.now().Truncate(time.Second) // the state file keeps whole seconds
	}

	return nil
}

// position is how many chain skills the task is past: the last one drive
// recorded on the branch, else what its run state proves - plan and ship
// are the phases the badge tracks - and a plan the worktree reads counts as
// plan done, so driven ship takes it rather than planning again.
func (in *driveInput) position(ctx context.Context, o openedTask) (int, error) {
	if last := gitx.Out(ctx, in.main, "config", "--get", "branch."+o.branch+"."+cfgDrive); last != "" {
		if i := slices.Index(in.chain, last); i >= 0 {
			return i + 1, nil
		}
	}

	pos := 0

	if i := slices.Index(in.chain, "plan"); i >= 0 && o.plan != "" {
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

		if i := slices.Index(in.chain, phase); i >= 0 {
			pos = max(pos, i+1)
		}
	}

	return pos, nil
}

// next sends the task its next element: none left ends it, and a branch
// marked landed is caught up with the default branch before any skill,
// finish included, runs on it. finish runs in this process once the pull
// request merges cleanly; one that conflicts is marked landed and caught up
// first.
func (in *driveInput) next(ctx context.Context, w io.Writer, r *driveRun) error {
	if r.pos >= len(in.chain) {
		in.end(ctx, w, r, driveResult{row: "done: " + strings.Join(in.chain, " ")})

		return nil
	}

	skill := in.chain[r.pos]

	if skill == "finish" && !in.landed(ctx, r) {
		if res := in.readyToMerge(ctx, w, r); res != nil {
			in.end(ctx, w, r, *res)

			return nil
		}
	}

	switch {
	case in.landed(ctx, r):
		skill = "catchup"
	case skill == "finish":
		in.end(ctx, w, r, in.runFinish(ctx, w, r.task))

		return nil
	}

	return in.send(ctx, w, r, skill)
}

// readyToMerge brings the pushed branch to where the host can merge it, or
// says why it cannot. The host merges origin's copy of the branch, so that
// is what is tried against origin's default branch: clean is ready; a local
// branch already caught up is pushed; anything else is marked landed, which
// sends it a catchup, once per drive. A branch origin does not have is left
// to finish, whose refusal names it.
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

	base, pushed := "origin/"+def, gitx.Out(ctx, r.o.path, "rev-parse", "-q", "--verify", "origin/"+branch)
	if pushed == "" || gitx.MergesCleanly(ctx, r.o.path, base, pushed) {
		return nil
	}

	if gitx.IsAncestor(ctx, r.o.path, base, "HEAD") {
		lease, how := "", "pushed"
		if !gitx.IsAncestor(ctx, r.o.path, pushed, "HEAD") {
			lease, how = pushed, "pushed with --force-with-lease (the catchup rewrote it)"
		}

		if err := gitx.Push(ctx, r.o.path, branch, lease); err != nil {
			res := halted(id, "finish", fmt.Sprintf("pushing %s, caught up with %s: %v", branch, base, err))

			return &res
		}

		fmt.Fprintf(w, "%s finish: %s caught up with %s, %s\n", id, branch, base, how)

		return nil
	}

	if r.mergeCatchup {
		res := halted(id, "finish", fmt.Sprintf("%s still conflicts with %s after a catchup: %s moved again, "+
			"rerun the drive to catch up once more", branch, base, def))

		return &res
	}

	if err := gitx.SetConfig(ctx, in.main, "branch."+branch+"."+cfgLanded, def); err != nil {
		res := halted(id, "finish", fmt.Sprintf("marking %s landed on %s: %v", branch, def, err))

		return &res
	}

	r.mergeCatchup = true

	fmt.Fprintf(w, "%s finish: %s conflicts with %s, catching up first\n", id, branch, base)

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
func (in *driveInput) runFinish(ctx context.Context, w io.Writer, task driveTask) driveResult {
	id := task.ref.ID

	pr, err := in.finishAnswer(id)
	if err != nil {
		return halted(id, "finish", err.Error())
	}

	fmt.Fprintf(w, "%s finish: running\n", id)

	var buf bytes.Buffer

	err = in.finish(ctx, &buf, task.ref, pr)

	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		fmt.Fprintf(w, "  %s\n", sc.Text())
	}

	if fp := (foreignPullError{}); errors.As(err, &fp) {
		return driveResult{
			row:  "asked at finish",
			code: exitAsk,
			lines: []string{fmt.Sprintf("%s asks at finish: 1. %s was opened by %s: [%s | %s]",
				id, fp.url, fp.author, finishLocal, finishMerge)},
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

// finishAnswer is the answer to finish's question this drive was resumed
// with: one keyed to the task, which must be merge or local, else a bare
// one that is, so a bare answer meant for an agent's question stays there.
func (in *driveInput) finishAnswer(id string) (string, error) {
	if a, ok := in.answers[id]; ok {
		delete(in.answers, id)

		if c := finishChoice(a); c != "" {
			return c, nil
		}

		return "", fmt.Errorf("the answer to finish is %q: answer %s or %s", a, finishLocal, finishMerge)
	}

	if c := finishChoice(in.answer); c != "" {
		in.answer = ""

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

	switch {
	case ws.Agent == "working":
		r.begun = true

		fmt.Fprintf(w, "%s %s: attached to the turn in progress\n", id, skill)
	case run != nil && stoppedRun(run):
		answer := in.takeAnswer(id)
		if answer == "" {
			in.end(ctx, w, r, in.verdict(r.task, skill, run))

			return nil
		}

		if err := promptAgent(ctx, r.o.path, id, r.task.suffix, answer); err != nil {
			return err
		}

		fmt.Fprintf(w, "%s %s: answer sent\n", id, skill)
	default:
		fwd := forwardInput{stateDir: in.stateDir, ttl: in.ttl, now: r.sentAt, since: r.freshAt}
		if err := refuseStalled(fwd, ws, agentName(id, r.task.suffix, r.o.path), r.o.path); err != nil {
			in.end(ctx, w, r, halted(id, skill, err.Error()))

			return nil
		}

		if err := promptAgent(
			ctx,
			r.o.path,
			id,
			r.task.suffix,
			skillPrompt(skill, r.task.ref.Label(), r.o.dep, in.remote),
		); err != nil {
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

	ws := workspaceByID(wss, r.o.ws.ID)
	if ws == nil {
		return workspaceGone(r)
	}

	var run *state.Run
	if runs := inRoot(live, r.o.path); len(runs) > 0 {
		run = r.current(runs[0])
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
		// Herdr shows no agent yet: the start window bounds this wait too
		if !r.begun && now.Sub(r.sentAt) > startWindow {
			in.end(ctx, w, r, halted(id, skill, fmt.Sprintf("workspace %s shows no agent %s after the prompt: "+
				"check its pane, then rerun this drive", workspaceName(ws), startWindow)))
		}

		return nil
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
		in.end(ctx, w, r, in.verdict(r.task, skill, run))

		return nil
	}

	// plan and ship own a phase; one still running with the agent idle is a
	// turn that ended without reporting, once the Stop hook had time
	if (skill == "plan" || skill == "ship") && run != nil && run.Status(skill) == "running" {
		if r.idleSince.IsZero() {
			r.idleSince = now
		}

		if now.Sub(r.idleSince) >= stallGrace {
			in.end(
				ctx,
				w,
				r,
				halted(id, skill, fmt.Sprintf("agent %s in workspace %s is idle while its run says %s %s: "+
					"it stopped without reporting, answer it in that pane", agentName(id, r.task.suffix, r.o.path),
					workspaceName(ws), skill, run.PhaseText(skill))),
			)
		}

		return nil
	}

	fmt.Fprintf(w, "%s %s: done\n", id, skill)

	if skill == "catchup" {
		if reason := in.unsettled(ctx, r); reason != "" {
			in.end(ctx, w, r, halted(id, skill, "the catchup did not end clean: "+reason))

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

	if res.code == exitHalted && !res.fromRun {
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

	ledger.Record(ctx, ledger.BusyAsync, e)
}

// stoppedRun reports whether any phase halted: blocked, failed or partial.
func stoppedRun(r *state.Run) bool {
	return slices.ContainsFunc(state.Phases, func(p string) bool { return state.Stopped(r.Status(p)) })
}

// look is the task's workspace, by the id open settled on, since a
// checkout can show in more than one, and the newest run reported from the
// checkout. A workspace gone mid-run is an error: the agent went with it.
func (in *driveInput) look(ctx context.Context, r *driveRun) (*herdr.Listed, *state.Run, error) {
	wss, err := herdr.Workspaces(ctx, in.main)
	if err != nil {
		return nil, nil, err
	}

	ws := workspaceByID(wss, r.o.ws.ID)
	if ws == nil {
		return nil, nil, workspaceGone(r)
	}

	run, err := in.run(r.o.path)

	return ws, run, err
}

func workspaceGone(r *driveRun) error {
	return fmt.Errorf("herdr no longer shows workspace %s, which showed %s", r.o.ws.ID, r.o.path)
}

// workspaceName is a workspace as a halt names it: its label and its id.
func workspaceName(ws *herdr.Listed) string {
	if ws.Label == "" || ws.Label == ws.ID {
		return ws.ID
	}

	return ws.Label + " (" + ws.ID + ")"
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

// skillPrompt is what the agent is told for one skill, marked driven. Driven
// mode takes every gap on its default and reuses a saved plan, so plan and
// ship are the bare skill; both name the dependency when the task has a
// base. catchup is not a chain skill: drive sends it to a branch its finished
// base marked landed. id is a bundle's label, `ABC-1+ABC-2`: plan and ship
// take it whole, every other skill its lead, from which it finds the branch.
func skillPrompt(skill, id string, dep *dependency, remote bool) string {
	var p string

	if skill != "plan" && skill != "ship" {
		id, _, _ = strings.Cut(id, ref.Separator)
	}

	switch skill {
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

// sharedMember is the first id two listed entries both name.
func sharedMember(a, b ref.Ref) string {
	for _, id := range b.Members() {
		if slices.Contains(a.Members(), id) {
			return id
		}
	}

	return b.ID
}
