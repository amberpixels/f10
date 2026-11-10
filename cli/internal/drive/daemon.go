package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
)

// WriteDaemonStatus prints what the daemon runs and watches.
func WriteDaemonStatus(ctx context.Context, w io.Writer, links *render.TaskLinks, rep daemon.Reply) {
	fmt.Fprintf(w, "pid      %d\nversion  %s\nsince    %s\ndrives   %d\nwatching %d\n", rep.PID, rep.Version,
		rep.Started.Format(time.DateTime), len(rep.Runs), len(rep.Watches))

	for _, r := range rep.Runs {
		fmt.Fprintf(w, "\n%s\n", DriveTitle(r))
		fmt.Fprintf(w, "  pid    %d, since %s\n  log    %s\n", r.PID, r.Since.Format(time.DateTime), r.Log)

		if r.Drive == 0 {
			continue
		}

		if rec, err := drives.Read(r.Root, r.Drive); err == nil {
			// each drive's ids resolve under its own project, since the
			// daemon runs drives for several
			project := ""
			if links.On {
				project = target.ProjectOf(ctx, r.Dir)
			}

			ids := make([]string, 0, len(rec.Tasks))
			for _, t := range rec.Tasks {
				ids = append(ids, t.ID)
			}

			links.Load(ctx, project, ids...)

			for _, t := range rec.Tasks {
				fmt.Fprintf(w, "  %s %s\n", render.PadRight(links.ID(project, t.ID), 6), TaskStanding(t, true))
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
}

func DriveTitle(r daemon.Run) string {
	project := projectName(r.Root)
	if r.Drive == 0 {
		return project + ": drive starting"
	}

	return fmt.Sprintf("%s: drive %d", project, r.Drive)
}

func projectName(root string) string {
	if project := filepath.Base(root); project != storage.DirName {
		return project
	}

	return filepath.Base(filepath.Dir(root))
}

func ThroughDaemon(ctx context.Context, w io.Writer, t *target.Target, argv []string, a driverec.DriveArgs) error {
	dir, ver := daemon.RunDir(), buildinfo.Version()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	req := daemon.Request{
		Op:   daemon.OpRun,
		Argv: argv,
		Dir:  cwd,
		Root: t.Layout.StorageRoot.Path(),
		Env:  daemon.DriveEnv(os.Environ(), t.Effective.DriveEnv),
	}

	// a rerun may attach to the drive still running, which reads its cap
	// from the record each tick
	if a.Parallel > 0 {
		setDriveParallel(w, t.Layout.StorageRoot.Path(), argv, a)
	}

	// a drive given answers starts its own process, so the answers reach it
	if len(a.Answers) == 0 {
		req.Resume = a.Resume
		if a.Resume == 0 {
			req.Key = drives.Key(RecordArgs(argv))
		}
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// a task the drive leaves unfinished is watched once the drive ends
	driverec.WatchProject(ctx, w, t)

	return FollowDrive(ctx, w, dir, ver, req, nil)
}

// setDriveParallel writes a given --parallel into the record --resume names
// or this command line picks up; with none, the new drive records it itself.
func setDriveParallel(w io.Writer, root string, argv []string, a driverec.DriveArgs) {
	var (
		rec *drives.Record
		err error
	)

	if a.Resume > 0 {
		rec, err = drives.Read(root, a.Resume)
	} else {
		rec, err = drives.Find(root, RecordArgs(argv))
	}

	if err != nil || rec == nil || rec.Parallel == a.Parallel {
		return
	}

	if _, err := drives.Update(root, rec.Number, func(r *drives.Record) { r.Parallel = a.Parallel }); err != nil {
		fmt.Fprintf(w, "  recording drive %d's --parallel: %v\n", rec.Number, err)
	}
}

// FollowDrive follows the drive until it exits, into the next daemon when
// an upgrade drained it. spawn starts a daemon; nil is this binary's.
func FollowDrive(
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

		exit, err := TailLog(ctx, w, rep.Log)
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

		resumed := exit.Reason == daemon.ReasonUpgrade || exit.Reason == daemon.ReasonResumed

		switch {
		case exit.Code == 0:
			return nil
		case exit.Code != daemon.ExitDrained && exit.Code != daemon.ExitResume:
			return cli.Exit("", exit.Code)
		case !resumed || exit.Drive == 0:
			fmt.Fprintf(
				w,
				"drive %d stopped with the f10 daemon: f10 drive --resume %d goes on\n",
				exit.Drive,
				exit.Drive,
			)

			return cli.Exit("", exit.Code)
		}

		// the drive runs again, after an upgrade in the new daemon: follow
		// it there, whatever that daemon's version
		follow = true
		req = daemon.ResumeRequest(req.Root, req.Dir, req.Env, exit.Drive)
	}
}

// TailLog copies a drive's log to w as it grows, until its exit file
// appears, and returns the exit; ctx ending stops only the copy.
func TailLog(ctx context.Context, w io.Writer, path string) (daemon.Exit, error) {
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
