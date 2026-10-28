package lookaround

import (
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// Facts is what the collectors found, before any interpretation.
type Facts struct {
	Project Project
	Prefix  string // the task id prefix, for reading ids off branches and titles
	Tasks   []Task
	PRs     []PR
	Local   *Local // nil when the local collector was skipped or failed
	People  []Person
	Sources []Source

	// the feed's: what the host did in the window, each started task's
	// other requests by id, and a local-only checkout's branch tips
	Activity *forge.Activity
	Earlier  map[string][]forge.PR
	Tips     []Tip
}

// A Person joins the identities one person has across the sources: the
// tracker's name for them, their host login, the emails their commits
// carry. Every field but Name is optional.
type Person struct {
	Name    string
	Tracker string
	Host    string
	Emails  []string
}

// A PR is a request with what the host says around it.
type PR struct {
	forge.PR

	Block      *trace.State   `json:"block,omitempty"`
	Trailers   []trace.Parsed `json:"trailers,omitempty"`
	HeadExists *bool          `json:"head_exists,omitempty"` // read for merged requests only
	RemoteOpen bool           `json:"remote_open,omitempty"` // the declared remote reviewer left one unhandled
}

// Local is what this machine knows: live runs, worktrees, branches,
// plans, findings, verdicts, what the ledger closed, and Herdr's view.
type Local struct {
	Runs       []state.Run
	Worktrees  []Worktree
	Branches   map[string]Branch    // by branch name
	Plans      map[string]time.Time // task id to the plan file's mtime
	Archived   map[string]bool      // task ids whose plan is archived
	Findings   map[string]Findings  // task id to its latest round
	Verdicts   map[string]string    // task id to its latest verdict
	Finished   map[string]bool      // task ids the ledger holds task_finished for
	Workspaces map[string]Workspace // worktree path to the Herdr workspace on it
}

// A Tip is a local branch's last commit, what a checkout with no host
// shows in its feed.
type Tip struct {
	Branch string
	At     time.Time
}

// A Worktree is one checkout of the repo.
type Worktree struct {
	Path   string
	Branch string
	Ahead  int      // commits past its base
	Emails []string // the distinct author emails on those commits
}

// A Branch is what the local config records on a branch.
type Branch struct {
	Task   string   // the id the branch is linked to, else what its name spells
	Riders []string // a bundle's other members
	After  string   // the base task recorded on it
	Model  string   // the agent's model, where start recorded it
	// Finish is `f10 drive`'s record of running the branch's finish, while
	// that drive holds the task's lease
	Finish *ref.DriveFinish
}

// Findings is a task's latest review round as its findings file holds it.
type Findings struct {
	Round int
	Open  int // findings with no verdict, or a verdict of ask
	At    time.Time
}

// A Workspace is a Herdr workspace open on a worktree.
type Workspace struct {
	ID          string
	Label       string
	AgentStatus string
}
