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

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
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
				Usage: "base the branch on `TASK-ID`'s branch and record the dependency: ship rebases onto it, the PR stacks on it",
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
// the pr step read both with `git config --get` and resolve nothing.
const (
	cfgAfter       = "f10-after"
	cfgAfterBranch = "f10-after-branch"
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

	if mode == modeLocal && !declaresPipeline(t.eff, modeLocal) {
		return errors.New("--local: project.md declares no `local` pipeline; " +
			"add `local: implement -> commit` under Ship pipeline(s), since ship never picks one unasked")
	}

	id, suffix := ref.SplitSuffix(token)

	r, err := t.reference(ctx, id)
	if err != nil {
		return err
	}

	in := startInput{
		main:   cmp.Or(t.res.MainRoot, t.res.CheckoutRoot),
		driver: driver.Find(t.res.StorageRoot, t.dir),
		task:   r,
		suffix: suffix,
		mode:   mode,
		base:   base,
	}

	// out-of-tree storage shares one plans dir between every worktree;
	// in-repo storage keeps each worktree's plans under its own .f10/
	if t.res.StorageRoot != filepath.Join(t.res.CheckoutRoot, ".f10") {
		in.plansDir = t.pb.PlansDir
	}

	if after != "" {
		afterRef, err := t.reference(ctx, after)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		in.after = afterRef.ID
	}

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
func declaresPipeline(eff *facts.Effective, name string) bool {
	for _, f := range eff.Fields {
		if f.Name != "Ship pipeline(s)" {
			continue
		}

		for line := range strings.SplitSeq(f.Value, "\n") {
			line = strings.TrimLeft(strings.TrimSpace(line), "-* ")
			if head, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(head), name) {
				return true
			}
		}
	}

	return false
}

