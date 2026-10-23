// Package lookaround collects facts about one project and derives where
// each of its tasks stands. Collectors (host, tracker, local) fetch and
// interpret nothing; Derive is one pure function, facts in, rows out, and
// the only place a stage is decided; the Board is the JSON every renderer
// reads, docs/board.md its spec and docs/board.schema.json its schema.
package lookaround

import "time"

// Version is the Board JSON's version; a change to a stage rule or a
// field's meaning bumps it with the spec.
const Version = 1

// A Board is one project's rows, with where each fact came from.
type Board struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Project     Project   `json:"project"`
	Window      Window    `json:"window"`
	Sources     []Source  `json:"sources"`
	Rows        []Row     `json:"rows"`
}

// Project names the project the board is about.
type Project struct {
	ID       string `json:"id"`                 // the normalised remote, `github.com/org/repo`
	Name     string `json:"name"`               // the main checkout's basename
	Host     string `json:"host,omitempty"`     // gh or glab
	Checkout string `json:"checkout,omitempty"` // the main checkout; empty with no checkout read
	Remote   bool   `json:"remote"`             // local collectors skipped
}

// Window is the period done rows cover.
type Window struct {
	Since time.Time `json:"since"`
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

// Where a stage's evidence came from: local state, the traces f10 left on
// the host, or the host's own facts alone.
const (
	FromLocal   = "local"
	FromTrace   = "trace"
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
}

// Who is working on the row, and who it waits on.
type Who struct {
	Agent       string   `json:"agent,omitempty"`        // the Herdr agent in its worktree
	AgentStatus string   `json:"agent_status,omitempty"` // idle, working, done
	Model       string   `json:"model,omitempty"`
	Author      string   `json:"author,omitempty"` // the request's author
	Assignees   []string `json:"assignees,omitempty"`
	Reviewers   []string `json:"reviewers,omitempty"` // requested and not yet heard from
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
