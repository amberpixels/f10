package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The finish verb is start's inverse. Closing a task by hand is the same
// three tools in reverse - the host CLI, git, Herdr - and the two steps
// people skip, the worktree and the pull, leave sibling directories behind
// and a main that is behind the PR it just merged. With in-repo storage the
// worktree also holds the task's plan, so removing it by hand deletes the
// only record of the plan.
//
// Everything after the merge is gated on the merge having landed: the
// merge commit must be in the local default branch before anything local
// is touched. Every refusal comes before the first change.
func finishCommand() *cli.Command {
	return &cli.Command{
		Name:      "finish",
		Usage:     "finish a task: merge its PR, archive the plan, close the workspace, remove the worktree, pull main",
		ArgsUsage: "<task-id>[-suffix]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "when run from inside the task's own workspace, close it after the report instead of refusing",
			},
		},
		Action: runFinish,
	}
}

func runFinish(ctx context.Context, cmd *cli.Command) error {
	if err := herdr.Available(); err != nil {
		return err
	}

	token := strings.TrimSpace(cmd.Args().First())
	if token == "" {
		return errors.New("finish needs a task id")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	host, err := t.hostCLI()
	if err != nil {
		return err
	}

	id, suffix := ref.SplitSuffix(token)

	r, err := t.reference(ctx, id)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	in := finishInput{
		main:      cmp.Or(t.res.MainRoot, t.res.CheckoutRoot),
		driver:    driver.Find(t.res.StorageRoot, t.dir),
		task:      r,
		suffix:    suffix,
		host:      host,
		hosting:   factValue(t.eff, "Hosting & PR"),
		cwd:       cwd,
		workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		yes:       cmd.Bool("yes"),
	}

	// out-of-tree storage shares one plans dir between every worktree;
	// in-repo storage keeps each worktree's plans under its own .f10/
	if t.res.StorageRoot != filepath.Join(t.res.CheckoutRoot, ".f10") {
		in.plansDir = t.pb.PlansDir
	}

	return finish(ctx, cmd.Writer, in)
}

// factValue is the effective value of one project.md field, or "".
func factValue(eff *facts.Effective, name string) string {
	for _, f := range eff.Fields {
		if f.Name == name {
			return f.Value
		}
	}

	return ""
}

// finishInput is everything finish needs once the target is resolved, so
// the flow can be tested without a checkout.
type finishInput struct {
	main      string         // the main checkout: host and git commands run here
	driver    *driver.Driver // nil without one
	task      ref.Ref
	suffix    string
	host      string // gh or glab
	hosting   string // the Hosting & PR fact, read for a declared merge method
	cwd       string // where the command runs: inside the worktree is the self-close case
	workspace string // HERDR_WORKSPACE_ID: the workspace this process runs in, or ""
	yes       bool
	plansDir  string // the plans dir every worktree shares, or "" when each keeps its own under .f10/plans
}

// finish runs the flow: branch, worktree, the refusals, the merge, the
// pull, the plan, the report, the workspace, the removal. The report is
// printed before the workspace closes, because on the self-close path the
// close ends this process too.
func finish(ctx context.Context, w io.Writer, in finishInput) error {
	name, path, err := finishTarget(ctx, in)
	if err != nil {
		return err
	}

	if err := refuseDirty(ctx, path); err != nil {
		return err
	}

	wss, err := herdr.Workspaces(ctx, in.main)
	if err != nil {
		return err
	}

	ws, mainWS := workspaceAt(wss, path), workspaceAt(wss, in.main)

	self := inside(in.cwd, path) || (ws != nil && in.workspace != "" && in.workspace == ws.ID)
	if self && !in.yes {
		return fmt.Errorf("f10 finish %s would close its own workspace (%s): "+
			"run it from the main checkout, or pass --yes to close this workspace after the report",
			in.task.ID, cmp.Or(workspaceLabel(ws), name))
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return err
	}

	var notes []string

	p, note, err := landPull(ctx, in, name, def)
	if err != nil {
		return err
	}

	if note != "" {
		notes = append(notes, note)
	}

	if err := pullMain(ctx, in.main, def, p.sha(), &notes); err != nil {
		return err
	}

	archived, planNotes := archivePlan(path, in.main, in.plansDir, in.task.ID)
	notes = append(notes, planNotes...)

	rows := []fact{
		{label: "task", value: in.task.ID},
		{label: "pr", value: p.url(), url: true},
		{label: "merge", value: p.sha()},
	}

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived})
	}

	if ws != nil {
		rows = append(rows, fact{label: "workspace", value: ws.ID})
	} else {
		notes = append(notes, "no Herdr workspace shows the worktree, so nothing to close")
	}

	rows = append(rows, fact{label: "path", value: path})

	if self {
		notes = append(notes, fmt.Sprintf(
			"this workspace closes after this report; if %s is still listed by `git worktree list` afterwards, "+
				"run `f10 finish %s` again from the main checkout to remove it", path, in.task.ID))
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if self && mainWS != nil {
		if err := herdr.FocusWorkspace(ctx, in.main, mainWS.ID); err != nil {
			return err
		}
	}

	if ws != nil {
		if err := herdr.CloseWorkspace(ctx, in.main, ws.ID); err != nil {
			return err
		}
	}

	return removeCheckout(ctx, in.main, path, name)
}

