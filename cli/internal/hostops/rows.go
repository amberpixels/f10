package hostops

import (
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// SettlePriorities warns once per distinct unknown value: a driver emitting
// its tracker's own word does so on every row of the page.
func SettlePriorities(rows []Row, warn io.Writer) []Row {
	var (
		unknown []string
		count   = map[string]int{}
	)

	for i, r := range rows {
		if r.Priority == "" {
			continue
		}

		if rank, ok := trackerdriver.PriorityRank(r.Priority); ok {
			rows[i].Priority = trackerdriver.Priorities[rank]

			continue
		}

		if count[r.Priority] == 0 {
			unknown = append(unknown, r.Priority)
		}

		count[r.Priority]++
		rows[i].Priority = ""
	}

	for _, v := range unknown {
		ui.Warnf(warn, "priority %q on %d row(s) is not one of %s; ignored",
			v, count[v], strings.Join(trackerdriver.Priorities, ", "))
	}

	return rows
}

// WriteRows applies the rule reading a task does: a terminal gets a table,
// anything else the JSON. f10 never picks a winner among rows: choosing is
// fzf's job in a shell and the agent's in a turn.
func WriteRows(w io.Writer, rows []Row, none string) error {
	if !ui.IsTerminal(w) {
		for i := range rows {
			rows[i].Body = ""
		}

		return ui.JSON(w, rows)
	}

	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, none)

		return err
	}

	idW, statusW, priorityW := 0, 0, 0
	for _, r := range rows {
		idW = max(idW, len(r.ID))
		statusW = max(statusW, len(r.Status))
		priorityW = max(priorityW, len(r.Priority))
	}

	links := render.LinksOn(true)

	for _, r := range rows {
		// ↗ marks a row that opens, as in the report blocks; it is one cell wide
		marker := "  "
		if r.URL != "" {
			marker = render.Hyperlink(r.URL, "↗") + " "
		}

		links.Known("", r.ID, r.URL)
		shown := render.PadRight(links.ID("", r.ID), idW)
		title := render.Hyperlink(r.URL, r.Title)

		priority := ""
		if priorityW > 0 {
			priority = fmt.Sprintf("%-*s  ", priorityW, r.Priority)
		}

		if _, err := fmt.Fprintf(
			w,
			"%s%s  %-*s  %s%s\n",
			marker,
			shown,
			statusW,
			r.Status,
			priority,
			title,
		); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}
	}

	return nil
}
