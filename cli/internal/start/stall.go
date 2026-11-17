package start

import (
	"cmp"
	"fmt"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// A StallCheck is what RefuseStalled reads run state with: runs updated
// before since were written by an agent that is gone.
type StallCheck struct {
	StateDir   string
	Ttl        time.Duration
	Now, Since time.Time
}

// RefuseStalled stops a drive's prompt to an agent idle while its run says
// running: it may sit on a permission dialog, and a new skill prompt typed
// there could answer it. Forward reads the screen instead (forwardReady).
func RefuseStalled(in StallCheck, ws *agentctl.Workspace, agent, root string) error {
	if ws.Status != agentctl.StatusIdle {
		return nil
	}

	runs, err := liveRuns(in, root)
	if err != nil {
		return err
	}

	for _, r := range runs {
		if p := runningPhase(r); p != "" {
			return fmt.Errorf("agent %s in workspace %s is idle while its run says %s %s: "+
				"it is waiting on something in its own pane: answer it there, or if that run was cancelled, "+
				"quit that agent and rerun f10 start, which starts a new one in its place",
				agent, cmp.Or(ws.Label, ws.ID), p, r.PhaseText(p))
		}
	}

	return nil
}

// landing says where a forward's text lands: the open ask it answers, or the
// run an agent at rest stopped in. stalled names that run, "" when none.
func landing(in StallCheck, ws *agentctl.Workspace, root string) (string, string, error) {
	runs, err := liveRuns(in, root)
	if err != nil {
		return "", "", err
	}

	for _, r := range runs {
		if r.Ask != "" {
			return "the text answers the open ask: " + r.Ask, "", nil
		}
	}

	// done is idle after a turn nobody looked at, the likely state of a run
	// that stopped unattended
	if ws.Status != agentctl.StatusIdle && ws.Status != agentctl.StatusDone {
		return "", "", nil
	}

	for _, r := range runs {
		if p := runningPhase(r); p != "" {
			stalled := p + " " + r.PhaseText(p)

			return "agent was idle while its run says " + stalled + ": the text resumes that turn", stalled, nil
		}
	}

	return "", "", nil
}

// liveRuns are the runs in root an agent still writes: not expired, and
// updated since in.Since.
func liveRuns(in StallCheck, root string) ([]*state.Run, error) {
	live, err := state.List(in.StateDir, in.Now, in.Ttl)
	if err != nil {
		return nil, err
	}

	var out []*state.Run

	for _, r := range statusview.InRoot(live, root) {
		if !r.Updated.Before(in.Since) {
			out = append(out, r)
		}
	}

	return out, nil
}

func runningPhase(r *state.Run) string {
	for _, p := range state.Phases {
		if r.Status(p) == "running" {
			return p
		}
	}

	return ""
}
