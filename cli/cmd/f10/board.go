package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The board noun is where every task of a project stands: done lately, in
// progress at which stage and who owes the next move, and what comes next.
// It reads the host, the tracker and this machine, each a source that can
// fail on its own, and derives nothing here: lookaround decides every
// stage, and the terminal and --json draw the same rows.
func boardCommand() *cli.Command {
	return &cli.Command{
		Name:  "board",
		Usage: "where every task of this project stands: done, in progress with its stage and next action, next",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "since",
				Usage: "the window done rows cover: an RFC 3339 time, or a `DATE` (YYYY-MM-DD, local midnight); default 7 days",
			},
			&cli.BoolFlag{
				Name:  "remote",
				Usage: "read the host and the tracker only, as a viewer without the checkout would",
			},
			&cli.BoolFlag{Name: "json", Usage: "emit the Board JSON (the schema is docs/board.schema.json)"},
		},
		Action: runBoard,
	}
}

// boardWindow is how far back done rows go without --since.
const boardWindow = 7 * 24 * time.Hour

// boardPage is how many open tasks the board asks the host for.
const boardPage = 100

func runBoard(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	now := time.Now()
	since := now.Add(-boardWindow)

	if cmd.IsSet("since") {
		if since, err = parseSince(cmd.String("since")); err != nil {
			return err
		}
	}

	in := boardInputs(ctx, t, cmd.ErrWriter, since, cmd.Bool("remote"))

	board := lookaround.Derive(lookaround.Collect(ctx, in), now, since)

	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, board)
	}

	renderBoard(cmd.Writer, board, termWidth(cmd.Writer))

	return nil
}

// boardInputs binds the collectors to the target: the host CLI the probe
// routed to, the tracker through the driver or the host's issues, and
// this machine's state, worktrees, plans and files.
func boardInputs(ctx context.Context, t *target, warn io.Writer, since time.Time, remote bool) lookaround.Inputs {
	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	place := ledger.Where(ctx, main)

	in := lookaround.Inputs{
		Project: lookaround.Project{ID: place.Project, Name: t.lay.Project, Remote: remote},
		Prefix:  t.eff.IDPrefix,
		Since:   since,
		Remote:  remote,
	}

	if !remote {
		in.Project.Checkout = main
	}

	h, err := t.host()
	if err != nil {
		in.HostError = err.Error()
	} else {
		in.Host = &h.Host
		in.Project.Host = h.Name

		if t.eff.Review.Remote() {
			in.RemoteOpen = func(ctx context.Context, number int) bool {
				rv, err := newReviewer(ctx, h, t.eff.Review, strconv.Itoa(number))
				if err != nil {
					return false
				}

				_, err = rv.pick(ctx)

				return err == nil
			}
		}
	}

	in.Tasks = func(ctx context.Context) ([]lookaround.Task, error) {
		rows, err := taskRows(ctx, t, boardPage)
		if err != nil {
			return nil, err
		}

		return boardTasks(settlePriorities(rows, warn)), nil
	}

	if !remote {
		in.Local = &lookaround.LocalInputs{
			Main:       main,
			StateDir:   state.Dir(),
			TTL:        state.TTL(),
			LedgerPath: ledger.Path(),
			ProjectID:  place.Project,
			Herdr:      herdr.Available() == nil,
		}

		if dir := t.sharedPlansDir(); dir != "" {
			in.Local.SharedRoot = t.lay.StorageRoot
		}
	}

	return in
}

// boardTasks is tracker rows in the board's shape.
func boardTasks(rows []row) []lookaround.Task {
	out := make([]lookaround.Task, 0, len(rows))

	for _, r := range rows {
		out = append(out, lookaround.Task{
			ID: r.ID, Title: r.Title, URL: r.URL, Status: r.Status, Priority: r.Priority, After: r.After,
		})
	}

	return out
}

