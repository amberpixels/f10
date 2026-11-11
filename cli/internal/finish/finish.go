// Package finish closes a task: it decides what landing means, lands the
// request, archives what the task leaves and cleans up its checkout, workspace
// and branch.
package finish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Ref is the task a token names, its suffix, and for a bare call the
// branch checked out here, which picks it among suffixed siblings. The
// session's task is refused: nothing else would stand between it and a merge.
func Ref(ctx context.Context, t *target.Target, token string) (ref.Ref, string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		r, err := t.Reference(ctx, "")
		if err != nil {
			return ref.Ref{}, "", "", err
		}

		if r.Origin == ref.OriginSession {
			return ref.Ref{}, "", "", fmt.Errorf("the branch names no task: name it (f10 finish %s)", r.Label())
		}

		return r, "", gitx.Out(ctx, t.Dir, "rev-parse", "--abbrev-ref", "HEAD"), nil
	}

	id, suffix := ref.SplitSuffix(token)
	r, err := t.Reference(ctx, id)

	return r, suffix, "", err
}

var ErrNotMerged = errors.New("its request has not merged on the host, and only merged work is finished here")

func Lease(root, id string) (*drives.Lease, error) {
	lease, err := drives.Acquire(root, id, 0)
	if held := (drives.HeldError{}); errors.As(err, &held) {
		if held.Holder.Drive > 0 {
			return nil, fmt.Errorf("%w: f10 drive cancel %d stops it, or let it finish", held, held.Holder.Drive)
		}

		return nil, fmt.Errorf("%w: wait for it to end", held)
	}

	return lease, err
}

// A ForeignPullError is a refusal: nothing changed, the choice is the user's.
type ForeignPullError struct {
	URL    string
	Author string
}

func (e ForeignPullError) Error() string {
	return fmt.Sprintf("%s was opened by %s: pass --merge to merge it, "+
		"or --local to clean up here and leave it open", e.URL, e.Author)
}

// FinishInput is everything finish needs once the target is settled, so
// the flow can be tested without a checkout.
type FinishInput struct {
	Main   string         // the main checkout: host and git commands run here
	Root   string         // the shared storage root, for the pull's lease; "" takes none
	Driver *driver.Driver // nil without one
	Task   ref.Ref
	Change string // the lore change this merge closes; finish fills it from the task's series
	Suffix string
	Branch string // a bare call's own branch, "" to look it up by task and suffix
	Host   hostops.Host
	Method string // the merge method project.md declares, "" for none
	Traces bool   // traces: on; the squash or merge commit's body ends in the task's trailers
	// HostCloses: a merge's closing keyword closes the task (target.HostCloses)
	HostCloses bool
	// LoreStore feeds the report's lore line and the pre-merge check (nil reads
	// nothing); gate is lore_gate: warn notes a failing check, block refuses
	LoreStore *lore.Store
	Gate      string
	Cwd       string // where the command runs: inside the worktree is the self-close case
	Workspace string // HERDR_WORKSPACE_ID: the workspace this process runs in, or ""
	Flags     Flags
	Storage   storage.Root // the same root as root, for task files: plans, findings, handoffs
	// Title reads the task's title for a squash merged here, nil for none
	Title func(context.Context) (string, error)
	// Close is the riders the PR delivers, closed with the lead; nil, no
	// --close answer yet, refuses a bundle's merge unless FollowBody
	Close []string
	// FollowBody takes a nil Close from the PR body's closing keywords, for
	// the drive and the watcher, which have no one to ask
	FollowBody bool
	// Sweep runs the janitor before the workspace closes, since a self-close
	// ends the process; nil for none
	Sweep func(context.Context)
	// Progress names each action as it starts, for drive's record; nil for none
	Progress func(string)
	// Handoff takes the lines a merge prints under its report, for drive's report; nil for none
	Handoff func([]string)
	// Deferred takes a deferred pull's note, for drive's report; nil for none
	Deferred func(string)
	// Subtasks is every subtask, closed ones kept, for a parent with no branch;
	// nil, like ErrNoParentLink, is a tracker with no parent link
	Subtasks func(context.Context, ref.Ref) ([]stack.ChildRow, error)
	// CloseTask closes a parent its subtasks delivered; nil where the tracker is
	// not the host, which f10 has no "done" word for
	CloseTask func(context.Context, ref.Ref) error
}

// Flags are the caller's choices; everything else finish derives.
type Flags struct {
	Yes       bool // close the workspace finish runs in, after the report
	Merge     bool // merge the PR even when someone else opened it
	LeaveOpen bool // --local: leave the PR open and clean up only what is here
	Keep      bool // a spike's experiment is committed onto a kept/ branch, not discarded
	// MergedOnly refuses all but an already merged request: the watcher never merges
	MergedOnly bool
}

// InputFor fills what comes from the task and the project's settings,
// so the command, the drive and the watcher all take the same ones; each
// caller then sets only its own fields.
func InputFor(t *target.Target, main string, h hostops.Host, task ref.Ref) FinishInput {
	task = task.Head()

	return FinishInput{
		Main:       main,
		Root:       t.Layout.StorageRoot.Path(),
		Driver:     t.Driver(),
		Task:       task,
		Host:       h.In(main),
		Method:     t.Effective.MergeMethod,
		Traces:     t.Effective.Traces,
		HostCloses: t.HostCloses(),
		LoreStore:  &lore.Store{Root: t.LoreRoot()},
		Gate:       t.Effective.LoreGate,
		Storage:    t.Layout.StorageRoot,
		Title:      worktree.TitleLookup(t, task.Number),
		Subtasks:   stack.TrackerFor(t).AllChildren,
		CloseTask:  CloseTaskFor(t, h.In(main)),
	}
}

// CloseTaskFor closes the task on the host only where the host is the tracker.
func CloseTaskFor(t *target.Target, h hostops.Host) func(context.Context, ref.Ref) error {
	if !t.HostCloses() {
		return nil
	}

	return func(ctx context.Context, task ref.Ref) error { return h.SetIssueOpen(ctx, task.Number, false) }
}

// ProcessPlace is this process's directory and workspace, for finish's
// self-close check.
func ProcessPlace() (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}

	return cwd, os.Getenv("HERDR_WORKSPACE_ID"), nil
}

// FinishedEvent records a finish that ran in this process, where no hook
// sees it; by names who ran it, "" for the drive.
func FinishedEvent(id, by string, at time.Time) ledger.Event {
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
