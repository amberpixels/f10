package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// The trace noun renders what a step leaves on the host for a reader of the
// history, so no step hand-writes the format. With traces off both verbs do
// nothing and exit 0, so every step calls them the same way and the prose
// carries no switch.
func traceCommand() *cli.Command {
	return &cli.Command{
		Name:     "trace",
		Hidden:   true,
		Category: plumbingCategory,
		Usage:    "the trailers a step's commit carries, rendered from the project's traces switch",
		Commands: []*cli.Command{
			{
				Name:  "trailers",
				Usage: "print the Task:, Step: and Review: trailer lines for a commit; nothing with traces off",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "step",
						Usage:    "the `STEP` whose work the commit carries: implement, resolve, judge, catchup, or a project-defined step",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "task",
						Usage: "the task `ID`, or a bundle (id+id); the branch's task and riders when absent",
					},
					&cli.IntFlag{Name: "round", Usage: "a resolve fix commit: the review round it settled"},
					&cli.IntFlag{Name: "fixed", Usage: "a resolve fix commit: findings fixed"},
					&cli.IntFlag{Name: "skipped", Usage: "a resolve fix commit: findings skipped"},
				},
				Action: runTraceTrailers,
			},
			{
				Name:  "state",
				Usage: "rewrite the hidden state block in the branch's open PR body; nothing with traces off",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "step",
						Usage:    "the `STEP` that is writing the block: pr, review, resolve, judge, or a project-defined step",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "task",
						Usage: "the task `ID`, or a bundle (id+id); the branch's task and riders when absent",
					},
					&cli.IntFlag{Name: "round", Usage: "the review round the counts describe; unset keeps the block's"},
					&cli.IntFlag{Name: "open", Usage: "findings awaiting a verdict; unset keeps the block's, 0 clears"},
					&cli.IntFlag{
						Name:  "asks",
						Usage: "questions waiting on a person; unset keeps the block's, 0 clears",
					},
					&cli.StringFlag{
						Name:  "body-file",
						Usage: "put the block into this drafted body `FILE` instead of editing the PR; the counts still carry from the PR's block",
					},
				},
				Action: runTraceState,
			},
		},
	}
}

