package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The task's write verbs go to the driver, else to the host's issues; a
// tracker that is neither answers exit 3, which a ship run reads as "report
// the shipment locally only".

// exitNoWrite is comment's and update's "this tracker takes no such write
// here": the driver declines it and the tracker has no host-issues fallback.
const exitNoWrite = 3

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

	url, err := h.comment(ctx, r.Number, body)
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, mayPosted)
	}

	return url, nil
}

// runTaskUpdate takes absolute values, so a rerun after a failure is safe.
func runTaskUpdate(ctx context.Context, cmd *cli.Command) error {
	p, err := readProps(cmd)
	if err != nil {
		return err
	}

	if p.empty() {
		return errors.New("task update needs a property to set: --status, --priority, --label, --assignee or --set")
	}

	t, r, d, err := writeTarget(ctx, cmd, driver.VerbUpdate)
	if err != nil {
		return err
	}

	return updateTask(ctx, cmd.ErrWriter, t, d, r, p)
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
	if t.hostIssues() {
		return t.host()
	}

	why := "no driver was found (looked for " + strings.Join(t.driverPaths(), ", ") + ")"
	if d != nil {
		why = "the driver at " + d.Path + " declines `" + verb + "`"
	}

	return host{}, cli.Exit(fmt.Sprintf("f10: %s, and a %s tracker has no host-issues fallback; nothing was written",
		why, t.eff.TrackerKind), exitNoWrite)
}

func (h host) comment(ctx context.Context, number, body string) (string, error) {
	if err := h.mustBeIssue(ctx, number); err != nil {
		return "", err
	}

	out, err := h.Run(ctx, h.Pick(
		[]string{"issue", "comment", number, "--body", body},
		[]string{"issue", "note", number, "--message", body},
	)...)
	if err != nil {
		return "", err
	}

	for line := range strings.Lines(out) {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "http") {
			return line, nil
		}
	}

	return "", nil
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

	fields := "labels,assignees"
	if h.titles != "" {
		fields += ",title"
	}

	iss, err := h.issue(ctx, number, fields)
	if err != nil {
		return err
	}

	if err := iss.notPull(number); err != nil {
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

	// gh only adds an assignee, so the others come off by name
	var unassign []string

	if p.assignee != "" && !h.GLab() {
		// gh reads @me as the signed-in login, which must stay on
		keep := p.assignee
		if keep == "@me" {
			if keep, err = h.Login(ctx); err != nil {
				return err
			}
		}

		for _, a := range iss.Assignees {
			if a.Login != "" && !strings.EqualFold(a.Login, keep) {
				unassign = append(unassign, a.Login)
			}
		}
	}

	if len(add) > 0 || len(remove) > 0 || p.assignee != "" || title != "" {
		if _, err := h.Run(ctx, h.editArgs(number, title, add, remove, p.assignee, unassign)...); err != nil {
			return err
		}
	}

	if state != "" {
		if _, err := h.Run(ctx, "issue", state, number); err != nil {
			return err
		}
	}

	return nil
}

