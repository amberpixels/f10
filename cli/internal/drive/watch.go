package drive

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

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// The watcher reconciles a project's tasks that no drive owns, from the
// daemon's `f10 watch --pass` every few minutes. Owning is holding the
// task's lease, so a task a drive or a finish holds is never touched, and
// the watcher's own finish takes the lease first.

// watchStallGrace is longer than drive's, since nobody asked the watcher to
// keep the task moving.
const watchStallGrace = 10 * time.Minute

// WatchFile records what each pass last reported, so a fact is told once.
const WatchFile = "watch.yaml"

// What a pass reports about a task.
const (
	WatchMerged   = "merged"   // its request merged on the host
	WatchClosed   = "closed"   // its request closed without a merge
	WatchStalled  = "stalled"  // its agent idle while its run says running
	WatchStopped  = "stopped"  // its run stopped: done, blocked, failed or partial
	WatchFinished = "finished" // the watcher finished it
	WatchRefused  = "refused"  // the watcher's finish was refused
	// the host did not answer for a task's request
	watchUnanswered = "unanswered"
	// WatchUnpulled is a pull of the default branch a finish deferred, which the watcher retries
	WatchUnpulled = "unpulled"
	// WatchCatchup is a catchup the watcher sent, pushed, or did not end clean
	WatchCatchup = "catchup"
	// WatchBehind is a branch behind its target, and the watcher cannot catch it up
	WatchBehind = "behind"
)

// watchClause tells a driven catchup the watcher sent it: nobody follows
// up in its pane, so a failure leaves the branch as it was, at head.
func watchClause(head string) string {
	return "sent by the watcher: a failure aborts the merge or rebase, or resets to " + gitx.ShortSHA(head) +
		", so the branch is as it was"
}

// WatchInput is everything a pass needs, so it runs in a test without a
// daemon, Herdr or a host.
type WatchInput struct {
	Main, Root string
	Mode       string // watch: report or finish
	Jan        finish.JanitorInput
	Agents     agentctl.Backend // nil when this process cannot reach one
	StateDir   string
	TTL        time.Duration
	Now        func() time.Time
	// Finish finishes a merged task under its lease; nil in report mode
	Finish func(context.Context, WatchedTask) error
	Out    Reporter
	Warn   func(string) // a line for the pass's output that is no report; nil drops it
	// Catchup is watch_catchup: on; prompt is nil with no backend to reach
	// an agent
	Catchup bool
	Prompt  func(ctx context.Context, task WatchedTask, text string) error
	// Engine integrates a catchup; storage names the task's review series
	// for a catchup merge's trailers
	Engine  *stack.CatchupEngine
	Storage storage.Root
}

// RetryPull retries a pull a finish deferred, under watch: finish alone,
// since pulling is an act. A cause already told is not told again.
func (in WatchInput) RetryPull(ctx context.Context, store map[string]WatchReport) bool {
	if in.Mode != "finish" {
		return false
	}

	line, pending := finish.RetryPendingPull(ctx, in.Root, in.Main)
	if !pending {
		if line != "" {
			in.warnf("%s", line)
		}

		delete(store, WatchPullKey)

		return false
	}

	if store[WatchPullKey].Key == line {
		return true
	}

	rep := WatchReport{Task: WatchPullKey, Kind: WatchUnpulled, Text: line, Key: line, At: in.Now()}
	in.Out.Report(rep)
	store[WatchPullKey] = rep

	return true
}

// HostTrouble is told every pass, naming the tasks left out, since those
// tasks go silent; f10 watch shows it until the host answers again.
func (in WatchInput) HostTrouble(tasks []WatchedTask, store map[string]WatchReport) {
	var (
		ids   []string
		cause error
	)

	for _, t := range tasks {
		if t.HostErr != nil {
			ids, cause = append(ids, t.Id), cmp.Or(cause, t.HostErr)
		}
	}

	if cause == nil {
		delete(store, WatchHostKey)

		return
	}

	rep := WatchReport{
		Task: WatchHostKey,
		Kind: watchUnanswered,
		Text: fmt.Sprintf("%v; %s left out of this pass", cause, strings.Join(ids, ", ")),
		Next: "check the host CLI's login in the shell that registered the project, then run f10 start or f10 drive there",
		Key:  watchUnanswered,
		At:   in.Now(),
	}

	in.Out.Report(rep)
	store[WatchHostKey] = rep
}

