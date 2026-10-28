// Command f10 is the lookaround binary and the one that starts a task. It
// prints the effective configuration of the repo it runs in with a
// provenance per value, reads the tasks, plans, demo reports and pull
// requests that configuration points at, says where a run is right now, and
// reports what one person did in a checkout since a time.
//
// The lookaround verbs change nothing - no file they did not read, no
// tracker item, no cache - and act on the reader's behalf only by handing a
// url to a browser or a file to an editor. Six verbs act: `init` creates
// the two files that register a project and never overwrites either,
// `start` creates a branch and a worktree, opens them in Herdr and prompts
// an agent there, `forward` hands a command to the agent a task already has,
// `drive` runs a list of tasks through a chain of skills that way,
// `standup mark` records a standup as reported so the next one starts where
// it ended, and `finish` undoes start once the work merged: the PR merged on
// the host, the plan archived, the workspace closed, the worktree and branch
// removed, main pulled. bin/f10-load-project.sh stays the agent-facing
// surface for a pipeline run; this is the surface for a person, and for the commands an agent runs
// to read a task, start the next one, reach another or finish this one.
//
// `ledger dump` reads the local record hooks and badge calls append to.
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
	"runtime/debug"
	"strings"

	"github.com/urfave/cli/v3"
)

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
		Usage:   "look around a project's f10 configuration, tasks, plans and your activity, start a task, reach its agent, drive a list and finish it",
		Version: version(debug.ReadBuildInfo()),
		// -C takes a path whole, commas included
		DisableSliceFlagSeparator: true,
		// `f10 completion <shell>` prints the script; --flow completes the declared flows
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "C",
				Aliases: []string{"project"},
				Usage: "answer for another checkout: a `PATH` always, or a project name once F10_ROOTS is set; " +
					"standup and board take it more than once",
			},
		},
		Commands: []*cli.Command{
			initCommand(),
			configCommand(),
			flowCommand(),
			doctorCommand(),
			taskCommand(),
			planCommand(),
			demoCommand(),
			prCommand(),
			reviewCommand(),
			traceCommand(),
			statusCommand(),
			standupCommand(),
			boardCommand(),
			startCommand(),
			forwardCommand(),
			driveCommand(),
			daemonCommand(),
			finishCommand(),
			janitorCommand(),
			hookCommand(),
			stateCommand(),
			ledgerCommand(),
			statsCommand(),
			loreCommand(),
		},
	}
}

// stampedVersion is the plugin version a build was made for, set with
// `-ldflags "-X main.stampedVersion=X.Y.Z"` by the plugin's bin/f10 shim and
// the release workflow. Empty in any other build.
var stampedVersion string

// version is what `f10 --version` prints, read from the build rather than a
// constant that drifts. The skills describe the binary's flags, so the binary
// carries the plugin's version: the stamp when the shim or a release built
// it. Without one, a `go install ...@v0.25.0` prints the bare number, read
// from the `cli/vX.Y.Z` tag each release puts beside `f10--vX.Y.Z`, and a
// checkout build prints the commit, its date and a dirty mark, even at the
// tag, since Go derives a version from tags only for a module at the
// repository root.
func version(bi *debug.BuildInfo, ok bool) string {
	return versionOf(stampedVersion, bi, ok)
}

func versionOf(stamp string, bi *debug.BuildInfo, ok bool) string {
	if stamp != "" {
		return stamp
	}

	if !ok || bi == nil {
		return "unknown"
	}

	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}

	var revision, at, dirty string

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value[:min(12, len(s.Value))]
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " dirty"
			}
		}
	}

	if revision == "" {
		return "devel"
	}

	return "devel " + revision + " " + at + dirty
}
