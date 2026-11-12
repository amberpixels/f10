package hostops

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// OneIDHeading collapses `# <id>: <id>: <title>`, the id a driver adds over
// the one tracker.title wrote, to one id.
func OneIDHeading(doc string, r ref.Ref, titles tasktitle.Template) string {
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

// A Row holds the fields f10 relies on; a driver may carry more, which f10
// neither requires nor reads.
type Row struct {
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

// StripTitles is for driver rows; the host's come stripped from ListIssues.
func StripTitles(rows []Row, titles tasktitle.Template) []Row {
	for i, r := range rows {
		n := strings.TrimPrefix(r.ID[strings.LastIndexByte(r.ID, '-')+1:], "#")
		rows[i].Title = titles.Strip(r.Title, r.ID, n)
	}

	return rows
}

// RetitleAfter writes tracker.title onto an issue just written, since the
// template needs the number and a driver has no title verb; the write
// stands, so a failure is a warning.
func RetitleAfter(ctx context.Context, warnTo io.Writer, t *target.Target, r ref.Ref, verb string) {
	if t.Effective.TitleTemplate == "" || !t.HostIssues() {
		return
	}

	if err := retitleOne(ctx, t, r.Number); err != nil {
		ui.Warnf(warnTo, "%s was %s, but its title was not written through tracker.title: %v; "+
			"run `f10 task retitle %s`", r.ID, verb, err, r.ID)
	}
}

func retitleOne(ctx context.Context, t *target.Target, number string) error {
	h, err := HostOf(t)
	if err != nil {
		return err
	}

	changes, err := h.TitleChanges(ctx, number)
	if err != nil || len(changes) == 0 {
		return err
	}

	return h.SetTitle(ctx, number, changes[0].To)
}

func TaskTitle(doc string) string {
	title, ok := strings.CutPrefix(strings.TrimSpace(textx.FirstLine(doc)), "# ")
	if !ok {
		return ""
	}

	return strings.TrimSpace(title)
}
