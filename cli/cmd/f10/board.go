package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The board noun is where every task of a project stands: done lately, in
// progress at which stage and who owes the next move, and what comes next.
// It reads the host, the tracker and this machine, each a source that can
// fail on its own, and derives nothing here: lookaround decides every
// stage, and the terminal, --json and --html draw the same rows.
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
			&cli.BoolFlag{
				Name:  "html",
				Usage: "emit one static HTML page: the Board JSON embedded, drawn by an inlined renderer; opens from disk",
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write --json or --html to this `PATH` instead of stdout",
			},
		},
		Action: runBoard,
	}
}

// boardWindow is how far back done rows go without --since.
const boardWindow = 7 * 24 * time.Hour

// boardPage is how many open tasks the board asks the host for.
const boardPage = 100

// The board's output formats; the empty one is the terminal.
const (
	boardJSON = "json"
	boardHTML = "html"
)

func runBoard(ctx context.Context, cmd *cli.Command) error {
	format, err := boardFormat(cmd)
	if err != nil {
		return err
	}

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

	if format == "" {
		renderBoard(cmd.Writer, board, termWidth(cmd.Writer))

		return nil
	}

	data, err := boardBytes(board, format)
	if err != nil {
		return err
	}

	return emitBoard(cmd.Writer, cmd.String("out"), data)
}

// boardFormat settles the output format from the flags before anything is
// collected: --json and --html are exclusive, and --out has no meaning for
// the terminal, whose width is the terminal's.
func boardFormat(cmd *cli.Command) (string, error) {
	switch {
	case cmd.Bool("json") && cmd.Bool("html"):
		return "", errors.New("--json and --html are exclusive: pick one")
	case cmd.Bool("json"):
		return boardJSON, nil
	case cmd.Bool("html"):
		return boardHTML, nil
	case cmd.String("out") != "":
		return "", errors.New("--out writes --json or --html: add one")
	default:
		return "", nil
	}
}

