package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// The task noun goes through the project's driver, else the host's issues:
// a repo with no .f10/ at all still answers, and one whose tracker has no
// CLI answers the same way once it drops in a driver.
func taskCommand() *cli.Command {
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
				Category: plumbingCategory,
				Usage: "print the task the branch names, else the one this session's run records " +
					"(exit 1: none)",
				Action: runTaskCurrent,
			},
			{
				Name:     "ref",
				Hidden:   true,
				Category: plumbingCategory,
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
				Category: plumbingCategory,
				Usage: "print a report's facts block for the tasks: one task's task, title and url rows, else a row " +
					"per task; from the titles f10 already read",
				ArgsUsage: "<task-id>...",
				Action:    runTaskReport,
			},
			{
				Name:     "stale",
				Hidden:   true,
				Category: plumbingCategory,
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
				Flags:     []cli.Flag{linkFlag(), printFlag()},
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
				Category:  plumbingCategory,
				Usage:     "list the task's open subtasks in After: order (exit 3: the tracker has no parent link)",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "emit the rows as JSON"},
					&cli.BoolFlag{Name: "open", Usage: "leave out finished rows too (merged, or the plan archived)"},
					&cli.BoolFlag{
						Name:  "plain",
						Usage: "print the tree, each task's url beneath it, even into a pipe (for a skill to relay)",
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
					&cli.StringFlag{Name: "priority", Usage: "one of " + strings.Join(driver.Priorities, ", ")},
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
				Usage: "set the task's status, priority, labels, assignee or a named property " +
					"(exit 3: this tracker takes no updates here)",
				ArgsUsage: "[task-id]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "status",
						Usage: "the status, in the tracker's own word (host issues: open or closed)",
					},
					&cli.StringFlag{Name: "priority", Usage: "one of " + strings.Join(driver.Priorities, ", ")},
					&cli.StringSliceFlag{Name: "label", Usage: "a label to add; repeat for several"},
					&cli.StringFlag{Name: "assignee", Usage: "who holds the task, in the tracker's own name for them"},
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
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	token := cmd.Args().First()

	r, err := t.reference(ctx, token)
	if err != nil {
		return fetchFallback(err)
	}

	members := []ref.Ref{r}
	if strings.Contains(token, ref.Separator) {
		members = r.MemberRefs()
	}

	docs := make([]string, 0, len(members))

	for _, m := range members {
		doc, err := readDoc(ctx, t, m)
		if err != nil {
			return fetchFallback(err)
		}

		// a person at a terminal opens the links themselves and would wait on
		// the downloads; an agent gets local files in place of urls that expire
		if !isTerminal(cmd.Writer) && !cmd.Bool("no-images") {
			doc = withImages(ctx, doc, t.imagesDir(ctx, m.ID), images.Downloader{Authorize: githubAuth(ctx, t)})
		}

		docs = append(docs, strings.TrimRight(doc, "\n"))
	}

	return writeDoc(ctx, cmd.Writer, t.dir, strings.Join(docs, "\n\n---\n\n"))
}

// runTaskCurrent names the task a bare skill call stands for, by the cascade
// a bare finish walks, so no skill parses a branch name itself.
func runTaskCurrent(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, "")
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

// readDoc feeds the ledger's task refs, so a later ref costs no tracker call.
func readDoc(ctx context.Context, t *target, r ref.Ref) (string, error) {
	if d := t.driver(); d != nil {
		doc, err := d.Run(ctx, driver.VerbRead, r.Number)
		if err == nil {
			doc = oneIDHeading(doc, r, t.eff.TitleTemplate)
			t.rememberRefs(ctx, ledger.TaskRef{ID: r.ID, Title: t.driverTitle(r, doc)})

			return doc, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return "", err
		}
	}

	h, err := t.issues(driver.VerbRead)
	if err != nil {
		return "", err
	}

	doc, iss, err := h.issueDoc(ctx, r.ID, r.Number, t.eff.StaleAfter)
	if err == nil {
		t.rememberRefs(
			ctx,
			ledger.TaskRef{ID: r.ID, Title: cleanTitle(r.ID, h.title(iss.Title, r.Number)), URL: iss.url()},
		)
	}

	return doc, err
}

// oneIDHeading collapses `# <id>: <id>: <title>`, the id a driver adds over
// the one tracker.title wrote, to one id.
func oneIDHeading(doc string, r ref.Ref, titles tasktitle.Template) string {
	first, rest, _ := strings.Cut(doc, "\n")

	heading, ok := strings.CutPrefix(first, "# ")
	if !ok {
		return doc
	}

	once := titles.Strip(heading, r.ID, r.Number)
	if twice := titles.Strip(once, r.ID, r.Number); once != heading && twice != once {
		first = "# " + r.ID + ": " + twice
	}

	if rest == "" && !strings.Contains(doc, "\n") {
		return first
	}

	return first + "\n" + rest
}

