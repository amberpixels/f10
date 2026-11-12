package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The task's write verbs go to the driver, else to the host's issues; a
// tracker that is neither answers exit 3, which a ship run reads as "report
// the shipment locally only".

func readProps(cmd *cli.Command) (hostops.Props, error) {
	var p hostops.Props

	p.Status = strings.TrimSpace(cmd.String("status"))

	if raw := cmd.String("priority"); raw != "" {
		rank, ok := trackerdriver.PriorityRank(raw)
		if !ok {
			return hostops.Props{}, fmt.Errorf(
				"priority %q is not one of %s",
				raw,
				strings.Join(trackerdriver.Priorities, ", "),
			)
		}

		p.Priority = trackerdriver.Priorities[rank]
	}

	for _, l := range cmd.StringSlice("label") {
		if l = strings.TrimSpace(l); l != "" {
			p.Labels = append(p.Labels, l)
		}
	}

	if cmd.IsSet("assignee") {
		p.Assignee = strings.TrimSpace(cmd.String("assignee"))
	}

	if cmd.IsSet("set") {
		for _, kv := range cmd.StringSlice("set") {
			key, _, ok := strings.Cut(kv, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return hostops.Props{}, fmt.Errorf("--set %q is not key=value", kv)
			}

			p.Set = append(p.Set, kv)
		}
	}

	return p, nil
}

func runTaskComment(ctx context.Context, cmd *cli.Command) error {
	t, r, err := writeTarget(ctx, cmd, trackerdriver.VerbComment)
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

	url, err := tasksource.TasksFor(t).Comment(ctx, cmd.ErrWriter, r, body)
	if err != nil {
		return err
	}

	if url != "" {
		_, err = fmt.Fprintln(cmd.Writer, url)
	}

	return err
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
	if p.Empty() && !relink {
		return errors.New(
			"task update needs a property to set: --status, --priority, --label, --assignee, --parent or --set",
		)
	}

	t, r, err := writeTarget(ctx, cmd, trackerdriver.VerbUpdate)
	if err != nil {
		return err
	}

	parent := ""
	if relink {
		if parent, err = parentNumber(ctx, t, r, cmd.String("parent")); err != nil {
			return err
		}
	}

	if !p.Empty() {
		if err := tasksource.TasksFor(t).Update(ctx, cmd.ErrWriter, r, p); err != nil {
			return err
		}
	}

	if !relink {
		return nil
	}

	// the parent goes alone, so a source declining it has dropped nothing else
	unwritten := tasksource.UnwrittenNothing
	if !p.Empty() {
		unwritten = "the other properties were written, the parent link was not"
	}

	return tasksource.TasksFor(t).Relink(ctx, cmd.ErrWriter, r, parent, unwritten)
}

// parentNumber resolves --parent before anything is written; empty means
// "take the task out from under its parent".
func parentNumber(ctx context.Context, t *target.Target, r ref.Ref, raw string) (string, error) {
	if raw = strings.TrimSpace(raw); raw == "" {
		return "", nil
	}

	pr, err := t.Reference(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("--parent %s: %w", raw, err)
	}

	if pr.Number == r.Number {
		return "", fmt.Errorf("--parent %s: a task cannot be its own parent; nothing was written", raw)
	}

	return pr.Number, nil
}

// writeTarget settles the task for a write verb, and refuses before stdin
// is read when nothing could take the write.
func writeTarget(ctx context.Context, cmd *cli.Command, verb string) (*target.Target, ref.Ref, error) {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return nil, ref.Ref{}, err
	}

	r, err := t.Reference(ctx, cmd.Args().First())
	if err != nil {
		return nil, ref.Ref{}, err
	}

	if rs, ok := refusedUpFrontFor(t); ok {
		return nil, ref.Ref{}, rs.RefuseWrite(verb, tasksource.UnwrittenNothing)
	}

	return t, r, nil
}

// runTaskRetitle leaves a title already written through tracker.title alone,
// so a second run changes nothing.
func runTaskRetitle(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	if t.Effective.TitleTemplate == "" {
		ui.Warnf(cmd.ErrWriter, "project.md declares no tracker.title; nothing to retitle")

		return nil
	}

	if !t.HostIssues() {
		return ui.Exit(ui.ExitDeclined, "retitle writes the host's issue titles, and a %s tracker is not the host's; "+
			"nothing was written", t.Effective.TrackerKind)
	}

	all := cmd.Bool("all")
	if all && cmd.Args().Present() {
		return errors.New("retitle takes a task id or --all, not both")
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	number := ""
	if !all {
		r, err := t.Reference(ctx, cmd.Args().First())
		if err != nil {
			return err
		}

		number = r.Number
	}

	changes, err := h.TitleChanges(ctx, number)
	if err != nil {
		return err
	}

	dry := cmd.Bool("dry-run")

	for i, c := range changes {
		if !dry {
			if err := h.SetTitle(ctx, c.Number, c.To); err != nil {
				return fmt.Errorf("retitling %s: %w (%d retitled before it)", c.ID, err, i)
			}
		}

		fmt.Fprintf(cmd.Writer, "%s  %s  ->  %s\n", c.ID, c.From, c.To)
	}

	verb := "retitled"
	if dry {
		verb = "would retitle"
	}

	ui.Warnf(cmd.ErrWriter, "%s %d issue(s)", verb, len(changes))

	return nil
}