// Reconcile forgets tasks gone from the project, except a finish, kept a
// day for f10 watch.
func (in WatchInput) Reconcile(ctx context.Context, tasks []WatchedTask, store map[string]WatchReport) {
	now := in.Now()
	seen := map[string]bool{}

	for _, task := range tasks {
		seen[task.Id] = true

		if in.Catchup {
			in.Tell(store, task.Id, now, in.catchupStep(ctx, task, store))
		}

		rep, act := DecideWatch(task, in.Mode, now)

		if act && in.Finish != nil {
			rep = in.finishReport(ctx, task)
		}

		in.Tell(store, task.Id, now, rep)
	}

	for id, r := range store {
		project := id == WatchHostKey || id == WatchPullKey
		if !seen[id] && !project && (r.Kind != WatchFinished || now.Sub(r.At) > 24*time.Hour) {
			delete(store, id)
		}
	}
}

// Tell carries the stored report's told keys and catchup record over.
func (in WatchInput) Tell(store map[string]WatchReport, id string, now time.Time, rep *WatchReport) {
	if rep == nil {
		return
	}

	rep.Task, rep.At = id, now

	prev := store[id]
	if prev.Key == rep.Key || slices.Contains(prev.Told, rep.Key) {
		return
	}

	in.Out.Report(*rep)

	rep.Told = append(slices.Clone(prev.Told), rep.Key)
	if len(rep.Told) > watchToldMax {
		rep.Told = rep.Told[len(rep.Told)-watchToldMax:]
	}

	rep.Catchup = prev.Catchup
	store[id] = *rep
}

// ReadTargets fetches origin once, for every task that could be caught up
// or has a catchup to settle. A failed fetch reads nothing: a stale origin
// could hide a move, and a push lease needs the current sha.
func (in WatchInput) ReadTargets(ctx context.Context, tasks []WatchedTask, store map[string]WatchReport) {
	var want []int

	for i, t := range tasks {
		if t.Owner != "" || t.Off || t.HostErr != nil || !t.Listed || t.Agent == agentctl.StatusWorking ||
			WaitingOnAsk(t.Run) {
			continue
		}

		rec := store[t.Id].Catchup
		if rec == nil && (t.Pr == nil || t.Pr.Status != forge.Open) {
			continue
		}

		want = append(want, i)
	}

	if len(want) == 0 {
		return
	}

	if _, err := gitx.Exec(ctx, in.Main, "fetch", "origin"); err != nil {
		in.warnf("no catchup this pass, fetching origin: %v", err)

		return
	}

	for _, i := range want {
		t := &tasks[i]
		t.fetched = true

		t.originSHA, t.targetErr = gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+t.Branch)
		if t.targetErr != nil {
			continue
		}

		t.target, t.targetErr = stack.PRTargetOf(ctx, in.Main, t.Branch)
		t.busy = stack.TreeBusy(ctx, t.Path)
	}
}

func (in WatchInput) warnf(format string, args ...any) {
	if in.Warn != nil {
		in.Warn(fmt.Sprintf(format, args...))
	}
}

// finishReport: a lease someone took meanwhile is no report, they own it now.
func (in WatchInput) finishReport(ctx context.Context, task WatchedTask) *WatchReport {
	err := in.Finish(ctx, task)

	if held := (drives.HeldError{}); errors.As(err, &held) {
		return nil
	}

	url := task.Pr.URL
	if err != nil {
		return &WatchReport{
			Kind: WatchRefused,
			Text: url + " merged, and finishing it was refused: " + err.Error(),
			Next: "f10 finish " + task.Id,
			Key:  WatchRefused + " " + url, // a refusal's text may vary between passes
		}
	}

	return &WatchReport{
		Kind: WatchFinished,
		Text: "finished by the watcher: " + url + " merged, worktree removed, plan archived",
		Key:  WatchFinished + " " + url,
	}
}

