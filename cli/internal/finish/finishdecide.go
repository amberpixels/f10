package finish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Finish runs in the order the refusals need: every refusal before anything
// changes, the report before anything local goes, and the workspace close
// last, since on the self-close path the close ends this process.
func Finish(ctx context.Context, w io.Writer, in FinishInput) error {
	// read before the merge archives the series, which raises the count
	if in.Change == "" {
		in.Change = archive.ChangeOf(in.Storage.Reviews(in.Task.ID))
	}

	d, err := decideFinish(ctx, in)
	if gone := (goneError{}); errors.As(err, &gone) {
		return finishBranchless(ctx, w, in, gone)
	}

	if err != nil {
		return err
	}

	errs := &cleanupErrs{landed: d.kind.landed()}

	l, err := land(ctx, w, in, d, errs)
	if err != nil {
		return err
	}

	return cleanup(ctx, w, in, d, l, errs)
}

// A checkoutPlace is where the task's branch is checked out, which decides
// what cleanup removes.
type checkoutPlace int

const (
	inWorktree     checkoutPlace = iota
	inMainCheckout               // main switches to the default branch, no worktree or workspace goes
	noWorktree                   // a finish that stopped halfway left only the branch
)

// finishDecision is everything decideFinish settled, once nothing can refuse.
type finishDecision struct {
	kind  landingKind
	place checkoutPlace
	name  string // the task's branch
	path  string // its checkout, "" with no worktree
	def   string // the local default branch
	pull  forge.PR
	spike string   // the spike report, "" for none
	dirty []string // a spike's uncommitted experiment
	// the task's workspace and main's; self is this process running in the task's
	ws, mainWS *agentctl.Workspace
	self       bool
	loreNote   string // a failing lore check under lore_gate: warn
}

// stays names what a failure before the removal keeps for the rerun.
func (d finishDecision) stays() string {
	if d.place == noWorktree {
		return "the branch stays"
	}

	return "the worktree and branch stay"
}

// decideFinish runs every refusal, so nothing has changed when one fires.
// A branch with no worktree and a merged PR completes a finish that stopped
// halfway; no branch at all returns the goneError for finishBranchless.
func decideFinish(ctx context.Context, in FinishInput) (finishDecision, error) {
	name, path, onMain, err := FinishTarget(ctx, in)
	if gone := (goneError{}); errors.As(err, &gone) && gone.branch != "" {
		return decideGone(ctx, in, gone)
	}

	if err != nil {
		return finishDecision{}, err
	}

	d := finishDecision{name: name, path: path, place: inWorktree}
	if onMain {
		d.place = inMainCheckout
	}

	// a spike's worktree holds its experiment uncommitted, which the close
	// discards; main's tree may hold anything, so it stays refused
	d.spike = TaskSpike(path, in.Storage, in.Task.ID)

	switch {
	case in.Flags.Keep && d.spike == "":
		return d, fmt.Errorf("--keep keeps a spike's experiment, and %s has no spike report", in.Task.ID)
	case d.spike == "" || onMain:
		if err := refuseDirty(ctx, path); err != nil {
			return d, err
		}
	}

	wss, err := worktree.Agents(in.Main).Workspaces(ctx)
	if err != nil {
		return d, err
	}

	if !onMain {
		d.ws, d.mainWS = WorkspaceAt(wss, path), WorkspaceAt(wss, in.Main)
		d.self = gitx.Inside(in.Cwd, path) || (d.ws != nil && in.Workspace != "" && in.Workspace == d.ws.ID)
	}

	if d.self && !in.Flags.Yes {
		return d, fmt.Errorf("f10 finish %s would close its own workspace (%s): "+
			"run it from the main checkout, or pass --yes to close this workspace after the report",
			in.Task.ID, cmp.Or(WorkspaceLabel(d.ws), name))
	}

	if d.def, err = stack.LocalDefault(ctx, in.Main); err != nil {
		return d, err
	}

	d.pull, err = in.Host.PR(ctx, name)

	unpushed, err := neverPushed(ctx, in, name, err)
	if err != nil {
		return d, err
	}

	if in.Flags.MergedOnly && (unpushed || d.pull.Status != forge.Merged) {
		return d, ErrNotMerged
	}

	// a spike with a PR finishes the normal way, and its tree must be clean
	closeSpike := d.spike != "" && unpushed

	if d.spike != "" && !onMain {
		if !closeSpike {
			if err := refuseDirty(ctx, path); err != nil {
				return d, err
			}
		}

		if d.dirty, err = gitx.Status(ctx, path); err != nil {
			return d, err
		}
	}

	if in.Flags.Keep && !closeSpike {
		return d, fmt.Errorf("--keep keeps a spike's experiment, and %s has a PR: finish it the normal way", in.Task.ID)
	}

	var leave bool

	if !unpushed {
		if leave, err = in.leaveOpen(ctx, d.pull); err != nil {
			return d, err
		}
	}

	switch {
	case closeSpike:
		d.kind = landingSpike

		return d, nil
	case leave:
		d.kind = landingLeftOpen

		return d, refuseUnpushed(ctx, in.Main, name)
	case unpushed:
		d.kind = landingUnpushed
	default:
		d.kind = landingPull
	}

	// before anything leaves the machine: a block refuses with nothing changed
	if d.loreNote, err = checkLore(in, unpushed || d.pull.Status == forge.Open); err != nil {
		return d, err
	}

	return d, refuseRiders(in)
}