// withImages ends doc with a footer naming its downloaded images. A dir
// that cannot be written fails every image, never the read.
func withImages(ctx context.Context, doc, dir string, d images.Downloader) string {
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
func (t *target) imagesDir(ctx context.Context, id string) string {
	if !t.outOfTree() && t.eff.Visibility == "stealth" {
		_, _ = excludeF10(ctx, t.lay.CheckoutRoot)
	}

	return filepath.Join(t.lay.StorageRoot, "images", strings.ToUpper(id))
}

// githubAuth sends the gh token only to GitHub's own hosts over https,
// where a private repo's attachments sit. It is read once; a failed read
// leaves the request bare, so its error names the image.
func githubAuth(ctx context.Context, t *target) func(*http.Request) {
	h, err := t.host()
	if err != nil || h.GLab() {
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
			out, err := h.Run(ctx, "auth", "token")
			if err == nil {
				token = strings.TrimSpace(out)
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

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	// no task id is the list, never the branch's task: read is for that
	switch {
	case listing && cmd.Bool("print"):
		return writeTaskList(ctx, t, cmd.Writer, cmd.ErrWriter)
	case listing:
		url, err := taskListURL(ctx, t)
		if err != nil {
			return err
		}

		return follow(ctx, cmd.Writer, t.dir, url, cmd.Bool("link"))
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	if d := t.driver(); d != nil {
		url, err := d.Run(ctx, driver.VerbURL, r.Number)
		if err == nil {
			return follow(ctx, cmd.Writer, t.dir, strings.TrimSpace(url), cmd.Bool("link"))
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return err
		}
	}

	h, err := t.issues(driver.VerbURL)
	if err != nil {
		return err
	}

	// through the url, never `issue view --web`, which opens a pull request
	// that shares the number
	url, err := h.issueURL(ctx, r.Number)
	if err != nil {
		return err
	}

	return follow(ctx, cmd.Writer, t.dir, url, cmd.Bool("link"))
}

func taskListURL(ctx context.Context, t *target) (string, error) {
	if d := t.driver(); d != nil {
		url, err := d.Run(ctx, driver.VerbIndex)
		if err == nil {
			return strings.TrimSpace(url), nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return "", err
		}
	}

	h, err := t.issues(driver.VerbIndex)
	if err != nil {
		return "", err
	}

	return h.issuesURL(ctx)
}

func writeTaskList(ctx context.Context, t *target, w, warn io.Writer) error {
	rows, err := taskRows(ctx, t, issuePage)
	if err != nil {
		return err
	}

	return writeRows(w, rankRows(settlePriorities(rows, warn)), "no open tasks")
}

func taskRows(ctx context.Context, t *target, limit int) ([]row, error) {
	if d := t.driver(); d != nil {
		out, err := d.Run(ctx, driver.VerbList)
		if err == nil {
			var rows []row
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				return nil, fmt.Errorf("parsing driver list rows: %w", err)
			}

			rows = stripTitles(rows, t.eff.TitleTemplate)
			t.rememberRefs(ctx, rowRefs(rows)...)

			return rows, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}
	}

	h, err := t.issues(driver.VerbList)
	if err != nil {
		return nil, err
	}

	rows, err := h.listIssues(ctx, "", t.eff.IDPrefix, t.eff.PriorityLabels, limit)
	t.rememberRefs(ctx, rowRefs(rows)...)

	return rows, err
}

func runTaskSearch(ctx context.Context, cmd *cli.Command) error {
	query := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
	if query == "" {
		return errors.New("search needs a query")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	rows, err := searchRows(ctx, t, query)
	if err != nil {
		return err
	}

	// search keeps the tracker's relevance order; only the open list ranks
	return writeRows(cmd.Writer, settlePriorities(rows, cmd.ErrWriter), "no matches")
}

// A row holds the fields f10 relies on; a driver may carry more, which f10
// neither requires nor reads.
type row struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Status   string    `json:"status,omitempty"`
	URL      string    `json:"url,omitempty"`
	Priority string    `json:"priority,omitempty"`
	After    string    `json:"after,omitempty"`    // the base its body's After: line names, where the source gives one
	Body     string    `json:"body,omitempty"`     // read for its After: line; never printed
	Assignee string    `json:"assignee,omitempty"` // the tracker's assignee, where the source gives one
	Created  time.Time `json:"created,omitzero"`
}

// settlePriorities warns once per distinct unknown value: a driver emitting
// its tracker's own word does so on every row of the page.
func settlePriorities(rows []row, warn io.Writer) []row {
	var (
		unknown []string
		count   = map[string]int{}
	)

	for i, r := range rows {
		if r.Priority == "" {
			continue
		}

		if rank, ok := driver.PriorityRank(r.Priority); ok {
			rows[i].Priority = driver.Priorities[rank]

			continue
		}

		if count[r.Priority] == 0 {
			unknown = append(unknown, r.Priority)
		}

		count[r.Priority]++
		rows[i].Priority = ""
	}

	for _, v := range unknown {
		fmt.Fprintf(warn, "f10: priority %q on %d row(s) is not one of %s; ignored\n",
			v, count[v], strings.Join(driver.Priorities, ", "))
	}

	return rows
}

// rankRows is stable, so the tracker's order holds inside a level.
func rankRows(rows []row) []row {
	rank := func(r row) int {
		if i, ok := driver.PriorityRank(r.Priority); ok {
			return i
		}

		return len(driver.Priorities)
	}

	slices.SortStableFunc(rows, func(a, b row) int { return rank(a) - rank(b) })

	return rows
}

func searchRows(ctx context.Context, t *target, query string) ([]row, error) {
	if d := t.driver(); d != nil {
		out, err := d.Run(ctx, driver.VerbSearch, query)
		if err == nil {
			var rows []row
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				return nil, fmt.Errorf("parsing driver search rows: %w", err)
			}

			rows = stripTitles(rows, t.eff.TitleTemplate)
			t.rememberRefs(ctx, rowRefs(rows)...)

			return rows, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}
	}

	h, err := t.issues(driver.VerbSearch)
	if err != nil {
		return nil, err
	}

	rows, err := h.listIssues(ctx, query, t.eff.IDPrefix, t.eff.PriorityLabels, issuePage)
	t.rememberRefs(ctx, rowRefs(rows)...)

	return rows, err
}

// stripTitles is for driver rows; the host's come stripped from listIssues.
func stripTitles(rows []row, titles tasktitle.Template) []row {
	for i, r := range rows {
		n := strings.TrimPrefix(r.ID[strings.LastIndexByte(r.ID, '-')+1:], "#")
		rows[i].Title = titles.Strip(r.Title, r.ID, n)
	}

	return rows
}

// writeRows applies the rule reading a task does: a terminal gets a table,
// anything else the JSON. f10 never picks a winner among rows: choosing is
// fzf's job in a shell and the agent's in a turn.
func writeRows(w io.Writer, rows []row, none string) error {
	if !isTerminal(w) {
		for i := range rows {
			rows[i].Body = ""
		}

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}

		return nil
	}

	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, none)

		return err
	}

	idW, statusW, priorityW := 0, 0, 0
	for _, r := range rows {
		idW = max(idW, len(r.ID))
		statusW = max(statusW, len(r.Status))
		priorityW = max(priorityW, len(r.Priority))
	}

	links := linksOn(true)

	for _, r := range rows {
		// ↗ marks a row that opens, as in the report blocks; it is one cell wide
		marker := "  "
		if r.URL != "" {
			marker = hyperlink(r.URL, "↗") + " "
		}

		links.Known("", r.ID, r.URL)
		shown := padRight(links.ID("", r.ID), idW)
		title := hyperlink(r.URL, r.Title)

		priority := ""
		if priorityW > 0 {
			priority = fmt.Sprintf("%-*s  ", priorityW, r.Priority)
		}

		if _, err := fmt.Fprintf(
			w,
			"%s%s  %-*s  %s%s\n",
			marker,
			shown,
			statusW,
			r.Status,
			priority,
			title,
		); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}
	}

	return nil
}

