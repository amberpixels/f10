package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// The finish verb is start's inverse. Nothing local moves until the merge
// commit is in the local default branch, and the plan leaves the worktree
// before the worktree goes: by hand, those are the steps that get skipped.
func finishCommand() *cli.Command {
	return &cli.Command{
		Name: "finish",
		Usage: "finish a task: merge its PR (or a branch never pushed, here), archive the plan, remove the worktree, " +
			"close the workspace, pull main; a spike with no PR closes without a merge",
		ArgsUsage: "[<task-id>[-suffix] | <any id of a bundle>]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "when run from inside the task's own workspace, close it after the report instead of refusing",
			},
			&cli.BoolFlag{Name: "merge", Usage: "merge the PR even when someone else opened it"},
			&cli.BoolFlag{
				Name:  "local",
				Usage: "clean up the worktree, branch and workspace and leave the PR open: a review handed back",
			},
			&cli.BoolFlag{
				Name:  "keep",
				Usage: "for a spike, commit the experiment onto a kept/ branch instead of discarding it",
			},
			&cli.StringFlag{
				Name: "close",
				Usage: "for a bundle, the riders the PR delivers, closed with the lead: `IDS` (comma-separated), " +
					"all or none; the rest stay open",
			},
		},
		Action: runFinish,
	}
}

// finishRef is the task a token names, its suffix, and for a bare call the
// branch checked out here, which picks it among suffixed siblings. The
// session's task is refused: nothing else would stand between it and a merge.
func finishRef(ctx context.Context, t *target, token string) (ref.Ref, string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		r, err := t.reference(ctx, "")
		if err != nil {
			return ref.Ref{}, "", "", err
		}

		if r.Origin == ref.OriginSession {
			return ref.Ref{}, "", "", fmt.Errorf("the branch names no task: name it (f10 finish %s)", r.Label())
		}

		return r, "", gitx.Out(ctx, t.dir, "rev-parse", "--abbrev-ref", "HEAD"), nil
	}

	id, suffix := ref.SplitSuffix(token)
	r, err := t.reference(ctx, id)

	return r, suffix, "", err
}

var errNotMerged = errors.New("its request has not merged on the host, and only merged work is finished here")

// exitForeign is finish's exit when the PR is someone else's and no flag
// says what to do with it: the finish skill asks, and reruns with one.
const exitForeign = 3

func runFinish(ctx context.Context, cmd *cli.Command) error {
	if cmd.Bool("merge") && cmd.Bool("local") {
		return errors.New("--merge and --local are exclusive: one merges the PR, the other leaves it open")
	}

	if cmd.Bool("keep") && (cmd.Bool("merge") || cmd.Bool("local")) {
		return errors.New("--keep keeps a spike's experiment, which has no PR to --merge or leave --local")
	}

	if err := agents("").Available(); err != nil {
		return err
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	r, suffix, branch, err := finishRef(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if suffix != "" && r.Bundle != nil {
		return fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
	}

	// any member finishes the whole bundle, found by its lead
	r = r.Head()

	var closing []string

	if v := strings.TrimSpace(cmd.String("close")); v != "" {
		if len(r.Riders()) == 0 {
			return fmt.Errorf("--close names riders, and %s ships alone", r.ID)
		}

		resolve := func(tok string) (ref.Ref, error) { return t.reference(ctx, tok) }
		if closing, err = closeChoice(r, v, resolve); err != nil {
			return err
		}
	}

	// drive's own finish runs under the lease drive holds, so only the
	// command takes it
	lease, err := finishLease(t.lay.StorageRoot, r.ID)
	if err != nil {
		return err
	}
	defer lease.Release()

	in := finishInputFor(t, cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot), h, r)
	if in.cwd, in.workspace, err = processPlace(); err != nil {
		return err
	}

	in.suffix, in.branch = suffix, branch
	in.yes, in.merge, in.local, in.keep = cmd.Bool("yes"), cmd.Bool("merge"), cmd.Bool("local"), cmd.Bool("keep")
	in.close = closing
	in.sweep = func(ctx context.Context) {
		sweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.lay.StorageRoot, janitorStamp), time.Now(),
			func(ctx context.Context) ([]string, error) { return janitor(ctx, janitorFor(t)) })
	}

	err = finish(ctx, cmd.Writer, in)

	if fp := (foreignPullError{}); errors.As(err, &fp) {
		return cli.Exit("f10: "+fp.Error(), exitForeign)
	}

	if re := (ridersError{}); errors.As(err, &re) {
		return cli.Exit("f10: "+re.Error(), exitRiders)
	}

	return err
}

func finishLease(root, id string) (*drives.Lease, error) {
	lease, err := drives.Acquire(root, id, 0)
	if held := (drives.HeldError{}); errors.As(err, &held) {
		if held.Holder.Drive > 0 {
			return nil, fmt.Errorf("%w: f10 drive cancel %d stops it, or let it finish", held, held.Holder.Drive)
		}

		return nil, fmt.Errorf("%w: wait for it to end", held)
	}

	return lease, err
}

// A foreignPullError is a refusal: nothing changed, the choice is the user's.
type foreignPullError struct {
	url    string
	author string
}

func (e foreignPullError) Error() string {
	return fmt.Sprintf("%s was opened by %s: pass --merge to merge it, "+
		"or --local to clean up here and leave it open", e.url, e.author)
}

