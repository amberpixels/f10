package api

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/target"
)

// deliverer moves an answer given through the API on as f10 asks answer
// does, except that a stopped drive resumes in this daemon directly: dialling
// its own socket would run an Ensurer against itself.
func deliverer(sup Supervisor) agents.Deliverer {
	return agents.Deliverer{
		Prompt: func(ctx context.Context, w io.Writer, a asks.Ask) error {
			note, err := launch.PromptChecked(ctx, a.Worktree, a.Task, a.Suffix, a.Answer)
			if note != "" {
				fmt.Fprintln(w, note)
			}

			return err
		},
		Resume: func(ctx context.Context, w io.Writer, a asks.Ask) error {
			// no daemon runs drives here: the user resumes it in the foreground
			if !daemon.Supported {
				fmt.Fprintf(
					w,
					"drive %d is stopped: f10 start --resume %d in %s takes the answer\n",
					a.Drive,
					a.Drive,
					a.Main,
				)

				return nil
			}

			t, err := target.At(ctx, a.Main, false, "asks")
			if err != nil {
				return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
			}

			if _, err := sup.ResumeDrive(a.Root, a.Main, driveEnvFor(sup, t), a.Drive); err != nil {
				return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
			}

			fmt.Fprintf(w, "drive %d resumed: f10 daemon logs %d -f follows it\n", a.Drive, a.Drive)

			return nil
		},
		Live: func(a asks.Ask) bool {
			_, held := drivestore.Probe(a.Root, drivestore.DriveKey(a.Drive))

			return held
		},
	}
}

// askLinks are the task urls the ledger holds for asks, by display project
// and task id: each ask's project read from its main checkout, as the asks
// list linked them in process.
func askLinks(ctx context.Context, all []asks.Ask) map[string]map[string]string {
	links := render.LinksOn(true)
	projects := map[string]string{}
	out := map[string]map[string]string{}

	for _, a := range all {
		dir := cmp.Or(a.Main, a.Worktree)
		if dir == "" {
			continue
		}

		if _, ok := projects[dir]; !ok {
			projects[dir] = target.ProjectOf(ctx, dir)
		}

		links.LoadAs(ctx, projects[dir], a.Project, a.Task)

		for id := range strings.SplitSeq(a.Task, ref.Separator) {
			if url := links.URL(a.Project, id); url != "" {
				if out[a.Project] == nil {
					out[a.Project] = map[string]string{}
				}

				out[a.Project][id] = url
			}
		}
	}

	return out
}
