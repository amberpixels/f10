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
	"github.com/amberpixels/f10/cli/internal/state"
)

// The watcher reconciles a project's tasks that no drive owns, from the
// daemon's `f10 watch --pass` every few minutes. Owning is holding the
// task's lease, so a task a drive or a finish holds is never touched, and
// the watcher's own finish takes the lease first.

// watchStallGrace is longer than drive's, since nobody asked the watcher to
// keep the task moving.
const watchStallGrace = 10 * time.Minute

// watchFile records what each pass last reported, so a fact is told once.
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
	// the host did not answer for a task's request
	watchUnanswered = "unanswered"
	// a catchup the watcher sent, pushed, or did not end clean
	watchCatchup = "catchup"
	// behind its target, and the watcher cannot catch it up
	watchBehind = "behind"
)

// watchClause tells a driven catchup the watcher sent it: nobody follows
// up in its pane, so a failure leaves the branch as it was.
const watchClause = "sent by the watcher: a failure aborts the merge or rebase, so the branch is as it was"

func watchCommand() *cli.Command {
	return &cli.Command{
		Name: "watch",
		Usage: "what the f10 daemon watches in this project: tasks no drive owns, " +
			"their merged PRs, stalled agents and stopped runs",
		Description: "Bare, it lists each task the last pass saw and what it reported. " +
			"f10 start and f10 drive hand the project to the daemon, which runs a pass every few minutes; " +
			"watch: finish (the default), report or off in project.md says what a pass may do, " +
			"and watch_catchup: on lets it catch an open PR up with its target through the task's agent.",
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
	fmt.Fprintf(w, "catchup  %s\n", onOff(t.eff.WatchCatchup))

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
		if r.Kind == "" {
			continue // only a catchup record, nothing told yet
		}

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
		warn:     func(line string) { fmt.Fprintln(cmd.Writer, line) },
		catchup:  t.eff.WatchCatchup,
	}

	if b := reachableAgents(main); b != nil {
		in.agents = b
		in.prompt = func(ctx context.Context, task watchedTask, text string) error {
			return promptAgent(ctx, task.path, task.id, "", text)
		}
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

	// the janitor deletes branches: an act, which only watch: finish allows
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
// none runs. It never replaces or waits out a running daemon (f10 drive's
// job, which can tell the user), and an error is one warning, never a failed
// command. A variable so tests start no daemon.
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
	warn   func(string) // a line for the pass's output that is no report; nil drops it
	// catchup is watch_catchup: on; prompt is nil with no backend to reach
	// an agent
	catchup bool
	prompt  func(ctx context.Context, task watchedTask, text string) error
}

func (in watchInput) warnf(format string, args ...any) {
	if in.warn != nil {
		in.warn(fmt.Sprintf(format, args...))
	}
}

// A watchedTask is what one pass knows about a task with a worktree.
type watchedTask struct {
	id       string // the lead
	branch   string
	path     string
	owner    string          // who holds its lease; "" for nobody
	off      bool            // f10 watch off
	pr       *forge.PR       // nil with no request, or no host
	hostErr  error           // the host did not answer for its request this pass
	localSHA string          // the branch's tip
	run      *state.Run      // the newest run reported from its worktree
	agent    agentctl.Status // "" with no workspace, or no listing
	listed   bool            // the workspaces were listed, so "" is no workspace
	ws       string          // its workspace, for a message
	// what a catchup needs, read after the pass's fetch under watch_catchup
	fetched   bool     // origin was fetched and the fields below read
	originSHA string   // origin's tip; "" when origin lacks the branch
	target    prTarget // what git says its request targets
	targetErr error    // the target, or origin's tip, could not be read
	busy      string   // why its worktree cannot take a catchup, or ""
}

// A sentCatchup lives on the task's stored report, since a pass keeps
// nothing else between runs.
type sentCatchup struct {
	Target string    `yaml:"target"` // the sha the branch was caught up to
	Head   string    `yaml:"head"`   // the branch's tip when the catchup went out
	Origin string    `yaml:"origin"` // origin's tip then: what the push after it leases against
	Base   string    `yaml:"base"`   // the target's name in prose
	At     time.Time `yaml:"at"`
	// Failed is a catchup that did not end clean, told once; another goes
	// out only when the target or the head moves
	Failed bool `yaml:"failed,omitempty"`
}

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
	// Catchup is the catchup the watcher sent and has not settled, kept on
	// the stored report like Told
	Catchup *sentCatchup `yaml:"catchup,omitempty"`
}

