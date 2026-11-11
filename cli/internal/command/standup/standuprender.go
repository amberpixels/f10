package standup

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// RenderStandup is the report for a terminal. --json is the contract; this
// is a glance at it.
func RenderStandup(w io.Writer, links *render.TaskLinks, s standup.Standup) error {
	var b strings.Builder

	fmt.Fprintf(&b, "since %s", s.Since.Local().Format("2006-01-02 15:04"))

	if s.Period != "" {
		fmt.Fprintf(&b, " (%s)", strings.ReplaceAll(s.Period, "_", " "))
	}

	b.WriteString("\n")

	if len(s.Days) > 0 {
		fmt.Fprintf(&b, "days  %s\n", dayList(s.Days))
	}

	empty := true

	for _, p := range s.Projects {
		if len(p.Tasks)+len(p.Other.PRs)+len(p.Other.Commits)+len(p.Reviewed) == 0 {
			continue
		}

		empty = false

		if len(s.Projects) > 1 {
			fmt.Fprintf(&b, "\n== %s%s\n", p.Name, repoNames(p))
		}

		renderProject(&b, links, p)
	}

	if empty {
		b.WriteString("\nnothing\n")
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// repoNames is a project's repositories, for its heading, when they are
// not just the one the project is named after.
func repoNames(p standup.Project) string {
	if len(p.Repos) == 1 && p.Repos[0].Name == p.Name {
		return ""
	}

	names := make([]string, 0, len(p.Repos))
	for _, r := range p.Repos {
		names = append(names, r.Name)
	}

	return " (" + strings.Join(names, ", ") + ")"
}

func renderProject(b *strings.Builder, links *render.TaskLinks, p standup.Project) {
	// a number names a PR only within its repository, written as its host
	// writes it
	label := func(repo string, n int) string {
		h := forge.Host{}

		i := slices.IndexFunc(p.Repos, func(r standup.Repo) bool { return r.Name == repo })
		if i < 0 && len(p.Repos) == 1 {
			i = 0
		}

		if i >= 0 && p.Repos[i].Host != nil {
			h.Name = *p.Repos[i].Host
		}

		if len(p.Repos) > 1 {
			return repo + h.Ref(n)
		}

		return h.Ref(n)
	}

	if len(p.Tasks) > 0 {
		b.WriteString("\n")
	}

	var upkeep []string

	for _, t := range p.Tasks {
		if t.Class == standup.ClassUpkeep {
			upkeep = append(upkeep, links.ID(p.Name, t.ID))

			continue
		}

		fmt.Fprintf(
			b,
			"%s %-12s %s%s\n",
			render.PadRight(links.ID(p.Name, t.ID), 10),
			t.State,
			prList(t.PRs, label),
			commitCount(t.PRs, t.Commits),
		)
	}

	// tasks that only caught up with their base, rebased or regenerated
	if len(upkeep) > 0 {
		fmt.Fprintf(b, "kept up to date  %s\n", strings.Join(upkeep, ", "))
	}

	if n := len(p.Other.PRs) + len(p.Other.Commits); n > 0 {
		fmt.Fprintf(b, "\nother      %s%s\n", prList(p.Other.PRs, label), commitCount(p.Other.PRs, p.Other.Commits))
	}

	if len(p.Reviewed) > 0 {
		b.WriteString("\nreviewed\n")

		for _, r := range p.Reviewed {
			var kinds []string
			for _, a := range r.Actions {
				kinds = append(kinds, a.Kind)
			}

			fmt.Fprintf(b, "  %s %s (%s): %s\n",
				label(r.Repo, r.Number), r.Title, r.Author, strings.Join(dedupe(kinds), ", "))
		}
	}
}

// prList names the PRs: " -> " where a PR sits on the one before it, ", "
// between anything else (siblings, roots, other repositories).
func prList(prs []standup.PR, label func(string, int) string) string {
	var b strings.Builder

	for i, p := range prs {
		switch {
		case i == 0:
		case prs[i-1].Repo == p.Repo && p.BasePR == prs[i-1].Number:
			b.WriteString(" -> ")
		default:
			b.WriteString(", ")
		}

		b.WriteString(label(p.Repo, p.Number))
	}

	return b.String()
}

func commitCount(prs []standup.PR, loose []standup.Commit) string {
	n := len(loose)
	for _, p := range prs {
		n += len(p.Commits)
	}

	switch {
	case n == 0:
		return ""
	case len(prs) == 0:
		return fmt.Sprintf("%d commit(s)", n)
	default:
		return fmt.Sprintf("  %d commit(s)", n)
	}
}

func dedupe(s []string) []string {
	var out []string

	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}

	return out
}

// dayList names the active days with their weekday, "Fri 10-09, Mon 10-12",
// which is what tells a long weekend from a missed day.
func dayList(days []string) string {
	parts := make([]string, 0, len(days))

	for _, d := range days {
		if t, err := time.Parse(time.DateOnly, d); err == nil {
			d = t.Format("Mon 01-02")
		}

		parts = append(parts, d)
	}

	return strings.Join(parts, ", ")
}
