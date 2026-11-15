// Package history is the f10 history command: what git, the ledger and lore
// know about a set of files, for the plan and judge steps to weigh.
package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/history"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the history verb.
func Command() *cli.Command {
	return &cli.Command{
		Name: "history",
		Usage: "what the tasks that touched these files went through: review rounds, recurring findings, " +
			"regressions, and the lessons and decisions anchored there, in at most 15 lines",
		ArgsUsage: "<path>...",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "since", Value: "180d", Usage: "the window: commits of the last `N`d or Nw"},
			&cli.StringFlag{
				Name:  "task",
				Usage: "the task being planned, left out of its own history; the branch's task when unset",
			},
			ui.JSONFlag("emit the whole brief as JSON, uncapped"),
		},
		Action: runHistory,
	}
}

func runHistory(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return ui.Exit(ui.ExitUsage, "history: name at least one <path>")
	}

	window, err := calendar.Parse(cmd.String("since"), time.Now(), calendar.Offsets, nil)
	if err != nil {
		return ui.Exit(ui.ExitUsage, "history: --since: %v", err)
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	top, err := gitx.Toplevel(ctx, t.Dir)
	if err != nil {
		return fmt.Errorf("history needs a git repository: %w", err)
	}

	paths, err := repoPaths(top, cmd.Args().Slice())
	if err != nil {
		return ui.Exit(ui.ExitUsage, "history: %v", err)
	}

	in, err := gather(ctx, t, top, paths, window.Start, ownTask(ctx, t, cmd.String("task")))
	if err != nil {
		return err
	}

	brief := history.Build(in)

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, brief)
	}

	return brief.Write(cmd.Writer)
}

// gather reads git first, because the neighbors it finds bound the ledger
// read to their sessions and the project's regression rows.
func gather(
	ctx context.Context,
	t *target.Target,
	top string,
	paths []string,
	since time.Time,
	own string,
) (history.Input, error) {
	in := history.Input{Paths: paths, Since: since, Kinds: map[string]string{}}

	touches, err := history.Neighbors(ctx, top, t.Effective.IDPrefix, paths, since, own)
	if err != nil {
		return in, fmt.Errorf("history: %w", err)
	}

	in.Touches = touches

	project := ledger.Where(ctx, t.Dir).Project

	var tasks []string

	for _, tc := range touches {
		tasks = append(tasks, tc.Task)
		in.Kinds[tc.Task] = history.PlanKind(t.Layout.StorageRoot.Plans(), tc.Task)
	}

	if in.Events, err = history.ReadLedger(ctx, ledger.Path(), project, tasks); err != nil {
		return in, err
	}

	store := &lore.Store{Root: t.LoreRoot(), Git: lore.CLIGit{Dir: top}}

	for _, p := range paths {
		lessons, err := store.Lessons(p)
		if err != nil {
			return in, err
		}

		decisions, err := store.Decisions(p)
		if err != nil {
			return in, err
		}

		in.Lessons, in.Decisions = append(in.Lessons, lessons...), append(in.Decisions, decisions...)
	}

	return in, nil
}

// ownTask is the task the brief is for: --task, else the branch's, else none.
func ownTask(ctx context.Context, t *target.Target, explicit string) string {
	r, err := t.BranchReference(ctx, explicit)
	if err != nil {
		return strings.TrimSpace(explicit)
	}

	return r.Head().ID
}

// repoPaths is each path repo-relative and slash-separated, as lore anchors
// and git pathspecs at the root spell it. A relative path reads from the
// cwd inside the repository, and from its root under -C from elsewhere.
func repoPaths(top string, args []string) ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	root := gitx.Resolved(top)
	if !inside(root, gitx.Resolved(cwd)) {
		cwd = root
	}

	var out []string

	for _, p := range args {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, p)
		}

		if !inside(root, gitx.Resolved(abs)) {
			return nil, fmt.Errorf("%s is outside the repository at %s", p, top)
		}

		rel, _ := filepath.Rel(root, gitx.Resolved(abs))
		out = append(out, filepath.ToSlash(rel))
	}

	return out, nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