// finishInput is everything finish needs once the target is settled, so
// the flow can be tested without a checkout.
type finishInput struct {
	main   string         // the main checkout: host and git commands run here
	root   string         // the shared storage root, for the pull's lease; "" takes none
	driver *driver.Driver // nil without one
	task   ref.Ref
	change string // the lore change this merge closes; finish fills it from the task's series
	suffix string
	branch string // a bare call's own branch, "" to look it up by task and suffix
	host   host
	method string // the merge method project.md declares, "" for none
	traces bool   // traces: on; the squash or merge commit's body ends in the task's trailers
	// hostIssues: the tracker is the host's issues, which a commit's closing keyword closes
	hostIssues bool
	// loreStore feeds the report's lore line and the pre-merge check (nil reads
	// nothing); gate is lore_gate: warn notes a failing check, block refuses
	loreStore *lore.Store
	gate      string
	cwd       string // where the command runs: inside the worktree is the self-close case
	workspace string // HERDR_WORKSPACE_ID: the workspace this process runs in, or ""
	yes       bool
	merge     bool   // merge the PR even when someone else opened it
	local     bool   // leave the PR open and clean up only what is here
	keep      bool   // a spike's experiment is committed onto a kept/ branch, not discarded
	plansDir  string // the plans dir every checkout shares; the spikes dir sits beside it
	// title reads the task's title for a squash merged here, nil for none
	title func(context.Context) (string, error)
	// close is the riders the PR delivers, closed with the lead; nil, no
	// --close answer yet, refuses a bundle's merge
	close []string
	// sweep runs the janitor before the workspace closes, since a self-close
	// ends the process; nil for none
	sweep func(context.Context)
	// progress names each action as it starts, for drive's record; nil for none
	progress func(string)
	// handoff takes the lines a merge prints under its report, for drive's report; nil for none
	handoff func([]string)
	// deferred takes a deferred pull's note, for drive's report; nil for none
	deferred func(string)
	// mergedOnly refuses all but an already merged request: the watcher never merges
	mergedOnly bool
	// subtasks is every subtask, closed ones kept, for a parent with no branch;
	// nil, like errNoParentLink, is a tracker with no parent link
	subtasks func(context.Context, ref.Ref) ([]childRow, error)
	// closeTask closes a parent its subtasks delivered; nil where the tracker is
	// not the host, which f10 has no "done" word for
	closeTask func(context.Context, ref.Ref) error
}

// finishInputFor fills what comes from the task and the project's settings,
// so the command, the drive and the watcher all take the same ones; each
// caller then sets only its own fields.
func finishInputFor(t *target, main string, h host, task ref.Ref) finishInput {
	task = task.Head()

	return finishInput{
		main:       main,
		root:       t.lay.StorageRoot,
		driver:     t.driver(),
		task:       task,
		host:       h.at(main),
		method:     t.eff.MergeMethod,
		traces:     t.eff.Traces,
		hostIssues: t.hostIssues(),
		loreStore:  &lore.Store{Root: loreRootFor(t)},
		gate:       t.eff.LoreGate,
		plansDir:   t.sharedPlansDir(),
		title:      titleLookup(t, task.Number),
		subtasks:   t.tracker().allChildren,
		closeTask:  closeTaskFor(t, h.at(main)),
	}
}

// closeTaskFor treats a driver as settleRiders does: not the host, so nil.
func closeTaskFor(t *target, h host) func(context.Context, ref.Ref) error {
	if t.driver() != nil || !t.hostIssues() {
		return nil
	}

	return func(ctx context.Context, task ref.Ref) error { return h.closeIssue(ctx, task.Number) }
}

// processPlace is this process's directory and workspace, for finish's
// self-close check.
func processPlace() (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}

	return cwd, os.Getenv("HERDR_WORKSPACE_ID"), nil
}

// finishedEvent records a finish that ran in this process, where no hook
// sees it; by names who ran it, "" for the drive.
func finishedEvent(id, by string, at time.Time) ledger.Event {
	p := ledger.Payload{"args": []any{id}}
	if by != "" {
		p["by"] = by
	}

	return ledger.Event{
		At:      at,
		Task:    id,
		Kind:    ledger.TaskFinished,
		Source:  ledger.Observed,
		Payload: ledger.NewPayload(p),
	}
}