// decideGone takes over a branch with no worktree only when its PR merged;
// anything else keeps the refusal, which guards branches start never made.
func decideGone(ctx context.Context, in FinishInput, gone goneError) (finishDecision, error) {
	p, err := in.Host.PR(ctx, gone.branch)
	if err != nil || p.Status != forge.Merged {
		return finishDecision{}, gone.err
	}

	def, err := stack.LocalDefault(ctx, in.Main)
	if err != nil {
		return finishDecision{}, err
	}

	d := finishDecision{kind: landingGone, place: noWorktree, name: gone.branch, def: def, pull: p}

	return d, refuseRiders(in)
}

// refuseRiders stops a bundle's merge until the riders it closes are known.
func refuseRiders(in FinishInput) error {
	if len(in.Task.Riders()) > 0 && in.Close == nil {
		return RidersError{task: in.Task}
	}

	return nil
}

// cleanupErrs collects the failures after a merge: the merge has landed, so
// each step runs on. A rerun completes what failed only while the branch
// exists, so a failure before its removal keeps it. Before a landing (a
// spike's close, a PR left open) check returns the error and the run stops.
type cleanupErrs struct {
	landed bool
	failed []string // "<step>: <error>"
	// keep, when set, says what stays and why
	keep string
	// removed: the branch is gone, so a rerun reads the task as finished
	removed bool
}

// check returns err before a landing, and collects it after, under step
// unless the error names its step itself (step "").
func (a *cleanupErrs) check(step string, err error) error {
	if err == nil || !a.landed {
		return err
	}

	if step != "" {
		err = fmt.Errorf("%s failed: %w", step, err)
	}

	a.failed = append(a.failed, err.Error())

	return nil
}

func (a *cleanupErrs) done(id, ended string) error {
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
func appendClosed(rows []render.Fact, in FinishInput, unpushed bool) []render.Fact {
	if len(in.Close) == 0 || !in.HostCloses || unpushed {
		return rows
	}

	return append(rows, render.Fact{Label: "closed", Value: strings.Join(in.Close, ", ")})
}

func (in FinishInput) step(action string) {
	if in.Progress != nil {
		in.Progress(action)
	}
}

func (in FinishInput) sweepNow(ctx context.Context) {
	if in.Sweep != nil {
		in.Sweep(ctx)
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
func (in FinishInput) leaveOpen(ctx context.Context, p forge.PR) (bool, error) {
	switch {
	case p.Status == forge.Merged:
		return false, nil
	case in.Flags.LeaveOpen:
		return true, nil
	case in.Flags.Merge || p.Status != forge.Open:
		return false, nil
	}

	author := p.Author
	if author == "" {
		return false, nil
	}

	me, err := in.Host.Login(ctx)
	if err != nil {
		return false, fmt.Errorf("telling whose PR %s is: %w", p.URL, err)
	}

	if !strings.EqualFold(author, me) {
		return false, ForeignPullError{Url: p.URL, Author: author}
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

func AppendNote(notes []string, note string) []string {
	if note == "" {
		return notes
	}

	return append(notes, note)
}

// FinishTarget is the task's branch and the checkout holding it, main's or a
// worktree's. A branch checked out nowhere is a refusal: finish cleans up
// only what start made.
func FinishTarget(ctx context.Context, in FinishInput) (string, string, bool, error) {
	name, path, err := in.Branch, "", error(nil)
	if name == "" {
		name, path, err = stack.TaskCheckout(ctx, in.Main, in.Driver, in.Task, in.Suffix)
	} else if wts, werr := gitx.Worktrees(ctx, in.Main); werr != nil {
		err = werr
	} else {
		path = stack.WorktreeOf(wts, name)
	}

	switch {
	case err != nil:
		return "", "", false, err
	case name == "":
		return "", "", false, goneError{fmt.Errorf("%s has no branch here: nothing to finish", in.Task.ID), ""}
	case path == "":
		return "", "", false, goneError{
			fmt.Errorf("branch %s has no worktree here: finish removes only what start created", name),
			name,
		}
	}

	return name, path, gitx.SamePath(path, in.Main), nil
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

func WorkspaceAt(wss []agentctl.Workspace, path string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].Path != "" && gitx.SamePath(wss[i].Path, path) {
			return &wss[i]
		}
	}

	return nil
}

func WorkspaceByID(wss []agentctl.Workspace, id string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].ID == id {
			return &wss[i]
		}
	}

	return nil
}

func WorkspaceLabel(ws *agentctl.Workspace) string {
	if ws == nil {
		return ""
	}

	return ws.Label
}
