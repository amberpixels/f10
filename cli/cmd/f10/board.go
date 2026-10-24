package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/forge"
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
// stage, and the terminal, --json and --html draw the same rows. A scope
// (-C, --group, --all) reads several projects, each on its own, and a
// repository with no checkout here reads from its host alone.
func boardCommand() *cli.Command {
	return &cli.Command{
		Name: "board",
		Usage: "where every task stands: done, in progress with its stage and next action, next " +
			"(this project, or several with -C, --group, --all)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "since",
				Usage: "the window done rows cover: an RFC 3339 time, or a `DATE` (YYYY-MM-DD, local midnight); default 7 days",
			},
			&cli.BoolFlag{
				Name:  "remote",
				Usage: "read the host and the tracker only, as a viewer without the checkout would",
			},
			&cli.StringSliceFlag{
				Name:  "group",
				Usage: "read the projects of `NAME`, a group in ~/.f10/board.md, instead of this one; repeatable",
			},
			&cli.BoolFlag{
				Name:  "all",
				Usage: "read every checkout under F10_ROOTS, and the repos ~/.f10/board.md watches",
			},
			&cli.BoolFlag{Name: "detail", Usage: "with --all or --group, every project's rows instead of a line each"},
			&cli.StringFlag{Name: "by", Usage: "group rows by `person` across projects"},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit the Board JSON (the schema is docs/board.schema.json); an array over several projects",
			},
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

	now := time.Now()
	since := now.Add(-boardWindow)

	if cmd.IsSet("since") {
		var err error
		if since, err = parseSince(cmd.String("since")); err != nil {
			return err
		}
	}

	by := strings.TrimSpace(cmd.String("by"))
	if by != "" && by != "person" {
		return fmt.Errorf("--by %q: rows group by person only", by)
	}

	cfg, cfgPath, err := readBoardConfig()
	if err != nil {
		return err
	}

	people := boardPeople(cfg.People)
	remote := cmd.Bool("remote")

	// one pipeline whatever the scope: this checkout is a scope of one
	var entries []scopeEntry

	if scoped(cmd) {
		items, err := scopeItems(cmd, boardConfigFile, cfg.Repos, cfgPath)
		if err != nil {
			return err
		}

		if entries = bindScope(ctx, items, cmd.Name); len(entries) == 0 {
			return errors.New("the scope names no project: the groups and roots given matched none")
		}
	} else {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		entries = []scopeEntry{{dir: t.dir, target: t}}
	}

	boards := collectBoards(ctx, entries, cmd.ErrWriter, now, since, remote, people)

	// several projects are --all, --group or more than one -C: their JSON
	// is an array, the page draws one project, and their glance is a line
	// each unless --detail asks for the rows; one project, named or not,
	// is one document and its rows
	several := cmd.Bool("all") || len(cmd.StringSlice("group")) > 0 || len(checkoutFlags(cmd)) > 1
	summary := (cmd.Bool("all") || len(cmd.StringSlice("group")) > 0) && !cmd.Bool("detail")

	// the page draws one project; several boards are one JSON array
	switch {
	case format == boardHTML && several:
		return errors.New("--html draws one project: name one with -C, or none")
	case format != "" && several:
		var buf bytes.Buffer
		if err := encodeJSON(&buf, boards); err != nil {
			return err
		}

		return emitBoard(cmd.Writer, cmd.String("out"), buf.Bytes())
	case format != "":
		data, err := boardBytes(boards[0], format)
		if err != nil {
			return err
		}

		return emitBoard(cmd.Writer, cmd.String("out"), data)
	case by == "person":
		renderByPerson(cmd.Writer, boards, termWidth(cmd.Writer))
	case summary:
		renderSummary(cmd.Writer, boards, now, termWidth(cmd.Writer))
	case !several:
		renderBoard(cmd.Writer, boards[0], termWidth(cmd.Writer))
	default:
		faint := lipgloss.NewStyle().Faint(true).Bold(true)

		for i, b := range boards {
			if i > 0 {
				fmt.Fprintln(cmd.Writer)
			}

			fmt.Fprintln(cmd.Writer, faint.Render("## "+b.Project.Name))
			renderBoard(cmd.Writer, b, termWidth(cmd.Writer))
		}
	}

	return nil
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

