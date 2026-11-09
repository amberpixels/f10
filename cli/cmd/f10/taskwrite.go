package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The task's write verbs go to the driver, else to the host's issues; a
// tracker that is neither answers exit 3, which a ship run reads as "report
// the shipment locally only".

// mayPosted follows every failed comment: the tracker may have taken it
// before failing, so posting again could post it twice.
const mayPosted = "the comment may have been posted: check the task before running comment again"

type props struct {
	status, priority, assignee string
	labels, set                []string
}

func readProps(cmd *cli.Command) (props, error) {
	var p props

	p.status = strings.TrimSpace(cmd.String("status"))

	if raw := cmd.String("priority"); raw != "" {
		rank, ok := driver.PriorityRank(raw)
		if !ok {
			return props{}, fmt.Errorf("priority %q is not one of %s", raw, strings.Join(driver.Priorities, ", "))
		}

		p.priority = driver.Priorities[rank]
	}

	for _, l := range cmd.StringSlice("label") {
		if l = strings.TrimSpace(l); l != "" {
			p.labels = append(p.labels, l)
		}
	}

	if cmd.IsSet("assignee") {
		p.assignee = strings.TrimSpace(cmd.String("assignee"))
	}

	if cmd.IsSet("set") {
		for _, kv := range cmd.StringSlice("set") {
			key, _, ok := strings.Cut(kv, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return props{}, fmt.Errorf("--set %q is not key=value", kv)
			}

			p.set = append(p.set, kv)
		}
	}

	return p, nil
}

func (p props) empty() bool {
	return p.status == "" && p.priority == "" && p.assignee == "" && p.labels == nil && p.set == nil
}

func (p props) args() []string {
	var args []string

	if p.status != "" {
		args = append(args, "--status", p.status)
	}

	if p.priority != "" {
		args = append(args, "--priority", p.priority)
	}

	for _, l := range p.labels {
		args = append(args, "--label", l)
	}

	if p.assignee != "" {
		args = append(args, "--assignee", p.assignee)
	}

	for _, kv := range p.set {
		args = append(args, "--set", kv)
	}

	return args
}

func runTaskComment(ctx context.Context, cmd *cli.Command) error {
	t, r, d, err := writeTarget(ctx, cmd, driver.VerbComment)
	if err != nil {
		return err
	}

	in, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return fmt.Errorf("reading the comment from stdin: %w", err)
	}

	body := strings.TrimSpace(string(in))
	if body == "" {
		return errors.New("task comment reads the comment as markdown on stdin, and it was empty")
	}

	url, err := postComment(ctx, cmd.ErrWriter, t, d, r, body)
	if err != nil {
		return err
	}

	if url != "" {
		_, err = fmt.Fprintln(cmd.Writer, url)
	}

	return err
}

func postComment(
	ctx context.Context,
	warnTo io.Writer,
	t *target,
	d *driver.Driver,
	r ref.Ref,
	body string,
) (string, error) {
	if d != nil {
		out, warn, err := d.RunInput(ctx, body, driver.VerbComment, r.Number)
		if err == nil {
			if warn != "" {
				fmt.Fprintln(warnTo, warn)
			}

			return strings.TrimSpace(out), nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return "", fmt.Errorf("%w\n%s", err, mayPosted)
		}
	}

	h, err := writeHost(t, d, driver.VerbComment)
	if err != nil {
		return "", err
	}

	url, err := h.CommentIssue(ctx, r.Number, body)
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, mayPosted)
	}

	return url, nil
}

// runTaskUpdate takes absolute values, so a rerun after a failure is safe.
// The parent link is a write of its own, after the properties, so a tracker
// without parent links declines only the link.
func runTaskUpdate(ctx context.Context, cmd *cli.Command) error {
	p, err := readProps(cmd)
	if err != nil {
		return err
	}

	relink := cmd.IsSet("parent")
	if p.empty() && !relink {
		return errors.New(
			"task update needs a property to set: --status, --priority, --label, --assignee, --parent or --set",
		)
	}

	t, r, d, err := writeTarget(ctx, cmd, driver.VerbUpdate)
	if err != nil {
		return err
	}

	parent := ""
	if relink {
		if parent, err = parentNumber(ctx, t, r, cmd.String("parent")); err != nil {
			return err
		}
	}

	if !p.empty() {
		if err := updateTask(ctx, cmd.ErrWriter, t, d, r, p); err != nil {
			return err
		}
	}

	if !relink {
		return nil
	}

	return relinkTask(ctx, cmd.ErrWriter, t, d, r, parent, !p.empty())
}

// parentNumber resolves --parent before anything is written; empty means
// "take the task out from under its parent".
func parentNumber(ctx context.Context, t *target, r ref.Ref, raw string) (string, error) {
	if raw = strings.TrimSpace(raw); raw == "" {
		return "", nil
	}

	pr, err := t.reference(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("--parent %s: %w", raw, err)
	}

	if pr.Number == r.Number {
		return "", fmt.Errorf("--parent %s: a task cannot be its own parent; nothing was written", raw)
	}

	return pr.Number, nil
}

