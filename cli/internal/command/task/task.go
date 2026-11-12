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
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
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
				Usage: "print fetch's stale check against stale_after: fresh, judged <verdict file>, needs judge, " +
					"or undated",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{&cli.StringFlag{
					Name:  "created",
					Usage: "the task's created date as `YYYY-MM-DD`, for a tracker the host's issues do not answer",
				}},
				Action: runTaskStale,
			},
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
					"(exit 3: this tracker takes no comments here)",
				ArgsUsage: "[task-id]",
				Action:    runTaskComment,
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
				Usage: "set the task's status, priority, labels, assignee, parent or a named property " +
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
				},
				Action: runTaskUpdate,
			},
		},
	}
}

func runTaskRead(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	token := cmd.Args().First()

	r, err := t.Reference(ctx, token)
	if err != nil {
		return fetchFallback(err)
	}

	members := []ref.Ref{r}
	if strings.Contains(token, ref.Separator) {
		members = r.MemberRefs()
	}

	docs := make([]string, 0, len(members))

	for _, m := range members {
		doc, err := ReadDoc(ctx, t, m)
		if err != nil {
			return fetchFallback(err)
		}

		// a person at a terminal opens the links themselves and would wait on
		// the downloads; an agent gets local files in place of urls that expire
		if !ui.IsTerminal(cmd.Writer) && !cmd.Bool("no-images") {
			doc = WithImages(ctx, doc, imagesDirFor(ctx, t, m.ID), images.Downloader{Authorize: GithubAuth(ctx, t)})
		}

		docs = append(docs, strings.TrimRight(doc, "\n"))
	}

	return render.WriteDoc(ctx, cmd.Writer, t.Dir, strings.Join(docs, "\n\n---\n\n"))
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

// fetchFallback tells an agent reading a failed read that the project's
// fetch adapter is its fallback: here, when it applies, not in every prompt.
func fetchFallback(err error) error {
	return fmt.Errorf("%w\nfetch the task through the project's fetch adapter instead "+
		"(project.md, Tracker)", err)
}

// ReadDoc feeds the ledger's task refs, so a later ref costs no tracker call.
func ReadDoc(ctx context.Context, t *target.Target, r ref.Ref) (string, error) {
	doc, k, err := hostops.TasksFor(t).Read(ctx, r)
	if err == nil {
		stack.RememberRefsFor(ctx, t, k)
	}

	return doc, err
}

// WithImages ends doc with a footer naming its downloaded images. A dir
// that cannot be written fails every image, never the read.
func WithImages(ctx context.Context, doc, dir string, d images.Downloader) string {
	links := images.Links(doc)
	if len(links) == 0 {
		return doc
	}

	results, err := d.Download(ctx, dir, links)
	if err != nil {
		results = make([]images.Result, len(links))
		for i, u := range links {
			results[i] = images.Result{URL: u, Err: err}
		}
	}

	return strings.TrimRight(doc, "\n") + "\n\n" + images.Footer(results)
}

// imagesDir puts .f10/ into info/exclude first under stealth, since a read
// may be the first thing to create it.
func imagesDirFor(ctx context.Context, t *target.Target, id string) string {
	if !t.OutOfTree() && t.Effective.Visibility == "stealth" {
		_, _ = archive.ExcludeF10(ctx, t.Layout.CheckoutRoot)
	}

	return t.Layout.StorageRoot.Images(strings.ToUpper(id))
}

// GithubAuth sends the gh token only to GitHub's own hosts over https,
// where a private repo's attachments sit. It is read once; a failed read
// leaves the request bare, so its error names the image.
func GithubAuth(ctx context.Context, t *target.Target) func(*http.Request) {
	h, err := hostops.HostOf(t)
	if err != nil {
		return nil
	}

	var (
		once  sync.Once
		token string
	)

	return func(r *http.Request) {
		host := r.URL.Hostname()
		if r.URL.Scheme != "https" || (host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
			return
		}

		once.Do(func() {
			if tok, err := h.Token(ctx); err == nil {
				token = tok
			}
		})

		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
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

	url, err := hostops.TasksFor(t).URL(ctx, r)
	if err != nil {
		return err
	}

	return render.Follow(ctx, cmd.Writer, t.Dir, url, cmd.Bool("link"))
}

func TaskListURL(ctx context.Context, t *target.Target) (string, error) {
	return hostops.TasksFor(t).Index(ctx)
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
	rows, err := hostops.TasksFor(t).Search(ctx, query)
	stack.RememberRefsFor(ctx, t, hostops.RowRefs(rows)...)

	return rows, err
}

// runTaskCreate sends the task on stdin to the driver's create, else makes it
// an issue on the host. f10 never retries it, since a failed write is an
// unknown outcome.
func runTaskCreate(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	// nowhere to create answers exit 3 before stdin or any check, so capture falls back
	if rs, ok := refusedUpFrontFor(t); ok {
		return rs.RefuseCreate()
	}

	in, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return fmt.Errorf("reading the task from stdin: %w", err)
	}

	// the contract puts the title on the first line, so leading blanks go
	doc := strings.TrimLeft(string(in), " \t\r\n")
	if hostops.TaskTitle(doc) == "" {
		return errors.New("task create reads the task as markdown on stdin, `# <title>` on its first line")
	}

	doc = linkBodyAfterFor(ctx, t, doc, cmd.ErrWriter)

	nt, err := readNewTask(ctx, t, cmd, doc)
	if err != nil {
		return err
	}

	r, url, err := hostops.TasksFor(t).Create(ctx, cmd.ErrWriter, nt)
	if err != nil {
		return err
	}

	// the number exists only now, so the template goes on in a second write
	hostops.RetitleAfter(ctx, cmd.ErrWriter, t, r, "created")

	// the url too, so naming the new task costs no tracker call
	stack.RememberRefsFor(ctx, t, ledger.TaskRef{ID: r.ID, Title: docTitle(r.ID, doc), URL: url})

	_, err = fmt.Fprintln(cmd.Writer, r.ID)

	return err
}

// readNewTask reads the properties and resolves the parent and the body's
// After: base once, before any source sees them.
func readNewTask(ctx context.Context, t *target.Target, cmd *cli.Command, doc string) (hostops.NewTask, error) {
	p, err := readProps(cmd)
	if err != nil {
		return hostops.NewTask{}, err
	}

	nt := hostops.NewTask{Doc: doc, Props: p}

	for _, link := range []struct {
		flag, id string
		to       *ref.Ref
	}{
		{"--parent", cmd.String("parent"), &nt.Parent},
		{"--after", hostops.AfterIn(doc), &nt.After},
	} {
		if strings.TrimSpace(link.id) == "" {
			continue
		}

		r, err := t.Reference(ctx, link.id)
		if err != nil {
			return hostops.NewTask{}, fmt.Errorf("%s %s: %w", link.flag, link.id, err)
		}

		*link.to = r
	}

	return nt, nil
}
