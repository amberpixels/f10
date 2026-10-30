package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The pr noun needs nothing declared: the CLI follows from the git remote,
// which probe already answers.
func prCommand() *cli.Command {
	return &cli.Command{
		Name:  "pr",
		Usage: "open a pull or merge request, or their list; read a change's base, diff and description",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the PR/MR in the browser: a number is a PR/MR, a task id its PR/MR; with none, the list",
				ArgsUsage: "[number | task-id]",
				Flags:     []cli.Flag{linkFlag(), printFlag()},
				Action:    runPROpen,
			},
			{
				Name:      "base",
				Usage:     "the branch a change is based on: its open PR/MR's target, else the stacked base, else the default branch",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags: []cli.Flag{&cli.BoolFlag{
					Name:  "origin",
					Usage: "fail when origin does not have the base, which a PR cannot target",
				}},
				Action: runPRBase,
			},
			{
				Name: "diff",
				Usage: "a change against its base: the working tree for the branch checked out here, " +
					"else its open PR/MR's diff, else the branch's commits",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "name-only", Usage: "list the changed files only"}},
				Action:    runPRDiff,
			},
			{
				Name:      "view",
				Usage:     "a change's branch, base, task and open PR/MR with its description",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "comments", Usage: "add the PR/MR's comments"}},
				Action:    runPRView,
			},
			{
				Name: "create",
				Usage: "open a PR/MR for the branch checked out here against its base and print its url; " +
					"an open one is printed instead",
				Hidden:   true,
				Category: plumbingCategory,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "title", Usage: "the `TITLE`", Required: true},
					&cli.StringFlag{Name: "body-file", Usage: "the body from `FILE`; - reads stdin", Required: true},
					&cli.StringSliceFlag{Name: "label", Usage: "a `LABEL`; repeat for several"},
					&cli.StringSliceFlag{Name: "assignee", Usage: "an `ASSIGNEE`; repeat for several"},
					&cli.BoolFlag{Name: "draft", Usage: "open it as a draft"},
				},
				Action: runPRCreate,
			},
			{
				Name:      "edit",
				Usage:     "rewrite an open PR/MR's title, its body, or both; base, labels and assignees stay",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "title", Usage: "the new `TITLE`"},
					&cli.StringFlag{Name: "body-file", Usage: "the new body from `FILE`; - reads stdin"},
				},
				Action: runPREdit,
			},
			{
				Name: "comment",
				Usage: "post the markdown on stdin as a comment on an open PR/MR and print its url; " +
					"one already posted with that text is not posted again",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "reply-to",
						Usage: "reply in the thread this review or comment `URL` names, else comment at the top level",
					},
					&cli.StringFlag{
						Name:  "quote",
						Usage: "a line of `TEXT` quoted above a reply that fell back to the top level",
					},
				},
				Action: runPRComment,
			},
			{
				Name: "status",
				Usage: "set a named commit status on an open PR/MR's head; one already set the same way " +
					"is left alone",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  plumbingCategory,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "the status `NAME` (GitHub's context)", Required: true},
					&cli.StringFlag{Name: "state", Usage: "`success` or failure", Required: true},
					&cli.StringFlag{
						Name: "description", Aliases: []string{"m"}, Required: true,
						Usage: "the `TEXT`, cut to 140 characters",
					},
					&cli.StringFlag{
						Name:  "sha",
						Usage: "refuse unless the head is this `SHA` or this SHA is a push on top of it",
					},
				},
				Action: runPRStatus,
			},
			prCatchupCommand(),
		},
	}
}

