// Package herdr is the binary's door to Herdr, the terminal multiplexer
// `f10 start` opens a task's worktree in, `f10 forward` reaches its agent
// through and `f10 finish` closes it from. All over the herdr CLI: open a
// worktree as a workspace, start an agent in its root pane, hand that agent
// a prompt, look an agent up, read what a pane runs, list the workspaces,
// focus one, close one. Every answer is JSON, and the ids the next call
// needs are read from it rather than predicted.
package herdr

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// The error codes this package acts on. Herdr answers agent_pane_busy while
// the pane runs anything but an idle shell, its rc files included.
const (
	CodePaneBusy      = "agent_pane_busy"
	CodeAgentNotFound = "agent_not_found"
)

// How long StartAgent waits for a pane whose shell is still starting, how
// often it asks, and the longest gap between two progress reports.
// Variables so tests need not wait.
var (
	StartDeadline = 5 * time.Second
	StartPoll     = 200 * time.Millisecond
	StartReport   = time.Second
)

// An Error is herdr's own refusal: the error envelope it printed on stderr.
type Error struct {
	Op      string // the command, as `herdr <verb> <sub>`
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Op + ": " + e.Message
	}

	return e.Op + ": " + e.Code + ": " + e.Message
}

// IsCode reports whether err is herdr's refusal with code.
func IsCode(err error, code string) bool {
	var he *Error

	return errors.As(err, &he) && he.Code == code
}

// ErrNotInside is the one refusal: start, forward, drive and finish run inside a
// Herdr session and nowhere else, by decision rather than by accident of a
// missing fallback.
var ErrNotInside = errors.New("f10 start, forward, drive and finish run inside a Herdr session: " +
	"open a pane there, or install it from https://herdr.dev")

// Available reports whether this process can reach Herdr: the session
// marks itself in the environment, and the CLI has to be on PATH.
func Available() error {
	if os.Getenv("HERDR_ENV") != "1" || !shell.Has("herdr") {
		return ErrNotInside
	}

	return nil
}

// A Workspace is what opening a worktree yields: the ids the agent calls
// are addressed by.
type Workspace struct {
	ID          string
	RootPane    string
	AlreadyOpen bool // Herdr was showing this checkout before the call
}

// OpenWorktree turns the checkout at path into a workspace labelled label,
// or returns the one already showing it.
func OpenWorktree(ctx context.Context, dir, path, label string) (Workspace, error) {
	res, err := call(ctx, dir, "worktree", "open", "--path", path, "--label", label)
	if err != nil {
		return Workspace{}, err
	}

	ws := Workspace{
		ID:          field(res, "workspace", "workspace_id"),
		RootPane:    field(res, "root_pane", "pane_id"),
		AlreadyOpen: res["already_open"] == true,
	}

	switch {
	case ws.ID == "":
		return Workspace{}, errors.New("herdr worktree open: no workspace.workspace_id in the response")
	case ws.RootPane == "":
		return Workspace{}, errors.New("herdr worktree open: no root_pane.pane_id in the response")
	}

	return ws, nil
}

