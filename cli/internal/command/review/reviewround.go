package review

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// runReviewRound needs no remote review declared: the host is read only for
// the PR a series belongs to, and a branch checked out elsewhere, or a
// subject that does not resolve, gets the rounds the directory holds.
func runReviewRound(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	var sweep reviews.Sweep
	if since := cmd.String("since"); since != "" {
		if sweep, err = reviews.CheckSweep(ctx, t, since, cmd.ErrWriter); err != nil {
			return err
		}

		if sweep.Decline != "" {
			return writeRounds(cmd, findings.Status{Decline: sweep.Decline, Branch: sweep.Branch, Detail: sweep.Detail})
		}
	}

	dir, series, err := reviews.OpenReviews(ctx, t, cmd.Args().First(), cmd.ErrWriter)
	if err != nil {
		return err
	}

	st := findings.Status{Dir: dir, Decline: findings.Merged, Latest: findings.Latest(dir)}
	if !series.Merged {
		if st, err = findings.Rounds(dir, findings.Series(dir)); err != nil {
			return err
		}
	}

	st.PR, st.Archived = series.Pr, series.Archived

	// a sweep starts a series of its own, so a directory holding any round
	// belongs to other work, or to this sweep's own earlier run
	if sweep.Branch != "" && st.Latest != "" {
		st.Decline, st.Round, st.Path, st.Branch = findings.Started, 0, "", sweep.Branch
		if sweep.Here {
			st.Branch, st.Detail = "", "here"
		}
	}

	return writeRounds(cmd, st)
}

func writeRounds(cmd *cli.Command, st findings.Status) error {
	if cmd.Bool("json") {
		if err := ui.JSON(cmd.Writer, st); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(cmd.Writer, roundsDoc(st)); err != nil {
		return err
	}

	if st.Decline != "" {
		return cli.Exit("", ui.ExitDeclined)
	}

	return nil
}

func roundsDoc(st findings.Status) string {
	var b strings.Builder

	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-8s %s\n", k+":", v)
		}
	}

	row("dir", st.Dir)
	row("archive", st.Archived)
	row("latest", st.Latest)
	row("pr", st.PR)
	row("branch", st.Branch)

	switch st.Decline {
	case findings.OnDefault:
		row("decline", "a sweep does not run on the default branch: git switch -c "+st.Branch)
	case findings.Swept:
		row("decline", st.Detail+": run the sweep on its own branch (git switch -c "+st.Branch+")")
	case findings.Started:
		if st.Detail == "here" {
			row("decline", "this branch already holds this range's sweep: run /f10:resolve, or sweep from a later ref")

			break
		}

		row("decline", "a sweep starts its own series, and this directory holds a round: "+
			"run it on a fresh branch (git switch -c "+st.Branch+")")
	case findings.Merged:
		row("decline", "already merged, nothing to review")
	case findings.Unsettled:
		row("decline", "round 1 has findings without a verdict: run /f10:resolve")
	case findings.Exhausted:
		why := "every finding is settled"
		if len(st.Open) > 0 {
			why = "these wait: run /f10:resolve"
		}

		row("decline", "round "+strconv.Itoa(findings.LastRound)+" is the last; "+why)
	default:
		row("round", strconv.Itoa(st.Round))
		row("path", st.Path)
	}

	for _, o := range st.Open {
		fmt.Fprintf(&b, "  %s %s - %s\n", o.ID, o.Title, o.Why)
	}

	return b.String()
}
