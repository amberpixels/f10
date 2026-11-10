package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// Text that leaves the machine names another task by its title and url.
// `task ref` hands them out from the ledger's task refs, which every
// tracker read f10 makes fills, and asks the tracker only on a miss.

func docTitle(id, doc string) string {
	return tasktitle.Clean(hostops.TaskTitle(strings.TrimLeft(doc, " \t\r\n")), id)
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

	t, err := target.For(ctx, cmd)
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

		if r, err := t.Reference(ctx, token); err == nil {
			refs[i] = r
		}
	}

	known := statusview.TaskRefsFor(t, ctx, refs, md, cmd.Bool("fresh"), cmd.ErrWriter)

	for i, token := range tokens {
		if _, err := fmt.Fprintln(cmd.Writer, refLine(token, refs[i].ID, known[refs[i].ID], md)); err != nil {
			return err
		}
	}

	return nil
}

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

// linkBodyAfter links the body's `After: <id>` line to the base task, so a
// tracker reader sees what it waits for; a base f10 cannot title stays
// bare, since create never fails over a link.
func linkBodyAfterFor(ctx context.Context, t *target.Target, doc string, warn io.Writer) string {
	id := hostops.AfterIn(doc)
	if id == "" {
		return doc
	}

	r, err := t.Reference(ctx, id)
	if err != nil {
		return doc
	}

	k := statusview.TaskRefsFor(t, ctx, []ref.Ref{r}, true, false, warn)[r.ID]
	if k.Title == "" || k.URL == "" {
		return doc
	}

	return stack.LinkAfter(doc, mdLink(r.ID, k.Title, k.URL))
}