// exitNoCreate is `task create`'s "no driver creates tasks here": a normal
// answer the capture step reads as "use the project's Create adapter".
const exitNoCreate = 3

// mayExist follows every failed create but a driver's exit 4: the driver may
// have made the task before failing, so running create again could make it twice.
const mayExist = "the task may exist already: check the tracker before running create again"

// runTaskCreate sends the task on stdin to the driver's create, else makes it
// an issue on the host. f10 never retries it, since a failed write is an
// unknown outcome.
func runTaskCreate(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	// nowhere to create answers exit 3 before stdin or any check, so capture falls back
	d := t.driver()
	if d == nil && !t.createsOnHost() {
		return cli.Exit("f10: no driver creates tasks here (looked for "+strings.Join(t.driverPaths(), ", ")+
			"), and no host issues take them; "+
			"create it through the project's Create adapter (project.md, Tracker)", exitNoCreate)
	}

	in, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return fmt.Errorf("reading the task from stdin: %w", err)
	}

	// the contract puts the title on the first line, so leading blanks go
	doc := strings.TrimLeft(string(in), " \t\r\n")
	if taskTitle(doc) == "" {
		return errors.New("task create reads the task as markdown on stdin, `# <title>` on its first line")
	}

	doc = t.linkBodyAfter(ctx, doc, cmd.ErrWriter)

	args, err := createArgs(ctx, t, cmd, doc)
	if err != nil {
		return err
	}

	var r ref.Ref

	created := ledger.TaskRef{}

	if d != nil {
		r, created.URL, err = driverCreate(ctx, cmd.ErrWriter, t, d, doc, args)
		if errors.Is(err, driver.ErrUnsupported) && !t.createsOnHost() {
			return cli.Exit("f10: the driver at "+d.Path+" declines `create`; "+
				"create the task through the project's Create adapter (project.md, Tracker)", exitNoCreate)
		}
	}

	if d == nil || errors.Is(err, driver.ErrUnsupported) {
		var p props
		if p, err = readProps(cmd); err == nil {
			r, created.URL, err = hostCreate(ctx, cmd.ErrWriter, t, doc, p, cmd.String("parent"))
		}
	}

	if err != nil {
		return err
	}

	// the number exists only now, so the template goes on in a second write
	retitleAfter(ctx, cmd.ErrWriter, t, r, "created")

	// the url too, so naming the new task costs no tracker call
	created.ID, created.Title = r.ID, docTitle(r.ID, doc)
	t.rememberRefs(ctx, created)

	_, err = fmt.Fprintln(cmd.Writer, r.ID)

	return err
}