// handedBack counts a mark git cannot read as handed back, with a line naming
// it: a finish must never run on a task the user may have taken back.
func (in WatchInput) handedBack(ctx context.Context, branch string) bool {
	v, err := branchmeta.Get(ctx, in.Main, branch, branchmeta.Watch)
	if err != nil {
		if in.Warn != nil {
			in.Warn(fmt.Sprintf("%s skipped this pass: its f10 watch off mark could not be read: %v", branch, err))
		}

		return true
	}

	return v == "false"
}

func (in WatchInput) catchupStep(ctx context.Context, task WatchedTask, store map[string]WatchReport) *WatchReport {
	if !task.fetched || task.Agent == agentctl.StatusWorking || WaitingOnAsk(task.Run) {
		return nil
	}

	if task.targetErr != nil {
		in.warnf("%s skipped this pass, reading what %s targets: %v", task.Id, task.Branch, task.targetErr)

		return nil
	}

	rec := store[task.Id].Catchup
	if rec != nil && !rec.Failed {
		return in.settleCatchup(ctx, task, rec, store)
	}

	return in.sendCatchup(ctx, task, rec, store)
}

// settleCatchup pushes a clean catchup under the task's lease, against the
// origin tip it started from, so a commit pushed meanwhile is never
// overwritten; one not clean with the agent idle is told once and left as
// it is, since an operation in progress there may be someone's hand merge.
func (in WatchInput) settleCatchup(
	ctx context.Context, task WatchedTask, rec *drives.Catchup, store map[string]WatchReport,
) *WatchReport {
	if task.Pr == nil || task.Pr.Status != forge.Open {
		setCatchup(store, task.Id, nil) // its request is gone: nothing to keep current

		return nil
	}

	tree, err := in.Engine.Left(
		ctx,
		in.Main,
		task.Path,
		task.Branch,
		stack.CatchupPlan{Base: rec.Base, SHA: rec.Target},
	)
	reason := tree.Left()
	if err != nil {
		reason = err.Error()
	}

	if reason != "" {
		rec.Failed = true
		in.failedAt(ctx, task, rec)

		return &WatchReport{
			Kind: WatchCatchup,
			Text: fmt.Sprintf("catching %s up with %s did not end clean: %s", task.Branch, rec.Base, reason),
			Next: "/f10:catchup in " + task.Path,
			Key:  WatchCatchup + " failed " + rec.Target + " " + rec.Head,
		}
	}

	lease, err := drives.AcquireAs(in.Root, task.Id, drives.Holder{Watch: true})

	if held := (drives.HeldError{}); errors.As(err, &held) {
		setCatchup(store, task.Id, nil) // whoever holds it pushes

		return nil
	}

	if err != nil {
		return in.pushFailed(task, rec, err)
	}

	defer lease.Release()

	if in.worktreeBusy(ctx, task) {
		return nil
	}

	head, err := gitx.Ref(ctx, task.Path, "HEAD")
	if err != nil {
		return in.pushFailed(task, rec, err)
	}

	how, err := stack.PushCatchup(ctx, task.Path, task.Branch, rec)

	switch moved := (stack.OriginMovedError{}); {
	case errors.As(err, &moved):
		// someone pushed to the request while the catchup ran: the branch
		// here no longer carries it, so a push would drop their commits
		rec.Failed, rec.Head = true, head
		in.failedAt(ctx, task, rec)

		return &WatchReport{
			Kind: WatchCatchup,
			Text: err.Error(),
			Next: "bring origin/" + task.Branch + " into " + task.Path + ", then /f10:catchup there",
			Key:  WatchCatchup + " origin moved " + task.originSHA,
		}
	case errors.Is(err, stack.ErrBaseNotPushed):
		return nil // a later pass, once the base is on origin
	case err != nil:
		return in.pushFailed(task, rec, err)
	}

	setCatchup(store, task.Id, nil)

	if how == "" {
		return nil // nothing to push, or nowhere to push it
	}

	return &WatchReport{
		Kind: WatchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, %s", task.Branch, rec.Base, how),
		Key:  WatchCatchup + " pushed " + head,
	}
}

