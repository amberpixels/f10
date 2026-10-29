package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// statusLimit is the longest description a commit status takes.
const statusLimit = 140

// openPull is the subject's open request, read whole, for a command that
// writes to it: a merged or closed one, or none, is refused by name.
func openPull(ctx context.Context, cmd *cli.Command) (host, pull, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return host{}, pull{}, err
	}

	s, err := resolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return host{}, pull{}, err
	}

	if s.lookupErr != nil {
		return host{}, pull{}, s.lookupErr
	}

	if s.number == 0 {
		return host{}, pull{}, fmt.Errorf("no open PR for %s", s.branch)
	}

	h, err := t.host()
	if err != nil {
		return host{}, pull{}, err
	}

	p, err := h.pullNumber(ctx, strconv.Itoa(s.number))
	if err != nil {
		return host{}, pull{}, err
	}

	if err := requireOpen(p); err != nil {
		return host{}, pull{}, err
	}

	return h, p, nil
}

func requireOpen(p pull) error {
	if p.status() != pullOpen {
		return fmt.Errorf("%s is %s: nothing written", p.url(), p.status())
	}

	return nil
}

func runPREdit(ctx context.Context, cmd *cli.Command) error {
	title, file := cmd.String("title"), cmd.String("body-file")
	if title == "" && file == "" {
		return errors.New("pr edit needs --title, --body-file, or both")
	}

	var body string

	if file != "" {
		var err error
		if body, err = readBody(cmd, file); err != nil {
			return err
		}
	}

	h, p, err := openPull(ctx, cmd)
	if err != nil {
		return err
	}

	return h.EditPull(ctx, p.id(), title, body)
}

// readBody reads a PR body from file, - meaning stdin; an empty one is
// refused, since it would blank the description.
func readBody(cmd *cli.Command, file string) (string, error) {
	var (
		in  []byte
		err error
	)

	if file == "-" {
		in, err = io.ReadAll(cmd.Reader)
	} else {
		in, err = os.ReadFile(filepath.Clean(file))
	}

	if err != nil {
		return "", fmt.Errorf("reading the body: %w", err)
	}

	body := strings.TrimSpace(string(in))
	if body == "" {
		return "", fmt.Errorf("the body in %s is empty", file)
	}

	return body, nil
}

