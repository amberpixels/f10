// Package drive is the f10 start, f10 resume and f10 watch commands, and
// serves a start's proposal.
package drive

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/driveplan"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Command is the hidden drive verb, kept for one release: `drive plan` is
// now `f10 start <ids> --propose`, and runs as it.
func Command() *cli.Command {
	return &cli.Command{
		Name:            "drive",
		Hidden:          true,
		Usage:           "plumbing: the old spelling of f10 start <ids> --propose",
		SkipFlagParsing: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			argv := cmd.Args().Slice()

			switch {
			case len(argv) == 0 || slices.Contains(argv, "--help") || slices.Contains(argv, "-h"):
				return cli.ShowSubcommandHelp(cmd)
			case argv[0] != "plan":
				return errors.New("this command is gone: f10 start runs tasks, " +
					"f10 status --batches lists batches, f10 stop stops one, f10 resume goes on with one")
			}

			fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("f10 drive plan", "f10 start <ids> --propose"))

			a, err := drivectl.ParsePropose(argv[1:])
			if err != nil {
				return err
			}

			return runPropose(ctx, cmd, a)
		},
	}
}

// ResumeCommand is the resume verb: it goes on with a stopped batch,
// wherever each task stopped. It is no flag of start, because a resume may
// only have a finish left to run.
func ResumeCommand() *cli.Command {
	return &cli.Command{
		Name: "resume",
		Usage: "go on with a stopped batch, wherever each task stopped, the finish included: " +
			"named by any of its tasks, or the one stopped batch in this project",
		ArgsUsage: "[<task-id>] " + drive.ResumeUsage() + " [--foreground]",
		Description: "A batch is the tasks one f10 start started, kept as a record. " +
			"Any task id of the batch names it, a bundle's rider included. " +
			"With no task id, the one stopped batch in the project resumes, and several are listed. " +
			"--answer answers the batch's open asks, --every and --parallel change how it runs, " +
			"and --foreground runs it in this process. A batch still running is followed.",
		SkipFlagParsing: true,
		Action:          runResume,
	}
}

func runResume(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	ra, err := drivectl.ParseResume(argv)
	if err != nil {
		return err
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	n, err := resumeNumber(ctx, cmd, t, ra.Token)
	if err != nil {
		return err
	}

	return Run(ctx, cmd, append([]string{"--resume", strconv.Itoa(n)}, ra.Flags...))
}

// resumeNumber is the record a resume names: by a task id, by the one
// stopped batch, or by its number for one more release.
func resumeNumber(ctx context.Context, cmd *cli.Command, t *target.Target, token string) (int, error) {
	if n, ok := drivectl.OldNumber(token); ok {
		lead := "<task-id>"
		if rec, err := drivestore.Read(t.Layout.StorageRoot.Path(), n); err == nil && rec.Lead() != "" {
			lead = rec.Lead()
		}

		fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("a batch number", "f10 resume "+lead))

		return n, nil
	}

	rec, err := drive.Resumable(ctx, t, token)

	if several, ok := errors.AsType[drive.SeveralStoppedError](err); ok {
		for _, l := range several.Stopped {
			ids := make([]string, 0, len(l.Tasks))
			for _, task := range l.Tasks {
				ids = append(ids, task.ID)
			}

			fmt.Fprintf(cmd.Writer, "f10 resume %s   batch %d: %s\n",
				l.Name(t.Layout.StorageRoot.Path()), l.Number, strings.Join(ids, " "))
		}

		return 0, cli.Exit(err.Error(), 1)
	}

	if err != nil {
		return 0, err
	}

	return rec.Number, nil
}

// Run is f10 start for tasks run through their flow: the drive, in the
// daemon unless --foreground, else in this process.
func Run(ctx context.Context, cmd *cli.Command, argv []string) error {
	a, err := drivectl.ParseDriveArgs(argv)
	if err != nil {
		return err
	}

	return runDrive(ctx, cmd, argv, a)
}

