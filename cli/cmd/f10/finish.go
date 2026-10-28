package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// The finish verb is start's inverse across the host CLI, git and Herdr.
// By hand, the skipped steps (the worktree, the pull) leave sibling
// directories and a main behind its own merge; a worktree from before
// storage moved to main's, removed by hand, takes the only copy of its plan.
//
// Everything after the merge is gated on the merge having landed: the
// merge commit must be in the local default branch before anything local
// is touched. Every refusal comes before the first change, and the plan
// is out of the worktree before the worktree goes. A task shipped from the
// main checkout has no worktree: main is switched to the default branch
// and only the local branch goes.
//
// A PR someone else opened is not merged unasked: --merge merges it, and
// --local cleans up here and leaves it open, for a review handed back.
//
// A spike with no PR is closed without a merge (finishspike.go).
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

// finishRef is the task a finish token names, its suffix, and for an empty
// token the branch checked out here, which picks it among suffixed siblings.
// The session's task is refused: off a task branch no workspace refusal
// stands between a bare call and the merge.
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

	if err := herdr.Available(); err != nil {
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

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	err = finish(ctx, cmd.Writer, finishInput{
		main:      main,
		driver:    t.driver(),
		task:      r,
		suffix:    suffix,
		branch:    branch,
		host:      h.at(main),
		method:    t.eff.MergeMethod,
		traces:    t.eff.Traces,
		cwd:       cwd,
		workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		yes:       cmd.Bool("yes"),
		merge:     cmd.Bool("merge"),
		local:     cmd.Bool("local"),
		keep:      cmd.Bool("keep"),
		plansDir:  t.sharedPlansDir(),
		title:     titleLookup(t, r.Number),
		close:     closing,
		sweep: func(ctx context.Context) {
			sweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.sharedRoot(), janitorStamp), time.Now(),
				func(ctx context.Context) ([]string, error) { return janitor(ctx, janitorFor(t)) })
		},
	})

	if fp := (foreignPullError{}); errors.As(err, &fp) {
		return cli.Exit("f10: "+fp.Error(), exitForeign)
	}

	if re := (ridersError{}); errors.As(err, &re) {
		return cli.Exit("f10: "+re.Error(), exitRiders)
	}

	return err
}

// A foreignPullError is an open PR someone else opened, met with neither
// --merge nor --local: finish changed nothing and the choice is the user's.
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
	main      string         // the main checkout: host and git commands run here
	driver    *driver.Driver // nil without one
	task      ref.Ref
	suffix    string
	branch    string // a bare call's own branch, "" to look it up by task and suffix
	host      host
	method    string // the merge method project.md declares, "" for none
	traces    bool   // traces: on; the squash or merge commit's body ends in the task's trailers
	cwd       string // where the command runs: inside the worktree is the self-close case
	workspace string // HERDR_WORKSPACE_ID: the workspace this process runs in, or ""
	yes       bool
	merge     bool   // merge the PR even when someone else opened it
	local     bool   // leave the PR open and clean up only what is here
	keep      bool   // a spike's experiment is committed onto a kept/ branch, not discarded
	plansDir  string // the plans dir every checkout shares; the spikes dir sits beside it
	// title reads the task's title for a squash merged here, nil for none
	title func(context.Context) (string, error)
	// close is a bundle's riders the PR delivers, closed with the lead; nil
	// until --close answers, which a bundle's merge refuses to go without
	close []string
	// sweep runs the janitor once the task's own cleanup is done and before
	// a workspace closes, since closing this one ends the process; nil for none
	sweep func(context.Context)
	// progress names each action as it starts, for drive's record; nil for none
	progress func(string)
}