// failedAt records the fingerprint a failed catchup left, so another goes
// out only once the target or the conflict moves.
func (in WatchInput) failedAt(ctx context.Context, task WatchedTask, rec *drives.Catchup) {
	conflicts, err := gitx.Conflicts(ctx, task.Path, rec.Target, "HEAD")
	if err != nil {
		in.warnf("%s: reading what %s conflicts on: %v", task.Id, task.Branch, err)

		return
	}

	rec.Fingerprint = drives.Fingerprint(rec.Target, stack.ConflictWay(conflicts), "")
}

// pushFailed is told once per cause, the record kept so the next pass retries.
func (in WatchInput) pushFailed(task WatchedTask, rec *drives.Catchup, err error) *WatchReport {
	return &WatchReport{
		Kind: WatchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, and pushing it failed: %v", task.Branch, rec.Base, err),
		Next: "push " + task.Branch + " from " + task.Path + ", with --force-with-lease after a rebase",
		Key:  WatchCatchup + " push failed " + rec.Target + " " + rec.Head,
	}
}

// sendCatchup integrates through the engine and prompts the agent only for
// what git left (a conflict) and for verify, under the task's lease, idle
// and with a clean tree; what it cannot act on is told once. failed, the
// last catchup that did not end clean, blocks another until its
// fingerprint moves.
func (in WatchInput) sendCatchup(
	ctx context.Context, task WatchedTask, failed *drives.Catchup, store map[string]WatchReport,
) *WatchReport {
	pr, aim := task.Pr, task.target
	if pr == nil || pr.Status != forge.Open || task.originSHA == "" || aim.SHA == "" {
		return nil
	}

	behind := func(reason, text string) *WatchReport {
		return &WatchReport{
			Kind: WatchBehind,
			Text: text,
			Next: "/f10:catchup in " + task.Path,
			Key:  WatchBehind + " " + aim.SHA + " " + reason,
		}
	}

	st, err := stack.ReadTarget(ctx, in.Main, aim, pr.Base, task.LocalSHA, task.originSHA)

	switch base := strings.TrimPrefix(aim.Name, "origin/"); {
	case err != nil:
		in.warnf("%s skipped this pass: %v", task.Id, err)

		return nil
	case st.UnpushedBase:
		// a stacked base read from its local branch only: its unpushed
		// commits would ride into this request, so the catchup waits for its push
		return nil
	case st.Retargeted:
		return behind("retargeted", fmt.Sprintf("%s targets %s, not %s, which %s was started on: not caught up",
			pr.URL, pr.Base, base, task.Branch))
	case st.Diverged:
		// a worktree behind origin would rebase a stale branch; one ahead of
		// it holds work the request has not seen
		return behind("diverged", fmt.Sprintf("%s moved past %s, whose tip is not origin's: not caught up",
			aim.Name, task.Branch))
	}

	// a mark already on the branch (a finish that landed its base, a ship guard
	// stopped on a conflict, a failed watcher catchup) is a catchup owed that
	// nobody else sends: set again below
	has, err := gitx.IsAncestor(ctx, in.Main, aim.SHA, "refs/heads/"+task.Branch)

	switch {
	case err != nil:
		in.warnf("%s skipped this pass, reading %s: %v", task.Id, task.Branch, err)

		return nil
	case has:
		return nil
	}

	if task.Ws == "" || in.Prompt == nil {
		return behind("no agent", fmt.Sprintf("%s moved past %s, and no agent is in its workspace to catch it up",
			aim.Name, task.Branch))
	}

	if task.busy != "" {
		return nil // a later pass
	}

	if failed != nil {
		conflicts, err := gitx.Conflicts(ctx, task.Path, aim.SHA, "HEAD")
		if err != nil {
			in.warnf("%s skipped this pass, reading what %s conflicts on: %v", task.Id, task.Branch, err)

			return nil
		}

		if drives.Fingerprint(aim.SHA, stack.ConflictWay(conflicts), "") == failed.Fingerprint {
			return nil
		}
	}

	lease, err := drives.AcquireAs(in.Root, task.Id, drives.Holder{Watch: true})
	if err != nil {
		if held := (drives.HeldError{}); !errors.As(err, &held) {
			in.warnf("%s skipped this pass: %v", task.Id, err)
		}

		return nil
	}

	defer lease.Release()

	if in.worktreeBusy(ctx, task) {
		return nil
	}

	return in.integrate(ctx, task, store, behind)
}

