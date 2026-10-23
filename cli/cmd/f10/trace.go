package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// The trace noun: what a step leaves on the host for a reader of the
// history, rendered by the binary so the steps never hand-write the format.
// `trailers` prints the Task:, Step: and Review: lines a commit message
// ends in; a step appends them as the last -m paragraph of its git commit.
// With traces off it prints nothing and exits 0, so every step calls it the
// same way and the prose carries no switch.
func traceCommand() *cli.Command {
	return &cli.Command{
		Name:  "trace",
		Usage: "the trailers a step's commit carries, rendered from the project's traces switch",
		Commands: []*cli.Command{
			{
				Name:  "trailers",
				Usage: "print the Task:, Step: and Review: trailer lines for a commit; nothing with traces off",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "step",
						Usage:    "the `STEP` whose work the commit carries: implement, resolve, judge, catchup, or a project-defined step",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "task",
						Usage: "the task `ID`, or a bundle (id+id); the branch's task and riders when absent",
					},
					&cli.IntFlag{Name: "round", Usage: "a resolve fix commit: the review round it settled"},
					&cli.IntFlag{Name: "fixed", Usage: "a resolve fix commit: findings fixed"},
					&cli.IntFlag{Name: "skipped", Usage: "a resolve fix commit: findings skipped"},
				},
				Action: runTraceTrailers,
			},
		},
	}
}

func runTraceTrailers(ctx context.Context, cmd *cli.Command) error {
	counts, hasReview, err := reviewCounts(cmd)
	if err != nil {
		return err
	}

	var review *trace.Review
	if hasReview {
		review = &counts
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	if !t.eff.Traces {
		return nil
	}

	// the lookup itself, not target.reference: under -C the branch the
	// trailers describe is the one -C points at, which is what a step
	// committing there wants; and never the session's task, since a commit
	// on a branch naming no task carries none
	r, err := ref.Lookup{Prefix: t.eff.IDPrefix, Dir: t.dir, NoSession: true}.Find(ctx, cmd.String("task"))

	var tasks []string

	switch {
	case err == nil:
		tasks = r.Members()
	case errors.Is(err, ref.ErrNoReference) && strings.TrimSpace(cmd.String("task")) == "":
		fmt.Fprintln(errWriter(cmd), "f10: no task on this branch: Step: written alone")
	default:
		return err
	}

	out, err := trace.Render(trace.Trailers{Tasks: tasks, Step: cmd.String("step"), Review: review})
	if err != nil {
		return err
	}

	fmt.Fprint(cmd.Writer, out)

	return nil
}

// reviewCounts reads the three review flags: all or none, since a Review:
// line with a count missing would read as zero. The bool is false without them.
func reviewCounts(cmd *cli.Command) (trace.Review, bool, error) {
	set := 0
	for _, name := range []string{"round", "fixed", "skipped"} {
		if cmd.IsSet(name) {
			set++
		}
	}

	switch set {
	case 0:
		return trace.Review{}, false, nil
	case 3:
		return trace.Review{Round: cmd.Int("round"), Fixed: cmd.Int("fixed"), Skipped: cmd.Int("skipped")}, true, nil
	default:
		return trace.Review{}, false,
			errors.New("--round, --fixed and --skipped go together: a Review: line carries all three counts")
	}
}

func errWriter(cmd *cli.Command) io.Writer {
	if w := cmd.Root().ErrWriter; w != nil {
		return w
	}

	return os.Stderr
}