// finish runs the flow: branch, worktree, the refusals, the merge, the
// pull, the dependents, the plan, the report, the removal, the workspace.
// A branch never pushed, with no request, is merged into the default branch
// here instead, and nothing is pulled or pushed.
// The report is printed before anything local goes, and the workspace
// closes last: on the self-close path the close ends this process, so
// everything that must happen has happened by then. With the branch checked
// out in main, main's workspace is neither closed nor focused.
func finish(ctx context.Context, w io.Writer, in finishInput) error {
	name, path, onMain, err := finishTarget(ctx, in)
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

	wss, err := herdr.Workspaces(ctx, in.main)
	if err != nil {
		return err
	}

	var ws, mainWS *herdr.Listed

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
	)

	if local {
		ended = name + " merged into " + def
	}

	var kept string

	switch {
	case closeSpike:
		ended = "the spike " + in.task.ID + " closed"

		if in.keep {
			kept = keptName(ctx, in.main, name, in.task.Members())
		}

		released, err := releaseDependents(ctx, in.main, name, def)
		if err != nil {
			return err
		}

		rows = []fact{{label: "task", value: in.task.Label()}}
		if kept != "" {
			rows = append(rows, fact{label: "branch", value: kept})
		}

		notes = append(spikeNotes(ctx, in, name, def, kept, dirty), released...)
	case leave:
		if err := refuseUnpushed(ctx, in.main, name); err != nil {
			return err
		}

		ended = p.url() + " left open"
		rows = []fact{{label: "task", value: in.task.ID}, {label: "pr", value: p.url(), url: true}}
		notes = append(notes, fmt.Sprintf("%s is left %s: nothing merged, pulled or deleted on origin",
			p.url(), p.status()))
	default:
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

		var sha string

		// measured before landing, recorded once it landed; a branch with
		// nothing to measure, as a rerun of a local land finds it, records none
		size, sized := measureBranch(ctx, in.main, name, def, local)
		sized = sized && size.files > 0

		if local {
			in.step("merging " + name + " into " + def)

			if sha, notes, err = landLocal(ctx, in, name, def); err != nil {
				return err
			}

			notes = append(notes, unclosedNote(in))
		} else {
			in.step("merging " + pullNumber(p.url()))

			if p, notes, err = landPull(ctx, in, name, def, p); err != nil {
				return err
			}

			notes = append(notes, riderNotes...)

			// on main, the default branch is checked out nowhere, so pullMain fetches
			// into it and main stays on the task's branch until the end: a failure
			// before then leaves a rerun the same shape to find
			in.step("pulling " + def)

			note, err := pullMain(ctx, in.main, def, p.sha())
			if err != nil {
				return err
			}

			if !onMain {
				notes = appendNote(notes, note)
			}

			sha = p.sha()
		}

		if sized {
			recordSize(ctx, in.main, name, in.task.ID, size)
		}

		released, err := releaseDependents(ctx, in.main, name, def)
		if err != nil {
			return err
		}

		notes = append(notes, released...)

		rows = []fact{{label: "task", value: in.task.Label()}}
		if !local {
			rows = append(rows, fact{label: "pr", value: p.url(), url: true})
		}

		rows = append(rows, fact{label: "merge", value: sha})

		// a tracker that is not the host closes nothing on merge
		if len(in.close) > 0 && in.driver == nil && !local {
			rows = append(rows, fact{label: "closed", value: strings.Join(in.close, ", ")})
		}
	}

	in.step("archiving the plan")

	archived, note, err := archivePlan(path, in.main, in.plansDir, in.task.ID)
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)
	notes = appendNote(notes, removeImages(in.main, in.plansDir, in.task.ID))

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived})
	}

	// a worktree's own report is copied out before the worktree goes, and
	// goes with it; a shared one moves after the removal: either way a rerun
	// after a failed removal still finds the spike
	var spikeDst string

	early := spike != "" && !onMain && inside(spike, path)

	if spike != "" {
		if spikeDst, err = spikeArchiveName(in.main, in.plansDir, in.task.ID); err != nil {
			return err
		}

		if early {
			if err := copySpike(spike, spikeDst); err != nil {
				return err
			}
		}

		rows = append(rows, fact{label: "spike", value: spikeDst})
	}

	archiveLate := func() error {
		if spike == "" || early {
			return nil
		}

		if err := archiveSpike(spike, spikeDst); err != nil {
			return fmt.Errorf("%s, but %w", ended, err)
		}

		return nil
	}

	switch {
	case onMain:
		notes = append(notes, fmt.Sprintf("%s is checked out in the main checkout at %s: after this report "+
			"main switches to %s and the branch is deleted; no worktree removed, no workspace closed",
			name, in.main, def))
	case ws != nil:
		rows = append(rows, fact{label: "workspace", value: ws.ID})
	default:
		notes = append(notes, "no Herdr workspace shows the worktree, so nothing to close")
	}

	rows = append(rows, fact{label: "path", value: path})

	if self {
		notes = append(notes, "this workspace closes after this report")
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if onMain {
		if err := switchMain(ctx, in.main, def, name, kept, ended); err != nil {
			return err
		}

		if err := archiveLate(); err != nil {
			return err
		}

		in.sweepNow(ctx)

		return nil
	}

	// the last action: removing the checkout deletes the branch, and the
	// record with it
	in.step("removing worktree")

	if err := removeTaskCheckout(ctx, in, path, name, kept, dirty, closeSpike); err != nil {
		return err
	}

	if err := archiveLate(); err != nil {
		return err
	}

	in.sweepNow(ctx)

	if self && mainWS != nil {
		if err := herdr.FocusWorkspace(ctx, in.main, mainWS.ID); err != nil {
			return err
		}
	}

	if ws != nil {
		return herdr.CloseWorkspace(ctx, in.main, ws.ID)
	}

	return nil
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

// switchMain ends a finish run on the main checkout: main onto the default
// branch, then the task's branch deleted, or moved to kept when not "".
// Each error names what already happened to the PR (ended: "<url> merged")
// and what is left to do by hand.
func switchMain(ctx context.Context, main, def, branch, kept, ended string) error {
	if err := gitx.Switch(ctx, main, def); err != nil {
		return fmt.Errorf("%s, but switching the main checkout to %s failed: %w", ended, def, err)
	}

	if kept != "" {
		if err := keepBranch(ctx, main, branch, kept); err != nil {
			return fmt.Errorf("%s and main is on %s, but %w", ended, def, err)
		}

		return nil
	}

	if err := gitx.DeleteBranch(ctx, main, branch); err != nil {
		return fmt.Errorf("%s and main is on %s, but deleting %s failed (git branch -D %s): %w",
			ended, def, branch, branch, err)
	}

	return nil
}

// leaveOpen reports whether finish cleans up locally and leaves the PR as
// it is. A merged PR always takes the full path, which only confirms it.
// An open PR someone else opened, with neither flag, is a refusal that
// changes nothing; a closed one is left only with --local.
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
	if gitx.IsAncestor(ctx, main, "refs/heads/"+branch, "refs/remotes/origin/"+branch) {
		return nil
	}

	return fmt.Errorf("%s has commits origin/%s does not: push or drop them first, "+
		"since leaving the PR open deletes the local branch", branch, branch)
}

