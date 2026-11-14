package task

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/regression"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
)

func regressionCommand() *cli.Command {
	return &cli.Command{
		Name:     "regression",
		Hidden:   true,
		Category: command.Plumbing,
		Usage: "record a bug task's origin: the task whose commit introduced it and how its review let it " +
			"through, printed as `<origin> (<class>, <confidence>)`; or, with --claimed, the origin its text names",
		ArgsUsage: "<task-id>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "commit",
				Usage: "the commit that introduced the fault; leave out when none was found",
			},
			&cli.StringSliceFlag{
				Name:  "lines",
				Usage: "`path:start-end` of the faulty lines at --base; repeat for several",
			},
			&cli.StringFlag{Name: "base", Usage: "the revision --lines is numbered in", Value: "HEAD"},
			&cli.StringFlag{
				Name:  "found-by",
				Usage: "one of " + strings.Join(regression.FoundBy, ", "),
				Value: "blame",
			},
			&cli.StringFlag{Name: "confidence", Usage: "one of " + strings.Join(regression.Confidences, ", ")},
			&cli.StringFlag{
				Name:  "repro",
				Usage: "the debug pass's outcome: one of " + strings.Join(regression.Repros, ", "),
			},
			&cli.StringFlag{
				Name:  "class",
				Usage: "out-of-reach, when reading the code shows no diff review could see it",
			},
			&cli.StringFlag{Name: "claimed", Usage: "the task id the task's own text names as its origin"},
			&cli.StringFlag{Name: "text", Usage: "with --claimed: the phrase that names it"},
		},
		Action: runTaskRegression,
	}
}

func runTaskRegression(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.Reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	task := r.Head().ID

	if origin := cmd.String("claimed"); origin != "" {
		e, err := ledger.NewEvent(ledger.RegressionClaimed,
			ledger.RegressionClaimedPayload{OriginTask: origin, Text: cmd.String("text")})
		if err != nil {
			return err
		}

		record(ctx, t, task, e)

		return nil
	}

	in, err := regressionInput(cmd)
	if err != nil {
		return err
	}

	in.Dir, in.StorageRoot, in.Prefix = t.Dir, t.Layout.StorageRoot.Path(), t.Effective.IDPrefix

	p := regression.Attribute(ctx, in)

	e, err := ledger.NewEvent(ledger.RegressionFound, p)
	if err != nil {
		return err
	}

	record(ctx, t, task, e)

	_, err = fmt.Fprintln(cmd.Writer, regression.Line(p))

	return err
}

func regressionInput(cmd *cli.Command) (regression.Input, error) {
	in := regression.Input{
		Base:       cmd.String("base"),
		Commit:     cmd.String("commit"),
		FoundBy:    cmd.String("found-by"),
		Confidence: cmd.String("confidence"),
		Repro:      cmd.String("repro"),
		Class:      cmd.String("class"),
	}

	for flag, ok := range map[string]bool{
		"--found-by":   slices.Contains(regression.FoundBy, in.FoundBy),
		"--confidence": slices.Contains(regression.Confidences, in.Confidence),
		"--repro":      slices.Contains(regression.Repros, in.Repro),
		"--class":      in.Class == "" || in.Class == regression.OutOfReach,
	} {
		if !ok {
			return in, fmt.Errorf("%s: unknown value; see f10 task regression --help", flag)
		}
	}

	for _, s := range cmd.StringSlice("lines") {
		rg, err := regression.ParseRange(s)
		if err != nil {
			return in, fmt.Errorf("--lines %w", err)
		}

		in.Lines = append(in.Lines, rg)
	}

	if in.Commit != "" && len(in.Lines) == 0 {
		return in, errors.New("--commit needs --lines: the faulty lines it introduced")
	}

	return in, nil
}

// record puts e on the ledger as the agent's claim about task.
func record(ctx context.Context, t *target.Target, task string, e ledger.Event) {
	place := ledger.Where(ctx, t.Dir)

	e.At, e.Session, e.Task, e.Source = time.Now(), state.Session(), task, ledger.Claimed
	e.Project, e.ProjectLocal, e.Checkout = place.Project, place.ProjectLocal, place.Checkout
	e.Branch = gitx.CurrentBranch(ctx, t.Dir)
	ledger.Record(e)
}