// finishTarget is the task's branch and the worktree that has it checked
// out. Both must exist: finish removes only what start could have created.
func finishTarget(ctx context.Context, in finishInput) (string, string, error) {
	// no title lookup: the branch exists, so the id narrows the search by
	// itself and a slug would cost a host call for nothing
	want, _, err := branchName(ctx, in.driver, in.task, in.suffix, nil)
	if err != nil {
		return "", "", err
	}

	name, err := existingBranch(ctx, in.main, want, in.task.ID, in.suffix)
	if err != nil {
		return "", "", err
	}

	if name == "" {
		return "", "", fmt.Errorf("%s has no branch here: nothing to finish", in.task.ID)
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return "", "", err
	}

	path := worktreeOf(wts, name)
	if path == "" {
		return "", "", fmt.Errorf("branch %s has no worktree here: finish removes only what start created", name)
	}

	return name, path, nil
}

// refuseDirty stops on anything git would list in the worktree, named file
// by file. There is no --force: the work is the user's to commit or drop.
func refuseDirty(ctx context.Context, path string) error {
	lines, err := gitx.Status(ctx, path)
	if err != nil {
		return err
	}

	if len(lines) == 0 {
		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "worktree %s has uncommitted changes, commit or discard them first:", path)

	for _, l := range lines {
		b.WriteString("\n  " + l)
	}

	return errors.New(b.String())
}

// workspaceAt is the workspace showing the checkout at path, or nil.
func workspaceAt(wss []herdr.Listed, path string) *herdr.Listed {
	for i := range wss {
		if wss[i].Path != "" && samePath(wss[i].Path, path) {
			return &wss[i]
		}
	}

	return nil
}

func workspaceLabel(ws *herdr.Listed) string {
	if ws == nil {
		return ""
	}

	return ws.Label
}

// samePath compares two paths with symlinks resolved where they exist.
func samePath(a, b string) bool {
	return resolved(a) == resolved(b)
}

// inside reports whether dir is path or lies below it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(resolved(path), resolved(dir))
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolved is the path with symlinks resolved. A component that does not
// exist - a removed or never-created path - keeps its name below the
// longest prefix that does, so two spellings of one place still compare.
func resolved(p string) string {
	p = filepath.Clean(p)

	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}

	parent := filepath.Dir(p)
	if parent == p {
		return p
	}

	return filepath.Join(resolved(parent), filepath.Base(p))
}

// localDefault is the default branch as a local name ("main"), read from
// what origin points HEAD at. Without it finish cannot tell a stacked PR
// from one against main, nor what to pull.
func localDefault(ctx context.Context, dir string) (string, error) {
	def := defaultBranch(ctx, dir)
	if def == "" {
		return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
	}

	return strings.TrimPrefix(def, "origin/"), nil
}

// landPull makes sure the branch's request is merged: merges it when open,
// confirms it when already merged, refuses otherwise. It then deletes the
// remote branch when the host still has it, which is what retargets a PR
// stacked on this branch. The note says when the merge was not this run's.
func landPull(ctx context.Context, in finishInput, name, def string) (pull, string, error) {
	p, err := viewPull(ctx, in.main, in.host, name)
	if err != nil {
		return pull{}, "", err
	}

	var note string

	switch p.status() {
	case pullOpen:
		if base := p.base(); base != "" && base != def {
			return pull{}, "", fmt.Errorf("%s is stacked on %s, not %s: finish the task that branch belongs to first, "+
				"and the host retargets this PR to %s", p.url(), base, def, def)
		}

		method, err := mergeMethod(ctx, in.main, in.host, in.hosting)
		if err != nil {
			return pull{}, "", err
		}

		if p, err = mergePull(ctx, in.main, in.host, name, method); err != nil {
			return pull{}, "", err
		}
	case pullMerged:
		note = p.url() + " was already merged"
	default:
		return pull{}, "", fmt.Errorf("%s is %s without a merge: finish only finishes merged or mergeable work",
			p.url(), p.status())
	}

	if p.status() != pullMerged || p.sha() == "" {
		return pull{}, "", fmt.Errorf("%s reports %s as %s with no merge commit after the merge",
			in.host, p.url(), p.status())
	}

	exists, err := gitx.RemoteBranchExists(ctx, in.main, name)
	if err != nil {
		return pull{}, "", err
	}

	if exists {
		if err := deleteRemoteBranch(ctx, in.main, in.host, name); err != nil {
			return pull{}, "", fmt.Errorf("%s merged, but deleting its remote branch failed: %w", p.url(), err)
		}
	}

	return p, note, nil
}