func runDrive(ctx context.Context, cmd *cli.Command, argv []string, a drivectl.DriveArgs) error {
	// the daemon may drain this drive before it opens anything: the signal
	// waits in the channel until the loop exists to take it
	drain, unwatch := drive.WatchDrain(a.Fore)
	defer unwatch()

	if err := agents.RequireAgents(); err != nil {
		return err
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	if handed, err := handOver(ctx, cmd.Writer, t, argv, a); handed || err != nil {
		return err
	}

	// the drive outlives this process unless asked to run in it
	if !a.Fore && daemon.Supported {
		return drive.ThroughDaemon(ctx, cmd.Writer, t, argv, a, true)
	}

	// a resume runs the record's own command line, with the answers and
	// --every given now
	if a.Resume > 0 {
		rec, err := drivestore.Read(t.Layout.StorageRoot.Path(), a.Resume)
		if err != nil {
			return err
		}

		given := a
		if a, err = drivectl.ParseDriveArgs(rec.Args); err != nil {
			return fmt.Errorf("batch %d: %w", rec.Number, err)
		}

		a.Resume, a.Answers, a.Every, a.Fore = given.Resume, given.Answers, given.Every, given.Fore
		a.Parallel = given.Parallel
		argv = rec.Args
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: a.Pace})
	if err != nil {
		return err
	}

	fl, err := run.Flow(a.Flow)
	if err != nil {
		return err
	}

	// the drive lays --agent and each task's suffix on per task
	layers := runplan.AgentMapOf(t.Effective)

	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	drv := t.Driver()
	h, hostErr := hostops.HostOf(t)

	// --plan stops at plan, so a finish in the flow never runs and needs no host
	if fl.Finishes && !a.PlanOnly && hostErr != nil {
		return fmt.Errorf("the flow ends in finish, and finish merges through the host: %w", hostErr)
	}

	var (
		pullOf   func(context.Context, string) (forge.PR, error)
		checksOf func(context.Context, string) (forge.HeadChecks, string, error)
		landed   start.LandedLookup
	)

	if hostErr == nil {
		pullOf, checksOf, landed = h.PR, h.OpenChecks, start.LandedVia(h.Host)
	}

	in := &drive.DriveInput{
		Main:    main,
		Root:    t.Layout.StorageRoot.Path(),
		Args:    a,
		Argv:    argv,
		Flow:    fl,
		Resolve: run.Flow,
		Paced: func(fl runplan.ShipFlow, p string) (runplan.ShipFlow, error) {
			if fl.ByArea {
				return run.FlowAt(p, "")
			}

			return run.FlowAt(p, fl.Recorded())
		},
		Storage:         t.Layout.StorageRoot,
		Settings:        runplan.ClaudeSettingsPath(),
		Layers:          layers,
		StateDir:        state.Dir(),
		Session:         state.Session(),
		TTL:             state.TTL(),
		InboxDir:        asks.Dir(),
		DefaultParallel: drive.DefaultParallel(),
		Pull:            pullOf,
		Checks:          checksOf,
		Driver:          drv,
		Tracker:         stack.TrackerFor(t),
		Title:           func(r ref.Ref) func(context.Context) (string, error) { return worktree.TitleLookup(t, r.Number) },
		Started:         start.StartedMove(t),
		Landed:          landed,
		Record:          drive.RecordAt,
		Finish: func(ctx context.Context, w io.Writer, req drive.DriveFinish) error {
			fin, err := drive.DriveFinishInput(t, main, h, req)
			if err != nil {
				return err
			}

			return finish.Finish(ctx, w, fin)
		},
		Now:    time.Now,
		Notify: asknotify.Notify,
	}
	in.Sleep = in.Wake
	in.DaemonRun = daemon.EnvOf(daemon.ResumeEnv) != ""

	spec, why := drive.VerifySpecOf(t.Effective)
	in.Catchup = stack.NewCatchupEngine(stack.CatchupPolicy(t.Effective), spec, why, t.Effective.Traces, main)

	if drain != nil {
		go func() {
			for range drain {
				in.Draining.Store(true)
			}
		}()
	}

	return drive.Drive(ctx, cmd.Writer, in)
}

// runPropose prints a proposal and changes nothing. It needs no agents, no
// daemon and no record, so a hook or a status line may call it.
func runPropose(ctx context.Context, cmd *cli.Command, a drivectl.DriveArgs) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	// the flow the start would run: a join needs one that finishes
	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: a.Pace})
	if err != nil {
		return err
	}

	fl, err := run.Flow(a.Flow)
	if err != nil {
		return err
	}

	res, err := drive.Plan(ctx, drive.PlanInput{
		Storage:         t.Layout.StorageRoot,
		Tracker:         stack.TrackerFor(t),
		FlowByArea:      t.Effective.FlowByArea,
		MaxLines:        t.Effective.BundleMaxLines,
		DefaultParallel: drive.DefaultParallel(),
		Joins:           fl.Finishes && !fl.Mixed,
		Main:            cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
	}, a)
	if err != nil {
		return err
	}

	w, pa := cmd.Writer, a.Plan

	switch {
	case pa.JSON:
		return writePlanJSON(w, res)
	case pa.Hint:
		if line := res.Plan.Hint(); line != "" {
			_, err := fmt.Fprintln(w, line)

			return err
		}

		return nil
	}

	return res.Plan.Write(w, res.Notes, res.Extra...)
}

