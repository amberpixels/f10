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
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/textx"
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

	links := linksOn(limit > 0)

	nodes := make([]tasktree.Node, len(m.Rows))
	for i, r := range m.Rows {
		nodes[i] = boardNode(r, "", false, links)
	}

	tree := tasktree.Tree{Align: true, Width: limit}
	tree.Columns = tree.Measure(nodes)

	for start := 0; start < len(m.Rows); {
		end := start + 1
		for end < len(m.Rows) && m.Rows[end].Group == m.Rows[start].Group {
			end++
		}

		fmt.Fprintf(w, "\n%s\n", faint.Render(m.Rows[start].Group))
		_ = tree.Write(w, nodes[start:end])
		start = end
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
			parts = append(
				parts,
				red.Render("failed")+"  "+faint.Render(cutError(firstLine(failed), limit, limit-nameW-10)),
			)
		case b.View == lookaround.ViewFeed:
			parts = append(parts, feedCounts(b, faint)...)
		case inFlight == 0:
			parts = append(parts, faint.Render("nothing in flight"))
		default:
			parts = append(parts, fmt.Sprintf("%d in flight", inFlight), fmt.Sprintf("%d waiting on you", waiting))

			if oldest.Key != "" {
				parts = append(parts, "oldest "+oldest.Key+" "+textx.Age(now, oldest.LastActivity))
			}
		}

		if failed == "" {
			for _, s := range b.Sources {
				if !s.OK && !s.Skipped {
					parts = append(
						parts,
						red.Render(
							s.Name+" failed",
						)+"  "+faint.Render(
							cutError(firstLine(s.Error), limit, limit-nameW-40),
						),
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

	by := map[string][]entry{}

	add := func(project, person string, e entry) {
		e.project = project
		by[person] = append(by[person], e)
	}

	for _, b := range boards {
		label := ""
		if len(boards) > 1 {
			label = b.Project.Name
		}

		renderSources(w, b, label)

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

	if len(by) == 0 {
		fmt.Fprintln(w, "nothing in flight, nothing next, nothing done in the window")

		return
	}

	persons := slices.Sorted(maps.Keys(by))
	if i := slices.Index(persons, ""); i >= 0 {
		persons = append(slices.Delete(persons, i, i+1), "")
	}

	links := linksOn(limit > 0)
	groups := make([][]tasktree.Node, len(persons))

	var all []tasktree.Node

	for i, name := range persons {
		entries := by[name]
		slices.SortStableFunc(entries, func(a, c entry) int {
			if d := a.rank - c.rank; d != 0 {
				return d
			}

			return c.at.Compare(a.at)
		})

		for _, e := range entries {
			groups[i] = append(groups[i], boardNode(e.line, e.project, len(boards) > 1, links))
		}

		all = append(all, groups[i]...)
	}

	tree := tasktree.Tree{Align: true, Width: limit}
	tree.Columns = tree.Measure(all)
	faint := lipgloss.NewStyle().Faint(true)

	for i, name := range persons {
		fmt.Fprintf(w, "\n%s\n", faint.Render(cmp.Or(name, "nobody")))
		_ = tree.Write(w, groups[i])
	}
}

// cutError keeps an error whole when piped (limit 0), and never cuts it
// below ten columns, where what is left would say nothing.
func cutError(s string, limit, n int) string {
	if limit <= 0 {
		return s
	}

	return textx.Cut(s, max(n, 10))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

// boardNode is a board line as a tree row: the title fills what the
// terminal leaves, and the tail (PRs, who, next) follows it.
func boardNode(r boardLine, project string, withProject bool, links *taskLinks) tasktree.Node {
	faint := func(s string) string { return lipgloss.NewStyle().Faint(true).Render(s) }

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

	// local and tracker are the common cases at a glance; the HTML shows
	// every origin
	from := ""
	if r.StageFrom != lookaround.FromLocal && r.StageFrom != lookaround.FromTracker {
		from = r.StageFrom
	}

	links.Known(project, r.Key, r.URL)

	var cells []tasktree.Cell
	if withProject {
		cells = append(cells, tasktree.Cell{Text: project, Render: faint})
	}

	cells = append(cells,
		tasktree.Cell{Text: r.Key, Render: func(string) string { return links.ID(project, r.Key) }},
		tasktree.Cell{Text: r.Stage, Render: func(s string) string { return stageStyle(r.Stage).Render(s) }},
		tasktree.Cell{Text: from, Render: faint},
		tasktree.Cell{Text: r.Title, Fill: true},
		tasktree.Cell{Text: strings.Join(tail, "  "), Render: faint},
	)

	return tasktree.Node{Cells: cells}
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
