package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
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
				Name:  "on",
				Usage: "base the branch on `TASK-ID`'s existing branch instead of the default branch",
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

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, err := startMode(cmd)
	if err != nil {
		return err
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

	base, suffix := ref.SplitSuffix(token)

	r, err := t.reference(ctx, base)
	if err != nil {
		return err
	}

	in := startInput{
		main:   cmp.Or(t.res.MainRoot, t.res.CheckoutRoot),
		driver: driver.Find(t.res.StorageRoot, t.dir),
		task:   r,
		suffix: suffix,
		mode:   mode,
	}

	if on := strings.TrimSpace(cmd.String("on")); on != "" {
		onRef, err := t.reference(ctx, on)
		if err != nil {
			return fmt.Errorf("--on: %w", err)
		}

		in.on = onRef.ID
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

// startInput is everything start needs once the target is resolved, so
// the flow can be tested without a checkout.
type startInput struct {
	main   string         // the main checkout: worktrees are its siblings
	driver *driver.Driver // nil without one
	task   ref.Ref
	suffix string
	mode   string
	on     string // task id whose branch is the base, or ""
}

// start runs the flow in the order the issue fixes it: branch name, base,
// existing branch, worktree, workspace, agent, prompt, report. Every
// lookup that can refuse runs before the first thing is created.
func start(ctx context.Context, w io.Writer, in startInput) error {
	name, err := branchName(ctx, in.driver, in.task, in.suffix)
	if err != nil {
		return err
	}

	var base string
	if in.on != "" {
		if base, err = baseBranch(ctx, in.main, in.on); err != nil {
			return err
		}
	}

	existing, err := existingBranch(ctx, in.main, name, in.task.ID, in.suffix)
	if err != nil {
		return err
	}

	var notes []string

	if existing != "" {
		name = existing

		if base != "" {
			notes = append(notes, fmt.Sprintf("--on ignored: branch %s already exists and keeps its base", name))
			base = ""
		}
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return err
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
		if path, err = checkout(ctx, in.main, name, base, true); err != nil {
			return err
		}
	}

	ws, err := herdr.OpenWorktree(ctx, in.main, path, name)
	if err != nil {
		return err
	}

	agent := agentName(in.task.ID, in.suffix)

	if err := herdr.StartAgent(ctx, path, agent, agentKind, ws.RootPane); err != nil {
		return err
	}

	if err := herdr.Prompt(ctx, path, agent, prompt(in.mode, in.task.ID)); err != nil {
		return err
	}

	return writeStartReport(w, [][2]string{
		{"task", in.task.ID},
		{"branch", name},
		{"path", path},
		{"workspace", ws.ID},
	}, notes)
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

// baseBranch is the branch --on names: the task's local branch, else its
// branch on origin. Nothing matching is a failure before anything is
// created; several matching is one too, since guessing would base the work
// on the wrong attempt.
func baseBranch(ctx context.Context, dir, id string) (string, error) {
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
			return "", fmt.Errorf("--on %s: several branches match, name one: %s", id, strings.Join(refs, ", "))
		}
	}

	return "", fmt.Errorf("--on %s: no branch for it locally or on origin", id)
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

func prompt(mode, id string) string {
	switch mode {
	case modePlan:
		return "/f10:plan " + id
	case modeLocal:
		return fmt.Sprintf("/f10:plan %s && /f10:ship local: %s && %s", id, id, defaults)
	default:
		return fmt.Sprintf("/f10:plan %s && /f10:ship %s && %s", id, id, defaults)
	}
}

// writeStartReport prints the facts block conventions/report.md fixes:
// labels padded to the widest including its colon, two spaces, the value.
// None of these values is a url, so the marker cell stays blank. Notes
// follow as prose beneath the block.
func writeStartReport(w io.Writer, rows [][2]string, notes []string) error {
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0])+1)
	}

	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%-*s   %s\n", width, r[0]+":", r[1]); err != nil {
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
