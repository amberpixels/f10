package drive

import (
	"cmp"
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
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// WriteDaemonStatus prints what the daemon runs and watches.
func WriteDaemonStatus(ctx context.Context, w io.Writer, links *render.TaskLinks, rep daemon.Status) {
	fmt.Fprintf(w, "pid      %d\nversion  %s\nsince    %s\nbatches  %d\nwatching %d\n", rep.PID, rep.Version,
		rep.Started.Format(time.DateTime), len(rep.Runs), len(rep.Watches))

	switch r := rep.Remote; {
	case r.Addr != "":
		fmt.Fprintf(w, "remote   %s\n", remoteLine(r.URL, r.Addr, r.HTTPSReason))
	case r.Enabled:
		fmt.Fprintf(w, "remote   not listening: %s\n", r.Reason)
	}

	for _, r := range rep.Runs {
		fmt.Fprintf(w, "\n%s\n", driveTitle(r))
		fmt.Fprintf(w, "  pid    %d, since %s\n  log    %s\n", r.PID, r.Since.Format(time.DateTime), r.Log)

		if r.Drive == 0 {
			continue
		}

		if rec, err := drivestore.Read(r.Root, r.Drive); err == nil {
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
				standing := taskStanding(t, true, rec.Tasks, drivectl.JoinPlanned(drivectl.PlanOnly(rec), r.Root))
				fmt.Fprintf(w, "  %s %s\n", render.PadRight(links.ID(project, t.ID), 6), standing)
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

func driveTitle(r daemon.Run) string {
	project := projectName(r.Root)
	if r.Drive == 0 {
		return project + ": batch starting"
	}

	return fmt.Sprintf("%s: batch %d", project, r.Drive)
}

func projectName(root string) string {
	if project := filepath.Base(root); project != storage.DirName {
		return project
	}

	return filepath.Base(filepath.Dir(root))
}

// RecordKey is the key the daemon attaches a drive's arguments by: its
// record's command line, "" for a drive given answers or a resume, which
// starts or names its own.
func RecordKey(argv []string) string {
	a, err := drivectl.ParseDriveArgs(argv)
	if err != nil || len(a.Answers) > 0 || a.Resume > 0 {
		return ""
	}

	return drivestore.Key(recordArgs(argv))
}

// ThroughDaemon runs the drive in the daemon. With follow it prints the
// drive's progress until the drive ends, and Ctrl+C stops only the
// following. Without follow it returns once the daemon has the drive. A
// start inside a discussion's worktree needs that, because the drive
// prompts that same agent, which must end this turn first.
func ThroughDaemon(
	ctx context.Context, w io.Writer, t *target.Target, argv []string, a drivectl.DriveArgs, follow bool,
) error {
	dir, ver := daemon.RunDir(), buildinfo.Version()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	req := daemon.DriveCall{
		Args: argv,
		Dir:  cwd,
		Env:  daemon.DriveEnv(os.Environ(), t.Effective.BatchEnv),
	}

	if !follow {
		e := &daemon.Ensurer{Dir: dir, Version: ver}
		if _, err := e.Ensure(ctx); err != nil {
			return err
		}

		drivectl.WatchProject(ctx, w, t)

		rep, err := daemon.RunDrive(ctx, dir, req)
		if err != nil {
			return err
		}

		n := rep.Drive
		if n == 0 {
			n = runningDrive(dir, rep.Log)
		}

		fmt.Fprintf(w, "batch %d runs in the f10 daemon; this agent gets its first prompt once this turn ends: "+
			"%s follows it\n", n, startedLogsHint(t.Layout.StorageRoot.Path(), n, a))

		return nil
	}

	req.Resume = TuneAttach(w, t.Layout.StorageRoot.Path(), argv, a)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// a task the drive leaves unfinished is watched once the drive ends
	drivectl.WatchProject(ctx, w, t)

	return followDrive(ctx, w, dir, ver, t.Layout.StorageRoot.Path(), req, nil)
}

// TuneAttach prepares a command line the daemon may attach to a batch still
// running, which then never reads the line. A given --parallel is written
// into the record, which a running batch reads each tick. It returns the
// record to attach by: none for a batch given answers, which starts a
// process of its own so the answers reach it, and the daemon keys the rest
// by RecordKey. The CLI and the API both hand a batch over through it.
func TuneAttach(w io.Writer, root string, argv []string, a drivectl.DriveArgs) int {
	if a.Parallel > 0 {
		setDriveParallel(w, root, argv, a)
	}

	if len(a.Answers) > 0 {
		return 0
	}

	return a.Resume
}

// setDriveParallel writes a given --parallel into the record --resume names
// or this command line picks up; with none, the new drive records it itself.
func setDriveParallel(w io.Writer, root string, argv []string, a drivectl.DriveArgs) {
	var (
		rec *drivestore.Record
		err error
	)

	if a.Resume > 0 {
		rec, err = drivestore.Read(root, a.Resume)
	} else {
		rec, err = drivestore.Find(root, recordArgs(argv))
	}

	if err != nil || rec == nil || rec.Parallel == a.Parallel {
		return
	}

	if _, err := drivestore.Update(
		root,
		rec.Number,
		func(r *drivestore.Record) { r.Parallel = a.Parallel },
	); err != nil {
		fmt.Fprintf(w, "  recording batch %d's --parallel: %v\n", rec.Number, err)
	}
}

// followDrive follows the drive until it exits, into the next daemon when
// an upgrade drained it, and through its asks into the run an answer
// resumes. root is the storage root the daemon keys the drive's logs by.
// spawn starts a daemon, and nil is this binary's.
func followDrive(
	ctx context.Context,
	w io.Writer,
	dir, ver, root string,
	req daemon.DriveCall,
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
				fmt.Fprintf(w, "replacing f10 daemon %s with %s: its batches stop at their next tick\n", old, ver)
			},
			Kept: func(running string) {
				fmt.Fprintf(w, "the f10 daemon runs %s, not this %s: f10 daemon stop replaces it\n", running, ver)
			},
		}

		if _, err := e.Ensure(ctx); err != nil {
			return err
		}

		rep, err := daemon.RunDrive(ctx, dir, req)
		if err != nil {
			return err
		}

		if rep.Attach {
			fmt.Fprintln(w, "following the batch the daemon already runs")
		}

		exit, err := followAsks(ctx, w, dir, root, rep.Log)
		if errors.Is(err, context.Canceled) {
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
			fmt.Fprintf(w, "batch %d stopped with the f10 daemon: %s goes on\n",
				exit.Drive, resumeHint(root, exit.Drive))

			return cli.Exit("", exit.Code)
		}

		// the drive runs again, after an upgrade in the new daemon: follow
		// it there, whatever that daemon's version
		follow = true
		req = daemon.ResumeCall(req.Dir, req.Env, exit.Drive)
	}
}

// askPoll is how often a drive stopped on its asks is checked for the run
// an answer resumes. Tests shorten it.
var askPoll = 2 * time.Second

// driveGone reports a drive no answer can resume: f10 stop cancelled its
// record, or the record is gone. Tests swap it.
var driveGone = func(root string, n int) bool {
	rec, err := drivestore.Read(root, n)

	return err != nil || rec.Cancelled != nil
}

// followAsks tails log. When the drive stops on its asks, it also tails the
// run an answer resumes, because a rerun would stop on the same asks again.
// It returns the last run's exit.
func followAsks(ctx context.Context, w io.Writer, dir, root, log string) (daemon.Exit, error) {
	for {
		exit, err := TailLog(ctx, w, log)
		if errors.Is(err, context.Canceled) {
			keepsRunning(w, dir, root, log)
		}

		if err != nil || exit.Code != ui.ExitAsk || exit.Drive == 0 {
			return exit, err
		}

		fmt.Fprintf(w, "\nbatch %d waits on its asks: answer them in the asking agent's pane, "+
			"with f10 inbox or /f10:inbox; this follows the batch once it resumes\n", exit.Drive)

		next, err := awaitResume(ctx, dir, root, exit.Drive, log)
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(w, "\nbatch %d still waits on its asks: f10 status --batches shows it\n", exit.Drive)
		}

		if err != nil {
			return exit, err
		}

		if next == "" {
			fmt.Fprintf(w, "batch %d was stopped while it waited on its asks\n", exit.Drive)

			return exit, nil
		}

		log = next
	}
}