// finish runs the flow in the order the refusals need: the report prints
// before anything local goes, and the workspace closes last, since on the
// self-close path the close ends this process.
func finish(ctx context.Context, w io.Writer, in finishInput) error {
	// read before the merge archives the series, which raises the count
	if in.change == "" {
		in.change = changeOf(taskReviews(in.main, in.plansDir, in.task.ID))
	}

	name, path, onMain, err := finishTarget(ctx, in)
	if gone := (goneError{}); errors.As(err, &gone) {
		return finishGone(ctx, w, in, gone)
	}

	if err != nil {
		return err
	}

	// a spike's worktree holds its experiment uncommitted, which the close
	// discards; main's tree may hold anything, so it stays refused
	spike := taskSpike(path, in.plansDir, in.task.ID)

	switch {
	case in.keep && spike == "":
		return fmt.Errorf("--keep keeps a spike's experiment, and %s has no spike report", in.task.ID)
	case spike == "" || onMain:
		if err := refuseDirty(ctx, path); err != nil {
			return err
		}
	}

	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return err
	}

	var ws, mainWS *agentctl.Workspace

	var self bool

	if !onMain {
		ws, mainWS = workspaceAt(wss, path), workspaceAt(wss, in.main)
		self = inside(in.cwd, path) || (ws != nil && in.workspace != "" && in.workspace == ws.ID)
	}

	if self && !in.yes {
		return fmt.Errorf("f10 finish %s would close its own workspace (%s): "+
			"run it from the main checkout, or pass --yes to close this workspace after the report",
			in.task.ID, cmp.Or(workspaceLabel(ws), name))
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return err
	}

	p, err := in.host.pull(ctx, name)

	local, err := neverPushed(ctx, in, name, err)
	if err != nil {
		return err
	}

	if in.mergedOnly && (local || p.status() != pullMerged) {
		return errNotMerged
	}

	// a spike with a PR finishes the normal way, and its tree must be clean
	closeSpike := spike != "" && local

	var dirty []string

	if spike != "" && !onMain {
		if !closeSpike {
			if err := refuseDirty(ctx, path); err != nil {
				return err
			}
		}

		if dirty, err = gitx.Status(ctx, path); err != nil {
			return err
		}
	}

	if in.keep && !closeSpike {
		return fmt.Errorf("--keep keeps a spike's experiment, and %s has a PR: finish it the normal way", in.task.ID)
	}

	var leave bool

	if !local {
		if leave, err = in.leaveOpen(ctx, p); err != nil {
			return err
		}
	}

	var (
		rows  []fact
		notes []string
		ended = p.url() + " merged"
		// a merge ends the series; a PR left open or a spike's close only
		// moves what rounds there were
		end *seriesEnd
	)

	if local {
		ended = name + " merged into " + def
	}

	var (
		kept string
		// set only on a merge, since a PR left open or a spike's close ships nothing
		hand  []string
		after = &afterMerge{}
	)

	switch {
	case closeSpike:
		ended = "the spike " + in.task.ID + " closed"

		if in.keep {
			kept = keptName(ctx, in.main, name, in.task.Members())
		}

		released, err := releaseDependents(ctx, in.main, name, def, false)
		if err != nil {
			return err
		}

		rows = []fact{taskFact(ctx, w, in.main, in.task.Label())}
		if kept != "" {
			rows = append(rows, fact{label: "branch", value: kept})
		}

		notes = append(spikeNotes(ctx, in, name, def, kept, dirty), released...)
	case leave:
		if err := refuseUnpushed(ctx, in.main, name); err != nil {
			return err
		}

		ended = p.url() + " left open"
		rows = []fact{taskFact(ctx, w, in.main, in.task.ID), {label: "pr", value: p.url(), url: true}}
		notes = append(notes, fmt.Sprintf("%s is left %s: nothing merged, pulled or deleted on origin",
			p.url(), p.status()))
	default:
		// before anything leaves the machine: a block refuses with nothing changed
		var loreNote string

		if loreNote, err = checkLore(in, local || p.status() == pullOpen); err != nil {
			return err
		}

		var riderNotes []string

		if len(in.task.Riders()) > 0 {
			if in.close == nil {
				return ridersError{task: in.task}
			}

			if !local {
				if riderNotes, err = settleRiders(ctx, in, name, p.status() == pullMerged); err != nil {
					return err
				}
			}
		}

		// read before the merge, while the branch tip it was written at still exists
		hand = handoffLines(taskHandoff(in.main, in.plansDir, in.task.ID), in.task.ID, branchTip(ctx, in.main, name, p))

		var sha string

		recordLanded := sizeRecorder(ctx, in, name, def, local)

		if local {
			in.step("merging " + name + " into " + def)

			if sha, notes, err = landLocal(ctx, in, name, def); err != nil {
				return err
			}
		} else {
			in.step("merging " + pullNumber(p.url()))

			if p, notes, err = landPull(ctx, in, name, def, p); err != nil {
				return err
			}

			notes = append(notes, riderNotes...)

			// main stays on the task's branch until the end, so a failure before
			// then leaves a rerun the same shape to find
			in.step("pulling " + def)

			note, deferred := pullMain(ctx, in.root, in.main, def, p.sha())
			if !onMain || deferred {
				notes = appendNote(notes, note)
			}

			if deferred && in.deferred != nil {
				in.deferred(note)
			}

			sha = p.sha()
		}

		end = &seriesEnd{pr: "none", head: sha}
		if !local {
			end.pr = pullNumber(p.url())
		}

		notes = appendNote(notes, loreNote)

		recordLanded()

		// the merge has landed: a failing step from here is collected and the
		// rest still run, so a rerun finds only what is left
		after.on = true

		released, err := releaseDependents(ctx, in.main, name, def, true)
		if err := after.check("releasing dependents", err); err != nil {
			return err
		}

		notes = append(notes, released...)

		rows = []fact{taskFact(ctx, w, in.main, in.task.Label())}
		if !local {
			rows = append(rows, fact{label: "pr", value: p.url(), url: true})
		}

		rows = append(rows, fact{label: "merge", value: sha})

		rows = appendClosed(rows, in, local)
	}

	in.step("archiving the plan")

	archived, note, err := archivePlan(path, in.main, in.plansDir, in.task.ID)
	if err := after.check("archiving the plan", err); err != nil {
		return err
	}

	if err != nil {
		// a branch gone with its plan live reads as no task to a rerun, and a
		// plan in the worktree's own .f10/ would go with it
		after.keep = "the worktree and branch stay, since the plan is not archived"
	}

	notes = appendNote(notes, note)
	notes = appendNote(notes, removeImages(in.main, in.plansDir, in.task.ID))

	handoffSrc, handed := handoffTarget(in.main, in.plansDir, in.task.ID)

	var reviews string

	if end != nil {
		reviews, err = closeSeries(taskReviews(in.main, in.plansDir, in.task.ID), *end)
	} else {
		reviews, err = archiveReviews(taskReviews(in.main, in.plansDir, in.task.ID), seriesEnd{})
	}

	if err := after.check("archiving the reviews", err); err != nil {
		return err
	}

	notes = appendNote(notes, seriesLore(&in, end))

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived, path: true})
	}

	if handed != "" && hand != nil {
		rows = append(rows, fact{label: "handoff", value: handed, path: true})
	}

	if reviews != "" {
		rows = append(rows, fact{label: "reviews", value: reviews})
	}

	// the report is copied out before the worktree goes and moved only after
	// it, so a rerun after a failed removal still finds the spike
	var spikeDst string

	early := spike != "" && !onMain && inside(spike, path)

	if spike != "" {
		spikeDst, err = spikeArchiveName(in.main, in.plansDir, in.task.ID)
		if err == nil && early {
			err = copySpike(spike, spikeDst)
		}

		if err := after.check("archiving the spike report", err); err != nil {
			return err
		}

		if err != nil {
			// removing the worktree would delete the report's only copy
			after.keep = "the worktree and branch stay, since the spike report is not archived"
		} else {
			rows = append(rows, fact{label: "spike", value: spikeDst, path: true})
		}
	}

	archiveLate := func() error {
		if spike == "" || early || spikeDst == "" {
			return nil
		}

		return archiveSpike(spike, spikeDst)
	}

	switch {
	case onMain:
		notes = append(notes, fmt.Sprintf("%s is checked out in the main checkout at %s: after this report "+
			"main switches to %s and the branch is deleted; no worktree removed, no workspace closed",
			name, in.main, def))
	case ws != nil:
		rows = append(rows, fact{label: "workspace", value: ws.ID})
	default:
		notes = append(notes, "no "+agents(in.main).Name()+" workspace shows the worktree, so nothing to close")
	}

	rows = append(rows, fact{label: "path", value: path})

	if self {
		notes = append(notes, "this workspace closes after this report")
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if err := writeHandoff(w, hand); err != nil {
		return err
	}

	if err := after.check("", moveHandoff(handoffSrc, handed)); err != nil {
		return fmt.Errorf("%s, but %w", ended, err)
	}

	if in.handoff != nil && hand != nil {
		in.handoff(hand)
	}

	// a rerun finds the task by its branch, so a step that failed keeps it
	if after.keep == "" && len(after.failed) > 0 {
		after.keep = "the worktree and branch stay for the rerun"
	}

	if after.keep != "" {
		return after.done(in.task.ID, ended)
	}

	if onMain {
		if err := after.check("", switchMain(ctx, in.main, def, name, kept)); err != nil {
			return fmt.Errorf("%s, but %w", ended, err)
		}

		after.removed = true

		if err := after.check("archiving the spike report", archiveLate()); err != nil {
			return fmt.Errorf("%s, but archiving the spike report failed: %w", ended, err)
		}

		finishRecords(w, in, name)
		in.sweepNow(ctx)

		return after.done(in.task.ID, ended)
	}

	// last: removing the checkout deletes the branch, and the record with it
	in.step("removing worktree")

	err = removeTaskCheckout(ctx, in, path, name, kept, dirty, closeSpike)
	if err := after.check("removing the worktree", err); err != nil {
		return err
	}

	after.removed = err == nil

	if err := after.check("archiving the spike report", archiveLate()); err != nil {
		return fmt.Errorf("%s, but archiving the spike report failed: %w", ended, err)
	}

	// before the close, which ends the process on the self-close path
	finishRecords(w, in, name)
	in.sweepNow(ctx)

	// closing its own workspace would end this process before the error prints
	if self && len(after.failed) > 0 {
		after.keep = "this workspace stays open"

		return after.done(in.task.ID, ended)
	}

	if self && mainWS != nil {
		err := agents(in.main).Focus(ctx, mainWS.ID)
		if err := after.check("focusing the main workspace", err); err != nil {
			return err
		}
	}

	if ws != nil {
		err := agents(in.main).Close(ctx, ws.ID)
		if err := after.check("closing workspace "+ws.ID, err); err != nil {
			return err
		}
	}

	return after.done(in.task.ID, ended)
}

