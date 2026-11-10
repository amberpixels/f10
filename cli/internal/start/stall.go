package start

import (
	"cmp"
	"fmt"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// A StallCheck is what refuseStalled reads run state with: runs updated
// before since were written by an agent that is gone.
type StallCheck struct {
	StateDir   string
	Ttl        time.Duration
	Now, Since time.Time
}

// RefuseStalled stops a forward to an agent idle while its run says running:
// it sits on something f10 cannot see (a permission dialog, a question asked
// outside driven mode), and a prompt behind that would wait with it.
func RefuseStalled(in StallCheck, ws *agentctl.Workspace, agent, root string) error {
	if ws.Status != agentctl.StatusIdle {
		return nil
	}

	live, err := state.List(in.StateDir, in.Now, in.Ttl)
	if err != nil {
		return err
	}

	for _, r := range statusview.InRoot(live, root) {
		if r.Updated.Before(in.Since) {
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
