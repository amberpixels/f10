package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// exitDeclined is `review round`'s "no next round": a normal end for the
// review step, which prints the decline and stops.
const exitDeclined = 3

// runReviewRound reads only the storage root, so a project with no remote
// review declared runs it too.
func runReviewRound(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	name, err := reviewsKey(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	dir := filepath.Join(t.lay.StorageRoot, "reviews", name)

	st, err := findings.Rounds(dir, numbered(dir))
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(st); err != nil {
			return fmt.Errorf("encoding rounds: %w", err)
		}
	} else if _, err := fmt.Fprint(cmd.Writer, roundsDoc(st)); err != nil {
		return err
	}

	if st.Decline != "" {
		return cli.Exit("", exitDeclined)
	}

	return nil
}

// reviewsKey names a task's findings directory: the task id (a bundle's
// lead), else the branch with `/` as `-` for work no task names.
func reviewsKey(ctx context.Context, t *target, explicit string) (string, error) {
	r, err := t.reference(ctx, explicit)
	if err == nil {
		return r.ID, nil
	}

	if !errors.Is(err, ref.ErrNoReference) {
		return "", err
	}

	branch := gitx.CurrentBranch(ctx, t.dir)
	if branch == "" {
		return "", fmt.Errorf("%w, and no branch is checked out to name the findings directory", err)
	}

	return strings.ReplaceAll(branch, "/", "-"), nil
}

func roundsDoc(st findings.Status) string {
	var b strings.Builder

	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-8s %s\n", k+":", v)
		}
	}

	row("dir", st.Dir)
	row("latest", st.Latest)

	switch st.Decline {
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