// watchToldMax bounds a task's told keys; the oldest go first.
const watchToldMax = 32

// A reporter is where a pass tells what it found; the daemon keeps the
// pass's output as the project's watch log.
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

// watchPass reports false once the project has no task worktree and no
// local task branch, so the daemon drops it.
func watchPass(ctx context.Context, in watchInput) (bool, error) {
	tasks, branches, err := collectWatched(ctx, in)
	if err != nil {
		return false, err
	}

	store, err := loadWatchStore(in.root)
	if err != nil {
		return false, err
	}

	if in.catchup {
		in.readTargets(ctx, tasks, store)
	}

	in.reconcile(ctx, tasks, store)
	in.hostTrouble(tasks, store)

	if err := saveWatchStore(in.root, store); err != nil {
		return false, err
	}

	// a branch with no worktree is the janitor's, which only finish runs
	return len(tasks) > 0 || (in.mode == "finish" && branches > 0), nil
}

// watchHostKey is the store's entry for the host not answering, about the
// project, not one task.
const watchHostKey = "host"

// hostTrouble is told every pass, naming the tasks left out, since those
// tasks go silent; f10 watch shows it until the host answers again.
func (in watchInput) hostTrouble(tasks []watchedTask, store map[string]watchReport) {
	var (
		ids   []string
		cause error
	)

	for _, t := range tasks {
		if t.hostErr != nil {
			ids, cause = append(ids, t.id), cmp.Or(cause, t.hostErr)
		}
	}

	if cause == nil {
		delete(store, watchHostKey)

		return
	}

	rep := watchReport{
		Task: watchHostKey,
		Kind: watchUnanswered,
		Text: fmt.Sprintf("%v; %s left out of this pass", cause, strings.Join(ids, ", ")),
		Next: "check the host CLI's login in the shell that registered the project, then run f10 start or f10 drive there",
		Key:  watchUnanswered,
		At:   in.now(),
	}

	in.out.report(rep)
	store[watchHostKey] = rep
}

// reconcile forgets tasks gone from the project, except a finish, kept a
// day for f10 watch.
func (in watchInput) reconcile(ctx context.Context, tasks []watchedTask, store map[string]watchReport) {
	now := in.now()
	seen := map[string]bool{}

	for _, task := range tasks {
		seen[task.id] = true

		if in.catchup {
			in.tell(store, task.id, now, in.catchupStep(ctx, task, store))
		}

		rep, act := decideWatch(task, in.mode, now)

		if act && in.finish != nil {
			rep = in.finishReport(ctx, task)
		}

		in.tell(store, task.id, now, rep)
	}

	for id, r := range store {
		if !seen[id] && id != watchHostKey && (r.Kind != watchFinished || now.Sub(r.At) > 24*time.Hour) {
			delete(store, id)
		}
	}
}

// tell carries the stored report's told keys and catchup record over.
func (in watchInput) tell(store map[string]watchReport, id string, now time.Time, rep *watchReport) {
	if rep == nil {
		return
	}

	rep.Task, rep.At = id, now

	prev := store[id]
	if prev.Key == rep.Key || slices.Contains(prev.Told, rep.Key) {
		return
	}

	in.out.report(*rep)

	rep.Told = append(slices.Clone(prev.Told), rep.Key)
	if len(rep.Told) > watchToldMax {
		rep.Told = rep.Told[len(rep.Told)-watchToldMax:]
	}

	rep.Catchup = prev.Catchup
	store[id] = *rep
}

func setCatchup(store map[string]watchReport, id string, rec *sentCatchup) {
	r := store[id]
	r.Catchup = rec
	store[id] = r
}

// finishReport: a lease someone took meanwhile is no report, they own it now.
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

