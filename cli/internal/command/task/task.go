// Package task is the f10 task, f10 plan and f10 demo commands: reading,
// creating and updating the project's tasks and opening their plans and demos.
package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/taskwrite"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the task noun; it goes through the project's driver, else the host's issues:
// a repo with no .f10/ at all still answers, and one whose tracker has no
// CLI answers the same way once it drops in a driver.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "task",
		Usage: "read, open, search, create, comment on or update the project's tasks",
		Commands: []*cli.Command{
			{
				Name:      "read",
				Usage:     "write the task to stdout as markdown",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{&cli.BoolFlag{
					Name:  "no-images",
					Usage: "into a pipe too, leave the task's images as links (a title lookup)",
				}},
				Action: runTaskRead,
			},
			{
				Name:     "current",
				Hidden:   true,
				Category: command.Plumbing,
				Usage: "print the task the branch names, else the one this session's run records " +
					"(exit 1: none)",
				Action: runTaskCurrent,
			},
			{
				Name:     "ref",
				Hidden:   true,
				Category: command.Plumbing,
				Usage: "print each task as `<id> (<title>)`, or a markdown link with --md, from the titles f10 " +
					"already read; an unknown id prints bare",
				ArgsUsage: "<task-id>...",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "md", Usage: "print `[<id>: <title>](<url>)`"},
					&cli.BoolFlag{Name: "fresh", Usage: "ask the tracker even for a task f10 already read"},
				},
				Action: runTaskRef,
			},
			{
				Name:     "report",
				Hidden:   true,
				Category: command.Plumbing,
				Usage: "print a report's facts block for the tasks: one task's task, title and url rows, else a row " +
					"per task; from the titles f10 already read",
				ArgsUsage: "<task-id>... | --children <parent-id>",
				Flags: []cli.Flag{&cli.BoolFlag{
					Name:  "children",
					Usage: "draw the parent with its open subtasks under it, in After: order, each with its title and url",
				}},
				Action: runTaskReport,
			},
			{
				Name:     "stale",
				Hidden:   true,
				Category: command.Plumbing,
				Usage: "print the stale step's check against stale_after: fresh, judged <verdict file>, needs judge, " +
					"or undated",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{&cli.StringFlag{
					Name:  "created",
					Usage: "the task's created date as `YYYY-MM-DD`, for a tracker the host's issues do not answer",
				}},
				Action: runTaskStale,
			},
			regressionCommand(),
			{
				Name:      "open",
				Usage:     "open the task in the browser; with no task id, the task list",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{render.LinkFlag(), render.PrintFlag()},
				Action:    runTaskOpen,
			},
			{
				Name:      "search",
				Usage:     "find tasks by description",
				ArgsUsage: "<query>",
				Action:    runTaskSearch,
			},
			{
				Name:      "children",
				Hidden:    true,
				Category:  command.Plumbing,
				Usage:     "list the task's open subtasks in After: order (exit 3: the tracker has no parent link)",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					ui.JSONFlag("emit the rows as JSON"),
					&cli.BoolFlag{Name: "open", Usage: "leave out finished rows too (merged, or the plan archived)"},
					&cli.BoolFlag{
						Name:  "plain",
						Usage: "print the tree, titles and after notes and no urls, even into a pipe (for a skill to relay)",
					},
				},
				Action: runTaskChildren,
			},
			{
				Name: "create",
				Usage: "create a task through the driver, else as an issue on the host, from markdown on stdin, " +
					"`# <title>` first, and print its id (exit 3: neither creates tasks here)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "status", Usage: "the status, in the tracker's own word"},
					&cli.StringFlag{Name: "priority", Usage: "one of " + strings.Join(trackerdriver.Priorities, ", ")},
					&cli.StringSliceFlag{Name: "label", Usage: "a label; repeat for several"},
					&cli.StringFlag{Name: "parent", Usage: "the task id to link it under as a subtask"},
					&cli.StringSliceFlag{
						Name:  "set",
						Usage: "`key=value` for a property the driver knows; repeat for several (host issues refuse it)",
					},
				},
				Action: runTaskCreate,
			},
			{
				Name: "comment",
				Usage: "add markdown on stdin to the task as a comment, and print its url when the tracker gives one " +
					"(exit 3: this tracker takes no comments here); a closed task is refused without --closed",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "closed", Usage: "comment even though the task is closed"},
				},
				Action: runTaskComment,
			},
			{
				Name: "retitle",
				Usage: "write tracker.title onto the task's title on the host's issues, or onto every issue's " +
					"with --all (exit 3: the tracker is not the host's)",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "every issue, open and closed"},
					&cli.BoolFlag{Name: "dry-run", Usage: "print the changes and write nothing"},
				},
				Action: runTaskRetitle,
			},
			{
				Name: "update",
				Usage: "set the task's status, priority, labels, assignee, parent, After: line or a named property " +
					"(exit 3: this tracker takes no updates here)",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "status",
						Usage: "the status, in the tracker's own word (host issues: open or closed)",
					},
					&cli.StringFlag{Name: "priority", Usage: "one of " + strings.Join(trackerdriver.Priorities, ", ")},
					&cli.StringSliceFlag{Name: "label", Usage: "a label to add; repeat for several"},
					&cli.StringFlag{Name: "assignee", Usage: "who holds the task, in the tracker's own name for them"},
					&cli.StringFlag{
						Name:  "parent",
						Usage: "the task id to move it under as a subtask; \"\" takes it out from under its parent",
					},
					&cli.StringSliceFlag{
						Name:  "set",
						Usage: "`key=value` for a property the driver knows; repeat for several",
					},
					&cli.StringFlag{
						Name:  "after",
						Usage: "the task id its body's After: line names; none drops the line",
					},
				},
				Action: runTaskUpdate,
			},
		},
	}
}

