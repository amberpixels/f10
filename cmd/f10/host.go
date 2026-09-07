package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/internal/shell"
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

func (i issue) url() string   { return firstNonEmpty(i.URL, i.WebURL) }
func (i issue) body() string  { return firstNonEmpty(i.Body, i.Description) }
func (i issue) ident() string { return firstNonEmpty(itoa(i.Number), itoa(i.IID)) }

func itoa(n int) string {
	if n == 0 {
		return ""
	}

	return strconv.Itoa(n)
}

func hostIssueDoc(ctx context.Context, t *target, id, number string) (string, error) {
	host, err := t.hostCLI()
	if err != nil {
		return "", err
	}

	args := []string{"issue", "view", number, "--json", "number,title,state,url,body,comments"}
	if host == "glab" {
		args = []string{"issue", "view", number, "-F", "json"}
	}

	out, err := runHost(ctx, t, host, args...)
	if err != nil {
		return "", err
	}

	var iss issue
	if err := json.Unmarshal([]byte(out), &iss); err != nil {
		return "", fmt.Errorf("parsing %s issue: %w", host, err)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, iss.Title)

	if meta := strings.Join(nonEmpty(iss.State, iss.url()), " · "); meta != "" {
		fmt.Fprintf(&b, "%s\n\n", meta)
	}

	b.WriteString(strings.TrimSpace(iss.body()))
	b.WriteString("\n")

	for _, c := range iss.Comments {
		fmt.Fprintf(&b, "\n---\n\n**%s**\n\n%s\n", firstNonEmpty(c.Author.Login, "comment"), strings.TrimSpace(c.Body))
	}

	return b.String(), nil
}

func hostIssueURL(ctx context.Context, t *target, host, number string) (string, error) {
	args := []string{"issue", "view", number, "--json", "url"}
	if host == "glab" {
		args = []string{"issue", "view", number, "-F", "json"}
	}

	out, err := runHost(ctx, t, host, args...)
	if err != nil {
		return "", err
	}

	var iss issue
	if err := json.Unmarshal([]byte(out), &iss); err != nil {
		return "", fmt.Errorf("parsing %s issue: %w", host, err)
	}

	if url := iss.url(); url != "" {
		return url, nil
	}

	return "", fmt.Errorf("%s returned no url for issue %s", host, number)
}

func hostIssueSearch(ctx context.Context, t *target, query string) ([]row, error) {
	host, err := t.hostCLI()
	if err != nil {
		return nil, err
	}

	args := []string{"issue", "list", "--search", query, "--json", "number,title,state,url", "--limit", "30"}
	if host == "glab" {
		args = []string{"issue", "list", "--search", query, "-F", "json", "--per-page", "30"}
	}

	out, err := runHost(ctx, t, host, args...)
	if err != nil {
		return nil, err
	}

	var found []issue
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		return nil, fmt.Errorf("parsing %s issue list: %w", host, err)
	}

	rows := make([]row, 0, len(found))
	for _, iss := range found {
		id := iss.ident()
		if t.eff.IDPrefix != "" {
			id = t.eff.IDPrefix + "-" + id
		}

		rows = append(rows, row{ID: id, Title: iss.Title, Status: iss.State, URL: iss.url()})
	}

	return rows, nil
}

func runHost(ctx context.Context, t *target, host string, args ...string) (string, error) {
	res, err := shell.Capture(ctx, t.dir, host, args...)
	if err != nil {
		return "", fmt.Errorf("running %s: %w", host, err)
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%s: %s", host, firstNonEmpty(res.Stderr, fmt.Sprintf("exit %d", res.Code)))
	}

	return res.Stdout, nil
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))

	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}

	return out
}
