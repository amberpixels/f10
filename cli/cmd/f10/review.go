package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// The review noun reads the declared remote review off the branch's request.
// The Review section's four facts and the optional settled mark are the whole
// configuration; only ack, settle and trigger write to the host.
func reviewCommand() *cli.Command {
	return &cli.Command{
		Name:     "review",
		Hidden:   true,
		Category: plumbingCategory,
		Usage:    "read, await or acknowledge the remote review of a branch's PR or MR",
		Commands: []*cli.Command{
			{
				Name:      "pick",
				Usage:     "write the latest unhandled review to stdout",
				ArgsUsage: "[number]",
				Flags: []cli.Flag{
					jsonFlag(),
					&cli.BoolFlag{Name: "latest", Usage: "the latest review whether handled or settled"},
				},
				Action: runReviewPick,
			},
			{
				Name:      "wait",
				Usage:     "poll until the latest unhandled review is done, within a budget",
				ArgsUsage: "[number]",
				Flags: []cli.Flag{
					jsonFlag(),
					&cli.DurationFlag{Name: "budget", Value: 10 * time.Minute, Usage: "give up after this long"},
					&cli.DurationFlag{Name: "every", Value: 20 * time.Second, Usage: "poll interval"},
					&cli.TimestampFlag{
						Name:   "since",
						Usage:  "count only a review posted or updated after this time, as trigger printed it",
						Config: cli.TimestampConfig{Layouts: []string{time.RFC3339}},
					},
				},
				Action: runReviewWait,
			},
			{
				Name:      "trigger",
				Usage:     "perform the declared manual trigger: post its comment or request its review",
				ArgsUsage: "[number]",
				Action:    runReviewTrigger,
			},
			{
				Name:      "ack",
				Usage:     "mark the latest unhandled review handled, the way Review declares",
				ArgsUsage: "[number]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "id",
						Usage: "the review to mark, as pick printed it, instead of the latest unhandled",
					},
				},
				Action: runReviewAck,
			},
			{
				Name:      "settle",
				Usage:     "mark a review settled, the way Review declares, unless it changed since it was read",
				ArgsUsage: "[number]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "id", Required: true, Usage: "the review's id, as pick printed it"},
					&cli.StringFlag{
						Name:     "revision",
						Required: true,
						Usage:    "the revision the review was read at, as pick printed it",
					},
				},
				Action: runReviewSettle,
			},
			{
				Name: "round",
				Usage: "print the task's findings directory, its latest round and the next one, or why none is " +
					"allowed (exit 3)",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "emit the rounds as JSON"}},
				Action:    runReviewRound,
			},
		},
	}
}

func jsonFlag() cli.Flag {
	return &cli.BoolFlag{Name: "json", Usage: "emit the review as JSON instead of markdown"}
}

// remoteReviewer refuses before any host call, each refusal naming what to fix.
func remoteReviewer(ctx context.Context, cmd *cli.Command) (*reviewer, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, err
	}

	f := t.eff.Review
	if !f.Remote() {
		return nil, errors.New("project.md declares no remote review: declare Review's four facts " +
			"(Reviewer, Arrives as, Trigger, Done, handled) and optionally Settled, or run `f10 init` on a checkout with a Claude Code review workflow")
	}

	if missing := f.Missing(); len(missing) > 0 {
		return nil, fmt.Errorf(
			"the Review section's %s line(s) could not be read - see the vocabulary in docs/project-setup.md",
			strings.Join(missing, ", "),
		)
	}

	if f.Handled == facts.HandledThread && f.Arrives != facts.ArrivesInline {
		return nil, errors.New(
			"the Review section declares `thread resolved` for a review that does not arrive inline; " +
				"declare reaction, marker or checkbox instead",
		)
	}

	if err := settledConflict(f); err != nil {
		return nil, err
	}

	h, err := t.host()
	if err != nil {
		return nil, err
	}

	return newReviewer(ctx, h, f, cmd.Args().First())
}

// settledConflict refuses a settled mark the host could not tell from handled.
func settledConflict(f facts.Review) error {
	switch {
	case f.Settled == "":
		return nil
	case f.Settled == f.Handled && f.SettledText == f.HandledText:
		return errors.New("the Review section's settled mark is the handled mark: settled could never be told " +
			"from handled; declare a different reaction or marker")
	}

	return nil
}

