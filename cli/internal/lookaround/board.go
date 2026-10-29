// Package lookaround collects facts about one project and derives the feed
// (what changed in a window) or the state (where each task stands). Only
// Derive and DeriveFeed decide a stage or a group; docs/board.md is the spec.
package lookaround

import (
	"slices"
	"time"
)

// Version is the Board JSON's version; a change to a stage rule or a
// field's meaning bumps it with the spec.
const Version = 4

// The two views a board is.
const (
	ViewFeed  = "feed"
	ViewState = "state"
)

// A Board is one project's feed or rows, with where each fact came from.
type Board struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	View        string    `json:"view"`
	Project     Project   `json:"project"`
	Window      Window    `json:"window"`
	Sources     []Source  `json:"sources"`
	Rows        []Row     `json:"rows"`           // the state view's; empty in the feed
	Feed        *Feed     `json:"feed,omitempty"` // the feed view's
}

// Project names the project the board is about.
type Project struct {
	ID       string `json:"id"`                 // the normalised remote, `github.com/org/repo`
	Name     string `json:"name"`               // the main checkout's basename
	Host     string `json:"host,omitempty"`     // gh or glab
	Checkout string `json:"checkout,omitempty"` // the main checkout; empty with no checkout read
	Remote   bool   `json:"remote"`             // local collectors skipped
	// LocalOnly is a checkout with no origin remote: no host to read, so
	// the board is this machine's alone.
	LocalOnly bool `json:"local_only,omitempty"`
}

// Window is the period the board covers: the feed's span, or the time
// since which the state view's done rows count.
type Window struct {
	Since time.Time `json:"since"`
	// End closes the feed's span, exclusive; zero in the state view.
	End time.Time `json:"end,omitzero"`
	// Chosen is how the feed's span was picked: the window named
	// (`today`, `last-week`, a date) or the default's pick.
	Chosen string `json:"chosen,omitempty"`
	// Fallback is the default falling back to the last working day
	// because today has no activity yet.
	Fallback bool `json:"fallback,omitempty"`
}

// The three sources, in the order the board lists them.
const (
	SourceHost    = "host"
	SourceTracker = "tracker"
	SourceLocal   = "local"
)

// A Source is one collector's result: ok with its facts in, failed with
// its error and nothing from it, or skipped on purpose.
type Source struct {
	Name    string   `json:"name"`
	OK      bool     `json:"ok"`
	Skipped bool     `json:"skipped,omitempty"`
	Error   string   `json:"error,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	TookMS  int64    `json:"took_ms"`
}

// The stages, in the order the board lists rows.
const (
	StageAsksWaiting      = "asks-waiting"
	StageBlocked          = "blocked"
	StageReady            = "ready"
	StageInReview         = "in-review"
	StagePROpen           = "pr-open"
	StageImplementing     = "implementing"
	StagePlanning         = "planning"
	StagePlanned          = "planned"
	StageMergedUnfinished = "merged-unfinished"
	StageNext             = "next"
	StageDone             = "done"
)

// Stages is every stage in listing order.
var Stages = []string{
	StageAsksWaiting, StageBlocked, StageReady, StageInReview, StagePROpen,
	StageImplementing, StagePlanning, StagePlanned, StageMergedUnfinished, StageNext, StageDone,
}

// The groups the state's rows draw under, in listing order.
const (
	GroupInProgress = "in progress"
	GroupNext       = "next"
	GroupDone       = "done"
)

// StateGroups is every state group in listing order.
var StateGroups = []string{GroupInProgress, GroupNext, GroupDone}

// StateGroup is the heading a state row draws under: a row held only by
// its base, with no work of its own, is still next. board.html's group()
// mirrors it.
func StateGroup(r Row) string {
	switch {
	case r.Stage == StageNext:
		return GroupNext
	case r.Stage == StageDone:
		return GroupDone
	case r.Stage == StageBlocked && len(r.BlockedBy) > 0 &&
		r.Where.Worktree == "" && r.Where.Branch == "" && r.Where.PR == nil:
		return GroupNext
	default:
		return GroupInProgress
	}
}

// StateRank orders state rows: by group, then by stage, except that a row
// waiting on its base follows the rows that can start now.
func StateRank(r Row) int {
	stage := slices.Index(Stages, r.Stage)
	if StateGroup(r) == GroupNext {
		stage = len(Stages) - stage
	}

	return slices.Index(StateGroups, StateGroup(r))*(len(Stages)+1) + stage
}

// Where a stage's or a group's evidence came from: local state, the
// traces f10 left on the host, the host's certain facts (a merge, a
// creation), the tracker's list, or the host's facts read as a guess.
const (
	FromLocal   = "local"
	FromTrace   = "trace"
	FromHost    = "host"
	FromTracker = "tracker"
	FromGuessed = "guessed"
)

// A Row is one task, or one request naming no task.
type Row struct {
	Key          string       `json:"key"` // the task id, else `#<number>`
	Task         *Task        `json:"task,omitempty"`
	Stage        string       `json:"stage"`
	StageFrom    string       `json:"stage_from"`
	Who          Who          `json:"who"`
	Where        Where        `json:"where"`
	Review       *ReviewFacts `json:"review,omitempty"`
	BlockedBy    []string     `json:"blocked_by,omitempty"`
	LastActivity time.Time    `json:"last_activity,omitzero"`
	Next         Next         `json:"next"`
}

