package api

import (
	"context"
	"fmt"
	"io"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/launch"
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
			t, err := target.At(ctx, a.Main, false, "asks")
			if err != nil {
				return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
			}

			if _, err := sup.ResumeDrive(a.Root, a.Main, driveEnvFor(sup, t), a.Drive); err != nil {
				return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
			}

			fmt.Fprintf(w, "drive %d resumed\n", a.Drive)

			return nil
		},
		Live: func(a asks.Ask) bool {
			_, held := drivestore.Probe(a.Root, drivestore.DriveKey(a.Drive))

			return held
		},
	}
}
