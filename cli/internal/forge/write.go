package forge

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The writes a step makes on a request: its title and body, a comment or a
// thread reply, a commit status. Comments go through the API, whose response
// carries the url the CLIs' comment verbs do not reliably print.

// EditPull rewrites a request's title, its body, or both; "" leaves one as
// it is. selector is what the CLI takes: a number or a branch.
func (h Host) EditPull(ctx context.Context, selector, title, body string) error {
	if title == "" && body == "" {
		return errors.New("nothing to edit: give a title or a body")
	}

	if h.GLab() {
		args := []string{"mr", "update", selector}
		if title != "" {
			args = append(args, "--title", title)
		}

		if body != "" {
			args = append(args, "--description", body)
		}

		_, err := h.Run(ctx, args...)

		return err
	}

	args := []string{"pr", "edit", selector}
	if title != "" {
		args = append(args, "--title", title)
	}

	if body != "" {
		f, err := os.CreateTemp("", "f10-pr-body-*.md")
		if err != nil {
			return fmt.Errorf("writing the PR body: %w", err)
		}

		defer os.Remove(f.Name())

		if _, err := f.WriteString(body); err != nil {
			_ = f.Close()

			return fmt.Errorf("writing the PR body: %w", err)
		}

		if err := f.Close(); err != nil {
			return fmt.Errorf("writing the PR body: %w", err)
		}

		args = append(args, "--body-file", f.Name())
	}

	_, err := h.Run(ctx, args...)

	return err
}

// ErrNoThread is a url that names no thread to reply in: an issue comment,
// a review with no inline comments, a check run.
var ErrNoThread = errors.New("the url names no thread to reply in")

var (
	ghCommentRE = regexp.MustCompile(`#discussion_r(\d+)$`)
	ghReviewRE  = regexp.MustCompile(`#pullrequestreview-(\d+)$`)
	glNoteRE    = regexp.MustCompile(`#note_(\d+)$`)
)

// Thread is the thread a url names on request number: GitHub's top-level
// review comment, which replies attach to (a reply to a reply is refused),
// or GitLab's discussion. A review url is the url review pick records for an
// inline review, and names the thread of its first comment.
func (h Host) Thread(ctx context.Context, number int, threadURL string) (string, error) {
	n := strconv.Itoa(number)

	if h.GLab() {
		m := glNoteRE.FindStringSubmatch(threadURL)
		if m == nil {
			return "", ErrNoThread
		}

		type discussion struct {
			ID             string `json:"id"`
			IndividualNote bool   `json:"individual_note"`
			Notes          []struct {
				ID int64 `json:"id"`
			} `json:"notes"`
		}

		ds, err := DecodePages[discussion](ctx, h, "projects/:fullpath/merge_requests/"+n+"/discussions?per_page=100")
		if err != nil {
			return "", err
		}

		for _, d := range ds {
			for _, note := range d.Notes {
				if strconv.FormatInt(note.ID, 10) == m[1] {
					if d.IndividualNote {
						return "", ErrNoThread
					}

					return d.ID, nil
				}
			}
		}

		return "", fmt.Errorf("no discussion on !%d holds note %s", number, m[1])
	}

	type comment struct {
		ID        int64 `json:"id"`
		InReplyTo int64 `json:"in_reply_to_id"`
	}

	root := func(c comment) string { return strconv.FormatInt(cmp.Or(c.InReplyTo, c.ID), 10) }

	if m := ghCommentRE.FindStringSubmatch(threadURL); m != nil {
		var c comment
		if err := h.Decode(ctx, &c, "api", "repos/{owner}/{repo}/pulls/comments/"+m[1]); err != nil {
			return "", err
		}

		return root(c), nil
	}

	if m := ghReviewRE.FindStringSubmatch(threadURL); m != nil {
		cs, err := DecodePages[comment](
			ctx,
			h,
			"repos/{owner}/{repo}/pulls/"+n+"/reviews/"+m[1]+"/comments?per_page=100",
		)
		if err != nil {
			return "", err
		}

		if len(cs) == 0 {
			return "", ErrNoThread
		}

		return root(cs[0]), nil
	}

	return "", ErrNoThread
}

// A Posted comment is one already on a request, as far as a duplicate check
// needs it.
type Posted struct {
	Author string
	Body   string
	URL    string
}

