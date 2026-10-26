// Package lookaround collects facts about one project and derives what
// changed in a window (the feed) or where each of its tasks stands (the
// state). Collectors (host, tracker, local) fetch and interpret nothing;
// DeriveFeed and Derive are pure functions, facts in, rows out, and the
// only places a group or a stage is decided; the Board is the JSON every
// renderer reads, docs/board.md its spec and docs/board.schema.json its
// schema.
package lookaround

import "time"

// Version is the Board JSON's version; a change to a stage rule or a
// field's meaning bumps it with the spec.
const Version = 2

// The two views a board is: what changed in the window, and where every
// task stands.
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
	StagePlanned          = "planned"
	StageMergedUnfinished = "merged-unfinished"
	StageNext             = "next"
	StageDone             = "done"
)

// Stages is every stage in listing order.
var Stages = []string{
	StageAsksWaiting, StageBlocked, StageReady, StageInReview, StagePROpen,
	StageImplementing, StagePlanned, StageMergedUnfinished, StageNext, StageDone,
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
	// Person is the one name the row groups under: the tracker assignee's,
	// else the author's, else the first committer's, each through the
	// people map where one matches, else as the source spelled it.
	Person string `json:"person,omitempty"`
}

// Where the work is.
type Where struct {
	PR        *PRRef `json:"pr,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Workspace string `json:"workspace,omitempty"`
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

// A Feed is what changed in the window: one row per request touched or
// task created there, and the open requests nobody touched, counted.
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

// A FeedRow is one request touched in the window, or one task created
// there.
type FeedRow struct {
	Group string `json:"group"`
	Key   string `json:"key"` // the task id, else `#<number>`, else the branch
	Task  *Task  `json:"task,omitempty"`
	PR    *PRRef `json:"pr,omitempty"`
	From  string `json:"from"`
	Who   Who    `json:"who"`
	// Owes is who the next move waits on: the author after a review, a
	// requested reviewer before one.
	Owes       string    `json:"owes,omitempty"`
	Abandoned  bool      `json:"abandoned,omitempty"`    // closed without merging
	FollowUpOf []int     `json:"follow_up_of,omitempty"` // the task's earlier requests
	Summary    string    `json:"summary,omitempty"`      // what happened, one sentence
	At         time.Time `json:"at"`                     // the latest event in the window
}