// afterMerge collects the failures of the steps after a merge: the merge
// has landed, so each step runs on. A rerun completes what failed only while
// the branch exists, so a failure before its removal keeps it.
type afterMerge struct {
	on     bool     // the merge landed
	failed []string // "<step>: <error>"
	// keep, when set, says what stays and why
	keep string
	// removed: the branch is gone, so a rerun reads the task as finished
	removed bool
}

// check returns err before the merge, and collects it after, under step
// unless the error names its step itself (step "").
func (a *afterMerge) check(step string, err error) error {
	if err == nil || !a.on {
		return err
	}

	if step != "" {
		err = fmt.Errorf("%s failed: %w", step, err)
	}

	a.failed = append(a.failed, err.Error())

	return nil
}

func (a *afterMerge) done(id, ended string) error {
	if len(a.failed) == 0 {
		return nil
	}

	msg := ended + ", but " + strings.Join(a.failed, "; ")
	if a.keep != "" {
		msg += "; " + a.keep
	}

	if a.removed {
		return fmt.Errorf("%s: the task is finished, so settle what failed by hand", msg)
	}

	return fmt.Errorf("%s: rerun f10 finish %s to complete", msg, id)
}

// sizeRecorder measures the branch while it still differs from its base and
// returns what records it, run only once the merge has landed.
func sizeRecorder(ctx context.Context, in finishInput, branch, def string, local bool) func() {
	size, sized := measureBranch(ctx, in.main, branch, def, local)
	if !sized || size.files == 0 {
		return func() {}
	}

	return func() { recordSize(ctx, in.main, branch, in.task.ID, size) }
}

// appendClosed adds the riders the merge closed; a tracker that is not the
// host closes nothing on merge.
func appendClosed(rows []fact, in finishInput, local bool) []fact {
	if len(in.close) == 0 || in.driver != nil || local {
		return rows
	}

	return append(rows, fact{label: "closed", value: strings.Join(in.close, ", ")})
}

// seriesLore sets in.change to the lore change the closed series names, then
// returns the report's lore line ("" with no store).
func seriesLore(in *finishInput, end *seriesEnd) string {
	if end != nil {
		if change, ok := closedChange(taskReviews(in.main, in.plansDir, in.task.ID), *end); ok {
			in.change = change
		}
	}

	if in.loreStore == nil {
		return ""
	}

	return loreNote(in.loreStore, in.loreChange())
}

// finishRecords marks the task finished in every stopped drive record that
// lists it, so drive list needs no rerun. A failure is a line: the merge
// already landed.
func finishRecords(w io.Writer, in finishInput, branch string) {
	if in.root == "" {
		return
	}

	recs, err := drives.List(in.root)
	if err != nil {
		fmt.Fprintf(w, "  marking drive records: %v\n", err)

		return
	}

	for _, rec := range recs {
		match := attemptRows(in, branch, rec.Tasks)
		if !slices.ContainsFunc(rec.Tasks, func(t drives.Task) bool { return !rowDone(t) && match(t) }) {
			continue
		}

		if err := markFinished(in.root, rec.Number, match); err != nil {
			fmt.Fprintf(w, "  marking drive %d: %v\n", rec.Number, err)
		}
	}
}

// attemptRows matches the rows of the attempt finish acted on, since one
// attempt finished leaves the task's others alive. A bare finish, or one
// that found the lone <id>-* branch, types no suffix: the branch's ending
// names the attempt, the same way branchesFor finds it.
func attemptRows(in finishInput, branch string, rows []drives.Task) func(drives.Task) bool {
	ofTask := func(row string) (string, bool) {
		id, suffix := ref.SplitSuffix(row)

		return suffix, strings.EqualFold(id, in.task.Label()) ||
			slices.ContainsFunc(in.task.Members(), func(m string) bool { return strings.EqualFold(id, m) })
	}

	acted := in.suffix
	if acted == "" {
		for _, t := range rows {
			if suffix, ok := ofTask(t.ID); ok && suffix != "" &&
				strings.HasSuffix(strings.ToLower(branch), "-"+strings.ToLower(suffix)) {
				acted = suffix
			}
		}
	}

	return func(t drives.Task) bool {
		suffix, ok := ofTask(t.ID)

		return ok && strings.EqualFold(suffix, acted)
	}
}

// checkLore runs only right before a merge lands (a merged request, --local
// and a spike's close merge nothing). An unreadable store is a reason; under
// block it is the error, before anything changed.
func checkLore(in finishInput, merging bool) (string, error) {
	if in.loreStore == nil || !merging {
		return "", nil
	}

	in.step("checking lore")

	var reasons string

	switch res, err := in.loreStore.Check(in.loreChange()); {
	case err != nil:
		reasons = err.Error()
	case res.Pass():
		return "", nil
	default:
		reasons = strings.Join(res.Reasons, "; ")
	}

	if in.gate == "block" {
		return "", fmt.Errorf(
			"lore check %s: %s (lore_gate: block): add the entries, or declare lore_gate: warn to merge anyway",
			in.loreChange(),
			reasons,
		)
	}

	return fmt.Sprintf("lore check %s: %s (lore_gate: warn): merged anyway", in.loreChange(), reasons), nil
}

