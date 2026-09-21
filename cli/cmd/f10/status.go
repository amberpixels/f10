package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/resolve"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The status noun reads what the badge reads, and says the rest: which
// step a run stopped on, why, and what unblocks it. Inside a session the
// session id is in the environment and the answer is that one run; from a
// plain terminal there is none, so it lists this repo's live runs, and
// --all every live run on the machine.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "where this session's f10 run is, or every live run in this repo",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "every live run on this machine, not only this repo's"},
			&cli.BoolFlag{Name: "json", Usage: "emit the same runs as JSON"},
		},
		Action: runStatus,
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	dir := state.Dir()
	now := time.Now()
	all := cmd.Bool("all")

	var (
		runs     []*state.Run
		showRoot bool
	)

	switch sid := state.Session(); {
	case sid != "" && !all:
		r, err := state.Load(state.Path(dir, sid))
		if errors.Is(err, os.ErrNotExist) {
			_, err = fmt.Fprintln(cmd.Writer, "no f10 run in this session")

			return err
		}

		if err != nil {
			return err
		}

		runs = []*state.Run{r}
	default:
		live, err := state.List(dir, now, state.TTL())
		if err != nil {
			return err
		}

		showRoot = true
		runs = live

		if !all {
			root, err := checkoutRoot(ctx, cmd)
			if err != nil {
				return err
			}

			runs = inRoot(live, root)

			if len(runs) == 0 {
				_, err = fmt.Fprintf(cmd.Writer, "no live f10 run in %s (--all lists every session)\n", root)

				return err
			}
		}

		if len(runs) == 0 {
			_, err = fmt.Fprintln(cmd.Writer, "no live f10 run on this machine")

			return err
		}
	}

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(runs); err != nil {
			return fmt.Errorf("encoding runs: %w", err)
		}

		return nil
	}

	limit := termWidth(cmd.Writer)

	for i, r := range runs {
		if i > 0 {
			fmt.Fprintln(cmd.Writer)
		}

		renderRun(cmd.Writer, r, showRoot, now, limit)
	}

	return nil
}

// checkoutRoot is the repo a plain terminal asks about: -C's target or the
// cwd, resolved the way the seed hook resolved it when it recorded root.
func checkoutRoot(ctx context.Context, cmd *cli.Command) (string, error) {
	dir, _, err := rootDir(cmd)
	if err != nil {
		return "", err
	}

	return resolve.Resolve(ctx, dir).CheckoutRoot, nil
}

// inRoot keeps the runs recorded against root. The hook stores git's
// toplevel and the resolver stores the physical path, so both sides are
// compared in physical form.
func inRoot(runs []*state.Run, root string) []*state.Run {
	var kept []*state.Run

	for _, r := range runs {
		if r.Root != "" && canonPath(r.Root) == canonPath(root) {
			kept = append(kept, r)
		}
	}

	return kept
}

func canonPath(p string) string {
	if c, err := filepath.EvalSymlinks(p); err == nil {
		return c
	}

	return p
}

// renderRun prints one run as the label/value block config uses, in the
// order the script's show prints the same facts. The root row appears only
// when listing: for one session it is the directory you are in.
func renderRun(w io.Writer, r *state.Run, showRoot bool, now time.Time, limit int) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	headerRow(w, "task", valueOr(r.Task), limit, plain)
	headerRow(w, "url", valueOr(r.URL), limit, plain)

	if showRoot {
		headerRow(w, "root", valueOr(r.Root), limit, faint)
	}

	for _, p := range state.Phases {
		headerRow(w, p, r.PhaseText(p), limit, statusStyle(r.Status(p)))
	}

	if r.Note != "" {
		headerRow(w, "note", r.Note, limit, plain)
	}

	if r.Next != "" {
		headerRow(w, "next", r.Next, limit, plain)
	}

	if r.Final != "" {
		headerRow(w, "final", r.Final, limit, faint)
	}

	age := ago(now, r.Updated)
	if !r.Live(now, state.TTL()) {
		age += " (stale)"
	}

	headerRow(w, "updated", age, limit, faint)
}

func valueOr(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// statusStyle is the badge's palette: shape first, color second, so the
// same status reads the same in the status line and here.
func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch status {
	case "done":
		return s.Foreground(lipgloss.Color("2"))
	case "running":
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	case "failed":
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case "partial":
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case "blocked":
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case "prior":
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	default:
		return s.Faint(true)
	}
}

// ago is a duration a human reads at a glance: seconds under a minute,
// then the largest whole unit that fits.
func ago(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}

	d := now.Sub(then).Round(time.Second)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
