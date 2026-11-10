package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The forward verb hands a command to the agent already working on a task:
// a `/f10:ship` typed outside the task's worktree would run on the wrong
// branch, without the plan, so the skills call this first.
func forwardCommand() *cli.Command {
	return &cli.Command{
		Name:      "forward",
		Hidden:    true,
		Category:  plumbingCategory,
		Usage:     "hand a command to the agent working on a task in another worktree",
		ArgsUsage: "<task-id>[-suffix] <text...>",
		Action:    runForward,
	}
}

// drivenMarker tells a skill the run is driven from another session: no
// interactive question, every ask through run state (modes/driven.md).
const drivenMarker = "--driven"

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

	return forward(ctx, cmd.Writer, forwardInput{
		main:     cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		here:     t.Layout.CheckoutRoot,
		driver:   t.Driver(),
		task:     r,
		suffix:   suffix,
		text:     text,
		stateDir: state.Dir(),
		ttl:      state.TTL(),
		now:      time.Now(),
	})
}

// forwardInput is everything forward needs once the target is settled, so
// the flow can be tested without a checkout.
type forwardInput struct {
	main     string         // the main checkout: worktrees are its siblings
	here     string         // the checkout this command answers for: a task here is not forwarded
	driver   *driver.Driver // nil without one
	task     ref.Ref
	suffix   string
	text     string        // what the agent is told, before the marker
	stateDir string        // where run state lives, for the idle check
	ttl      time.Duration // how long a run counts as live
	now      time.Time
	since    time.Time // runs last updated before it are not read: the agent that wrote them is gone
}

// forward: every refusal comes before the one thing that leaves this process.
func forward(ctx context.Context, w io.Writer, in forwardInput) error {
	name, path, err := stack.TaskCheckout(ctx, in.main, in.driver, in.task, in.suffix)
	if err != nil {
		return err
	}

	if reason := hereReason(in, name, path); reason != "" {
		return ui.Exit(ui.ExitDeclined, "%s", reason)
	}

	if err := agents("").Available(); err != nil {
		return err
	}

	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return err
	}

	ws := workspaceAt(wss, path)
	if ws == nil {
		return fmt.Errorf("no %s workspace shows %s (branch %s): open it with f10 start %s",
			agents(in.main).Name(), path, name, in.task.ID)
	}

	if err := refuseStalled(in.stall(), ws, agentctl.AgentName(in.task.ID, in.suffix, path), path); err != nil {
		return err
	}

	text := driven(in.text)

	note, err := promptChecked(ctx, path, in.task.ID, in.suffix, text)
	if err != nil {
		return err
	}

	return render.WriteReport(w, []render.Fact{
		render.TaskFact(ctx, w, in.main, in.task.ID),
		{Label: "workspace", Value: ws.ID},
		{Label: "sent", Value: text},
	}, appendNote(nil, note))
}

func hereReason(in forwardInput, name, path string) string {
	switch {
	case name == "":
		return in.task.ID + " has no branch here: run it in this session"
	case path == "":
		return fmt.Sprintf("branch %s is checked out nowhere: run it in this session", name)
	case gitx.SamePath(path, in.here):
		return fmt.Sprintf("branch %s is checked out here: run it in this session", name)
	}

	return ""
}

// A stallCheck is what refuseStalled reads run state with: runs updated
// before since were written by an agent that is gone.
type stallCheck struct {
	stateDir   string
	ttl        time.Duration
	now, since time.Time
}

func (in forwardInput) stall() stallCheck {
	return stallCheck{stateDir: in.stateDir, ttl: in.ttl, now: in.now, since: in.since}
}

// refuseStalled stops a forward to an agent idle while its run says running:
// it sits on something f10 cannot see (a permission dialog, a question asked
// outside driven mode), and a prompt behind that would wait with it.
func refuseStalled(in stallCheck, ws *agentctl.Workspace, agent, root string) error {
	if ws.Status != agentctl.StatusIdle {
		return nil
	}

	live, err := state.List(in.stateDir, in.now, in.ttl)
	if err != nil {
		return err
	}

	for _, r := range inRoot(live, root) {
		if r.Updated.Before(in.since) {
			continue
		}

		for _, p := range state.Phases {
			if r.Status(p) != "running" {
				continue
			}

			return fmt.Errorf("agent %s in workspace %s is idle while its run says %s %s: "+
				"it is waiting on something in its own pane: answer it there, or if that run was cancelled, "+
				"quit that agent and rerun f10 start or f10 drive, which start a new one in its place",
				agent, cmp.Or(ws.Label, ws.ID), p, r.PhaseText(p))
		}
	}

	return nil
}

// driven marks an f10 command as sent from outside; plain text (an answer, a
// steering note) goes as it is, since the agent it reaches already knows.
func driven(text string) string {
	if !strings.HasPrefix(text, "/f10:") || slices.Contains(strings.Fields(text), drivenMarker) {
		return text
	}

	return text + " " + drivenMarker
}