// boardPeople is the people map in lookaround's shape.
func boardPeople(people []person) []lookaround.Person {
	out := make([]lookaround.Person, 0, len(people))
	for _, p := range people {
		out = append(out, lookaround.Person{Name: p.Name, Tracker: p.Tracker, Host: p.Host, Emails: p.Emails})
	}

	return out
}

// collectBoards derives one board per scope entry, a few at a time,
// keeping the scope's order. An entry that could not be bound is a board
// whose sources all failed; every other one still renders.
func collectBoards(
	ctx context.Context, entries []scopeEntry, warn io.Writer, now, since time.Time, remote bool,
	people []lookaround.Person,
) []lookaround.Board {
	boards := make([]lookaround.Board, len(entries))
	sem := make(chan struct{}, scopeParallel)

	var wg sync.WaitGroup

	for i, e := range entries {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			var in lookaround.Inputs

			switch {
			case e.err != nil:
				p := lookaround.Project{ID: e.id, Name: filepath.Base(e.main), Checkout: e.main}
				boards[i] = lookaround.Unreachable(p, e.err, now, since)

				return
			case e.repo != nil:
				in = repoInputs(*e.repo, warn, since)
			default:
				in = boardInputs(ctx, e.target, warn, since, remote)
			}

			in.People = people
			boards[i] = lookaround.Derive(lookaround.Collect(ctx, in), now, since)
		})
	}

	wg.Wait()

	return boards
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

// repoInputs binds the collectors to a repository with no checkout: the
// host CLI named outright for its requests and its issues, no driver, no
// priority labels, and nothing local, as --remote reads.
func repoInputs(r repoRef, warn io.Writer, since time.Time) lookaround.Inputs {
	h := host{forge.Host{Name: r.CLI, Repo: r.repo()}}

	return lookaround.Inputs{
		Project: lookaround.Project{ID: r.id(), Name: r.name(), Host: r.CLI, Remote: true},
		Prefix:  r.Prefix,
		Since:   since,
		Remote:  true,
		Host:    &h.Host,
		Tasks: func(ctx context.Context) ([]lookaround.Task, error) {
			rows, err := h.listIssues(ctx, "", r.Prefix, nil, boardPage)
			if err != nil {
				return nil, err
			}

			return boardTasks(settlePriorities(rows, warn), r.Prefix), nil
		},
	}
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
			ID: r.ID, Title: r.Title, URL: r.URL, Status: r.Status, Priority: r.Priority, Assignee: r.Assignee,
			After: after,
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
		m.Rows = append(m.Rows, lineOf(r))
	}

	return m
}

// lineOf is one row reduced to its line.
func lineOf(r lookaround.Row) boardLine {
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
		line.Title = titleAfterKey(r.Where.PR.Title, r.Key)
	}

	if r.Where.PR != nil {
		line.PR, line.PRURL = r.Where.PR.Number, r.Where.PR.URL
	}

	if r.Next.Command != "" {
		line.Next, line.NextCommand = r.Next.Command, true
	} else {
		line.Next = r.Next.Text
	}

	return line
}

// titleAfterKey drops the "<key>: " a request's title often starts with,
// since the row already shows the key; FT-12 never matches FT-121.
func titleAfterKey(title, key string) string {
	rest, ok := strings.CutPrefix(title, key)
	if !ok || key == "" || rest == "" || (rest[0] != ':' && rest[0] != ' ') {
		return title
	}

	return cmp.Or(strings.TrimLeft(rest, ": -"), title)
}