func runReviewPick(ctx context.Context, cmd *cli.Command) error {
	rv, err := remoteReviewer(ctx, cmd)
	if err != nil {
		return err
	}

	pick := rv.pick
	if cmd.Bool("latest") {
		pick = rv.latest
	}

	r, err := pick(ctx)
	if err != nil {
		return err
	}

	return writeReview(ctx, cmd, rv, r)
}

func runReviewWait(ctx context.Context, cmd *cli.Command) error {
	rv, err := remoteReviewer(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := awaitReview(ctx, rv, cmd.Timestamp("since"), cmd.Duration("budget"), cmd.Duration("every"))
	if err != nil {
		return err
	}

	return writeReview(ctx, cmd, rv, r)
}

// awaitReview reads everything short of done (nothing posted, still running,
// not updated after since, a transient host error) as "not yet", quoting the
// last reason when the budget expires.
func awaitReview(
	ctx context.Context,
	rv *reviewer,
	since time.Time,
	budget, every time.Duration,
) (remoteReview, error) {
	if every <= 0 {
		return remoteReview{}, errors.New("--every must be positive")
	}

	deadline := time.Now().Add(budget)

	var last string

	for {
		r, err := rv.pick(ctx)

		switch {
		case err == nil && !since.IsZero() && !r.UpdatedAt.After(since):
			last = "the latest review (" + r.URL + ") predates " + since.UTC().Format(time.RFC3339)
		case err == nil && r.Done:
			return r, nil
		case err == nil:
			last = "the latest review (" + r.URL + ") is not done"
		default:
			last = err.Error()
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return remoteReview{}, fmt.Errorf(
				"no completed review from %s within %s: %s",
				rv.facts.Reviewer,
				budget,
				last,
			)
		}

		timer := time.NewTimer(min(every, remaining))

		select {
		case <-ctx.Done():
			timer.Stop()

			return remoteReview{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func runReviewTrigger(ctx context.Context, cmd *cli.Command) error {
	rv, err := remoteReviewer(ctx, cmd)
	if err != nil {
		return err
	}

	what, url, at, err := rv.trigger(ctx)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Writer, "%s  %s  %s\n", at.UTC().Format(time.RFC3339), what, url)

	return err
}

func runReviewAck(ctx context.Context, cmd *cli.Command) error {
	rv, err := remoteReviewer(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := rv.ackTarget(ctx, cmd.String("id"))
	if err != nil {
		return err
	}

	what, err := rv.ack(ctx, r)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Writer, "%s  %s  %s\n", r.ID, what, r.URL)

	return err
}

// errReviewChanged is settle refusing a review rewritten since it was read:
// a sticky comment edited by a re-review is a new, unread review.
var errReviewChanged = errors.New("the review changed since it was read")

func runReviewSettle(ctx context.Context, cmd *cli.Command) error {
	rv, err := remoteReviewer(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := rv.settleTarget(ctx, cmd.String("id"), cmd.String("revision"))
	if err != nil {
		return err
	}

	what, err := rv.settle(ctx, r)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Writer, "%s  %s  %s\n", r.ID, what, r.URL)

	return err
}

func writeReview(ctx context.Context, cmd *cli.Command, rv *reviewer, r remoteReview) error {
	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("encoding review: %w", err)
		}

		return nil
	}

	return writeDoc(ctx, cmd.Writer, rv.h.Dir, reviewDoc(r))
}

func reviewDoc(r remoteReview) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# review by %s · %s\n\n", r.Author, r.Kind)

	state := "not done"
	if r.Done {
		state = "done"
	}

	handled := "unhandled"
	if r.Handled {
		handled = "handled"
	}

	meta := []string{r.URL, r.SHA, "posted " + r.CreatedAt.UTC().Format(time.RFC3339), state, handled}
	if r.Settled {
		meta = append(meta, "settled")
	}

	if r.Stale {
		meta = append(meta, "stale")
	}

	meta = append(meta, "revision "+r.Revision)

	if !r.UpdatedAt.IsZero() && !r.UpdatedAt.Equal(r.CreatedAt) {
		meta = append(meta, "updated "+r.UpdatedAt.UTC().Format(time.RFC3339))
	}

	b.WriteString(strings.Join(meta, " · "))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(r.Body))
	b.WriteString("\n")

	return b.String()
}