// integrate marks the branch behind its aim, integrates through the
// engine and prompts the agent with what is left; a prompt that cannot go
// out puts the branch back as it was.
func (in WatchInput) integrate(
	ctx context.Context, task WatchedTask, store map[string]WatchReport, behind func(string, string) *WatchReport,
) *WatchReport {
	aim := task.target

	if err := branchmeta.Set(ctx, in.Main, task.Branch, aim.Key, aim.Value); err != nil {
		in.warnf("%s skipped this pass, marking %s behind %s: %v", task.Id, task.Branch, aim.Name, err)

		return nil
	}

	unmark := func() { _ = branchmeta.Unset(ctx, in.Main, task.Branch, aim.Key) } // nobody will clear it

	// readTargets fetched origin for this pass
	rec, err := stack.CatchupRecord(
		ctx,
		in.Main,
		task.Path,
		task.Branch,
		aim.Key,
		in.Now(),
		&stack.FetchOnce{Done: task.fetched},
	)
	if err != nil {
		unmark()
		in.warnf("%s skipped this pass: %v", task.Id, err)

		return nil
	}

	rec.Base = aim.Name

	tasks := []string{task.Id}

	// the review series is read only for a merge, the one method with a message
	message := func(p stack.CatchupPlan) []string {
		return in.Engine.Message(tasks, archive.ChangeOf(in.Storage.Reviews(task.Id)))(p)
	}

	p, stopped, err := in.Engine.Integrate(ctx, in.Engine.Input(in.Main, task.Path), message)
	if err != nil {
		unmark()

		return behind("integrate", fmt.Sprintf("%s moved past %s, and catching it up failed: %v",
			aim.Name, task.Branch, err))
	}

	if !stopped && (p.Method == stack.MethodNone || p.Method == stack.MethodSkip) {
		unmark()

		return nil
	}

	steering := in.Engine.CleanSteering(cmp.Or(p.Base, aim.Name))
	if stopped {
		paths, _ := gitx.Unmerged(ctx, task.Path)
		steering = in.Engine.ConflictSteering(p, task.Branch, "the watcher", paths)
	}

	if err := in.Prompt(
		ctx,
		task,
		Steered(launch.Driven("/f10:catchup")+" && "+watchClause(rec.Head), steering),
	); err != nil {
		in.undo(ctx, task, rec.Head, stopped, p.Method)
		unmark()

		if errors.Is(err, agentctl.ErrAgentNotFound) {
			return behind("no agent", fmt.Sprintf("%s moved past %s, and its agent was not found to catch it up",
				aim.Name, task.Branch))
		}

		return behind("prompt", fmt.Sprintf("%s moved past %s, and prompting its agent failed: %v",
			aim.Name, task.Branch, err))
	}

	setCatchup(store, task.Id, rec)

	return &WatchReport{
		Kind: WatchCatchup,
		Text: fmt.Sprintf("catching %s up with %s, which moved past it", task.Branch, aim.Name),
		Key:  WatchCatchup + " sent " + aim.SHA + " " + rec.Head,
	}
}