func (h host) editArgs(number, title string, add, remove []string, assignee string, unassign []string) []string {
	if h.GLab() {
		args := []string{"issue", "update", number}
		if title != "" {
			args = append(args, "--title", title)
		}

		if len(add) > 0 {
			args = append(args, "--label", strings.Join(add, ","))
		}

		if len(remove) > 0 {
			args = append(args, "--unlabel", strings.Join(remove, ","))
		}

		if assignee != "" {
			args = append(args, "--assignee", assignee)
		}

		return args
	}

	args := []string{"issue", "edit", number}
	if title != "" {
		args = append(args, "--title", title)
	}

	for _, l := range add {
		args = append(args, "--add-label", l)
	}

	for _, l := range remove {
		args = append(args, "--remove-label", l)
	}

	if assignee != "" {
		args = append(args, "--add-assignee", assignee)
	}

	for _, a := range unassign {
		args = append(args, "--remove-assignee", a)
	}

	return args
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

// mustBeIssue refuses a pull request where a write was meant for an issue:
// gh numbers both from one counter and would comment on either.
func (h host) mustBeIssue(ctx context.Context, number string) error {
	iss, err := h.issue(ctx, number, "url")
	if err != nil {
		return err
	}

	return iss.notPull(number)
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
	var found []issue

	if number != "" {
		iss, err := h.issue(ctx, number, "number,title")
		if err != nil {
			return nil, err
		}

		if err := iss.notPull(number); err != nil {
			return nil, err
		}

		found = []issue{iss}
	} else {
		all, err := h.allIssues(ctx)
		if err != nil {
			return nil, err
		}

		found = all
	}

	var changes []titleChange

	for _, iss := range found {
		n := iss.ident()
		if to := h.retitled(iss.Title, n); to != "" {
			changes = append(changes, titleChange{id: h.taskID(n), number: n, from: iss.Title, to: to})
		}
	}

	return changes, nil
}

// retitlePage is how many issues glab lists a page; gh takes one limit.
const retitlePage = 100

// allIssues: gh issue list never includes pull requests, nor glab's merge
// requests, so nothing here filters them.
func (h host) allIssues(ctx context.Context) ([]issue, error) {
	if !h.GLab() {
		var all []issue

		err := h.Decode(ctx, &all, "issue", "list", "--state", "all", "--json", "number,title", "--limit", "100000")

		return all, err
	}

	var all []issue

	for page := 1; ; page++ {
		var found []issue
		if err := h.Decode(ctx, &found, "issue", "list", "--all", "--output", "json",
			"--per-page", strconv.Itoa(retitlePage), "--page", strconv.Itoa(page)); err != nil {
			return nil, err
		}

		all = append(all, found...)

		if len(found) < retitlePage {
			return all, nil
		}
	}
}

func (h host) setTitle(ctx context.Context, number, title string) error {
	_, err := h.Run(ctx, h.editArgs(number, title, nil, nil, "", nil)...)

	return err
}

// runTaskRetitle leaves a title already written through tracker.title alone,
// so a second run changes nothing.
func runTaskRetitle(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	if t.eff.TitleTemplate == "" {
		fmt.Fprintln(cmd.ErrWriter, "f10: project.md declares no tracker.title; nothing to retitle")

		return nil
	}

	if !t.hostIssues() {
		return cli.Exit(fmt.Sprintf("f10: retitle writes the host's issue titles, and a %s tracker is not the host's; "+
			"nothing was written", t.eff.TrackerKind), exitNoWrite)
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

	fmt.Fprintf(cmd.ErrWriter, "f10: %s %d issue(s)\n", verb, len(changes))

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

	url, number, err := h.createIssue(ctx, taskTitle(doc), strings.TrimSpace(body), labels)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, mayExist)
	}

	r, err := t.reference(ctx, number)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%s: %w\n%s", url, err, mayExist)
	}

	if parent = strings.TrimSpace(parent); parent != "" {
		if err := linkUnder(ctx, t, h, parent, number); err != nil {
			fmt.Fprintf(warnTo, "f10: %s was created, but not linked under %s: %v; "+
				"link it by hand, and `f10 task children %s` confirms it\n", r.ID, parent, err, parent)
		}
	}

	return r, url, nil
}

func linkUnder(ctx context.Context, t *target, h host, parent, child string) error {
	pr, err := t.reference(ctx, parent)
	if err != nil {
		return err
	}

	return h.linkParent(ctx, pr.Number, child)
}

var issueNumberRe = regexp.MustCompile(`/issues/(\d+)\b`)

// createIssue returns the new issue's url and number, read from the url the
// CLI prints: a list query afterwards could find another create's issue.
func (h host) createIssue(ctx context.Context, title, body string, labels []string) (string, string, error) {
	args := h.Pick(
		[]string{"issue", "create", "--title", title, "--body", body},
		[]string{"issue", "create", "--title", title, "--description", body, "--no-editor", "--yes"},
	)

	for _, l := range labels {
		args = append(args, "--label", l)
	}

	out, err := h.Run(ctx, args...)
	if err != nil {
		return "", "", err
	}

	for _, line := range slices.Backward(strings.Split(strings.TrimSpace(out), "\n")) {
		line = strings.TrimSpace(line)
		if m := issueNumberRe.FindStringSubmatch(line); m != nil && strings.HasPrefix(line, "http") {
			return line, m[1], nil
		}
	}

	return "", "", fmt.Errorf("%s issue create printed no issue url: %q", h.Name, out)
}
