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
// Herdr is a hard dependency. Outside it the command refuses before
// touching anything: a fallback that execs claude in the current terminal
// is a later decision, not a silent degrade.
func startCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "start a task: a worktree, a Herdr workspace and a prompted agent",
		ArgsUsage: "<task-id>[-suffix]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "plan", Usage: "prompt only the plan"},
			&cli.BoolFlag{Name: "local", Usage: "ship through the project's local pipeline: commits, nothing pushed"},
			&cli.StringFlag{
				Name:  "after",
				Usage: "base the branch on `TASK-ID`'s branch and record the dependency: ship rebases onto it, the PR stacks on it; default: the task body's After: line",
			},
			&cli.StringFlag{
				Name:  "base",
				Usage: "base the branch on `BRANCH`, local or on origin, instead of the default branch",
			},
		},
		Action: runStart,
	}
}

// The prompt per mode. The default is the whole flow with every gap on
// its default; --plan stops after the plan; --local is the default flow
// with the ship routed through the `local` pipeline.
const (
	modeDefault = ""
	modePlan    = "plan"
	modeLocal   = "local"

	agentKind = "claude"
	defaults  = "take defaults for all gaps; we review them at the end"
)

// The keys --after writes under `branch.<name>.` in the local git config:
// the base task's id, and its branch as a bare name, so the ship skill and
// the pr step read both with `git config --get` and look up nothing.
// finish clears them from every dependent once the base branch merged, and
// marks the dependent landed with the base's id: the base's code is in the
// default branch and not yet in this one, so ship and catchup bring the
// default branch in before anything else runs, then clear the mark.
const (
	cfgAfter       = "f10-after"
	cfgAfterBranch = "f10-after-branch"
	cfgLanded      = "f10-landed"
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

	id, suffix := ref.SplitSuffix(token)

	r, err := t.reference(ctx, id)
	if err != nil {
		return err
	}

	in := startInput{
		main:     cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		driver:   driver.Find(t.lay.StorageRoot, t.dir),
		task:     r,
		suffix:   suffix,
		mode:     mode,
		base:     base,
		plansDir: t.sharedPlansDir(),
		title:    titleLookup(t, r.Number),
	}

	switch {
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

	in.progress = cmd.ErrWriter

	return start(ctx, cmd.Writer, in)
}

func startMode(cmd *cli.Command) (string, error) {
	switch {
	case cmd.Bool("plan") && cmd.Bool("local"):
		return "", errors.New("--plan and --local are exclusive: a plan-only run ships nothing")
	case cmd.Bool("plan"):
		return modePlan, nil
	case cmd.Bool("local"):
		return modeLocal, nil
	default:
		return modeDefault, nil
	}
}

// declaresPipeline reports whether project.md names a pipeline: a line of
// the Ship pipeline(s) fact that opens with the name and a colon, in the
// shape docs/project-setup.md shows (`- local: implement -> commit`).
func declaresPipeline(t *target, name string) bool {
	for line := range strings.SplitSeq(t.fact("Ship pipeline(s)"), "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "-* ")
		if head, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(head), name) {
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
	progress io.Writer                             // where a wait in progress is reported, or nil for nowhere
}

// titleLookup asks the host for the task's title. A checkout with no host
// CLI answers with the reason, so the note beneath the report can quote it.
func titleLookup(t *target, number string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		h, err := t.host()
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
			prompt(in.mode, in.task.ID, o.dep),
		); err != nil {
			return err
		}
	}

	rows := []fact{{label: "task", value: in.task.ID}, {label: "branch", value: o.branch}}
	if o.dep != nil {
		rows = append(rows, fact{label: "after", value: o.dep.task})
	}

	return writeReport(
		w,
		append(rows, fact{label: "path", value: o.path}, fact{label: "workspace", value: o.ws.ID}),
		o.notes,
	)
}

