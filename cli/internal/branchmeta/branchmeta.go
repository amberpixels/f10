// Package branchmeta is the record f10 keeps on a branch, as entries under
// `branch.<name>.` in the repository's local config: per branch, shared by
// every worktree, never pushed, and deleted with the branch.
package branchmeta

import (
	"context"
	"strings"
	"sync"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A Key is one entry's name under the branch's section. The names are on
// disk in live checkouts, so they never change.
type Key string

const (
	// Task links the branch to its task: a branch picked up from a PR need
	// not carry the id in its name, and a bundle's branch links its lead.
	Task Key = "f10-task"
	// Riders lists a bundle's riders, space-separated: the tasks one branch
	// ships beside its lead.
	Riders Key = "f10-tasks"

	// After and AfterBranch are the base task and its branch `f10 start
	// --after` records. Finish swaps them for Landed so the next step brings
	// the default branch in first.
	After       Key = "f10-after"
	AfterBranch Key = "f10-after-branch"
	Landed      Key = "f10-landed"
	// Moved is drive's note that the base's head moved past the branch, for
	// the next catchup, which clears it and Landed.
	Moved Key = "f10-base-moved"
	// Tip is the base commit the branch was last built on: a catchup rebases
	// onto from it once the base's history is rewritten.
	Tip Key = "f10-base-tip"

	// Agent is the whole profile (kind:model:effort) a start or drive gave
	// the branch's agent, read back on a rerun; absent for the kind's default.
	Agent Key = "f10-agent"
	// Finish is `f10 drive`'s record that it finishes the branch, and where
	// that finish stands. Whether the drive still runs is its task's lease,
	// never this: finish removes it with the branch, and a drive that stops
	// short sets it back to queued.
	Finish Key = "f10-finish"
	// Drive is the last skill drive saw the task finish: plan, ship or finish.
	Drive Key = "f10-drive"
	// Chain is the skills an older drive ran the task through, space-joined.
	// Drive no longer writes it, and removes one it finds.
	Chain Key = "f10-chain"
	// Flow and Pace are what a started task runs, for status and a rerun.
	Flow Key = "f10-flow"
	Pace Key = "f10-pace"
	// Watch is "false" once `f10 watch off` hands the task back.
	Watch Key = "f10-watch"
)

// keys is every Key, for splitting a listed entry's name.
var keys = []Key{Task, Riders, After, AfterBranch, Landed, Moved, Tip, Agent, Finish, Drive, Chain, Flow, Pace, Watch}

// For is the full config name of the key on branch, as git and its errors
// spell it.
func (k Key) For(branch string) string {
	return "branch." + branch + "." + string(k)
}

// An Entry is one listed key on one branch.
type Entry struct {
	Branch string
	Key    Key
	Value  string
}

// A Backend stores the records. Every read treats an absent key as "" with
// no error, as `git config --get` exits 1 for it.
type Backend interface {
	Get(ctx context.Context, dir, branch string, key Key) (string, error)
	Set(ctx context.Context, dir, branch string, key Key, value string) error
	// Unset fails on an absent key, as git does.
	Unset(ctx context.Context, dir, branch string, key Key) error
	// All lists every known key on every branch, in the store's order.
	All(ctx context.Context, dir string) ([]Entry, error)
}

var (
	mu      sync.RWMutex
	backend Backend = Git{}
)

// SetBackend swaps the store, for a test that answers records without git,
// and returns the function that puts the previous one back.
func SetBackend(b Backend) func() {
	mu.Lock()
	prev := backend
	backend = b
	mu.Unlock()

	return func() {
		mu.Lock()
		backend = prev
		mu.Unlock()
	}
}

func current() Backend {
	mu.RLock()
	defer mu.RUnlock()

	return backend
}

// Get is one key on branch; absent is "" with no error.
func Get(ctx context.Context, dir, branch string, key Key) (string, error) {
	return current().Get(ctx, dir, branch, key)
}

// Set writes one key on branch.
func Set(ctx context.Context, dir, branch string, key Key, value string) error {
	return current().Set(ctx, dir, branch, key, value)
}

// Unset removes one key on branch, failing when it is absent.
func Unset(ctx context.Context, dir, branch string, key Key) error {
	return current().Unset(ctx, dir, branch, key)
}

// UnsetIfSet removes the key only where it is set, so an absent one is no
// error.
func UnsetIfSet(ctx context.Context, dir, branch string, key Key) error {
	v, err := Get(ctx, dir, branch, key)
	if err != nil || v == "" {
		return err
	}

	return Unset(ctx, dir, branch, key)
}

// A Record is every key one branch holds, "" or nil where absent.
type Record struct {
	Branch      string
	Task        string
	Riders      []string
	After       string
	AfterBranch string
	Landed      string
	Moved       string
	Tip         string
	Agent       string
	Finish      *DriveFinish // nil when absent or malformed
	Drive       string
	Flow        string
	Pace        string
	Watch       string
}

func (r *Record) set(key Key, value string) {
	switch key {
	case Task:
		r.Task = value
	case Riders:
		r.Riders = strings.Fields(value)
	case After:
		r.After = value
	case AfterBranch:
		r.AfterBranch = value
	case Landed:
		r.Landed = value
	case Moved:
		r.Moved = value
	case Tip:
		r.Tip = value
	case Agent:
		r.Agent = value
	case Finish:
		if d, ok := ParseDriveFinish(value); ok {
			r.Finish = &d
		} else {
			r.Finish = nil
		}
	case Drive:
		r.Drive = value
	case Flow:
		r.Flow = value
	case Pace:
		r.Pace = value
	case Watch:
		r.Watch = value
	case Chain: // an older drive's, which only the drive's unset reads
	}
}

// Load is branch's record.
func Load(ctx context.Context, dir, branch string) (Record, error) {
	all, err := LoadAll(ctx, dir)
	if err != nil {
		return Record{Branch: branch}, err
	}

	for _, r := range all {
		if r.Branch == branch {
			return r, nil
		}
	}

	return Record{Branch: branch}, nil
}

// LoadAll is every branch's record in one read, ordered by each branch's
// first entry. It is a snapshot: a caller that writes and then looks up
// again loads again.
func LoadAll(ctx context.Context, dir string) ([]Record, error) {
	entries, err := current().All(ctx, dir)
	if err != nil {
		return nil, err
	}

	var records []Record

	at := map[string]int{}

	for _, e := range entries {
		i, ok := at[e.Branch]
		if !ok {
			i = len(records)
			at[e.Branch] = i

			records = append(records, Record{Branch: e.Branch})
		}

		records[i].set(e.Key, e.Value)
	}

	return records, nil
}

// Dependents are the branches whose AfterBranch is branch, in order.
func Dependents(records []Record, branch string) []string {
	var deps []string

	for _, r := range records {
		if r.AfterBranch == branch {
			deps = append(deps, r.Branch)
		}
	}

	return deps
}

// Git is the store in the repository's local config.
type Git struct{}

// allPattern lists every f10 key under every branch section.
const allPattern = `^branch\..*\.f10-`

func (Git) Get(ctx context.Context, dir, branch string, key Key) (string, error) {
	return gitx.Config(ctx, dir, key.For(branch))
}

func (Git) Set(ctx context.Context, dir, branch string, key Key, value string) error {
	return gitx.SetConfig(ctx, dir, key.For(branch), value)
}

func (Git) Unset(ctx context.Context, dir, branch string, key Key) error {
	return gitx.UnsetConfig(ctx, dir, key.For(branch))
}

func (Git) All(ctx context.Context, dir string) ([]Entry, error) {
	listed, err := gitx.ConfigEntries(ctx, dir, allPattern)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(listed))

	for _, e := range listed {
		if branch, key, ok := Split(e.Key); ok {
			entries = append(entries, Entry{Branch: branch, Key: key, Value: e.Value})
		}
	}

	return entries, nil
}

// Split reads a full config name back into its branch and key. A key this
// binary does not know is not ok, so a newer binary's record breaks no read.
func Split(name string) (string, Key, bool) {
	rest, ok := strings.CutPrefix(name, "branch.")
	if !ok {
		return "", "", false
	}

	for _, k := range keys {
		if branch, ok := strings.CutSuffix(rest, "."+string(k)); ok && branch != "" {
			return branch, k, true
		}
	}

	return "", "", false
}

// Out is Get with a failed read taken as no record, for the callers that
// treat an unreadable config the way gitx.Out does.
func Out(ctx context.Context, dir, branch string, key Key) string {
	v, err := Get(ctx, dir, branch, key)
	if err != nil {
		return ""
	}

	return v
}
