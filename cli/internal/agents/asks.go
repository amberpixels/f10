package agents

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/render"
)

// FindOpen is the open ask a token names: a task id, or project:task where
// two projects share one.
func FindOpen(dir, token string) (asks.Ask, error) {
	open, err := asks.Open(dir)
	if err != nil {
		return asks.Ask{}, err
	}

	project, task, scoped := strings.Cut(token, ":")
	if !scoped {
		task, project = token, ""
	}

	named := func(a asks.Ask) bool {
		return strings.EqualFold(a.Task, task) && (project == "" || strings.EqualFold(a.Project, project))
	}

	var hits []asks.Ask

	for _, a := range open {
		if named(a) {
			hits = append(hits, a)
		}
	}

	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		// the newest answer names who closed it
		answered, _ := asks.Answered(dir, time.Time{})
		for _, a := range slices.Backward(answered) {
			if named(a) {
				return asks.Ask{}, fmt.Errorf(
					"%s has no open ask: %w",
					token,
					asks.ClosedError{By: a.By, At: a.AnsweredAt},
				)
			}
		}

		return asks.Ask{}, fmt.Errorf("%s has no open ask: f10 inbox list shows them", token)
	}

	names := make([]string, len(hits))
	for i, a := range hits {
		names[i] = AskName(a)
	}

	return asks.Ask{}, fmt.Errorf("%s asks in several projects: name one, %s", task, strings.Join(names, " or "))
}

// FindKey is the open ask whose inbox key is key.
func FindKey(dir, key string) (asks.Ask, error) {
	a, isOpen, err := asks.Get(dir, key)
	if err != nil {
		return asks.Ask{}, err
	}

	if !isOpen {
		return asks.Ask{}, fmt.Errorf(
			"%s has no open ask: %w",
			AskName(*a),
			asks.ClosedError{By: a.By, At: a.AnsweredAt},
		)
	}

	return *a, nil
}

// AskName is the task, with its project where the inbox spans several.
func AskName(a asks.Ask) string {
	if a.Project == "" {
		return a.Task
	}

	return a.Project + ":" + a.Task
}

// AnswersFor resolves N=VALUE answers into the ask's answer text; a
// question not named takes its default, and one with none is refused.
func AnswersFor(a asks.Ask, values []string) (string, error) {
	given := map[int]string{}

	for _, v := range values {
		num, value, ok := strings.Cut(v, "=")
		n, err := strconv.Atoi(strings.TrimSpace(num))

		if !ok || err != nil {
			return "", fmt.Errorf("%q is not N=VALUE", v)
		}

		i := slices.IndexFunc(a.Questions, func(q asks.Question) bool { return q.N == n })
		if i < 0 {
			return "", fmt.Errorf("%s's ask has no question %d", a.Task, n)
		}

		resolved, err := asks.Resolve(a.Questions[i], value)
		if err != nil {
			return "", err
		}

		given[n] = resolved
	}

	for _, q := range a.Questions {
		if given[q.N] == "" && q.Default() == "" {
			return "", fmt.Errorf("question %d (%s) has no default: answer it as %d=VALUE", q.N, q.Text, q.N)
		}
	}

	return asks.Format(a.Questions, given), nil
}

// A Deliverer moves a claimed answer on: prompt reaches an agent no drive
// watches, resume restarts a stopped drive, and live says whether a drive
// still runs. Resume and live are the owner's, because this core knows no
// drives.
type Deliverer struct {
	Prompt func(ctx context.Context, w io.Writer, a asks.Ask) error
	Resume func(ctx context.Context, w io.Writer, a asks.Ask) error
	Live   func(a asks.Ask) bool
}

