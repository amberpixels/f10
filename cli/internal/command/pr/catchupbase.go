package pr

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The catchup step's base and method, decided here so the step, ship's
// guard and the dry run read one answer; settling conflicts stays the step's.
// It sits under pr beside pr base, whose recorded base it shares.
func prCatchupCommand() *cli.Command {
	return &cli.Command{
		Name:     "catchup",
		Hidden:   true,
		Category: command.Plumbing,
		Usage: "print what a catchup of the current branch brings in: the base, how it was found, " +
			"the method and why, and the mark a conflict sets",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "guard",
				Usage: "decide as ship's guard before this `ENTRY`: implement, pr or judge",
			},
			&cli.BoolFlag{Name: "no-fetch", Usage: "read the refs at hand instead of fetching origin first"},
		},
		Action: runCatchupBase,
	}
}

func runCatchupBase(ctx context.Context, cmd *cli.Command) error {
	guard := cmd.String("guard")
	if guard != "" && !slices.Contains(flow.Guarded, guard) {
		return fmt.Errorf("--guard %q: want implement, pr or judge", guard)
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	in := stack.CatchupPolicy(t.Effective)
	in.Dir = t.Layout.CheckoutRoot
	in.Main = cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	in.Guard = guard
	in.ShipStopped = ShipStopped()

	if !cmd.Bool("no-fetch") {
		if err := gitx.Fetch(ctx, in.Main); err != nil {
			ui.Warnf(cmd.ErrWriter, "fetching origin failed, deciding on the refs at hand: %v", err)
		}
	}

	p, err := stack.ResolveCatchup(ctx, in)
	if err != nil {
		return err
	}

	return p.Write(cmd.Writer)
}

// ShipStopped reads this session's ship status; no session or no run file
// is a ship that never stopped.
func ShipStopped() bool {
	session := state.Session()
	if session == "" {
		return false
	}

	r, err := state.Load(state.Path(state.Dir(), session))
	if err != nil || r == nil {
		return false
	}

	s := r.Status("ship")

	return s == "failed" || s == "partial"
}
