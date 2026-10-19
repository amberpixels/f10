package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The state noun writes the run state the status-line badge renders. Steps
// reach it through bin/f10-state.sh, the name their prose types. The badge
// is cosmetic, so no session or an unwritable directory is a silent no-op;
// only a malformed call exits 2, since staying silent would freeze the badge
// over a typo.
func stateCommand() *cli.Command {
	verb := func(name, args, usage string, apply func(r *state.Run, a cli.Args) error) *cli.Command {
		kind := "state_" + name

		return &cli.Command{
			Name:      name,
			ArgsUsage: args,
			Usage:     usage,
			// notes and asks are free prose: a leading dash is text, not a flag
			SkipFlagParsing: true,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return writeState(ctx, cmd, kind, apply)
			},
		}
	}

	return &cli.Command{
		Name:   "state",
		Usage:  "write this session's run state for the status-line badge",
		Hidden: true,
		Commands: []*cli.Command{
			verb("set", "<capture|plan|ship> <status> [<leaf>]", "record a phase's status",
				func(r *state.Run, a cli.Args) error { return r.Set(a.Get(0), a.Get(1), a.Get(2)) }),
			verb("task", "<id> [<url>]", "record the task this run is about",
				func(r *state.Run, a cli.Args) error { return r.SetTask(a.Get(0), a.Get(1)) }),
			verb("note", "<reason> [<next>]", "why a run stopped, and what unblocks it",
				func(r *state.Run, a cli.Args) error { return r.SetNote(a.Get(0), a.Get(1)) }),
			verb("ask", "<text>", "the question a driven run stopped on, options included",
				func(r *state.Run, a cli.Args) error { return r.SetAsk(a.Get(0)) }),
			verb("final", "<step>", "declare the ship pipeline's last step",
				func(r *state.Run, a cli.Args) error { return r.SetFinal(a.Get(0)) }),
			{
				Name:  "show",
				Usage: "the current run, one line per fact",
				Action: func(_ context.Context, cmd *cli.Command) error {
					sid := state.Session()
					if sid == "" {
						_, err := fmt.Fprintln(cmd.Writer, "no session id - nothing to show")

						return err
					}

					r, err := state.Load(state.Path(state.Dir(), sid))
					if err != nil {
						_, err = fmt.Fprintln(cmd.Writer, "no state for session", sid)

						return err
					}

					r.Show(cmd.Writer, time.Now())

					return nil
				},
			},
			{
				Name:  "clear",
				Usage: "drop this session's state",
				Action: func(context.Context, *cli.Command) error {
					if sid := state.Session(); sid != "" {
						state.Clear(state.Dir(), sid)
					}

					return nil
				},
			},
			{
				Name:  "prune",
				Usage: "drop long-dead sessions' files",
				Action: func(context.Context, *cli.Command) error {
					state.Prune(state.Dir(), time.Now())

					return nil
				},
			},
		},
	}
}

func writeState(ctx context.Context, cmd *cli.Command, kind string, apply func(r *state.Run, a cli.Args) error) error {
	// a typo fails even without a session, or step prose would hide it
	// until the first real run
	r, _ := state.LoadOrFresh(state.Dir(), state.Session())

	if err := apply(r, cmd.Args()); err != nil {
		if ue, ok := errors.AsType[*state.UsageError](err); ok {
			return cli.Exit("f10-state: "+ue.Error(), 2)
		}

		return err
	}

	if r.Session == "" {
		return nil
	}

	now := time.Now()
	_ = r.Save(state.Dir(), now)

	recordClaim(ctx, r, kind, cmd.Args().Slice(), now)

	return nil
}

// recordClaim puts the call in the ledger as the model made it, beside the
// state it left. Its outcome never reaches the exit code.
func recordClaim(ctx context.Context, r *state.Run, kind string, args []string, now time.Time) {
	payload := ledger.Payload{
		"args":    args,
		"capture": r.Capture,
		"plan":    r.Plan,
		"ship":    r.Ship,
	}

	for key, v := range map[string]string{"leaf": r.Leaf, "final": r.Final, "note": r.Note, "next": r.Next, "ask": r.Ask} {
		if v != "" {
			payload[key] = v
		}
	}

	cwd, _ := os.Getwd()
	place := ledger.Where(ctx, cwd)

	ledger.Record(ctx, ledger.BusyAsync, ledger.Event{
		At:           now,
		Project:      place.Project,
		ProjectLocal: place.ProjectLocal,
		Checkout:     place.Checkout,
		Branch:       place.Branch,
		Session:      r.Session,
		Task:         r.Task,
		Kind:         kind,
		Source:       ledger.Claimed,
		Payload:      ledger.NewPayload(payload),
	})
}