// decideWatch answers the request first, then a stalled agent, then a
// stopped run.
func decideWatch(t watchedTask, mode string, now time.Time) (*watchReport, bool) {
	// a host that did not answer leaves the request unknown: saying
	// anything else about the task this pass could hide a merge
	if t.owner != "" || t.off || t.hostErr != nil {
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
		if stalled(t.agent, run, phase, time.Time{}, now.Sub(run.Updated), watchStallGrace) {
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

func collectWatched(ctx context.Context, in watchInput) ([]watchedTask, int, error) {
	def, err := janitorDefault(ctx, in.main)
	if err != nil {
		return nil, 0, err
	}

	local := in.jan
	local.localOnly = true

	branches, held, err := taskBranches(ctx, local, def)
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
			task.off = in.handedBack(ctx, b.name)
		}

		if task.owner != "" || task.off {
			tasks = append(tasks, task)

			continue
		}

		if in.jan.host != nil {
			task.pr, task.hostErr = j.request(ctx, b)
		}

		if runs := inRoot(live, path); len(runs) > 0 {
			task.run = runs[0]
		}

		if ws := workspaceAt(wss, path); ws != nil {
			task.agent, task.ws = ws.Status, workspaceLabel(ws)
		}

		tasks = append(tasks, task)
	}

	return tasks, len(branches), nil
}

// watcherFinish holds the lease as the watcher and refuses anything but a
// merged request. It writes no drive finish record: the board would read
// the watcher as a drive.
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

	in := finishInputFor(t, main, h, r)
	in.branch, in.mergedOnly = task.branch, true
	// never processPlace: the self-close path would end the daemon, and its
	// directory may be a worktree since removed, where getwd fails
	in.cwd = main

	err = finish(ctx, &buf, in)

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line != "" {
			fmt.Fprintln(w, "  "+line)
		}
	}

	if err == nil {
		recordAt(ctx, main, finishedEvent(task.id, "watch", time.Now()))
	}

	return err
}

// handedBack counts a mark git cannot read as handed back, with a line naming
// it: a finish must never run on a task the user may have taken back.
func (in watchInput) handedBack(ctx context.Context, branch string) bool {
	v, err := gitx.Config(ctx, in.main, "branch."+branch+"."+cfgWatch)
	if err != nil {
		if in.warn != nil {
			in.warn(fmt.Sprintf("%s skipped this pass: its f10 watch off mark could not be read: %v", branch, err))
		}

		return true
	}

	return v == "false"
}

