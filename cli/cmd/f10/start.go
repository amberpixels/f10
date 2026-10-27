package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The start verb is the one that leaves the checkout: a branch, a
// worktree beside the main checkout, a Herdr workspace showing it, and a
// claude agent in that workspace already prompted with the task. It
// replaces three hand-offs across three tools, each a place to lose the
// branch suffix, the base branch or the "keep it local" words.
//
// Herdr is a hard dependency: outside it the command refuses before
// touching anything rather than degrade to claude in the current terminal.
func startCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "start a task: a worktree, a Herdr workspace and a prompted agent",
		ArgsUsage: "<task-id>[-suffix] | <PR url | #n>  [note...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "plan", Usage: "prompt only the plan"},
			&cli.BoolFlag{
				Name:  "discuss",
				Usage: "prompt only the task's fetch: the agent summarises it and waits to discuss it before any plan",
			},
			&cli.BoolFlag{Name: "local", Usage: "ship through the project's local flow: --flow local"},
			flowFlag(),
			&cli.StringFlag{
				Name:  "after",
				Usage: "base the branch on `TASK-ID`'s branch and record the dependency: ship rebases onto it, the PR stacks on it; default: the task body's After: line",
			},
			&cli.StringFlag{
				Name:  "base",
				Usage: "base the branch on `BRANCH`, local or on origin, instead of the default branch",
			},
			&cli.StringFlag{
				Name:  "task",
				Usage: "with a PR, the `TASK-ID` it belongs to, when its body, title and branch name none",
			},
			&cli.StringFlag{
				Name: "model",
				Usage: "start the agent on `MODEL`: a family (opus, fable), a version (opus-5.5) or a full id; " +
					"near misses are corrected, and a name that is no model falls back to claude's default",
			},
		},
		Action:        runStart,
		ShellComplete: completeFlow,
	}
}

// The prompt per mode. The default is the whole flow with every gap on
// its default, through the flow --flow or --local names; --plan stops
// after the plan; --discuss stops after the fetch, for a task the user
// talks through in the agent's own pane.
const (
	modeDefault = ""
	modePlan    = "plan"
	modeDiscuss = "discuss"

	agentKind = "claude"
)

// The keys --after writes under `branch.<name>.` in the local git config:
// the base task's id and its bare branch name, which ship and the pr step
// read with `git config --get`. Once the base merges, finish clears both and
// sets f10-landed to the base's id, so ship and catchup bring the default
// branch in before anything else runs, then clear the mark.
const (
	cfgAfter       = ref.AfterKey
	cfgAfterBranch = ref.AfterBranchKey
	cfgLanded      = ref.LandedKey
	cfgMoved       = ref.MovedKey
	cfgModel       = ref.ModelKey
	// cfgTaskID links a branch whose name need not carry the id to its task
	cfgTaskID = ref.TaskKey
	// cfgRiders lists a bundle's riders beside the lead cfgTaskID names
	cfgRiders = ref.RidersKey
)

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, err := startMode(cmd)
	if err != nil {
		return err
	}

	flowName, err := flowFlags(cmd)
	if err != nil {
		return err
	}

	if flowName != "" && mode != modeDefault {
		return fmt.Errorf(
			"--flow %s and --%s are exclusive: --%s stops before ship, which runs the flow",
			flowName,
			mode,
			mode,
		)
	}

	after, base := strings.TrimSpace(cmd.String("after")), strings.TrimSpace(cmd.String("base"))
	if after != "" && base != "" {
		return errors.New("--base and --after are exclusive: one names a git ref, the other a task to depend on")
	}

	if err := herdr.Available(); err != nil {
		return err
	}

	token := strings.TrimSpace(cmd.Args().First())
	if token == "" {
		return errors.New("start needs a task id")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	fl, err := resolveShipFlow(t.eff, flowName)
	if err != nil {
		return err
	}

	pick, pr, err := pickupFor(ctx, t, token)
	if err != nil {
		return err
	}

	note := strings.TrimSpace(strings.Join(cmd.Args().Tail(), " "))
	if err := refuseNote(mode, pick, note); err != nil {
		return err
	}

	task := strings.TrimSpace(cmd.String("task"))

	var (
		r      ref.Ref
		suffix string
	)

	if task, err = pickedTask(t.eff.IDPrefix, t.hostIssues(), pick, pr, task); err != nil {
		return err
	}

	switch {
	case pick != nil:
		if err := refusePickupFlags(flowName, after, base); err != nil {
			return err
		}

		if strings.Contains(task, ref.Separator) {
			return fmt.Errorf("--task %s: a PR is picked up for one task, not a bundle", task)
		}

		if r, err = t.reference(ctx, task); err != nil {
			return fmt.Errorf("%s: %w", pick.url, err)
		}

		r.Bundle = nil
	default:
		var id string

		id, suffix = ref.SplitSuffix(token)

		if r, err = t.reference(ctx, id); err != nil {
			return err
		}

		if suffix != "" && r.Bundle != nil {
			return fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
		}

		// any member starts, or reopens, the whole bundle under its lead
		r = r.Head()
	}

	in := startInput{
		main:     cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		driver:   t.driver(),
		task:     r,
		suffix:   suffix,
		mode:     mode,
		flow:     fl,
		base:     base,
		plansDir: t.sharedPlansDir(),
		title:    titleLookup(t, r.Number),
		pick:     pick,
		note:     note,
	}

	if checksParent(mode, pick) {
		in.children = t.tracker().children
	}

	if pick == nil {
		in.started = startedMove(t)
	}

	// before the body's After: line, whose own refusal would hide this one
	if err := refuseStartParent(ctx, in); err != nil {
		return err
	}

	switch {
	case pick != nil:
		// the branch and its base are the PR's
		if err := fetchPickup(ctx, in.main, pick); err != nil {
			return err
		}
	case after != "":
		afterRef, err := t.reference(ctx, after)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		in.after = afterRef.Head().ID
	case base == "":
		// no flag names a base: the body's After: line may
		if in.after, in.notes, err = afterFromBody(ctx, t.tracker(), in.main, r); err != nil {
			return err
		}
	}

	var modelLine string
	if in.model, modelLine = resolveModel(cmd.String("model"), claudeSettingsPath()); modelLine != "" {
		in.notes = append([]string{modelLine}, in.notes...)
	}

	in.progress = cmd.ErrWriter

	return start(ctx, cmd.Writer, in)
}