// relinkTask sends the parent alone, so a driver declining it has dropped
// nothing else; wrote says the properties before it already landed.
func relinkTask(
	ctx context.Context, warnTo io.Writer, t *target, d *driver.Driver, r ref.Ref, parent string, wrote bool,
) error {
	unwritten := "nothing was written"
	if wrote {
		unwritten = "the other properties were written, the parent link was not"
	}

	if d != nil {
		_, warn, err := d.RunInput(ctx, "", driver.VerbUpdate, r.Number, "--parent", parent)
		if err == nil {
			if warn != "" {
				fmt.Fprintln(warnTo, warn)
			}

			return nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			if wrote {
				return fmt.Errorf("%w\n%s", err, unwritten)
			}

			return err
		}
	}

	h, err := hostOrExit(t, d, driver.VerbUpdate+" --parent", unwritten)
	if err != nil {
		return err
	}

	err = h.relink(ctx, parent, r.Number)
	if errors.Is(err, forge.ErrNoIssueParent) {
		return ui.Exit(ui.ExitDeclined, "%v; %s", err, unwritten)
	}

	return err
}

func updateTask(ctx context.Context, warnTo io.Writer, t *target, d *driver.Driver, r ref.Ref, p props) error {
	if d != nil {
		_, warn, err := d.RunInput(ctx, "", driver.VerbUpdate, append([]string{r.Number}, p.args()...)...)
		if err == nil {
			if warn != "" {
				fmt.Fprintln(warnTo, warn)
			}

			retitleAfter(ctx, warnTo, t, r, "updated")

			return nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return err
		}
	}

	h, err := writeHost(t, d, driver.VerbUpdate)
	if err != nil {
		return err
	}

	return h.update(ctx, r.Number, p, t.eff.PriorityLabels)
}

// writeTarget settles the task and its driver for a write verb, and
// answers exit 3 before stdin is read when nothing could take the write.
func writeTarget(ctx context.Context, cmd *cli.Command, verb string) (*target, ref.Ref, *driver.Driver, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, ref.Ref{}, nil, err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return nil, ref.Ref{}, nil, err
	}

	d := t.driver()
	if d == nil && !t.hostIssues() {
		_, err := writeHost(t, nil, verb)

		return nil, ref.Ref{}, nil, err
	}

	return t, r, d, nil
}

func writeHost(t *target, d *driver.Driver, verb string) (host, error) {
	return hostOrExit(t, d, verb, "nothing was written")
}

// hostOrExit is writeHost with the exit message's last clause given, for a
// write that follows another one.
func hostOrExit(t *target, d *driver.Driver, verb, unwritten string) (host, error) {
	if t.hostIssues() {
		return t.host()
	}

	why := "no driver was found (looked for " + strings.Join(t.driverPaths(), ", ") + ")"
	if d != nil {
		why = "the driver at " + d.Path + " declines `" + verb + "`"
	}

	return host{}, ui.Exit(ui.ExitDeclined, "%s, and a %s tracker has no host-issues fallback; %s",
		why, t.eff.TrackerKind, unwritten)
}

// update: an issue has two states and no custom properties, so `--status`
// takes open or closed and `--set` is refused; the labels of every other
// priority level come off, so the issue carries one; an assignee replaces
// the issue's, as glab's flag does; a title renamed by hand gets its prefix
// back.
func (h host) update(ctx context.Context, number string, p props, priorities map[string]string) error {
	if len(p.set) > 0 {
		return fmt.Errorf("%s issues have no custom properties for --set %s", h.Name, strings.Join(p.set, ", "))
	}

	state := ""

	switch strings.ToLower(p.status) {
	case "":
	case "open", "opened", "reopen", "reopened":
		state = "reopen"
	case "closed", "close", "done":
		state = "close"
	default:
		return fmt.Errorf("status %q: %s issues are open or closed", p.status, h.Name)
	}

	iss, err := h.Issue(ctx, number)
	if err != nil {
		return err
	}

	title := h.retitled(iss.Title, number)

	add, remove := slices.Clone(p.labels), []string(nil)

	if p.priority != "" {
		label := priorityLabel(p.priority, priorities)
		if label == "" {
			return fmt.Errorf("priority %s: tracker.priority in project.md maps no label to it", p.priority)
		}

		add = append(add, label)

		for _, name := range iss.Labels {
			if level, ok := priorities[strings.ToLower(name)]; ok && level != p.priority {
				remove = append(remove, name)
			}
		}
	}

	edit := forge.IssueEdit{
		Title: title, AddLabels: add, RemoveLabels: remove, Assignee: p.assignee, Current: iss.Assignees,
	}
	if err := h.EditIssue(ctx, number, edit); err != nil {
		return err
	}

	if state != "" {
		if err := h.SetIssueOpen(ctx, number, state == "reopen"); err != nil {
			return err
		}
	}

	return nil
}

