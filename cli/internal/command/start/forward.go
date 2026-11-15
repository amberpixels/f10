package start

import (
	"cmp"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// ForwardCommand is the forward verb, which hands a command to the agent already working on a task:
// a `/f10:ship` typed outside the task's worktree would run on the wrong
// branch, without the plan, so the skills call this first.
func ForwardCommand() *cli.Command {
	return &cli.Command{
		Name:      "forward",
		Hidden:    true,
		Category:  command.Plumbing,
		Usage:     "hand a command to the agent working on a task in another worktree",
		ArgsUsage: "<task-id>[-suffix] <text...>",
		Action:    runForward,
	}
}

func runForward(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()

	token := ""
	if len(args) > 0 {
		token = strings.TrimSpace(args[0])
	}

	if token == "" {
		return errors.New("forward needs a task id and the text to send")
	}

	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		return errors.New("forward needs the text to send after the task id")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	id, suffix := ref.SplitSuffix(token)

	r, err := t.Reference(ctx, id)
	if err != nil {
		return err
	}

	// a bundle's member goes to the bundle's agent, named for its lead
	r = r.Head()

	return Forward(ctx, cmd.Writer, ForwardInput{
		Main:     cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Here:     t.Layout.CheckoutRoot,
		Driver:   t.Driver(),
		Task:     r,
		Suffix:   suffix,
		Text:     text,
		StateDir: state.Dir(),
		Ttl:      state.TTL(),
		Now:      time.Now(),
	})
}

// ForwardInput is everything forward needs once the target is settled.
type ForwardInput = start.ForwardInput

// Forward hands the text on and reports where it went.
func Forward(ctx context.Context, w io.Writer, in ForwardInput) error {
	out, err := start.Forward(ctx, in)
	if err != nil {
		return err
	}

	return render.WriteReport(w, []render.Fact{
		render.TaskFact(ctx, w, in.Main, in.Task.ID),
		{Label: "workspace", Value: out.Workspace},
		{Label: "sent", Value: out.Sent},
	}, textx.AppendNote(nil, out.Note))
}
