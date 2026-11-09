package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
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
	lease, err := finishLease(t.lay.StorageRoot.Path(), r.ID)
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
		sweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.lay.StorageRoot.Path(), janitorStamp), time.Now(),
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
	merge     bool         // merge the PR even when someone else opened it
	local     bool         // leave the PR open and clean up only what is here
	keep      bool         // a spike's experiment is committed onto a kept/ branch, not discarded
	storage   storage.Root // the storage root every checkout shares
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
		root:       t.lay.StorageRoot.Path(),
		driver:     t.driver(),
		task:       task,
		host:       h.at(main),
		method:     t.eff.MergeMethod,
		traces:     t.eff.Traces,
		hostIssues: t.hostIssues(),
		loreStore:  &lore.Store{Root: loreRootFor(t)},
		gate:       t.eff.LoreGate,
		storage:    t.lay.StorageRoot,
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
