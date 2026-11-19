// Package taskwrite creates and reads tracker tasks for a project: what f10
// task create and task read do, for the command and the daemon alike.
package taskwrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/reflink"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A New is a task to create: its markdown, `# <title>` first, the
// properties to set, and the parent it is a subtask of.
type New struct {
	Doc    string
	Props  hostops.Props
	Parent string
}

// RefusedUpFront is the source that creates nothing in t's project, which
// answers before anything is read, so capture falls back.
func RefusedUpFront(t *target.Target) (tasksource.RefusedSource, bool) {
	s, ok := tasksource.TasksFor(t).(tasksource.RefusedSource)

	return s, ok
}

// Create sends the task to the driver's create, else makes it an issue on
// the host, and returns it with its url. It is never retried, since a
// failed write is an unknown outcome.
func Create(ctx context.Context, warn io.Writer, t *target.Target, n New) (ref.Ref, string, error) {
	if rs, ok := RefusedUpFront(t); ok {
		return ref.Ref{}, "", rs.RefuseCreate()
	}

	// the contract puts the title on the first line, so leading blanks go
	doc := strings.TrimLeft(n.Doc, " \t\r\n")
	if hostops.TaskTitle(doc) == "" {
		return ref.Ref{}, "", errors.New("task create reads the task as markdown, `# <title>` on its first line")
	}

	doc = LinkBodyAfter(ctx, t, doc, warn)

	// the title line renders nothing, so only the body below it is linked
	if title, body, ok := strings.Cut(doc, "\n"); ok {
		doc = title + "\n" + LinkMentions(ctx, t, body, warn)
	}

	nt := tasksource.NewTask{Doc: doc, Props: n.Props}

	for _, link := range []struct {
		flag, id string
		to       *ref.Ref
	}{
		{"--parent", n.Parent, &nt.Parent},
		{"--after", hostops.AfterIn(doc), &nt.After},
	} {
		if strings.TrimSpace(link.id) == "" {
			continue
		}

		r, err := t.Reference(ctx, link.id)
		if err != nil {
			return ref.Ref{}, "", fmt.Errorf("%s %s: %w", link.flag, link.id, err)
		}

		*link.to = r
	}

	r, url, err := tasksource.TasksFor(t).Create(ctx, warn, nt)
	if err != nil {
		return ref.Ref{}, "", err
	}

	// the number exists only now, so the template goes on in a second write
	hostops.RetitleAfter(ctx, warn, t, r, "created")

	// the url too, so naming the new task costs no tracker call
	stack.RememberRefsFor(ctx, t, ledger.TaskRef{ID: r.ID, Title: DocTitle(r.ID, doc), URL: url})

	return r, url, nil
}

// Read is the task's text as f10 renders it, feeding the ledger's task
// refs so a later ref costs no tracker call.
func Read(ctx context.Context, t *target.Target, r ref.Ref) (string, error) {
	doc, k, err := tasksource.TasksFor(t).Read(ctx, r)
	if err == nil {
		stack.RememberRefsFor(ctx, t, k)
	}

	return doc, err
}

// DocTitle is the title a task's markdown opens with, cleaned of its id.
func DocTitle(id, doc string) string {
	return tasktitle.Clean(hostops.TaskTitle(strings.TrimLeft(doc, " \t\r\n")), id)
}

// MdLink is reflink.MarkdownLink.
func MdLink(id, title, url string) string { return reflink.MarkdownLink(id, title, url) }

// LinkMentions links the first bare mention of each task id in a markdown
// body that renders links, from the titles f10 already read.
func LinkMentions(ctx context.Context, t *target.Target, body string, warn io.Writer) string {
	out, _ := LinkWith(ctx, t, reflink.Markdown, body, warn)

	return out
}

// LinkWith runs scan once to find the bare ids, resolves them in one lookup,
// and runs it again to link them; it names each linked id on warn. An id it
// cannot title stays bare.
func LinkWith(
	ctx context.Context,
	t *target.Target,
	scan reflink.Scan,
	text string,
	warn io.Writer,
) (string, []reflink.Mention) {
	prefix := t.Effective.IDPrefix

	_, bare := scan(text, prefix, reflink.None)
	if len(bare) == 0 {
		return text, nil
	}

	refs := make([]ref.Ref, 0, len(bare))

	for _, m := range bare {
		if r, err := t.Reference(ctx, m.ID); err == nil {
			refs = append(refs, r)
		}
	}

	known := statusview.TaskRefsFor(t, ctx, refs, true, false, warn)

	out, mentions := scan(text, prefix, func(id string) (string, string, bool) {
		k, ok := known[id]

		return k.Title, k.URL, ok
	})

	var ids []string

	for _, m := range mentions {
		if m.Linked {
			ids = append(ids, m.ID)
		}
	}

	if len(ids) > 0 {
		ui.Warnf(warn, "linked %s", strings.Join(ids, ", "))
	}

	return out, mentions
}

// LinkBodyAfter turns the body's `After: <id>` line into a link titled with
// the base task, when its title and url are known.
func LinkBodyAfter(ctx context.Context, t *target.Target, doc string, warn io.Writer) string {
	id := hostops.AfterIn(doc)
	if id == "" {
		return doc
	}

	r, err := t.Reference(ctx, id)
	if err != nil {
		return doc
	}

	k := statusview.TaskRefsFor(t, ctx, []ref.Ref{r}, true, false, warn)[r.ID]
	if k.Closed {
		ui.Warnf(warn, "the After: line names a closed task; build on it as shipped work, not as a dependency")
	}

	if k.Title == "" || k.URL == "" {
		return doc
	}

	return stack.LinkAfter(doc, MdLink(r.ID, k.Title, k.URL))
}