// startInput is everything start needs once the target is resolved, so
// the flow can be tested without a checkout.
type startInput struct {
	main     string         // the main checkout: worktrees are its siblings
	driver   *driver.Driver // nil without one
	task     ref.Ref
	suffix   string
	mode     string
	after    string // task id whose branch is the base and whose plan is the contract, or ""
	base     string // branch whose ref is the base, or ""
	plansDir string // the plans dir every worktree shares, or "" when each keeps its own under .f10/plans
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
// a branch and ship resolves local-first), and where its plan is.
type dependency struct {
	task    string
	branch  string
	plan    string // the plan's path, or "" when no checkout can reach it
	planned bool   // the plan exists at that path
}

// start runs the flow: branch name, base, existing branch, worktree,
// workspace, agent, prompt, report. Every lookup that can refuse runs
// before the first thing is created.
func start(ctx context.Context, w io.Writer, in startInput) error {
	name, err := branchName(ctx, in.driver, in.task, in.suffix)
	if err != nil {
		return err
	}

	base, dep, err := resolveBase(ctx, in)
	if err != nil {
		return err
	}

	existing, err := existingBranch(ctx, in.main, name, in.task.ID, in.suffix)
	if err != nil {
		return err
	}

	var notes []string

	if existing != "" {
		name = existing

		if base.ref != "" {
			notes = append(notes, ignoredBaseNote(base, dep, name))
			base = baseRef{}
		}
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return err
	}

	// the dependency is a fact about the branch, recorded whether or not
	// the branch is new - a branch started without --after can still be
	// stacked by running start again with it
	if dep != nil {
		notes = append(notes, locatePlan(dep, in.plansDir, wts)...)

		if err := recordDependency(ctx, in.main, name, dep); err != nil {
			return err
		}
	}

	path := worktreeOf(wts, name)

	switch {
	case path != "":
		notes = append(notes, "reused the existing worktree")
	case existing != "":
		if path, err = checkout(ctx, in.main, name, "", false); err != nil {
			return err
		}
	default:
		// a base at the default branch's commit changes nothing, which is
		// otherwise indistinguishable from no base at all
		if n := sameAsDefaultNote(ctx, in.main, base, dep); n != "" {
			notes = append(notes, n)
		}

		if path, err = checkout(ctx, in.main, name, base.ref, true); err != nil {
			return err
		}
	}

	ws, err := herdr.OpenWorktree(ctx, in.main, path, name)
	if err != nil {
		return err
	}

	// a workspace Herdr already showed has its agent in the root pane, or
	// whatever the user left there - starting another would be refused
	if ws.AlreadyOpen {
		notes = append(notes, "workspace already open: its agent left as it was, nothing prompted")
	} else {
		agent := agentName(in.task.ID, in.suffix)

		if err := herdr.StartAgent(ctx, path, agent, agentKind, ws.RootPane); err != nil {
			return err
		}

		if err := herdr.Prompt(ctx, path, agent, prompt(in.mode, in.task.ID, dep)); err != nil {
			return err
		}
	}

	rows := []fact{{label: "task", value: in.task.ID}, {label: "branch", value: name}}
	if dep != nil {
		rows = append(rows, fact{label: "after", value: dep.task})
	}

	return writeReport(w, append(rows, fact{label: "path", value: path}, fact{label: "workspace", value: ws.ID}), notes)
}

// resolveBase turns --after or --base into the ref the worktree is created
// from, and for --after the dependency to record. Neither given is an empty
// base, which lets git choose.
func resolveBase(ctx context.Context, in startInput) (baseRef, *dependency, error) {
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

// branchName is the driver's answer to `branch`, or the bare task id for a
// project with no driver or a driver that declines, with the suffix
// appended to whichever came back.
func branchName(ctx context.Context, d *driver.Driver, task ref.Ref, suffix string) (string, error) {
	name := task.ID

	if d != nil {
		out, err := d.Run(ctx, driver.VerbBranch, task.Number)

		switch {
		case err == nil:
			name, _, _ = strings.Cut(strings.TrimSpace(out), "\n")
			if name == "" {
				return "", errors.New("driver branch printed nothing")
			}
		case !errors.Is(err, driver.ErrUnsupported):
			return "", err
		}
	}

	if suffix != "" {
		name += "-" + suffix
	}

	return name, nil
}

// taskRefs are the patterns a task's branch can sit under a root: the id
// itself (which for-each-ref extends to everything below it at a slash),
// and the id with a dash suffix.
func taskRefs(root, id string) []string {
	return []string{root + id, root + id + "-*"}
}

// branchesFor lists the branches under root that belong to task id,
// narrowed to one suffix when the caller salted the name.
func branchesFor(ctx context.Context, dir, root, id, suffix string) ([]string, error) {
	refs, err := gitx.Refs(ctx, dir, taskRefs(root, id)...)
	if err != nil {
		return nil, err
	}

	if suffix == "" {
		return refs, nil
	}

	return slices.DeleteFunc(refs, func(r string) bool { return !strings.HasSuffix(r, "-"+suffix) }), nil
}

// taskBranch is the branch --after names: the task's local branch, else its
// branch on origin. Nothing matching is a failure before anything is
// created; several matching is one too, since guessing would base the work
// on the wrong attempt.
func taskBranch(ctx context.Context, dir, id string) (string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
		refs, err := branchesFor(ctx, dir, root, id, "")
		if err != nil {
			return "", err
		}

		switch len(refs) {
		case 0:
			continue
		case 1:
			return refs[0], nil
		default:
			return "", fmt.Errorf("--after %s: several branches match, name one: %s", id, strings.Join(refs, ", "))
		}
	}

	return "", fmt.Errorf("--after %s: no branch for it locally or on origin", id)
}

// namedRef is the ref --base names, with the commit it points at: the local
// branch, else a remote branch typed as such (`origin/main`), else the
// branch on origin. Exact refs rather than for-each-ref, since a base is
// one commit and a literal pattern would also match everything below the
// name at a slash. Nothing matching is a failure before anything is created.
func namedRef(ctx context.Context, dir, branch string) (string, string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/", "refs/remotes/origin/"} {
		if sha := refSHA(ctx, dir, root+branch); sha != "" {
			return strings.TrimPrefix(strings.TrimPrefix(root+branch, "refs/heads/"), "refs/remotes/"), sha, nil
		}
	}

	return "", "", fmt.Errorf("--base %s: no branch %s locally or on origin", branch, branch)
}

// refSHA is the commit a ref points at - a full ref, or a branch name git
// resolves itself - or "" when there is no such ref.
func refSHA(ctx context.Context, dir, r string) string {
	return gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", r)
}