// BatchResume is a Deliverer's Resume for the stopped batch an answered ask
// belongs to. hand is the caller's, because the daemon resumes in process and
// a command dials the daemon. Unsupported, it tells the user to resume by hand.
func BatchResume(
	supported bool, hand func(context.Context, asks.Ask) error,
) func(context.Context, io.Writer, asks.Ask) error {
	return func(ctx context.Context, w io.Writer, a asks.Ask) error {
		if !supported {
			fmt.Fprintf(w, "batch %d is stopped: f10 resume %s in %s takes the answer\n", a.Drive, a.Task, a.Main)

			return nil
		}

		if err := hand(ctx, a); err != nil {
			return fmt.Errorf("resuming batch %d: %w", a.Drive, err)
		}

		fmt.Fprintf(w, "batch %d resumed: f10 daemon logs %s -f follows it\n", a.Drive, a.Task)

		return nil
	}
}

// Deliver moves a claimed answer to where it acts. A drive's ask goes to its
// drive, a running one taking it as it exits so nothing runs twice; any
// other is sent to its agent, unless typed in the pane already. sent is true
// when the answer reached the agent here.
func (d Deliverer) Deliver(ctx context.Context, w io.Writer, dir string, a asks.Ask) (bool, error) {
	switch {
	case a.Drive > 0 && d.Live(a):
		fmt.Fprintf(w, "batch %d takes it as it stops\n", a.Drive)

		return false, nil
	case a.Drive > 0:
		return false, d.Resume(ctx, w, a)
	case !a.DeliveredAt.IsZero():
		return false, nil
	}

	if err := d.Prompt(ctx, w, a); err != nil {
		return false, fmt.Errorf(
			"sending the answer to %s's agent: %w; type it in its pane: %s", a.Task, err, a.Answer)
	}

	// no drive takes an answer sent this way: it is done once sent
	if err := asks.Update(
		dir,
		a.Key(),
		func(x *asks.Ask) { x.DeliveredAt, x.TakenAt = time.Now(), time.Now() },
	); err != nil {
		fmt.Fprintf(w, "  recording the answer sent: %v\n", err)
	}

	fmt.Fprintf(w, "sent to %s's agent\n", asknotify.AskLink(render.LinksFor(w, false), a))

	return true, nil
}

// Answer claims a's answer from N=VALUE values and delivers it, writing
// what happened to w.
func Answer(
	ctx context.Context,
	w io.Writer,
	dir string,
	a asks.Ask,
	values []string,
	by string,
	d Deliverer,
) (asks.Ask, bool, error) {
	text, err := AnswersFor(a, values)
	if err != nil {
		return asks.Ask{}, false, err
	}

	claimed, err := asks.Claim(dir, a.Key(), by, text, false, time.Now())
	if err != nil {
		return asks.Ask{}, false, fmt.Errorf("%s: %w", AskName(a), err)
	}

	fmt.Fprintf(w, "%s answered: %s\n", AskName(claimed), text)

	ReleasePane(ctx, w, claimed)

	sent, err := d.Deliver(ctx, w, dir, claimed)

	return claimed, sent, err
}

// ReportBlocked shows the asking agent's herdr pane blocked on the ask's
// first question while it is open. Outside herdr it does nothing.
func ReportBlocked(ctx context.Context, w io.Writer, a asks.Ask) {
	if a.Pane == "" || !a.AnsweredAt.IsZero() || herdr.Available() != nil {
		return
	}

	msg := a.Text
	if len(a.Questions) > 0 {
		msg = a.Questions[0].Text
	}

	if err := herdr.ReportBlocked(ctx, "", a.Pane, msg); err != nil {
		fmt.Fprintf(w, "  showing %s's pane blocked: %v\n", AskName(a), err)
	}
}

// ReleasePane hands an answered ask's pane back to herdr's own detection,
// so it stops showing blocked. A failure is a line on w, never the answer's.
func ReleasePane(ctx context.Context, w io.Writer, a asks.Ask) {
	if a.Pane == "" {
		return
	}

	b, ok := Agents(a.Worktree).(interface {
		Available() error
		ReleaseAgent(ctx context.Context, pane string) error
	})
	if !ok || b.Available() != nil {
		return
	}

	if err := b.ReleaseAgent(ctx, a.Pane); err != nil {
		fmt.Fprintf(w, "  clearing %s's blocked pane: %v\n", AskName(a), err)
	}
}
