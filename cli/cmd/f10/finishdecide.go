package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

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

// appendClosed adds the riders the merge closed; a tracker that is not the
// host closes nothing on merge.
func appendClosed(rows []fact, in finishInput, local bool) []fact {
	if len(in.close) == 0 || in.driver != nil || local {
		return rows
	}

	return append(rows, fact{label: "closed", value: strings.Join(in.close, ", ")})
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