// loreChange is the change the merge closes; a finishInput built without
// finish (a test, mergeBody alone) reads the task id.
func (in finishInput) loreChange() string {
	return cmp.Or(in.change, in.task.ID)
}

func (in finishInput) step(action string) {
	if in.progress != nil {
		in.progress(action)
	}
}

func (in finishInput) sweepNow(ctx context.Context) {
	if in.sweep != nil {
		in.sweep(ctx)
	}
}

// switchMain ends a finish on the main checkout; each error names what
// already happened to the PR (ended) and what is left to do by hand.
func switchMain(ctx context.Context, main, def, branch, kept string) error {
	if err := gitx.Switch(ctx, main, def); err != nil {
		return fmt.Errorf("switching the main checkout to %s failed: %w", def, err)
	}

	if kept != "" {
		if err := keepBranch(ctx, main, branch, kept); err != nil {
			return fmt.Errorf("main is on %s, and %w", def, err)
		}

		return nil
	}

	if err := gitx.DeleteBranch(ctx, main, branch); err != nil {
		return fmt.Errorf("main is on %s, and deleting %s failed (git branch -D %s): %w",
			def, branch, branch, err)
	}

	return nil
}

// leaveOpen reports whether finish leaves the PR as it is. A merged PR always
// takes the full path; someone else's open one, with neither flag, is a
// refusal that changes nothing; a closed one is left only with --local.
func (in finishInput) leaveOpen(ctx context.Context, p pull) (bool, error) {
	switch {
	case p.status() == pullMerged:
		return false, nil
	case in.local:
		return true, nil
	case in.merge || p.status() != pullOpen:
		return false, nil
	}

	author := p.author()
	if author == "" {
		return false, nil
	}

	me, err := in.host.Login(ctx)
	if err != nil {
		return false, fmt.Errorf("telling whose PR %s is: %w", p.url(), err)
	}

	if !strings.EqualFold(author, me) {
		return false, foreignPullError{url: p.url(), author: author}
	}

	return false, nil
}

// refuseUnpushed stops a cleanup that leaves the PR open while the branch
// has commits origin's copy lacks: deleting the branch would lose them.
func refuseUnpushed(ctx context.Context, main, branch string) error {
	pushed, err := gitx.IsAncestor(ctx, main, "refs/heads/"+branch, "refs/remotes/origin/"+branch)
	if err != nil {
		return fmt.Errorf("could not tell whether origin/%s has every commit of %s: %w", branch, branch, err)
	}

	if pushed {
		return nil
	}

	return fmt.Errorf("%s has commits origin/%s does not: push or drop them first, "+
		"since leaving the PR open deletes the local branch", branch, branch)
}

func appendNote(notes []string, note string) []string {
	if note == "" {
		return notes
	}

	return append(notes, note)
}

// finishTarget is the task's branch and the checkout holding it, main's or a
// worktree's. A branch checked out nowhere is a refusal: finish cleans up
// only what start made.
func finishTarget(ctx context.Context, in finishInput) (string, string, bool, error) {
	name, path, err := in.branch, "", error(nil)
	if name == "" {
		name, path, err = taskCheckout(ctx, in.main, in.driver, in.task, in.suffix)
	} else if wts, werr := gitx.Worktrees(ctx, in.main); werr != nil {
		err = werr
	} else {
		path = worktreeOf(wts, name)
	}

	switch {
	case err != nil:
		return "", "", false, err
	case name == "":
		return "", "", false, goneError{fmt.Errorf("%s has no branch here: nothing to finish", in.task.ID), ""}
	case path == "":
		return "", "", false, goneError{
			fmt.Errorf("branch %s has no worktree here: finish removes only what start created", name),
			name,
		}
	}

	return name, path, gitx.SamePath(path, in.main), nil
}

// refuseDirty has no --force: the work is the user's to commit or drop.
func refuseDirty(ctx context.Context, path string) error {
	lines, err := gitx.Status(ctx, path)
	if err != nil {
		return err
	}

	if len(lines) == 0 {
		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "worktree %s has uncommitted changes, commit or discard them first:", path)

	for _, l := range lines {
		b.WriteString("\n  " + l)
	}

	return errors.New(b.String())
}

func workspaceAt(wss []agentctl.Workspace, path string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].Path != "" && gitx.SamePath(wss[i].Path, path) {
			return &wss[i]
		}
	}

	return nil
}

func workspaceByID(wss []agentctl.Workspace, id string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].ID == id {
			return &wss[i]
		}
	}

	return nil
}

func workspaceLabel(ws *agentctl.Workspace) string {
	if ws == nil {
		return ""
	}

	return ws.Label
}

// landPull merges an open request or confirms a merged one. Requests based
// on the branch move to the merged one's base before the branch goes, since
// the delete would close them; the notes say which moved.
func landPull(ctx context.Context, in finishInput, name, def string, p pull) (pull, []string, error) {
	var notes []string

	switch p.status() {
	case pullOpen:
		if base := p.base(); base != "" && base != def {
			return pull{}, nil, fmt.Errorf(
				"%s is stacked on %s, not %s: finish the task that branch belongs to first, "+
					"and its finish retargets this PR to %s",
				p.url(),
				base,
				def,
				def,
			)
		}

		method, note, err := in.host.mergeMethod(ctx, in.method)
		if err != nil {
			return pull{}, nil, err
		}

		notes = appendNote(notes, note)

		// settleRiders may have just rewritten the description's closing
		// keywords, and the body under the commit must be the host's current one
		description := p.body()
		if in.traces {
			var note string

			if description, note, err = lastState(ctx, in, name, description); err != nil {
				return pull{}, nil, err
			}

			notes = appendNote(notes, note)
		}

		if p, err = in.host.mergePull(ctx, p, name, method, mergeBody(in, description, method)); err != nil {
			return pull{}, nil, err
		}
	case pullMerged:
		notes = append(notes, p.url()+" was already merged")
	default:
		return pull{}, nil, fmt.Errorf("%s is %s without a merge: finish only finishes merged or mergeable work; "+
			"--local cleans up here and leaves it %s", p.url(), p.status(), p.status())
	}

	if p.status() != pullMerged || p.sha() == "" {
		return pull{}, nil, fmt.Errorf("%s reports %s as %s with no merge commit after the merge",
			in.host.Name, p.url(), p.status())
	}

	exists, err := gitx.RemoteBranchExists(ctx, in.main, name)
	if err != nil {
		return pull{}, nil, err
	}

	if exists {
		moved, err := retargetStacked(ctx, in.host, p, name, cmp.Or(p.base(), def))
		if err != nil {
			return pull{}, nil, err
		}

		notes = append(notes, moved...)

		if err := in.host.deleteRemoteBranch(ctx, name); err != nil {
			return pull{}, nil, fmt.Errorf("%s merged, but deleting its remote branch failed: %w", p.url(), err)
		}
	}

	// the local copy of origin's branch outlives the API's delete, and a
	// task with any branch left reads as not finished to drive and start
	if err := gitx.DeleteRemoteRef(ctx, in.main, name); err != nil {
		notes = append(notes, fmt.Sprintf("origin/%s is gone but its local copy is not: %v", name, err))
	}

	return p, notes, nil
}

