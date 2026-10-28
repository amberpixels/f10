package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A spike's run ends at its report with the experiment code uncommitted and
// no PR. Finish closes it without a merge: the report is archived beside the
// plan, the experiment discarded (or, with --keep, committed onto a branch
// that no longer carries the task id), the worktree and branch removed.

// taskSpikesDir is where a task's spike report sits: the shared spikes dir
// beside the shared plans dir, else (none given) the checkout's own.
func taskSpikesDir(checkout, plansDir string) string {
	if plansDir != "" {
		return filepath.Join(filepath.Dir(plansDir), "spikes")
	}

	return filepath.Join(checkout, ".f10", "spikes")
}

// taskSpike is the task's spike report, "" for none: in the shared spikes
// dir, unless only a worktree keeping its own .f10 holds it.
func taskSpike(checkout, plansDir, id string) string {
	for _, dir := range []string{taskSpikesDir(checkout, plansDir), filepath.Join(checkout, ".f10", "spikes")} {
		if p := filepath.Join(dir, id+".md"); exists(p) {
			return p
		}
	}

	return ""
}

// archiveSpike moves the spike report into the plans archive beside the
// plan, as <id>.spike.md, which also counts the task finished for a spike
// planned inline with no plan file.
func archiveSpike(report, main, plansDir, id string) (string, error) {
	dst := filepath.Join(main, ".f10", "plans", "archive")
	if plansDir != "" {
		dst = filepath.Join(plansDir, "archive")
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", fmt.Errorf("archiving the spike report at %s: %w", report, err)
	}

	target := freeArchiveName(dst, id+".spike")
	if err := os.Rename(report, target); err != nil {
		return "", fmt.Errorf("archiving the spike report at %s: %w", report, err)
	}

	return target, nil
}

// spikeNotes are the report's lines for a spike closed without a merge:
// what the experiment loses or where it is kept, and that nothing closes
// the task. dirty is the worktree's status before the close.
func spikeNotes(ctx context.Context, in finishInput, name, def, kept string, dirty []string) []string {
	notes := []string{unclosedNote(in)}

	if kept != "" {
		return append(notes, fmt.Sprintf("the experiment is kept on %s, committed there, never pushed", kept))
	}

	if len(dirty) > 0 {
		notes = append(notes, "the experiment is discarded: "+strings.Join(trimStatus(dirty), ", "))
	}

	if n := gitx.Out(ctx, in.main, "rev-list", "--count", "refs/heads/"+def+"..refs/heads/"+name); n != "" && n != "0" {
		tip := gitx.Out(ctx, in.main, "rev-parse", "--short", "refs/heads/"+name)
		notes = append(notes, fmt.Sprintf("%s's %s commit(s) go with it: git branch %s %s brings them back",
			name, n, name, tip))
	}

	return notes
}

// trimStatus is the paths of `git status --porcelain` lines; the output's
// trim may have taken the first line's leading space.
func trimStatus(lines []string) []string {
	paths := make([]string, 0, len(lines))
	for _, l := range lines {
		_, path, _ := strings.Cut(strings.TrimSpace(l), " ")
		paths = append(paths, strings.TrimSpace(path))
	}

	return paths
}

// keptName is the branch a kept spike moves to: kept/ and the branch's name
// with the task's ids taken out, so no task lookup finds it, numbered when
// taken.
func keptName(ctx context.Context, main, branch string, ids []string) string {
	slug := branch
	for _, id := range ids {
		slug = strings.ReplaceAll(slug, id, "")
	}

	slug = strings.NewReplacer("/-", "/", "-/", "/", "+", "", "//", "/").Replace(slug)
	slug = strings.Trim(slug, "/-")

	if slug == "" {
		slug = "spike"
	}

	name := "kept/" + slug
	for n := 2; gitx.Out(ctx, main, "rev-parse", "-q", "--verify", "refs/heads/"+name) != ""; n++ {
		name = fmt.Sprintf("kept/%s-%d", slug, n)
	}

	return name
}

// commitExperiment commits everything in the worktree onto the branch,
// skipping hooks: experiment code need not pass them, and never leaves the
// machine.
func commitExperiment(ctx context.Context, path, id string, dirty []string) error {
	if len(dirty) == 0 {
		return nil
	}

	if _, err := gitx.Exec(ctx, path, "add", "-A"); err != nil {
		return fmt.Errorf("keeping the experiment: %w", err)
	}

	if _, err := gitx.Exec(
		ctx,
		path,
		"commit",
		"--no-verify",
		"-m",
		id+": spike experiment, not for merge",
	); err != nil {
		return fmt.Errorf("keeping the experiment: %w", err)
	}

	return nil
}

// discardExperiment leaves the worktree clean, so either removal backend
// takes it: tracked changes reset, untracked files removed, ignored ones (a
// legacy .f10 among them) left for the removal.
func discardExperiment(ctx context.Context, path string) error {
	if _, err := gitx.Exec(ctx, path, "reset", "--hard"); err != nil {
		return fmt.Errorf("discarding the experiment in %s: %w", path, err)
	}

	if _, err := gitx.Exec(ctx, path, "clean", "-fd"); err != nil {
		return fmt.Errorf("discarding the experiment in %s: %w", path, err)
	}

	return nil
}

// keepBranch moves the branch to kept, created at its tip; deleting the old
// name drops its config section, the task link with it.
func keepBranch(ctx context.Context, main, branch, kept string) error {
	if _, err := gitx.Exec(ctx, main, "branch", kept, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("keeping %s as %s: %w", branch, kept, err)
	}

	return gitx.DeleteBranch(ctx, main, branch)
}

// removeTaskCheckout removes the worktree and the branch. A spike closed
// here first discards its experiment, or with kept commits it and keeps
// the branch under that name, removing only the worktree.
func removeTaskCheckout(
	ctx context.Context,
	in finishInput,
	path, name, kept string,
	dirty []string,
	spike bool,
) error {
	switch {
	case !spike:
		return removeCheckout(ctx, in.main, path, name)
	case kept == "":
		if len(dirty) > 0 {
			if err := discardExperiment(ctx, path); err != nil {
				return err
			}
		}

		return removeCheckout(ctx, in.main, path, name)
	}

	if err := commitExperiment(ctx, path, in.task.ID, dirty); err != nil {
		return err
	}

	if err := gitx.RemoveWorktree(ctx, in.main, path); err != nil {
		return err
	}

	return keepBranch(ctx, in.main, name, kept)
}
