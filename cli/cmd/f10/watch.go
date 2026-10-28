package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The watcher reconciles a project's tasks that no drive owns: the daemon
// runs `f10 watch --pass` in its main checkout every few minutes. Owning
// is holding the task's lease, so a task a drive or a finish holds is
// never touched, and the watcher's own finish takes the lease first.

// watchStallGrace is how long an agent sits idle under a running phase
// before the watcher reports it; longer than drive's, since nobody asked
// the watcher to keep the task moving.
const watchStallGrace = 10 * time.Minute

// watchFile is the shared storage root's record of what each pass last
// reported per task, so the same fact is told once.
const watchFile = "watch.yaml"

// cfgWatch is the branch config key `f10 watch off` sets to false.
const cfgWatch = "f10-watch"

// What a pass reports about a task.
const (
	watchMerged   = "merged"   // its request merged on the host
	watchClosed   = "closed"   // its request closed without a merge
	watchStalled  = "stalled"  // its agent idle while its run says running
	watchStopped  = "stopped"  // its run stopped: done, blocked, failed or partial
	watchFinished = "finished" // the watcher finished it
	watchRefused  = "refused"  // the watcher's finish was refused
)

func watchCommand() *cli.Command {
	return &cli.Command{
		Name: "watch",
		Usage: "what the f10 daemon watches in this project: tasks no drive owns, " +
			"their merged PRs, stalled agents and stopped runs",
		Description: "Bare, it lists each task the last pass saw and what it reported. " +
			"f10 start and f10 drive hand the project to the daemon, which runs a pass every few minutes; " +
			"watch: report (the default), finish or off in project.md says what a pass may do.",
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
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("pass") {
		return runWatchPass(ctx, cmd, t)
	}

	store, err := loadWatchStore(t.sharedRoot())
	if err != nil {
		return err
	}

	w := cmd.Writer
	fmt.Fprintf(w, "watch    %s\n", t.eff.Watch)

	if len(store) == 0 {
		fmt.Fprintln(w, "nothing reported yet")

		return nil
	}

	ids := make([]string, 0, len(store))
	for id := range store {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		r := store[id]
		fmt.Fprintf(w, "\n%s  %s, %s\n  %s\n", id, r.Kind, r.At.Local().Format(time.DateTime), r.Text)

		if r.Ask != "" {
			fmt.Fprintf(w, "  asks: %s\n", r.Ask)
		}

		if r.Next != "" {
			fmt.Fprintf(w, "  next: %s\n", r.Next)
		}
	}

	return nil
}

// runWatchToggle sets or clears a task's opt-out on its branch.
func runWatchToggle(ctx context.Context, cmd *cli.Command, on bool) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	r = r.Head()
	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	name, _, err := taskCheckout(ctx, main, t.driver(), r, "")
	if err != nil {
		return err
	}

	if name == "" {
		return fmt.Errorf("%s has no branch here: nothing to watch", r.ID)
	}

	key := "branch." + name + "." + cfgWatch

	if on {
		_ = gitx.UnsetConfig(ctx, main, key) // absent is fine

		fmt.Fprintf(cmd.Writer, "%s is watched again\n", r.ID)

		return nil
	}

	if err := gitx.SetConfig(ctx, main, key, "false"); err != nil {
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

func runWatchPass(ctx context.Context, cmd *cli.Command, t *target) error {
	if t.eff.Watch == "off" {
		return cli.Exit("watch: off in this project", daemon.ExitNothingWatched)
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	root := t.sharedRoot()

	in := watchInput{
		main:     main,
		root:     root,
		mode:     t.eff.Watch,
		jan:      janitorFor(t),
		stateDir: state.Dir(),
		ttl:      state.TTL(),
		now:      time.Now,
		out:      lineReporter{w: cmd.Writer},
	}

	if b := reachableAgents(main); b != nil {
		in.agents = b
	}

	if in.mode == "finish" && in.jan.host != nil {
		in.finish = func(ctx context.Context, task watchedTask) error {
			return watcherFinish(ctx, cmd.Writer, t, main, root, task)
		}
	}

	watching, err := watchPass(ctx, in)
	if err != nil {
		return err
	}

	// the janitor deletes branches, here and on origin: an act, which only
	// watch: finish lets a pass take
	if in.mode == "finish" {
		sweepIfDue(ctx, cmd.Writer, filepath.Join(root, janitorStamp), time.Now(),
			func(ctx context.Context) ([]string, error) { return janitor(ctx, janitorFor(t)) })
	}

	if !watching {
		return cli.Exit("nothing left to watch", daemon.ExitNothingWatched)
	}

	return nil
}

// watchProject hands the project to this user's daemon, starting one when
// none runs, so its tasks are watched once nothing drives them. It never
// replaces or waits out a running daemon: that is f10 drive's, which can
// tell the user. Nothing it does fails the command: an error is one
// warning. A variable so tests start no daemon.
var watchProject = func(ctx context.Context, w io.Writer, t *target) {
	if !daemon.Supported || t.eff.Watch == "off" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, watchHandWait)
	defer cancel()

	dir := daemon.RunDir()

	rep, err := daemon.Call(ctx, dir, daemon.Request{Op: daemon.OpHello}, 0)

	switch {
	case err == nil && rep.Stopping:
		fmt.Fprintln(w, "watch: the f10 daemon is stopping: the next f10 start or f10 drive hands this project over")

		return
	case errors.Is(err, daemon.ErrNotRunning):
		if _, err := (&daemon.Ensurer{Dir: dir, Version: daemonVersion()}).Ensure(ctx); err != nil {
			fmt.Fprintf(w, "watch: warning: %v\n", err)

			return
		}
	case err != nil:
		fmt.Fprintf(w, "watch: warning: %v\n", err)

		return
	}

	_, err = daemon.Call(ctx, dir, daemon.Request{
		Op:   daemon.OpWatch,
		Root: t.sharedRoot(),
		Dir:  cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		Env:  driveEnv(os.Environ(), t.eff.DriveEnv),
	}, 0)
	if err != nil {
		fmt.Fprintf(w, "watch: warning: %v\n", err)
	}
}

// watchHandWait bounds handing a project over, a daemon started included.
const watchHandWait = 10 * time.Second

// watchInput is everything a pass needs, so it runs in a test without a
// daemon, Herdr or a host.
type watchInput struct {
	main, root string
	mode       string // watch: report or finish
	jan        janitorInput
	agents     agentctl.Backend // nil when this process cannot reach one
	stateDir   string
	ttl        time.Duration
	now        func() time.Time
	// finish finishes a merged task under its lease; nil in report mode
	finish func(context.Context, watchedTask) error
	out    reporter
}

// A watchedTask is what one pass knows about a task with a worktree.
type watchedTask struct {
	id       string // the lead
	branch   string
	path     string
	owner    string          // who holds its lease; "" for nobody
	off      bool            // f10 watch off
	pr       *forge.PR       // nil with no request, or no host
	hostless bool            // the host did not answer for its request this pass
	localSHA string          // the branch's tip
	run      *state.Run      // the newest run reported from its worktree
	agent    agentctl.Status // "" with no workspace, or no listing
	listed   bool            // the workspaces were listed, so "" is no workspace
	ws       string          // its workspace, for a message
}

// A watchReport is one thing a pass tells about a task.
type watchReport struct {
	Task string `yaml:"-"`
	Kind string `yaml:"kind"`
	Text string `yaml:"text"`
	Next string `yaml:"next,omitempty"`
	Ask  string `yaml:"ask,omitempty"` // a stopped run's question, options included
	// Key is the fact the report was made on; Told, kept on the stored
	// report, every key told for the task, so an old fact coming back after
	// another (a host that did not answer for a pass) is not told again
	Key  string    `yaml:"key"`
	Told []string  `yaml:"told,omitempty"`
	At   time.Time `yaml:"at"`
}

// watchToldMax bounds a task's told keys; the oldest go first.
const watchToldMax = 32

// A reporter is where a pass tells what it found. The pass's own output,
// which the daemon keeps in the project's watch log, is the only one.
type reporter interface {
	report(watchReport)
}

type lineReporter struct{ w io.Writer }

func (l lineReporter) report(r watchReport) {
	line := r.Task + " " + r.Kind + ": " + r.Text
	if r.Ask != "" {
		line += "; asks: " + r.Ask
	}

	if r.Next != "" {
		line += " (next: " + r.Next + ")"
	}

	fmt.Fprintln(l.w, line)
}

// watchPass reads every task worktree, reports what is new about each one
// no drive owns, and finishes merged ones when the mode says so. watching
// is false once the project has no task worktree and no local task branch.
func watchPass(ctx context.Context, in watchInput) (bool, error) {
	tasks, branches, err := collectWatched(ctx, in)
	if err != nil {
		return false, err
	}

	store, err := loadWatchStore(in.root)
	if err != nil {
		return false, err
	}

	in.reconcile(ctx, tasks, store)

	if err := saveWatchStore(in.root, store); err != nil {
		return false, err
	}

	return len(tasks) > 0 || branches > 0, nil
}

// reconcile reports what is new about each task, finishing the merged
// ones the mode lets it, and forgets tasks gone from the project, except a
// finish, kept a day for f10 watch.
func (in watchInput) reconcile(ctx context.Context, tasks []watchedTask, store map[string]watchReport) {
	now := in.now()
	seen := map[string]bool{}

	for _, task := range tasks {
		seen[task.id] = true

		rep, act := decideWatch(task, in.mode, now)

		if act && in.finish != nil {
			rep = in.finishReport(ctx, task)
		}

		if rep == nil {
			continue
		}

		rep.Task, rep.At = task.id, now

		prev := store[task.id]
		if prev.Key == rep.Key || slices.Contains(prev.Told, rep.Key) {
			continue
		}

		in.out.report(*rep)

		rep.Told = append(slices.Clone(prev.Told), rep.Key)
		if len(rep.Told) > watchToldMax {
			rep.Told = rep.Told[len(rep.Told)-watchToldMax:]
		}

		store[task.id] = *rep
	}

	for id, r := range store {
		if !seen[id] && (r.Kind != watchFinished || now.Sub(r.At) > 24*time.Hour) {
			delete(store, id)
		}
	}
}

// finishReport runs the watcher's finish of a merged task, as a report.
// A lease someone took meanwhile is no report: they own it now.
func (in watchInput) finishReport(ctx context.Context, task watchedTask) *watchReport {
	err := in.finish(ctx, task)

	if held := (drives.HeldError{}); errors.As(err, &held) {
		return nil
	}

	url := task.pr.URL
	if err != nil {
		return &watchReport{
			Kind: watchRefused,
			Text: url + " merged, and finishing it was refused: " + err.Error(),
			Next: "f10 finish " + task.id,
			Key:  watchRefused + " " + url, // a refusal's text may vary between passes
		}
	}

	return &watchReport{
		Kind: watchFinished,
		Text: "finished by the watcher: " + url + " merged, worktree removed, plan archived",
		Key:  watchFinished + " " + url,
	}
}

// decideWatch is what a pass says about one task, and whether it finishes
// it: the request first, then a stalled agent, then a stopped run.
func decideWatch(t watchedTask, mode string, now time.Time) (*watchReport, bool) {
	// a host that did not answer leaves the request unknown: saying
	// anything else about the task this pass could hide a merge
	if t.owner != "" || t.off || t.hostless {
		return nil, false
	}

	if p := t.pr; p != nil {
		switch p.Status {
		case forge.Merged:
			if t.localSHA != "" && t.localSHA != p.HeadSHA {
				return &watchReport{
					Kind: watchMerged,
					Text: p.URL + " merged, and " + t.branch + " has commits it lacks",
					Next: "look at " + t.path + " before f10 finish " + t.id,
					Key:  watchMerged + " " + p.URL + " " + t.localSHA,
				}, false
			}

			// closing the workspace ends the agent's conversation: never
			// while someone is using it, or while nobody can tell
			act := mode == "finish" && t.listed && t.agent != agentctl.StatusWorking

			return &watchReport{
				Kind: watchMerged,
				Text: p.URL + " merged",
				Next: "f10 finish " + t.id,
				Key:  watchMerged + " " + p.URL,
			}, act
		case forge.Closed:
			return &watchReport{
				Kind: watchClosed,
				Text: p.URL + " closed without a merge",
				Next: "f10 finish " + t.id + " --local, or reopen it",
				Key:  watchClosed + " " + p.URL,
			}, false
		}
	}

	run := t.run
	if run == nil {
		return nil, false
	}

	updated := run.Updated.UTC().Format(time.RFC3339)

	for _, phase := range []string{"ship", "plan"} {
		if stalled(t.agent, run, phase, now.Sub(run.Updated), watchStallGrace) {
			return &watchReport{
				Kind: watchStalled,
				Text: fmt.Sprintf("agent idle while its run says %s %s", phase, run.PhaseText(phase)),
				Next: "answer it in workspace " + cmp.Or(t.ws, t.path),
				Key:  watchStalled + " " + updated,
			}, false
		}
	}

	if !stoppedRun(run) && run.Ship != "done" {
		return nil, false
	}

	text := runStopText(run)
	if run.Note != "" {
		text += ": " + run.Note
	}

	return &watchReport{
		Kind: watchStopped,
		Text: text,
		Next: run.Next,
		Ask:  run.Ask,
		Key:  watchStopped + " " + updated,
	}, false
}

// runStopText names the phase a run stopped in and how: `ship blocked
// at judge`, `ship done`.
func runStopText(run *state.Run) string {
	for _, phase := range slices.Backward(state.Phases) {
		if s := run.Status(phase); state.Stopped(s) || (phase == "ship" && s == "done") {
			text := phase + " " + s
			if run.Leaf != "" && s != "done" {
				text += " at " + run.Leaf
			}

			return text
		}
	}

	return "stopped"
}

// collectWatched reads every task worktree, and counts the local task
// branches, with or without one.
func collectWatched(ctx context.Context, in watchInput) ([]watchedTask, int, error) {
	def, err := janitorDefault(ctx, in.main)
	if err != nil {
		return nil, 0, err
	}

	branches, held, err := taskBranches(ctx, in.jan, def)
	if err != nil {
		return nil, 0, err
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return nil, 0, err
	}

	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, 0, err
	}

	var (
		wss    []agentctl.Workspace
		listed bool
	)

	if in.agents != nil {
		// no listing is no agent status: stalls and finishes wait for a pass
		// that has one
		var err error
		wss, err = in.agents.Workspaces(ctx)
		listed = err == nil
	}

	j := &sweeper{in: in.jan, def: def}

	var tasks []watchedTask

	for _, b := range branches {
		path := worktreeOf(wts, b.name)
		if !held[b.name] || path == "" || gitx.SamePath(path, in.main) {
			continue
		}

		task := watchedTask{id: b.ids[0], branch: b.name, path: path, localSHA: b.localSHA, listed: listed}

		if h, ok := drives.Probe(in.root, task.id); ok {
			task.owner = h.String()
		} else {
			task.off = gitx.Out(ctx, in.main, "config", "--get", "branch."+b.name+"."+cfgWatch) == "false"
		}

		if task.owner != "" || task.off {
			tasks = append(tasks, task)

			continue
		}

		if in.jan.host != nil {
			var err error
			task.pr, err = j.request(ctx, b)
			task.hostless = err != nil
		}

		if runs := inRoot(live, path); len(runs) > 0 {
			task.run = runs[0]
		}

		if ws := workspaceAt(wss, path); ws != nil {
			task.agent, task.ws = ws.Status, workspaceLabel(ws)
		}

		tasks = append(tasks, task)
	}

	// origin's task branches are anyone's: only this checkout's keep it watched
	local := 0

	for _, b := range branches {
		if b.localSHA != "" {
			local++
		}
	}

	return tasks, local, nil
}

// watcherFinish finishes a merged task under its lease, held as the
// watcher, with a finish that refuses anything but a merged request. Its
// report goes to the pass's output, a line per row. It writes no drive
// finish record: the board would read the watcher as a drive.
func watcherFinish(ctx context.Context, w io.Writer, t *target, main, root string, task watchedTask) error {
	lease, err := drives.AcquireAs(root, task.id, drives.Holder{Watch: true})
	if err != nil {
		return err
	}
	defer lease.Release()

	if err := recheckFinish(ctx, reachableAgents(main), main, task); err != nil {
		return err
	}

	r, err := t.reference(ctx, task.id)
	if err != nil {
		return err
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	err = finish(ctx, &buf, finishInput{
		main:       main,
		driver:     t.driver(),
		task:       r.Head(),
		branch:     task.branch,
		host:       h.at(main),
		method:     t.eff.MergeMethod,
		traces:     t.eff.Traces,
		cwd:        main,
		plansDir:   t.sharedPlansDir(),
		title:      titleLookup(t, r.Number),
		mergedOnly: true,
	})

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line != "" {
			fmt.Fprintln(w, "  "+line)
		}
	}

	if err == nil {
		// finish ran in this process, where no hook sees it
		recordAt(ctx, main, ledger.Event{
			At:      time.Now(),
			Task:    task.id,
			Kind:    ledger.TaskFinished,
			Source:  ledger.Observed,
			Payload: ledger.NewPayload(ledger.Payload{"args": []any{task.id}, "by": "watch"}),
		})
	}

	return err
}

// recheckFinish reads again, under the lease, what the pass read before
// it: an agent that started working, or a commit the PR lacks, since then
// keeps the task.
func recheckFinish(ctx context.Context, b agentctl.Backend, main string, task watchedTask) error {
	if tip := gitx.Out(ctx, main, "rev-parse", "-q", "--verify", "refs/heads/"+task.branch); tip != task.pr.HeadSHA {
		return fmt.Errorf("%s has commits %s lacks", task.branch, task.pr.URL)
	}

	if b == nil {
		return errors.New("no agent status to read, so its workspace stays")
	}

	wss, err := b.Workspaces(ctx)
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}

	if ws := workspaceAt(wss, task.path); ws != nil && ws.Status == agentctl.StatusWorking {
		return fmt.Errorf("its agent in workspace %s is working", workspaceLabel(ws))
	}

	return nil
}

func loadWatchStore(root string) (map[string]watchReport, error) {
	store := map[string]watchReport{}

	raw, err := os.ReadFile(filepath.Join(root, watchFile))
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}

	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}

	if err := yaml.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("watch: %s: %w", watchFile, err)
	}

	return store, nil
}

func saveWatchStore(root string, store map[string]watchReport) error {
	raw, err := yaml.Marshal(store)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	if err := os.WriteFile(filepath.Join(root, watchFile), raw, 0o600); err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	return nil
}
