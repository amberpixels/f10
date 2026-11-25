// Package daemon is the f10 daemon command: it runs the per-user process that
// drives and watches tasks, and reports on it, stops it or shows its log.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/api"
	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/remote"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/webui"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:  "daemon",
		Usage: "the per-user process that runs batches and watches started tasks; f10 start starts it when needed",
		Description: "Bare, it serves in the foreground: what f10 start starts, detached. " +
			"It exits after ten minutes with no batch, no watched project and no call, unless f10 remote on " +
			"turned the tailnet listener on or it runs as the service f10 remote install set up. A client of a " +
			"newer release replaces it: each batch stops at its next tick and the new daemon resumes it.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "service",
				Usage: "run as the launchd or systemd service: never idle out, and stand by while another daemon runs",
			},
		},
		Action: runDaemon,
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "pid, version, the batches it runs and the projects it watches",
				Action: runDaemonStatus,
			},
			{
				Name:   "stop",
				Usage:  "stop the daemon; each batch stops at its next tick, and f10 resume goes on",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "now", Usage: "stop each batch at once, not at its next tick"}},
				Action: runDaemonStop,
			},
			{
				Name:      "logs",
				Usage:     "the daemon's log, or the newest log of the batch a task belongs to",
				ArgsUsage: "[<task-id>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "keep printing until the batch exits"},
				},
				Action: runDaemonLogs,
			},
		},
	}
}

func runDaemon(ctx context.Context, cmd *cli.Command) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	logger := log.New(cmd.ErrWriter, "", log.LstdFlags)

	s := &daemon.Server{
		Dir:     daemon.RunDir(),
		Version: buildinfo.Version(),
		Log:     logger,
		Handler: func(s *daemon.Server) http.Handler { return api.Handler(s) },
		Key:     drive.RecordKey,
		Service: cmd.Bool("service"),
	}

	if dir, err := remote.Dir(); err == nil {
		s.Remote = &remote.Listener{
			Dir:     dir,
			App:     webui.Handler(),
			Certs:   &remote.Certs{Dir: filepath.Join(dir, "tls")},
			Version: buildinfo.Version(),
		}
	} else {
		logger.Printf("no tailnet listener: %v", err)
	}

	// the handlers' Herdr calls run under the newest local session, since the
	// daemon outlives the client that spawned it
	agents.SessionEnv = s.SessionEnv

	err := s.Serve(ctx)
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(cmd.Writer, err)

		return nil
	}

	return err
}

func runDaemonStatus(ctx context.Context, cmd *cli.Command) error {
	w := cmd.Writer

	rep, err := daemon.GetStatus(ctx, daemon.RunDir(), buildinfo.Version())
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Fprintln(w, "no f10 daemon is running")

		return nil
	}

	if err != nil {
		return err
	}

	drive.WriteDaemonStatus(ctx, w, render.LinksFor(w, false), rep)

	return nil
}

func runDaemonStop(ctx context.Context, cmd *cli.Command) error {
	err := daemon.Stop(ctx, daemon.RunDir(), !cmd.Bool("now"), false)
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Fprintln(cmd.Writer, "no f10 daemon is running")

		return nil
	}

	if err != nil {
		return err
	}

	if cmd.Bool("now") {
		fmt.Fprintln(cmd.Writer, "f10 daemon stopping: each batch stops now; f10 resume <task-id> goes on with one")
	} else {
		fmt.Fprintln(cmd.Writer,
			"f10 daemon stopping: each batch stops at its next tick; f10 resume <task-id> goes on with one")
	}

	return nil
}

func runDaemonLogs(ctx context.Context, cmd *cli.Command) error {
	dir := daemon.RunDir()
	path := daemon.LogPath(dir)

	if arg := cmd.Args().First(); arg != "" {
		t, err := target.For(ctx, cmd)
		if err != nil {
			return err
		}

		// one-release alias: f10 daemon logs <n>, a batch by its number
		n, old := drivectl.OldNumber(arg)
		if old {
			fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("a batch number", "f10 daemon logs <task-id>"))
		} else if n, err = drive.LogBatch(ctx, t, arg); err != nil {
			return err
		}

		if path = daemon.FindLog(dir, t.Layout.StorageRoot.Path(), n); path == "" {
			return fmt.Errorf("no daemon log for batch %d in this project", n)
		}
	}

	if !cmd.Bool("follow") {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(cmd.Writer, f)

		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	_, err := drive.TailLog(ctx, cmd.Writer, path)
	if errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}
