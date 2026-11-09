package forge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An Issue is a task on the host's own tracker, as the host reported it.
type Issue struct {
	Number    string
	Title     string
	State     string // as the CLI spells it: OPEN, opened, closed...
	URL       string
	Body      string
	Labels    []string
	Assignees []string
	CreatedAt time.Time
	Comments  []IssueComment // read only by IssueWithComments
	Pull      bool           // the number names a pull or merge request
}

// An IssueComment is one comment on an issue, by its author's login.
type IssueComment struct {
	Author string
	Body   string
}

// ErrNotIssue is a number that names a pull request where an issue was
// meant: gh numbers both from one counter and answers `issue view` for either.
var ErrNotIssue = errors.New("a pull request, not an issue")

type notIssueError struct{ number string }

func (e notIssueError) Error() string        { return "#" + e.number + " is a pull request, not an issue" }
func (e notIssueError) Is(target error) bool { return target == ErrNotIssue }

// ErrNoIssueParent is GitLab, which nests tasks, not issues, under an issue.
var ErrNoIssueParent = errors.New("GitLab nests no issue under an issue; list the subtasks in the parent's body")

// The fields every issue read asks gh for; comments only where they are shown,
// since they are the one field that grows without bound.
const (
	issueFields         = "number,title,state,labels,createdAt,url,body,assignees"
	issueCommentsFields = issueFields + ",comments"
)

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
	Assignees   []user    `json:"assignees"`

	Comments []struct {
		Body   string `json:"body"`
		Author user   `json:"author"`
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

func (i issue) issue() Issue {
	out := Issue{
		Number:    strconv.Itoa(cmp.Or(i.Number, i.IID)),
		Title:     i.Title,
		State:     i.State,
		URL:       cmp.Or(i.URL, i.WebURL),
		Body:      cmp.Or(i.Body, i.Description),
		Labels:    i.Labels,
		CreatedAt: cmp.Or(i.CreatedAt, i.CreatedAtGL),
		Pull:      strings.Contains(i.URL, "/pull/") || strings.Contains(i.WebURL, "/merge_requests/"),
	}

	for _, a := range i.Assignees {
		if name := a.name(); name != "" {
			out.Assignees = append(out.Assignees, name)
		}
	}

	for _, c := range i.Comments {
		out.Comments = append(out.Comments, IssueComment{Author: c.Author.name(), Body: c.Body})
	}

	return out
}

// Item reads number whether it names an issue or a request; Pull tells which.
func (h Host) Item(ctx context.Context, number string) (Issue, error) {
	return h.readIssue(ctx, number, issueFields)
}

// Issue reads an issue, refusing a number that names a request.
func (h Host) Issue(ctx context.Context, number string) (Issue, error) {
	return h.mustIssue(h.readIssue(ctx, number, issueFields))
}

// IssueWithComments is Issue with the issue's comments read too.
func (h Host) IssueWithComments(ctx context.Context, number string) (Issue, error) {
	return h.mustIssue(h.readIssue(ctx, number, issueCommentsFields))
}

func (h Host) mustIssue(iss Issue, err error) (Issue, error) {
	if err != nil {
		return Issue{}, err
	}

	if iss.Pull {
		return Issue{}, notIssueError{number: iss.Number}
	}

	return iss, nil
}

// readIssue: glab has no field selection, so fields shape gh's call alone.
func (h Host) readIssue(ctx context.Context, number, fields string) (Issue, error) {
	var iss issue

	err := h.decode(ctx, &iss, h.pick(
		[]string{"issue", "view", number, "--json", fields},
		[]string{"issue", "view", number, "-F", "json"},
	)...)
	if err != nil {
		return Issue{}, err
	}

	out := iss.issue()
	if out.Number == "0" {
		out.Number = number
	}

	return out, nil
}

// Issues lists the open issues, those matching query when one is given.
func (h Host) Issues(ctx context.Context, query string, limit int) ([]Issue, error) {
	ghArgs := []string{"issue", "list"}
	glabArgs := []string{"issue", "list"}

	if query != "" {
		ghArgs = append(ghArgs, "--search", query)
		glabArgs = append(glabArgs, "--search", query)
	}

	ghArgs = append(ghArgs, "--json", "number,title,state,url,labels,body,assignees,createdAt",
		"--limit", strconv.Itoa(limit))
	glabArgs = append(glabArgs, "--output", "json", "--per-page", strconv.Itoa(limit))

	var found []issue
	if err := h.decode(ctx, &found, h.pick(ghArgs, glabArgs)...); err != nil {
		return nil, err
	}

	return issues(found), nil
}

// allIssuesPage is how many issues glab lists a page; gh takes one limit.
const allIssuesPage = 100

// AllIssues is every issue in any state, numbers and titles only. Neither
// CLI's issue list includes requests, so nothing here filters them.
func (h Host) AllIssues(ctx context.Context) ([]Issue, error) {
	if !h.glab() {
		var all []issue

		err := h.decode(ctx, &all, "issue", "list", "--state", "all", "--json", "number,title", "--limit", "100000")

		return issues(all), err
	}

	var all []issue

	for page := 1; ; page++ {
		var found []issue
		if err := h.decode(ctx, &found, "issue", "list", "--all", "--output", "json",
			"--per-page", strconv.Itoa(allIssuesPage), "--page", strconv.Itoa(page)); err != nil {
			return nil, err
		}

		all = append(all, found...)

		if len(found) < allIssuesPage {
			return issues(all), nil
		}
	}
}

func issues(found []issue) []Issue {
	out := make([]Issue, 0, len(found))
	for _, iss := range found {
		out = append(out, iss.issue())
	}

	return out
}

// CommentIssue posts body on an issue, never a request, and returns the
// comment's url when the CLI prints one.
func (h Host) CommentIssue(ctx context.Context, number, body string) (string, error) {
	if _, err := h.mustIssue(h.readIssue(ctx, number, issueFields)); err != nil {
		return "", err
	}

	out, err := h.run(ctx, h.pick(
		[]string{"issue", "comment", number, "--body", body},
		[]string{"issue", "note", number, "--message", body},
	)...)
	if err != nil {
		return "", err
	}

	for line := range strings.Lines(out) {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "http") {
			return line, nil
		}
	}

	return "", nil
}