// retargetStacked moves every open request based on branch to base. A
// failure stops before the delete, so the request stays open for a rerun;
// its error names the requests already moved, since no report follows it.
func retargetStacked(ctx context.Context, h host, merged pull, branch, base string) ([]string, error) {
	stacked, err := h.stackedPulls(ctx, branch)
	if err != nil {
		return nil, fmt.Errorf("%s merged, but listing the requests based on %s failed, "+
			"so origin/%s is left in place: %w", merged.url(), branch, branch, err)
	}

	var notes, moved []string

	for _, s := range stacked {
		if err := h.retargetPull(ctx, s, base); err != nil {
			if len(moved) > 0 {
				err = fmt.Errorf("%w (already retargeted: %s)", err, strings.Join(moved, ", "))
			}

			return nil, fmt.Errorf("%s merged, but retargeting %s to %s failed, "+
				"so origin/%s is left in place to keep it open: %w", merged.url(), s.url(), base, branch, err)
		}

		moved = append(moved, s.url())
		notes = append(notes, fmt.Sprintf("retargeted %s to %s", s.url(), base))
	}

	return notes, nil
}

// lastState sets the PR body's state block to finish, returning the body the
// host holds. A failed read or write is a note, except for a bundle, whose
// fallback body would close a rider settleRiders just kept open.
func lastState(ctx context.Context, in finishInput, branch, fallback string) (string, string, error) {
	body, err := in.host.pullBody(ctx, branch)

	switch {
	case err != nil && len(in.task.Riders()) > 0:
		return "", "", err
	case err != nil:
		body = fallback
	default:
		body, err = putLastState(ctx, in, branch, body)
	}

	return body, stateNote(err), nil
}

func putLastState(ctx context.Context, in finishInput, branch, body string) (string, error) {
	out, err := trace.Rewrite(body, trace.Update{
		Step:   "finish",
		Task:   in.task.ID,
		Riders: in.task.Riders(),
		After:  branchAfter(ctx, in.main, branch),
	})
	if err != nil || out == body {
		return body, err
	}

	if err := in.host.editPullBody(ctx, branch, out); err != nil {
		return body, err
	}

	return out, nil
}

func stateNote(err error) string {
	if err == nil {
		return ""
	}

	return "the PR body's state block was not rewritten: " + err.Error()
}

// mergeBody is the host commit's body: the description minus its state
// block, then a Task: line per member, so a squash still names what it
// shipped. A rebase, or glab's plain merge (""), keeps the branch's trailers.
func mergeBody(in finishInput, description, method string) string {
	if !in.traces || (method != "--squash" && method != "--merge") {
		return ""
	}

	trailers, err := trace.Render(trace.Trailers{Tasks: in.task.Members()})
	if err != nil {
		return ""
	}

	trailers += changeTrailer(in.task.ID, in.loreChange())

	if body := strings.TrimSpace(trace.StripState(description)); body != "" {
		return body + "\n\n" + trailers
	}

	return trailers
}

// changeTrailer names a later series' lore change, since a squash drops the
// branch's own trailers and Task: alone leads lore why to the first PR.
func changeTrailer(task, change string) string {
	if change == "" || change == task {
		return ""
	}

	return lore.TrailerChange + ": " + change + "\n"
}

// mainPullKey is the lease every finish pulling the main checkout takes, so
// two finishes at once (a drive's, the watcher's, the command) never race on
// origin's refs.
const mainPullKey = "main-pull"

// mainPullWait bounds the wait for another finish's pull.
const mainPullWait = 2 * time.Minute

// pullMain brings the local default branch up to the merge. The merge has
// landed on origin by then, so a pull that fails defers to a later retry
// (retryPendingPull) and comes back as a note naming the cause; it never
// rebases or resets the branch.
func pullMain(ctx context.Context, root, main, def, sha string) (string, bool) {
	note, err := pullDefault(ctx, root, main, def, sha)
	if err == nil {
		clearPendingPull(ctx, root, main, def)

		return note, false
	}

	note = deferredPullNote(err, main, def)

	if root == "" {
		return note + "; nothing records the retry here, so pull it by hand", true
	}

	if err := os.WriteFile(filepath.Join(root, pendingPullFile), []byte(def+" "+sha+"\n"), 0o600); err != nil {
		note += fmt.Sprintf("; recording the retry failed (%v), so pull it by hand", err)
	}

	return note, true
}

// errPullBusy is another finish holding the pull's lease past mainPullWait.
var errPullBusy = errors.New("another finish held the pull")

func pullDefault(ctx context.Context, root, main, def, sha string) (string, error) {
	if root != "" {
		lease, err := drives.Await(ctx, root, mainPullKey, drives.Holder{}, mainPullWait)
		if err != nil {
			return "", fmt.Errorf("%w: %w", errPullBusy, err)
		}
		defer lease.Release()
	}

	var note string

	if gitx.CurrentBranch(ctx, main) == def {
		if err := gitx.PullFF(ctx, main); err != nil {
			return "", err
		}
	} else {
		if err := gitx.FetchInto(ctx, main, def); err != nil {
			return "", err
		}

		note = fmt.Sprintf("%s updated by fetch: the main checkout at %s is on another branch and was left there",
			def, main)
	}

	merged, err := gitx.IsAncestor(ctx, main, sha, def)
	if err != nil {
		return "", fmt.Errorf("could not tell whether merge commit %s is in %s: %w", sha, def, err)
	}

	if !merged {
		return "", fmt.Errorf("merge commit %s is not in %s after pulling", sha, def)
	}

	return note, nil
}

