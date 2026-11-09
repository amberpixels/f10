package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// renderBoard is a glance at the board; --json is the contract.
func renderBoard(w io.Writer, b lookaround.Board, limit int) {
	m := boardLines(b)
	faint := lipgloss.NewStyle().Faint(true)

	if m.Header != "" {
		fmt.Fprintln(w, faint.Bold(true).Render(m.Header))
	}

	renderSources(w, b, "")

	if m.Empty != "" {
		fmt.Fprintln(w, m.Empty)
	}

	if m.Quiet != "" {
		defer fmt.Fprintf(w, "\n%s\n", faint.Render(m.Quiet))
	}

	if m.Empty != "" {
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

func renderSources(w io.Writer, b lookaround.Board, project string) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	lead := ""
	if project != "" {
		lead = faint.Render(project) + "  "
	}

	for _, n := range boardLines(b).Notes {
		switch n.Status {
		case "skipped":
			fmt.Fprintln(w, lead+faint.Render(n.Name+"  skipped"))
		case localOnly:
			fmt.Fprintln(w, lead+faint.Render(localOnly))
		default:
			fmt.Fprintln(w, lead+red.Render(n.Name+"  failed")+"  "+faint.Render(n.Error))
		}
	}
}

// renderSummary is a scope at a glance, one line per project.
func renderSummary(w io.Writer, boards []lookaround.Board, now time.Time, limit int) {
	faint := lipgloss.NewStyle().Faint(true)
	red := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	nameW := 0
	for _, b := range boards {
		nameW = max(nameW, len(b.Project.Name))
	}

	// a scope shares one span, so the feed names it once
	if len(boards) > 0 && boards[0].View == lookaround.ViewFeed {
		fmt.Fprintln(w, faint.Bold(true).Render(windowLabel(boards[0].Window)))
	}

	for _, b := range boards {
		var (
			inFlight, waiting int
			oldest            lookaround.Row
		)

		for _, r := range b.Rows {
			if lookaround.StateGroup(r) != lookaround.GroupInProgress {
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
		case b.View == lookaround.ViewFeed:
			parts = append(parts, feedCounts(b, faint)...)
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

		if b.Project.LocalOnly {
			parts = append(parts, faint.Render(localOnly))
		}

		fmt.Fprintf(w, "%-*s  %s\n", nameW, b.Project.Name, strings.Join(parts, "  "))
	}
}

func feedCounts(b lookaround.Board, faint lipgloss.Style) []string {
	if b.Feed == nil {
		return nil
	}

	count := map[string]int{}
	for _, r := range b.Feed.Rows {
		count[r.Group]++
	}

	var parts []string

	for _, g := range lookaround.Groups {
		if count[g] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count[g], strings.ReplaceAll(g, "-", " ")))
		}
	}

	if len(parts) == 0 {
		parts = append(parts, faint.Render("no activity"))
	}

	if q := b.Feed.Quiet.Count; q > 0 {
		parts = append(parts, faint.Render(fmt.Sprintf("%d quiet", q)))
	}

	return parts
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

func renderByPerson(w io.Writer, boards []lookaround.Board, limit int) {
	type entry struct {
		project string
		line    boardLine
		rank    int
		at      time.Time
	}

	var (
		rows  []boardLine
		names []string
		by    = map[string][]entry{}
	)

	add := func(project, person string, e entry) {
		e.project = project
		by[person] = append(by[person], e)
		rows = append(rows, e.line)
	}

	for _, b := range boards {
		label := ""
		if len(boards) > 1 {
			label = b.Project.Name
		}

		renderSources(w, b, label)
		names = append(names, b.Project.Name)

		for _, r := range b.Rows {
			add(b.Project.Name, r.Who.Person, entry{
				line: lineOf(r), rank: lookaround.StateRank(r), at: r.LastActivity,
			})
		}

		if b.Feed != nil {
			for _, r := range b.Feed.Rows {
				add(b.Project.Name, r.Who.Person, entry{
					line: feedLine(r), rank: slices.Index(lookaround.Groups, r.Group), at: r.At,
				})
			}
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
			if d := a.rank - c.rank; d != 0 {
				return d
			}

			return c.at.Compare(a.at)
		})

		for _, e := range entries {
			lay.line(w, e.line, e.project)
		}
	}
}

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

func cut(s string, n int) string {
	runes := []rune(s)
	if n <= 8 || len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

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

func (lay boardLayout) line(w io.Writer, r boardLine, project string) {
	var tail []string

	if r.PR != 0 {
		var prs []string
		for _, p := range r.Parts {
			prs = append(prs, fmt.Sprintf("#%d", p.Number))
		}

		tail = append(tail, strings.Join(append(prs, fmt.Sprintf("#%d", r.PR)), " + "))
	}

	if r.Who != "" {
		tail = append(tail, r.Who)
	}

	if r.BranchLeft {
		tail = append(tail, "branch left")
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

	// local and tracker are the common cases at a glance; the HTML shows
	// every origin
	from := ""
	if r.StageFrom != lookaround.FromLocal && r.StageFrom != lookaround.FromTracker {
		from = r.StageFrom
	}

	links := linksOn(lay.limit > 0)
	links.Known(project, r.Key, r.URL)
	key := links.ID(project, r.Key)

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
	case lookaround.StageDone, lookaround.GroupFinished:
		return s.Foreground(lipgloss.Color("2"))
	case lookaround.GroupStarted:
		return s.Bold(true).Foreground(lipgloss.Color("4"))
	case lookaround.GroupUpdated:
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case lookaround.GroupReviewed:
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case "abandoned":
		return s.Foreground(lipgloss.Color("1"))
	case lookaround.StageReady:
		return s.Bold(true).Foreground(lipgloss.Color("2"))
	case lookaround.StageAsksWaiting, lookaround.StageBlocked:
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case lookaround.StageImplementing:
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case lookaround.StageInReview, lookaround.StagePROpen:
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case lookaround.StagePlanning:
		return s.Bold(true).Foreground(lipgloss.Color("4"))
	case lookaround.StagePlanned:
		return s.Foreground(lipgloss.Color("4"))
	case lookaround.StageMergedUnfinished:
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	default:
		return s.Faint(true)
	}
}