func writePlanJSON(w io.Writer, res drive.PlanResult) error {
	out := struct {
		Command string           `json:"command"`
		Plan    *driveplan.Plan  `json:"proposal,omitempty"`
		Tasks   []driveplan.Task `json:"tasks,omitempty"`
		Notes   []string         `json:"notes,omitempty"`
		Saving  string           `json:"saving,omitempty"`
	}{Notes: res.Notes, Tasks: res.Tasks, Plan: &res.Plan, Command: res.Plan.Command(res.Extra...), Saving: res.Plan.Saving()}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("writing the proposal: %w", err)
	}

	return nil
}

func WatchCommand() *cli.Command {
	return &cli.Command{
		Name: "watch",
		Usage: "what the f10 daemon watches in this project: tasks no batch owns, " +
			"their merged PRs, stalled agents and stopped runs",
		Description: "Bare, it lists each task the last pass saw and what it reported. " +
			"f10 start hands the project to the daemon, which runs a pass every few minutes; " +
			"watch: finish (the default), report or off in project.md says what a pass may do, " +
			"and watch_catchup: on lets it catch an open PR up with its target, " +
			"leaving conflicts and verify to the task's agent. A red PR is reported once per head, " +
			"and watch_ci: on sends it to the task's agent to triage.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "pass", Hidden: true, Usage: "run one pass over this project, as the daemon does"},
		},
		Action: runWatch,
		Commands: []*cli.Command{
			{
				Name:      "off",
				Usage:     "hand a task back to manual control: no pass reports on it or finishes it",
				ArgsUsage: "<task-id>",
				Action:    func(ctx context.Context, cmd *cli.Command) error { return runWatchToggle(ctx, cmd, false) },
			},
			{
				Name:      "on",
				Usage:     "watch a task again after f10 watch off",
				ArgsUsage: "<task-id>",
				Action:    func(ctx context.Context, cmd *cli.Command) error { return runWatchToggle(ctx, cmd, true) },
			},
		},
	}
}

func runWatch(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("pass") {
		return runWatchPass(ctx, cmd, t)
	}

	store, err := drive.LoadWatchStore(t.Layout.StorageRoot.Path())
	if err != nil {
		return err
	}

	w := cmd.Writer
	fmt.Fprintf(w, "watch    %s\n", t.Effective.Watch)
	fmt.Fprintf(w, "catchup  %s\n", drive.OnOff(t.Effective.WatchCatchup))
	fmt.Fprintf(w, "ci       %s\n", drive.OnOff(t.Effective.WatchCI))

	if len(store) == 0 {
		fmt.Fprintln(w, "nothing reported yet")

		return nil
	}

	ids := make([]string, 0, len(store))
	for id := range store {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	links, project := render.LinksFor(w, false), ""
	if links.On {
		project = t.RefProject(ctx)
	}

	links.Load(ctx, project, ids...)

	for _, id := range ids {
		r := store[id]
		if r.Kind == "" {
			continue // only a catchup record, nothing told yet
		}

		fmt.Fprintf(
			w,
			"\n%s  %s, %s\n  %s\n",
			links.ID(project, id),
			r.Kind,
			r.At.Local().Format(time.DateTime),
			r.Text,
		)

		if r.Ask != "" {
			fmt.Fprintf(w, "  asks: %s\n", r.Ask)
		}

		if r.Next != "" {
			fmt.Fprintf(w, "  next: %s\n", r.Next)
		}
	}

	return nil
}

func runWatchToggle(ctx context.Context, cmd *cli.Command, on bool) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.Reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	r = r.Head()
	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)

	name, _, err := stack.TaskCheckout(ctx, main, t.Driver(), r, "")
	if err != nil {
		return err
	}

	if name == "" {
		return fmt.Errorf("%s has no branch here: nothing to watch", r.ID)
	}

	if on {
		_ = branchmeta.Unset(ctx, main, name, branchmeta.Watch) // absent is fine

		fmt.Fprintf(cmd.Writer, "%s is watched again\n", render.TaskFact(ctx, cmd.Writer, main, r.ID).Value)

		return nil
	}

	if err := branchmeta.Set(ctx, main, name, branchmeta.Watch, "false"); err != nil {
		return err
	}

	fmt.Fprintf(
		cmd.Writer,
		"%s is yours: no pass reports on it or finishes it (f10 watch on %s undoes it)\n",
		r.ID,
		r.ID,
	)

	return nil
}