// pickupFor is the PR the start argument names, or nil when it names a
// task. A bare #n that the host knows as no PR is a task id, as before.
func pickupFor(ctx context.Context, t *target, token string) (*pickup, pull, error) {
	number, probe := pullToken(token)
	if number == "" {
		return nil, pull{}, nil
	}

	h, err := t.host()

	switch {
	case err != nil && probe:
		return nil, pull{}, nil
	case err != nil:
		return nil, pull{}, err
	case probe && h.GLab():
		// GitLab numbers issues and MRs apart: #n is an issue there, !n an MR
		return nil, pull{}, nil
	}

	p, err := h.pullNumber(ctx, number)

	switch {
	case err != nil && probe && noSuchPull(err):
		return nil, pull{}, nil
	case err != nil:
		return nil, pull{}, err
	}

	if err := refusePull(p); err != nil {
		return nil, pull{}, err
	}

	return h.pickupOf(number, p), p, nil
}

// pickedTask is the task id for a picked-up PR: --task, else the one the
// PR names. A PR naming none exits with exitNoTask, which the start skill
// turns into a question; --task with no PR is a refusal.
func pickedTask(prefix string, hostIssues bool, pick *pickup, pr pull, task string) (string, error) {
	switch {
	case pick == nil && task != "":
		return "", errors.New("--task names the task of a PR: pass the PR's link or #n")
	case pick == nil || task != "":
		return task, nil
	}

	if task = pickupTask(prefix, hostIssues, pr); task == "" {
		return "", cli.Exit(fmt.Sprintf("f10: %s names no task in its body, title or branch %s: "+
			"rerun with --task <ID>", pick.url, pick.branch), exitNoTask)
	}

	return task, nil
}

// refuseNote stops a note no prompt can carry: only the prompts that stop
// and wait are plain text, and a driven chain's arguments belong to its
// skills.
func refuseNote(mode string, pick *pickup, note string) error {
	if note == "" || mode == modeDiscuss || (pick != nil && mode != modePlan) {
		return nil
	}

	return fmt.Errorf("%q: a note goes to an agent that stops and waits, with --discuss or a PR picked up; "+
		"a driven run takes its instructions from the task", note)
}

// refusePickupFlags stops the flags a picked-up PR has no use for: its
// branch and base are the PR's, and nobody ships someone else's PR unread.
func refusePickupFlags(flow, after, base string) error {
	switch {
	case after != "" || base != "":
		return errors.New("--after and --base do not apply to a PR: its branch and its base are the PR's")
	case flow != "":
		return errors.New(
			"--local and --flow do not apply to a PR: the agent summarises it and waits, or plans with --plan",
		)
	}

	return nil
}

func startMode(cmd *cli.Command) (string, error) {
	var set []string

	for _, m := range []string{modePlan, modeDiscuss} {
		if cmd.Bool(m) {
			set = append(set, "--"+m)
		}
	}

	switch len(set) {
	case 0:
		return modeDefault, nil
	case 1:
		return strings.TrimPrefix(set[0], "--"), nil
	default:
		return "", fmt.Errorf("%s are exclusive: each names how far the agent runs", strings.Join(set, " and "))
	}
}