// An IssueEdit is one edit of an issue's title, labels and assignee; the
// zero value of a field leaves it alone.
type IssueEdit struct {
	Title        string
	AddLabels    []string
	RemoveLabels []string
	// Assignee replaces the issue's assignees, as glab's flag does; on gh,
	// which only adds one, the others in Current come off by name.
	Assignee string
	Current  []string
}

func (e IssueEdit) empty() bool {
	return e.Title == "" && len(e.AddLabels) == 0 && len(e.RemoveLabels) == 0 && e.Assignee == ""
}

// EditIssue writes e; an empty edit writes nothing.
func (h Host) EditIssue(ctx context.Context, number string, e IssueEdit) error {
	if e.empty() {
		return nil
	}

	var unassign []string

	if e.Assignee != "" && !h.glab() {
		// gh reads @me as the signed-in login, which must stay on
		keep := e.Assignee
		if keep == "@me" {
			login, err := h.Login(ctx)
			if err != nil {
				return err
			}

			keep = login
		}

		for _, a := range e.Current {
			if !strings.EqualFold(a, keep) {
				unassign = append(unassign, a)
			}
		}
	}

	_, err := h.run(ctx, h.editIssueArgs(number, e, unassign)...)

	return err
}

func (h Host) editIssueArgs(number string, e IssueEdit, unassign []string) []string {
	if h.glab() {
		args := []string{"issue", "update", number}
		if e.Title != "" {
			args = append(args, "--title", e.Title)
		}

		if len(e.AddLabels) > 0 {
			args = append(args, "--label", strings.Join(e.AddLabels, ","))
		}

		if len(e.RemoveLabels) > 0 {
			args = append(args, "--unlabel", strings.Join(e.RemoveLabels, ","))
		}

		if e.Assignee != "" {
			args = append(args, "--assignee", e.Assignee)
		}

		return args
	}

	args := []string{"issue", "edit", number}
	if e.Title != "" {
		args = append(args, "--title", e.Title)
	}

	for _, l := range e.AddLabels {
		args = append(args, "--add-label", l)
	}

	for _, l := range e.RemoveLabels {
		args = append(args, "--remove-label", l)
	}

	if e.Assignee != "" {
		args = append(args, "--add-assignee", e.Assignee)
	}

	for _, a := range unassign {
		args = append(args, "--remove-assignee", a)
	}

	return args
}

