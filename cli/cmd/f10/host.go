package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/forge"
)

// A host is the forge binding with the nouns only this command acts on:
// issues here, requests in merge.go, reviews in reviewhost.go. The reads
// standup and the board share live on forge.Host itself.
type host struct{ forge.Host }

func newHost(name, dir string) host { return host{forge.Host{Name: name, Dir: dir}} }

// at is the same host answering from another checkout of the repo.
func (h host) at(dir string) host { return host{h.At(dir)} }

// The host-issues fallback: what `task` does in a project with no driver,
// so a repo tracking its work as issues on its own host needs no .f10/ at
// all. A driver is for a tracker no host CLI can reach.
//
// Both CLIs are asked for JSON and the answer is composed into markdown
// here, so a driver's `read` and this fallback hand `writeDoc` the same
// kind of document.

// issue is the union of the two CLIs' issue shapes, tagged for both.
type issue struct {
	Number      int    `json:"number"`
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	State       string `json:"state"`
	URL         string `json:"url"`
	WebURL      string `json:"web_url"`
	Body        string `json:"body"`
	Description string `json:"description"`
	Labels      labels `json:"labels"`

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

// notPull refuses a pull request where an issue was asked for.
func (i issue) notPull(number string) error {
	if i.pull() {
		return fmt.Errorf("#%s is a pull request, not an issue", number)
	}

	return nil
}

// gh numbers issues, glab gives them an iid; whichever came back is the one
// this issue has.
func (i issue) ident() string { return strconv.Itoa(cmp.Or(i.Number, i.IID)) }

// issue reads one issue. gh is asked for the named fields and the url,
// which tells a pull request apart; glab has no field selection and answers
// with the whole issue.
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

// issueDoc is the issue as the markdown document `task read` prints.
func (h host) issueDoc(ctx context.Context, id, number string) (string, error) {
	iss, err := h.issue(ctx, number, "number,title,state,labels,url,body,comments")
	if err != nil {
		return "", err
	}

	if err := iss.notPull(number); err != nil {
		return "", err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, iss.Title)

	// labels sit up top: fetch reads a task's kind from them
	meta := slices.DeleteFunc(
		[]string{iss.State, strings.Join(iss.Labels, ", "), iss.url()},
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

	return b.String(), nil
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

// issuesURL is the repository's issue list.
func (h host) issuesURL(ctx context.Context) (string, error) {
	return h.repoPage(ctx, "/issues", "/-/issues")
}

// pullsURL is the repository's pull or merge request list.
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

	return iss.Title, nil
}

// issuePage is the first page `task open --print` and `task search` show.
const issuePage = 30

// listIssues lists up to limit open issues matching query, or the first
// of them for "", as task rows: ids carry prefix when the project has one,
// and priorities, the lower-cased label to level index, ranks them; nil
// ranks none. A row keeps the id its body's After: line names.
func (h host) listIssues(
	ctx context.Context, query, prefix string, priorities map[string]string, limit int,
) ([]row, error) {
	ghArgs := []string{"issue", "list"}
	glabArgs := []string{"issue", "list"}

	if query != "" {
		ghArgs = append(ghArgs, "--search", query)
		glabArgs = append(glabArgs, "--search", query)
	}

	ghArgs = append(ghArgs, "--json", "number,title,state,url,labels,body", "--limit", strconv.Itoa(limit))
	glabArgs = append(glabArgs, "-F", "json", "--per-page", strconv.Itoa(limit))

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

		rows = append(rows, row{
			ID:       id,
			Title:    iss.Title,
			Status:   iss.State,
			URL:      iss.url(),
			Priority: priorityOf(iss.Labels, priorities),
			After:    afterIn(iss.body()),
		})
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