// ThreadComments is every comment in thread, or the request's top-level
// conversation when thread is "". webURL is the request's own url, which a
// GitLab note's url hangs off.
func (h Host) ThreadComments(ctx context.Context, number int, webURL, thread string) ([]Posted, error) {
	n := strconv.Itoa(number)

	if h.GLab() {
		type note struct {
			ID     int64  `json:"id"`
			Body   string `json:"body"`
			System bool   `json:"system"`
			Author user   `json:"author"`
		}

		var notes []note

		if thread == "" {
			var err error
			if notes, err = DecodePages[note](
				ctx,
				h,
				"projects/:fullpath/merge_requests/"+n+"/notes?per_page=100",
			); err != nil {
				return nil, err
			}
		} else {
			var d struct {
				Notes []note `json:"notes"`
			}
			if err := h.Decode(
				ctx,
				&d,
				"api",
				"projects/:fullpath/merge_requests/"+n+"/discussions/"+thread,
			); err != nil {
				return nil, err
			}

			notes = d.Notes
		}

		out := make([]Posted, 0, len(notes))

		for _, nt := range notes {
			if !nt.System {
				out = append(out, Posted{Author: nt.Author.name(), Body: nt.Body, URL: noteURL(webURL, nt.ID)})
			}
		}

		return out, nil
	}

	type comment struct {
		ID        int64  `json:"id"`
		InReplyTo int64  `json:"in_reply_to_id"`
		Body      string `json:"body"`
		HTMLURL   string `json:"html_url"`
		User      user   `json:"user"`
	}

	path := "repos/{owner}/{repo}/issues/" + n + "/comments?per_page=100"
	if thread != "" {
		path = "repos/{owner}/{repo}/pulls/" + n + "/comments?per_page=100"
	}

	cs, err := DecodePages[comment](ctx, h, path)
	if err != nil {
		return nil, err
	}

	out := make([]Posted, 0, len(cs))

	for _, c := range cs {
		if thread != "" && strconv.FormatInt(cmp.Or(c.InReplyTo, c.ID), 10) != thread {
			continue
		}

		out = append(out, Posted{Author: c.User.name(), Body: c.Body, URL: c.HTMLURL})
	}

	return out, nil
}

// PostComment posts body in thread, or as a top-level comment when thread is
// "", and returns the new comment's url.
func (h Host) PostComment(ctx context.Context, number int, webURL, thread, body string) (string, error) {
	n := strconv.Itoa(number)

	var posted struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}

	var path string

	switch {
	case h.GLab() && thread == "":
		path = "projects/:fullpath/merge_requests/" + n + "/notes"
	case h.GLab():
		path = "projects/:fullpath/merge_requests/" + n + "/discussions/" + thread + "/notes"
	case thread == "":
		path = "repos/{owner}/{repo}/issues/" + n + "/comments"
	default:
		path = "repos/{owner}/{repo}/pulls/" + n + "/comments/" + thread + "/replies"
	}

	if err := h.Decode(ctx, &posted, "api", "-X", "POST", path, "-f", "body="+body); err != nil {
		return "", err
	}

	if h.GLab() {
		return noteURL(webURL, posted.ID), nil
	}

	if posted.HTMLURL == "" {
		return "", fmt.Errorf("%s returned no url for the comment", h.Name)
	}

	return posted.HTMLURL, nil
}

func noteURL(webURL string, id int64) string {
	return strings.TrimRight(webURL, "/") + "#note_" + strconv.FormatInt(id, 10)
}

// A CommitStatus is one named status on a commit; State is "success",
// "failure" or the host's own word for anything else.
type CommitStatus struct {
	Name        string
	State       string
	Description string
}

// LatestStatus is the newest status named name on sha, nil when it has none.
func (h Host) LatestStatus(ctx context.Context, sha, name string) (*CommitStatus, error) {
	if h.GLab() {
		type status struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Status      string `json:"status"`
			Description string `json:"description"`
		}

		ss, err := DecodePages[status](ctx, h, "projects/:fullpath/repository/commits/"+sha+
			"/statuses?all=true&per_page=100&name="+url.QueryEscape(name))
		if err != nil {
			return nil, err
		}

		var newest *status

		for i, s := range ss {
			if s.Name == name && (newest == nil || s.ID > newest.ID) {
				newest = &ss[i]
			}
		}

		if newest == nil {
			return nil, nil
		}

		state := newest.Status
		if state == "failed" {
			state = "failure"
		}

		return &CommitStatus{Name: name, State: state, Description: newest.Description}, nil
	}

	type status struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		Description string `json:"description"`
	}

	// newest first, so the first with the name is the one that counts
	ss, err := DecodePages[status](ctx, h, "repos/{owner}/{repo}/commits/"+sha+"/statuses?per_page=100")
	if err != nil {
		return nil, err
	}

	for _, s := range ss {
		if s.Context == name {
			return &CommitStatus{Name: name, State: s.State, Description: s.Description}, nil
		}
	}

	return nil, nil
}

// SetStatus sets a named status on sha; state is "success" or "failure",
// which GitLab spells "failed".
func (h Host) SetStatus(ctx context.Context, sha string, s CommitStatus) error {
	if h.GLab() {
		state := s.State
		if state == "failure" {
			state = "failed"
		}

		_, err := h.Run(ctx, "api", "-X", "POST", "projects/:fullpath/statuses/"+sha,
			"-f", "state="+state, "-f", "name="+s.Name, "-f", "description="+s.Description)

		return err
	}

	_, err := h.Run(ctx, "api", "-X", "POST", "repos/{owner}/{repo}/statuses/"+sha,
		"-f", "state="+s.State, "-f", "context="+s.Name, "-f", "description="+s.Description)

	return err
}