// An openedTask is what openTask leaves behind: the branch, its worktree,
// the workspace showing it, the dependency recorded, and whether the agent
// in it was started by this call and so waits for its first prompt.
type openedTask struct {
	branch string
	path   string
	ws     herdr.Workspace
	dep    *dependency
	fresh  bool
	notes  []string
}

// openTask runs everything start does short of the prompt: branch name,
// base, existing branch, worktree, dependency, workspace, agent. Every
// lookup that can refuse runs before the first thing is created, and the
// first thing created is the worktree. drive prompts the agent itself, one
// skill at a time.
func openTask(ctx context.Context, in startInput) (openedTask, error) {
	name, slugNote, err := branchName(ctx, in.driver, in.task, in.suffix, in.title)
	if err != nil {
		return openedTask{}, err
	}

	base, dep, err := baseFor(ctx, in)
	if err != nil {
		return openedTask{}, err
	}

	existing, err := existingBranch(ctx, in.main, name, in.task.ID, in.suffix)
	if err != nil {
		return openedTask{}, err
	}

	notes := slices.Clone(in.notes)

	// the bare-id note explains a name this run gives; a branch that exists
	// keeps the name it has, whatever the title would have said
	switch {
	case existing != "":
		name = existing

		if base.ref != "" {
			notes = append(notes, ignoredBaseNote(base, dep, name))
			base = baseRef{}
		}
	case slugNote != "":
		notes = append(notes, slugNote)
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return openedTask{}, err
	}

	if dep != nil {
		notes = append(notes, locatePlan(dep, in.plansDir, wts)...)
	}

	path := worktreeOf(wts, name)

	switch {
	case path != "":
		notes = append(notes, "reused the existing worktree")
	case existing != "":
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

	ws, err := herdr.OpenWorktree(ctx, in.main, path, name)
	if err != nil {
		return openedTask{}, err
	}

	o := openedTask{branch: name, path: path, ws: ws, dep: dep}

	agent := agentName(in.task.ID, in.suffix, path)

	launch, note, err := needsAgent(ctx, path, ws)
	if err != nil {
		return openedTask{}, err
	}

	if launch {
		if err := herdr.StartAgent(
			ctx,
			path,
			agent,
			agentKind,
			ws.RootPane,
			shellWait(in.progress, ws.RootPane),
		); err != nil {
			return openedTask{}, err
		}

		o.fresh = true
	}

	if note != "" {
		notes = append(notes, note)
	}

	o.notes = notes

	return o, nil
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

// prompt is what the agent is told. A dependency adds the contract it must
// design against: the base task's plan, not the checkout, since the base
// task's code may not exist yet and a fallback for its absence is the one
// thing the dependent must not write. The driven marker closes it.
func prompt(mode, id string, dep *dependency) string {
	var p string

	switch mode {
	case modePlan:
		p = "/f10:plan " + id
	case modeLocal:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship local: %s && %s", id, id, defaults)
	default:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship %s && %s", id, id, defaults)
	}

	if dep != nil {
		p += " && " + dependencyClause(id, dep)
	}

	// the agent is prompted from here, not by someone in its pane: driven
	// from its first turn, every question goes through run state
	return driven(p)
}

func dependencyClause(id string, dep *dependency) string {
	const contract = "not against this checkout, and write no fallback for its absence"

	switch {
	case dep.planned:
		return fmt.Sprintf("%s depends on %s: design against %s's planned API as its plan at %s records it, %s",
			id, dep.task, dep.task, dep.plan, contract)
	case dep.plan != "":
		return fmt.Sprintf("%s depends on %s: its plan is not written yet (expected at %s), "+
			"so design against %s's task as planned, %s", id, dep.task, dep.plan, dep.task, contract)
	default:
		return fmt.Sprintf(
			"%s depends on %s: its plan is out of reach here, so design against %s's task as planned, %s",
			id,
			dep.task,
			dep.task,
			contract,
		)
	}
}
