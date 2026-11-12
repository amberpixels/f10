package drive

import (
	"context"
	"errors"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// watchRed is a red head on an undriven open request, told once per head.
const watchRed = "red"

// watchCI is the last checks a pass read on a task's request head, so a
// settled head is not read again, and the head and triage record a triage
// was last sent for.
type watchCI struct {
	SHA   string `yaml:"sha"`
	State string `yaml:"state"`
	Sent  string `yaml:"sent,omitempty"`
}

// ciStep tells a red head once, and under watch_ci: on sends it to an idle
// agent once per head and triage record. A green head is read again only
// once it moves; a pending or red one every pass, since a rerun settles it
// on the same head.
func (in WatchInput) ciStep(ctx context.Context, task WatchedTask, store map[string]watchReport) *watchReport {
	pr := task.Pr
	if in.Checks == nil || task.Owner != "" || task.Off || task.HostErr != nil || pr == nil ||
		pr.Status != forge.Open || pr.HeadSHA == "" {
		return nil
	}

	held := store[task.ID]
	if prev := held.CI; prev != nil && prev.SHA == pr.HeadSHA && prev.State != forge.Pending &&
		prev.State != forge.Red {
		return nil
	}

	hc, err := in.Checks(ctx, *pr)
	if err != nil {
		in.warnf("%s: reading the checks on %s: %v", task.ID, pr.URL, err)

		return nil
	}

	rec := &watchCI{SHA: pr.HeadSHA, State: hc.State}
	if held.CI != nil {
		rec.Sent = held.CI.Sent
	}

	held.CI = rec
	store[task.ID] = held

	if err := branchmeta.ClearOnGreen(ctx, in.Main, task.Branch, hc.State == forge.Green); err != nil {
		in.warnf("%s: clearing the triage record: %v", task.ID, err)
	}

	if hc.State != forge.Red {
		return nil
	}

	return in.redReport(ctx, task, hc, rec)
}

// redReport tells the red head, sending the triage where the pass may. A
// send is keyed by the head and the triage record, so a rerun that fails
// again on the same head is sent once more, and the step asks.
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

	tr, err := branchmeta.ReadTriage(ctx, in.Main, task.Branch)
	if err != nil {
		in.warnf("%s: reading the triage record: %v", task.ID, err)

		return rep
	}

	key := hc.SHA + " " + tr.Format()
	sentRep := &watchReport{
		Kind: watchRed,
		Text: fmt.Sprintf("%s checks red at %s%s: sent /f10:ci to its agent", task.Pr.URL, at, jobs),
		Key:  watchRed + " sent " + key,
	}

	// a head and record already sent keep their report, so neither is told
	// nor sent again
	if rec.Sent == key {
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

	rec.Sent = key

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
