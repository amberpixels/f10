// Command f10 is the read-only lookaround binary: it prints the effective
// configuration of the repo it runs in with a provenance per value, and
// reads the tasks, plans, demo reports and pull requests that
// configuration points at, and where a run is right now.
//
// Read-only means it changes nothing - no file it did not read, no tracker
// item, no cache - with one exception: `init` writes the two files that
// register a project, creating .f10/instructions/project.md and adding one
// line to the repo's info/exclude, and it never overwrites either. It does
// act on the reader's behalf, handing a url to a browser or a file to an
// editor, and that is the whole of what leaves this process.
// bin/resolve.sh stays the agent-facing surface for a pipeline run; this is
// the surface for a person, and for the one command an agent runs to read a
// task.
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
		Usage:   "look around a project's f10 configuration, tasks and plans",
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
		},
	}
}