// recheckFinish reads again, under the lease, what the pass read before it:
// an agent that started working, or a commit the PR lacks, keeps the task.
func recheckFinish(ctx context.Context, b agentctl.Backend, main string, task watchedTask) error {
	tip, err := gitx.Ref(ctx, main, "refs/heads/"+task.branch)
	if err != nil {
		return fmt.Errorf("reading %s's tip: %w", task.branch, err)
	}

	if tip != task.pr.HeadSHA {
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

func onOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}

// readTargets fetches origin once, for every task that could be caught up
// or has a catchup to settle. A failed fetch reads nothing: a stale origin
// could hide a move, and a push lease needs the current sha.
func (in watchInput) readTargets(ctx context.Context, tasks []watchedTask, store map[string]watchReport) {
	var want []int

	for i, t := range tasks {
		if t.owner != "" || t.off || t.hostErr != nil || !t.listed || t.agent == agentctl.StatusWorking ||
			waitingOnAsk(t.run) {
			continue
		}

		rec := store[t.id].Catchup
		if rec == nil && (t.pr == nil || t.pr.Status != forge.Open) {
			continue
		}

		want = append(want, i)
	}

	if len(want) == 0 {
		return
	}

	if _, err := gitx.Exec(ctx, in.main, "fetch", "origin"); err != nil {
		in.warnf("no catchup this pass, fetching origin: %v", err)

		return
	}

	for _, i := range want {
		t := &tasks[i]
		t.fetched = true

		t.originSHA, t.targetErr = gitx.Ref(ctx, in.main, "refs/remotes/origin/"+t.branch)
		if t.targetErr != nil {
			continue
		}

		t.target, t.targetErr = prTargetOf(ctx, in.main, t.branch)
		t.busy = treeBusy(ctx, t.path)
	}
}

func (in watchInput) catchupStep(ctx context.Context, task watchedTask, store map[string]watchReport) *watchReport {
	if !task.fetched || task.agent == agentctl.StatusWorking || waitingOnAsk(task.run) {
		return nil
	}

	if task.targetErr != nil {
		in.warnf("%s skipped this pass, reading what %s targets: %v", task.id, task.branch, task.targetErr)

		return nil
	}

	rec := store[task.id].Catchup
	if rec != nil && !rec.Failed {
		return in.settleCatchup(ctx, task, rec, store)
	}

	return in.sendCatchup(ctx, task, rec, store)
}

// settleCatchup pushes a clean catchup under the task's lease, against the
// origin tip it started from, so a commit pushed meanwhile is never
// overwritten; one not clean with the agent idle is told once and left as
// it is, since an operation in progress there may be someone's hand merge.
func (in watchInput) settleCatchup(
	ctx context.Context, task watchedTask, rec *sentCatchup, store map[string]watchReport,
) *watchReport {
	if task.pr == nil || task.pr.Status != forge.Open {
		setCatchup(store, task.id, nil) // its request is gone: nothing to keep current

		return nil
	}

	if reason := branchUnsettled(ctx, in.main, task.path, task.branch); reason != "" {
		rec.Failed = true

		return &watchReport{
			Kind: watchCatchup,
			Text: fmt.Sprintf("catching %s up with %s did not end clean: %s", task.branch, rec.Base, reason),
			Next: "/f10:catchup in " + task.path,
			Key:  watchCatchup + " failed " + rec.Target + " " + rec.Head,
		}
	}

	lease, err := drives.AcquireAs(in.root, task.id, drives.Holder{Watch: true})

	if held := (drives.HeldError{}); errors.As(err, &held) {
		setCatchup(store, task.id, nil) // whoever holds it pushes

		return nil
	}

	if err != nil {
		return in.pushFailed(task, rec, err)
	}

	defer lease.Release()

	if in.worktreeBusy(ctx, task) {
		return nil
	}

	head, err := gitx.Ref(ctx, task.path, "HEAD")
	if err != nil {
		return in.pushFailed(task, rec, err)
	}

	if task.originSHA == "" || head == task.originSHA {
		setCatchup(store, task.id, nil) // nothing to push, or nowhere to push it

		return nil
	}

	if task.originSHA != rec.Origin {
		// someone pushed to the request while the catchup ran: the branch
		// here no longer carries it, so a push would drop their commits
		rec.Failed, rec.Head = true, head

		return &watchReport{
			Kind: watchCatchup,
			Text: fmt.Sprintf("%s caught up with %s, and origin/%s moved meanwhile: not pushed",
				task.branch, rec.Base, task.branch),
			Next: "bring origin/" + task.branch + " into " + task.path + ", then /f10:catchup there",
			Key:  watchCatchup + " origin moved " + task.originSHA,
		}
	}

	how, err := pushCaughtUp(ctx, task.path, task.branch, rec.Origin, rec.Base)
	if err != nil {
		return in.pushFailed(task, rec, err)
	}

	setCatchup(store, task.id, nil)

	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, %s", task.branch, rec.Base, how),
		Key:  watchCatchup + " pushed " + head,
	}
}

// pushFailed is told once per cause, the record kept so the next pass retries.
func (in watchInput) pushFailed(task watchedTask, rec *sentCatchup, err error) *watchReport {
	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, and pushing it failed: %v", task.branch, rec.Base, err),
		Next: "push " + task.branch + " from " + task.path + ", with --force-with-lease after a rebase",
		Key:  watchCatchup + " push failed " + rec.Target + " " + rec.Head,
	}
}

