package drive

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
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

// Command is the drive verb, which runs tasks through their flow and exits when every
// task is through or one needs a human. It is Go, not a Claude session:
// ordering, prompting and polling are deterministic, and a session polling
// an agent pays a round trip and a slice of context per check.
func Command() *cli.Command {
	return &cli.Command{
		Name:      "drive",
		Usage:     "run a list of tasks through a flow, each once its base has a commit, stopping once an agent asks and nothing else can move",
		ArgsUsage: drive.DriveUsage(),
		Description: "Each task runs the default flow, or the one --flow or --local names (f10 flow lists them): " +
			"plan when the flow has a plan entry and the task no plan file, then ship, " +
			"then finish unless the pace's finish is off. A rerun keeps the flow a task's branch records.\n" +
			"--pace NAME runs every task at that pace (f10 config lists them), recorded on each branch so a rerun keeps it; " +
			"a pace skips or tunes the flow's entries and never adds one.\n" +
			"A task's base is its branch's recorded --after, else its body's After: line; " +
			"every task whose base branch has a commit runs, its finish waits for the base's, " +
			"and a halt stops only its dependents.\n" +
			"--parallel N caps the tasks running a skill at once (default: half the CPU cores, 2 to 8), " +
			"and the agents open to twice N; the rest queue in the drive's order. A task on an ask, its base or " +
			"its finish frees its slot. A rerun or --resume keeps the drive's cap unless it passes a new one.\n" +
			"--agent starts every agent on PROFILE (kind:model:effort, or a part), laid over the agents map's default; " +
			"--model is the model-only form; <id>:<profile> starts one task's agent on its own, which wins.\n" +
			"Each drive keeps a record under the storage root, picked up by the same command or --resume N, " +
			"and holds a lease on each task: a task another drive holds is skipped, and f10 finish refuses it. " +
			"list shows the drives; cancel N stops one from prompting further.\n" +
			"The drive runs in the per-user f10 daemon, started when none runs: this command prints its progress, " +
			"Ctrl+C stops only that, and rerunning it follows the drive again. --foreground runs it in this process.\n" +
			"Exit 0: every task went through its flow. 4: an agent asks; rerun with --answer. " +
			"5: a task halted (a judge stop, a failure, an agent that stopped without reporting, its base halted).",
		// a range is `GH-12 -- GH-15`, and flag parsing would swallow the --
		SkipFlagParsing: true,
		Action:          runDrive,
		ShellComplete:   configcmd.CompleteFlow,
	}
}

func runDrive(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	a, err := driverec.ParseDriveArgs(argv)
	if err != nil {
		return err
	}

	if a.List || a.Cancel > 0 {
		t, err := target.For(ctx, cmd)
		if err != nil {
			return err
		}

		if a.List {
			return drive.DriveList(
				ctx,
				cmd.Writer,
				render.LinksFor(cmd.Writer, false),
				t.RefProject(ctx),
				t.Layout.StorageRoot.Path(),
				a.All,
				drive.RowFinishedFor(ctx, t),
			)
		}

		if err := drive.DriveCancel(cmd.Writer, t.Layout.StorageRoot.Path(), a.Cancel, time.Now()); err != nil {
			return err
		}

		drive.CloseDriveAsks(asks.Dir(), t.Layout.StorageRoot.Path(), a.Cancel, time.Now())

		return nil
	}

	// the daemon may drain this drive before it opens anything: the signal
	// waits in the channel until the loop exists to take it
	drain, unwatch := drive.WatchDrain(a.Fore)
	defer unwatch()

	if err := worktree.RequireAgents(); err != nil {
		return err
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	// the drive outlives this process unless asked to run in it
	if !a.Fore && daemon.Supported {
		return drive.ThroughDaemon(ctx, cmd.Writer, t, argv, a)
	}

	// a resume runs the record's own command line, with the answers and
	// --every given now
	if a.Resume > 0 {
		rec, err := drives.Read(t.Layout.StorageRoot.Path(), a.Resume)
		if err != nil {
			return err
		}

		given := a
		if a, err = driverec.ParseDriveArgs(rec.Args); err != nil {
			return fmt.Errorf("drive %d: %w", rec.Number, err)
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

	if fl.Finishes() && hostErr != nil {
		return fmt.Errorf("finish is on (the pace's finish), and finish merges through the host: %w", hostErr)
	}

	var pullOf func(context.Context, string) (forge.PR, error)
	if hostErr == nil {
		pullOf = h.PR
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
		AsksDir:         asks.Dir(),
		DefaultParallel: drive.DefaultParallel(),
		Pull:            pullOf,
		Driver:          drv,
		Tracker:         stack.TrackerFor(t),
		Title:           func(r ref.Ref) func(context.Context) (string, error) { return worktree.TitleLookup(t, r.Number) },
		Started:         start.StartedMove(t),
		Record:          drive.RecordAt,
		Finish: func(ctx context.Context, w io.Writer, req drive.DriveFinish) error {
			fin, err := drive.DriveFinishInput(t, main, h, req)
			if err != nil {
				return err
			}

			return finish.Finish(ctx, w, fin)
		},
		Now:    time.Now,
		Notify: asknotify.AskNotifier,
	}
	in.Sleep = in.Wake
	in.DaemonRun = os.Getenv(daemon.ResumeEnv) != ""

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

func WatchCommand() *cli.Command {
	return &cli.Command{
		Name: "watch",
		Usage: "what the f10 daemon watches in this project: tasks no drive owns, " +
			"their merged PRs, stalled agents and stopped runs",
		Description: "Bare, it lists each task the last pass saw and what it reported. " +
			"f10 start and f10 drive hand the project to the daemon, which runs a pass every few minutes; " +
			"watch: finish (the default), report or off in project.md says what a pass may do, " +
			"and watch_catchup: on lets it catch an open PR up with its target, " +
			"leaving conflicts and verify to the task's agent.",
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
		Warn:     func(line string) { fmt.Fprintln(cmd.Writer, line) },
		Catchup:  t.Effective.WatchCatchup,
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

	if b := worktree.ReachableAgents(main); b != nil {
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
