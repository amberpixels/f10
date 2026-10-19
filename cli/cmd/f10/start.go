package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		ArgsUsage: "<task-id>[-suffix] | <PR url | #n>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "plan", Usage: "prompt only the plan"},
			&cli.BoolFlag{
				Name:  "discuss",
				Usage: "prompt only the task's fetch: the agent summarises it and waits to discuss it before any plan",
			},
			&cli.BoolFlag{Name: "local", Usage: "ship through the project's local pipeline: commits, nothing pushed"},
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
		Action: runStart,
	}
}

// The prompt per mode. The default is the whole flow with every gap on
// its default; --plan stops after the plan; --local is the default flow
// with the ship routed through the `local` pipeline; --discuss stops after
// the fetch, for a task the user talks through in the agent's own pane.
const (
	modeDefault = ""
	modePlan    = "plan"
	modeLocal   = "local"
	modeDiscuss = "discuss"

	agentKind = "claude"
)

// The keys --after writes under `branch.<name>.` in the local git config:
// the base task's id and its bare branch name, which ship and the pr step
// read with `git config --get`. Once the base merges, finish clears both and
// sets f10-landed to the base's id, so ship and catchup bring the default
// branch in before anything else runs, then clear the mark.
const (
	cfgAfter       = "f10-after"
	cfgAfterBranch = "f10-after-branch"
	cfgLanded      = "f10-landed"
	// cfgTaskID links a branch whose name need not carry the id to its task
	cfgTaskID = ref.TaskKey
)

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, err := startMode(cmd)
	if err != nil {
		return err
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

	if mode == modeLocal && !declaresPipeline(t, modeLocal) {
		return errors.New("--local: project.md declares no `local` pipeline; " +
			"add `local: implement -> commit` under Ship pipeline(s), since ship never picks one unasked")
	}

	pick, pr, err := pickupFor(ctx, t, token)
	if err != nil {
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
		if err := refusePickupFlags(mode, after, base); err != nil {
			return err
		}

		if r, err = t.reference(ctx, task); err != nil {
			return fmt.Errorf("%s: %w", pick.url, err)
		}
	default:
		var id string

		id, suffix = ref.SplitSuffix(token)

		if r, err = t.reference(ctx, id); err != nil {
			return err
		}
	}

	in := startInput{
		main:     cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		driver:   t.driver(),
		task:     r,
		suffix:   suffix,
		mode:     mode,
		base:     base,
		plansDir: t.sharedPlansDir(),
		title:    titleLookup(t, r.Number),
		pick:     pick,
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

		in.after = afterRef.ID
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
	case probe && h.glab():
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

// refusePickupFlags stops the flags a picked-up PR has no use for: its
// branch and base are the PR's, and nobody ships someone else's PR unread.
func refusePickupFlags(mode, after, base string) error {
	switch {
	case after != "" || base != "":
		return errors.New("--after and --base do not apply to a PR: its branch and its base are the PR's")
	case mode == modeLocal:
		return errors.New("--local does not apply to a PR: the agent summarises it and waits, or plans with --plan")
	}

	return nil
}

func startMode(cmd *cli.Command) (string, error) {
	var set []string

	for _, m := range []string{modePlan, modeLocal, modeDiscuss} {
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

// declaresPipeline reports whether project.md names a pipeline, in any case.
func declaresPipeline(t *target, name string) bool {
	for declared := range t.eff.Pipelines {
		if strings.EqualFold(declared, name) {
			return true
		}
	}

	return false
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
	after    string                                // task id whose branch is the base and whose plan is the contract, or ""
	base     string                                // branch whose ref is the base, or ""
	plansDir string                                // the plans dir every worktree shares, or "" when each keeps its own under .f10/plans
	title    func(context.Context) (string, error) // the task's title, for the default branch's slug; nil when nothing answers
	notes    []string                              // notes settled before the flow, printed beneath the report
	model    string                                // the agent's --model, resolved, or "" for claude's default
	progress io.Writer                             // where a wait in progress is reported, or nil for nowhere
	pick     *pickup                               // the PR picked up, or nil for a task started by its id
}

// titleLookup asks the host for the task's title. A checkout with no host
// CLI answers with the reason, so the note beneath the report can quote it.
func titleLookup(t *target, number string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		h, err := t.issues()
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
			agentName(in.task.ID, in.suffix, o.path),
			prompt(in.mode, in.task.ID, o.dep, o.plan, in.pick),
		); err != nil {
			return err
		}
	}

	rows := []fact{{label: "task", value: in.task.ID}}
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

// An openedTask is what openTask leaves behind: the branch, its worktree,
// the workspace showing it, the dependency recorded, the plan the worktree
// holds, and whether the agent in it was started by this call and so waits
// for its first prompt.
type openedTask struct {
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

	ws, wsNotes, err := openWorkspace(ctx, in.main, path, name)
	if err != nil {
		return openedTask{}, err
	}

	notes = append(notes, wsNotes...)

	o := openedTask{branch: name, path: path, ws: ws, dep: dep}

	agent := agentName(in.task.ID, in.suffix, path)

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
		if err := herdr.StartAgent(
			ctx,
			path,
			agent,
			agentKind,
			ws.RootPane,
			modelArgs(in.model),
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

// locatePlan finds the base task's plan. In-repo storage keeps a plan beside
// its branch, so it is reachable only through that branch's worktree;
// out-of-tree storage shares one dir. The notes name what was not found, so
// a prompt that cannot cite the plan is explained beneath the report.
func locatePlan(dep *dependency, plansDir string, wts []gitx.Worktree) []string {
	if plansDir == "" {
		wt := worktreeOf(wts, dep.branch)
		if wt == "" {
			return []string{
				dep.task + " has no checkout here, so its plan is out of reach: the prompt names the task alone",
			}
		}

		plansDir = filepath.Join(wt, ".f10", "plans")
	}

	dep.plan = filepath.Join(plansDir, dep.task+".md")

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

// adoptPlan settles which plan of the task the worktree reads, moving the
// one main saved into the worktree: moved, not copied, so a later ship in
// main finds none. Out-of-tree storage shares one dir, so nothing moves.
// A worktree that holds its own plan keeps it, and main's stays in place.
func adoptPlan(main, worktree, plansDir, id string) (adoption, error) {
	if plansDir != "" {
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
// worktree, else "": out-of-tree storage and the task checked out in main
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

// agentName names the task's agent. Herdr's live agent names share one
// namespace across every project, capped at 32 characters, so the bare
// name is cut to leave room for a short hash of the worktree path: the same
// id in two checkouts never collides.
func agentName(id, suffix, path string) string {
	sum := sha256.Sum256([]byte(path))
	base := bareAgentName(id, suffix)

	return base[:min(len(base), 25)] + "-" + hex.EncodeToString(sum[:3])
}

// bareAgentName is the id lowercased, with the suffix so two attempts on one
// task can both run: the whole name of an agent started before names carried
// the path hash.
func bareAgentName(id, suffix string) string {
	name := strings.ToLower(id)
	if suffix != "" {
		name += "-" + strings.ToLower(suffix)
	}

	return name
}

// promptAgent prompts the task's agent in the worktree at path, falling back
// to the bare name an agent started before the path hash still answers to.
func promptAgent(ctx context.Context, path, id, suffix, text string) error {
	err := herdr.Prompt(ctx, path, agentName(id, suffix, path), text)
	if herdr.IsCode(err, herdr.CodeAgentNotFound) {
		return herdr.Prompt(ctx, path, bareAgentName(id, suffix), text)
	}

	return err
}

// prompt is what the agent is told: the skills, the dependency where the
// task has one, and the driven marker. A saved plan turns plan-then-ship
// into ship alone; --plan still plans, and the plan step archives the saved
// one. Driven mode takes every gap on its default and the fetch step carries
// the dependency's contract, so the prompt states neither. A picked-up PR is
// summarised and discussed, or planned from its diff with --plan: never
// shipped unread.
func prompt(mode, id string, dep *dependency, plan string, pick *pickup) string {
	switch {
	case pick != nil && mode == modePlan:
		return driven(fmt.Sprintf("/f10:plan %s continue PR %s: its diff is the starting point", id, pick.url))
	case pick != nil:
		return pickupPrompt(id, plan, pick)
	case mode == modeDiscuss:
		return discussPrompt(id, dep, plan)
	}

	var p string

	switch {
	case mode == modePlan:
		p = "/f10:plan " + id
	case mode == modeLocal && plan != "":
		p = "/f10:ship local: " + id
	case mode == modeLocal:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship local: %s", id, id)
	case plan != "":
		p = "/f10:ship " + id
	default:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship %s", id, id)
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
// belong there. Comments come with `f10 task read`'s output and the
// fetch-adapter fallback with its error, so the prompt names neither.
func discussPrompt(id string, dep *dependency, plan string) string {
	p := fmt.Sprintf("Run `f10 task read %s` and summarise what it asks in a few lines, "+
		"opening any image it links.", id)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	if dep != nil {
		p += " For context: " + dependencyClause(id, dep) + "."
	}

	return p + " Then stop and wait."
}

// pickupPrompt is discussPrompt for a picked-up PR: the task, the PR's
// state and its diff summarised, then the agent waits in its own pane.
func pickupPrompt(id, plan string, pick *pickup) string {
	p := fmt.Sprintf("Run `f10 task read %s`, `%s` and `%s`, and summarise in a few lines what the task asks, "+
		"the PR's state (reviews, checks, open threads) and what its diff does, opening any image they link.",
		id, pick.view, pick.diff)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	return p + " Then stop and wait."
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
