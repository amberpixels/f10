package start

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
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
		driver:   t.Driver(),
		Task:     r,
		Suffix:   suffix,
		Text:     text,
		StateDir: state.Dir(),
		Ttl:      state.TTL(),
		Now:      time.Now(),
	})
}

// ForwardInput is everything forward needs once the target is settled, so
// the flow can be tested without a checkout.
type ForwardInput struct {
	Main     string         // the main checkout: worktrees are its siblings
	Here     string         // the checkout this command answers for: a task here is not forwarded
	driver   *driver.Driver // nil without one
	Task     ref.Ref
	Suffix   string
	Text     string        // what the agent is told, before the marker
	StateDir string        // where run state lives, for the idle check
	Ttl      time.Duration // how long a run counts as live
	Now      time.Time
	since    time.Time // runs last updated before it are not read: the agent that wrote them is gone
}

func (in ForwardInput) stall() start.StallCheck {
	return start.StallCheck{StateDir: in.StateDir, Ttl: in.Ttl, Now: in.Now, Since: in.since}
}

// Forward: every refusal comes before the one thing that leaves this process.
func Forward(ctx context.Context, w io.Writer, in ForwardInput) error {
	name, path, err := stack.TaskCheckout(ctx, in.Main, in.driver, in.Task, in.Suffix)
	if err != nil {
		return err
	}

	if reason := hereReason(in, name, path); reason != "" {
		return ui.Exit(ui.ExitDeclined, "%s", reason)
	}

	if err := worktree.Agents("").Available(); err != nil {
		return err
	}

	wss, err := worktree.Agents(in.Main).Workspaces(ctx)
	if err != nil {
		return err
	}

	ws := finish.WorkspaceAt(wss, path)
	if ws == nil {
		return fmt.Errorf("no %s workspace shows %s (branch %s): open it with f10 start %s",
			worktree.Agents(in.Main).Name(), path, name, in.Task.ID)
	}

	if err := start.RefuseStalled(in.stall(), ws, agentctl.AgentName(in.Task.ID, in.Suffix, path), path); err != nil {
		return err
	}

	text := launch.Driven(in.Text)

	note, err := launch.PromptChecked(ctx, path, in.Task.ID, in.Suffix, text)
	if err != nil {
		return err
	}

	return render.WriteReport(w, []render.Fact{
		render.TaskFact(ctx, w, in.Main, in.Task.ID),
		{Label: "workspace", Value: ws.ID},
		{Label: "sent", Value: text},
	}, finish.AppendNote(nil, note))
}

func hereReason(in ForwardInput, name, path string) string {
	switch {
	case name == "":
		return in.Task.ID + " has no branch here: run it in this session"
	case path == "":
		return fmt.Sprintf("branch %s is checked out nowhere: run it in this session", name)
	case gitx.SamePath(path, in.Here):
		return fmt.Sprintf("branch %s is checked out here: run it in this session", name)
	}

	return ""
}
