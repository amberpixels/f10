package hostops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driver"
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

// MayExist follows every failed create but a driver's exit 4: the driver may
// have made the task before failing, so running create again could make it twice.
const MayExist = "the task may exist already: check the tracker before running create again"

// driverCreate returns the new task and its url, "" when the driver gives
// none; a declined create is driver.ErrUnsupported, unwrapped.
func driverCreate(
	ctx context.Context, warnTo io.Writer, t *target.Target, d *driver.Driver, doc string, args []string,
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
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, MayExist)
	}

	if warn != "" {
		fmt.Fprintln(warnTo, warn)
	}

	// the driver may print the number or the prefixed id; f10 prints the id
	line := strings.TrimSpace(out)
	if line == "" || strings.ContainsAny(line, " \t\n"+ref.Separator) {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q, not one task id\n%s", out, MayExist)
	}

	r, err := t.Reference(ctx, line)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q: %w\n%s", line, err, MayExist)
	}

	url, _ := d.Run(ctx, driver.VerbURL, r.Number)

	return r, strings.TrimSpace(url), nil
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