// SetIssueOpen reopens the issue, or closes it; both CLIs spell it alike.
func (h Host) SetIssueOpen(ctx context.Context, number string, open bool) error {
	verb := "close"
	if open {
		verb = "reopen"
	}

	_, err := h.run(ctx, "issue", verb, number)

	return err
}

var issueNumberRe = regexp.MustCompile(`/issues/(\d+)\b`)

// CreateIssue returns the new issue's url and number, read from the url the
// CLI prints: a list query afterwards could find another create's issue.
func (h Host) CreateIssue(ctx context.Context, title, body string, labels []string) (string, string, error) {
	args := h.pick(
		[]string{"issue", "create", "--title", title, "--body", body},
		[]string{"issue", "create", "--title", title, "--description", body, "--no-editor", "--yes"},
	)

	for _, l := range labels {
		args = append(args, "--label", l)
	}

	out, err := h.run(ctx, args...)
	if err != nil {
		return "", "", err
	}

	for _, line := range slices.Backward(strings.Split(strings.TrimSpace(out), "\n")) {
		line = strings.TrimSpace(line)
		if m := issueNumberRe.FindStringSubmatch(line); m != nil && strings.HasPrefix(line, "http") {
			return line, m[1], nil
		}
	}

	return "", "", fmt.Errorf("%s issue create printed no issue url: %q", h.Name, out)
}

// A Child is one issue linked under another.
type Child struct {
	Number string
	Title  string
	URL    string
	State  string
	Body   string
	Labels []string
}

// Children reads an issue's linked children: GitHub sub-issues, or GitLab
// work-item children. A host without either answers ErrUnsupported.
func (h Host) Children(ctx context.Context, number string) ([]Child, error) {
	if h.glab() {
		return h.childItems(ctx, number)
	}

	return h.subIssues(ctx, number)
}

// subIssues: a 404 is a host without sub-issues, since the issue itself
// was already read by the time anyone asks for its children.
func (h Host) subIssues(ctx context.Context, number string) ([]Child, error) {
	type label struct {
		Name string `json:"name"`
	}

	found, err := decodePages[struct {
		Number  int     `json:"number"`
		Title   string  `json:"title"`
		State   string  `json:"state"`
		HTMLURL string  `json:"html_url"`
		Body    string  `json:"body"`
		Labels  []label `json:"labels"`
	}](ctx, h, "repos/{owner}/{repo}/issues/"+number+"/sub_issues")
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnsupported
	}

	if err != nil {
		return nil, err
	}

	out := make([]Child, 0, len(found))

	for _, iss := range found {
		c := Child{
			Number: strconv.Itoa(iss.Number),
			Title:  iss.Title,
			URL:    iss.HTMLURL,
			State:  iss.State,
			Body:   iss.Body,
		}

		for _, l := range iss.Labels {
			c.Labels = append(c.Labels, l.Name)
		}

		out = append(out, c)
	}

	return out, nil
}

// childItemsQuery reads a GitLab issue's children from the work-item
// hierarchy, the only place GitLab keeps the parent link: REST issue links
// are "relates to" and "blocks", never a parent.
const childItemsQuery = `query($path: ID!, $iid: String!) {
  project(fullPath: $path) {
    workItems(iid: $iid) {
      nodes {
        widgets {
          ... on WorkItemWidgetHierarchy {
            children(first: 100) {
              nodes {
                iid title state webUrl description
                widgets { ... on WorkItemWidgetLabels { labels { nodes { title } } } }
              }
            }
          }
        }
      }
    }
  }
}`