// boardBytes is the board in one of the file formats.
func boardBytes(b lookaround.Board, format string) ([]byte, error) {
	var buf bytes.Buffer

	var err error
	if format == boardHTML {
		err = renderBoardHTML(&buf, b)
	} else {
		err = encodeJSON(&buf, b)
	}

	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// emitBoard writes the rendered board to the path, else to the writer. The
// file is world-readable on purpose: it is a report meant to be attached
// or hosted as is.
func emitBoard(w io.Writer, path string, data []byte) error {
	if path == "" {
		_, err := w.Write(data)

		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a report meant to be attached or hosted
		return fmt.Errorf("writing the board: %w", err)
	}

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
			in.RemoteOpen = func(ctx context.Context, number int) (bool, error) {
				rv, err := newReviewer(ctx, h, t.eff.Review, strconv.Itoa(number))
				if err != nil {
					return false, err
				}

				switch _, err := rv.pick(ctx); {
				case err == nil:
					return true, nil
				case errors.Is(err, errNoReview):
					return false, nil
				default:
					return false, err
				}
			}
		}
	}

	in.Tasks = func(ctx context.Context) ([]lookaround.Task, error) {
		rows, err := taskRows(ctx, t, boardPage)
		if err != nil {
			return nil, err
		}

		return boardTasks(settlePriorities(rows, warn), t.eff.IDPrefix), nil
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

// boardTasks is tracker rows in the board's shape. The base is the row's
// `after`, else the After: line of its body, in the project's id format:
// a driver may send the bare number.
func boardTasks(rows []row, prefix string) []lookaround.Task {
	out := make([]lookaround.Task, 0, len(rows))

	for _, r := range rows {
		after := cmp.Or(r.After, afterIn(r.Body))
		if id := ref.Match(prefix, after); id != "" {
			after = id
		} else if prefix != "" && after != "" && strings.Trim(after, "0123456789") == "" {
			after = prefix + "-" + after
		}

		out = append(out, lookaround.Task{
			ID: r.ID, Title: r.Title, URL: r.URL, Status: r.Status, Priority: r.Priority, After: after,
		})
	}

	return out
}

// A boardModel is what a renderer draws: the sources worth a line, the
// sentence for an empty board, one line per row. board.html builds the
// same model in JavaScript; the JSON tags are the parity test's contract.
type boardModel struct {
	Notes []boardNote `json:"notes"`
	Empty string      `json:"empty,omitempty"`
	Rows  []boardLine `json:"rows"`
}

// A boardNote is a source that did not deliver: failed, with its error,
// or skipped on purpose.
type boardNote struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// A boardLine is one row, reduced to what a glance needs.
type boardLine struct {
	Group       string `json:"group"`
	Key         string `json:"key"`
	URL         string `json:"url,omitempty"`
	Stage       string `json:"stage"`
	StageFrom   string `json:"stage_from"`
	Title       string `json:"title,omitempty"`
	PR          int    `json:"pr,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	Who         string `json:"who,omitempty"`
	Next        string `json:"next,omitempty"`
	NextCommand bool   `json:"next_command,omitempty"`
}

// boardLines reduces the board to its model: the rows and the sources,
// never the header facts.
//
//nolint:lostfield // a reduction by design
func boardLines(b lookaround.Board) boardModel {
	m := boardModel{Notes: []boardNote{}, Rows: []boardLine{}}

	for _, s := range b.Sources {
		switch {
		case s.Skipped:
			m.Notes = append(m.Notes, boardNote{Name: s.Name, Status: "skipped"})
		case !s.OK:
			m.Notes = append(m.Notes, boardNote{Name: s.Name, Status: "failed", Error: s.Error})
		}
	}

	if len(b.Rows) == 0 {
		m.Empty = "nothing in flight, nothing next, nothing done since " + b.Window.Since.Local().Format("2006-01-02")

		return m
	}

	for _, r := range b.Rows {
		line := boardLine{
			Group:     boardGroup(r.Stage),
			Key:       r.Key,
			URL:       rowURL(r),
			Stage:     r.Stage,
			StageFrom: r.StageFrom,
			Who:       boardWho(r),
		}

		if r.Task != nil {
			line.Title = r.Task.Title
		} else if r.Where.PR != nil {
			line.Title = r.Where.PR.Title
		}

		if r.Where.PR != nil {
			line.PR, line.PRURL = r.Where.PR.Number, r.Where.PR.URL
		}

		if r.Next.Command != "" {
			line.Next, line.NextCommand = r.Next.Command, true
		} else {
			line.Next = r.Next.Text
		}

		m.Rows = append(m.Rows, line)
	}

	return m
}

// renderBoard draws the board for a terminal: failed sources first, then
// one line per row, grouped by stage in the board's order: the key, the
// stage in the badge's palette, the title cut to fit, who, the request,
// and the next action. --json is the contract; this is a glance at it.
func renderBoard(w io.Writer, b lookaround.Board, limit int) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	m := boardLines(b)

	for _, n := range m.Notes {
		if n.Status == "skipped" {
			fmt.Fprintln(w, faint.Render(n.Name+"  skipped"))
		} else {
			fmt.Fprintln(w, red.Render(n.Name+"  failed")+"  "+faint.Render(n.Error))
		}
	}

	if m.Empty != "" {
		fmt.Fprintln(w, m.Empty)

		return
	}

	keyW, stageW := 0, 0
	for _, r := range m.Rows {
		keyW, stageW = max(keyW, len(r.Key)), max(stageW, len(r.Stage))
	}

	var last string

	for _, r := range m.Rows {
		if r.Group != last {
			fmt.Fprintf(w, "\n%s\n", faint.Render(r.Group))
			last = r.Group
		}

		var tail []string

		if r.PR != 0 {
			tail = append(tail, fmt.Sprintf("#%d", r.PR))
		}

		if r.Who != "" {
			tail = append(tail, r.Who)
		}

		if r.Next != "" {
			tail = append(tail, r.Next)
		}

		// cut on runes: a byte offset would split a multibyte character
		title := r.Title
		fixed := keyW + 2 + stageW + 2 + 7 + 2 + len(strings.Join(tail, "  ")) + 4

		if runes := []rune(title); limit > 0 && len(runes) > limit-fixed && limit-fixed > 8 {
			title = string(runes[:limit-fixed-1]) + "…"
		}

		// local is the common case at a glance; the HTML shows every origin
		from := ""
		if r.StageFrom != lookaround.FromLocal {
			from = r.StageFrom
		}

		key := r.Key
		if limit > 0 {
			key = hyperlink(r.URL, r.Key)
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