// afterFromBody reads the task's body for an After: line. A body that
// cannot be read is a note, never a refusal: --after stays the explicit way.
func afterFromBody(ctx context.Context, tr tracker, main string, task ref.Ref) (string, []string, error) {
	info, err := tr.info(ctx, task)
	if err != nil {
		return "", []string{fmt.Sprintf("no body read for %s (%v), so no After: line applies", task.ID, err)}, nil
	}

	after, note, err := bodyAfter(ctx, tr, main, task, info.body)
	if err != nil || note == "" {
		return after, nil, err
	}

	return after, []string{note}, nil
}

// startInput is everything start needs once the target is settled, so
// the flow can be tested without a checkout.
type startInput struct {
	main     string         // the main checkout: worktrees are its siblings
	driver   *driver.Driver // nil without one
	task     ref.Ref
	suffix   string
	mode     string
	flow     shipFlow                              // the flow ship runs; zero for default
	flowKept bool                                  // drive records the flow itself, keeping one the branch has
	after    string                                // task id whose branch is the base and whose plan is the contract, or ""
	base     string                                // branch whose ref is the base, or ""
	plansDir string                                // the plans dir every checkout shares, or "" from a worktree keeping its own .f10
	title    func(context.Context) (string, error) // the task's title, for the default branch's slug; nil when nothing answers
	notes    []string                              // notes settled before the flow, printed beneath the report
	model    string                                // the agent's --model, resolved, or "" for claude's default
	progress io.Writer                             // where a wait in progress is reported, or nil for nowhere
	pick     *pickup                               // the PR picked up, or nil for a task started by its id
	note     string                                // the user's words after the id, for the agent before it waits, or ""
	// children is the task's subtasks, read to refuse a parent; nil skips the
	// check (see checksParent)
	children func(ctx context.Context, task ref.Ref) ([]childRow, error)
	// started moves a task to tracker.status's started word; nil when none
	// is declared, or for a PR picked up, whose task someone else started
	started func(ctx context.Context, task ref.Ref) error
}

// titleLookup asks the host for the task's title, needed only when no driver
// names the branch. A checkout with no host CLI answers with the reason, so
// the note beneath the report can quote it.
func titleLookup(t *target, number string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		h, err := t.issues(driver.VerbBranch)
		if err != nil {
			return "", err
		}

		return h.issueTitle(ctx, number)
	}
}

// A baseRef is what the worktree is created from: the ref as git takes it,
// the flag that named it, and the commit it points at, which the note about
// a base that changes nothing compares to the default branch.
type baseRef struct {
	ref  string
	flag string
	sha  string
}

// A dependency is what --after records on the new branch: the base task,
// its branch as a bare name (`origin/` stripped, since the pr step targets
// a branch and ship looks it up local-first), and where its plan is.
type dependency struct {
	task    string
	branch  string
	plan    string // the plan's path, or "" when no checkout can reach it
	planned bool   // the plan exists at that path
}

// start runs the flow: the checkout and its agent, the prompt, the report.
// A workspace Herdr already showed keeps its agent, and nothing is
// prompted twice; one left with no agent gets its agent and its prompt.
func start(ctx context.Context, w io.Writer, in startInput) error {
	o, err := openTask(ctx, in)
	if err != nil {
		return err
	}

	if o.fresh {
		if err := herdr.Prompt(
			ctx,
			o.path,
			herdr.AgentName(in.task.ID, in.suffix, o.path),
			prompt(in.mode, in.flow, o.task.Label(), o.dep, o.plan, in.pick, in.note),
		); err != nil {
			return err
		}

		o.notes = append(o.notes, markStarted(ctx, in.started, o.task)...)
	}

	rows := []fact{{label: "task", value: o.task.Label()}}
	if in.pick != nil {
		rows = append(rows, fact{label: "pr", value: in.pick.url, url: true})
	}

	rows = append(rows, fact{label: "branch", value: o.branch})
	if o.dep != nil {
		rows = append(rows, fact{label: "after", value: o.dep.task})
	}

	if o.plan != "" {
		rows = append(rows, fact{label: "plan", value: o.plan})
	}

	return writeReport(
		w,
		append(rows, fact{label: "path", value: o.path}, fact{label: "workspace", value: o.ws.ID}),
		o.notes,
	)
}

// startedMove moves a task to tracker.status's started word, or is nil when
// none is declared.
func startedMove(t *target) func(context.Context, ref.Ref) error {
	word := t.eff.TrackerStatus["started"]
	if word == "" {
		return nil
	}

	return func(ctx context.Context, task ref.Ref) error {
		return updateTask(ctx, io.Discard, t, t.driver(), task, props{status: word})
	}
}

// markStarted moves every member of a freshly started task to its started
// status. The worktree is the result, so a failure is a note, never a
// refusal.
func markStarted(ctx context.Context, started func(context.Context, ref.Ref) error, task ref.Ref) []string {
	if started == nil {
		return nil
	}

	var notes []string

	for _, m := range task.MemberRefs() {
		if err := started(ctx, m); err != nil {
			notes = append(notes, fmt.Sprintf("%s was not moved to its started status: %v", m.ID, err))
		}
	}

	return notes
}

