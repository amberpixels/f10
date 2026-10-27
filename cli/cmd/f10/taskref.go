package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// Text that leaves the machine names another task by its title and url.
// `task ref` hands them out from the ledger's task refs, which every
// tracker read f10 makes fills, and asks the tracker only on a miss.

// projectOf is the ledger's project id for dir. A var, so tests that fake
// git leave the task refs alone.
var projectOf = func(ctx context.Context, dir string) string { return ledger.Where(ctx, dir).Project }

// rememberRefs records refs as this project's; it never fails its caller.
func (t *target) rememberRefs(ctx context.Context, refs ...ledger.TaskRef) {
	if len(refs) == 0 {
		return
	}

	ledger.RememberRefs(ctx, projectOf(ctx, t.dir), refs...)
}

func rowRefs(rows []row) []ledger.TaskRef {
	refs := make([]ledger.TaskRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, ledger.TaskRef{ID: r.ID, Title: cleanTitle(r.ID, r.Title), URL: r.URL})
	}

	return refs
}

func childRefs(rows []childRow) []ledger.TaskRef {
	refs := make([]ledger.TaskRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, ledger.TaskRef{ID: r.ID, Title: cleanTitle(r.ID, r.Title), URL: r.URL})
	}

	return refs
}

// cleanTitle is title without a heading mark or a leading copy of the id,
// so `# FT-12: Add it` from a driver and `Add it` from a host store alike.
func cleanTitle(id, title string) string {
	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(title), "# "))

	if id != "" && len(title) > len(id) && strings.EqualFold(title[:len(id)], id) {
		rest := title[len(id):]
		if r, ok := strings.CutPrefix(rest, ":"); ok {
			return strings.TrimSpace(r)
		}

		if rest[0] == ' ' {
			return strings.TrimSpace(rest)
		}
	}

	return title
}

// docTitle is the title on a task document's first line, `# <title>`.
func docTitle(id, doc string) string {
	return cleanTitle(id, taskTitle(strings.TrimLeft(doc, " \t\r\n")))
}

// mdLink is the task as a markdown link, brackets in the title escaped so
// the link text always ends where the link does.
func mdLink(id, title, url string) string {
	title = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(title)

	return "[" + id + ": " + title + "](" + url + ")"
}

func runTaskRef(ctx context.Context, cmd *cli.Command) error {
	tokens := cmd.Args().Slice()
	if len(tokens) == 0 {
		return errors.New("ref needs a task id")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	md := cmd.Bool("md")
	refs := make([]ref.Ref, len(tokens))

	for i, token := range tokens {
		// a bundle is several tasks, not one reference
		if strings.Contains(token, ref.Separator) {
			continue
		}

		if r, err := t.reference(ctx, token); err == nil {
			refs[i] = r
		}
	}

	known := t.taskRefs(ctx, refs, md, cmd.Bool("fresh"), cmd.ErrWriter)

	for i, token := range tokens {
		if _, err := fmt.Fprintln(cmd.Writer, refLine(token, refs[i].ID, known[refs[i].ID], md)); err != nil {
			return err
		}
	}

	return nil
}

// refLine is one task as `ref` prints it: `<id> (<title>)`, or the link
// with md, else the id bare, else the token as typed when it named no task.
func refLine(token, id string, k ledger.TaskRef, md bool) string {
	switch {
	case id == "":
		return token
	case k.Title == "", md && k.URL == "":
		return id
	case md:
		return mdLink(id, k.Title, k.URL)
	}

	return id + " (" + k.Title + ")"
}

// taskRefs is the title and url of each task in refs, by id: from the
// ledger, else one tracker call per task, whose answers the ledger keeps.
// A task with md needs a url too; fresh asks the tracker for every task.
// A tracker that cannot answer leaves the task out, with a note on warn.
func (t *target) taskRefs(
	ctx context.Context,
	refs []ref.Ref,
	md, fresh bool,
	warn io.Writer,
) map[string]ledger.TaskRef {
	project := projectOf(ctx, t.dir)

	var ids []string

	for _, r := range refs {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}

	known := map[string]ledger.TaskRef{}
	if !fresh && project != "" {
		maps.Copy(known, ledger.LookupRefs(ctx, project, ids))
	}

	var read []ledger.TaskRef

	for _, r := range refs {
		if k, ok := known[r.ID]; r.ID == "" || (ok && k.Title != "" && (!md || k.URL != "")) {
			continue
		}

		k, err := t.lookupRef(ctx, r)
		if err != nil {
			fmt.Fprintf(warn, "f10: %s: %v\n", r.ID, err)
			delete(known, r.ID)

			continue
		}

		known[r.ID] = k
		read = append(read, k)
	}

	if project != "" {
		ledger.RememberRefs(ctx, project, read...)
	}

	return known
}

// lookupRef asks the tracker for one task's title and url: the driver's
// `read` and `url`, else the host's issue.
//
//nolint:lostfield // a lookup by id, not a conversion
func (t *target) lookupRef(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	if d := t.driver(); d != nil {
		doc, err := d.Run(ctx, driver.VerbRead, r.Number)
		if err == nil {
			k := ledger.TaskRef{ID: r.ID, Title: docTitle(r.ID, doc)}
			if url, err := d.Run(ctx, driver.VerbURL, r.Number); err == nil {
				k.URL = strings.TrimSpace(url)
			}

			if k.Title == "" {
				return k, errors.New("the driver's read has no `# <title>` line")
			}

			return k, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return ledger.TaskRef{}, err
		}
	}

	h, err := t.issues(driver.VerbRead)
	if err != nil {
		return ledger.TaskRef{}, err
	}

	iss, err := h.issue(ctx, r.Number, "title,url")
	if err != nil {
		return ledger.TaskRef{}, err
	}

	if err := iss.notPull(r.Number); err != nil {
		return ledger.TaskRef{}, err
	}

	if iss.Title == "" {
		return ledger.TaskRef{}, fmt.Errorf("%s returned no title for issue %s", h.Name, r.Number)
	}

	return ledger.TaskRef{ID: r.ID, Title: cleanTitle(r.ID, iss.Title), URL: iss.url()}, nil
}

// linkBodyAfter turns the body's bare `After: <id>` line into a link titled
// with the base task, so a reader on the tracker sees what it waits for.
// A base f10 cannot title stays bare: create never fails over a link.
func (t *target) linkBodyAfter(ctx context.Context, doc string, warn io.Writer) string {
	id := afterIn(doc)
	if id == "" {
		return doc
	}

	r, err := t.reference(ctx, id)
	if err != nil {
		return doc
	}

	k := t.taskRefs(ctx, []ref.Ref{r}, true, false, warn)[r.ID]
	if k.Title == "" || k.URL == "" {
		return doc
	}

	return linkAfter(doc, mdLink(r.ID, k.Title, k.URL))
}