// renderBoard draws the board for a terminal: failed sources first, then
// one line per row, grouped by stage in the board's order. --json is the
// contract; this is a glance at it.
func renderBoard(w io.Writer, b lookaround.Board, limit int) {
	m := boardLines(b)
	renderSources(w, b, "")

	if m.Empty != "" {
		fmt.Fprintln(w, m.Empty)

		return
	}

	lay := newBoardLayout(m.Rows, nil, limit)

	var last string

	for _, r := range m.Rows {
		if r.Group != last {
			fmt.Fprintf(w, "\n%s\n", lay.faint.Render(r.Group))
			last = r.Group
		}

		lay.line(w, r, "")
	}
}

// renderSources prints the sources that did not run: failed with their
// error, skipped on purpose; under the project's name where rows of
// several are drawn together.
func renderSources(w io.Writer, b lookaround.Board, project string) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	lead := ""
	if project != "" {
		lead = faint.Render(project) + "  "
	}

	for _, n := range boardLines(b).Notes {
		if n.Status == "skipped" {
			fmt.Fprintln(w, lead+faint.Render(n.Name+"  skipped"))
		} else {
			fmt.Fprintln(w, lead+red.Render(n.Name+"  failed")+"  "+faint.Render(n.Error))
		}
	}
}

// renderSummary is one line per project, for a scope read at a glance:
// how much is in flight, how much of it waits on a person (a next action
// that is a command: an ask to answer, a review to resolve, a merge to
// finish, a plan to ship), the oldest of it, and what failed.
func renderSummary(w io.Writer, boards []lookaround.Board, now time.Time, limit int) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	nameW := 0
	for _, b := range boards {
		nameW = max(nameW, len(b.Project.Name))
	}

	for _, b := range boards {
		var (
			inFlight, waiting int
			oldest            lookaround.Row
		)

		for _, r := range b.Rows {
			if r.Stage == lookaround.StageDone || r.Stage == lookaround.StageNext {
				continue
			}

			inFlight++

			if r.Next.Command != "" {
				waiting++
			}

			if !r.LastActivity.IsZero() && (oldest.Key == "" || r.LastActivity.Before(oldest.LastActivity)) {
				oldest = r
			}
		}

		var parts []string

		// one line per project: an error keeps its first line
		failed := unreachable(b)

		switch {
		case failed != "":
			parts = append(parts, red.Render("failed")+"  "+faint.Render(cut(firstLine(failed), limit-nameW-10)))
		case inFlight == 0:
			parts = append(parts, faint.Render("nothing in flight"))
		default:
			parts = append(parts, fmt.Sprintf("%d in flight", inFlight), fmt.Sprintf("%d waiting on you", waiting))

			if oldest.Key != "" {
				parts = append(parts, "oldest "+oldest.Key+" "+age(now, oldest.LastActivity))
			}
		}

		if failed == "" {
			for _, s := range b.Sources {
				if !s.OK && !s.Skipped {
					parts = append(
						parts,
						red.Render(s.Name+" failed")+"  "+faint.Render(cut(firstLine(s.Error), limit-nameW-40)),
					)
				}
			}
		}

		fmt.Fprintf(w, "%-*s  %s\n", nameW, b.Project.Name, strings.Join(parts, "  "))
	}
}

// unreachable is the one error a board of a project nothing could read
// carries on every source, else "".
func unreachable(b lookaround.Board) string {
	err := ""

	for _, s := range b.Sources {
		switch {
		case s.Skipped:
		case s.OK || err != "" && s.Error != err:
			return ""
		default:
			err = s.Error
		}
	}

	return err
}

