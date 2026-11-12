// Package pr is the f10 pr command: a change's base, diff and description, and
// opening, editing and commenting on its PR or MR.
package pr

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the pr noun, which needs nothing declared: the CLI follows from the git remote,
// which probe already answers.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "pr",
		Usage: "open a pull or merge request, or their list; read a change's base, diff and description",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the PR/MR in the browser: a number is a PR/MR, a task id its PR/MR; with none, the list",
				ArgsUsage: "[number | task-id]",
				Flags:     []cli.Flag{render.LinkFlag(), render.PrintFlag()},
				Action:    runPROpen,
			},
			{
				Name:      "base",
				Usage:     "the branch a change is based on: its open PR/MR's target, else the stacked base, else the default branch",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  command.Plumbing,
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
				Category:  command.Plumbing,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "name-only", Usage: "list the changed files only"}},
				Action:    runPRDiff,
			},
			{
				Name:      "view",
				Usage:     "a change's branch, base, task and open PR/MR with its description",
				ArgsUsage: "[PR | task-id]",
				Hidden:    true,
				Category:  command.Plumbing,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "comments", Usage: "add the PR/MR's comments"}},
				Action:    runPRView,
			},
			{
				Name: "create",
				Usage: "open a PR/MR for the branch checked out here against its base and print its url; " +
					"an open one is printed instead",
				Hidden:   true,
				Category: command.Plumbing,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "title",
						Usage:    "the `TITLE`; a bundle's riders are added after the lead id",
						Required: true,
					},
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
				Category:  command.Plumbing,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "title",
						Usage: "the new `TITLE`; a bundle's riders are added after the lead id",
					},
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
				Category:  command.Plumbing,
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
				Category:  command.Plumbing,
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
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := reviews.ResolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.LookupErr != nil {
		ui.Warnf(cmd.ErrWriter, "no PR read (%v), so the base is the recorded or default branch", s.LookupErr)
	}

	// the same base diff forks from, so a step reading both sees one answer;
	// a gone recorded base is said below as a typed line, not a warning
	base, err := s.Base(ctx, io.Discard)
	if err != nil {
		return err
	}

	if cmd.Bool("origin") && stack.RefSHA(ctx, s.Main, "refs/remotes/origin/"+base) == "" {
		return fmt.Errorf("the base %s is not on origin: push it first", base)
	}

	out := base + "\n"

	if s.Target == "" {
		// the same found: value pr catchup prints, so both steps key off one word
		after, sha, err := stack.RecordedBase(ctx, s.Main, s.Branch)
		if err != nil {
			return err
		}

		if after != "" && sha == "" {
			out += "found: " + stack.FoundGone + "\n"
		}
	}

	_, err = io.WriteString(cmd.Writer, out)

	return err
}

// runPRDiff reads the checkout's own state where it can: a branch checked out
// here is diffed with its uncommitted work, which no pushed PR holds.
func runPRDiff(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := reviews.ResolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.LookupErr != nil {
		ui.Warnf(cmd.ErrWriter, "no PR read (%v), so the base is the recorded or default branch", s.LookupErr)
	}

	var out string

	if s.Number > 0 && !s.Here {
		h, err := hostops.HostOf(t)
		if err != nil {
			return err
		}

		if out, err = h.PullDiff(ctx, s.Number); err != nil {
			return err
		}

		if cmd.Bool("name-only") {
			out = PatchFiles(out)
		}
	} else {
		base, err := s.Base(ctx, cmd.ErrWriter)
		if err != nil {
			return err
		}

		dir := cmp.Or(s.Path, s.Main)

		args := []string{"diff"}
		if cmd.Bool("name-only") {
			args = append(args, "--name-only")
		}

		head := s.HeadRef()
		if s.Here {
			if head, err = reviews.CapturedHead(ctx, dir); err != nil {
				return err
			}
		}

		into, tree, err := reviews.MergedTree(ctx, dir, head, base)
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

			ui.Warnf(cmd.ErrWriter,
				"%s merged into %s shows nothing new (%s), so the diff is from where it forked",
				s.Branch, base, why)

			from, err := stack.ForkPoint(ctx, dir, head, base)
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

// PatchFiles is the paths a unified diff touches, for a host diff with no
// --name-only of its own.
func PatchFiles(patch string) string {
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

func runPRView(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := reviews.ResolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	base, err := s.Base(ctx, cmd.ErrWriter)
	if err != nil {
		return err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "branch: %s\nbase: %s\n", s.Branch, base)

	if s.Task != nil {
		fmt.Fprintf(&b, "task: %s\n", s.Task.Label())
	}

	if s.LookupErr != nil {
		fmt.Fprintf(&b, "\nno PR read for %s: %v\n", s.Branch, s.LookupErr)
		_, err = io.WriteString(cmd.Writer, b.String())

		return err
	}

	if s.Number == 0 {
		fmt.Fprintf(&b, "\nno open PR for %s\n", s.Branch)
		_, err = io.WriteString(cmd.Writer, b.String())

		return err
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	p, err := h.PR(ctx, strconv.Itoa(s.Number))
	if err != nil {
		return err
	}

	fmt.Fprintf(&b, "pr: %s%d\nurl: %s\nstate: %s\nhead: %s\ntarget: %s\nauthor: %s\n",
		h.Sigil(), p.Number, p.URL, p.Status, p.HeadSHA, p.Base, p.Author)

	b.WriteString(MineLine(ctx, cmd.ErrWriter, h, p))

	fmt.Fprintf(&b, "title: %s\n", p.Title)

	if body := strings.TrimSpace(p.Body); body != "" {
		fmt.Fprintf(&b, "\n## Description\n\n%s\n", body)
	}

	if cmd.Bool("comments") {
		comments, err := h.PullComments(ctx, s.Number)
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
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	number := cmd.Args().First()

	// a bare number is a request; anything else names a task, whose open
	// request opens, else its last one
	if number != "" && reviews.PullNumberOf(number) == 0 {
		s, err := reviews.ResolvePRSubject(ctx, t, number)
		if err != nil {
			return err
		}

		if s.Number == 0 {
			return fmt.Errorf("%s has no PR: its branch %s was never opened as one", number, s.Branch)
		}

		number = strconv.Itoa(s.Number)
	}

	switch {
	case number == "" && cmd.Bool("print"):
		rows, err := h.PullRows(ctx)
		if err != nil {
			return err
		}

		return hostops.WriteRows(cmd.Writer, rows, "no open requests")
	case number == "":
		url, err := h.PullsURL(ctx)
		if err != nil {
			return err
		}

		return render.Follow(ctx, cmd.Writer, h.Dir, url, cmd.Bool("link"))
	case cmd.Bool("print"):
		return h.ShowPR(ctx, number, false)
	}

	if cmd.Bool("link") {
		url, err := h.PRURL(ctx, number)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.Writer, url)

		return err
	}

	return h.ShowPR(ctx, number, true)
}