// appendNote adds note to notes unless it is empty.
func appendNote(notes []string, note string) []string {
	if note == "" {
		return notes
	}

	return append(notes, note)
}

// finishTarget is the task's branch, the checkout that has it checked out,
// and whether that checkout is main's: a worktree start created, or main
// itself for a task shipped from there. A branch checked out nowhere is a
// refusal, since finish cleans up only those two shapes.
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
		return "", "", false, fmt.Errorf("%s has no branch here: nothing to finish", in.task.ID)
	case path == "":
		return "", "", false, fmt.Errorf("branch %s has no worktree here: finish removes only what start created", name)
	}

	return name, path, samePath(path, in.main), nil
}

// refuseDirty stops on anything git would list in the worktree, named file
// by file. There is no --force: the work is the user's to commit or drop.
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

// workspaceAt is the workspace showing the checkout at path, or nil.
func workspaceAt(wss []herdr.Listed, path string) *herdr.Listed {
	for i := range wss {
		if wss[i].Path != "" && samePath(wss[i].Path, path) {
			return &wss[i]
		}
	}

	return nil
}

// workspacesAt is every workspace showing the checkout at path, in Herdr's
// order: more than one when something opened the checkout twice.
func workspacesAt(wss []herdr.Listed, path string) []herdr.Listed {
	var shown []herdr.Listed

	for _, ws := range wss {
		if ws.Path != "" && samePath(ws.Path, path) {
			shown = append(shown, ws)
		}
	}

	return shown
}

// workspaceByID is the workspace with id, or nil once Herdr no longer shows it.
func workspaceByID(wss []herdr.Listed, id string) *herdr.Listed {
	for i := range wss {
		if wss[i].ID == id {
			return &wss[i]
		}
	}

	return nil
}

func workspaceLabel(ws *herdr.Listed) string {
	if ws == nil {
		return ""
	}

	return ws.Label
}

// samePath compares two paths with symlinks resolved where they exist.
func samePath(a, b string) bool {
	return resolved(a) == resolved(b)
}

// inside reports whether dir is path or lies below it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(resolved(path), resolved(dir))
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolved is the path with symlinks resolved. A component that does not
// exist - a removed or never-created path - keeps its name below the
// longest prefix that does, so two spellings of one place still compare.
func resolved(p string) string {
	p = filepath.Clean(p)

	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}

	parent := filepath.Dir(p)
	if parent == p {
		return p
	}

	return filepath.Join(resolved(parent), filepath.Base(p))
}