// childItems: a GitLab without work items answers an unknown-field error.
func (h Host) childItems(ctx context.Context, number string) ([]Child, error) {
	path, err := h.projectPath(ctx)
	if err != nil {
		return nil, err
	}

	type labels struct {
		Labels *struct {
			Nodes []struct {
				Title string `json:"title"`
			} `json:"nodes"`
		} `json:"labels"`
	}

	var payload struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []struct {
						Widgets []struct {
							Children *struct {
								Nodes []struct {
									IID         string   `json:"iid"`
									Title       string   `json:"title"`
									State       string   `json:"state"`
									WebURL      string   `json:"webUrl"`
									Description string   `json:"description"`
									Widgets     []labels `json:"widgets"`
								} `json:"nodes"`
							} `json:"children"`
						} `json:"widgets"`
					} `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
	}

	err = h.decode(ctx, &payload, "api", "graphql", "-f", "query="+childItemsQuery,
		"-f", "path="+path, "-f", "iid="+number)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't exist on type") {
			return nil, ErrUnsupported
		}

		return nil, err
	}

	var out []Child

	for _, item := range payload.Data.Project.WorkItems.Nodes {
		for _, w := range item.Widgets {
			if w.Children == nil {
				continue
			}

			for _, c := range w.Children.Nodes {
				child := Child{Number: c.IID, Title: c.Title, URL: c.WebURL, State: c.State, Body: c.Description}

				for _, lw := range c.Widgets {
					if lw.Labels == nil {
						continue
					}

					for _, l := range lw.Labels.Nodes {
						child.Labels = append(child.Labels, l.Title)
					}
				}

				out = append(out, child)
			}
		}
	}

	return out, nil
}

// projectPath is glab's `group/project` path for the checkout's repository.
func (h Host) projectPath(ctx context.Context) (string, error) {
	var repo struct {
		Path string `json:"path_with_namespace"`
	}

	err := h.decode(ctx, &repo, "repo", "view", "-F", "json")

	return repo.Path, err
}

// LinkParent puts child under parent, moving it from a parent it already
// has: GitHub gives an issue one parent, and links by the child's database
// id, not its number.
func (h Host) LinkParent(ctx context.Context, parent, child string) error {
	if h.glab() {
		return ErrNoIssueParent
	}

	link, err := h.issueLink(ctx, child)
	if err != nil {
		return err
	}

	_, err = h.run(ctx, "api", "-X", "POST", "repos/{owner}/{repo}/issues/"+parent+"/sub_issues",
		"-F", "sub_issue_id="+strconv.FormatInt(link.ID, 10), "-F", "replace_parent=true")

	return err
}

// Unlink takes child out from under the parent it has; an issue with none
// is left alone, so a rerun is safe.
func (h Host) Unlink(ctx context.Context, child string) error {
	if h.glab() {
		return ErrNoIssueParent
	}

	link, err := h.issueLink(ctx, child)
	if err != nil || link.ParentURL == "" {
		return err
	}

	// the parent may live in another repo of the owner, so its own api path
	// is used, never this repo's issue of the same number
	_, apiPath, ok := strings.Cut(link.ParentURL, "/repos/")
	if !ok {
		return fmt.Errorf("issue %s names its parent as %q, not an api url", child, link.ParentURL)
	}

	_, err = h.run(ctx, "api", "-X", "DELETE", "repos/"+apiPath+"/sub_issue",
		"-F", "sub_issue_id="+strconv.FormatInt(link.ID, 10))

	return err
}

type issueLink struct {
	ID        int64  `json:"id"`
	ParentURL string `json:"parent_issue_url"`
}

func (h Host) issueLink(ctx context.Context, number string) (issueLink, error) {
	var link issueLink

	err := h.decode(ctx, &link, "api", "repos/{owner}/{repo}/issues/"+number)

	return link, err
}

// RepoURL is the repository's web page, read from the host rather than the
// remote, so a fork or a self-hosted GitLab gets its own.
func (h Host) RepoURL(ctx context.Context) (string, error) {
	var payload struct {
		URL    string `json:"url"`
		WebURL string `json:"web_url"`
	}

	if err := h.decode(ctx, &payload, h.pick(
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

// IssuesURL is the repository's issue list page.
func (h Host) IssuesURL(ctx context.Context) (string, error) {
	return h.repoPage(ctx, "/issues", "/-/issues")
}

// PullsURL is the repository's request list page.
func (h Host) PullsURL(ctx context.Context) (string, error) {
	return h.repoPage(ctx, "/pulls", "/-/merge_requests")
}

func (h Host) repoPage(ctx context.Context, gh, glab string) (string, error) {
	url, err := h.RepoURL(ctx)
	if err != nil {
		return "", err
	}

	if h.glab() {
		return url + glab, nil
	}

	return url + gh, nil
}

// Token is gh's auth token, for fetching GitHub attachments the CLI cannot;
// "" on glab, whose attachments need none.
func (h Host) Token(ctx context.Context) (string, error) {
	if h.glab() {
		return "", nil
	}

	out, err := h.run(ctx, "auth", "token")

	return strings.TrimSpace(out), err
}