// undo puts a branch the watcher integrated back at head when no agent took
// the turn: the stopped merge or rebase is aborted, a clean one reset.
func (in WatchInput) undo(ctx context.Context, task WatchedTask, head string, stopped bool, method string) {
	args := []string{"reset", "--hard", head}

	if stopped {
		args = []string{"rebase", "--abort"}
		if method == stack.MethodMerge {
			args = []string{"merge", "--abort"}
		}
	}

	if _, err := gitx.Exec(ctx, task.Path, args...); err != nil {
		in.warnf("%s: putting %s back at %s: %v", task.Id, task.Branch, gitx.ShortSHA(head), err)
	}
}

// worktreeBusy is recheckFinish's read for a catchup: a working agent or an
// unclean tree waits for the next pass.
func (in WatchInput) worktreeBusy(ctx context.Context, task WatchedTask) bool {
	if in.Agents != nil {
		wss, err := in.Agents.Workspaces(ctx)
		if err != nil {
			return true
		}

		if ws := finish.WorkspaceAt(wss, task.Path); ws != nil && ws.Status == agentctl.StatusWorking {
			return true
		}
	}

	return stack.TreeBusy(ctx, task.Path) != ""
}

// A WatchedTask is what one pass knows about a task with a worktree.
type WatchedTask struct {
	Id       string // the lead
	Branch   string
	Path     string
	Owner    string     // who holds its lease; "" for nobody
	Off      bool       // f10 watch off
	Pr       *forge.PR  // nil with no request, or no host
	HostErr  error      // the host did not answer for its request this pass
	LocalSHA string     // the branch's tip
	Run      *state.Run // the newest run reported from its worktree
	// background is when run's session launched background work still
	// pending, the zero time for none
	background time.Time
	Agent      agentctl.Status // "" with no workspace, or no listing
	Listed     bool            // the workspaces were listed, so "" is no workspace
	Ws         string          // its workspace, for a message
	// what a catchup needs, read after the pass's fetch under watch_catchup
	fetched   bool           // origin was fetched and the fields below read
	originSHA string         // origin's tip; "" when origin lacks the branch
	target    stack.PRTarget // what git says its request targets
	targetErr error          // the target, or origin's tip, could not be read
	busy      string         // why its worktree cannot take a catchup, or ""
}

type WatchReport struct {
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
	Catchup *drives.Catchup `yaml:"catchup,omitempty"`
}

// watchToldMax bounds a task's told keys; the oldest go first.
const watchToldMax = 32

// A Reporter is where a pass tells what it found; the daemon keeps the
// pass's output as the project's watch log.
type Reporter interface {
	Report(WatchReport)
}

type LineReporter struct{ Out io.Writer }

func (l LineReporter) Report(r WatchReport) {
	line := r.Task + " " + r.Kind + ": " + r.Text
	if r.Ask != "" {
		line += "; asks: " + r.Ask
	}

	if r.Next != "" {
		line += " (next: " + r.Next + ")"
	}

	fmt.Fprintln(l.Out, line)
}

// WatchPass reports false once the project has no task worktree and no
// local task branch, so the daemon drops it.
func WatchPass(ctx context.Context, in WatchInput) (bool, error) {
	tasks, branches, err := CollectWatched(ctx, in)
	if err != nil {
		return false, err
	}

	store, err := LoadWatchStore(in.Root)
	if err != nil {
		return false, err
	}

	if in.Catchup {
		in.ReadTargets(ctx, tasks, store)
	}

	in.Reconcile(ctx, tasks, store)
	in.HostTrouble(tasks, store)
	pending := in.RetryPull(ctx, store)

	if err := SaveWatchStore(in.Root, store); err != nil {
		return false, err
	}

	// a branch with no worktree is the janitor's, which only finish runs;
	// a deferred pull keeps the project until it lands
	return len(tasks) > 0 || (in.Mode == "finish" && (branches > 0 || pending)), nil
}

