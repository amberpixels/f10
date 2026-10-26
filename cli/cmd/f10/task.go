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
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The task noun goes through the project's driver when it has one, and
// through the host's issues when it does not - so a repo with no .f10/ at
// all still answers, and a repo whose tracker has no CLI answers the same
// way once it drops in a driver.
func taskCommand() *cli.Command {
	return &cli.Command{
		Name:  "task",
		Usage: "read, open or search the project's tasks",
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
				Usage:     "list the task's open subtasks in After: order (exit 3: the tracker has no parent link)",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "emit the rows as JSON"}},
				Action:    runTaskChildren,
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

	// a typed bundle reads every member, lead first; an id alone reads itself
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

// fetchFallback tells an agent reading a failed read that the project's
// fetch adapter is its fallback: here, when it applies, not in every prompt.
func fetchFallback(err error) error {
	return fmt.Errorf("%w\nfetch the task through the project's fetch adapter instead "+
		"(project.md, Tracker)", err)
}

// readDoc is the task as markdown: the driver's `read`, else the host's
// issue with its comments.
func readDoc(ctx context.Context, t *target, r ref.Ref) (string, error) {
	if d := t.driver(); d != nil {
		doc, err := d.Run(ctx, driver.VerbRead, r.Number)
		if err == nil {
			return doc, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return "", err
		}
	}

	h, err := t.issues()
	if err != nil {
		return "", err
	}

	return h.issueDoc(ctx, r.ID, r.Number)
}

// withImages downloads every image doc links into dir and ends doc with
// the footer naming them; a doc with no images comes back as it was. A dir
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

// imagesDir is where a task's images land: <storage root>/images/<ID>.
// In-repo storage under stealth keeps .f10/ in info/exclude first, since
// a read may be the first thing to create it.
func (t *target) imagesDir(ctx context.Context, id string) string {
	if t.sharedPlansDir() == "" && t.eff.Visibility == "stealth" {
		_, _ = excludeF10(ctx, t.lay.CheckoutRoot)
	}

	return filepath.Join(t.lay.StorageRoot, "images", strings.ToUpper(id))
}

// githubAuth adds the gh token to image requests for GitHub's own hosts,
// where a private repo's attachments sit; nothing else ever sees it. The
// token is read once, on the first such request, and a failed read leaves
// the request bare, so its error names the image.
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

	h, err := t.issues()
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

// taskListURL is the tracker's task list: the driver's index when it has
// one, else the host's issues page.
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

	h, err := t.issues()
	if err != nil {
		return "", err
	}

	return h.issuesURL(ctx)
}

// writeTaskList is `task open --print` with no id: the first page of open
// tasks, priorities settled (warnings to warn) and ranked, written to w.
func writeTaskList(ctx context.Context, t *target, w, warn io.Writer) error {
	rows, err := taskRows(ctx, t, issuePage)
	if err != nil {
		return err
	}

	return writeRows(w, rankRows(settlePriorities(rows, warn)), "no open tasks")
}

// taskRows is the tracker's open tasks: the driver's list when it has one,
// else up to limit of the host's open issues.
func taskRows(ctx context.Context, t *target, limit int) ([]row, error) {
	if d := t.driver(); d != nil {
		out, err := d.Run(ctx, driver.VerbList)
		if err == nil {
			var rows []row
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				return nil, fmt.Errorf("parsing driver list rows: %w", err)
			}

			return rows, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}
	}

	h, err := t.issues()
	if err != nil {
		return nil, err
	}

	return h.listIssues(ctx, "", t.eff.IDPrefix, t.eff.PriorityLabels, limit)
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

// A row is what a search returns: the four fields f10 relies on, plus an
// optional priority from the contract's vocabulary. A driver may carry more,
// and f10 neither requires nor reads them.
type row struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status,omitempty"`
	URL      string `json:"url,omitempty"`
	Priority string `json:"priority,omitempty"`
	After    string `json:"after,omitempty"`    // the base its body's After: line names, where the source gives one
	Body     string `json:"body,omitempty"`     // read for its After: line; never printed
	Assignee string `json:"assignee,omitempty"` // the tracker's assignee, where the source gives one
	// Created is when the tracker created the task, where the source says.
	Created time.Time `json:"created,omitzero"`
}

// settlePriorities keeps each row's priority in its canonical form and
// clears one outside the vocabulary, warning once per distinct value: a
// driver emitting its tracker's own word does so on every row of the page.
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

// rankRows orders rows by priority, most urgent first and unranked last,
// keeping the tracker's order inside a level.
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

			return rows, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}
	}

	h, err := t.issues()
	if err != nil {
		return nil, err
	}

	return h.listIssues(ctx, query, t.eff.IDPrefix, t.eff.PriorityLabels, issuePage)
}

// writeRows applies the same rule reading a task does: a terminal gets a
// table, anything else gets the JSON. f10 prints rows and never picks a
// winner among them - choosing is fzf's job in a shell and the agent's in a
// turn, and a picker here would be wrong for one of the two.
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

	for _, r := range rows {
		// ↗ marks a row that opens, as in the report blocks; it is one cell wide
		marker := "  "
		if r.URL != "" {
			marker = hyperlink(r.URL, "↗") + " "
		}

		id := hyperlink(r.URL, r.ID) + strings.Repeat(" ", idW-len(r.ID))
		title := hyperlink(r.URL, r.Title)

		// the priority column exists only when a row carries one
		priority := ""
		if priorityW > 0 {
			priority = fmt.Sprintf("%-*s  ", priorityW, r.Priority)
		}

		if _, err := fmt.Fprintf(w, "%s%s  %-*s  %s%s\n", marker, id, statusW, r.Status, priority, title); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}
	}

	return nil
}

// hyperlink wraps text in an OSC 8 link to url, which a terminal shows as
// text alone and one without support ignores; no url leaves text plain.
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}

	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
