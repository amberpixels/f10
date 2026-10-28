package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
)

// The daemon noun runs drives outside any session: `f10 drive` starts it
// on demand, and it exits once nothing is left to drive.
func daemonCommand() *cli.Command {
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

func daemonVersion() string { return version(debug.ReadBuildInfo()) }

func runDaemon(ctx context.Context, cmd *cli.Command) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	s := &daemon.Server{
		Dir:     daemon.RunDir(),
		Version: daemonVersion(),
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

	fmt.Fprintf(w, "pid      %d\nversion  %s\nsince    %s\ndrives   %d\nwatching %d\n", rep.PID, rep.Version,
		rep.Started.Format(time.DateTime), len(rep.Runs), len(rep.Watches))

	for _, r := range rep.Runs {
		fmt.Fprintf(w, "\n%s\n", driveTitle(r))
		fmt.Fprintf(w, "  pid    %d, since %s\n  log    %s\n", r.PID, r.Since.Format(time.DateTime), r.Log)

		if r.Drive == 0 {
			continue
		}

		if rec, err := drives.Read(r.Root, r.Drive); err == nil {
			for _, t := range rec.Tasks {
				fmt.Fprintf(w, "  %-6s %s\n", t.ID, taskStanding(t, true))
			}
		}
	}

	for _, wt := range rep.Watches {
		fmt.Fprintf(w, "\n%s: watched\n", projectName(wt.Root))

		if wt.Last.IsZero() {
			fmt.Fprintln(w, "  pass   first one running")
		} else {
			fmt.Fprintf(w, "  pass   last %s, exit %d\n", wt.Last.Format(time.DateTime), wt.Exit)
		}

		fmt.Fprintf(w, "  log    %s\n", wt.Log)
	}

	return nil
}

// driveTitle names a run by its project and drive number.
func driveTitle(r daemon.Run) string {
	project := projectName(r.Root)
	if r.Drive == 0 {
		return project + ": drive starting"
	}

	return fmt.Sprintf("%s: drive %d", project, r.Drive)
}

// projectName is the checkout an in-repo storage root sits in, or an
// out-of-tree root's own name.
func projectName(root string) string {
	if project := filepath.Base(root); project != ".f10" {
		return project
	}

	return filepath.Base(filepath.Dir(root))
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
		n, err := driveNumber(arg)
		if err != nil {
			return err
		}

		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		if path = daemon.FindLog(dir, t.sharedRoot(), n); path == "" {
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

	_, err := tailLog(ctx, cmd.Writer, path)
	if errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}

// driveThroughDaemon hands the drive to this user's daemon, starting one
// when needed, and prints its log until it exits, with its exit code. A
// drive drained for an upgrade is followed into the new daemon.
func driveThroughDaemon(ctx context.Context, w io.Writer, t *target, argv []string, a driveArgs) error {
	dir, ver := daemon.RunDir(), daemonVersion()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	req := daemon.Request{
		Op:   daemon.OpRun,
		Argv: argv,
		Dir:  cwd,
		Root: t.sharedRoot(),
		Env:  driveEnv(os.Environ(), t.eff.DriveEnv),
	}

	// a drive given answers starts its own process, so the answers reach it
	if len(a.answers) == 0 {
		req.Resume = a.resume
		if a.resume == 0 {
			req.Key = strings.Join(recordArgs(argv), " ")
		}
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// a task the drive leaves unfinished is watched once the drive ends
	watchProject(ctx, w, t)

	return followDrive(ctx, w, dir, ver, req, nil)
}

// followDrive hands req to the daemon in dir and follows the drive until
// it exits, into the next daemon when an upgrade drained it. spawn starts a
// daemon; nil is this binary's.
func followDrive(
	ctx context.Context,
	w io.Writer,
	dir, ver string,
	req daemon.Request,
	spawn func(context.Context) error,
) error {
	follow := false

	for {
		e := &daemon.Ensurer{
			Dir:     dir,
			Version: ver,
			Follow:  follow,
			Spawn:   spawn,
			Draining: func(old string) {
				fmt.Fprintf(w, "replacing f10 daemon %s with %s: its drives stop at their next tick\n", old, ver)
			},
			Kept: func(running string) {
				fmt.Fprintf(w, "the f10 daemon runs %s, not this %s: f10 daemon stop replaces it\n", running, ver)
			},
		}

		if _, err := e.Ensure(ctx); err != nil {
			return err
		}

		rep, err := daemon.Call(ctx, dir, req, 0)
		if err != nil {
			return err
		}

		if rep.Attach {
			fmt.Fprintln(w, "following the drive the daemon already runs")
		}

		exit, err := tailLog(ctx, w, rep.Log)
		if errors.Is(err, context.Canceled) {
			if n := runningDrive(dir, rep.Log); n > 0 {
				fmt.Fprintf(w, "\ndrive %d keeps running in the f10 daemon: f10 daemon logs %d -f\n", n, n)
			} else {
				fmt.Fprintln(w, "\nthe drive keeps running in the f10 daemon: f10 daemon status")
			}

			return cli.Exit("", 130)
		}

		if err != nil {
			return err
		}

		switch {
		case exit.Code == 0:
			return nil
		case exit.Code != exitDrained:
			return cli.Exit("", exit.Code)
		case exit.Reason != daemon.ReasonUpgrade || exit.Drive == 0:
			fmt.Fprintf(
				w,
				"drive %d stopped with the f10 daemon: f10 drive --resume %d goes on\n",
				exit.Drive,
				exit.Drive,
			)

			return cli.Exit("", exit.Code)
		}

		// the new daemon resumed it: follow it there, whatever its version
		follow = true
		req = daemon.Request{
			Op:     daemon.OpRun,
			Argv:   []string{"--resume", strconv.Itoa(exit.Drive)},
			Dir:    req.Dir,
			Root:   req.Root,
			Env:    req.Env,
			Resume: exit.Drive,
		}
	}
}

// tailLog copies a drive's log to w as it grows, until its exit file
// appears, and returns the exit; ctx ending stops only the copy.
func tailLog(ctx context.Context, w io.Writer, path string) (daemon.Exit, error) {
	var f *os.File

	for i := 0; ; i++ {
		var err error
		if f, err = os.Open(path); err == nil {
			break
		} else if i >= 50 {
			return daemon.Exit{}, err
		}

		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			return daemon.Exit{}, err
		}
	}
	defer f.Close()

	for {
		if _, err := io.Copy(w, f); err != nil {
			return daemon.Exit{}, err
		}

		if exit, ok := daemon.ReadExit(path); ok {
			_, err := io.Copy(w, f)

			return exit, err
		}

		if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
			return daemon.Exit{}, err
		}
	}
}

// runningDrive is the number of the drive writing log, as the daemon
// knows it; 0 before the drive opened its record, or with no daemon.
func runningDrive(dir, logPath string) int {
	rep, err := daemon.Call(context.Background(), dir, daemon.Request{Op: daemon.OpStatus}, 0)
	if err != nil {
		return 0
	}

	for _, r := range rep.Runs {
		if r.Log == logPath {
			return r.Drive
		}
	}

	return 0
}

// driveEnvPrefixes and driveEnvKeys are the environment a drive passes to
// its commands: what git, gh, glab, Herdr, claude and f10 itself read.
var (
	driveEnvPrefixes = []string{"LC_", "XDG_", "GH_", "GLAB_", "HERDR_", "F10_", "CLAUDE_"}
	driveEnvKeys     = []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "TERM", "SSH_AUTH_SOCK",
		"GITHUB_TOKEN", "GITLAB_TOKEN",
	}
)

// driveEnv keeps the allowlisted entries of environ, and the keys the
// project's drive_env names.
func driveEnv(environ, extra []string) []string {
	var out []string

	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		if slices.Contains(driveEnvKeys, key) || slices.Contains(extra, key) ||
			slices.ContainsFunc(driveEnvPrefixes, func(p string) bool { return strings.HasPrefix(key, p) }) {
			out = append(out, kv)
		}
	}

	return out
}
