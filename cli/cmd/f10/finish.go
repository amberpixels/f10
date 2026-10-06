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
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
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
// is touched. Every refusal comes before the first change, and the plan
// is out of the worktree before the worktree goes.
func finishCommand() *cli.Command {
	return &cli.Command{
		Name:      "finish",
		Usage:     "finish a task: merge its PR, archive the plan, remove the worktree, close the workspace, pull main",
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

	h, err := t.host()
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

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	return finish(ctx, cmd.Writer, finishInput{
		main:      main,
		driver:    driver.Find(t.lay.StorageRoot, t.dir),
		task:      r,
		suffix:    suffix,
		host:      h.at(main),
		hosting:   t.fact("Hosting & PR"),
		cwd:       cwd,
		workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		yes:       cmd.Bool("yes"),
		plansDir:  t.sharedPlansDir(),
	})
}

// finishInput is everything finish needs once the target is settled, so
// the flow can be tested without a checkout.
type finishInput struct {
	main      string         // the main checkout: host and git commands run here
	driver    *driver.Driver // nil without one
	task      ref.Ref
	suffix    string
	host      host
	hosting   string // the Hosting & PR fact, read for a declared merge method
	cwd       string // where the command runs: inside the worktree is the self-close case
	workspace string // HERDR_WORKSPACE_ID: the workspace this process runs in, or ""
	yes       bool
	plansDir  string // the plans dir every worktree shares, or "" when each keeps its own under .f10/plans
}

// finish runs the flow: branch, worktree, the refusals, the merge, the
// pull, the dependents, the plan, the report, the removal, the workspace.
// The report is printed before anything local goes, and the workspace
// closes last: on the self-close path the close ends this process, so
// everything that must happen has happened by then.
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

	p, notes, err := landPull(ctx, in, name, def)
	if err != nil {
		return err
	}

	note, err := pullMain(ctx, in.main, def, p.sha())
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)

	released, err := releaseDependents(ctx, in.main, name, def)
	if err != nil {
		return err
	}

	notes = append(notes, released...)

	archived, note, err := archivePlan(path, in.main, in.plansDir, in.task.ID)
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)

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
		notes = append(notes, "this workspace closes after this report")
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if err := removeCheckout(ctx, in.main, path, name); err != nil {
		return err
	}

	if self && mainWS != nil {
		if err := herdr.FocusWorkspace(ctx, in.main, mainWS.ID); err != nil {
			return err
		}
	}

	if ws != nil {
		return herdr.CloseWorkspace(ctx, in.main, ws.ID)
	}

	return nil
}

// appendNote adds note to notes unless it is empty.
func appendNote(notes []string, note string) []string {
	if note == "" {
		return notes
	}

	return append(notes, note)
}

