// Package agentctl is what `f10 start`, `drive`, `finish` and `forward` ask
// of whatever hosts a task's agent: a workspace per checkout, an agent
// started and prompted in it, and whether that agent is working. Herdr is
// the first backend; a backend owns its own layout (panes, sessions), so
// none of it shows here.
package agentctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrAgentNotFound matches, under errors.Is, a backend's answer that no
// live agent goes by the name asked for.
var ErrAgentNotFound = errors.New("agent not found")

// A Status is a workspace's agent as the backend reports it. The constants
// are the values f10 acts on; a backend passes any other word through, so a
// reader such as the board shows what the backend said.
type Status string

const (
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusDone    Status = "done" // idle since a turn nobody has looked at yet
)

// A Workspace is one workspace as a listing reports it. Path is the
// checkout it shows, or "" for one with no checkout.
type Workspace struct {
	ID      string
	Label   string
	Path    string
	Focused bool
	Status  Status
}

// Opened is the workspace a checkout was opened or adopted in, and the
// slot its agent runs in: an id only the backend reads (Herdr: a pane).
type Opened struct {
	ID          string
	Slot        string
	AlreadyOpen bool // the backend was showing this checkout before the call
}

// An Occupancy is what holds a workspace's agent slot.
type Occupancy int

const (
	AgentPresent Occupancy = iota // an agent runs there, whatever its name
	Free                          // nothing runs there: an agent can start
	Busy                          // something other than an agent runs there
)

// A Backend hosts task agents. Every method answers from the directory the
// backend was made for.
type Backend interface {
	// Name names the backend in messages.
	Name() string
	// Available is nil when this process can reach the backend, else the
	// refusal to print.
	Available() error
	// Workspaces lists every workspace the backend shows.
	Workspaces(ctx context.Context) ([]Workspace, error)
	// Open opens a workspace labelled label on the checkout at path, or
	// adopts one already showing it; notes name what it left alone.
	Open(ctx context.Context, path, label string) (Opened, []string, error)
	// Occupancy says what holds ws's agent slot.
	Occupancy(ctx context.Context, ws Opened) (Occupancy, error)
	// StartAgent launches an agent of kind under name in ws's slot, with
	// args on its command line. While it waits for the slot, waiting (when
	// not nil) hears how long it has waited and the most it will.
	StartAgent(
		ctx context.Context,
		ws Opened,
		name, kind string,
		args []string,
		waiting func(waited, limit time.Duration),
	) error
	// Prompt submits text to the named agent and returns once submitted.
	Prompt(ctx context.Context, name, text string) error
	// Focus brings the workspace to the front.
	Focus(ctx context.Context, id string) error
	// Close closes the workspace and whatever agent ran in it.
	Close(ctx context.Context, id string) error
}

// AgentName names a task's agent. Herdr's live agent names share one
// namespace across every project, capped at 32 characters, so the bare
// name is cut to leave room for a short hash of the worktree path: the same
// id in two checkouts never collides.
func AgentName(id, suffix, path string) string {
	sum := sha256.Sum256([]byte(path))
	base := BareAgentName(id, suffix)

	return base[:min(len(base), 25)] + "-" + hex.EncodeToString(sum[:3])
}

// BareAgentName is the id lowercased, with the suffix so two attempts on
// one task can both run: the whole name of an agent started before names
// carried the path hash.
func BareAgentName(id, suffix string) string {
	name := strings.ToLower(id)
	if suffix != "" {
		name += "-" + strings.ToLower(suffix)
	}

	return name
}
