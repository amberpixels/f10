package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A spike's run ends at its report with the experiment uncommitted and no
// PR, so finish closes it without a merge.

func taskSpikesDir(checkout, plansDir string) string {
	if plansDir != "" {
		return filepath.Join(filepath.Dir(plansDir), "spikes")
	}

	return filepath.Join(checkout, ".f10", "spikes")
}

// taskSpike is the task's spike report, "" for none. A plan that is not a
// spike's wins over a report, so a report left from an earlier spike never
// closes a task planned again as something else.
func taskSpike(checkout, plansDir, id string) string {
	for _, dir := range []string{taskSpikesDir(checkout, plansDir), filepath.Join(checkout, ".f10", "spikes")} {
		p := filepath.Join(dir, id+".md")
		if !exists(p) {
			continue
		}

		if plan := taskPlan(checkout, plansDir, id); exists(plan) && !spikePlan(plan) {
			return ""
		}

		return p
	}

	return ""
}

// kindSpike matches a plan's kind line in the shapes plans write it.
var kindSpike = regexp.MustCompile(`(?i)^(- )?kind\s*:\s*spike\b`)

func spikePlan(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	lines := strings.Split(string(b), "\n")
	for _, l := range lines[:min(len(lines), 20)] {
		if kindSpike.MatchString(strings.NewReplacer("*", "", "_", "", "`", "").Replace(strings.TrimSpace(l))) {
			return true
		}
	}

	return false
}

// spikeArchiveName is <id>.spike.md in the plans archive, which also counts
// the task finished for a spike planned inline with no plan file.
func spikeArchiveName(main, plansDir, id string) (string, error) {
	dst := filepath.Join(main, ".f10", "plans", "archive")
	if plansDir != "" {
		dst = filepath.Join(plansDir, "archive")
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", fmt.Errorf("archiving the spike report: %w", err)
	}

	return freeArchiveName(dst, id+".spike"), nil
}

func archiveSpike(report, target string) error {
	if err := os.Rename(report, target); err != nil {
		return fmt.Errorf("archiving the spike report at %s: %w", report, err)
	}

	return nil
}

// copySpike copies the report to target, leaving the original where a
// rerun still finds it until the worktree holding it is removed.
func copySpike(report, target string) error {
	b, err := os.ReadFile(report)
	if err == nil {
		err = os.WriteFile(target, b, 0o600) //nolint:gosec // a path in the storage root this run named
	}

	if err != nil {
		return fmt.Errorf("archiving the spike report at %s: %w", report, err)
	}

	return nil
}

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

// trimStatus is the paths of porcelain lines; the first line may have lost
// its leading space to the output's trim.
func trimStatus(lines []string) []string {
	paths := make([]string, 0, len(lines))
	for _, l := range lines {
		_, path, _ := strings.Cut(strings.TrimSpace(l), " ")
		paths = append(paths, strings.TrimSpace(path))
	}

	return paths
}

// keptName is kept/ and the branch's name with the task's ids taken out, so
// no task lookup finds it; numbered when taken.
func keptName(ctx context.Context, main, branch string, ids []string) string {
	slug := branch
	for _, id := range ids {
		slug = idPattern(id).ReplaceAllString(slug, "")
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

// idPattern matches id as branch lookups do: any case, the dash before
// the number optional.
func idPattern(id string) *regexp.Regexp {
	prefix, num, ok := strings.Cut(id, "-")
	if !ok {
		return regexp.MustCompile(`(?i)` + regexp.QuoteMeta(id))
	}

	return regexp.MustCompile(`(?i)` + regexp.QuoteMeta(prefix) + `-?` + regexp.QuoteMeta(num) + `\b`)
}

// commitExperiment skips hooks: experiment code need not pass them and
// never leaves the machine.
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

// discardExperiment leaves the worktree clean for either removal backend;
// ignored files (a legacy .f10 among them) are left to the removal.
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
	if err := createKept(ctx, main, branch, kept); err != nil {
		return err
	}

	return gitx.DeleteBranch(ctx, main, branch)
}

func createKept(ctx context.Context, main, branch, kept string) error {
	if _, err := gitx.Exec(ctx, main, "branch", kept, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("keeping %s as %s: %w", branch, kept, err)
	}

	return nil
}

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

	// the kept branch exists before the worktree goes, so a failed removal
	// leaves the worktree a rerun can still finish
	if err := commitExperiment(ctx, path, in.task.ID, dirty); err != nil {
		return err
	}

	if err := createKept(ctx, in.main, name, kept); err != nil {
		return err
	}

	if err := gitx.RemoveWorktree(ctx, in.main, path); err != nil {
		return err
	}

	return gitx.DeleteBranch(ctx, in.main, name)
}