// checksParent reports whether start refuses a parent: a PR pickup is
// someone else's branch, and talking an umbrella task through before
// splitting it is what --discuss is for.
func checksParent(mode string, pick *pickup) bool {
	return pick == nil && mode != modeDiscuss
}

// refuseStartParent stops a parent with unfinished subtasks before anything
// is created, since its agent would ship the whole umbrella on one branch;
// in a bundle, every member is checked. A task with a branch was started
// already and reopens as before, as drive never treats a started task as a
// parent.
func refuseStartParent(ctx context.Context, in startInput) error {
	if in.children == nil {
		return nil
	}

	branched, err := hasTaskBranch(ctx, in.main, in.task.ID)
	if err != nil || branched {
		return err
	}

	for _, m := range in.task.MemberRefs() {
		open, err := openSubtasks(ctx, tracker{children: in.children}, m)
		if err != nil {
			return err
		}

		if len(open) > 0 {
			return cli.Exit(fmt.Sprintf("f10: %s is a parent of %s: run /f10:drive %s to drive them, "+
				"or start one subtask (f10 start %s)", m.ID, strings.Join(open, ", "), m.ID, open[0]), exitParent)
		}
	}

	return nil
}

// settleBundle checks the bundle against the branch start found, before
// anything is created, and returns the task with the riders that branch
// ships. A task alone takes the riders of the bundle branch it leads, which
// a branch fetched from origin carries in its name only. It refuses what
// would split a task across two branches: a rider another branch already
// ships, or a branch that exists with other riders than the ones typed,
// since riders are fixed at start.
func settleBundle(ctx context.Context, main string, task ref.Ref, existing string) (ref.Ref, error) {
	if existing != "" {
		lead, riders := branchBundle(ctx, main, existing, strings.TrimSuffix(prefixOf(task), "-"))

		switch {
		case len(task.Riders()) == 0 && len(riders) > 0 && strings.EqualFold(lead, task.ID):
			task.Bundle = append([]string{task.ID}, riders...)
		case !strings.EqualFold(lead, task.ID) && lead != "":
			return ref.Ref{}, fmt.Errorf("%s is already on branch %s, led by %s: finish it first, or start %s",
				task.ID, existing, lead, lead)
		case !slices.Equal(riders, task.Riders()):
			return ref.Ref{}, fmt.Errorf("branch %s ships %s; riders are fixed at start, so %s cannot reopen it: "+
				"finish it first, or start %s", existing,
				ref.Ref{ID: task.ID, Bundle: append([]string{task.ID}, riders...)}.Label(),
				task.Label(), task.ID)
		}
	}

	for _, rider := range task.Riders() {
		branches := linkedBranches(ctx, main, rider)

		for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
			refs, err := gitx.Refs(ctx, main, append(taskRefs(root, rider), riderRefs(root, rider)...)...)
			if err != nil {
				return ref.Ref{}, err
			}

			for _, r := range refs {
				branches = append(branches, strings.TrimPrefix(r, "origin/"))
			}
		}

		for _, b := range branches {
			if b != existing {
				return ref.Ref{}, fmt.Errorf("%s is already on branch %s: finish it first, or leave it out of %s",
					rider, b, task.Label())
			}
		}
	}

	return task, nil
}

// recordBundle links the branch to its lead and lists its riders, under
// the branch's section of the local git config, so any member finds it.
func recordBundle(ctx context.Context, dir, branch string, task ref.Ref) error {
	section := "branch." + branch + "."

	if err := gitx.SetConfig(ctx, dir, section+cfgTaskID, task.ID); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	if err := gitx.SetConfig(ctx, dir, section+cfgRiders, strings.Join(task.Riders(), " ")); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	return nil
}

// An openedTask is what openTask leaves behind: the branch, its worktree,
// the workspace showing it, the dependency recorded, the plan the worktree
// holds, and whether the agent in it was started by this call and so waits
// for its first prompt.
type openedTask struct {
	task   ref.Ref // the task, with the riders its branch ships
	branch string
	path   string
	ws     herdr.Workspace
	dep    *dependency
	plan   string // the task's plan the worktree reads, or "" when none was saved or no agent was started
	fresh  bool
	notes  []string
}