// priorityLabel takes the first label in name order when several carry the
// level; the index is lower-cased, and the hosts match case-insensitively.
func priorityLabel(level string, priorities map[string]string) string {
	var names []string

	for name, l := range priorities {
		if l == level {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	if len(names) == 0 {
		return ""
	}

	return names[0]
}

func (h host) retitled(raw, number string) string {
	id := h.taskID(number)
	if raw == "" || h.titles.Applied(raw, id, number) {
		return ""
	}

	return h.titles.Render(id, number, h.titles.Strip(raw, id, number))
}

type titleChange struct{ id, number, from, to string }

func (h host) titleChanges(ctx context.Context, number string) ([]titleChange, error) {
	var found []forge.Issue

	if number != "" {
		iss, err := h.Issue(ctx, number)
		if err != nil {
			return nil, err
		}

		found = []forge.Issue{iss}
	} else {
		all, err := h.AllIssues(ctx)
		if err != nil {
			return nil, err
		}

		found = all
	}

	var changes []titleChange

	for _, iss := range found {
		n := iss.Number
		if to := h.retitled(iss.Title, n); to != "" {
			changes = append(changes, titleChange{id: h.taskID(n), number: n, from: iss.Title, to: to})
		}
	}

	return changes, nil
}

func (h host) setTitle(ctx context.Context, number, title string) error {
	return h.EditIssue(ctx, number, forge.IssueEdit{Title: title})
}

// runTaskRetitle leaves a title already written through tracker.title alone,
// so a second run changes nothing.
func runTaskRetitle(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	if t.eff.TitleTemplate == "" {
		ui.Warnf(cmd.ErrWriter, "project.md declares no tracker.title; nothing to retitle")

		return nil
	}

	if !t.hostIssues() {
		return ui.Exit(ui.ExitDeclined, "retitle writes the host's issue titles, and a %s tracker is not the host's; "+
			"nothing was written", t.eff.TrackerKind)
	}

	all := cmd.Bool("all")
	if all && cmd.Args().Present() {
		return errors.New("retitle takes a task id or --all, not both")
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	number := ""
	if !all {
		r, err := t.reference(ctx, cmd.Args().First())
		if err != nil {
			return err
		}

		number = r.Number
	}

	changes, err := h.titleChanges(ctx, number)
	if err != nil {
		return err
	}

	dry := cmd.Bool("dry-run")

	for i, c := range changes {
		if !dry {
			if err := h.setTitle(ctx, c.number, c.to); err != nil {
				return fmt.Errorf("retitling %s: %w (%d retitled before it)", c.id, err, i)
			}
		}

		fmt.Fprintf(cmd.Writer, "%s  %s  ->  %s\n", c.id, c.from, c.to)
	}

	verb := "retitled"
	if dry {
		verb = "would retitle"
	}

	ui.Warnf(cmd.ErrWriter, "%s %d issue(s)", verb, len(changes))

	return nil
}

// createsOnHost is a tracker on the host's issues with a host CLI to reach
// them; without one, create answers exit 3 like a tracker f10 cannot reach.
func (t *target) createsOnHost() bool {
	if !t.hostIssues() {
		return false
	}

	_, err := t.host()

	return err == nil
}

// hostCreate makes the task an issue on the host. The parent link is a second
// write, since it needs the new number; the issue exists by then, so a failed
// link is a warning and never a reason to create it again. An issue has no
// named properties, so --set is refused before anything is written.
func hostCreate(
	ctx context.Context, warnTo io.Writer, t *target, doc string, p props, parent string,
) (ref.Ref, string, error) {
	if len(p.set) > 0 {
		return ref.Ref{}, "", fmt.Errorf(
			"issues on the host have no custom properties for --set %s; nothing was created",
			strings.Join(p.set, ", "),
		)
	}

	switch strings.ToLower(p.status) {
	case "", "open", "opened":
	default:
		return ref.Ref{}, "", fmt.Errorf("status %q: a new issue is open", p.status)
	}

	labels := p.labels

	if p.priority != "" {
		label := priorityLabel(p.priority, t.eff.PriorityLabels)
		if label == "" {
			return ref.Ref{}, "", fmt.Errorf(
				"priority %s: tracker.priority in project.md maps no label to it", p.priority)
		}

		labels = append(labels, label)
	}

	h, err := t.host()
	if err != nil {
		return ref.Ref{}, "", err
	}

	_, body, _ := strings.Cut(doc, "\n")

	url, number, err := h.CreateIssue(ctx, taskTitle(doc), strings.TrimSpace(body), labels)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, mayExist)
	}

	r, err := t.reference(ctx, number)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%s: %w\n%s", url, err, mayExist)
	}

	if parent = strings.TrimSpace(parent); parent != "" {
		if err := linkUnder(ctx, t, h, parent, number); err != nil {
			ui.Warnf(warnTo, "%s was created, but not linked under %s: %v; "+
				"link it by hand, and `f10 task children %s` confirms it", r.ID, parent, err, parent)
		}
	}

	return r, url, nil
}

func linkUnder(ctx context.Context, t *target, h host, parent, child string) error {
	pr, err := t.reference(ctx, parent)
	if err != nil {
		return err
	}

	return h.LinkParent(ctx, pr.Number, child)
}