func runWatchPass(ctx context.Context, cmd *cli.Command, t *target.Target) error {
	if t.Effective.Watch == "off" {
		return cli.Exit("watch: off in this project", daemon.ExitNothingWatched)
	}

	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	root := t.Layout.StorageRoot.Path()

	in := drive.WatchInput{
		Main:     main,
		Root:     root,
		Mode:     t.Effective.Watch,
		Jan:      finish.JanitorFor(t),
		StateDir: state.Dir(),
		TTL:      state.TTL(),
		Now:      time.Now,
		Out:      drive.LineReporter{Out: cmd.Writer},
		Record:   drive.LedgerReports(ctx, main),
		Warn:     func(line string) { fmt.Fprintln(cmd.Writer, line) },
		Catchup:  t.Effective.WatchCatchup,
		CI:       t.Effective.WatchCI,
		Storage:  t.Layout.StorageRoot,
		// a pass stays short, so verify is always the agent's turn
		Engine: stack.NewCatchupEngine(
			stack.CatchupPolicy(t.Effective),
			stack.VerifySpec{},
			"the watcher leaves verify to the agent",
			t.Effective.Traces,
			main,
		),
	}

	if h, err := hostops.HostOf(t); err == nil {
		in.Checks = h.HeadChecks
	}

	if b := agents.ReachableAgents(main); b != nil {
		in.Agents = b
		in.Prompt = func(ctx context.Context, task drive.WatchedTask, text string) error {
			note, err := launch.PromptChecked(ctx, task.Path, task.ID, "", text)
			if note != "" {
				in.Warn(task.ID + ": " + note)
			}

			return err
		}
	}

	if in.Mode == "finish" && in.Jan.Host != nil {
		in.Finish = func(ctx context.Context, task drive.WatchedTask) error {
			return drive.WatcherFinish(ctx, cmd.Writer, t, main, root, task)
		}
	}

	watching, err := drive.WatchPass(ctx, in)
	if err != nil {
		return err
	}

	// the janitor deletes branches: an act, which only watch: finish allows
	if in.Mode == "finish" {
		finish.SweepIfDue(ctx, cmd.Writer, filepath.Join(root, finish.JanitorStamp), time.Now(),
			func(ctx context.Context) ([]string, error) { return finish.Janitor(ctx, finish.JanitorFor(t)) })
	}

	if !watching {
		return cli.Exit("nothing left to watch", daemon.ExitNothingWatched)
	}

	return nil
}

// handOver gives a discussion's worktree to the start's first group and
// hands the drive to the daemon. It never follows the drive, because the
// drive prompts this worktree's agent, whose turn runs this command.
func handOver(ctx context.Context, w io.Writer, t *target.Target, argv []string, a drivectl.DriveArgs) (bool, error) {
	// the daemon's own drive process runs in this worktree after the hand-off
	if a.Resume > 0 || len(a.Tasks) == 0 || daemon.EnvOf(daemon.ResumeEnv) != "" {
		return false, nil
	}

	branch, err := start.Discussing(ctx, t)
	if err != nil || branch == "" {
		return false, err
	}

	if a.Fore || !daemon.Supported {
		return true, errors.New("this worktree holds a discussion, and the daemon prompts its agent once this turn " +
			"ends: run f10 start through the daemon, without --foreground")
	}

	// every id resolves before anything is renamed
	for _, token := range a.Tasks {
		id, _ := ref.SplitSuffix(ref.TypedLead(token))
		if _, err := t.Reference(ctx, id); err != nil {
			return true, fmt.Errorf("%s: %w", token, err)
		}
	}

	h, err := start.PrepareHandOver(ctx, t, branch, a.Tasks[0])
	if err != nil {
		return true, err
	}

	notes, undo, err := h.Run(ctx)
	if err != nil {
		return true, err
	}

	if err := drive.ThroughDaemon(ctx, w, t, argv, a, false); err != nil {
		return true, errors.Join(err, undo(ctx))
	}

	for _, n := range notes {
		fmt.Fprintln(w, n)
	}

	return true, nil
}
