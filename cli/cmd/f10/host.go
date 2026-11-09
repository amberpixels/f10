package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// A host is the forge binding with the nouns only this command acts on; the
// reads standup and the board share live on forge.Host itself.
type host struct {
	forge.Host

	titles tasktitle.Template // tracker.title; zero leaves titles as the host holds them
	prefix string             // the task id prefix titles are rendered and stripped with
}

func newHost(name, dir string) host { return host{Name: name, Dir: dir} }

func (h host) at(dir string) host {
	h.Host = h.At(dir)

	return h
}

func (h host) taskID(number string) string {
	if h.prefix == "" {
		return number
	}

	return h.prefix + "-" + number
}

func (h host) title(raw, number string) string {
	return h.titles.Strip(raw, h.taskID(number), number)
}

// The host-issues fallback is `task` with no driver, so a repo tracking its
// work as issues on its own host needs no .f10/ at all.

func (h host) issueDoc(
	ctx context.Context,
	id, number string,
	staleAfter facts.StaleAfter,
) (string, forge.Issue, error) {
	iss, err := h.IssueWithComments(ctx, number)
	if err != nil {
		return "", forge.Issue{}, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, h.titles.Strip(iss.Title, id, number))

	// labels sit up top: fetch reads a task's kind from them, and its age
	// from the created date when stale_after is declared
	var created string
	if at := iss.CreatedAt; !at.IsZero() {
		created = "created " + at.UTC().Format(time.DateOnly)

		// the comparison is made here so fetch reads it, never works it out
		if staleAt(at, staleAfter, time.Now()) {
			created += " (stale: older than " + staleAfter.String() + ")"
		}
	}

	meta := slices.DeleteFunc(
		[]string{iss.State, strings.Join(iss.Labels, ", "), created, iss.URL},
		func(s string) bool { return s == "" },
	)
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n\n", strings.Join(meta, " · "))
	}

	b.WriteString(strings.TrimSpace(iss.Body))
	b.WriteString("\n")

	for _, c := range iss.Comments {
		fmt.Fprintf(&b, "\n---\n\n**%s**\n\n%s\n", cmp.Or(c.Author, "comment"), strings.TrimSpace(c.Body))
	}

	return b.String(), iss, nil
}

// issueCreated is the issue's created date, zero when the host gives none.
func (h host) issueCreated(ctx context.Context, number string) (time.Time, error) {
	iss, err := h.Issue(ctx, number)

	return iss.CreatedAt, err
}

// staleAt is task read's stale mark and task stale's age check in one place,
// so the two never disagree; an undeclared or bad stale_after is never stale.
func staleAt(created time.Time, staleAfter facts.StaleAfter, now time.Time) bool {
	return staleAfter.On() && !created.IsZero() && now.Sub(created) > staleAfter.Duration
}

func (h host) issueURL(ctx context.Context, number string) (string, error) {
	iss, err := h.Issue(ctx, number)
	if err != nil {
		return "", err
	}

	if url := iss.URL; url != "" {
		return url, nil
	}

	return "", fmt.Errorf("%s returned no url for issue %s", h.Name, number)
}

// issueTitle is the one field the default branch name carries.
func (h host) issueTitle(ctx context.Context, number string) (string, error) {
	iss, err := h.Issue(ctx, number)
	if err != nil {
		return "", err
	}

	if iss.Title == "" {
		return "", fmt.Errorf("%s returned no title for issue %s", h.Name, number)
	}

	return h.title(iss.Title, number), nil
}

// issuePage is the first page `task open --print` and `task search` show.
const issuePage = 30

func (h host) listIssues(
	ctx context.Context, query, prefix string, priorities map[string]string, limit int,
) ([]row, error) {
	found, err := h.Issues(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(found))
	for _, iss := range found {
		id := iss.Number
		if prefix != "" {
			id = prefix + "-" + id
		}

		r := row{
			ID:       id,
			Title:    h.titles.Strip(iss.Title, id, iss.Number),
			Status:   iss.State,
			URL:      iss.URL,
			Priority: priorityOf(iss.Labels, priorities),
			After:    afterIn(iss.Body),
			Created:  iss.CreatedAt,
		}

		if len(iss.Assignees) > 0 {
			r.Assignee = iss.Assignees[0]
		}

		rows = append(rows, r)
	}

	return rows, nil
}

// priorityOf is the most urgent level the issue's labels map to, or "" when
// none does. Labels compare case-insensitively, as the hosts treat them.
func priorityOf(names []string, priorities map[string]string) string {
	best, level := len(driver.Priorities), ""

	for _, name := range names {
		if rank, ok := driver.PriorityRank(priorities[strings.ToLower(name)]); ok && rank < best {
			best, level = rank, driver.Priorities[rank]
		}
	}

	return level
}
