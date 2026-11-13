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
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
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

// watchFile records what each pass last reported, so a fact is told once.
const watchFile = "watch.yaml"

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
	// watchUnpulled is a pull of the default branch a finish deferred, which the watcher retries
	watchUnpulled = "unpulled"
	// watchCatchup is a catchup the watcher sent, pushed, or did not end clean
	watchCatchup = "catchup"
	// watchBehind is a branch behind its target, and the watcher cannot catch it up
	watchBehind = "behind"
	// watchLeft is a branch behind its target that merges into it clean, left
	// alone so its CI is not restarted for nothing
	watchLeft = "left"
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
	Out    reporter
	Warn   func(string) // a line for the pass's output that is no report; nil drops it
	// Catchup is watch_catchup: on; prompt is nil with no backend to reach
	// an agent
	Catchup bool
	Prompt  func(ctx context.Context, task WatchedTask, text string) error
	// Engine integrates a catchup; storage names the task's review series
	// for a catchup merge's trailers
	Engine  *stack.CatchupEngine
	Storage storage.Root
	// CI is watch_ci: on, which sends a red head to its agent; Checks reads
	// a request's head checks, nil with no host to ask
	CI     bool
	Checks func(context.Context, forge.PR) (forge.HeadChecks, error)
}

// retryPull retries a pull a finish deferred, under watch: finish alone,
// since pulling is an act. A cause already told is not told again.
func (in WatchInput) retryPull(ctx context.Context, store map[string]watchReport) bool {
	if in.Mode != "finish" {
		return false
	}

	line, pending := finish.RetryPendingPull(ctx, in.Root, in.Main)
	if !pending {
		if line != "" {
			in.warnf("%s", line)
		}

		delete(store, watchPullKey)

		return false
	}

	if store[watchPullKey].Key == line {
		return true
	}

	rep := watchReport{Task: watchPullKey, Kind: watchUnpulled, Text: line, Key: line, At: in.Now()}
	in.Out.Report(rep)
	store[watchPullKey] = rep

	return true
}