// finishTarget is the task's branch and the worktree that has it checked
// out. Both must exist: finish removes only what start could have created.
func finishTarget(ctx context.Context, in finishInput) (string, string, error) {
	name, path, err := taskCheckout(ctx, in.main, in.driver, in.task, in.suffix)

	switch {
	case err != nil:
		return "", "", err
	case name == "":
		return "", "", fmt.Errorf("%s has no branch here: nothing to finish", in.task.ID)
	case path == "":
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

// landPull makes sure the branch's request is merged: merges it when open,
// confirms it when already merged, refuses otherwise. It then deletes the
// remote branch when the host still has it, which is what retargets a PR
// stacked on this branch. The notes say when the merge was not this run's
// and when the merge method was a default rather than a declaration.
func landPull(ctx context.Context, in finishInput, name, def string) (pull, []string, error) {
	p, err := in.host.pull(ctx, name)
	if err != nil {
		return pull{}, nil, err
	}

	var notes []string

	switch p.status() {
	case pullOpen:
		if base := p.base(); base != "" && base != def {
			return pull{}, nil, fmt.Errorf(
				"%s is stacked on %s, not %s: finish the task that branch belongs to first, "+
					"and the host retargets this PR to %s",
				p.url(),
				base,
				def,
				def,
			)
		}

		method, note, err := in.host.mergeMethod(ctx, in.hosting)
		if err != nil {
			return pull{}, nil, err
		}

		notes = appendNote(notes, note)

		if p, err = in.host.mergePull(ctx, name, method); err != nil {
			return pull{}, nil, err
		}
	case pullMerged:
		notes = append(notes, p.url()+" was already merged")
	default:
		return pull{}, nil, fmt.Errorf("%s is %s without a merge: finish only finishes merged or mergeable work",
			p.url(), p.status())
	}

	if p.status() != pullMerged || p.sha() == "" {
		return pull{}, nil, fmt.Errorf("%s reports %s as %s with no merge commit after the merge",
			in.host.name, p.url(), p.status())
	}

	exists, err := gitx.RemoteBranchExists(ctx, in.main, name)
	if err != nil {
		return pull{}, nil, err
	}

	if exists {
		if err := in.host.deleteRemoteBranch(ctx, name); err != nil {
			return pull{}, nil, fmt.Errorf("%s merged, but deleting its remote branch failed: %w", p.url(), err)
		}
	}

	return p, notes, nil
}

// pullMain brings the local default branch up to the merge: a
// fast-forward pull when the main checkout is on it, a fetch into the
// branch when the checkout is parked elsewhere, which the note says.
// Either way the merge commit must then be in it, or nothing local is
// removed.
func pullMain(ctx context.Context, main, def, sha string) (string, error) {
	var note string

	if gitx.CurrentBranch(ctx, main) == def {
		if err := gitx.PullFF(ctx, main); err != nil {
			return "", err
		}
	} else {
		if err := gitx.FetchInto(ctx, main, def); err != nil {
			return "", err
		}

		note = fmt.Sprintf("%s updated by fetch: the main checkout at %s is on another branch and was left there",
			def, main)
	}

	if !gitx.IsAncestor(ctx, main, sha, def) {
		return "", fmt.Errorf("merge commit %s is not in %s after pulling: nothing removed", sha, def)
	}

	return note, nil
}

// releaseDependents clears the dependency other branches recorded on the
// finished one with `start --after`, and marks each landed. Its code is in
// the default branch now, so a dependent's pr targets the default branch;
// but the dependent branched before that code existed, so the mark has its
// next ship or catchup bring the default branch in first. Without the
// release the dependents would point at a branch that no longer exists
// anywhere; without the mark, ship would implement on a tree missing the
// base's change.
func releaseDependents(ctx context.Context, dir, branch, def string) ([]string, error) {
	var notes []string

	for _, e := range gitx.ConfigEntries(ctx, dir, `^branch\..*\.`+cfgAfterBranch+`$`) {
		if e.Value != branch {
			continue
		}

		dependent := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+cfgAfterBranch)
		section := "branch." + dependent + "."
		base := gitx.Out(ctx, dir, "config", "--get", section+cfgAfter)

		for _, key := range []string{cfgAfter, cfgAfterBranch} {
			if err := gitx.UnsetConfig(ctx, dir, section+key); err != nil {
				return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
			}
		}

		if err := gitx.SetConfig(ctx, dir, section+cfgLanded, cmp.Or(base, branch)); err != nil {
			return nil, fmt.Errorf("marking %s landed on %s: %w", dependent, def, err)
		}

		notes = append(notes, fmt.Sprintf("%s depended on %s: the dependency is cleared and its pr targets %s now; "+
			"its next ship or /f10:catchup brings %s in first", dependent, branch, def, def))
	}

	return notes, nil
}

// archivePlan moves the task's plan out of the worktree before the worktree
// goes: into main's plans/archive/ for in-repo storage, into the shared
// root's archive/ for out-of-tree. Prior versions already archived beside it
// move along. A missing plan is a note, since a task shipped without one
// still needs cleaning up; a plan that cannot be moved is an error, since
// removing the worktree would delete it.
func archivePlan(worktree, main, plansDir, id string) (string, string, error) {
	src := filepath.Join(worktree, ".f10", "plans")
	dst := filepath.Join(main, ".f10", "plans", "archive")

	if plansDir != "" {
		src, dst = plansDir, filepath.Join(plansDir, "archive")
	}

	plan := filepath.Join(src, id+".md")
	if !exists(plan) {
		return "", fmt.Sprintf("no plan at %s, nothing archived", plan), nil
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	// in-repo: older versions the worktree archived go first, so the
	// current plan ends up with the highest number
	if plansDir == "" {
		older, _ := filepath.Glob(filepath.Join(src, "archive", id+".*.md"))
		for _, o := range older {
			if err := os.Rename(o, freeArchiveName(dst, id)); err != nil {
				return "", "", fmt.Errorf("archiving the prior plan at %s: %w", o, err)
			}
		}
	}

	target := freeArchiveName(dst, id)
	if err := os.Rename(plan, target); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	return target, "", nil
}

// freeArchiveName is `<id>.md` under dir when free, else `<id>.<N>.md` with
// the next integer, the numbering steps/plan.md uses.
func freeArchiveName(dir, id string) string {
	candidate := filepath.Join(dir, id+".md")
	if !exists(candidate) {
		return candidate
	}

	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s.%d.md", id, n))
		if !exists(candidate) {
			return candidate
		}
	}
}

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