func runPRBase(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := resolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.lookupErr != nil {
		fmt.Fprintf(cmd.ErrWriter, "f10: no PR read (%v), so the base is the recorded or default branch\n", s.lookupErr)
	}

	// the same base diff forks from, so a step reading both sees one answer;
	// a gone recorded base is said below as a typed line, not a warning
	base, err := s.base(ctx, io.Discard)
	if err != nil {
		return err
	}

	if cmd.Bool("origin") && refSHA(ctx, s.main, "refs/remotes/origin/"+base) == "" {
		return fmt.Errorf("the base %s is not on origin: push it first", base)
	}

	out := base + "\n"

	if s.target == "" {
		// the same found: value pr catchup prints, so both steps key off one word
		after, sha, err := recordedBase(ctx, s.main, s.branch)
		if err != nil {
			return err
		}

		if after != "" && sha == "" {
			out += "found: " + foundGone + "\n"
		}
	}

	_, err = io.WriteString(cmd.Writer, out)

	return err
}

// runPRDiff reads the checkout's own state where it can: a branch checked out
// here is diffed with its uncommitted work, which no pushed PR holds.
func runPRDiff(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := resolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.lookupErr != nil {
		fmt.Fprintf(cmd.ErrWriter, "f10: no PR read (%v), so the base is the recorded or default branch\n", s.lookupErr)
	}

	var out string

	if s.number > 0 && !s.here {
		h, err := t.host()
		if err != nil {
			return err
		}

		if out, err = h.PullDiff(ctx, s.number); err != nil {
			return err
		}

		if cmd.Bool("name-only") {
			out = patchFiles(out)
		}
	} else {
		base, err := s.base(ctx, cmd.ErrWriter)
		if err != nil {
			return err
		}

		dir := cmp.Or(s.path, s.main)

		args := []string{"diff"}
		if cmd.Bool("name-only") {
			args = append(args, "--name-only")
		}

		head := s.headRef()
		if s.here {
			if head, err = capturedHead(ctx, dir); err != nil {
				return err
			}
		}

		into, tree, err := mergedTree(ctx, dir, head, base)
		if err == nil {
			out, err = gitx.Exec(ctx, dir, append(args, into, tree)...)
		}

		// a conflicting branch has no merged tree, and one main already holds
		// merges to nothing; explain and judge still read its change after the
		// merge, so both diff from the fork point
		if err != nil || out == "" {
			why := "main already holds it"
			if err != nil {
				why = fmt.Sprintf("it does not merge: %v", err)
			}

			fmt.Fprintf(cmd.ErrWriter,
				"f10: %s merged into %s shows nothing new (%s), so the diff is from where it forked\n",
				s.branch, base, why)

			from, err := forkPoint(ctx, dir, head, base)
			if err != nil {
				return err
			}

			if out, err = gitx.Exec(ctx, dir, append(args, from, head)...); err != nil {
				return err
			}
		}
	}

	if out == "" {
		return nil
	}

	_, err = fmt.Fprintln(cmd.Writer, out)

	return err
}

// patchFiles is the paths a unified diff touches, for a host diff with no
// --name-only of its own.
func patchFiles(patch string) string {
	var files []string

	for line := range strings.SplitSeq(patch, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			if _, b, ok := strings.Cut(rest, " b/"); ok {
				files = append(files, b)
			}
		}
	}

	return strings.Join(files, "\n")
}

// viewFields is what view reads of a request by number.
const viewFields = "number,url,state,title,body,author,headRefName,headRefOid,baseRefName"

