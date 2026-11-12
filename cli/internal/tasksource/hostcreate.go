package tasksource

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// MayPosted follows every failed comment: the tracker may have taken it
// before failing, so posting again could post it twice.
const MayPosted = "the comment may have been posted: check the task before running comment again"

// hostCreate makes the task an issue on the host. The parent link is a second
// write, since it needs the new number; the issue exists by then, so a failed
// link is a warning and never a reason to create it again. An issue has no
// named properties, so --set is refused before anything is written.
func hostCreate(
	ctx context.Context,
	warnTo io.Writer,
	t *target.Target,
	h hostops.Host,
	nt NewTask,
) (ref.Ref, string, error) {
	p := nt.Props

	if len(p.Set) > 0 {
		return ref.Ref{}, "", fmt.Errorf(
			"issues on the host have no custom properties for --set %s; nothing was created",
			strings.Join(p.Set, ", "),
		)
	}

	switch strings.ToLower(p.Status) {
	case "", "open", "opened":
	default:
		return ref.Ref{}, "", fmt.Errorf("status %q: a new issue is open", p.Status)
	}

	labels := p.Labels

	if p.Priority != "" {
		label := hostops.PriorityLabel(p.Priority, t.Effective.PriorityLabels)
		if label == "" {
			return ref.Ref{}, "", fmt.Errorf(
				"priority %s: tracker.priority in project.md maps no label to it", p.Priority)
		}

		labels = append(labels, label)
	}

	_, body, _ := strings.Cut(nt.Doc, "\n")

	url, number, err := h.CreateIssue(ctx, hostops.TaskTitle(nt.Doc), strings.TrimSpace(body), labels)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, MayExist)
	}

	r, err := t.Reference(ctx, number)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%s: %w\n%s", url, err, MayExist)
	}

	if parent := nt.Parent; parent.Number != "" {
		if err := h.LinkParent(ctx, parent.Number, number); err != nil {
			ui.Warnf(warnTo, "%s was created, but not linked under %s: %v; "+
				"link it by hand, and `f10 task children %s` confirms it", r.ID, parent.ID, err, parent.ID)
		}
	}

	return r, url, nil
}
