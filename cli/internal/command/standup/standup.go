package standup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the standup noun, which changes nothing, so it does not fetch. The user is their
// git email and host login, never a name two people can share. Only `mark`
// moves the period, so a script calling --json does not move the next start.
func Command() *cli.Command {
	return &cli.Command{
		Name: "standup",
		Usage: "what you did since your last standup: commits and PRs, grouped by project and task " +
			"(this checkout, or several with -C, --group, --all)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "since",
				Usage: "start the period here instead: an RFC 3339 time, or a `DATE` (YYYY-MM-DD, local midnight)",
			},
			&cli.BoolFlag{Name: "week", Usage: "start the period at last week's Monday"},
			&cli.StringSliceFlag{
				Name:  "group",
				Usage: "read the checkouts of `NAME`, a group in ~/.f10/standup.md, instead of this one; repeatable",
			},
			&cli.BoolFlag{Name: "all", Usage: "read every checkout under F10_ROOTS instead of this one"},
			ui.JSONFlag("emit the report as JSON (the schema is in docs/cli.md)"),
		},
		Action: runStandup,
		Commands: []*cli.Command{
			{
				Name:     "mark",
				Hidden:   true,
				Category: command.Plumbing,
				Usage:    "record a standup as reported: the next period starts when this one ended",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "since",
						Usage:    "the `TIME` the reported standup started at, as its report gave it",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "at",
						Usage: "the `TIME` the reported standup was read at, where the next one starts (default: now)",
					},
				},
				Action: runStandupMark,
			},
		},
	}
}

func runStandup(ctx context.Context, cmd *cli.Command) error {
	if cmd.IsSet("since") && cmd.Bool("week") {
		return errors.New("--since and --week each set the start: pass one")
	}

	var since time.Time

	if cmd.IsSet("since") {
		var err error
		if since, err = ParseSince(cmd.String("since")); err != nil {
			return err
		}
	}

	last, err := scope.ReadLastStandup()
	if err != nil {
		return err
	}

	now := time.Now()
	start, period := standup.Resolve(now, since, cmd.Bool("week"), last)

	// with no standup stored, one wider collection finds the last working
	// day with activity, and the same input is grouped again from it
	from := start
	if period == standup.PeriodLastWorkingDay {
		from = calendar.FallbackFrom(now)
	}

	targets, err := scope.StandupTargets(ctx, cmd)
	if err != nil {
		return err
	}

	ins, err := collectAll(ctx, targets, from)
	if err != nil {
		return err
	}

	// only the fallback reads the working days, so a broken standup.md
	// fails no run that would not use it; the day is the person's, so it is
	// found over every repository at once
	if period == standup.PeriodLastWorkingDay {
		workdays, err := scope.ReadWorkdays()
		if err != nil {
			return err
		}

		var activity []time.Time
		for _, in := range ins {
			activity = append(activity, standup.ActivityTimes(standup.Group(in))...)
		}

		start = calendar.LastActiveWorkday(now, workdays, activity)
		for i := range ins {
			ins[i].Since = start
		}
	}

	reports := make([]standup.Report, len(ins))
	for i, in := range ins {
		reports[i] = standup.Group(in)
	}

	rep := standup.Merge(start, period, reports)
	standup.SetDays(&rep, time.Local)

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, rep)
	}

	links := render.LinksFor(cmd.Writer, false)
	LoadStandupLinks(ctx, links, rep)

	return RenderStandup(cmd.Writer, links, rep)
}

// LoadStandupLinks resolves each project's ids in its own ledger project,
// found from its first repository, keyed by the project's name.
func LoadStandupLinks(ctx context.Context, links *render.TaskLinks, rep standup.Standup) {
	if links == nil || !links.On {
		return
	}

	for _, p := range rep.Projects {
		if len(p.Repos) == 0 || len(p.Tasks) == 0 {
			continue
		}

		ids := make([]string, 0, len(p.Tasks))
		for _, t := range p.Tasks {
			ids = append(append(ids, t.ID), t.Riders...)
		}

		links.LoadAs(ctx, target.ProjectOf(ctx, p.Repos[0].Checkout), p.Name, ids...)
	}
}

// runStandupMark takes the start the report showed rather than working it
// out again, so the record and the report agree across midnight and scopes.
// --at is when the report was read: work done since falls in the next one.
func runStandupMark(_ context.Context, cmd *cli.Command) error {
	since, err := ParseSince(cmd.String("since"))
	if err != nil {
		return err
	}

	now := time.Now()
	at := now

	if cmd.IsSet("at") {
		if at, err = ParseSince(cmd.String("at")); err != nil {
			return fmt.Errorf("--at: %w", err)
		}
	}

	switch {
	case at.After(now):
		return fmt.Errorf("--at %s is in the future", at.Format(time.RFC3339))
	case since.After(at):
		return fmt.Errorf(
			"--since %s is after the standup was read (%s)",
			since.Format(time.RFC3339),
			at.Format(time.RFC3339),
		)
	}

	return scope.WriteLastStandup(standup.Last{At: at.UTC(), Since: since.UTC()})
}

// ParseSince reads --since. The period's other forms (since the last
// standup, a week) are not spellings of this one.
func ParseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	p, err := calendar.Parse(s, time.Now(), calendar.Instants, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q: want an RFC 3339 time or a YYYY-MM-DD date", s)
	}

	return p.Start, nil
}