// A Task is a tracker row.
type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	After    string `json:"after,omitempty"` // the base its body names
	// Created is when the tracker created it, where the tracker says.
	Created time.Time `json:"created,omitzero"`
}

// Who is working on the row, and who it waits on.
type Who struct {
	Agent       string   `json:"agent,omitempty"`        // the Herdr agent in its worktree
	AgentStatus string   `json:"agent_status,omitempty"` // idle, working, done
	Model       string   `json:"model,omitempty"`
	Author      string   `json:"author,omitempty"` // the request's author
	Assignees   []string `json:"assignees,omitempty"`
	Reviewers   []string `json:"reviewers,omitempty"`  // requested and not yet heard from
	Committers  []string `json:"committers,omitempty"` // the author emails on the row's commits
	// Person is the one name the row groups under: the assignee, else the
	// author, else the first committer, through the people map where it matches.
	Person string `json:"person,omitempty"`
}

// Where the work is.
type Where struct {
	PR        *PRRef `json:"pr,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	// BranchLeft marks a merged row whose branch outlived its worktree
	BranchLeft bool `json:"branch_left,omitempty"`
}

// A PRRef is the request a row hangs on.
type PRRef struct {
	Number    int    `json:"number"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	State     string `json:"state"` // open, merged or closed
	Draft     bool   `json:"draft,omitempty"`
	Head      string `json:"head,omitempty"`
	Base      string `json:"base,omitempty"`
	Mergeable string `json:"mergeable,omitempty"` // clean, conflicts or blocked
	Checks    string `json:"checks,omitempty"`    // green, red or pending
}

// ReviewFacts is what the row's reviews add up to.
type ReviewFacts struct {
	Step             string `json:"step,omitempty"` // the step the state block names
	Round            int    `json:"round,omitempty"`
	Open             int    `json:"open,omitempty"`
	Asks             int    `json:"asks,omitempty"`
	Approvals        int    `json:"approvals,omitempty"`
	ChangesRequested int    `json:"changes_requested,omitempty"`
	RemoteOpen       bool   `json:"remote_open,omitempty"` // the declared remote reviewer left one unhandled
}

// Next is the row's next action: a command where one exists, else a
// sentence.
type Next struct {
	Command string `json:"command,omitempty"`
	Text    string `json:"text,omitempty"`
}

// The feed's groups, in the order it lists them.
const (
	GroupStarted  = "started"
	GroupUpdated  = "updated"
	GroupReviewed = "reviewed"
	GroupFinished = "finished"
	GroupNewTasks = "new-tasks"
)

// Groups is every feed group in listing order.
var Groups = []string{GroupStarted, GroupUpdated, GroupReviewed, GroupFinished, GroupNewTasks}

// A Feed is what changed in the window: one row per task whose requests
// were touched there, per touched request naming no task, and per task
// created there, and the open requests nobody touched, counted.
type Feed struct {
	Rows  []FeedRow `json:"rows"`
	Quiet Quiet     `json:"quiet"`
}

// Quiet is the open requests with no row in the window: no commits,
// reviews or status changes there.
type Quiet struct {
	Count  int       `json:"count"`
	Oldest time.Time `json:"oldest,omitzero"` // the oldest one's creation time
}

// A FeedRow is one task whose requests were touched in the window, one
// request naming no task, or one task created there.
type FeedRow struct {
	Group string `json:"group"`
	Key   string `json:"key"` // the task id, else `#<number>`, else the branch
	Task  *Task  `json:"task,omitempty"`
	PR    *PRRef `json:"pr,omitempty"`
	From  string `json:"from"`
	Who   Who    `json:"who"`
	// Owes is who the next move waits on: the author after a review, a
	// requested reviewer before one.
	Owes      string      `json:"owes,omitempty"`
	Abandoned bool        `json:"abandoned,omitempty"` // closed without merging, nothing later
	Related   []RelatedPR `json:"related,omitempty"`   // the task's requests opened before PR
	Summary   string      `json:"summary,omitempty"`   // what happened, one sentence
	At        time.Time   `json:"at"`                  // the latest event in the window
}

// How a task's earlier request relates to the one its feed row leads with.
const (
	RelationPart     = "part"      // the two overlapped
	RelationFollowUp = "follow-up" // it merged before the later one opened
	RelationReplaced = "replaced"  // it closed unmerged
)

// A RelatedPR is one of a task's earlier requests, beside the row's own.
type RelatedPR struct {
	Number   int    `json:"number"`
	URL      string `json:"url,omitempty"`
	State    string `json:"state"` // open, merged or closed
	Relation string `json:"relation"`
}
