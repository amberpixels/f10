// Package daemon is the f10 daemon command: it runs the per-user process that
// drives and watches tasks, and reports on it, stops it or shows its log.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/target"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:  "daemon",
		Usage: "the per-user process that runs drives and watches started tasks; f10 drive and f10 start start it when needed",
		Description: "Bare, it serves in the foreground: what f10 drive starts, detached. " +
			"It exits after ten minutes with no drive, no watched project and no call. A client of a newer release " +
			"replaces it: each drive stops at its next tick and the new daemon resumes it.",
		Action: runDaemon,
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "pid, version, the drives it runs and the projects it watches",
				Action: runDaemonStatus,
			},
			{
				Name:   "stop",
				Usage:  "stop the daemon; each drive stops at its next tick, and f10 drive --resume N goes on",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "now", Usage: "stop each drive at once, not at its next tick"}},
				Action: runDaemonStop,
			},
			{
				Name:      "logs",
				Usage:     "the daemon's log, or a drive's newest log in this project",
				ArgsUsage: "[<drive>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "keep printing until the drive exits"},
				},
				Action: runDaemonLogs,
			},
		},
	}
}

func runDaemon(ctx context.Context, cmd *cli.Command) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	s := &daemon.Server{
		Dir:     daemon.RunDir(),
		Version: buildinfo.Version(),
		Log:     log.New(cmd.ErrWriter, "", log.LstdFlags),
	}

	err := s.Serve(ctx)
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(cmd.Writer, err)

		return nil
	}

	return err
}

func runDaemonStatus(ctx context.Context, cmd *cli.Command) error {
	w := cmd.Writer

	rep, err := daemon.Call(ctx, daemon.RunDir(), daemon.Request{Op: daemon.OpStatus}, 0)
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
	_, err := daemon.Call(ctx, daemon.RunDir(), daemon.Request{Op: daemon.OpStop, Drain: !cmd.Bool("now")}, 0)
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Fprintln(cmd.Writer, "no f10 daemon is running")

		return nil
	}

	if err != nil {
		return err
	}

	if cmd.Bool("now") {
		fmt.Fprintln(cmd.Writer, "f10 daemon stopping: each drive stops now; f10 drive --resume <n> goes on with one")
	} else {
		fmt.Fprintln(cmd.Writer,
			"f10 daemon stopping: each drive stops at its next tick; f10 drive --resume <n> goes on with one")
	}

	return nil
}

func runDaemonLogs(ctx context.Context, cmd *cli.Command) error {
	dir := daemon.RunDir()
	path := daemon.LogPath(dir)

	if arg := cmd.Args().First(); arg != "" {
		n, err := drivectl.DriveNumber(arg)
		if err != nil {
			return err
		}

		t, err := target.For(ctx, cmd)
		if err != nil {
			return err
		}

		if path = daemon.FindLog(dir, t.Layout.StorageRoot.Path(), n); path == "" {
			return fmt.Errorf("no daemon log for drive %d in this project", n)
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
