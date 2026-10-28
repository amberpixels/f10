package main

import (
	"bufio"
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
		Usage: "the per-user process that runs drives; f10 drive starts it when needed",
		Description: "Bare, it serves in the foreground: what f10 drive starts, detached. " +
			"It exits after ten minutes with no drive and no call, and a client of a newer f10 " +
			"replaces it, each drive resumed from its record.",
		Action: runDaemon,
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "pid, version and the drives it runs",
				Action: runDaemonStatus,
			},
			{
				Name:   "stop",
				Usage:  "stop the daemon; each drive stops at its next tick and resumes on its next run",
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

	fmt.Fprintf(w, "pid      %d\nversion  %s\nsince    %s\ndrives   %d\n", rep.PID, rep.Version,
		rep.Started.Format(time.DateTime), len(rep.Runs))

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

	return nil
}

// driveTitle names a run by its project and drive number.
func driveTitle(r daemon.Run) string {
	project := filepath.Base(filepath.Dir(r.Root))
	if r.Drive == 0 {
		return project + ": drive starting"
	}

	return fmt.Sprintf("%s: drive %d", project, r.Drive)
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
		fmt.Fprintln(cmd.Writer, "f10 daemon stopping: each drive stops now; rerun it to resume")
	} else {
		fmt.Fprintln(cmd.Writer,
			"f10 daemon stopping: each drive stops at its next tick; the next f10 drive resumes them")
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

	for {
		e := &daemon.Ensurer{Dir: dir, Version: ver, Draining: func(old string) {
			fmt.Fprintf(w, "replacing f10 daemon %s with %s: its drives stop at their next tick\n", old, ver)
		}}

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

		code, err := tailLog(ctx, w, rep.Log)
		if errors.Is(err, context.Canceled) {
			n := logDrive(rep.Log)
			fmt.Fprintf(w, "\ndrive %d keeps running in the f10 daemon: f10 daemon logs %d -f\n", n, n)

			return cli.Exit("", 130)
		}

		if err != nil {
			return err
		}

		if code != exitDrained {
			if code != 0 {
				return cli.Exit("", code)
			}

			return nil
		}

		n := logDrive(rep.Log)
		if n == 0 {
			return cli.Exit("", code)
		}

		req = daemon.Request{
			Op:     daemon.OpRun,
			Argv:   []string{"--resume", strconv.Itoa(n)},
			Dir:    cwd,
			Root:   req.Root,
			Env:    req.Env,
			Resume: n,
		}
	}
}

// tailLog copies a drive's log to w as it grows, until its exit file
// appears, and returns the exit code; ctx ending stops only the copy.
func tailLog(ctx context.Context, w io.Writer, path string) (int, error) {
	var f *os.File

	for i := 0; ; i++ {
		var err error
		if f, err = os.Open(path); err == nil {
			break
		} else if i >= 50 {
			return 0, err
		}

		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
	defer f.Close()

	for {
		if _, err := io.Copy(w, f); err != nil {
			return 0, err
		}

		if code, ok := daemon.ReadExit(path); ok {
			_, err := io.Copy(w, f)

			return code, err
		}

		if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

// logDrive is the number a drive's log names on its `drive <n>` line.
func logDrive(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan() && i < 100; i++ {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "drive "); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				return n
			}
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
