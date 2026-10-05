// Command f10 is the lookaround binary and the one that starts a task. It
// prints the effective configuration of the repo it runs in with a
// provenance per value, reads the tasks, plans, demo reports and pull
// requests that configuration points at, and says where a run is right now.
//
// The lookaround verbs change nothing - no file they did not read, no
// tracker item, no cache - and act on the reader's behalf only by handing a
// url to a browser or a file to an editor. Two verbs write: `init` creates
// the two files that register a project and never overwrites either, and
// `start` creates a branch and a worktree, opens them in Herdr and prompts
// an agent there. bin/resolve.sh stays the agent-facing surface for a
// pipeline run; this is the surface for a person, and for the commands an
// agent runs to read a task or start the next one.
//
// A verb means one thing under every noun: `read` writes content, `open`
// follows an address, `search` finds by description. The matrix is sparse
// where a verb has no meaning for a noun; it never redefines one to fill a
// hole.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

const version = "0.1.0"

func main() {
	if err := newApp().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "f10:", err)
		os.Exit(1)
	}
}

// newApp builds the command tree. It is a function so a test can run the
// real one rather than a copy of its wiring.
func newApp() *cli.Command {
	return &cli.Command{
		Name:    "f10",
		Usage:   "look around a project's f10 configuration, tasks and plans, and start a task",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "C",
				Aliases: []string{"project"},
				Usage:   "answer for another checkout: a `PATH` always, or a project name once F10_ROOTS is set",
			},
		},
		Commands: []*cli.Command{
			initCommand(),
			configCommand(),
			taskCommand(),
			planCommand(),
			demoCommand(),
			prCommand(),
			statusCommand(),
			startCommand(),
		},
	}
}
