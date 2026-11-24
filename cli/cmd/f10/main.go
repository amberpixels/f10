// Command f10 is the surface for a person and for the commands an agent runs
// between pipeline runs; bin/f10-load-project.sh is the surface for the run
// itself. Lookaround verbs change nothing and act for the reader only by
// handing a url to a browser or a file to an editor; init, start, stop,
// forward, standup mark and finish act. A verb means one thing under every noun
// (`read` writes content, `open` follows an address) and the matrix stays
// sparse rather than redefining one to fill a hole. `f10 --help` shows the
// human verbs; the verbs the steps call are Hidden under plumbingCategory.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/api"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/buildinfo"
	atlascmd "github.com/amberpixels/f10/cli/internal/command/atlas"
	auditcmd "github.com/amberpixels/f10/cli/internal/command/audit"
	boardcmd "github.com/amberpixels/f10/cli/internal/command/board"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	daemoncmd "github.com/amberpixels/f10/cli/internal/command/daemon"
	drivecmd "github.com/amberpixels/f10/cli/internal/command/drive"
	finishcmd "github.com/amberpixels/f10/cli/internal/command/finish"
	historycmd "github.com/amberpixels/f10/cli/internal/command/history"
	hookcmd "github.com/amberpixels/f10/cli/internal/command/hook"
	inboxcmd "github.com/amberpixels/f10/cli/internal/command/inbox"
	ledgercmd "github.com/amberpixels/f10/cli/internal/command/ledger"
	lorecmd "github.com/amberpixels/f10/cli/internal/command/lore"
	mcpcmd "github.com/amberpixels/f10/cli/internal/command/mcp"
	prcmd "github.com/amberpixels/f10/cli/internal/command/pr"
	refscmd "github.com/amberpixels/f10/cli/internal/command/refs"
	remotecmd "github.com/amberpixels/f10/cli/internal/command/remote"
	reviewcmd "github.com/amberpixels/f10/cli/internal/command/review"
	standupcmd "github.com/amberpixels/f10/cli/internal/command/standup"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	statecmd "github.com/amberpixels/f10/cli/internal/command/state"
	statscmd "github.com/amberpixels/f10/cli/internal/command/stats"
	statuscmd "github.com/amberpixels/f10/cli/internal/command/status"
	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	tracecmd "github.com/amberpixels/f10/cli/internal/command/trace"
)

// the control-plane commands serve the API in process where no daemon runs,
// and in tests
func init() {
	apiclient.Local = func() http.Handler { return api.Handler(apiclient.InProcess{}) }
}

func main() {
	if err := newApp().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "f10:", err)
		os.Exit(1)
	}
}

// newApp is a function so a test runs the real tree, not a copy of its wiring.
func newApp() *cli.Command {
	return &cli.Command{
		Name:    "f10",
		Usage:   "look around a project's f10 configuration, tasks, plans and your activity, start tasks, reach their agents and finish them",
		Version: buildinfo.Version(),
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
			configcmd.InitCommand(),
			configcmd.Command(),
			configcmd.AgentsCommand(),
			configcmd.FlowCommand(),
			configcmd.DoctorCommand(),
			taskcmd.Command(),
			taskcmd.PlanCommand(),
			taskcmd.DemoCommand(),
			prcmd.Command(),
			refscmd.Command(),
			reviewcmd.Command(),
			tracecmd.Command(),
			statuscmd.Command(),
			standupcmd.Command(),
			boardcmd.Command(),
			atlascmd.Command(),
			drivecmd.StartCommand(),
			drivecmd.ResumeCommand(),
			startcmd.StopCommand(),
			startcmd.ForwardCommand(),
			drivecmd.Command(),
			daemoncmd.Command(),
			remotecmd.Command(),
			mcpcmd.Command(),
			inboxcmd.Command(),
			inboxcmd.AsksAlias(),
			drivecmd.WatchCommand(),
			finishcmd.Command(),
			finishcmd.JanitorCommand(),
			hookcmd.Command(),
			statecmd.Command(),
			ledgercmd.Command(),
			statscmd.Command(),
			historycmd.Command(),
			auditcmd.Command(),
			lorecmd.Command(),
			helpCommand(),
		},
	}
}

// stampedVersion is set with `-ldflags "-X main.stampedVersion=X.Y.Z"` by the
// plugin's bin/f10 shim and the release workflow; empty in any other build.
var stampedVersion string

// the link-time stamp lands here, and every reader of the version asks buildinfo
func init() { buildinfo.Stamp = stampedVersion }
