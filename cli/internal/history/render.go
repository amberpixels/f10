package history

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MaxLines caps the text form: a brief that grows makes planning worse.
const MaxLines = 15

// NoHistory is the whole text of an empty brief, which a plan copies so a
// reader tells "checked, nothing" from "not checked".
const NoHistory = "No history for these files."

const noteWidth = 160

// Lines is the text form, most decision-relevant first: the neighbors by id,
// regressions, the statistics, decisions, lessons, then a line per
// neighbor. Lines past MaxLines drop from the bottom, and every line cites
// a task or a lore id.
func (b *Brief) Lines() []string {
	if b.Empty() {
		return []string{NoHistory}
	}

	var out []string

	if len(b.Neighbors) > 0 || len(b.Stale) > 0 {
		out = append(out, b.header())
	}

	if len(b.Regressions) > 0 {
		var parts []string
		for _, r := range b.Regressions {
			parts = append(parts, r.Bug+" from "+r.Origin)
		}

		out = append(out, fmt.Sprintf("regressions: %d (%s)", len(b.Regressions), strings.Join(parts, ", ")))
	}

	if a := b.Aggregates; a != nil {
		for _, r := range a.Recurring {
			out = append(out, fmt.Sprintf("recurring finding: %s in %d of %d (%s)%s",
				r.Category, len(r.Tasks), a.N, strings.Join(r.Tasks, ", "), b.settled(r.Category)))
		}

		if a.MedianRounds != nil {
			var parts []string
			for _, n := range b.Neighbors {
				if n.Rounds > 0 {
					parts = append(parts, n.Task+" "+strconv.Itoa(n.Rounds))
				}
			}

			out = append(
				out,
				fmt.Sprintf("median review rounds: %s (%s)", number(*a.MedianRounds), strings.Join(parts, ", ")),
			)
		}
	}

	for _, d := range b.Decisions {
		out = append(
			out,
			clip(fmt.Sprintf("decision %s (%s, %s): chose %s%s", d.ID, d.Change, d.Path, d.Chosen, rejected(d))),
		)
	}

	for _, l := range b.Lessons {
		out = append(out, clip(fmt.Sprintf("lesson %s (%s, %s): %s", l.ID, l.Change, l.Path, l.Text)))
	}

	for _, n := range b.Neighbors {
		out = append(out, n.line())
	}

	return out[:min(len(out), MaxLines)]
}

// Write prints the text form.
func (b *Brief) Write(w io.Writer) error {
	for _, l := range b.Lines() {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}

	return nil
}

func (b *Brief) header() string {
	paths := strings.Join(b.Paths[:min(len(b.Paths), 3)], ", ")
	if len(b.Paths) > 3 {
		paths += fmt.Sprintf(" +%d", len(b.Paths)-3)
	}

	s := fmt.Sprintf("%s: %s", paths, plural(len(b.Neighbors), "task", "tasks"))
	if len(b.Neighbors) > 0 {
		s += " (" + strings.Join(tasks(b.Neighbors), ", ") + ")"
	}

	if len(b.Stale) > 0 {
		var ids []string
		for _, t := range b.Stale {
			ids = append(ids, t.Task)
		}

		s += fmt.Sprintf(", %d stale (%s)", len(b.Stale), strings.Join(ids, ", "))
	}

	return s
}

func (n Neighbor) line() string {
	parts := []string{}

	if n.Kind != "" {
		parts = append(parts, n.Kind)
	}

	if n.Lines != nil {
		parts = append(parts, strconv.Itoa(*n.Lines)+" lines")
	}

	if n.ImplementMinutes != nil {
		parts = append(parts, fmt.Sprintf("%.0fm implement", *n.ImplementMinutes))
	}

	if n.Rounds > 0 {
		parts = append(parts, plural(n.Rounds, "review round", "review rounds"))
	}

	for _, cat := range slices.Sorted(maps.Keys(n.Categories)) {
		c := n.Categories[cat]
		parts = append(parts, count(cat, c))
	}

	if n.Superseded > 0 {
		parts = append(parts, plural(n.Superseded, "plan superseded", "plans superseded"))
	}

	if n.Halts > 0 {
		parts = append(parts, plural(n.Halts, "halt", "halts"))
	}

	if len(n.Verdicts) > 0 {
		parts = append(parts, "judged "+strings.Join(n.Verdicts, ", "))
	}

	if len(n.Regressions) > 0 {
		parts = append(parts, "shipped "+strings.Join(n.Regressions, ", "))
	}

	if len(parts) == 0 {
		parts = append(parts, "nothing recorded")
	}

	return clip(n.Task + ": " + strings.Join(parts, "; "))
}

// settled is the category's fixed and skipped totals over the neighbors,
// when any verdict recorded them.
func (b *Brief) settled(cat string) string {
	var fixed, skipped int

	for _, n := range b.Neighbors {
		fixed, skipped = fixed+n.Categories[cat].Fixed, skipped+n.Categories[cat].Skipped
	}

	if fixed+skipped == 0 {
		return ""
	}

	return fmt.Sprintf(", %d fixed, %d skipped", fixed, skipped)
}

func count(cat string, c Count) string {
	s := fmt.Sprintf("%s %d raised", cat, max(c.Raised, c.Fixed+c.Skipped))
	if c.Fixed > 0 {
		s += fmt.Sprintf(" %d fixed", c.Fixed)
	}

	if c.Skipped > 0 {
		s += fmt.Sprintf(" %d skipped", c.Skipped)
	}

	return s
}

func rejected(d Note) string {
	if len(d.Rejected) == 0 {
		return ""
	}

	var parts []string
	for _, r := range d.Rejected {
		parts = append(parts, r.Option+" ("+r.Reason+")")
	}

	return "; rejected " + strings.Join(parts, ", ")
}

func tasks(ns []Neighbor) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Task)
	}

	return out
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return strconv.Itoa(n) + " " + many
}

// clip keeps a line readable in a plan's context; the --json form is whole.
func clip(s string) string {
	if r := []rune(s); len(r) > noteWidth {
		return string(r[:noteWidth-1]) + "…"
	}

	return s
}
