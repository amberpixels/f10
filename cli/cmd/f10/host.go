package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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

// issue is the union of the two CLIs' issue shapes, tagged for both.
type issue struct {
	Number      int       `json:"number"`
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	URL         string    `json:"url"`
	WebURL      string    `json:"web_url"`
	Body        string    `json:"body"`
	Description string    `json:"description"`
	Labels      labels    `json:"labels"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatedAtGL time.Time `json:"created_at"`

	Assignees []struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"assignees"`

	Comments []struct {
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"comments"`
}

// labels are the issue's label names: gh gives objects, glab plain strings.
type labels []string

func (l *labels) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err == nil {
		*l = names

		return nil
	}

	var objs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &objs); err != nil {
		return err
	}

	*l = make(labels, 0, len(objs))
	for _, o := range objs {
		*l = append(*l, o.Name)
	}

	return nil
}

func (i issue) url() string  { return cmp.Or(i.URL, i.WebURL) }
func (i issue) body() string { return cmp.Or(i.Body, i.Description) }

// pull reports whether the number named a pull request: gh numbers issues
// and pull requests from one counter, and `gh issue view` answers for both.
func (i issue) pull() bool {
	return strings.Contains(i.URL, "/pull/") || strings.Contains(i.WebURL, "/merge_requests/")
}

func (i issue) notPull(number string) error {
	if i.pull() {
		return fmt.Errorf("#%s is a pull request, not an issue", number)
	}

	return nil
}

// ident is gh's number or glab's iid, whichever came back.
func (i issue) ident() string { return strconv.Itoa(cmp.Or(i.Number, i.IID)) }

// issue always asks gh for the url too, since it tells a pull request apart;
// glab has no field selection.
func (h host) issue(ctx context.Context, number, fields string) (issue, error) {
	var iss issue

	if !slices.Contains(strings.Split(fields, ","), "url") {
		fields += ",url"
	}

	err := h.Decode(ctx, &iss, h.Pick(
		[]string{"issue", "view", number, "--json", fields},
		[]string{"issue", "view", number, "-F", "json"},
	)...)

	return iss, err
}

func (h host) issueDoc(ctx context.Context, id, number, staleAfter string) (string, issue, error) {
	iss, err := h.issue(ctx, number, "number,title,state,labels,createdAt,url,body,comments")
	if err != nil {
		return "", issue{}, err
	}

	if err := iss.notPull(number); err != nil {
		return "", issue{}, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, h.titles.Strip(iss.Title, id, number))

	// labels sit up top: fetch reads a task's kind from them, and its age
	// from the created date when stale_after is declared
	var created string
	if at := cmp.Or(iss.CreatedAt, iss.CreatedAtGL); !at.IsZero() {
		created = "created " + at.UTC().Format(time.DateOnly)

		// the comparison is made here so fetch reads it, never works it out
		if d, err := facts.ParseStaleAfter(staleAfter); err == nil && time.Since(at) > d {
			created += " (stale: older than " + staleAfter + ")"
		}
	}

	meta := slices.DeleteFunc(
		[]string{iss.State, strings.Join(iss.Labels, ", "), created, iss.url()},
		func(s string) bool { return s == "" },
	)
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n\n", strings.Join(meta, " · "))
	}

	b.WriteString(strings.TrimSpace(iss.body()))
	b.WriteString("\n")

	for _, c := range iss.Comments {
		fmt.Fprintf(&b, "\n---\n\n**%s**\n\n%s\n", cmp.Or(c.Author.Login, "comment"), strings.TrimSpace(c.Body))
	}

	return b.String(), iss, nil
}

func (h host) issueURL(ctx context.Context, number string) (string, error) {
	iss, err := h.issue(ctx, number, "url")
	if err != nil {
		return "", err
	}

	if err := iss.notPull(number); err != nil {
		return "", err
	}

	if url := iss.url(); url != "" {
		return url, nil
	}

	return "", fmt.Errorf("%s returned no url for issue %s", h.Name, number)
}

// repoURL is the repository's web page, read from the host rather than the
// remote, so a fork or a self-hosted GitLab gets its own.
func (h host) repoURL(ctx context.Context) (string, error) {
	var payload struct {
		URL    string `json:"url"`
		WebURL string `json:"web_url"`
	}

	if err := h.Decode(ctx, &payload, h.Pick(
		[]string{"repo", "view", "--json", "url"},
		[]string{"repo", "view", "-F", "json"},
	)...); err != nil {
		return "", err
	}

	url := cmp.Or(payload.URL, payload.WebURL)
	if url == "" {
		return "", fmt.Errorf("%s returned no repository url", h.Name)
	}

	return strings.TrimSuffix(url, "/"), nil
}

func (h host) issuesURL(ctx context.Context) (string, error) {
	return h.repoPage(ctx, "/issues", "/-/issues")
}

func (h host) pullsURL(ctx context.Context) (string, error) {
	return h.repoPage(ctx, "/pulls", "/-/merge_requests")
}

func (h host) repoPage(ctx context.Context, gh, glab string) (string, error) {
	url, err := h.repoURL(ctx)
	if err != nil {
		return "", err
	}

	if h.GLab() {
		return url + glab, nil
	}

	return url + gh, nil
}

// issueTitle is the one field the default branch name carries.
func (h host) issueTitle(ctx context.Context, number string) (string, error) {
	iss, err := h.issue(ctx, number, "title")
	if err != nil {
		return "", err
	}

	if err := iss.notPull(number); err != nil {
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
	ghArgs := []string{"issue", "list"}
	glabArgs := []string{"issue", "list"}

	if query != "" {
		ghArgs = append(ghArgs, "--search", query)
		glabArgs = append(glabArgs, "--search", query)
	}

	ghArgs = append(
		ghArgs,
		"--json",
		"number,title,state,url,labels,body,assignees,createdAt",
		"--limit",
		strconv.Itoa(limit),
	)
	glabArgs = append(glabArgs, "--output", "json", "--per-page", strconv.Itoa(limit))

	var found []issue

	err := h.Decode(ctx, &found, h.Pick(ghArgs, glabArgs)...)
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(found))
	for _, iss := range found {
		id := iss.ident()
		if prefix != "" {
			id = prefix + "-" + id
		}

		r := row{
			ID:       id,
			Title:    h.titles.Strip(iss.Title, id, iss.ident()),
			Status:   iss.State,
			URL:      iss.url(),
			Priority: priorityOf(iss.Labels, priorities),
			After:    afterIn(iss.body()),
			Created:  cmp.Or(iss.CreatedAt, iss.CreatedAtGL),
		}

		if len(iss.Assignees) > 0 {
			r.Assignee = cmp.Or(iss.Assignees[0].Login, iss.Assignees[0].Username)
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