// deferredPullNote names why the pull failed and what settles it.
func deferredPullNote(err error, main, def string) string {
	const retry = "; f10 retries the pull"

	switch {
	case gitx.Diverged(err):
		// the switch is spelled out: the main checkout may be on another branch
		return fmt.Sprintf("%s not pulled: the local %s has commits origin/%s lacks; "+
			"settle it with git -C %s switch %s && git -C %s rebase origin/%s, or push them%s",
			def, def, def, main, def, main, def, retry)
	case gitx.StaleRef(err):
		return fmt.Sprintf("%s not pulled: a stale origin ref is in the way; "+
			"settle it with git -C %s remote prune origin%s", def, main, retry)
	case gitx.RefLocked(err), errors.Is(err, errPullBusy):
		return fmt.Sprintf("%s not pulled: another git held origin's refs%s, "+
			"or run git -C %s pull --ff-only", def, retry, main)
	default:
		return fmt.Sprintf("%s not pulled: %v; settle it with git -C %s pull --ff-only%s", def, err, main, retry)
	}
}

// pendingPullFile, in the storage root every checkout shares, records a
// deferred pull as "<default branch> <merge sha>". Only the newest sha
// matters, since origin's branch holds every earlier merge too.
const pendingPullFile = "pending-pull"

func readPendingPull(root string) (string, string) {
	if root == "" {
		return "", ""
	}

	b, err := os.ReadFile(filepath.Join(root, pendingPullFile))
	if err != nil {
		return "", ""
	}

	def, sha, _ := strings.Cut(strings.TrimSpace(string(b)), " ")

	return def, sha
}

// clearPendingPull drops the record once its merge is in the branch.
func clearPendingPull(ctx context.Context, root, main, def string) {
	branch, sha := readPendingPull(root)
	if branch != def || sha == "" {
		return
	}

	if in, err := gitx.IsAncestor(ctx, main, sha, def); err == nil && in {
		_ = os.Remove(filepath.Join(root, pendingPullFile)) // a retry that finds it gone has nothing to do
	}
}

// retryPendingPull retries a pull a finish deferred. It reports "" with no
// pull pending, else what happened, and whether the pull is still pending.
func retryPendingPull(ctx context.Context, root, main string) (string, bool) {
	def, sha := readPendingPull(root)
	if def == "" || sha == "" {
		return "", false
	}

	note, deferred := pullMain(ctx, root, main, def, sha)
	if deferred {
		return note, true
	}

	return "pulled " + def + " up to " + shortSHA(sha) + ", which a finish had deferred", false
}

// releaseDependents clears other branches' `start --after` dependency on the
// finished one and marks them landed: their pr must target the default
// branch now, and their next ship or catchup must bring it in first. merged
// says the finished branch's code reached the default branch.
func releaseDependents(ctx context.Context, dir, branch, def string, merged bool) ([]string, error) {
	entries, err := gitx.ConfigEntries(ctx, dir, `^branch\..*\.`+cfgAfterBranch+`$`)
	if err != nil {
		return nil, fmt.Errorf("reading which branches depend on %s: %w", branch, err)
	}

	var notes []string

	for _, e := range entries {
		if e.Value != branch {
			continue
		}

		dependent := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+cfgAfterBranch)
		section := "branch." + dependent + "."

		base, err := gitx.Config(ctx, dir, section+cfgAfter)
		if err != nil {
			return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
		}

		if err := releaseTip(ctx, dir, dependent, branch, merged); err != nil {
			return nil, err
		}

		for _, key := range []string{cfgAfter, cfgAfterBranch} {
			if err := gitx.UnsetConfig(ctx, dir, section+key); err != nil {
				return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
			}
		}

		if err := gitx.SetConfig(ctx, dir, section+cfgLanded, cmp.Or(base, branch)); err != nil {
			return nil, fmt.Errorf("marking %s landed on %s: %w", dependent, def, err)
		}

		notes = append(notes, fmt.Sprintf("%s depended on %s: the dependency is cleared and its pr targets %s now; "+
			"/f10:catchup, or its next ship unless catchup is off, brings %s in first", dependent, branch, def, def))
	}

	return notes, nil
}

// releaseTip records the dependent's base tip before the branch is deleted,
// the last time it can be read. A base that never merged (a closed spike)
// leaves none: an onto from it would drop the base's code from the dependent.
func releaseTip(ctx context.Context, dir, dependent, branch string, merged bool) error {
	if merged {
		return recordTip(ctx, dir, dependent, branch)
	}

	key := "branch." + dependent + "." + cfgTip

	tip, err := gitx.Config(ctx, dir, key)
	if err != nil || tip == "" {
		return err
	}

	if err := gitx.UnsetConfig(ctx, dir, key); err != nil {
		return fmt.Errorf("clearing %s's base tip on %s: %w", dependent, branch, err)
	}

	return nil
}

