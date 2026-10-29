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

	if p.status() != pullOpen {
		return host{}, pull{}, fmt.Errorf("%s is %s: nothing written", p.url(), p.status())
	}

	return h, p, nil
}

func runPREdit(ctx context.Context, cmd *cli.Command) error {
	title, file := cmd.String("title"), cmd.String("body-file")
	if title == "" && file == "" {
		return errors.New("pr edit needs --title, --body-file, or both")
	}

	var body string

	if file != "" {
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
			return fmt.Errorf("reading the body: %w", err)
		}

		if body = strings.TrimSpace(string(in)); body == "" {
			return fmt.Errorf("the body in %s is empty", file)
		}
	}

	h, p, err := openPull(ctx, cmd)
	if err != nil {
		return err
	}

	return h.EditPull(ctx, p.id(), title, body)
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

	url, err := commentOnce(ctx, cmd.ErrWriter, h, p, cmd.String("reply-to"), body)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(cmd.Writer, url)

	return err
}

// commentOnce never posts the same text twice where it lands: a retry after
// a post whose answer was lost finds it and returns its url instead.
func commentOnce(ctx context.Context, warn io.Writer, h host, p pull, replyTo, body string) (string, error) {
	number, _ := strconv.Atoi(p.id())

	var thread string

	if replyTo != "" {
		var err error

		thread, err = h.Thread(ctx, number, replyTo)

		switch {
		case errors.Is(err, forge.ErrNoThread):
			fmt.Fprintf(warn, "f10: %s names no thread: commenting at the top level\n", replyTo)
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
		return "", fmt.Errorf("head is %s, not %s: no status set", short(head), sha)
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

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}