func runTraceTrailers(ctx context.Context, cmd *cli.Command) error {
	counts, hasReview, err := reviewCounts(cmd)
	if err != nil {
		return err
	}

	var review *trace.Review
	if hasReview {
		review = &counts
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	if !t.eff.Traces {
		return nil
	}

	tasks, err := traceTasks(ctx, cmd, t, "Step: written alone")
	if err != nil {
		return err
	}

	out, err := trace.Render(trace.Trailers{Tasks: tasks, Step: cmd.String("step"), Review: review})
	if err != nil {
		return err
	}

	fmt.Fprint(cmd.Writer, out)

	return nil
}

// traceTasks uses the lookup itself, not target.reference: under -C the
// branch is the one -C points at, and never the session's task, since a
// branch naming no task carries none; that case is nil and a stderr line.
func traceTasks(ctx context.Context, cmd *cli.Command, t *target, alone string) ([]string, error) {
	r, err := ref.Lookup{Prefix: t.eff.IDPrefix, Dir: t.dir, NoSession: true}.Find(ctx, cmd.String("task"))

	switch {
	case err == nil:
		return r.Members(), nil
	case errors.Is(err, ref.ErrNoReference) && strings.TrimSpace(cmd.String("task")) == "":
		fmt.Fprintln(errWriter(cmd), "f10: no task on this branch: "+alone)

		return nil, nil
	default:
		return nil, err
	}
}

// runTraceState: no PR yet, one no longer open, or one someone else opened
// is a note and exit 0, so every step calls it the same way; an edited
// description is the author's. With traces off a draft still takes over the
// block the body holds, so the switch turned off never drops the last state.
func runTraceState(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	file := cmd.String("body-file")
	if !t.eff.Traces && file == "" {
		return nil
	}

	branch := gitx.CurrentBranch(ctx, t.dir)
	if branch == "" {
		return errors.New("no branch is checked out: the state block belongs to a branch's PR")
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	p, err := h.pull(ctx, branch)

	switch {
	case noSuchPull(err):
		fmt.Fprintf(errWriter(cmd), "f10: no PR for %s yet: no state to write\n", branch)

		return nil
	case err != nil:
		return err
	case p.status() != pullOpen:
		fmt.Fprintf(errWriter(cmd), "f10: %s is %s: its body is left alone\n", p.url(), p.status())

		return nil
	}

	login, err := h.Login(ctx)
	if err != nil {
		return err
	}

	if !strings.EqualFold(p.author(), login) {
		fmt.Fprintf(errWriter(cmd), "f10: %s was opened by %s: its body is left alone\n", p.url(), p.author())

		return nil
	}

	if !t.eff.Traces {
		return carryStateToFile(file, p.body())
	}

	tasks, err := traceTasks(ctx, cmd, t, "the block names none")
	if err != nil {
		return err
	}

	u := trace.Update{
		Step:  cmd.String("step"),
		After: branchAfter(ctx, t.dir, branch),
		Round: countFlag(cmd, "round"),
		Open:  countFlag(cmd, "open"),
		Asks:  countFlag(cmd, "asks"),
	}

	if len(tasks) > 0 {
		u.Task, u.Riders = tasks[0], tasks[1:]
	}

	if file != "" {
		return putStateInFile(file, p.body(), u)
	}

	out, err := trace.Rewrite(p.body(), u)
	if err != nil {
		return err
	}

	if out == p.body() {
		return nil
	}

	return h.editPullBody(ctx, branch, out)
}

// putStateInFile carries the counts from the body the host holds, so a step
// rewriting the description from scratch never copies the block by hand.
func putStateInFile(file, hostBody string, u trace.Update) error {
	file = filepath.Clean(file)

	draft, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	prev, _ := trace.ParseState(hostBody)

	out, err := trace.PutState(string(draft), trace.Merge(prev, u))
	if err != nil {
		return err
	}

	return os.WriteFile(file, []byte(out), 0o600) //nolint:gosec // the step's own draft, named on the command line
}

func carryStateToFile(file, hostBody string) error {
	file = filepath.Clean(file)

	draft, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	out := trace.CarryState(string(draft), hostBody)
	if out == string(draft) {
		return nil
	}

	return os.WriteFile(file, []byte(out), 0o600) //nolint:gosec // the step's own draft, named on the command line
}

// countFlag is an int flag the user passed, or nil for one left unset:
// the block keeps its count on nil and clears it on an explicit 0.
func countFlag(cmd *cli.Command, name string) *int {
	if !cmd.IsSet(name) {
		return nil
	}

	n := cmd.Int(name)

	return &n
}

func branchAfter(ctx context.Context, dir, branch string) string {
	return gitx.Out(ctx, dir, "config", "--get", "branch."+branch+"."+cfgAfter)
}

// reviewCounts reads the three review flags: all or none, since a Review:
// line with a count missing would read as zero. The bool is false without them.
func reviewCounts(cmd *cli.Command) (trace.Review, bool, error) {
	set := 0
	for _, name := range []string{"round", "fixed", "skipped"} {
		if cmd.IsSet(name) {
			set++
		}
	}

	switch set {
	case 0:
		return trace.Review{}, false, nil
	case 3:
		return trace.Review{Round: cmd.Int("round"), Fixed: cmd.Int("fixed"), Skipped: cmd.Int("skipped")}, true, nil
	default:
		return trace.Review{}, false,
			errors.New("--round, --fixed and --skipped go together: a Review: line carries all three counts")
	}
}

func errWriter(cmd *cli.Command) io.Writer {
	if w := cmd.Root().ErrWriter; w != nil {
		return w
	}

	return os.Stderr
}