// runPRCreate opens the branch checked out here against the base pr base
// prints, so the two never disagree. An open request is printed instead, so
// a retried step never opens two.
func runPRCreate(ctx context.Context, cmd *cli.Command) error {
	body, err := readBody(cmd, cmd.String("body-file"))
	if err != nil {
		return err
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := resolvePRSubject(ctx, t, "")
	if err != nil {
		return err
	}

	if s.lookupErr != nil {
		return fmt.Errorf("could not tell whether %s has an open PR: %w", s.branch, s.lookupErr)
	}

	if s.number > 0 {
		h, err := t.host()
		if err != nil {
			return err
		}

		p, err := h.pullNumber(ctx, strconv.Itoa(s.number))
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrWriter, "f10: already open for %s, nothing created\n", s.branch)
		_, err = fmt.Fprintln(cmd.Writer, p.url())

		return err
	}

	base, err := s.base(ctx, cmd.ErrWriter)
	if err != nil {
		return err
	}

	if refSHA(ctx, s.main, "refs/remotes/origin/"+base) == "" {
		return fmt.Errorf("the base %s is not on origin: push it first", base)
	}

	if head := refSHA(ctx, t.lay.CheckoutRoot, "HEAD"); refSHA(ctx, s.main, "refs/remotes/origin/"+s.branch) != head {
		return fmt.Errorf("origin does not have %s at HEAD: run `git push -u origin %s` first", s.branch, s.branch)
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	url, err := h.createPull(ctx, forge.NewPull{
		Head: s.branch, Base: base, Title: cmd.String("title"), Body: body,
		Labels: cmd.StringSlice("label"), Assignees: cmd.StringSlice("assignee"), Draft: cmd.Bool("draft"),
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(cmd.Writer, url)

	return err
}

// createPull reads the url back from the host, since neither CLI prints it in
// a form worth parsing.
func (h host) createPull(ctx context.Context, n forge.NewPull) (string, error) {
	if err := h.CreatePull(ctx, n); err != nil {
		return "", err
	}

	p, err := h.pull(ctx, n.Head)
	if err != nil {
		return "", fmt.Errorf("opened, but reading it back failed: %w", err)
	}

	return p.url(), nil
}

func runPRComment(ctx context.Context, cmd *cli.Command) error {
	in, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return fmt.Errorf("reading the comment from stdin: %w", err)
	}

	body := strings.TrimSpace(string(in))
	if body == "" {
		return errors.New("pr comment reads the comment as markdown on stdin, and it was empty")
	}

	h, p, err := openPull(ctx, cmd)
	if err != nil {
		return err
	}

	url, err := commentOnce(ctx, cmd.ErrWriter, h, p, cmd.String("reply-to"), cmd.String("quote"), body)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(cmd.Writer, url)

	return err
}

// commentOnce never posts the same text twice where it lands: a retry after
// a post whose answer was lost finds it and returns its url instead. quote
// heads a reply that fell back to the top level, which no thread places.
func commentOnce(ctx context.Context, warn io.Writer, h host, p pull, replyTo, quote, body string) (string, error) {
	number, _ := strconv.Atoi(p.id())

	var thread string

	if replyTo != "" {
		var err error

		thread, err = h.Thread(ctx, number, replyTo)

		switch {
		case errors.Is(err, forge.ErrNoThread):
			fmt.Fprintf(warn, "f10: %s names no thread: commenting at the top level\n", replyTo)

			if quote != "" {
				body = "> " + quote + "\n\n" + body
			}
		case err != nil:
			return "", err
		}
	}

	login, err := h.Login(ctx)
	if err != nil {
		return "", err
	}

	posted, err := h.ThreadComments(ctx, number, p.url(), thread)
	if err != nil {
		return "", err
	}

	for _, c := range posted {
		if strings.EqualFold(c.Author, login) && strings.TrimSpace(c.Body) == body {
			fmt.Fprintln(warn, "f10: already posted")

			return c.URL, nil
		}
	}

	return h.PostComment(ctx, number, p.url(), thread, body)
}

func runPRStatus(ctx context.Context, cmd *cli.Command) error {
	state := cmd.String("state")
	if state != "success" && state != "failure" {
		return fmt.Errorf("--state is success or failure, not %q", state)
	}

	h, p, err := openPull(ctx, cmd)
	if err != nil {
		return err
	}

	s := forge.CommitStatus{
		Name:        cmd.String("name"),
		State:       state,
		Description: cut(strings.TrimSpace(cmd.String("description")), statusLimit),
	}

	line, err := statusOnce(ctx, cmd.ErrWriter, h, p, cmd.String("sha"), s)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(cmd.Writer, line)

	return err
}

// statusOnce sets the status on the head only, so it never lands on a commit
// the PR has moved past; sha, when given, is the head the caller judged. One
// already set the same way is left alone, so a retry never posts twice.
func statusOnce(ctx context.Context, warn io.Writer, h host, p pull, sha string, s forge.CommitStatus) (string, error) {
	head := p.headSHA()
	if head == "" {
		return "", fmt.Errorf("%s returned no head sha for %s", h.Name, p.url())
	}

	if sha != "" && !strings.HasPrefix(head, sha) {
		// the host moves a PR's head a moment after a push lands, so a sha
		// built on the head it reports is that push, not a stale commit
		full, err := gitx.Exec(ctx, h.Dir, "rev-parse", "--verify", sha+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("head is %s, not %s: no status set", short(head), sha)
		}

		if ahead, err := gitx.IsAncestor(ctx, h.Dir, head, full); err != nil || !ahead {
			return "", fmt.Errorf("head is %s, not %s: no status set", short(head), sha)
		}

		head = full
	}

	line := fmt.Sprintf("%s %s %s", short(head), s.Name, s.State)

	prev, err := h.LatestStatus(ctx, head, s.Name)
	if err != nil {
		return "", err
	}

	if prev != nil && prev.State == s.State && prev.Description == s.Description {
		fmt.Fprintln(warn, "f10: already set")

		return line, nil
	}

	return line, h.SetStatus(ctx, head, s)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}

	return sha
}

// mineLine says whether the signed-in user opened p. A failed login read is
// unknown, never no, so a step guarding someone else's PR stops on it; the
// view itself goes on, since explain and demo need none of it.
func mineLine(ctx context.Context, warn io.Writer, h host, p pull) string {
	login, err := h.Login(ctx)

	switch {
	case err != nil:
		fmt.Fprintf(warn, "f10: who is signed in is unknown: %v\n", err)

		return "mine: unknown\n"
	case strings.EqualFold(p.author(), login):
		return "mine: yes\n"
	default:
		return "mine: no\n"
	}
}