// openTask runs everything start does short of the prompt: branch name,
// base, existing branch, worktree, dependency, workspace, agent. Every
// lookup that can refuse runs before the first thing is created, and the
// first thing created is the worktree. Both start and drive adopt main's
// plan; drive prompts the agent itself, one skill at a time.
func openTask(ctx context.Context, in startInput) (openedTask, error) {
	settled, err := branchFor(ctx, in)
	if err != nil {
		return openedTask{}, err
	}

	name, base, dep, existing := settled.name, settled.base, settled.dep, settled.existing

	if in.pick == nil {
		if in.task, err = settleBundle(ctx, in.main, in.task, existing.name); err != nil {
			return openedTask{}, err
		}
	}

	notes := slices.Clone(in.notes)

	// the bare-id note explains a name this run gives; a branch that exists
	// keeps the name it has, whatever the title would have said
	switch {
	case existing.name != "":
		name = existing.name

		if base.ref != "" {
			notes = append(notes, ignoredBaseNote(base, dep, name))
			base = baseRef{}
		}
	case settled.slugNote != "":
		notes = append(notes, settled.slugNote)
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return openedTask{}, err
	}

	if dep != nil {
		notes = append(notes, locatePlan(dep, in.plansDir, wts)...)
	}

	path := worktreeOf(wts, name)

	if in.pick != nil && !existing.remote {
		note, err := syncPickup(ctx, in.main, name, path != "")
		if err != nil {
			return openedTask{}, err
		}

		notes = appendNote(notes, note)
	}

	switch {
	case path != "":
		notes = append(notes, "reused the existing worktree")
	case existing.name != "":
		if existing.remote {
			if err := gitx.TrackBranch(ctx, in.main, name, false); err != nil {
				return openedTask{}, err
			}

			notes = append(notes, "created "+name+" tracking origin/"+name)
		}

		if path, err = checkout(ctx, in.main, name, "", false); err != nil {
			return openedTask{}, err
		}
	default:
		// a base at the default branch's commit changes nothing, which is
		// otherwise indistinguishable from no base at all
		if n := sameAsDefaultNote(ctx, in.main, base, dep); n != "" {
			notes = append(notes, n)
		}

		if path, err = checkout(ctx, in.main, name, base.ref, true); err != nil {
			return openedTask{}, err
		}
	}

	// the dependency is a fact about the branch, recorded once the branch
	// exists - whether this run made it or found it, since a branch started
	// without --after can still be stacked by running start again with it
	if dep != nil {
		if err := recordDependency(ctx, in.main, name, dep); err != nil {
			return openedTask{}, err
		}
	}

	if in.pick != nil {
		if err := gitx.SetConfig(ctx, in.main, "branch."+name+"."+cfgTaskID, in.task.ID); err != nil {
			return openedTask{}, fmt.Errorf("linking %s to %s: %w", name, in.task.ID, err)
		}
	}

	if len(in.task.Riders()) > 0 {
		if err := recordBundle(ctx, in.main, name, in.task); err != nil {
			return openedTask{}, err
		}
	}

	ws, wsNotes, err := openWorkspace(ctx, in.main, path, name)
	if err != nil {
		return openedTask{}, err
	}

	notes = append(notes, wsNotes...)

	o := openedTask{task: in.task, branch: name, path: path, ws: ws, dep: dep}

	agent := herdr.AgentName(in.task.ID, in.suffix, path)

	launch, note, err := needsAgent(ctx, path, ws)
	if err != nil {
		return openedTask{}, err
	}

	// the plan moves only into a worktree whose agent this call prompts, and
	// before that agent starts, so a failed move leaves no agent waiting
	var a adoption

	switch {
	case launch:
		if a, err = adoptPlan(in.main, path, in.plansDir, in.task.ID); err != nil {
			return openedTask{}, err
		}

		o.plan = a.plan
		if a.note != "" {
			notes = append(notes, a.note)
		}
	default:
		if src := mainPlan(in.main, path, in.plansDir, in.task.ID); src != "" {
			notes = append(notes, "main's plan "+src+" left in place: the workspace's agent was not prompted")
		}
	}

	if launch {
		// --add-dir on a missing dir may be refused; main's .f10 normally exists
		if in.plansDir != "" {
			if err := os.MkdirAll(filepath.Dir(in.plansDir), 0o750); err != nil {
				return openedTask{}, fmt.Errorf("creating the storage root: %w", err)
			}
		}

		if err := herdr.StartAgent(
			ctx,
			path,
			agent,
			agentKind,
			ws.RootPane,
			agentArgs(in.model, in.plansDir, path),
			shellWait(in.progress, ws.RootPane),
		); err != nil {
			if a.moved != "" {
				return openedTask{}, fmt.Errorf(
					"%w; the plan was moved from %s to %s: rerun to ship it",
					err, a.moved, a.plan,
				)
			}

			return openedTask{}, err
		}

		o.fresh = true

		// the flow is the branch's fact from here on: status lists its gates,
		// and a rerun of drive keeps it
		if !in.flowKept && in.flow.recorded() != "" {
			if err := gitx.SetConfig(ctx, in.main, "branch."+name+"."+cfgFlow, in.flow.recorded()); err != nil {
				notes = append(notes, "the flow was not recorded on the branch: "+err.Error())
			}
		}

		// the model is the branch's fact from here on, for a reader of the
		// branch (f10 board); a failed write loses nothing the agent needs
		if in.model != "" {
			if err := gitx.SetConfig(ctx, in.main, "branch."+name+"."+cfgModel, in.model); err != nil {
				notes = append(notes, "the model was not recorded on the branch: "+err.Error())
			}
		}
	}

	if note != "" {
		notes = append(notes, note)
	}

	if !launch && in.model != "" {
		notes = append(notes, "--model "+in.model+" ignored: no agent was started")
	}

	o.notes = notes

	return o, nil
}