// renderBoard draws the board for a terminal: failed sources first, then
// one line per row, grouped by stage in the board's order: the key, the
// stage in the badge's palette, the title cut to fit, who, the request,
// and the next action. --json is the contract; this is a glance at it.
func renderBoard(w io.Writer, b lookaround.Board, limit int) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	for _, s := range b.Sources {
		switch {
		case s.Skipped:
			fmt.Fprintln(w, faint.Render(s.Name+"  skipped"))
		case !s.OK:
			fmt.Fprintln(w, red.Render(s.Name+"  failed")+"  "+faint.Render(s.Error))
		}
	}

	if len(b.Rows) == 0 {
		fmt.Fprintln(
			w,
			"nothing in flight, nothing next, nothing done since "+b.Window.Since.Local().Format("2006-01-02"),
		)

		return
	}

	keyW, stageW := 0, 0
	for _, r := range b.Rows {
		keyW, stageW = max(keyW, len(r.Key)), max(stageW, len(r.Stage))
	}

	var last string

	for _, r := range b.Rows {
		if group := boardGroup(r.Stage); group != last {
			fmt.Fprintf(w, "\n%s\n", faint.Render(group))
			last = group
		}

		title := ""
		if r.Task != nil {
			title = r.Task.Title
		} else if r.Where.PR != nil {
			title = r.Where.PR.Title
		}

		next := cmp.Or(r.Next.Command, r.Next.Text)

		var tail []string

		if r.Where.PR != nil {
			tail = append(tail, fmt.Sprintf("#%d", r.Where.PR.Number))
		}

		if who := boardWho(r); who != "" {
			tail = append(tail, who)
		}

		if next != "" {
			tail = append(tail, next)
		}

		fixed := keyW + 2 + stageW + 2 + 7 + 2 + len(strings.Join(tail, "  ")) + 4
		if limit > 0 && len(title) > limit-fixed && limit-fixed > 8 {
			title = title[:limit-fixed-1] + "…"
		}

		from := ""
		if r.StageFrom != lookaround.FromLocal {
			from = r.StageFrom
		}

		key := r.Key
		if limit > 0 {
			key = hyperlink(rowURL(r), r.Key)
		}

		fmt.Fprintf(w, "%s%s  %s  %s  %s  %s\n",
			key, strings.Repeat(" ", keyW-len(r.Key)),
			stageStyle(r.Stage).Render(fmt.Sprintf("%-*s", stageW, r.Stage)),
			faint.Render(fmt.Sprintf("%-7s", from)),
			title, faint.Render(strings.Join(tail, "  ")))
	}
}

// boardGroup is the heading a stage sits under.
func boardGroup(stage string) string {
	switch stage {
	case lookaround.StageNext:
		return "next"
	case lookaround.StageDone:
		return "done"
	default:
		return "in progress"
	}
}

func boardWho(r lookaround.Row) string {
	switch {
	case r.Who.Agent != "":
		return strings.TrimSpace(r.Who.Agent + " " + r.Who.AgentStatus)
	case len(r.Who.Assignees) > 0:
		return r.Who.Assignees[0]
	default:
		return r.Who.Author
	}
}

func rowURL(r lookaround.Row) string {
	if r.Task != nil && r.Task.URL != "" {
		return r.Task.URL
	}

	if r.Where.PR != nil {
		return r.Where.PR.URL
	}

	return ""
}

// stageStyle is the badge's palette on the board's stages: done green,
// halted or asking red, implementing yellow, reviewing orange-ish
// (magenta here, the nearest ANSI), planning blue, next dim.
func stageStyle(stage string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch stage {
	case lookaround.StageDone:
		return s.Foreground(lipgloss.Color("2"))
	case lookaround.StageReady:
		return s.Bold(true).Foreground(lipgloss.Color("2"))
	case lookaround.StageAsksWaiting, lookaround.StageBlocked:
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case lookaround.StageImplementing:
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case lookaround.StageInReview, lookaround.StagePROpen:
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case lookaround.StagePlanned:
		return s.Foreground(lipgloss.Color("4"))
	case lookaround.StageMergedUnfinished:
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	default:
		return s.Faint(true)
	}
}
