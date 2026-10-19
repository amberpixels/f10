package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

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
				Action:    runTaskRead,
			},
			{
				Name:      "open",
				Usage:     "open the task in the browser",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{printFlag()},
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

	r, doc, err := readTask(ctx, t, cmd.Args().First())
	if err != nil {
		// an agent reading this has the project's fetch adapter as its
		// fallback, and learns of it here, when it applies, not in every prompt
		return fmt.Errorf("%w\nfetch the task through the project's fetch adapter instead "+
			"(project.md, Tracker)", err)
	}

	// a person at a terminal opens the links themselves and would wait on
	// the downloads; an agent gets local files in place of urls that expire
	if !isTerminal(cmd.Writer) {
		dir := filepath.Join(t.lay.StorageRoot, "images", strings.ToUpper(r.ID))

		doc, err = withImages(ctx, doc, dir, images.Downloader{Authorize: githubAuth(ctx, t)})
		if err != nil {
			return err
		}
	}

	return writeDoc(ctx, cmd.Writer, t.dir, doc)
}

// readTask is the task as markdown: the driver's `read`, else the host's
// issue with its comments.
func readTask(ctx context.Context, t *target, token string) (ref.Ref, string, error) {
	r, err := t.reference(ctx, token)
	if err != nil {
		return r, "", err
	}

	if d := t.driver(); d != nil {
		doc, err := d.Run(ctx, driver.VerbRead, r.Number)
		if err == nil {
			return r, doc, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return r, "", err
		}
	}

	h, err := t.issues()
	if err != nil {
		return r, "", err
	}

	doc, err := h.issueDoc(ctx, r.ID, r.Number)

	return r, doc, err
}

// withImages downloads every image doc links into dir and ends doc with
// the footer naming them; a doc with no images comes back as it was.
func withImages(ctx context.Context, doc, dir string, d images.Downloader) (string, error) {
	links := images.Links(doc)
	if len(links) == 0 {
		return doc, nil
	}

	results, err := d.Download(ctx, dir, links)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(doc, "\n") + "\n\n" + images.Footer(results), nil
}

// githubAuth adds the gh token to image requests for GitHub's own hosts,
// where a private repo's attachments sit; nothing else ever sees it. The
// token is read once, on the first such request, and a failed read leaves
// the request bare, so its error names the image.
func githubAuth(ctx context.Context, t *target) func(*http.Request) {
	h, err := t.host()
	if err != nil || h.glab() {
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
			out, err := h.run(ctx, "auth", "token")
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
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	if d := t.driver(); d != nil {
		url, err := d.Run(ctx, driver.VerbURL, r.Number)
		if err == nil {
			return follow(ctx, cmd.Writer, t.dir, strings.TrimSpace(url), cmd.Bool("print"))
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

	return follow(ctx, cmd.Writer, t.dir, url, cmd.Bool("print"))
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

	return writeRows(cmd.Writer, rows)
}

// A row is what a search returns: the four fields f10 relies on. A driver
// may carry more, and f10 neither requires nor reads them.
type row struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
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

	return h.searchIssues(ctx, query, t.eff.IDPrefix)
}

// writeRows applies the same rule reading a task does: a terminal gets a
// table, anything else gets the JSON. f10 prints rows and never picks a
// winner among them - choosing is fzf's job in a shell and the agent's in a
// turn, and a picker here would be wrong for one of the two.
func writeRows(w io.Writer, rows []row) error {
	if !isTerminal(w) {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}

		return nil
	}

	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no matches")

		return err
	}

	idW, statusW := 0, 0
	for _, r := range rows {
		idW = max(idW, len(r.ID))
		statusW = max(statusW, len(r.Status))
	}

	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%-*s  %-*s  %s\n", idW, r.ID, statusW, r.Status, r.Title); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}
	}

	return nil
}