// existingBranch is the branch to reuse, or "" when the task has none yet.
// The name start would create wins outright; otherwise one task branch is
// reused, and several is an error, since a suffix exists to say which.
func existingBranch(ctx context.Context, dir, want, id, suffix string) (string, error) {
	refs, err := branchesFor(ctx, dir, "refs/heads/", id, suffix)
	if err != nil {
		return "", err
	}

	if slices.Contains(refs, want) {
		return want, nil
	}

	switch len(refs) {
	case 0:
		return "", nil
	case 1:
		return refs[0], nil
	default:
		return "", fmt.Errorf("several branches for %s, add a suffix to pick one: %s", id, strings.Join(refs, ", "))
	}
}

func worktreeOf(wts []gitx.Worktree, branch string) string {
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path
		}
	}

	return ""
}

// checkout creates the worktree for branch and returns its path, read
// back from git rather than predicted. With worktrunk on PATH it goes
// through `wt switch`, so a project's worktrunk hooks keep firing; without
// it, plain git at the sibling path worktrunk would have chosen.
func checkout(ctx context.Context, main, branch, base string, create bool) (string, error) {
	if shell.Has("wt") {
		if err := wtSwitch(ctx, main, branch, base, create); err != nil {
			return "", err
		}
	} else {
		if create && base == "" {
			base = defaultBranch(ctx, main)
		}

		if err := gitx.AddWorktree(ctx, main, siblingPath(main, branch), branch, base, create); err != nil {
			return "", err
		}
	}

	wts, err := gitx.Worktrees(ctx, main)
	if err != nil {
		return "", err
	}

	path := worktreeOf(wts, branch)
	if path == "" {
		return "", fmt.Errorf("no worktree for %s after creating it", branch)
	}

	return path, nil
}

// wtSwitch is worktrunk's create-or-open. The flags keep it non-interactive:
// no shell to cd, no approval prompt, structured output nobody has to parse
// because git is asked for the path afterwards.
func wtSwitch(ctx context.Context, main, branch, base string, create bool) error {
	args := []string{"switch", "--no-cd", "--yes", "--format", "json"}

	if create {
		args = append(args, "--create")
		if base != "" {
			args = append(args, "--base", base)
		}
	}

	res, err := shell.Capture(ctx, main, "wt", append(args, branch)...)
	if err != nil {
		return fmt.Errorf("running wt switch: %w", err)
	}

	if res.Code != 0 {
		return fmt.Errorf("wt switch %s: %s", branch, cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)))
	}

	return nil
}

// defaultBranch is what origin points HEAD at, or "" to let git use the
// current HEAD - the same default worktrunk applies.
func defaultBranch(ctx context.Context, dir string) string {
	return gitx.Out(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
}

// siblingPath is the worktree layout worktrunk produces and per-project
// cleanup scripts already scan: beside the main checkout, named after it
// and the branch, slashes flattened to dashes.
func siblingPath(main, branch string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"."+strings.ReplaceAll(branch, "/", "-"))
}

// agentName is unique while the task's agent lives: the id lowercased,
// with the suffix so two attempts on one task can both run.
func agentName(id, suffix string) string {
	name := strings.ToLower(id)
	if suffix != "" {
		name += "-" + strings.ToLower(suffix)
	}

	return name
}

// prompt is what the agent is told. A dependency adds the contract it must
// design against: the base task's plan, not the checkout, since the base
// task's code may not exist yet and a fallback for its absence is the one
// thing the dependent must not write.
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

	return p
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

// A fact is one row of the facts block: a label, a value, and whether the
// value is a url, which earns the marker cell.
type fact struct {
	label string
	value string
	url   bool
}

// writeReport prints the facts block conventions/report.md fixes: labels
// padded to the widest including its colon, two spaces, the marker cell
// (the arrow for a url, blank otherwise), one space, the value. Notes
// follow as prose beneath the block.
func writeReport(w io.Writer, rows []fact, notes []string) error {
	width := 0
	for _, r := range rows {
		width = max(width, len(r.label)+1)
	}

	for _, r := range rows {
		marker := " "
		if r.url {
			marker = "↗"
		}

		if _, err := fmt.Fprintf(w, "%-*s  %s %s\n", width, r.label+":", marker, r.value); err != nil {
			return err
		}
	}

	for _, n := range notes {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return err
		}
	}

	return nil
}