// renderByPerson is every project's rows under the person each groups
// by, the rows with nobody last, each row naming its project.
func renderByPerson(w io.Writer, boards []lookaround.Board, limit int) {
	type entry struct {
		project string
		row     lookaround.Row
	}

	var (
		rows  []boardLine
		names []string
		by    = map[string][]entry{}
	)

	for _, b := range boards {
		label := ""
		if len(boards) > 1 {
			label = b.Project.Name
		}

		renderSources(w, b, label)
		names = append(names, b.Project.Name)

		for _, r := range b.Rows {
			by[r.Who.Person] = append(by[r.Who.Person], entry{project: b.Project.Name, row: r})
			rows = append(rows, lineOf(r))
		}
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "nothing in flight, nothing next, nothing done in the window")

		return
	}

	if len(boards) == 1 {
		names = nil
	}

	lay := newBoardLayout(rows, names, limit)

	persons := slices.Sorted(maps.Keys(by))
	if i := slices.Index(persons, ""); i >= 0 {
		persons = append(slices.Delete(persons, i, i+1), "")
	}

	for _, name := range persons {
		fmt.Fprintf(w, "\n%s\n", lay.faint.Render(cmp.Or(name, "nobody")))

		entries := by[name]
		slices.SortStableFunc(entries, func(a, c entry) int {
			if d := slices.Index(
				lookaround.Stages,
				a.row.Stage,
			) - slices.Index(
				lookaround.Stages,
				c.row.Stage,
			); d != 0 {
				return d
			}

			return c.row.LastActivity.Compare(a.row.LastActivity)
		})

		for _, e := range entries {
			lay.line(w, lineOf(e.row), e.project)
		}
	}
}

// age is how long ago t was, in the coarsest unit that is not zero.
func age(now, t time.Time) string {
	d := now.Sub(t)

	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 0))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

// cut shortens s to n runes with an ellipsis; a limit of zero or less
// leaves it whole.
func cut(s string, n int) string {
	runes := []rune(s)
	if n <= 8 || len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

// A boardLayout is the column widths one set of lines needs: the key, the
// stage, and the project when lines of several are drawn together.
type boardLayout struct {
	keyW, stageW, projW, limit int
	faint                      lipgloss.Style
}

func newBoardLayout(rows []boardLine, projects []string, limit int) boardLayout {
	lay := boardLayout{limit: limit, faint: lipgloss.NewStyle().Faint(true)}

	for _, r := range rows {
		lay.keyW, lay.stageW = max(lay.keyW, len(r.Key)), max(lay.stageW, len(r.Stage))
	}

	for _, p := range projects {
		lay.projW = max(lay.projW, len(p))
	}

	return lay
}

// line draws one row: the project where several are drawn together, the
// key, the stage in the badge's palette, where its evidence came from,
// the title cut to fit, then the request, who, and the next action.
func (lay boardLayout) line(w io.Writer, r boardLine, project string) {
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
	fixed := lay.keyW + 2 + lay.stageW + 2 + 7 + 2 + len(strings.Join(tail, "  ")) + 4

	if lay.projW > 0 {
		fixed += lay.projW + 2
	}

	if runes := []rune(title); lay.limit > 0 && len(runes) > lay.limit-fixed && lay.limit-fixed > 8 {
		title = string(runes[:lay.limit-fixed-1]) + "…"
	}

	// local is the common case at a glance; the HTML shows every origin
	from := ""
	if r.StageFrom != lookaround.FromLocal {
		from = r.StageFrom
	}

	key := r.Key
	if lay.limit > 0 {
		key = hyperlink(r.URL, r.Key)
	}

	lead := ""
	if lay.projW > 0 {
		lead = lay.faint.Render(fmt.Sprintf("%-*s", lay.projW, project)) + "  "
	}

	fmt.Fprintf(w, "%s%s%s  %s  %s  %s  %s\n",
		lead, key, strings.Repeat(" ", lay.keyW-len(r.Key)),
		stageStyle(r.Stage).Render(fmt.Sprintf("%-*s", lay.stageW, r.Stage)),
		lay.faint.Render(fmt.Sprintf("%-7s", from)),
		title, lay.faint.Render(strings.Join(tail, "  ")))
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