// WatchPullKey is the store's entry for a deferred pull, about the project,
// not one task.
const WatchPullKey = "pull"

// WatchHostKey is the store's entry for the host not answering, about the
// project, not one task.
const WatchHostKey = "host"

func setCatchup(store map[string]WatchReport, id string, rec *drives.Catchup) {
	r := store[id]
	r.Catchup = rec
	store[id] = r
}

// DecideWatch answers the request first, then a stalled agent, then a
// stopped run.
func DecideWatch(t WatchedTask, mode string, now time.Time) (*WatchReport, bool) {
	// a host that did not answer leaves the request unknown: saying
	// anything else about the task this pass could hide a merge
	if t.Owner != "" || t.Off || t.HostErr != nil {
		return nil, false
	}

	if p := t.Pr; p != nil {
		switch p.Status {
		case forge.Merged:
			if t.LocalSHA != "" && t.LocalSHA != p.HeadSHA {
				return &WatchReport{
					Kind: WatchMerged,
					Text: p.URL + " merged, and " + t.Branch + " has commits it lacks",
					Next: "look at " + t.Path + " before f10 finish " + t.Id,
					Key:  WatchMerged + " " + p.URL + " " + t.LocalSHA,
				}, false
			}

			// closing the workspace ends the agent's conversation: never
			// while someone is using it, or while nobody can tell
			act := mode == "finish" && t.Listed && t.Agent != agentctl.StatusWorking

			return &WatchReport{
				Kind: WatchMerged,
				Text: p.URL + " merged",
				Next: "f10 finish " + t.Id,
				Key:  WatchMerged + " " + p.URL,
			}, act
		case forge.Closed:
			return &WatchReport{
				Kind: WatchClosed,
				Text: p.URL + " closed without a merge",
				Next: "f10 finish " + t.Id + " --local, or reopen it",
				Key:  WatchClosed + " " + p.URL,
			}, false
		}
	}

	run := t.Run
	if run == nil {
		return nil, false
	}

	updated := run.Updated.UTC().Format(time.RFC3339)

	for _, phase := range []string{"ship", "plan"} {
		if Stalled(t.Agent, run, phase, time.Time{}, now.Sub(run.Updated), watchStallGrace, t.background, now) {
			return &WatchReport{
				Kind: WatchStalled,
				Text: fmt.Sprintf("agent idle while its run says %s %s", phase, run.PhaseText(phase)),
				Next: "answer it in workspace " + cmp.Or(t.Ws, t.Path),
				Key:  WatchStalled + " " + updated,
			}, false
		}
	}

	if !driverec.StoppedRun(run) && run.Ship != "done" {
		return nil, false
	}

	text := runStopText(run)
	if run.Note != "" {
		text += ": " + run.Note
	}

	return &WatchReport{
		Kind: WatchStopped,
		Text: text,
		Next: run.Next,
		Ask:  run.Ask,
		Key:  WatchStopped + " " + updated,
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

func CollectWatched(ctx context.Context, in WatchInput) ([]WatchedTask, int, error) {
	def, err := finish.JanitorDefault(ctx, in.Main)
	if err != nil {
		return nil, 0, err
	}

	local := in.Jan
	local.LocalOnly = true

	branches, held, err := finish.TaskBranches(ctx, local, def)
	if err != nil {
		return nil, 0, err
	}

	wts, err := gitx.Worktrees(ctx, in.Main)
	if err != nil {
		return nil, 0, err
	}

	live, err := state.List(in.StateDir, in.Now(), in.TTL)
	if err != nil {
		return nil, 0, err
	}

	var (
		wss    []agentctl.Workspace
		listed bool
	)

	if in.Agents != nil {
		// no listing is no agent status: stalls and finishes wait for a pass
		// that has one
		var err error
		wss, err = in.Agents.Workspaces(ctx)
		listed = err == nil
	}

	j := &finish.Sweeper{In: in.Jan, Def: def}

	var tasks []WatchedTask

	for _, b := range branches {
		path := stack.WorktreeOf(wts, b.Name)
		if !held[b.Name] || path == "" || gitx.SamePath(path, in.Main) {
			continue
		}

		task := WatchedTask{Id: b.Ids[0], Branch: b.Name, Path: path, LocalSHA: b.LocalSHA, Listed: listed}

		if h, ok := drives.Probe(in.Root, task.Id); ok {
			task.Owner = h.String()
		} else {
			task.Off = in.handedBack(ctx, b.Name)
		}

		if task.Owner != "" || task.Off {
			tasks = append(tasks, task)

			continue
		}

		if in.Jan.Host != nil {
			task.Pr, task.HostErr = j.Request(ctx, b)
		}

		if runs := statusview.InRoot(live, path); len(runs) > 0 {
			task.Run = runs[0]
			task.background = backgroundAt(in.StateDir, task.Run)
		}

		if ws := finish.WorkspaceAt(wss, path); ws != nil {
			task.Agent, task.Ws = ws.Status, finish.WorkspaceLabel(ws)
		}

		tasks = append(tasks, task)
	}

	return tasks, len(branches), nil
}

// WatcherFinish holds the lease as the watcher and refuses anything but a
// merged request. It writes no drive finish record: the board would read
// the watcher as a drive.
func WatcherFinish(ctx context.Context, w io.Writer, t *target.Target, main, root string, task WatchedTask) error {
	lease, err := drives.AcquireAs(root, task.Id, drives.Holder{Watch: true})
	if err != nil {
		return err
	}
	defer lease.Release()

	if err := RecheckFinish(ctx, worktree.ReachableAgents(main), main, task); err != nil {
		return err
	}

	r, err := t.Reference(ctx, task.Id)
	if err != nil {
		return err
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	in := finish.InputFor(t, main, h, r)
	in.Branch, in.Flags.MergedOnly = task.Branch, true
	// never processPlace: the self-close path would end the daemon, and its
	// directory may be a worktree since removed, where getwd fails
	in.Cwd = main

	err = finish.Finish(ctx, &buf, in)

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line != "" {
			fmt.Fprintln(w, "  "+line)
		}
	}

	if err == nil {
		RecordAt(ctx, main, finish.FinishedEvent(task.Id, "watch", time.Now()))
	}

	return err
}

// RecheckFinish reads again, under the lease, what the pass read before it:
// an agent that started working, or a commit the PR lacks, keeps the task.
func RecheckFinish(ctx context.Context, b agentctl.Backend, main string, task WatchedTask) error {
	tip, err := gitx.Ref(ctx, main, "refs/heads/"+task.Branch)
	if err != nil {
		return fmt.Errorf("reading %s's tip: %w", task.Branch, err)
	}

	if tip != task.Pr.HeadSHA {
		return fmt.Errorf("%s has commits %s lacks", task.Branch, task.Pr.URL)
	}

	if b == nil {
		return errors.New("no agent status to read, so its workspace stays")
	}

	wss, err := b.Workspaces(ctx)
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}

	if ws := finish.WorkspaceAt(wss, task.Path); ws != nil && ws.Status == agentctl.StatusWorking {
		return fmt.Errorf("its agent in workspace %s is working", finish.WorkspaceLabel(ws))
	}

	return nil
}

func LoadWatchStore(root string) (map[string]WatchReport, error) {
	store := map[string]WatchReport{}

	raw, err := os.ReadFile(filepath.Join(root, WatchFile))
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}

	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}

	if err := yaml.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("watch: %s: %w", WatchFile, err)
	}

	return store, nil
}

func SaveWatchStore(root string, store map[string]WatchReport) error {
	raw, err := yaml.Marshal(store)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	if err := os.WriteFile(filepath.Join(root, WatchFile), raw, 0o600); err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	return nil
}

func OnOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}

// WaitingOnAsk: the next prompt such an idle agent gets should be the
// answer, not a catchup.
func WaitingOnAsk(run *state.Run) bool { return run != nil && run.Ask != "" }