// A settledBranch is what start knows before creating anything: the name
// it would give, why that name is the bare id, the base, the dependency,
// and the branch the task already has.
type settledBranch struct {
	name     string
	slugNote string
	base     baseRef
	dep      *dependency
	existing foundBranch
}

// branchFor settles the branch before anything is created. A picked-up
// PR's branch and base are the PR's, so only whether it is local yet is open.
func branchFor(ctx context.Context, in startInput) (settledBranch, error) {
	if in.pick != nil {
		name := in.pick.branch
		local := refSHA(ctx, in.main, "refs/heads/"+name) != ""

		return settledBranch{name: name, existing: foundBranch{name: name, remote: !local}}, nil
	}

	name, slugNote, err := branchName(ctx, in.driver, in.task, in.suffix, in.title)
	if err != nil {
		return settledBranch{}, err
	}

	base, dep, err := baseFor(ctx, in)
	if err != nil {
		return settledBranch{}, err
	}

	existing, err := existingBranch(ctx, in.main, name, in.task.ID, in.suffix)
	if err != nil {
		return settledBranch{}, err
	}

	return settledBranch{name: name, slugNote: slugNote, base: base, dep: dep, existing: existing}, nil
}

// openWorkspace is the workspace showing the checkout at path. One Herdr
// already shows is adopted rather than opened again, since `worktree open`
// can answer with a second workspace beside it: the first with an agent in
// a pane wins, else the first. Any other is named in a note, never closed,
// since its panes may hold the user's own work.
func openWorkspace(ctx context.Context, dir, path, label string) (herdr.Workspace, []string, error) {
	wss, err := herdr.Workspaces(ctx, dir)
	if err != nil {
		return herdr.Workspace{}, nil, err
	}

	shown := workspacesAt(wss, path)
	if len(shown) == 0 {
		ws, err := herdr.OpenWorktree(ctx, dir, path, label)

		return ws, nil, err
	}

	var adopted herdr.Workspace

	for _, l := range shown {
		panes, err := herdr.Panes(ctx, dir, l.ID)
		if err != nil {
			return herdr.Workspace{}, nil, err
		}

		if i := slices.IndexFunc(panes, func(p herdr.Pane) bool { return p.Agent != "" }); i >= 0 {
			adopted = herdr.Workspace{ID: l.ID, RootPane: panes[i].ID, AlreadyOpen: true}

			break
		}

		if adopted.ID == "" && len(panes) > 0 {
			adopted = herdr.Workspace{ID: l.ID, RootPane: panes[0].ID, AlreadyOpen: true}
		}
	}

	if adopted.ID == "" {
		return herdr.Workspace{}, nil, fmt.Errorf("herdr shows %s in workspace %s, which has no pane",
			path, shown[0].ID)
	}

	var notes []string

	for _, l := range shown {
		if l.ID != adopted.ID {
			notes = append(notes, fmt.Sprintf("workspace %s also shows this checkout and was left open: "+
				"close it with herdr workspace close %s", cmp.Or(l.Label, l.ID), l.ID))
		}
	}

	return adopted, notes, nil
}

// shellWait reports a wait for the pane's shell to finish starting, so a
// slow rc file reads as progress rather than a hang. A nil w reports nothing.
func shellWait(w io.Writer, pane string) func(time.Duration) {
	if w == nil {
		return nil
	}

	return func(waited time.Duration) {
		fmt.Fprintf(w, "waiting for the shell in pane %s to finish starting: %.1fs of %s\n",
			pane, waited.Seconds(), herdr.StartDeadline)
	}
}

// needsAgent decides whether the workspace gets an agent started in its
// root pane. A fresh one always does. One Herdr already showed keeps the
// agent in that pane, whatever its name; with none, as after a start that
// failed on a busy pane, an idle shell gets one and anything else the user
// runs there is left alone.
func needsAgent(ctx context.Context, dir string, ws herdr.Workspace) (bool, string, error) {
	if !ws.AlreadyOpen {
		return true, "", nil
	}

	exists, err := herdr.AgentExists(ctx, dir, ws.RootPane)
	if err != nil {
		return false, "", err
	}

	if exists {
		return false, "workspace already open: its agent left as it was, nothing prompted", nil
	}

	idle, err := herdr.PaneIdleShell(ctx, dir, ws.RootPane)
	if err != nil {
		return false, "", err
	}

	if !idle {
		return false, fmt.Sprintf("workspace already open with no agent, and pane %s runs something "+
			"other than an idle shell: left alone, nothing prompted", ws.RootPane), nil
	}

	return true, "workspace already open with no agent: started one in its idle shell", nil
}

