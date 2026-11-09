package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The drive verb runs tasks through their flow and exits when every
// task is through or one needs a human. It is Go, not a Claude session:
// ordering, prompting and polling are deterministic, and a session polling
// an agent pays a round trip and a slice of context per check.
func driveCommand() *cli.Command {
	return &cli.Command{
		Name:  "drive",
		Usage: "run a list of tasks through a flow, each once its base has a commit, stopping once an agent asks and nothing else can move",
		ArgsUsage: "<id>[:profile]... | <id> -- <id>  [--flow NAME | --local] [--pace NAME] " +
			"[--agent PROFILE | --model MODEL] [--parallel N] [--answer [ID=]TEXT ...] [--every DURATION] [--foreground]\n" +
			"   f10 drive --resume N [--parallel N] [--answer [ID=]TEXT ...] | list [--all] | cancel N",
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
		ShellComplete:   completeFlow,
	}
}

const (
	// defaultEvery is the fallback look: a run-state write wakes a drive
	// sooner (drivewait.go)
	defaultEvery = 30 * time.Second
	// promptSettle is how old a prompt mark must be before an idle agent
	// beside it reads as a turn that ended, not one Herdr has yet to show
	promptSettle = 5 * time.Second
	// startWindow is how long a prompted agent has to show it took the
	// prompt: Herdr reporting it working, or its run reporting.
	startWindow = 2 * time.Minute
	// stallGrace absorbs the Stop hook, which promotes a finished phase
	// asynchronously after the agent already reads idle. A long verify left
	// in the background does not count against it (backgroundPending).
	stallGrace = time.Minute
	// maxSpan caps a range, since every id in it costs a tracker call.
	maxSpan = 50
)

func runDrive(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	a, err := parseDriveArgs(argv)
	if err != nil {
		return err
	}

	if a.list || a.cancel > 0 {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		if a.list {
			return driveList(
				ctx,
				cmd.Writer,
				linksFor(cmd.Writer, false),
				t.refProject(ctx),
				t.lay.StorageRoot.Path(),
				a.all,
				rowFinishedFor(ctx, t),
			)
		}

		if err := driveCancel(cmd.Writer, t.lay.StorageRoot.Path(), a.cancel, time.Now()); err != nil {
			return err
		}

		closeDriveAsks(asks.Dir(), t.lay.StorageRoot.Path(), a.cancel, time.Now())

		return nil
	}

	// the daemon may drain this drive before it opens anything: the signal
	// waits in the channel until the loop exists to take it
	drain, unwatch := watchDrain(a.fore)
	defer unwatch()

	if err := agents("").Available(); err != nil {
		return err
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	// the drive outlives this process unless asked to run in it
	if !a.fore && daemon.Supported {
		return driveThroughDaemon(ctx, cmd.Writer, t, argv, a)
	}

	// a resume runs the record's own command line, with the answers and
	// --every given now
	if a.resume > 0 {
		rec, err := drives.Read(t.lay.StorageRoot.Path(), a.resume)
		if err != nil {
			return err
		}

		given := a
		if a, err = parseDriveArgs(rec.Args); err != nil {
			return fmt.Errorf("drive %d: %w", rec.Number, err)
		}

		a.resume, a.answers, a.every, a.fore = given.resume, given.answers, given.every, given.fore
		a.parallel = given.parallel
		argv = rec.Args
	}

	paces, err := paceSet(t.eff)
	if err != nil {
		return err
	}

	pace, err := activePace(ctx, paces, a.pace, "", "")
	if err != nil {
		return err
	}

	fl, err := resolvePacedFlow(t.eff, paces, pace.name, a.flow)
	if err != nil {
		return err
	}

	layers, err := agentLayers(t.eff, "")
	if err != nil {
		return err
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	drv := t.driver()
	h, hostErr := t.host()

	if fl.finishes() && hostErr != nil {
		return fmt.Errorf("finish is on (the pace's finish), and finish merges through the host: %w", hostErr)
	}

	var pullOf func(context.Context, string) (pull, error)
	if hostErr == nil {
		pullOf = h.pull
	}

	in := &driveInput{
		main:    main,
		pull:    pullOf,
		root:    t.lay.StorageRoot.Path(),
		driver:  drv,
		tracker: t.tracker(),
		args:    a,
		argv:    argv,
		flow:    fl,
		resolve: func(name string) (shipFlow, error) { return resolvePacedFlow(t.eff, paces, pace.name, name) },
		paced: func(fl shipFlow, p string) (shipFlow, error) {
			if fl.byArea {
				return resolvePacedFlow(t.eff, paces, p, "")
			}

			return resolvePacedFlow(t.eff, paces, p, fl.recorded())
		},
		storage:  t.lay.StorageRoot,
		settings: claudeSettingsPath(),
		layers:   layers,
		title:    func(r ref.Ref) func(context.Context) (string, error) { return titleLookup(t, r.Number) },
		started:  startedMove(t),
		record:   recordAt,
		finish: func(ctx context.Context, w io.Writer, req driveFinish) error {
			fin, err := driveFinishInput(t, main, h, req)
			if err != nil {
				return err
			}

			return finish(ctx, w, fin)
		},
		stateDir: state.Dir(),
		session:  state.Session(),
		ttl:      state.TTL(),
		now:      time.Now,
		asksDir:  asks.Dir(),
		notify:   askNotifier,

		defaultParallel: defaultParallel(),
	}
	in.sleep = in.wake
	in.resume = in.resumeSelf

	if spec, why := verifySpecOf(t.eff); why == "" {
		in.catchup = newBinaryCatchup(catchupInput{
			catchup:        t.eff.Catchup,
			stacked:        t.eff.CatchupStacked,
			catchupDefault: factDefault(t.eff, "catchup"),
			stackedDefault: factDefault(t.eff, "catchup_stacked"),
		}, spec, t.eff.Traces, main)
	} else {
		in.catchupWhy = why
	}

	if drain != nil {
		go func() {
			for range drain {
				in.draining.Store(true)
			}
		}()
	}

	return drive(ctx, cmd.Writer, in)
}

func resumeCommand(argv []string) string {
	parts := []string{"f10", "drive"}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		switch {
		case tok == "--answer":
			i++

			continue
		case strings.HasPrefix(tok, "--answer="):
			continue
		case strings.ContainsAny(tok, " \t\"'"):
			tok = strconv.Quote(tok)
		}

		parts = append(parts, tok)
	}

	return strings.Join(parts, " ")
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