// awaitResume waits for drive n to run again after last, and returns the
// new run's log, or "" once the drive is gone. It reads the daemon's log index,
// not its live runs, so a resumed run that ends between two looks is
// followed all the same.
func awaitResume(ctx context.Context, dir, root string, n int, last string) (string, error) {
	for {
		if log := daemon.FindLog(dir, root, n); log != "" && log != last {
			return log, nil
		}

		if driveGone(root, n) {
			return "", nil
		}

		if err := sleepCtx(ctx, askPoll); err != nil {
			return "", err
		}
	}
}

func keepsRunning(w io.Writer, dir, root, log string) {
	if n := runningDrive(dir, log); n > 0 {
		fmt.Fprintf(w, "\nbatch %d keeps running in the f10 daemon: %s\n", n, logsHint(root, n))
	} else {
		fmt.Fprintln(w, "\nthe batch keeps running in the f10 daemon: f10 daemon status")
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
	rep, err := daemon.GetStatus(context.Background(), dir, buildinfo.Version())
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

// remoteLine is the web client's address, and why it is plain HTTP when it is.
func remoteLine(url, addr, httpsWhy string) string {
	line := cmp.Or(url, addr)
	if httpsWhy != "" {
		line += " (no HTTPS: " + httpsWhy + ")"
	}

	return line
}