// sendCatchup prompts the agent only under the task's lease, idle and with
// a clean tree; what it cannot act on is told once. failed, the last catchup
// that did not end clean, blocks another until the target or the head moves.
func (in watchInput) sendCatchup(
	ctx context.Context, task watchedTask, failed *sentCatchup, store map[string]watchReport,
) *watchReport {
	pr, target := task.pr, task.target
	if pr == nil || pr.Status != forge.Open || task.originSHA == "" || target.sha == "" {
		return nil
	}

	if failed != nil && failed.Target == target.sha && failed.Head == task.localSHA {
		return nil
	}

	// a stacked base read from its local branch only: its unpushed commits
	// would ride into this request, so the catchup waits for its push
	if target.key == cfgMoved {
		on, err := gitx.IsAncestor(ctx, in.main, target.sha, "refs/remotes/origin/"+target.name)

		switch {
		case err != nil:
			in.warnf("%s skipped this pass, reading origin/%s: %v", task.id, target.name, err)

			return nil
		case !on:
			return nil
		}
	}

	behind := func(reason, text string) *watchReport {
		return &watchReport{
			Kind: watchBehind,
			Text: text,
			Next: "/f10:catchup in " + task.path,
			Key:  watchBehind + " " + target.sha + " " + reason,
		}
	}

	// a request retargeted by hand: a catchup with git's base would carry
	// another branch's commits into it
	if base := strings.TrimPrefix(target.name, "origin/"); pr.Base != base {
		return behind("retargeted", fmt.Sprintf("%s targets %s, not %s, which %s was started on: not caught up",
			pr.URL, pr.Base, base, task.branch))
	}

	// a worktree behind origin would rebase a stale branch; one ahead of it
	// holds work the request has not seen
	if task.localSHA != task.originSHA {
		return behind("diverged", fmt.Sprintf("%s moved past %s, whose tip is not origin's: not caught up",
			target.name, task.branch))
	}

	// a mark already on the branch (a finish that landed its base, a ship guard
	// stopped on a conflict, a failed watcher catchup) is a catchup owed that
	// nobody else sends: set again below
	has, err := gitx.IsAncestor(ctx, in.main, target.sha, "refs/heads/"+task.branch)

	switch {
	case err != nil:
		in.warnf("%s skipped this pass, reading %s: %v", task.id, task.branch, err)

		return nil
	case has:
		return nil
	}

	if task.ws == "" || in.prompt == nil {
		return behind("no agent", fmt.Sprintf("%s moved past %s, and no agent is in its workspace to catch it up",
			target.name, task.branch))
	}

	if task.busy != "" {
		return nil // a later pass
	}

	lease, err := drives.AcquireAs(in.root, task.id, drives.Holder{Watch: true})
	if err != nil {
		if held := (drives.HeldError{}); !errors.As(err, &held) {
			in.warnf("%s skipped this pass: %v", task.id, err)
		}

		return nil
	}

	defer lease.Release()

	if in.worktreeBusy(ctx, task) {
		return nil
	}

	head, err := gitx.Ref(ctx, task.path, "HEAD")
	if err != nil {
		in.warnf("%s skipped this pass, reading the head of %s: %v", task.id, task.branch, err)

		return nil
	}

	mark := "branch." + task.branch + "." + target.key
	if err := gitx.SetConfig(ctx, in.main, mark, target.value); err != nil {
		in.warnf("%s skipped this pass, marking %s behind %s: %v", task.id, task.branch, target.name, err)

		return nil
	}

	if err := in.prompt(ctx, task, driven("/f10:catchup")+" && "+watchClause); err != nil {
		_ = gitx.UnsetConfig(ctx, in.main, mark) // nobody will clear it

		if errors.Is(err, agentctl.ErrAgentNotFound) {
			return behind("no agent", fmt.Sprintf("%s moved past %s, and its agent was not found to catch it up",
				target.name, task.branch))
		}

		return behind("prompt", fmt.Sprintf("%s moved past %s, and prompting its agent failed: %v",
			target.name, task.branch, err))
	}

	setCatchup(store, task.id, &sentCatchup{
		Target: target.sha, Head: head, Origin: task.originSHA, Base: target.name, At: in.now(),
	})

	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("catching %s up with %s, which moved past it", task.branch, target.name),
		Key:  watchCatchup + " sent " + target.sha + " " + head,
	}
}

// waitingOnAsk: the next prompt such an idle agent gets should be the
// answer, not a catchup.
func waitingOnAsk(run *state.Run) bool { return run != nil && run.Ask != "" }

// worktreeBusy is recheckFinish's read for a catchup: a working agent or an
// unclean tree waits for the next pass.
func (in watchInput) worktreeBusy(ctx context.Context, task watchedTask) bool {
	if in.agents != nil {
		wss, err := in.agents.Workspaces(ctx)
		if err != nil {
			return true
		}

		if ws := workspaceAt(wss, task.path); ws != nil && ws.Status == agentctl.StatusWorking {
			return true
		}
	}

	return treeBusy(ctx, task.path) != ""
}