// driverCreate returns the new task and its url, "" when the driver gives
// none; a declined create is driver.ErrUnsupported, unwrapped.
func driverCreate(
	ctx context.Context, warnTo io.Writer, t *target, d *driver.Driver, doc string, args []string,
) (ref.Ref, string, error) {
	out, warn, err := d.RunInput(ctx, doc, driver.VerbCreate, args...)
	if errors.Is(err, driver.ErrUnsupported) {
		return ref.Ref{}, "", err
	}

	// the driver rejected the input before writing, so running it again is safe
	if errors.Is(err, driver.ErrNotWritten) {
		return ref.Ref{}, "", err
	}

	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, mayExist)
	}

	if warn != "" {
		fmt.Fprintln(warnTo, warn)
	}

	// the driver may print the number or the prefixed id; f10 prints the id
	line := strings.TrimSpace(out)
	if line == "" || strings.ContainsAny(line, " \t\n"+ref.Separator) {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q, not one task id\n%s", out, mayExist)
	}

	r, err := t.reference(ctx, line)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q: %w\n%s", line, err, mayExist)
	}

	url, _ := d.Run(ctx, driver.VerbURL, r.Number)

	return r, strings.TrimSpace(url), nil
}

// retitleAfter writes tracker.title onto an issue just written, since the
// template needs the number and a driver has no title verb; the write
// stands, so a failure is a warning.
func retitleAfter(ctx context.Context, warnTo io.Writer, t *target, r ref.Ref, verb string) {
	if t.eff.TitleTemplate == "" || !t.hostIssues() {
		return
	}

	if err := retitleOne(ctx, t, r.Number); err != nil {
		fmt.Fprintf(warnTo, "f10: %s was %s, but its title was not written through tracker.title: %v; "+
			"run `f10 task retitle %s`\n", r.ID, verb, err, r.ID)
	}
}

func retitleOne(ctx context.Context, t *target, number string) error {
	h, err := t.host()
	if err != nil {
		return err
	}

	changes, err := h.titleChanges(ctx, number)
	if err != nil || len(changes) == 0 {
		return err
	}

	return h.setTitle(ctx, number, changes[0].to)
}

func taskTitle(doc string) string {
	line, _, _ := strings.Cut(doc, "\n")

	title, ok := strings.CutPrefix(strings.TrimSpace(line), "# ")
	if !ok {
		return ""
	}

	return strings.TrimSpace(title)
}

// createArgs passes the parent and the body's After: base as numbers, as
// every driver verb's argument is.
func createArgs(ctx context.Context, t *target, cmd *cli.Command, doc string) ([]string, error) {
	props, err := readProps(cmd)
	if err != nil {
		return nil, err
	}

	args := props.args()

	for _, link := range []struct{ flag, id string }{
		{"--parent", cmd.String("parent")},
		{"--after", afterIn(doc)},
	} {
		if strings.TrimSpace(link.id) == "" {
			continue
		}

		r, err := t.reference(ctx, link.id)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", link.flag, link.id, err)
		}

		args = append(args, link.flag, r.Number)
	}

	return args, nil
}