// archivePlan moves the plan into the shared archive. A missing plan is a
// note, since a task shipped without one still needs cleaning up; one that
// cannot move is an error, since removing the worktree would delete it.
func archivePlan(worktree, main, plansDir, id string) (string, string, error) {
	plan := taskPlan(worktree, plansDir, id)
	src, own := filepath.Dir(plan), filepath.Join(worktree, ".f10", "plans")
	dst := filepath.Join(main, ".f10", "plans", "archive")

	if plansDir != "" {
		dst = filepath.Join(plansDir, "archive")
	}

	if !exists(plan) {
		return "", fmt.Sprintf("no plan at %s, nothing archived", plan), nil
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	// a worktree's own archived versions move first, so the current plan
	// takes the highest number
	if gitx.SamePath(src, own) && !gitx.SamePath(worktree, main) {
		older, _ := filepath.Glob(filepath.Join(src, "archive", id+".*.md"))
		for _, o := range older {
			if err := os.Rename(o, freeArchiveName(dst, id)); err != nil {
				return "", "", fmt.Errorf("archiving the prior plan at %s: %w", o, err)
			}
		}
	}

	target := freeArchiveName(dst, id)
	if err := os.Rename(plan, target); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	return target, "", nil
}

// archiveReviews moves a findings directory to reviews/archive/<id>.<N>/,
// so the task's next PR starts its own series of rounds. N always counts
// from 1, since a bare <id>/ under archive would read as a live directory.
func archiveReviews(dir string, end seriesEnd) (string, error) {
	if !exists(dir) {
		return "", nil
	}

	archive := filepath.Join(filepath.Dir(dir), "archive")
	if err := os.MkdirAll(archive, 0o750); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	target := nextSeries(archive, filepath.Base(dir))
	if err := os.Rename(dir, target); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	return target, end.write(target)
}

// closeSeries ends the task's series even when it had no review rounds,
// since the archive is what counts a task's PRs for its lore change; a series
// already closed with the same PR or head (finish run again) adds none.
func closeSeries(dir string, end seriesEnd) (string, error) {
	if exists(dir) {
		return archiveReviews(dir, end)
	}

	archive := filepath.Join(filepath.Dir(dir), "archive")
	for _, n := range seriesNumbers(archive, filepath.Base(dir)) {
		if end.same(readSeriesEnd(filepath.Join(archive, fmt.Sprintf("%s.%d", filepath.Base(dir), n)))) {
			return "", nil
		}
	}

	target := nextSeries(archive, filepath.Base(dir))
	if err := os.MkdirAll(target, 0o750); err != nil {
		return "", fmt.Errorf("closing the review series at %s: %w", dir, err)
	}

	return "", end.write(target)
}

// A seriesEnd is what an archived series records in its closed file: the PR
// (#204, or none), the commit it ended at, and when. A restart is rounds
// ended by a rebase before any PR existed: still the same PR, so it counts
// for no lore change.
type seriesEnd struct {
	pr, head string
	at       time.Time
	restart  bool
}

const seriesClosedFile = "closed"

func (e seriesEnd) write(dir string) error {
	at := e.at
	if at.IsZero() {
		at = time.Now()
	}

	body := fmt.Sprintf("pr: %s\nhead: %s\nclosed: %s\n",
		cmp.Or(e.pr, "none"), e.head, at.UTC().Format(time.RFC3339))
	if e.restart {
		body += "restart: yes\n"
	}

	if err := os.WriteFile(filepath.Join(dir, seriesClosedFile), []byte(body), 0o600); err != nil {
		return fmt.Errorf("recording the end of the review series at %s: %w", dir, err)
	}

	return nil
}

// same compares by PR number, so gh's #7 and glab's !7 match, else by head.
func (e seriesEnd) same(o seriesEnd) bool {
	if n := strings.TrimLeft(e.pr, "#!"); digits(n) && n == strings.TrimLeft(o.pr, "#!") {
		return true
	}

	return e.head != "" && e.head == o.head
}

// readSeriesEnd is zero for a series archived before the file existed.
func readSeriesEnd(dir string) seriesEnd {
	raw, err := os.ReadFile(filepath.Join(dir, seriesClosedFile))
	if err != nil {
		return seriesEnd{}
	}

	var e seriesEnd

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, _ := strings.Cut(line, ":")

		switch value = strings.TrimSpace(value); key {
		case "pr":
			e.pr = value
		case "head":
			e.head = value
		case "closed":
			e.at, _ = time.Parse(time.RFC3339, value)
		case "restart":
			e.restart = value == "yes"
		}
	}

	return e
}

// seriesNumbers is the archived series of id, ascending.
func seriesNumbers(archive, id string) []int {
	entries, _ := os.ReadDir(archive)

	var out []int

	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), id+".")
		if n, err := strconv.Atoi(rest); ok && e.IsDir() && err == nil && n > 0 {
			out = append(out, n)
		}
	}

	slices.Sort(out)

	return out
}

func nextSeries(archive, id string) string {
	n := 1
	if have := seriesNumbers(archive, id); len(have) > 0 {
		n = have[len(have)-1] + 1
	}

	return filepath.Join(archive, fmt.Sprintf("%s.%d", id, n))
}

// closedPRs is the archived series that each ended a PR, oldest first: a
// restart is left out.
func closedPRs(dir string) []string {
	archive, id := filepath.Join(filepath.Dir(dir), "archive"), filepath.Base(dir)

	var out []string

	for _, n := range seriesNumbers(archive, id) {
		path := filepath.Join(archive, fmt.Sprintf("%s.%d", id, n))
		if !readSeriesEnd(path).restart {
			out = append(out, path)
		}
	}

	return out
}

// changeOf is the lore change of the task's open series: the task id for its
// first PR, <id>.<n> for its n-th, counted from the PRs archived.
func changeOf(dir string) string {
	return seriesChange(filepath.Base(dir), len(closedPRs(dir))+1)
}

// closedChange is the change of the PR end closed, when the newest archived
// PR is that one: a finish run again after its merge reports on it, not on
// the next series.
func closedChange(dir string, end seriesEnd) (string, bool) {
	closed := closedPRs(dir)
	if len(closed) == 0 || !end.same(readSeriesEnd(closed[len(closed)-1])) {
		return "", false
	}

	return seriesChange(filepath.Base(dir), len(closed)), true
}

func seriesChange(id string, n int) string {
	if n <= 1 {
		return id
	}

	return fmt.Sprintf("%s.%d", id, n)
}

// seriesStart is when the task's open series began, the newest archived
// PR's end; zero for a first series.
func seriesStart(dir string) time.Time {
	closed := closedPRs(dir)
	if len(closed) == 0 {
		return time.Time{}
	}

	newest := closed[len(closed)-1]
	if e := readSeriesEnd(newest); !e.at.IsZero() {
		return e.at
	}

	return modTime(newest)
}

// taskReviews is the task's findings directory, beside its plans.
func taskReviews(main, plansDir, id string) string {
	return filepath.Join(filepath.Dir(taskPlansDir(main, plansDir)), "reviews", id)
}

// removeImages drops the images `task read` downloaded for the task; a
// failure is a note, since the task is finished either way.
func removeImages(main, plansDir, id string) string {
	dir := filepath.Join(main, ".f10", "images", id)
	if plansDir != "" {
		dir = filepath.Join(filepath.Dir(plansDir), "images", id)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Sprintf("the task's images at %s stay: %v", dir, err)
	}

	return ""
}

func taskPlansDir(checkout, plansDir string) string {
	return cmp.Or(plansDir, filepath.Join(checkout, ".f10", "plans"))
}

// taskPlan falls back to the checkout's own .f10/plans, which a worktree
// from before storage moved to main's still holds.
func taskPlan(checkout, plansDir, id string) string {
	plan := filepath.Join(taskPlansDir(checkout, plansDir), id+".md")
	if own := filepath.Join(checkout, ".f10", "plans", id+".md"); !exists(plan) && exists(own) {
		return own
	}

	return plan
}

// freeArchiveName numbers `<id>.<N>.md` the way steps/plan.md does.
func freeArchiveName(dir, id string) string {
	candidate := filepath.Join(dir, id+".md")
	if !exists(candidate) {
		return candidate
	}

	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s.%d.md", id, n))
		if !exists(candidate) {
			return candidate
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(gitx.Resolved(path), gitx.Resolved(dir))
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
