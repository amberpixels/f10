package drive

import (
	"context"
	"errors"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// watchRed is a red head on an undriven open request, told once per head.
const watchRed = "red"

// watchCI is the last checks a pass read on a task's request head, so a
// settled head is not read again, and the head a triage was sent for.
type watchCI struct {
	SHA   string `yaml:"sha"`
	State string `yaml:"state"`
	Sent  string `yaml:"sent,omitempty"`
}

// ciStep tells a red head once, and under watch_ci: on sends it to an idle
// agent once. A green head is read again only once it moves; a pending or
// red one every pass, since a rerun settles it on the same head.
func (in WatchInput) ciStep(ctx context.Context, task WatchedTask, store map[string]watchReport) *watchReport {
	pr := task.Pr
	if in.Checks == nil || task.Owner != "" || task.Off || task.HostErr != nil || pr == nil ||
		pr.Status != forge.Open || pr.HeadSHA == "" {
		return nil
	}

	held := store[task.ID]

	rec := held.CI
	if rec == nil || rec.SHA != pr.HeadSHA || rec.State == forge.Pending || rec.State == forge.Red {
		hc, err := in.Checks(ctx, *pr)
		if err != nil {
			in.warnf("%s: reading the checks on %s: %v", task.ID, pr.URL, err)

			return nil
		}

		rec = &watchCI{SHA: pr.HeadSHA, State: hc.State}
		if held.CI != nil && held.CI.SHA == pr.HeadSHA {
			rec.Sent = held.CI.Sent
		}

		held.CI = rec
		store[task.ID] = held

		if hc.State == forge.Red {
			return in.redReport(ctx, task, hc, rec)
		}

		return nil
	}

	return nil
}

// redReport tells the red head, sending the triage where the pass may.
func (in WatchInput) redReport(ctx context.Context, task WatchedTask, hc forge.HeadChecks, rec *watchCI) *watchReport {
	at := gitx.ShortSHA(hc.SHA)
	jobs := ""

	if first, ok := hc.First(); ok {
		jobs = ": " + first.Name
		if first.URL != "" {
			jobs += " " + first.URL
		}
	}

	rep := &watchReport{
		Kind: watchRed,
		Text: fmt.Sprintf("%s checks red at %s%s", task.Pr.URL, at, jobs),
		Next: "/f10:ci in " + task.Path,
		Key:  watchRed + " " + hc.SHA,
	}

	sentRep := &watchReport{
		Kind: watchRed,
		Text: fmt.Sprintf("%s checks red at %s%s: sent /f10:ci to its agent", task.Pr.URL, at, jobs),
		Key:  watchRed + " sent " + hc.SHA,
	}

	// a head already sent keeps its report, so it is not told again
	if rec.Sent == hc.SHA {
		return sentRep
	}

	if !in.CI || !in.mayTriage(ctx, task) {
		return rep
	}

	sent, err := in.sendTriage(ctx, task, hc.SHA)
	if err != nil {
		in.warnf("%s: sending /f10:ci: %v", task.ID, err)

		return rep
	}

	if !sent {
		return rep
	}

	rec.Sent = hc.SHA

	return sentRep
}

// mayTriage is an idle agent with nothing pending and a clean tree.
func (in WatchInput) mayTriage(ctx context.Context, task WatchedTask) bool {
	if in.Prompt == nil || task.Ws == "" || !task.Listed ||
		(task.Agent != agentctl.StatusIdle && task.Agent != agentctl.StatusDone) {
		return false
	}

	return !waitingOnAsk(task.Run) && !in.parked(task) && stack.TreeBusy(ctx, task.Path) == ""
}

// sendTriage prompts the triage under the task's lease; false when someone
// else holds it.
func (in WatchInput) sendTriage(ctx context.Context, task WatchedTask, sha string) (bool, error) {
	lease, err := drivestore.AcquireAs(in.Root, task.ID, drivestore.Holder{Watch: true})
	if err != nil {
		if held := (drivestore.HeldError{}); errors.As(err, &held) {
			return false, nil
		}

		return false, err
	}

	defer lease.Release()

	text := launch.Driven("/f10:ci "+task.ID) + " && sent by the watcher at " + gitx.ShortSHA(sha) +
		": nobody follows up in this pane, so a question goes through f10 asks"
	if err := in.Prompt(ctx, task, text); err != nil {
		return false, err
	}

	return true, nil
}
