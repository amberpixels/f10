package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The host-issues fallback: what `task` does in a project with no driver.
// It exists so a repo needs no .f10/ at all to answer - most libraries
// track their work as issues on the host their code already lives on - and
// so the driver contract stays something you adopt when the tracker moves
// somewhere a CLI cannot reach.
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

	Comments []struct {
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"comments"`
}

func (i issue) url() string  { return cmp.Or(i.URL, i.WebURL) }
func (i issue) body() string { return cmp.Or(i.Body, i.Description) }

// gh numbers issues, glab gives them an iid; whichever came back is the one
// this issue has.
func (i issue) ident() string { return strconv.Itoa(cmp.Or(i.Number, i.IID)) }

// issue reads one issue. gh is asked for the named fields alone; glab has
// no field selection and answers with the whole issue.
func (h host) issue(ctx context.Context, number, fields string) (issue, error) {
	var iss issue

	err := h.decode(ctx, &iss, h.pick(
		[]string{"issue", "view", number, "--json", fields},
		[]string{"issue", "view", number, "-F", "json"},
	)...)

	return iss, err
}

// issueDoc is the issue as the markdown document `task read` prints.
func (h host) issueDoc(ctx context.Context, id, number string) (string, error) {
	iss, err := h.issue(ctx, number, "number,title,state,url,body,comments")
	if err != nil {
		return "", err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, iss.Title)

	meta := slices.DeleteFunc([]string{iss.State, iss.url()}, func(s string) bool { return s == "" })
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

	if url := iss.url(); url != "" {
		return url, nil
	}

	return "", fmt.Errorf("%s returned no url for issue %s", h.name, number)
}

// issueTitle is the one field the default branch name carries.
func (h host) issueTitle(ctx context.Context, number string) (string, error) {
	iss, err := h.issue(ctx, number, "title")
	if err != nil {
		return "", err
	}

	if iss.Title == "" {
		return "", fmt.Errorf("%s returned no title for issue %s", h.name, number)
	}

	return iss.Title, nil
}

// searchIssues lists the issues matching query as task rows, their ids
// carrying prefix when the project has one.
func (h host) searchIssues(ctx context.Context, query, prefix string) ([]row, error) {
	var found []issue

	err := h.decode(ctx, &found, h.pick(
		[]string{"issue", "list", "--search", query, "--json", "number,title,state,url", "--limit", "30"},
		[]string{"issue", "list", "--search", query, "-F", "json", "--per-page", "30"},
	)...)
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(found))
	for _, iss := range found {
		id := iss.ident()
		if prefix != "" {
			id = prefix + "-" + id
		}

		rows = append(rows, row{ID: id, Title: iss.Title, Status: iss.State, URL: iss.url()})
	}

	return rows, nil
}