// StartAgent launches an agent of kind in pane under name. A pane whose
// shell is still running its rc files answers busy, so that one refusal is
// retried until StartDeadline; past it, herdr's error stands. While it
// waits, waiting (when not nil) hears how long it has waited: on the first
// busy answer, then within StartReport of the last. Once started,
// waiting for the agent to be ready for input is herdr's wait, not ours.
func StartAgent(ctx context.Context, dir, name, kind, pane string, waiting func(waited time.Duration)) error {
	begun := time.Now()
	deadline := begun.Add(StartDeadline)

	var reported time.Time

	for {
		_, err := call(ctx, dir, "agent", "start", name, "--kind", kind, "--pane", pane)
		if err == nil || !IsCode(err, CodePaneBusy) || !time.Now().Before(deadline) {
			return err
		}

		// report a poll early: the sleep and the herdr call both overrun, and
		// only a herdr call slower than a poll can still push a gap past StartReport
		if waiting != nil && (reported.IsZero() || time.Since(reported)+2*StartPoll > StartReport) {
			reported = time.Now()
			waiting(reported.Sub(begun))
		}

		timer := time.NewTimer(StartPoll)

		select {
		case <-ctx.Done():
			timer.Stop()

			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}

// AgentExists reports whether a live agent answers to target: its name, or
// the id of the pane hosting it.
func AgentExists(ctx context.Context, dir, target string) (bool, error) {
	_, err := call(ctx, dir, "agent", "get", target)

	switch {
	case err == nil:
		return true, nil
	case IsCode(err, CodeAgentNotFound):
		return false, nil
	default:
		return false, err
	}
}

// PaneIdleShell reports whether pane sits at its shell's prompt: the shell
// owns the terminal's foreground process group. Anything the shell runs,
// an rc child or a program, owns a group of its own.
func PaneIdleShell(ctx context.Context, dir, pane string) (bool, error) {
	res, err := call(ctx, dir, "pane", "process-info", "--pane", pane)
	if err != nil {
		return false, err
	}

	info, _ := res["process_info"].(map[string]any)
	shellPID, _ := info["shell_pid"].(float64)
	group, _ := info["foreground_process_group_id"].(float64)

	return shellPID != 0 && shellPID == group, nil
}

// Prompt submits text to the named agent and returns as soon as it is
// submitted. Never --wait: the caller may itself be an agent in Herdr.
func Prompt(ctx context.Context, dir, name, text string) error {
	_, err := call(ctx, dir, "agent", "prompt", name, text)

	return err
}

// A Listed is one workspace as `workspace list` reports it. Path is the
// checkout the workspace shows, or "" for a workspace with no worktree.
type Listed struct {
	ID      string
	Label   string
	Path    string
	Focused bool
	Agent   string // herdr's agent_status: idle, working, done, unknown
}

// Workspaces lists every workspace the session shows.
func Workspaces(ctx context.Context, dir string) ([]Listed, error) {
	res, err := call(ctx, dir, "workspace", "list")
	if err != nil {
		return nil, err
	}

	raw, _ := res["workspaces"].([]any)

	list := make([]Listed, 0, len(raw))

	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		list = append(list, Listed{
			ID:      field(obj, "workspace_id"),
			Label:   field(obj, "label"),
			Path:    field(obj, "worktree", "checkout_path"),
			Focused: obj["focused"] == true,
			Agent:   field(obj, "agent_status"),
		})
	}

	return list, nil
}

// FocusWorkspace brings the workspace to the front.
func FocusWorkspace(ctx context.Context, dir, id string) error {
	_, err := call(ctx, dir, "workspace", "focus", id)

	return err
}

// CloseWorkspace closes the workspace, its tabs and panes, and whatever
// agent ran in them.
func CloseWorkspace(ctx context.Context, dir, id string) error {
	_, err := call(ctx, dir, "workspace", "close", id)

	return err
}

// call runs one herdr command and returns its `result` object. A failure
// is JSON on stderr; its code and message are what the user needs.
func call(ctx context.Context, dir string, args ...string) (map[string]any, error) {
	res, err := shell.Capture(ctx, dir, "herdr", args...)
	if err != nil {
		return nil, fmt.Errorf("running herdr %s: %w", args[0], err)
	}

	what := "herdr " + strings.Join(args[:min(2, len(args))], " ")

	if res.Code != 0 {
		if he := failure(what, res.Stderr); he != nil {
			return nil, he
		}

		return nil, fmt.Errorf("%s: %s", what, cmp.Or(res.Stderr, fmt.Sprintf("exit %d", res.Code)))
	}

	var env struct {
		Result map[string]any `json:"result"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		return nil, fmt.Errorf("parsing %s output: %w", what, err)
	}

	return env.Result, nil
}

// failure reads herdr's error envelope, or returns nil when stderr is not one.
func failure(op, stderr string) *Error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	// stderr that is not JSON is not herdr's envelope: the caller quotes it raw
	_ = json.Unmarshal([]byte(stderr), &env)

	if env.Error.Message == "" {
		return nil
	}

	return &Error{Op: op, Code: env.Error.Code, Message: env.Error.Message}
}

// field walks a nested object and returns the string at the path, or "".
func field(obj map[string]any, path ...string) string {
	var cur any = obj

	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}

		cur = m[key]
	}

	s, _ := cur.(string)

	return s
}