// landPull makes sure the branch's request is merged: merges it when open,
// confirms it when already merged, refuses otherwise. When the host still
// has the remote branch, it moves every open request based on it to the
// merged one's base, then deletes the branch and the local copy of it. The
// notes say which requests moved, when the merge was not this run's and
// when the merge method was a default rather than a declaration.
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

// retargetStacked moves every open request based on branch to base, a
// note each. A failure stops before the branch is deleted, so the request
// stays open and a rerun, reaching the merged case, retries it; its error
// names the requests already moved, since no report follows it.
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

// lastState leaves the PR body's hidden state block saying finish, the
// last state before the merge, and returns the body as the host now holds
// it. A failed read or write is a note, never a failed finish: the merge is
// the work, the block a trace of it. The one exception is a bundle whose
// body cannot be read: settleRiders just rewrote its closing keywords, and
// the fallback would close a rider the user kept.
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

// putLastState rewrites body's block for finish and writes it back when
// it changed, returning the body the host holds afterwards.
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

// stateNote is the report's line for a state block left as it was.
func stateNote(err error) string {
	if err == nil {
		return ""
	}

	return "the PR body's state block was not rewritten: " + err.Error()
}

// mergeBody is what the host's commit says under its subject: the request's
// description without its hidden state block, then one Task: line per
// member, so the commit that survives a squash still names what it shipped.
// Only with traces on and for a squash or a GitHub merge commit; a rebase,
// like glab's merge (""), lands the branch's own trailers.
func mergeBody(in finishInput, description, method string) string {
	if !in.traces || (method != "--squash" && method != "--merge") {
		return ""
	}

	trailers, err := trace.Render(trace.Trailers{Tasks: in.task.Members()})
	if err != nil {
		return ""
	}

	if body := strings.TrimSpace(trace.StripState(description)); body != "" {
		return body + "\n\n" + trailers
	}

	return trailers
}

// pullMain brings the local default branch up to the merge: a
// fast-forward pull when the main checkout is on it, a fetch into the
// branch when the checkout is parked elsewhere, which the note says.
// Either way the merge commit must then be in it, or nothing local is
// removed.
func pullMain(ctx context.Context, main, def, sha string) (string, error) {
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

	if !gitx.IsAncestor(ctx, main, sha, def) {
		return "", fmt.Errorf("merge commit %s is not in %s after pulling: nothing removed", sha, def)
	}

	return note, nil
}

// releaseDependents clears the dependency other branches recorded on the
// finished one with `start --after`, so their pr targets the default branch
// instead of a deleted one, and marks each landed: a dependent branched
// before the base's code existed, so its next ship or catchup brings the
// default branch in first.
func releaseDependents(ctx context.Context, dir, branch, def string) ([]string, error) {
	var notes []string

	for _, e := range gitx.ConfigEntries(ctx, dir, `^branch\..*\.`+cfgAfterBranch+`$`) {
		if e.Value != branch {
			continue
		}

		dependent := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+cfgAfterBranch)
		section := "branch." + dependent + "."
		base := gitx.Out(ctx, dir, "config", "--get", section+cfgAfter)

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

// archivePlan moves the task's plan into the shared plans dir's archive/,
// main's when that dir is "". A worktree keeping its own .f10 holds the plan
// and its prior versions, which move out with it. A missing plan is a note,
// since a task shipped without one still needs cleaning up; a plan that
// cannot be moved is an error, since removing the worktree would delete it.
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

	// a worktree's own: older versions it archived go first, so the current
	// plan ends up with the highest number; the shared archive is already
	// in place
	if samePath(src, own) && !samePath(worktree, main) {
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

// removeImages drops the images `task read` downloaded for the task into
// the shared root (main's .f10/ from a worktree keeping its own); a
// worktree's own go with the worktree. A failure is a note: the task is
// finished either way.
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

// taskPlansDir is where a task's plan sits: the shared plans dir, else
// (none given) the checkout's own .f10/plans.
func taskPlansDir(checkout, plansDir string) string {
	return cmp.Or(plansDir, filepath.Join(checkout, ".f10", "plans"))
}

// taskPlan is the task's plan file: in the shared plans dir, unless only
// the checkout's own .f10/plans holds it, as a worktree from before storage
// moved to main's does.
func taskPlan(checkout, plansDir, id string) string {
	plan := filepath.Join(taskPlansDir(checkout, plansDir), id+".md")
	if own := filepath.Join(checkout, ".f10", "plans", id+".md"); !exists(plan) && exists(own) {
		return own
	}

	return plan
}

// freeArchiveName is `<id>.md` under dir when free, else `<id>.<N>.md` with
// the next integer, the numbering steps/plan.md uses.
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

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