// baseFor turns --after or --base into the ref the worktree is created
// from, and for --after the dependency to record. Neither given is an empty
// base, which lets git choose.
func baseFor(ctx context.Context, in startInput) (baseRef, *dependency, error) {
	switch {
	case in.after != "":
		branch, err := taskBranch(ctx, in.main, in.after)
		if err != nil {
			return baseRef{}, nil, err
		}

		base := baseRef{ref: branch, flag: "--after", sha: refSHA(ctx, in.main, branch)}
		dep := &dependency{task: in.after, branch: strings.TrimPrefix(branch, "origin/")}

		return base, dep, nil
	case in.base != "":
		branch, sha, err := namedRef(ctx, in.main, in.base)
		if err != nil {
			return baseRef{}, nil, err
		}

		return baseRef{ref: branch, flag: "--base", sha: sha}, nil, nil
	default:
		return baseRef{}, nil, nil
	}
}

// ignoredBaseNote says the base flag did not apply because the branch
// exists and keeps the base it was created from. A dependency is still
// recorded, and the note says so, since it is what ship and pr act on.
func ignoredBaseNote(base baseRef, dep *dependency, name string) string {
	if dep == nil {
		return fmt.Sprintf("%s ignored: branch %s already exists and keeps its base", base.flag, name)
	}

	return fmt.Sprintf("%s ignored for the base: branch %s already exists and keeps it; dependency on %s recorded",
		base.flag, name, dep.task)
}

// sameAsDefaultNote names a base that sits at the default branch's commit:
// for --after, the base task has no code yet, which the dependent agent is
// told to design against rather than discover.
func sameAsDefaultNote(ctx context.Context, dir string, base baseRef, dep *dependency) string {
	if base.sha == "" {
		return ""
	}

	def := defaultBranch(ctx, dir)
	if def == "" || refSHA(ctx, dir, "refs/remotes/"+def) != base.sha {
		return ""
	}

	if dep != nil {
		return fmt.Sprintf("%s %s: branch %s is at the same commit as the default branch %s, so %s has no code yet",
			base.flag, dep.task, base.ref, def, dep.task)
	}

	return fmt.Sprintf("%s %s is at the same commit as the default branch %s", base.flag, base.ref, def)
}

// locatePlan finds the base task's plan: in the plans dir every checkout
// shares, else in the base's worktree when that keeps its own .f10 from
// before storage moved to main's. The notes name what was not found, so a
// prompt that cannot cite the plan is explained beneath the report.
func locatePlan(dep *dependency, plansDir string, wts []gitx.Worktree) []string {
	var candidates []string
	if plansDir != "" {
		candidates = append(candidates, filepath.Join(plansDir, dep.task+".md"))
	}

	if wt := worktreeOf(wts, dep.branch); wt != "" {
		candidates = append(candidates, filepath.Join(wt, ".f10", "plans", dep.task+".md"))
	}

	if len(candidates) == 0 {
		return []string{
			dep.task + " has no checkout here, so its plan is out of reach: the prompt names the task alone",
		}
	}

	// the first that exists, else the first, which the note below names
	dep.plan = candidates[0]
	if i := slices.IndexFunc(candidates, exists); i >= 0 {
		dep.plan = candidates[i]
	}

	if _, err := os.Stat(dep.plan); err != nil {
		return []string{fmt.Sprintf("%s has no plan yet at %s", dep.task, dep.plan)}
	}

	dep.planned = true

	return nil
}

// An adoption is the plan of the task the worktree reads, or "" when it has
// none, the path in main it was moved from when this call moved it, and the
// note saying what happened.
type adoption struct {
	plan  string
	moved string
	note  string
}

// adoptPlan settles which plan of the task the worktree reads: in place
// from shared storage, unless the worktree keeps its own .f10 with a plan.
// With plansDir "" main's plan moves into the worktree, not copied, so a
// later ship in main finds none.
func adoptPlan(main, worktree, plansDir, id string) (adoption, error) {
	if plansDir != "" {
		if own := planIf(filepath.Join(worktree, ".f10", "plans", id+".md")); own != "" {
			return adoption{plan: own}, nil
		}

		return adoption{plan: planIf(filepath.Join(plansDir, id+".md"))}, nil
	}

	dst := filepath.Join(worktree, ".f10", "plans", id+".md")
	src := mainPlan(main, worktree, plansDir, id)

	switch {
	case exists(dst) && src != "":
		note := fmt.Sprintf("kept the worktree's plan at %s; main's %s was left in place", dst, src)

		return adoption{plan: dst, note: note}, nil
	case src == "":
		return adoption{plan: planIf(dst)}, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return adoption{}, fmt.Errorf("moving the plan %s to %s: %w", src, dst, err)
	}

	if err := os.Rename(src, dst); err != nil {
		return adoption{}, fmt.Errorf("moving the plan %s to %s: %w", src, dst, err)
	}

	return adoption{plan: dst, moved: src, note: "moved the plan from " + src}, nil
}