// hostTrouble is told every pass, naming the tasks left out, since those
// tasks go silent; f10 watch shows it until the host answers again.
func (in WatchInput) hostTrouble(tasks []WatchedTask, store map[string]watchReport) {
	var (
		ids   []string
		cause error
	)

	for _, t := range tasks {
		if t.HostErr != nil {
			ids, cause = append(ids, t.ID), cmp.Or(cause, t.HostErr)
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
		At:   in.Now(),
	}

	in.Out.Report(rep)
	store[watchHostKey] = rep
}

// reconcile forgets tasks gone from the project, except a finish, kept a
// day for f10 watch.
func (in WatchInput) reconcile(ctx context.Context, tasks []WatchedTask, store map[string]watchReport) {
	now := in.Now()
	seen := map[string]bool{}

	for _, task := range tasks {
		seen[task.ID] = true

		if in.Catchup {
			in.tell(store, task.ID, now, in.catchupStep(ctx, task, store))
		}

		in.tell(store, task.ID, now, in.ciStep(ctx, task, store))

		rep, act := decideWatch(task, in.Mode, now)

		if act && in.Finish != nil {
			rep = in.finishReport(ctx, task)
		}

		in.tell(store, task.ID, now, rep)
	}

	for id, r := range store {
		project := id == watchHostKey || id == watchPullKey
		if !seen[id] && !project && (r.Kind != watchFinished || now.Sub(r.At) > 24*time.Hour) {
			delete(store, id)
		}
	}
}

// tell carries the stored report's told keys and catchup record over.
func (in WatchInput) tell(store map[string]watchReport, id string, now time.Time, rep *watchReport) {
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

	rep.Catchup, rep.CI = prev.Catchup, prev.CI
	store[id] = *rep
}

// readTargets fetches origin once, for every task that could be caught up
// or has a catchup to settle. A failed fetch reads nothing: a stale origin
// could hide a move, and a push lease needs the current sha.
func (in WatchInput) readTargets(ctx context.Context, tasks []WatchedTask, store map[string]watchReport) {
	var want []int

	for i, t := range tasks {
		if t.Owner != "" || t.Off || t.HostErr != nil || !t.Listed || t.Agent == agentctl.StatusWorking ||
			waitingOnAsk(t.Run) {
			continue
		}

		rec := store[t.ID].Catchup
		if rec == nil && (t.Pr == nil || t.Pr.Status != forge.Open) {
			continue
		}

		// a catchup in flight is read from git whatever the mark says
		if (rec == nil || rec.Failed) && in.parked(t) {
			continue
		}

		want = append(want, i)
	}

	if len(want) == 0 {
		return
	}

	fetch := &stack.FetchOnce{}
	if err := fetch.Fetch(ctx, in.Main); err != nil {
		in.warnf("no catchup this pass, fetching origin: %v", err)

		return
	}

	for _, i := range want {
		t := &tasks[i]
		t.fetch = fetch

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
func (in WatchInput) finishReport(ctx context.Context, task WatchedTask) *watchReport {
	err := in.Finish(ctx, task)

	if held := (drivestore.HeldError{}); errors.As(err, &held) {
		return nil
	}

	url := task.Pr.URL
	if err != nil {
		return &watchReport{
			Kind: watchRefused,
			Text: url + " merged, and finishing it was refused: " + err.Error(),
			Next: "f10 finish " + task.ID,
			Key:  watchRefused + " " + url, // a refusal's text may vary between passes
		}
	}

	return &watchReport{
		Kind: watchFinished,
		Text: "finished by the watcher: " + url + " merged, worktree removed, plan archived",
		Key:  watchFinished + " " + url,
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

// parked reports an idle agent whose last turn launched background work (a
// verify it may come back to). The mark outlives work that already ended, so
// it never holds back a catchup git shows clean: no catchup is sent under it,
// and one that did not end clean is not failed under it.
func (in WatchInput) parked(t WatchedTask) bool {
	return backgroundPending(t.Background, in.Now())
}

func (in WatchInput) catchupStep(ctx context.Context, task WatchedTask, store map[string]watchReport) *watchReport {
	if task.fetch == nil || task.Agent == agentctl.StatusWorking || waitingOnAsk(task.Run) {
		return nil
	}

	if task.targetErr != nil {
		in.warnf("%s skipped this pass, reading what %s targets: %v", task.ID, task.Branch, task.targetErr)

		return nil
	}

	rec := store[task.ID].Catchup
	if rec != nil && !rec.Failed {
		return in.settleCatchup(ctx, task, rec, store)
	}

	if in.parked(task) {
		return nil
	}

	return in.sendCatchup(ctx, task, rec, store)
}

// settleCatchup pushes a clean catchup under the task's lease, against the
// origin tip it started from, so a commit pushed meanwhile is never
// overwritten; one not clean with the agent idle is told once and left as
// it is, since an operation in progress there may be someone's hand merge.
func (in WatchInput) settleCatchup(
	ctx context.Context, task WatchedTask, rec *drivestore.Catchup, store map[string]watchReport,
) *watchReport {
	if task.Pr == nil || task.Pr.Status != forge.Open {
		setCatchup(store, task.ID, nil) // its request is gone: nothing to keep current

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
		if in.parked(task) {
			return nil // the turn may still come back to it
		}

		rec.Failed = true
		in.failedAt(ctx, task, rec)

		return &watchReport{
			Kind: watchCatchup,
			Text: fmt.Sprintf("catching %s up with %s did not end clean: %s", task.Branch, rec.Base, reason),
			Next: "/f10:catchup in " + task.Path,
			Key:  watchCatchup + " failed " + rec.Target + " " + rec.Head,
		}
	}

	lease, err := drivestore.AcquireAs(in.Root, task.ID, drivestore.Holder{Watch: true})

	if held := (drivestore.HeldError{}); errors.As(err, &held) {
		setCatchup(store, task.ID, nil) // whoever holds it pushes

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

		return &watchReport{
			Kind: watchCatchup,
			Text: err.Error(),
			Next: "bring origin/" + task.Branch + " into " + task.Path + ", then /f10:catchup there",
			Key:  watchCatchup + " origin moved " + task.originSHA,
		}
	case errors.Is(err, stack.ErrBaseNotPushed):
		return nil // a later pass, once the base is on origin
	case err != nil:
		return in.pushFailed(task, rec, err)
	}

	setCatchup(store, task.ID, nil)

	if how == "" {
		return nil // nothing to push, or nowhere to push it
	}

	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, %s", task.Branch, rec.Base, how),
		Key:  watchCatchup + " pushed " + head,
	}
}

// failedAt records the fingerprint a failed catchup left, so another goes
// out only once the target or the conflict moves.
func (in WatchInput) failedAt(ctx context.Context, task WatchedTask, rec *drivestore.Catchup) {
	fp, _, err := stack.ConflictFingerprint(ctx, task.Path, rec.Target)
	if err != nil {
		in.warnf("%s: reading what %s conflicts on: %v", task.ID, task.Branch, err)

		return
	}

	rec.Fingerprint = fp
}

// pushFailed is told once per cause, the record kept so the next pass retries.
func (in WatchInput) pushFailed(task WatchedTask, rec *drivestore.Catchup, err error) *watchReport {
	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("%s caught up with %s, and pushing it failed: %v", task.Branch, rec.Base, err),
		Next: "push " + task.Branch + " from " + task.Path + ", with --force-with-lease after a rebase",
		Key:  watchCatchup + " push failed " + rec.Target + " " + rec.Head,
	}
}

// sendCatchup integrates through the engine and prompts the agent only for
// what git left (a conflict) and for verify, under the task's lease, idle
// and with a clean tree; what it cannot act on is told once. failed, the
// last catchup that did not end clean, blocks another until its
// fingerprint moves.
func (in WatchInput) sendCatchup(
	ctx context.Context, task WatchedTask, failed *drivestore.Catchup, store map[string]watchReport,
) *watchReport {
	pr, aim := task.Pr, task.target
	if pr == nil || pr.Status != forge.Open || task.originSHA == "" || aim.SHA == "" {
		return nil
	}

	behind := func(reason, text string) *watchReport {
		return &watchReport{
			Kind: watchBehind,
			Text: text,
			Next: "/f10:catchup in " + task.Path,
			Key:  watchBehind + " " + aim.SHA + " " + reason,
		}
	}

	st, err := stack.ReadTarget(ctx, in.Main, aim, pr.Base, task.LocalSHA, task.originSHA)

	switch base := strings.TrimPrefix(aim.Name, "origin/"); {
	case err != nil:
		in.warnf("%s skipped this pass: %v", task.ID, err)

		return nil
	case st.UnpushedBase:
		// the watcher's side of stack's UnpushedBase rule: it waits for the push
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
		in.warnf("%s skipped this pass, reading %s: %v", task.ID, task.Branch, err)

		return nil
	case has:
		return nil
	}

	if task.busy != "" {
		return nil // a later pass
	}

	marked, err := branchmeta.Get(ctx, in.Main, task.Branch, aim.Key)
	if err != nil {
		in.warnf("%s skipped this pass, reading %s's %s mark: %v", task.ID, task.Branch, aim.Key, err)

		return nil
	}

	var conflicts []gitx.Conflict

	readConflicts := func() bool {
		if conflicts, err = gitx.Conflicts(ctx, task.Path, aim.SHA, "HEAD"); err != nil {
			in.warnf("%s skipped this pass, reading what %s conflicts on: %v", task.ID, task.Branch, err)

			return false
		}

		return true
	}

	// a failed catchup on record was owed when it went out, and still is
	owes := marked != "" || failed != nil

	// the merge-tree runs only when the mark and the host leave it open
	read := !owes && pr.Mergeable != forge.Behind
	if read && !readConflicts() {
		return nil
	}

	if !catchupOwed(owes, pr.Mergeable, conflicts) {
		return &watchReport{
			Kind: watchLeft,
			Text: fmt.Sprintf("%s is behind %s and merges into it clean: left alone", task.Branch, aim.Name),
			Key:  watchLeft + " " + pr.URL,
		}
	}

	if task.Ws == "" || in.Prompt == nil {
		return behind("no agent", fmt.Sprintf("%s moved past %s, and no agent is in its workspace to catch it up",
			aim.Name, task.Branch))
	}

	if failed != nil {
		if !read && !readConflicts() {
			return nil
		}

		if stack.FingerprintOf(aim.SHA, conflicts) == failed.Fingerprint {
			return nil
		}
	}

	lease, err := drivestore.AcquireAs(in.Root, task.ID, drivestore.Holder{Watch: true})
	if err != nil {
		if held := (drivestore.HeldError{}); !errors.As(err, &held) {
			in.warnf("%s skipped this pass: %v", task.ID, err)
		}

		return nil
	}

	defer lease.Release()

	if in.worktreeBusy(ctx, task) {
		return nil
	}

	return in.integrate(ctx, task, store, behind)
}

// catchupOwed: a catchup's push restarts the PR's CI, so a behind branch gets
// one only when already owed (a mark, a failed catchup), when the host will not
// merge it behind, or when it conflicts.
func catchupOwed(owes bool, mergeable string, conflicts []gitx.Conflict) bool {
	return owes || mergeable == forge.Behind || len(conflicts) > 0
}

// integrate marks the branch behind its aim, integrates through the
// engine and prompts the agent with what is left; a prompt that cannot go
// out puts the branch back as it was.
func (in WatchInput) integrate(
	ctx context.Context, task WatchedTask, store map[string]watchReport, behind func(string, string) *watchReport,
) *watchReport {
	aim := task.target

	if err := branchmeta.Set(ctx, in.Main, task.Branch, aim.Key, aim.Value); err != nil {
		in.warnf("%s skipped this pass, marking %s behind %s: %v", task.ID, task.Branch, aim.Name, err)

		return nil
	}

	unmark := func() { _ = branchmeta.Unset(ctx, in.Main, task.Branch, aim.Key) } // nobody will clear it

	rec, err := stack.CatchupRecord(
		ctx,
		in.Main,
		task.Path,
		task.Branch,
		aim.Key,
		in.Now(),
		task.fetch,
	)
	if err != nil {
		unmark()
		in.warnf("%s skipped this pass: %v", task.ID, err)

		return nil
	}

	rec.Base = aim.Name

	tasks := []string{task.ID}

	// the review series is read only for a merge, the one method with a message
	message := func(p stack.CatchupPlan) []string {
		return in.Engine.Message(tasks, archive.ChangeOf(in.Storage.Reviews(task.ID)))(p)
	}

	ci := in.Engine.Input(in.Main, task.Path)
	ci.Fetched = task.fetch

	p, stopped, err := in.Engine.Integrate(ctx, ci, message)
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
		steered(launch.Driven("/f10:catchup")+" && "+watchClause(rec.Head), steering),
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

	setCatchup(store, task.ID, rec)

	return &watchReport{
		Kind: watchCatchup,
		Text: fmt.Sprintf("catching %s up with %s, which moved past it", task.Branch, aim.Name),
		Key:  watchCatchup + " sent " + aim.SHA + " " + rec.Head,
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
		in.warnf("%s: putting %s back at %s: %v", task.ID, task.Branch, gitx.ShortSHA(head), err)
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

		if ws := worktree.WorkspaceAt(wss, task.Path); ws != nil && ws.Status == agentctl.StatusWorking {
			return true
		}
	}

	return stack.TreeBusy(ctx, task.Path) != ""
}

// A WatchedTask is what one pass knows about a task with a worktree.
type WatchedTask struct {
	ID       string // the lead
	Branch   string
	Path     string
	Owner    string     // who holds its lease; "" for nobody
	Off      bool       // f10 watch off
	Pr       *forge.PR  // nil with no request, or no host
	HostErr  error      // the host did not answer for its request this pass
	LocalSHA string     // the branch's tip
	Run      *state.Run // the newest run reported from its worktree
	// Background is when run's session launched background work still
	// pending, the zero time for none
	Background time.Time
	Agent      agentctl.Status // "" with no workspace, or no listing
	Listed     bool            // the workspaces were listed, so "" is no workspace
	Ws         string          // its workspace, for a message
	// what a catchup needs, read after the pass's fetch under watch_catchup

	// the pass's one fetch, which the catchup reuses; nil until origin was
	// fetched and the fields below read
	fetch     *stack.FetchOnce
	originSHA string         // origin's tip; "" when origin lacks the branch
	target    stack.PRTarget // what git says its request targets
	targetErr error          // the target, or origin's tip, could not be read
	busy      string         // why its worktree cannot take a catchup, or ""
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
	Catchup *drivestore.Catchup `yaml:"catchup,omitempty"`
	// CI is the head checks the watcher last read, kept like Catchup
	CI *watchCI `yaml:"ci,omitempty"`
}

// watchToldMax bounds a task's told keys; the oldest go first.
const watchToldMax = 32

// A reporter is where a pass tells what it found; the daemon keeps the
// pass's output as the project's watch log.
type reporter interface {
	Report(watchReport)
}

type LineReporter struct{ Out io.Writer }

func (l LineReporter) Report(r watchReport) {
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
	tasks, branches, err := collectWatched(ctx, in)
	if err != nil {
		return false, err
	}

	store, err := LoadWatchStore(in.Root)
	if err != nil {
		return false, err
	}

	if in.Catchup {
		in.readTargets(ctx, tasks, store)
	}

	in.reconcile(ctx, tasks, store)
	in.hostTrouble(tasks, store)
	pending := in.retryPull(ctx, store)

	if err := saveWatchStore(in.Root, store); err != nil {
		return false, err
	}

	// a branch with no worktree is the janitor's, which only finish runs;
	// a deferred pull keeps the project until it lands
	return len(tasks) > 0 || (in.Mode == "finish" && (branches > 0 || pending)), nil
}

// watchPullKey is the store's entry for a deferred pull, about the project,
// not one task.
const watchPullKey = "pull"

// watchHostKey is the store's entry for the host not answering, about the
// project, not one task.
const watchHostKey = "host"

func setCatchup(store map[string]watchReport, id string, rec *drivestore.Catchup) {
	r := store[id]
	r.Catchup = rec
	store[id] = r
}

// decideWatch answers the request first, then a stalled agent, then a
// stopped run.
func decideWatch(t WatchedTask, mode string, now time.Time) (*watchReport, bool) {
	// a host that did not answer leaves the request unknown: saying
	// anything else about the task this pass could hide a merge
	if t.Owner != "" || t.Off || t.HostErr != nil {
		return nil, false
	}

	if p := t.Pr; p != nil {
		switch p.Status {
		case forge.Merged:
			if t.LocalSHA != "" && t.LocalSHA != p.HeadSHA {
				return &watchReport{
					Kind: watchMerged,
					Text: p.URL + " merged, and " + t.Branch + " has commits it lacks",
					Next: "look at " + t.Path + " before f10 finish " + t.ID,
					Key:  watchMerged + " " + p.URL + " " + t.LocalSHA,
				}, false
			}

			// closing the workspace ends the agent's conversation: never
			// while someone is using it, or while nobody can tell
			act := mode == "finish" && t.Listed && t.Agent != agentctl.StatusWorking

			return &watchReport{
				Kind: watchMerged,
				Text: p.URL + " merged",
				Next: "f10 finish " + t.ID,
				Key:  watchMerged + " " + p.URL,
			}, act
		case forge.Closed:
			return &watchReport{
				Kind: watchClosed,
				Text: p.URL + " closed without a merge",
				Next: "f10 finish " + t.ID + " --local, or reopen it",
				Key:  watchClosed + " " + p.URL,
			}, false
		}
	}

	run := t.Run
	if run == nil {
		return nil, false
	}

	updated := run.Updated.UTC().Format(time.RFC3339)

	for _, phase := range []string{"ship", "plan"} {
		if stalled(t.Agent, run, phase, time.Time{}, now.Sub(run.Updated), watchStallGrace, t.Background, now) {
			return &watchReport{
				Kind: watchStalled,
				Text: fmt.Sprintf("agent idle while its run says %s %s", phase, run.PhaseText(phase)),
				Next: "answer it in workspace " + cmp.Or(t.Ws, t.Path),
				Key:  watchStalled + " " + updated,
			}, false
		}
	}

	if !drivectl.StoppedRun(run) && run.Ship != "done" {
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

func collectWatched(ctx context.Context, in WatchInput) ([]WatchedTask, int, error) {
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

		task := WatchedTask{ID: b.Ids[0], Branch: b.Name, Path: path, LocalSHA: b.LocalSHA, Listed: listed}

		if h, ok := drivestore.Probe(in.Root, task.ID); ok {
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
			task.Background = backgroundAt(in.StateDir, task.Run)
		}

		if ws := worktree.WorkspaceAt(wss, path); ws != nil {
			task.Agent, task.Ws = ws.Status, worktree.WorkspaceLabel(ws)
		}

		tasks = append(tasks, task)
	}

	return tasks, len(branches), nil
}

// watcherFinishInput is the finish the watcher runs: merged requests only,
// and a bundle's riders closed as the PR body says, since no one is asked.
func watcherFinishInput(t *target.Target, main string, h hostops.Host, r ref.Ref, branch string) finish.FinishInput {
	in := finish.InputFor(t, main, h, r)
	in.Branch, in.Flags.MergedOnly, in.FollowBody = branch, true, true
	// never ProcessPlace: the self-close path would end the daemon, and its
	// directory may be a worktree since removed, where getwd fails
	in.Cwd = main

	return in
}

// WatcherFinish holds the lease as the watcher and refuses anything but a
// merged request. It writes no drive finish record: the board would read
// the watcher as a drive.
func WatcherFinish(ctx context.Context, w io.Writer, t *target.Target, main, root string, task WatchedTask) error {
	lease, err := drivestore.AcquireAs(root, task.ID, drivestore.Holder{Watch: true})
	if err != nil {
		return err
	}
	defer lease.Release()

	if err := recheckFinish(ctx, worktree.ReachableAgents(main), main, task); err != nil {
		return err
	}

	r, err := t.Reference(ctx, task.ID)
	if err != nil {
		return err
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	err = finish.Finish(ctx, &buf, watcherFinishInput(t, main, h, r, task.Branch))

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line != "" {
			fmt.Fprintln(w, "  "+line)
		}
	}

	if err == nil {
		RecordAt(ctx, main, finish.FinishedEvent(task.ID, "watch", time.Now()))
	}

	return err
}

// recheckFinish reads again, under the lease, what the pass read before it:
// an agent that started working, or a commit the PR lacks, keeps the task.
func recheckFinish(ctx context.Context, b agentctl.Backend, main string, task WatchedTask) error {
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

	if ws := worktree.WorkspaceAt(wss, task.Path); ws != nil && ws.Status == agentctl.StatusWorking {
		return fmt.Errorf("its agent in workspace %s is working", worktree.WorkspaceLabel(ws))
	}

	return nil
}

func LoadWatchStore(root string) (map[string]watchReport, error) {
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

func OnOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}

// waitingOnAsk: the next prompt such an idle agent gets should be the
// answer, not a catchup.
func waitingOnAsk(run *state.Run) bool { return run != nil && run.Ask != "" }