// pullMain brings the local default branch up to the merge: a
// fast-forward pull when the main checkout is on it, a fetch into the
// branch when the checkout is parked elsewhere. Either way the merge
// commit must then be in it, or nothing local is removed.
func pullMain(ctx context.Context, main, def, sha string, notes *[]string) error {
	if gitx.CurrentBranch(ctx, main) == def {
		if err := gitx.PullFF(ctx, main); err != nil {
			return err
		}
	} else {
		if err := gitx.FetchInto(ctx, main, def); err != nil {
			return err
		}

		*notes = append(
			*notes,
			fmt.Sprintf("%s updated by fetch: the main checkout at %s is on another branch and was left there",
				def, main),
		)
	}

	if !gitx.IsAncestor(ctx, main, sha, def) {
		return fmt.Errorf("merge commit %s is not in %s after pulling: nothing removed", sha, def)
	}

	return nil
}

// archivePlan moves the task's plan out of the worktree before the worktree
// goes: into main's plans/archive/ for in-repo storage, into the shared
// root's archive/ for out-of-tree. Prior versions already archived beside it
// move along. A missing plan is a note, since a task shipped without one
// still needs cleaning up.
func archivePlan(worktree, main, plansDir, id string) (string, []string) {
	src := filepath.Join(worktree, ".f10", "plans")
	dst := filepath.Join(main, ".f10", "plans", "archive")

	if plansDir != "" {
		src, dst = plansDir, filepath.Join(plansDir, "archive")
	}

	plan := filepath.Join(src, id+".md")
	if _, err := os.Stat(plan); err != nil {
		return "", []string{fmt.Sprintf("no plan at %s, nothing archived", plan)}
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", []string{fmt.Sprintf("plan left at %s: %v", plan, err)}
	}

	var notes []string

	// in-repo: older versions the worktree archived go first, so the
	// current plan ends up with the highest number
	if plansDir == "" {
		older, _ := filepath.Glob(filepath.Join(src, "archive", id+".*.md"))
		for _, o := range older {
			if err := os.Rename(o, freeArchiveName(dst, id)); err != nil {
				notes = append(notes, fmt.Sprintf("prior plan left at %s: %v", o, err))
			}
		}
	}

	target := freeArchiveName(dst, id)
	if err := os.Rename(plan, target); err != nil {
		return "", append(notes, fmt.Sprintf("plan left at %s: %v", plan, err))
	}

	return target, notes
}

// freeArchiveName is `<id>.md` under dir when free, else `<id>.<N>.md` with
// the next integer, the numbering steps/plan.md uses.
func freeArchiveName(dir, id string) string {
	candidate := filepath.Join(dir, id+".md")
	if _, err := os.Stat(candidate); err != nil {
		return candidate
	}

	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s.%d.md", id, n))
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
}

// removeCheckout removes the worktree and deletes its branch, through
// worktrunk when it is on PATH so the project's hooks fire, else with git.
// The branch goes with -D: the host confirmed the merge, which a squash or
// rebase hides from git.
func removeCheckout(ctx context.Context, main, path, branch string) error {
	if shell.Has("wt") {
		res, err := shell.Capture(
			ctx,
			main,
			"wt",
			"remove",
			"--yes",
			"--force-delete",
			"--foreground",
			"--format",
			"json",
			branch,
		)
		if err != nil {
			return fmt.Errorf("running wt remove: %w", err)
		}

		if res.Code != 0 {
			return fmt.Errorf(
				"wt remove %s: %s",
				branch,
				cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)),
			)
		}

		return nil
	}

	if err := gitx.RemoveWorktree(ctx, main, path); err != nil {
		return err
	}

	return gitx.DeleteBranch(ctx, main, branch)
}