// runTaskCurrent names the task a bare skill call stands for, by the cascade
// a bare finish walks, so no skill parses a branch name itself.
func runTaskCurrent(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.Reference(ctx, "")
	if err != nil {
		return fmt.Errorf("no current task: %w", err)
	}

	_, err = fmt.Fprintln(cmd.Writer, r.Label())

	return err
}

// ReadDoc is taskwrite.Read.
func ReadDoc(ctx context.Context, t *target.Target, r ref.Ref) (string, error) {
	return taskwrite.Read(ctx, t, r)
}

// WithImages is taskwrite.WithImages.
func WithImages(ctx context.Context, doc, dir string, d images.Downloader) string {
	return taskwrite.WithImages(ctx, doc, dir, d)
}

// GithubAuth is taskwrite.GithubAuth.
func GithubAuth(ctx context.Context, t *target.Target) func(*http.Request) {
	return taskwrite.GithubAuth(ctx, t)
}

func runTaskOpen(ctx context.Context, cmd *cli.Command) error {
	listing := !cmd.Args().Present()

	if cmd.Bool("print") && !listing {
		return runTaskRead(ctx, cmd)
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	// no task id is the list, never the branch's task: read is for that
	switch {
	case listing && cmd.Bool("print"):
		return WriteTaskList(ctx, t, cmd.Writer, cmd.ErrWriter)
	case listing:
		url, err := TaskListURL(ctx, t)
		if err != nil {
			return err
		}

		return render.Follow(ctx, cmd.Writer, t.Dir, url, cmd.Bool("link"))
	}

	r, err := t.Reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	url, err := tasksource.TasksFor(t).URL(ctx, r)
	if err != nil {
		return err
	}

	return render.Follow(ctx, cmd.Writer, t.Dir, url, cmd.Bool("link"))
}

func TaskListURL(ctx context.Context, t *target.Target) (string, error) {
	return tasksource.TasksFor(t).Index(ctx)
}

func WriteTaskList(ctx context.Context, t *target.Target, w, warn io.Writer) error {
	rows, err := stack.TaskRows(ctx, t, hostops.IssuePage)
	if err != nil {
		return err
	}

	return hostops.WriteRows(w, RankRows(hostops.SettlePriorities(rows, warn)), "no open tasks")
}

func runTaskSearch(ctx context.Context, cmd *cli.Command) error {
	query := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
	if query == "" {
		return errors.New("search needs a query")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	rows, err := SearchRows(ctx, t, query)
	if err != nil {
		return err
	}

	// search keeps the tracker's relevance order; only the open list ranks
	return hostops.WriteRows(cmd.Writer, hostops.SettlePriorities(rows, cmd.ErrWriter), "no matches")
}

// RankRows is stable, so the tracker's order holds inside a level.
func RankRows(rows []hostops.Row) []hostops.Row {
	rank := func(r hostops.Row) int {
		if i, ok := trackerdriver.PriorityRank(r.Priority); ok {
			return i
		}

		return len(trackerdriver.Priorities)
	}

	slices.SortStableFunc(rows, func(a, b hostops.Row) int { return rank(a) - rank(b) })

	return rows
}

func SearchRows(ctx context.Context, t *target.Target, query string) ([]hostops.Row, error) {
	rows, err := tasksource.TasksFor(t).Search(ctx, query)
	stack.RememberRefsFor(ctx, t, hostops.RowRefs(rows)...)

	return rows, err
}