// mainPlan is the plan main saved for the task where it could move into the
// worktree, else "": shared storage and the task checked out in main
// itself have nothing to move.
func mainPlan(main, worktree, plansDir, id string) string {
	src := filepath.Join(main, ".f10", "plans", id+".md")
	if plansDir != "" || src == filepath.Join(worktree, ".f10", "plans", id+".md") {
		return ""
	}

	return planIf(src)
}

// planIf is path when a plan is there, else "".
func planIf(path string) string {
	if !exists(path) {
		return ""
	}

	return path
}

// recordDependency writes the dependency under the branch's section of the
// local git config: per-branch, shared by every worktree, never pushed.
func recordDependency(ctx context.Context, dir, branch string, dep *dependency) error {
	section := "branch." + branch + "."

	if err := gitx.SetConfig(ctx, dir, section+cfgAfter, dep.task); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.task, err)
	}

	if err := gitx.SetConfig(ctx, dir, section+cfgAfterBranch, dep.branch); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.task, err)
	}

	return nil
}

// promptAgent prompts the task's agent in the worktree at path, falling back
// to the bare name an agent started before the path hash still answers to.
func promptAgent(ctx context.Context, path, id, suffix, text string) error {
	err := herdr.Prompt(ctx, path, herdr.AgentName(id, suffix, path), text)
	if herdr.IsCode(err, herdr.CodeAgentNotFound) {
		return herdr.Prompt(ctx, path, herdr.BareAgentName(id, suffix), text)
	}

	return err
}

// prompt is what the agent is told: the skills, the dependency where the
// task has one, and the driven marker. A saved plan, or a flow with no
// plan entry, turns plan-then-ship into ship alone, told the flow by
// name unless it is default; --plan still plans, and the plan step
// archives the saved one. Driven mode takes every gap on its default and the fetch step carries
// the dependency's contract, so the prompt states neither. A picked-up PR is
// summarised and discussed, or planned from its diff with --plan: never
// shipped unread. A note goes only to the prompts that stop and wait.
func prompt(mode string, fl shipFlow, id string, dep *dependency, plan string, pick *pickup, note string) string {
	switch {
	case pick != nil && mode == modePlan:
		return driven(fmt.Sprintf("/f10:plan %s continue PR %s: its diff is the starting point", id, pick.url))
	case pick != nil:
		return pickupPrompt(id, plan, pick, note)
	case mode == modeDiscuss:
		return discussPrompt(id, dep, plan, note)
	}

	var p string

	switch {
	case mode == modePlan:
		p = "/f10:plan " + id
	case plan != "" || !fl.plans():
		p = "/f10:ship " + fl.word() + id
	default:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship %s%s", id, fl.word(), id)
	}

	if dep != nil {
		p += " && " + dependencyClause(id, dep)
	}

	// the agent is prompted from here, not by someone in its pane: driven
	// from its first turn, every question goes through run state
	return driven(p)
}

// discussPrompt fetches the task and stops. It is plain text with no driven
// marker: the user talks to this agent in its own pane, and its questions
// belong there. Comments and images come with `f10 task read`'s output and the
// fetch-adapter fallback with its error, so the prompt names neither. The
// user's note runs after the summary and before the wait.
func discussPrompt(id string, dep *dependency, plan, note string) string {
	p := fmt.Sprintf("Run `f10 task read %s` and summarise what it asks in a few lines.", id)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	if dep != nil {
		p += " For context: " + dependencyClause(id, dep) + "."
	}

	return p + noteClause(note) + " Then stop and wait."
}

// pickupPrompt is discussPrompt for a picked-up PR: the task, the PR's
// state and its diff summarised, then the agent waits in its own pane.
func pickupPrompt(id, plan string, pick *pickup, note string) string {
	p := fmt.Sprintf("Run `f10 task read %s`, `%s` and `%s`, and summarise in a few lines what the task asks, "+
		"the PR's state (reviews, checks, open threads) and what its diff does, opening any image the PR links.",
		id, pick.view, pick.diff)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	return p + noteClause(note) + " Then stop and wait."
}

// noteClause is the user's note as a sentence of the prompt, or "".
func noteClause(note string) string {
	if note == "" {
		return ""
	}

	return " Then, as the user asks: " + strings.TrimRight(note, ".") + "."
}

// dependencyClause names the base task and where its plan is. What to do
// with that (design against the plan, not this checkout, with no fallback
// for its absence) is the fetch step's rule, so the prompt does not repeat it.
func dependencyClause(id string, dep *dependency) string {
	switch {
	case dep.planned:
		return fmt.Sprintf("%s depends on %s, whose plan is at %s", id, dep.task, dep.plan)
	case dep.plan != "":
		return fmt.Sprintf("%s depends on %s, whose plan is not written yet (expected at %s)", id, dep.task, dep.plan)
	default:
		return fmt.Sprintf("%s depends on %s, whose plan is out of reach here", id, dep.task)
	}
}