func runPRView(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := resolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	base, err := s.base(ctx, cmd.ErrWriter)
	if err != nil {
		return err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "branch: %s\nbase: %s\n", s.branch, base)

	if s.task != nil {
		fmt.Fprintf(&b, "task: %s\n", s.task.Label())
	}

	if s.lookupErr != nil {
		fmt.Fprintf(&b, "\nno PR read for %s: %v\n", s.branch, s.lookupErr)
		_, err = io.WriteString(cmd.Writer, b.String())

		return err
	}

	if s.number == 0 {
		fmt.Fprintf(&b, "\nno open PR for %s\n", s.branch)
		_, err = io.WriteString(cmd.Writer, b.String())

		return err
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	var p pull
	if err := h.Decode(ctx, &p, h.prArgs(strconv.Itoa(s.number), h.Pick(
		[]string{"--json", viewFields}, []string{"-F", "json"})...)...); err != nil {
		return err
	}

	sigil := "#"
	if h.GLab() {
		sigil = "!"
	}

	fmt.Fprintf(&b, "pr: %s%s\nurl: %s\nstate: %s\nhead: %s\ntarget: %s\nauthor: %s\n",
		sigil, p.id(), p.url(), p.status(), p.headSHA(), p.base(), p.author())

	b.WriteString(mineLine(ctx, cmd.ErrWriter, h, p))

	fmt.Fprintf(&b, "title: %s\n", p.Title)

	if body := strings.TrimSpace(p.body()); body != "" {
		fmt.Fprintf(&b, "\n## Description\n\n%s\n", body)
	}

	if cmd.Bool("comments") {
		comments, err := h.PullComments(ctx, s.number)
		if err != nil {
			return err
		}

		if len(comments) > 0 {
			b.WriteString("\n## Comments\n")
		}

		for _, c := range comments {
			fmt.Fprintf(&b, "\n**%s** (%s):\n\n%s\n", c.Author, c.At.Format(time.DateOnly), strings.TrimSpace(c.Body))
		}
	}

	_, err = io.WriteString(cmd.Writer, b.String())

	return err
}

func runPROpen(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	number := cmd.Args().First()

	// a bare number is a request; anything else names a task, whose open
	// request opens, else its last one
	if number != "" && pullNumberOf(number) == 0 {
		s, err := resolvePRSubject(ctx, t, number)
		if err != nil {
			return err
		}

		if s.number == 0 {
			return fmt.Errorf("%s has no PR: its branch %s was never opened as one", number, s.branch)
		}

		number = strconv.Itoa(s.number)
	}

	switch {
	case number == "" && cmd.Bool("print"):
		rows, err := h.pullRows(ctx)
		if err != nil {
			return err
		}

		return writeRows(cmd.Writer, rows, "no open requests")
	case number == "":
		url, err := h.pullsURL(ctx)
		if err != nil {
			return err
		}

		return follow(ctx, cmd.Writer, h.Dir, url, cmd.Bool("link"))
	case cmd.Bool("print"):
		return shell.Passthrough(ctx, h.Dir, h.Name, h.prArgs(number)...)
	}

	if cmd.Bool("link") {
		url, err := h.prURL(ctx, number)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.Writer, url)

		return err
	}

	return shell.Passthrough(ctx, h.Dir, h.Name, h.prArgs(number, "--web")...)
}

// prArgs: GitHub has pull requests, GitLab merge requests.
func (h host) prArgs(number string, tail ...string) []string {
	args := h.Pick([]string{"pr", "view"}, []string{"mr", "view"})

	if number != "" {
		args = append(args, number)
	}

	return append(args, tail...)
}

// pullRows is the first page of open requests, newest first.
func (h host) pullRows(ctx context.Context) ([]row, error) {
	var found []pull

	err := h.Decode(ctx, &found, h.Pick(
		[]string{"pr", "list", "--json", "number,title,state,url", "--limit", "30"},
		[]string{"mr", "list", "-F", "json", "--per-page", "30"},
	)...)
	if err != nil {
		return nil, err
	}

	sigil := "#"
	if h.GLab() {
		sigil = "!"
	}

	rows := make([]row, 0, len(found))
	for _, p := range found {
		rows = append(rows, row{
			ID:     sigil + strconv.Itoa(cmp.Or(p.Number, p.IID)),
			Title:  p.Title,
			Status: p.status(),
			URL:    p.url(),
		})
	}

	return rows, nil
}

func (h host) prURL(ctx context.Context, number string) (string, error) {
	var payload map[string]any

	err := h.Decode(ctx, &payload, h.Pick(
		h.prArgs(number, "--json", "url"),
		h.prArgs(number, "-F", "json"),
	)...)
	if err != nil {
		return "", err
	}

	field := "url"
	if h.GLab() {
		field = "web_url"
	}

	url, _ := payload[field].(string)
	if url == "" {
		return "", fmt.Errorf("%s returned no %s", h.Name, field)
	}

	return url, nil
}
